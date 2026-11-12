// Package monitor implements the Run Monitor: the live screen showing the
// latest build of every Job in one Collection, plus builds triggered from it.
package monitor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/sysint"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Defaults for Deps.
const (
	DefaultFastInterval = 3 * time.Second
	DefaultSlowInterval = 30 * time.Second
	DefaultPollTimeout  = 10 * time.Second
	// clockSkew tolerates a Jenkins clock slightly behind ours when deciding
	// whether lastBuild belongs to this trigger.
	clockSkew = 5 * time.Second
)

// Client is the subset of the Jenkins API the monitor needs.
type Client interface {
	ResolveQueue(ctx context.Context, ref jenkins.QueueRef) (jenkins.QueueStatus, error)
	BuildStatus(ctx context.Context, fullName string, number int) (jenkins.Build, error)
	Stages(ctx context.Context, fullName string, number int) ([]jenkins.Stage, error)
	ConsoleText(ctx context.Context, fullName string, number int, start int64) (jenkins.LogChunk, error)
	StageLog(ctx context.Context, fullName string, number int, nodeID string) (string, error)
	StageSteps(ctx context.Context, fullName string, number int, stageID string) ([]jenkins.Stage, error)
}

// Sys is the operating-system integration the monitor uses (open a URL, copy
// text, notify, ring the bell). sysint.Sys satisfies it; tests fake it. A nil
// Deps.Sys hides the keys that need it.
type Sys interface {
	Open(url string) error
	Copy(text string) (sysint.Route, error)
	Notify(title, body string) error
	Bell()
}

// Deps are the monitor's dependencies. Zero durations get defaults.
type Deps struct {
	Client       Client
	Parent       string        // Parent Job; job names are shown relative to it
	FastInterval time.Duration // rows that are running or queued (default 3s)
	SlowInterval time.Duration // every other row (default 30s)
	PollTimeout  time.Duration // per-request context timeout (default 10s)
	Now          func() time.Time

	Sys     Sys    // open, copy, notify, bell; nil hides the keys that need it
	SaveDir string // where `S` saves a log; empty means the working directory
	Notify  string // "failures", "all" or "off" (config notify); empty means "failures"
	// Rerun builds the screen that reruns job with params (wired to the
	// trigger form in main.go); nil hides `r`.
	Rerun func(job string, params map[string]string) core.Screen
}

// Run is one triggered build to track. Err, when set, is the trigger failure
// and the run is shown as failed to start (Ref is ignored).
type Run struct {
	Job string
	Ref jenkins.QueueRef
	Err error
}

type phase int

const (
	phQueued phase = iota
	phRunning
	phFinished
	phLeftQueue   // queue item vanished and no matching build was found
	phFailedStart // trigger failed
	phUnpolled    // not looked at yet
	phNotBuilt    // the job has never been built
	phMissing     // the job is no longer in the catalog; never polled
)

type runState struct {
	run         Run
	rev         int       // unique per row creation; drops results for replaced rows
	triggered   bool      // started from a Trigger (queue ref resolved), not just observed
	at          time.Time // when this row was created; lower bound for the last-build fallback
	lastPolled  time.Time
	phase       phase
	why         string
	num         int
	build       jenkins.Build
	result      string // SUCCESS, FAILURE, UNSTABLE, ABORTED when finished
	stages      []jenkins.Stage
	stagesFinal bool  // stages were fetched after the build finished
	noStages    bool  // sticky after ErrNoStages
	pollErr     error // last transient poll error
}

func (s runState) success() bool { return s.phase == phFinished && s.result == "SUCCESS" }

// live rows are polled at the fast interval.
func (s runState) live() bool { return s.phase == phQueued || s.phase == phRunning }

func (s runState) pollable() bool {
	switch s.phase {
	case phLeftQueue, phFailedStart, phMissing:
		return false
	case phFinished:
		return !(s.triggered && s.num == 0) // cancelled in the queue: nothing to poll
	}
	return true
}

// Messages.
type (
	tickMsg   struct{ gen int }
	resultMsg struct {
		gen int
		st  runState
	}
	stagesMsg struct {
		epoch  int
		job    string
		rev    int
		num    int
		stages []jenkins.Stage
		err    error
	}
)

