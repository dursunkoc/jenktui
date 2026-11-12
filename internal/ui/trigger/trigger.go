// Package trigger implements the Trigger flow: load parameter definitions,
// collect Common and per-job parameters, confirm, then start the builds.
package trigger

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/paramsmerge"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/params"
)

// Started is the outcome of triggering one job.
type Started struct {
	Job string
	Ref jenkins.QueueRef
	Err error
}

// Client is the Jenkins API the flow needs.
type Client interface {
	JobParameters(ctx context.Context, fullName string) ([]jenkins.ParamDef, error)
	Trigger(ctx context.Context, fullName string, params map[string]string) (jenkins.QueueRef, error)
}

// Deps are the screen's dependencies.
type Deps struct {
	Client Client
	// Names says how jobs are shown ("project › branch" for a Multibranch
	// branch). A job not in Names is shown relative to Parent.
	Names  map[string]core.JobName
	Parent string
	// Next builds the screen shown after triggering (Replace). When nil a
	// simple result list is shown.
	Next func(started []Started) core.Screen
}

type stage int

const (
	stLoading stage = iota
	stFailed
	stConfirm // one job, no parameters
	stForm    // one job, parameter form
	stCommon  // many jobs, Common Parameters
	stJob     // many jobs, per-job form
	stSummary
	stTriggering
	stResult
)

const timeout = 30 * time.Second

type loadRes struct {
	job  string
	defs []jenkins.ParamDef
	err  error
}
type loadedMsg []loadRes
type doneMsg []Started

type screen struct {
	deps  Deps
	th    core.Theme
	keys  []string // requested jobs
	jobs  []string // jobs whose parameters loaded
	defs  map[string][]jenkins.ParamDef
	notes []string // excluded-job notices

	st   stage
	spin spinner.Model

	plan       paramsmerge.Plan
	form       *params.Form // single-job form
	commonForm *params.Form
	commonVals map[string]string
	jobSeq     []string // jobs with a per-job step
	pos        int
	jobForms   map[string]*params.Form
	baseline   map[string]map[string]string // job -> common name -> prefilled value
	overrides  map[string]map[string]string

	final   map[string]map[string]string
	summary []string
	scroll  int

	started []Started
	w, h    int
}

// New returns the Trigger screen for the given job full names.
func New(deps Deps, jobKeys []string) core.Screen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return &screen{deps: deps, th: core.NewTheme(), keys: append([]string(nil), jobKeys...), spin: sp,
		defs: map[string][]jenkins.ParamDef{}, jobForms: map[string]*params.Form{}, w: 80, h: 20}
}

func (s *screen) Title() string { return "Trigger" }

// name is how job is shown: from Deps.Names, otherwise relative to the
// Parent Job. Plain text, so it can sit inside a styled title.
func (s *screen) name(job string) string {
	if n, ok := s.deps.Names[job]; ok {
		return n.String()
	}
	return core.RelName(job, s.deps.Parent)
}

func (s *screen) Init() tea.Cmd {
	keys, c := s.keys, s.deps.Client
	load := func() tea.Msg {
		res := make([]loadRes, len(keys))
		var wg sync.WaitGroup
		for i, k := range keys {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				d, err := c.JobParameters(ctx, k)
				res[i] = loadRes{k, d, err}
			}()
		}
		wg.Wait()
		return loadedMsg(res)
	}
	return tea.Batch(s.spin.Tick, load)
}

func (s *screen) Keys() []key.Binding {
	confirm := key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "trigger"))
	no := key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "cancel"))
	next := key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab/shift+tab", "move"))
	switch s.st {
	case stConfirm:
		return []key.Binding{confirm, no}
	case stForm, stCommon, stJob:
		return []key.Binding{next, key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next/submit")), core.KeyBack}
	case stSummary:
		return []key.Binding{confirm, no, core.KeyUp, core.KeyDown, core.KeyBack}
	case stTriggering, stLoading:
		return nil
	}
	return []key.Binding{core.KeyBack}
}

func (s *screen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		s.resizeForms()
		return s, nil
	case spinner.TickMsg:
		var c tea.Cmd
		s.spin, c = s.spin.Update(m)
		return s, c
	case loadedMsg:
		return s.loaded(m)
	case doneMsg:
		s.started = m
		if s.deps.Next != nil {
			return s, core.Cmd(core.Replace{Screen: s.deps.Next(s.started)})
		}
		s.st = stResult
		return s, nil
	case tea.KeyMsg:
		return s.key(m)
	}
	return s, s.forward(msg)
}

