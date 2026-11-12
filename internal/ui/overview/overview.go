// Package overview is the screen that lists every Collection with the number
// of failed, running and queued jobs in it. It polls on its own (the
// per-Collection Run Monitors stop when left) and opens a Collection's monitor
// on Enter.
package overview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Defaults for zero Deps values.
const (
	DefaultInterval = 30 * time.Second
	DefaultTimeout  = 10 * time.Second
	workers         = 8
)

// Client is the part of the Jenkins API the overview needs. If it also has
// QueuedJobs (as *jenkins.Client does), queued jobs are counted.
type Client interface {
	BuildStatus(ctx context.Context, fullName string, number int) (jenkins.Build, error)
}

// folderColorer reads one folder's job colours in a request (*jenkins.Client
// has it); without it every job's latest build is read instead.
type folderColorer interface {
	FolderColors(ctx context.Context, folder string) (map[string]string, error)
}

type queueLister interface {
	QueuedJobs(ctx context.Context) (map[string]int, error)
}

// Collection is one Collection to summarise.
type Collection struct {
	Name string
	Jobs []string
}

// Opened is the reply to Deps.Open. Screen is built on the UI goroutine; a
// Notice is shown on the screen (an unreadable Collection).
type Opened struct {
	Screen func() core.Screen
	Notice string
}

// Deps are the screen's dependencies.
type Deps struct {
	Client Client
	// Load returns the current Collections; it is called in each poll, so
	// edits show up. It runs off the UI goroutine.
	Load func() ([]Collection, error)
	// Open returns the command that opens a Collection's Run Monitor and
	// yields an Opened. nil disables Enter.
	Open     func(name string) tea.Cmd
	Interval time.Duration // between polls (default 30s)
	Timeout  time.Duration // per request (default 10s)
	// After schedules msg after d; tests replace it.
	After func(d time.Duration, msg tea.Msg) tea.Cmd
}

// counts is one Collection's tally.
type counts struct {
	name                                         string
	jobs                                         int
	failed, running, queued, unreadable, missing int
}

type (
	tickMsg   struct{ gen int }
	resultMsg struct {
		gen  int
		rows []counts
		err  error
	}
)

type screen struct {
	deps    Deps
	th      core.Theme
	gen     int
	rows    []counts
	loaded  bool
	err     error
	cursor  int
	notice  string
	opening bool // Enter was pressed; a key cancels
	busy    bool // a cycle for the current gen is in flight
	w, h    int
}

// New returns the overview screen.
func New(deps Deps) core.Screen {
	if deps.Interval <= 0 {
		deps.Interval = DefaultInterval
	}
	if deps.Timeout <= 0 {
		deps.Timeout = DefaultTimeout
	}
	if deps.After == nil {
		deps.After = func(d time.Duration, msg tea.Msg) tea.Cmd {
			return tea.Tick(d, func(time.Time) tea.Msg { return msg })
		}
	}
	return &screen{deps: deps, th: core.NewTheme(), w: 80, h: 20}
}

func (s *screen) Title() string { return "Overview" }

var keyOpen = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open monitor"))
var keyRefresh = key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))

func (s *screen) Keys() []key.Binding {
	return []key.Binding{core.KeyUp, core.KeyDown, keyOpen, keyRefresh, core.KeyBack}
}

func (s *screen) Init() tea.Cmd { return s.start() }

// Resume restarts polling when a screen pushed over this one is popped.
func (s *screen) Resume() tea.Cmd { return s.start() }

// start begins a poll cycle and drops anything from an earlier one.
func (s *screen) start() tea.Cmd {
	s.gen++
	s.busy = true
	return s.cycle(s.gen)
}

// pause drops in-flight and scheduled work (the screen is left or covered).
func (s *screen) pause() { s.gen++; s.busy = false }

func (s *screen) cycle(gen int) tea.Cmd {
	deps := s.deps
	return func() tea.Msg {
		cols, err := deps.Load()
		if err != nil {
			return resultMsg{gen: gen, err: err}
		}
		return resultMsg{gen: gen, rows: tally(deps, cols)}
	}
}