type model struct {
	deps        Deps
	th          core.Theme
	name        string
	names       map[string]core.JobName // how known jobs are shown (SetNames)
	runs        []runState
	revs        int
	triggered   time.Time
	hideSuccess bool
	cursor      int // index into the visible rows
	details     bool
	log         *logView // the console log over the details view, when open
	logGen      int      // bumped when a log opens or closes; drops stale log messages
	stageCur    int      // highlighted line (stage or parallel child) in the details view
	w, h        int

	gen     int          // bumped on every poll cycle start and on exit; stale msgs are dropped
	epoch   int          // bumped on exit; guards detail stage fetches
	modal   bool         // the running cycle is behind the first-load modal
	pending int          // results outstanding in the current cycle
	load    core.Loading // spinner; active exactly while pending > 0

	manual bool // the running cycle was started by `R`, not by the tick chain

	notice      string    // timed status text for Subtitle (flash)
	noticeUntil time.Time // when the notice stops showing
	sections    []detailSection
	confirm     *confirmPrompt // yes/no question on the last body line, or nil
	overlay     overlay        // modal replacing the body, or nil
	md          mdState        // lane MD feature data (never on runState)
	ma          maState        // lane MA feature data (never on runState)

	schedule func(d time.Duration, gen int) tea.Cmd // injectable for tests
}

func newBase(deps Deps, name string) *model {
	if deps.FastInterval <= 0 {
		deps.FastInterval = DefaultFastInterval
	}
	if deps.SlowInterval <= 0 {
		deps.SlowInterval = DefaultSlowInterval
	}
	if deps.PollTimeout <= 0 {
		deps.PollTimeout = DefaultPollTimeout
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	th := core.NewTheme()
	m := &model{deps: deps, th: th, load: core.NewLoading(th), name: name, triggered: deps.Now()}
	m.load.SetNow(deps.Now)
	m.initHooks() // MD registers its detail sections there; MA registers its own below this line
	m.schedule = func(d time.Duration, gen int) tea.Cmd {
		return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{gen} })
	}
	return m
}

// New returns a Run Monitor tracking just the given triggered runs.
func New(deps Deps, runs []Run) core.Screen {
	m := newBase(deps, "")
	m.add(runs)
	return m
}

// NewCollection returns the Run Monitor of one Collection: one row per job
// (the latest build of each). Jobs in missing are no longer in the catalog.
func NewCollection(deps Deps, name string, jobs []string, missing map[string]bool) core.Screen {
	m := newBase(deps, name)
	m.setJobs(jobs, missing)
	return m
}

func (m *model) newRow(r Run, triggered bool) runState {
	m.revs++
	st := runState{run: r, rev: m.revs, triggered: triggered, at: m.deps.Now(), phase: phUnpolled}
	if triggered {
		st.phase = phQueued
		if r.Err != nil {
			st.phase = phFailedStart
		}
	}
	return st
}

func (m *model) index(job string) int {
	return slices.IndexFunc(m.runs, func(s runState) bool { return s.run.Job == job })
}

// add replaces the row of each run's job with a fresh queued row, or appends one.
func (m *model) add(runs []Run) {
	keep := m.currentJob()
	for _, r := range runs {
		st := m.newRow(r, true)
		if i := m.index(r.Job); i >= 0 {
			m.runs[i] = st
		} else {
			m.runs = append(m.runs, st)
		}
	}
	m.restoreCursor(keep)
}

func (m *model) setJobs(jobs []string, missing map[string]bool) {
	keep := m.currentJob()
	out := make([]runState, 0, len(jobs))
	for _, j := range jobs {
		if slices.ContainsFunc(out, func(s runState) bool { return s.run.Job == j }) {
			continue
		}
		var st runState
		if i := m.index(j); i >= 0 {
			st = m.runs[i]
		} else {
			st = m.newRow(Run{Job: j}, false)
		}
		switch {
		case missing[j]:
			st.phase, st.why, st.pollErr = phMissing, "", nil
		case st.phase == phMissing:
			st.phase, st.lastPolled = phUnpolled, time.Time{}
		}
		out = append(out, st)
	}
	m.runs = out
	m.restoreCursor(keep)
}

// SetJobs syncs a Collection monitor with the Collection's current members:
// new members get rows, removed ones are dropped, existing rows keep their
// state, and missing is applied. Returns false if s is not a monitor.
func SetJobs(s core.Screen, jobs []string, missing map[string]bool) bool {
	m, ok := s.(*model)
	if ok {
		m.setJobs(jobs, missing)
	}
	return ok
}

// SetNames tells the monitor how jobs are shown, e.g. a Multibranch branch
// as "project › branch". Jobs not in names are shown by their name relative
// to Deps.Parent. Returns false if s is not a monitor.
func SetNames(s core.Screen, names map[string]core.JobName) bool {
	m, ok := s.(*model)
	if ok {
		m.names = names
	}
	return ok
}