func (s *screen) forward(msg tea.Msg) tea.Cmd {
	if f := s.activeForm(); f != nil {
		return f.Update(msg)
	}
	return nil
}

func (s *screen) activeForm() *params.Form {
	switch s.st {
	case stForm:
		return s.form
	case stCommon:
		return s.commonForm
	case stJob:
		return s.jobForms[s.jobSeq[s.pos]]
	}
	return nil
}

func (s *screen) resizeForms() {
	hh := max(s.h-len(s.notes)-2, 5) // notes, blank top row, slack
	for _, f := range s.allForms() {
		f.SetSize(s.w, hh)
	}
}

func (s *screen) allForms() []*params.Form {
	var fs []*params.Form
	if s.form != nil {
		fs = append(fs, s.form)
	}
	if s.commonForm != nil {
		fs = append(fs, s.commonForm)
	}
	for _, f := range s.jobForms {
		fs = append(fs, f)
	}
	return fs
}

func (s *screen) loaded(res loadedMsg) (core.Screen, tea.Cmd) {
	for _, r := range res {
		if r.err != nil {
			s.notes = append(s.notes, fmt.Sprintf("%s excluded: %v", s.name(r.job), r.err))
			continue
		}
		s.jobs = append(s.jobs, r.job)
		s.defs[r.job] = r.defs
	}
	if len(s.jobs) == 0 {
		s.st = stFailed
		return s, nil
	}
	if len(s.jobs) == 1 {
		d := s.defs[s.jobs[0]]
		if len(d) == 0 {
			s.st = stConfirm
			return s, nil
		}
		s.form = params.NewForm(s.name(s.jobs[0]), d, params.Options{})
		s.st = stForm
		s.resizeForms()
		return s, s.form.Init()
	}
	s.plan = paramsmerge.Build(s.jobs, s.defs)
	for _, j := range s.jobs {
		// A job gets its own step when it has parameters of its own or Common
		// Parameters it can override.
		if len(s.plan.PerJob[j]) > 0 || len(s.plan.Common) > 0 {
			s.jobSeq = append(s.jobSeq, j)
		}
	}
	if len(s.plan.Common) > 0 {
		return s, s.enterCommon()
	}
	s.commonVals = map[string]string{}
	return s, s.afterCommon()
}

// commonDefs converts common parameters into form definitions. Common
// parameters whose defaults differ start unset: bool and choice get an empty
// choice shown as "(differs)".
func (s *screen) commonDefs() ([]jenkins.ParamDef, map[string]string) {
	ph := map[string]string{}
	var defs []jenkins.ParamDef
	for _, c := range s.plan.Common {
		d := jenkins.ParamDef{Name: c.Name, Type: c.Type, Description: c.Description, Default: c.Default, Choices: c.Choices}
		if c.DefaultsDiffer {
			ph[c.Name] = "(differs)"
			switch c.Type {
			case "bool":
				d.Type, d.Choices = "choice", []string{"", "true", "false"}
			case "choice":
				d.Choices = append([]string{""}, c.Choices...)
			}
		}
		defs = append(defs, d)
	}
	return defs, ph
}

// enterCommon shows the Common Parameters form; the command starts its cursor.
func (s *screen) enterCommon() tea.Cmd {
	if s.commonForm == nil {
		defs, ph := s.commonDefs()
		s.commonForm = params.NewForm(fmt.Sprintf("Common Parameters (applied to all %d jobs)", len(s.jobs)), defs,
			params.Options{Placeholder: ph, Hint: "Entered once. Parameters marked (differs) keep each job's own default when left unset."})
		s.resizeForms()
	}
	s.commonForm.Reopen()
	s.st = stCommon
	return s.commonForm.Init()
}

// effectiveCommon is the common value a job would receive without overrides.
func (s *screen) effectiveCommon(job string) map[string]string {
	r := paramsmerge.Resolve(s.plan, s.commonVals, nil)[job]
	out := map[string]string{}
	for _, c := range s.plan.Common {
		out[c.Name] = r[c.Name]
	}
	return out
}