// tally reads each distinct folder's job colours in one request, then the
// queue, and counts per Collection. A folder that cannot be read falls back
// to the latest build of each of its members. Everything runs one request at
// a time inside the command.
func tally(deps Deps, cols []Collection) []counts {
	folders := map[string][]string{} // folder -> distinct member job names
	var order []string
	seen := map[string]bool{}
	for _, c := range cols {
		for _, j := range c.Jobs {
			if seen[j] {
				continue
			}
			seen[j] = true
			f := folderOf(j)
			if _, ok := folders[f]; !ok {
				order = append(order, f)
			}
			folders[f] = append(folders[f], j)
		}
	}
	state := map[string]jobState{}
	fc, hasColors := deps.Client.(folderColorer)
	for _, f := range order {
		var colors map[string]string
		if hasColors {
			ctx, cancel := context.WithTimeout(context.Background(), deps.Timeout)
			var err error
			colors, err = fc.FolderColors(ctx, f)
			cancel()
			if err != nil {
				colors = nil
			}
		}
		for _, j := range folders[f] {
			if colors != nil {
				if col, ok := colors[leafOf(j)]; ok {
					state[j] = stateOfColor(col)
				} else {
					state[j] = stMissing
				}
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), deps.Timeout)
			b, err := deps.Client.BuildStatus(ctx, j, 0)
			cancel()
			switch {
			case err == nil:
				state[j] = stateOfBuild(b)
			case errors.Is(err, jenkins.ErrBuildNotFound):
				state[j] = stNone // never built
			default:
				state[j] = stUnreadable
			}
		}
	}
	var queued map[string]int
	if q, ok := deps.Client.(queueLister); ok {
		ctx, cancel := context.WithTimeout(context.Background(), deps.Timeout)
		queued, _ = q.QueuedJobs(ctx) // a failed read leaves queued uncounted
		cancel()
	}
	out := make([]counts, len(cols))
	for i, c := range cols {
		n := counts{name: c.Name, jobs: len(c.Jobs)}
		for _, j := range c.Jobs {
			switch state[j] {
			case stFailed:
				n.failed++
			case stRunning:
				n.running++
			case stMissing:
				n.missing++
			case stUnreadable:
				n.unreadable++
			}
			if queued[j] > 0 {
				n.queued++
			}
		}
		out[i] = n
	}
	return out
}

type jobState int

const (
	stNone jobState = iota
	stFailed
	stRunning
	stMissing
	stUnreadable
)

// stateOfColor maps Jenkins' colour hint: red and yellow (unstable) are
// failed, anything animated is running; blue, aborted, notbuilt and disabled
// count nowhere.
func stateOfColor(color string) jobState {
	switch jenkins.StatusFromColor(color) {
	case jenkins.JobRunning:
		return stRunning
	case jenkins.JobFailed, jenkins.JobUnstable:
		return stFailed
	}
	return stNone
}

func stateOfBuild(b jenkins.Build) jobState {
	switch {
	case b.Building:
		return stRunning
	case b.Result == "FAILURE" || b.Result == "UNSTABLE":
		return stFailed
	}
	return stNone
}

// folderOf is the job's parent folder path ("" for a top-level job).
func folderOf(job string) string {
	if i := strings.LastIndex(job, "/"); i >= 0 {
		return job[:i]
	}
	return ""
}

func leafOf(job string) string { return job[strings.LastIndex(job, "/")+1:] }

func (s *screen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
	case tickMsg:
		if m.gen != s.gen {
			return s, nil
		}
		return s, s.start()
	case resultMsg:
		if m.gen != s.gen {
			return s, nil
		}
		s.loaded, s.err, s.busy = true, m.err, false
		if m.err == nil {
			s.rows = m.rows
			s.cursor = max(min(s.cursor, len(s.rows)-1), 0)
		}
		return s, s.deps.After(s.deps.Interval, tickMsg{gen: s.gen})
	case Opened:
		wait := s.opening
		s.opening, s.notice = false, m.Notice
		if m.Screen != nil && wait {
			s.pause()
			return s, core.Cmd(core.Push{Screen: m.Screen()})
		}
	case tea.KeyMsg:
		return s.key(m)
	}
	return s, nil
}