// Add puts triggered runs into the monitor: each replaces its job's row with a
// fresh queued row (or is appended when the job has none). Returns false if s
// is not a monitor. Push or replace the same screen again to show it.
func Add(s core.Screen, runs []Run) bool {
	m, ok := s.(*model)
	if ok {
		m.add(runs)
	}
	return ok
}

// HideSuccess sets whether rows with a SUCCESS result are hidden. Returns
// false if s is not a monitor.
func HideSuccess(s core.Screen, on bool) bool {
	m, ok := s.(*model)
	if ok {
		keep := m.currentJob()
		m.hideSuccess = on
		m.restoreCursor(keep)
	}
	return ok
}

// Rename changes the Collection name shown in the title. Returns false if s is
// not a monitor.
func Rename(s core.Screen, name string) bool {
	m, ok := s.(*model)
	if ok {
		m.name = name
	}
	return ok
}

// visible returns indices into m.runs of the rows currently shown.
func (m *model) visible() []int {
	var v []int
	for i, r := range m.runs {
		if m.hideSuccess && r.success() {
			continue
		}
		v = append(v, i)
	}
	return v
}

// selected is the index into m.runs under the cursor, or -1.
func (m *model) selected() int {
	v := m.visible()
	if m.cursor < 0 || m.cursor >= len(v) {
		return -1
	}
	return v[m.cursor]
}

func (m *model) currentJob() string {
	if i := m.selected(); i >= 0 {
		return m.runs[i].run.Job
	}
	return ""
}

// restoreCursor puts the cursor back on job when still visible, else clamps it.
func (m *model) restoreCursor(job string) {
	v := m.visible()
	if job != "" {
		for vi, i := range v {
			if m.runs[i].run.Job == job {
				m.cursor = vi
				return
			}
		}
	}
	m.cursor = max(min(m.cursor, len(v)-1), 0)
	if len(v) == 0 {
		m.details = false
	}
}

// Title is "Run Monitor: Ops", or with a Parent Job "Run Monitor · team ·
// Ops", like the Collection screens.
func (m *model) Title() string {
	p := strings.Trim(m.deps.Parent, "/")
	switch {
	case m.name == "" && p == "":
		return "Run Monitor"
	case m.name == "":
		return "Run Monitor · " + p
	case p == "":
		return "Run Monitor: " + m.name
	}
	return "Run Monitor · " + p + " · " + m.name
}

var keyRefresh = key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))
var keyShow = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "show/hide successful"))

func (m *model) Keys() []key.Binding {
	if m.confirm != nil {
		return []key.Binding{keyConfirmYes, keyConfirmNo}
	}
	if m.overlay != nil {
		return m.overlay.keys()
	}
	if m.log != nil {
		return m.logKeys()
	}
	if m.details {
		return []key.Binding{core.KeyUp, core.KeyDown, keyLog, keyLogAll,
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "hide details")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to list")),
			core.KeyQuit}
	}
	return []key.Binding{keyDetails, keyShow, keyRefresh, core.KeyBack}
}

var keyDetails = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details"))

// FullHelp groups every key of the runs list for the help panel. The
// details view has few keys and shows them all in the help bar.
func (m *model) FullHelp() []core.HelpGroup {
	if m.details || m.log != nil || m.confirm != nil || m.overlay != nil {
		return nil
	}
	return []core.HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{core.KeyUp, core.KeyDown}},
		{Title: "Run Monitor", Keys: []key.Binding{keyDetails, keyShow, keyRefresh}},
		{Title: "General", Keys: []key.Binding{core.KeyBack, core.KeyQuit}},
	}
}

// Init starts the first poll cycle immediately and polls every row in it.
func (m *model) Init() tea.Cmd {
	for i := range m.runs {
		m.runs[i].lastPolled = time.Time{}
	}
	return core.Cmd(tickMsg{gen: m.gen})
}

// interval is the delay before the next cycle: fast while any row is running
// or queued, slow otherwise.
func (m *model) interval() time.Duration {
	for _, r := range m.runs {
		if r.live() {
			return m.deps.FastInterval
		}
	}
	return m.deps.SlowInterval
}

// due reports whether a row is polled in a cycle at now.
func (m *model) due(r runState, now time.Time) bool {
	if !r.pollable() {
		return false
	}
	if r.live() || r.lastPolled.IsZero() {
		return true
	}
	return now.Sub(r.lastPolled) >= m.deps.SlowInterval
}