func (s *screen) buildJobForm(job string) *params.Form {
	var defs []jenkins.ParamDef
	per := map[string]bool{}
	for _, p := range s.plan.PerJob[job] {
		per[p.Name] = true
	}
	eff := s.effectiveCommon(job)
	if s.baseline == nil {
		s.baseline = map[string]map[string]string{}
	}
	s.baseline[job] = eff
	pre := map[string]string{}
	for _, d := range s.defs[job] { // per-job first, then common (overridable)
		if per[d.Name] {
			defs = append(defs, d)
		}
	}
	for _, d := range s.defs[job] {
		if !per[d.Name] {
			if _, ok := eff[d.Name]; !ok {
				continue
			}
			if d.Description != "" {
				d.Description += " "
			}
			d.Description += "[common: change to override for this job]"
			defs = append(defs, d)
			pre[d.Name] = eff[d.Name]
		}
	}
	idx := slicesIndex(s.jobSeq, job) + 1
	f := params.NewForm(fmt.Sprintf("%s (%d/%d)", s.name(job), idx, len(s.jobSeq)), defs, params.Options{Prefill: pre})
	f.SetSize(s.w, max(s.h-len(s.notes)-2, 5))
	return f
}

func slicesIndex(a []string, v string) int {
	for i, x := range a {
		if x == v {
			return i
		}
	}
	return -1
}

func (s *screen) afterCommon() tea.Cmd {
	if len(s.jobSeq) == 0 {
		s.enterSummary()
		return nil
	}
	return s.enterJob(0)
}

func (s *screen) enterJob(i int) tea.Cmd {
	s.pos = i
	j := s.jobSeq[i]
	f := s.jobForms[j]
	if f == nil {
		f = s.buildJobForm(j)
		s.jobForms[j] = f
	}
	f.Reopen()
	s.st = stJob
	return f.Init()
}

func (s *screen) collectOverrides(job string) {
	if s.overrides == nil {
		s.overrides = map[string]map[string]string{}
	}
	ov := map[string]string{}
	per := map[string]bool{}
	for _, p := range s.plan.PerJob[job] {
		per[p.Name] = true
	}
	for n, v := range s.jobForms[job].Values() {
		if per[n] || v != s.baseline[job][n] {
			ov[n] = v
		}
	}
	s.overrides[job] = ov
}

func (s *screen) enterSummary() {
	s.scroll = 0
	if len(s.jobs) == 1 {
		s.final = map[string]map[string]string{s.jobs[0]: s.form.Values()}
	} else {
		s.final = paramsmerge.Resolve(s.plan, s.commonVals, s.overrides)
	}
	s.summary = nil
	for _, j := range s.jobs {
		s.summary = append(s.summary, s.th.Title.Render(s.name(j)))
		vals := s.final[j]
		if len(vals) == 0 {
			s.summary = append(s.summary, s.th.MutedTxt.Render("  (no parameters)"))
			continue
		}
		types := map[string]string{}
		for _, d := range s.defs[j] {
			types[d.Name] = d.Type
		}
		names := make([]string, 0, len(vals))
		for n := range vals {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			v := params.Masked(types[n], vals[n])
			if v == "" {
				v = s.th.MutedTxt.Render("(empty)")
			}
			s.summary = append(s.summary, fmt.Sprintf("  %s = %s", n, strings.ReplaceAll(v, "\n", "⏎")))
		}
	}
	s.st = stSummary
}

func (s *screen) back() (core.Screen, tea.Cmd) {
	pop := core.Cmd(core.Pop{})
	switch s.st {
	case stJob:
		s.collectOverrides(s.jobSeq[s.pos])
		if s.pos > 0 {
			return s, s.enterJob(s.pos - 1)
		}
		if s.commonForm != nil {
			return s, s.enterCommon()
		}
	case stSummary:
		switch {
		case len(s.jobs) == 1:
			s.form.Reopen()
			s.st = stForm
			return s, s.form.Init()
		case len(s.jobSeq) > 0:
			return s, s.enterJob(len(s.jobSeq) - 1)
		case s.commonForm != nil:
			return s, s.enterCommon()
		}
	}
	return s, pop
}

