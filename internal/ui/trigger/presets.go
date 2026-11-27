package trigger

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/paramsmerge"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/params"
)

// PresetStore reads and writes Presets. Values are parameter name -> value.
type PresetStore interface {
	// Load returns job -> preset name -> values for the given jobs. A job
	// without presets is absent from the result.
	Load(jobs []string) (map[string]map[string]map[string]string, error)
	// Save stores values (job -> values) under name, replacing a preset of
	// the same name (letter case ignored) for each of those jobs.
	Save(name string, values map[string]map[string]string) error
	// Delete removes the preset of that name (letter case ignored) for each job.
	Delete(name string, jobs []string) error
}

var saveKey = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save preset"))

const nameLimit = 40

// presetRow is one preset name on the step.
type presetRow struct {
	name string   // display spelling: the first job's
	jobs []string // loaded jobs that have it, in trigger order
}

// presetRows merges the preset names of the loaded jobs, case-insensitively,
// sorted by lower-cased name.
func (s *screen) presetRows() []presetRow {
	by := map[string]*presetRow{}
	for _, j := range s.jobs {
		for n := range s.presets[j] {
			k := strings.ToLower(n)
			r := by[k]
			if r == nil {
				r = &presetRow{name: n}
				by[k] = r
			}
			if !slices.Contains(r.jobs, j) {
				r.jobs = append(r.jobs, j)
			}
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]presetRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, *by[k])
	}
	return rows
}

// presetEntry finds job's values for name, letter case ignored.
func (s *screen) presetEntry(job, name string) (map[string]string, bool) {
	var vals map[string]string
	found := false
	for n, v := range s.presets[job] {
		if strings.EqualFold(n, name) {
			vals, found = v, true
		}
	}
	return vals, found
}

// presetValues is job's stored values for name, cleaned: values for removed
// parameters, passwords and values that are no longer valid are dropped (the
// last kind with a note), so the job's default applies.
func (s *screen) presetValues(job, name string) (map[string]string, []string) {
	stored, _ := s.presetEntry(job, name)
	defs := map[string]jenkins.ParamDef{}
	for _, d := range s.defs[job] {
		defs[d.Name] = d
	}
	var notes []string
	out := map[string]string{}
	stale := func(n, v string) {
		notes = append(notes, fmt.Sprintf("%s: %s = %s is no longer a choice, using the default", s.name(job), n, v))
	}
	names := make([]string, 0, len(stored))
	for n := range stored {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := stored[n]
		d, ok := defs[n]
		if !ok || d.Type == "password" {
			continue
		}
		switch d.Type {
		case "choice":
			if !slices.Contains(d.Choices, v) {
				stale(n, v)
				continue
			}
		case "bool":
			if !strings.EqualFold(v, "true") && !strings.EqualFold(v, "false") {
				stale(n, v)
				continue
			}
			v = strings.ToLower(v)
		}
		out[n] = v
	}
	return out, notes
}

// resetFlow clears everything a previous choice built.
func (s *screen) resetFlow() {
	s.form, s.commonForm, s.editForm = nil, nil, nil
	s.jobForms = map[string]*params.Form{}
	s.commonVals, s.overrides, s.baseline = nil, nil, nil
	s.presetNotes = nil
	s.applied, s.saveName = "", ""
	s.cursor, s.scroll = 0, 0
	s.pwNote = false
}

// toStep returns from the first form to the preset step.
func (s *screen) toStep() tea.Cmd {
	s.st = stPreset
	s.pdel, s.pmsg = false, ""
	return nil
}