func (m *model) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case spinner.TickMsg:
		return m, m.load.Update(msg)
	case tickMsg:
		if msg.gen != m.gen {
			return m, nil // stale tick
		}
		return m, m.startCycle(false)
	case resultMsg:
		if msg.gen != m.gen {
			return m, nil // stale result
		}
		m.pending--
		if i := m.index(msg.st.run.Job); i >= 0 && m.runs[i].rev == msg.st.rev {
			keep := m.currentJob()
			m.runs[i] = msg.st
			m.restoreCursor(keep)
		}
		if m.pending <= 0 {
			m.finishLoad()
			return m, m.schedule(m.interval(), m.gen)
		}
	case stagesMsg:
		if msg.epoch != m.epoch || msg.err != nil && !errors.Is(msg.err, jenkins.ErrNoStages) {
			return m, nil
		}
		if i := m.index(msg.job); i >= 0 && m.runs[i].rev == msg.rev && m.runs[i].num == msg.num {
			if msg.err != nil {
				m.runs[i].noStages, m.runs[i].stages = true, nil
			} else {
				m.runs[i].stages, m.runs[i].stagesFinal = msg.stages, m.runs[i].phase == phFinished
			}
		}
	case applier:
		return m, msg.apply(m)
	case logMsg, logTickMsg:
		return m, m.updateLog(msg)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *model) key(msg tea.KeyMsg) (core.Screen, tea.Cmd) {
	if m.overlay != nil && m.confirm == nil {
		cmd, done := m.overlay.key(msg)
		if done {
			m.overlay = nil
		}
		return m, cmd
	}
	if m.log != nil && m.confirm == nil {
		if cmd, ok := m.logKey(msg); ok {
			return m, cmd
		}
		if !key.Matches(msg, core.KeyQuit) {
			return m, nil // the log swallows keys it does not use; q still quits
		}
	}
	switch {
	case m.confirm != nil:
		return m, m.confirmKey(msg)
	case key.Matches(msg, keyLog) && m.details:
		return m, m.openLog(m.stageLines() > 0) // the selected stage or step when there are stages
	case key.Matches(msg, keyLogAll) && m.details:
		return m, m.openLog(false)
	case key.Matches(msg, core.KeyBack):
		if m.details {
			m.details = false // esc in the details view returns to the runs list
			return m, nil
		}
		m.gen++ // invalidate in-flight ticks/results; builds keep running on Jenkins
		m.epoch++
		m.closeLog()
		m.load.StopNow()
		return m, core.Cmd(core.Pop{})
	case key.Matches(msg, core.KeyQuit):
		m.gen++
		m.epoch++
		m.closeLog()
		m.load.StopNow()
		return m, core.Cmd(core.Quit{})
	case key.Matches(msg, core.KeyUp):
		if m.details {
			m.stageCur = max(m.stageCur-1, 0)
		} else if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, core.KeyDown):
		if m.details {
			m.stageCur = max(min(m.stageCur+1, m.stageLines()-1), 0)
		} else if m.cursor < len(m.visible())-1 {
			m.cursor++
		}
	case key.Matches(msg, keyRefresh):
		// Ignored in the details view and behind the first-load modal.
		if m.details || m.load.Active() && m.unpolled() {
			return m, nil
		}
		return m, m.startCycle(true)
	case key.Matches(msg, keyShow):
		if !m.details {
			keep := m.currentJob()
			m.hideSuccess = !m.hideSuccess
			m.restoreCursor(keep)
		}
	case key.Matches(msg, core.KeyEnter):
		if m.selected() < 0 {
			return m, nil
		}
		m.details = !m.details
		m.stageCur = 0
		if m.details {
			return m, m.detailStages()
		}
	}
	return m, nil
}

// stageLines is the number of lines the details view lists for the selected
// row: each stage plus its parallel children.
func (m *model) stageLines() int {
	i := m.selected()
	if i < 0 {
		return 0
	}
	n := 0
	for _, s := range m.runs[i].stages {
		n += 1 + len(s.Children)
	}
	return n
}

// detailStages fetches the stages of a finished, non-success row whose details
// were just opened (running rows already get theirs every cycle).
func (m *model) detailStages() tea.Cmd {
	i := m.selected()
	st := m.runs[i]
	if st.phase != phFinished || st.success() || st.stagesFinal || st.noStages || st.num == 0 {
		return nil
	}
	client, timeout, epoch := m.deps.Client, m.deps.PollTimeout, m.epoch
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		stages, err := client.Stages(ctx, st.run.Job, st.num)
		return stagesMsg{epoch: epoch, job: st.run.Job, rev: st.rev, num: st.num, stages: stages, err: err}
	}
}