func (s *screen) key(k tea.KeyMsg) (core.Screen, tea.Cmd) {
	switch s.st {
	case stLoading, stTriggering:
		return s, nil
	case stFailed, stResult:
		switch k.String() {
		case "esc", "q", "enter":
			return s, core.Cmd(core.Pop{})
		}
		return s, nil
	case stConfirm:
		switch k.String() {
		case "y", "Y":
			return s.fire()
		case "n", "N", "esc":
			return s, core.Cmd(core.Pop{})
		}
		return s, nil
	case stSummary:
		switch k.String() {
		case "y", "Y", "enter":
			return s.fire()
		case "n", "N", "esc":
			return s.back()
		case "up", "k":
			s.scroll = max(s.scroll-1, 0)
		case "down", "j":
			s.scroll = min(s.scroll+1, max(len(s.summary)-s.summaryRows(), 0))
		}
		return s, nil
	}
	// form stages
	if k.String() == "esc" {
		if s.st == stForm || (s.st == stCommon) {
			return s, core.Cmd(core.Pop{})
		}
		return s.back()
	}
	f := s.activeForm()
	cmd := f.Update(k)
	if !f.Done() {
		return s, cmd
	}
	switch s.st {
	case stForm:
		s.enterSummary()
	case stCommon:
		vals := f.Values()
		if !reflect.DeepEqual(vals, s.commonVals) {
			s.jobForms, s.overrides, s.baseline = map[string]*params.Form{}, nil, nil
		}
		s.commonVals = vals
		return s, s.afterCommon()
	case stJob:
		s.collectOverrides(s.jobSeq[s.pos])
		if s.pos+1 < len(s.jobSeq) {
			return s, s.enterJob(s.pos + 1)
		}
		s.enterSummary()
	}
	return s, nil
}

func (s *screen) summaryRows() int { return max(s.h-len(s.notes)-5, 3) }

// fire triggers every job concurrently, collecting per-job errors.
func (s *screen) fire() (core.Screen, tea.Cmd) {
	s.st = stTriggering
	jobs, c := s.jobs, s.deps.Client
	final := s.final
	var secrets []string
	for _, j := range jobs {
		for _, d := range s.defs[j] {
			if d.Type == "password" && final[j][d.Name] != "" {
				secrets = append(secrets, final[j][d.Name])
			}
		}
	}
	run := func() tea.Msg {
		out := make([]Started, len(jobs))
		var wg sync.WaitGroup
		for i, j := range jobs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				ref, err := c.Trigger(ctx, j, final[j])
				out[i] = Started{Job: j, Ref: ref, Err: redact(err, secrets)}
			}()
		}
		wg.Wait()
		return doneMsg(out)
	}
	return s, tea.Batch(s.spin.Tick, run)
}

// redact makes sure no password value can leak through an error message.
func redact(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	changed := false
	for _, sec := range secrets {
		if strings.Contains(msg, sec) {
			msg, changed = strings.ReplaceAll(msg, sec, "••••"), true
		}
	}
	if changed {
		return errors.New(msg)
	}
	return err
}

func (s *screen) banner() string {
	var b strings.Builder
	for _, n := range s.notes {
		b.WriteString(s.th.MutedTxt.Render("! "+n) + "\n")
	}
	return b.String()
}

// View draws the current step one blank line under the title.
func (s *screen) View() string { return "\n" + s.view() }

func (s *screen) view() string {
	switch s.st {
	case stLoading:
		return s.spin.View() + fmt.Sprintf(" Loading parameters for %d job(s)...", len(s.keys))
	case stTriggering:
		return s.spin.View() + fmt.Sprintf(" Triggering %d job(s)...", len(s.jobs))
	case stFailed:
		return s.th.ErrorView("Cannot trigger", errors.New(strings.Join(s.notes, "\n")), "esc to go back")
	case stConfirm:
		return s.banner() + s.th.NoticeView("Trigger "+s.name(s.jobs[0])+"?", "y trigger, n cancel (no parameters)")
	case stForm, stCommon, stJob:
		return s.banner() + s.activeForm().View()
	case stSummary:
		rows := s.summaryRows()
		end := min(s.scroll+rows, len(s.summary))
		head := s.th.Title.Render(fmt.Sprintf("Ready to trigger %d job(s)", len(s.jobs)))
		foot := s.th.MutedTxt.Render("y/enter trigger, n/esc back")
		if len(s.summary) > rows {
			foot += s.th.MutedTxt.Render(fmt.Sprintf("  (%d-%d of %d lines, up/down scroll)", s.scroll+1, end, len(s.summary)))
		}
		return s.banner() + head + "\n\n" + strings.Join(s.summary[s.scroll:end], "\n") + "\n\n" + foot
	case stResult:
		var b strings.Builder
		b.WriteString(s.banner() + s.th.Title.Render("Trigger results") + "\n\n")
		for _, r := range s.started {
			if r.Err != nil {
				b.WriteString(fmt.Sprintf("  FAILED  %s: %v\n", s.name(r.Job), r.Err))
			} else {
				b.WriteString(fmt.Sprintf("  queued  %s (queue item %d)\n", s.name(r.Job), r.Ref.ID))
			}
		}
		b.WriteString("\n" + s.th.MutedTxt.Render("esc to go back"))
		return b.String()
	}
	return ""
}