// apply fills the jobs that have the preset and opens the summary.
func (s *screen) apply(row presetRow) tea.Cmd {
	s.resetFlow()
	s.applied, s.saveName = row.name, row.name
	vals := map[string]map[string]string{}
	for _, j := range s.jobs {
		v, notes := s.presetValues(j, row.name)
		vals[j] = v
		s.presetNotes = append(s.presetNotes, notes...)
	}
	if len(s.jobs) == 1 {
		j := s.jobs[0]
		s.form = params.NewForm(s.name(j), s.defs[j], params.Options{Prefill: vals[j]})
		s.resizeForms()
		s.enterSummary()
		return nil
	}
	// Target values per job: its defaults overlaid with the preset.
	target := paramsmerge.Resolve(s.plan, nil, nil)
	for _, j := range s.jobs {
		for n, v := range vals[j] {
			target[j][n] = v
		}
	}
	// The Common Parameters form shows a value only when every job gets it.
	// commonVals always comes from the form itself.
	if len(s.plan.Common) > 0 {
		pre := map[string]string{}
		for _, c := range s.plan.Common {
			v, same := "", true
			for i, j := range s.jobs {
				if i == 0 {
					v = target[j][c.Name]
				} else if target[j][c.Name] != v {
					same = false
				}
			}
			if same {
				pre[c.Name] = v
			}
		}
		s.buildCommonForm(pre)
		s.commonVals = s.commonForm.Values()
	} else {
		s.commonVals = map[string]string{}
	}
	for _, j := range s.jobs {
		if len(s.defs[j]) == 0 {
			continue
		}
		f := s.buildJobForm(j, target[j], "")
		if slicesIndex(s.jobSeq, j) >= 0 {
			s.jobForms[j] = f
		}
		s.collectOverrides(j, target[j])
	}
	s.cursor = 0
	s.enterSummary()
	return nil
}

// presetKey handles keys on the preset step. It always returns.
func (s *screen) presetKey(k tea.KeyMsg) (core.Screen, tea.Cmd) {
	rows := s.presetRows()
	s.prow = max(min(s.prow, len(rows)), 0)
	if s.pdel {
		switch k.String() {
		case "y", "Y":
			s.pdel = false
			return s.deletePreset(rows[s.prow-1])
		case "n", "N", "esc":
			s.pdel = false
		}
		return s, nil
	}
	s.pmsg = ""
	switch k.String() {
	case "up", "k":
		s.prow = max(s.prow-1, 0)
	case "down", "j":
		s.prow = min(s.prow+1, len(rows))
	case "enter":
		if s.prow == 0 {
			s.resetFlow()
			return s, s.startFlow()
		}
		return s, s.apply(rows[s.prow-1])
	case "esc":
		return s, core.Cmd(core.Pop{})
	case "d":
		if s.prow > 0 {
			s.pdel = true
		}
	}
	return s, nil
}

func (s *screen) deletePreset(row presetRow) (core.Screen, tea.Cmd) {
	if err := s.deps.Presets.Delete(row.name, row.jobs); err != nil {
		s.pmsg = "delete failed: " + err.Error()
		return s, nil
	}
	for _, j := range row.jobs {
		for n := range s.presets[j] {
			if strings.EqualFold(n, row.name) {
				delete(s.presets[j], n)
			}
		}
		if len(s.presets[j]) == 0 {
			delete(s.presets, j)
		}
	}
	n := len(s.presetRows())
	s.prow = max(min(s.prow, n), 0)
	if n == 0 {
		s.sawStep = false
		s.resetFlow()
		return s, s.startFlow()
	}
	return s, nil
}

func (s *screen) presetKeys() []key.Binding {
	if s.pdel {
		return []key.Binding{key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "delete")),
			key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "cancel"))}
	}
	ks := []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))}
	if s.prow > 0 {
		ks = append(ks, key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")))
	}
	return append(ks, core.KeyUp, core.KeyDown, core.KeyBack)
}

func (s *screen) presetView() string {
	rows := s.presetRows()
	s.prow = max(min(s.prow, len(rows)), 0)
	lines := []string{"No preset (start from the defaults)"}
	width := 0
	for _, r := range rows {
		width = max(width, len([]rune(r.name)))
	}
	for _, r := range rows {
		l := fmt.Sprintf("%-*s", width, r.name)
		lines = append(lines, l)
	}
	body := max(s.h-len(s.notes)-8, 3)
	start := 0
	if s.prow >= body {
		start = s.prow - body + 1
	}
	end := min(start+body, len(lines))
	var b strings.Builder
	b.WriteString(s.banner() + s.th.Title.Render("Preset") + "\n\n")
	for i := start; i < end; i++ {
		count := ""
		if i > 0 && len(s.jobs) > 1 {
			count = s.th.MutedTxt.Render(fmt.Sprintf("  %d of %d jobs", len(rows[i-1].jobs), len(s.jobs)))
		}
		if i == s.prow {
			b.WriteString(s.th.Selected.Render("› "+lines[i]) + count + "\n")
		} else {
			b.WriteString("  " + lines[i] + count + "\n")
		}
	}
	switch {
	case s.pdel && s.prow > 0:
		r := rows[s.prow-1]
		target := fmt.Sprintf("%d jobs", len(r.jobs))
		if len(r.jobs) == 1 {
			target = s.name(r.jobs[0])
		}
		b.WriteString("\n" + fmt.Sprintf("Delete preset %s for %s? y/n", r.name, target))
	case s.pmsg != "":
		b.WriteString("\n" + s.th.FailTxt.Render(s.pmsg))
	}
	return b.String()
}