func (s *screen) key(msg tea.KeyMsg) (core.Screen, tea.Cmd) {
	s.opening, s.notice = false, ""
	switch {
	case key.Matches(msg, core.KeyBack):
		s.pause()
		return s, core.Cmd(core.Pop{})
	case key.Matches(msg, core.KeyQuit):
		s.pause()
		return s, core.Cmd(core.Quit{})
	case key.Matches(msg, core.KeyUp):
		s.cursor = max(s.cursor-1, 0)
	case key.Matches(msg, core.KeyDown):
		s.cursor = max(min(s.cursor+1, len(s.rows)-1), 0)
	case key.Matches(msg, keyRefresh):
		if s.busy {
			return s, nil // a cycle is already running
		}
		return s, s.start()
	case key.Matches(msg, keyOpen):
		if s.deps.Open == nil || s.cursor >= len(s.rows) {
			return s, nil
		}
		s.opening, s.notice = true, "opening "+s.rows[s.cursor].name+"…"
		return s, s.deps.Open(s.rows[s.cursor].name)
	}
	return s, nil
}

func (s *screen) View() string {
	switch {
	case s.err != nil && len(s.rows) == 0:
		return s.th.ErrTitle.Render("Could not read the Collections: "+ansi.Truncate(s.err.Error(), max(s.w-34, 10), "…")) + "\n"
	case !s.loaded:
		return s.th.MutedTxt.Render("Loading…")
	case len(s.rows) == 0:
		return s.th.MutedTxt.Render("No Collections yet. Create one under Collections.")
	}
	width := 0
	for _, r := range s.rows {
		width = max(width, lipgloss.Width(r.name))
	}
	width = min(width, max(s.w/3, 10))
	var lines []string
	for i, r := range s.rows {
		name := ansi.Truncate(r.name, width, "…")
		name += strings.Repeat(" ", width-lipgloss.Width(name))
		var line string
		if i == s.cursor {
			line = s.th.Selected.Render("> ") + lipgloss.NewStyle().Reverse(true).Render(name) + "  " + s.summary(r)
		} else {
			line = "  " + name + "  " + s.summary(r)
		}
		lines = append(lines, ansi.Truncate(line, s.w, "…"))
	}
	if s.busy {
		lines = append(lines, "", s.th.MutedTxt.Render("refreshing…"))
	}
	if s.notice != "" {
		lines = append(lines, "", s.th.WarnTxt.Render(ansi.Truncate(s.notice, s.w, "…")))
	}
	return strings.Join(lines, "\n")
}

// summary is the counts of one row; failed and queued also say what they are
// in words, so colour is not the only cue.
func (s *screen) summary(r counts) string {
	if r.jobs == 0 {
		return s.th.MutedTxt.Render("no jobs")
	}
	var parts []string
	if r.failed > 0 {
		parts = append(parts, s.th.FailTxt.Render(fmt.Sprintf("%d failed", r.failed)))
	}
	if r.running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", r.running))
	}
	if r.queued > 0 {
		parts = append(parts, s.th.WarnTxt.Render(fmt.Sprintf("%d queued", r.queued)))
	}
	if r.missing > 0 {
		parts = append(parts, s.th.WarnTxt.Render(fmt.Sprintf("%d missing", r.missing)))
	}
	if r.unreadable > 0 {
		parts = append(parts, s.th.MutedTxt.Render(fmt.Sprintf("%d unreadable", r.unreadable)))
	}
	if len(parts) == 0 {
		return s.th.MutedTxt.Render(fmt.Sprintf("%d jobs, nothing failed or running", r.jobs))
	}
	return strings.Join(parts, s.th.MutedTxt.Render(" · "))
}