// detailOpen reports whether row i is a finished non-success row whose
// details are open, so its stages are worth fetching.
func (m *model) detailOpen(i int) bool {
	st := m.runs[i]
	return m.details && i == m.selected() && st.phase == phFinished && !st.success()
}

// startCycle opens a new generation and polls every due row concurrently,
// delivering each result as a message. With manual every pollable row is
// polled regardless of due-ness (manual refresh). Bumping the generation
// drops the previous cycle's results and its scheduled tick, so only one tick
// chain stays alive.
func (m *model) startCycle(manual bool) tea.Cmd {
	m.gen++
	m.manual = manual
	m.modal = m.unpolled()
	now := m.deps.Now()
	var cmds []tea.Cmd
	for i := range m.runs {
		if manual && !m.runs[i].pollable() || !manual && !m.due(m.runs[i], now) {
			continue
		}
		m.runs[i].lastPolled = now
		cmds = append(cmds, m.pollCmd(m.gen, m.runs[i], m.detailOpen(i)))
	}
	m.pending = len(cmds)
	if m.pending == 0 {
		m.finishLoad()
		return m.schedule(m.interval(), m.gen)
	}
	return tea.Batch(append(cmds, m.load.Start())...)
}

// finishLoad ends the cycle's spinner: at once after the first-load modal,
// else after the minimum visible time.
func (m *model) finishLoad() {
	if m.modal {
		m.load.StopNow()
	} else {
		m.load.Stop()
	}
}

func (m *model) pollCmd(gen int, st runState, detail bool) tea.Cmd {
	client, timeout, trig := m.deps.Client, m.deps.PollTimeout, st.at
	if trig.IsZero() {
		trig = m.triggered
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return resultMsg{gen: gen, st: pollRun(ctx, client, trig, st, detail)}
	}
}

// pollRun advances one row's state by a single observation of Jenkins.
func pollRun(ctx context.Context, c Client, triggered time.Time, st runState, detail bool) runState {
	st.pollErr = nil
	job := st.run.Job
	if st.triggered && st.num == 0 && st.phase != phFinished {
		q, err := c.ResolveQueue(ctx, st.run.Ref)
		switch {
		case errors.Is(err, jenkins.ErrQueueItemGone):
			b, berr := c.BuildStatus(ctx, job, 0)
			if berr != nil || b.Number == 0 || b.Timestamp.Before(triggered.Add(-clockSkew)) {
				if berr != nil && !errors.Is(berr, jenkins.ErrBuildNotFound) {
					st.pollErr = berr // transient; try again next cycle
					return st
				}
				st.phase, st.why = phLeftQueue, ""
				return st
			}
			st.num = b.Number
		case err != nil:
			st.pollErr = err
			return st
		case q.State == jenkins.QueueCancelled:
			st.phase, st.result, st.why = phFinished, "ABORTED", "cancelled in queue"
			return st
		case q.State == jenkins.QueueStarted && q.BuildNumber > 0:
			st.num = q.BuildNumber
		default:
			st.phase, st.why = phQueued, q.Why
			return st
		}
	}
	// Follow a triggered build by number until it finishes; afterwards (and
	// for rows that were never triggered) follow the job's latest build.
	n := st.num
	if st.phase == phFinished || !st.triggered {
		n = 0
	}
	b, err := c.BuildStatus(ctx, job, n)
	if err != nil {
		if !st.triggered && errors.Is(err, jenkins.ErrBuildNotFound) {
			st.phase, st.num, st.why, st.stages, st.build, st.result = phNotBuilt, 0, "", nil, jenkins.Build{}, ""
			return st
		}
		st.pollErr = err // e.g. ErrBuildNotFound right after leaving the queue
		if st.phase == phQueued {
			st.phase = phRunning
		}
		return st
	}
	if b.Number > 0 && b.Number != st.num {
		st.num, st.stages, st.stagesFinal = b.Number, nil, false
	}
	st.build, st.why = b, ""
	if b.Building || b.Result == "" {
		st.phase, st.result = phRunning, ""
	} else {
		st.phase, st.result = phFinished, b.Result
	}
	if st.success() {
		st.stages = nil
	}
	if (st.phase == phRunning || detail && !st.success() && !st.stagesFinal) && !st.noStages && st.num > 0 {
		stages, err := c.Stages(ctx, job, st.num)
		switch {
		case errors.Is(err, jenkins.ErrNoStages):
			st.noStages, st.stages = true, nil
		case err != nil:
			st.pollErr = err // keep previous stages
		default:
			st.stages, st.stagesFinal = stages, st.phase == phFinished
		}
	}
	return st
}