// summaryNotes are the lines between the summary's head and body, each
// ending in a newline.
func (s *screen) summaryNotes() string {
	var b strings.Builder
	for _, n := range s.presetNotes {
		b.WriteString(s.th.MutedTxt.Render("! "+n) + "\n")
	}
	if s.pwNote {
		msg := "Passwords are not saved in presets. Press e to enter them."
		if len(s.jobs) == 1 {
			msg = "Passwords are not saved in presets. Press n or Esc to go back and enter them."
		}
		b.WriteString(s.th.MutedTxt.Render("! "+msg) + "\n")
	}
	return b.String()
}

// canSave reports whether the summary can save a preset.
func (s *screen) canSave() bool {
	if s.deps.Presets == nil {
		return false
	}
	for _, j := range s.jobs {
		for _, d := range s.defs[j] {
			if d.Type != "password" {
				return true
			}
		}
	}
	return false
}

func (s *screen) startNaming() tea.Cmd {
	in := textinput.New()
	in.Prompt, in.CharLimit, in.Width = "", nameLimit, 30
	in.PlaceholderStyle, in.CompletionStyle = s.th.Placeholder, s.th.Placeholder
	in.SetValue(s.saveName)
	in.CursorEnd()
	s.nameIn, s.naming = in, true
	return s.nameIn.Focus()
}

// nameKey handles every key while the name prompt is open.
func (s *screen) nameKey(k tea.KeyMsg) (core.Screen, tea.Cmd) {
	switch k.String() {
	case "esc":
		s.naming = false
		return s, nil
	case "enter":
		return s, s.save(strings.TrimSpace(s.nameIn.Value()))
	}
	var c tea.Cmd
	s.nameIn, c = s.nameIn.Update(k)
	return s, c
}

func (s *screen) fail(msg string) {
	s.flash, s.flashErr = msg, true
}

// save stores the summary's values, passwords left out, under name.
func (s *screen) save(name string) tea.Cmd {
	if name == "" {
		s.fail("preset name is required")
		return nil
	}
	s.naming = false
	if s.presetErr != nil {
		s.fail("Presets are unavailable: " + s.presetErr.Error())
		return nil
	}
	values := map[string]map[string]string{}
	var saved []string
	for _, j := range s.jobs {
		pw := map[string]bool{}
		for _, d := range s.defs[j] {
			if d.Type == "password" {
				pw[d.Name] = true
			}
		}
		v := map[string]string{}
		for n, x := range s.final[j] {
			if !pw[n] {
				v[n] = x
			}
		}
		if len(v) > 0 {
			values[j] = v
			saved = append(saved, j)
		}
	}
	if len(values) == 0 {
		s.fail("nothing to save")
		return nil
	}
	if err := s.deps.Presets.Save(name, values); err != nil {
		s.fail("save failed: " + err.Error())
		return nil
	}
	if s.presets == nil {
		s.presets = map[string]map[string]map[string]string{}
	}
	for _, j := range saved {
		if s.presets[j] == nil {
			s.presets[j] = map[string]map[string]string{}
		}
		for n := range s.presets[j] {
			if strings.EqualFold(n, name) {
				delete(s.presets[j], n)
			}
		}
		s.presets[j][name] = cloneVals(values[j])
	}
	s.saveName = name
	target := fmt.Sprintf("%d jobs", len(saved))
	if len(saved) == 1 {
		target = s.name(saved[0])
	}
	s.flash, s.flashErr = fmt.Sprintf("Saved preset %s for %s", name, target), false
	return nil
}

func cloneVals(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
