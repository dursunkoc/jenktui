// Package palette is the jump screen `ctrl+p` opens from anywhere: Collections
// and catalog jobs in one filterable list. Enter on a Collection opens its Run
// Monitor, on a job opens the Job Catalog filtered to it.
package palette

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
)

// Opened is the reply to Deps.OpenCollection. Screen is built on the UI
// goroutine; a Notice (unknown Collection) is shown instead.
type Opened struct {
	Screen func() core.Screen
	Notice string
}

// Deps are the palette's dependencies.
type Deps struct {
	Parent string
	// Collections reads the Collections' names; Jobs reads the catalog. Each
	// runs in its own command, so the Collections show without waiting for
	// the (slower) catalog.
	Collections func() ([]string, error)
	Jobs        func() ([]jenkins.Job, error)
	// OpenCollection returns the command that opens the named Collection's
	// Run Monitor and yields an Opened. nil hides Collections' Enter.
	OpenCollection func(name string) tea.Cmd
	// OpenJob builds the screen for a job (the catalog filtered to it).
	OpenJob func(fullName string) core.Screen
}

type (
	collectionsMsg struct {
		names []string
		err   error
	}
	jobsMsg struct {
		jobs []jenkins.Job
		err  error
	}
)

const collectionMark = "◆ "

type screen struct {
	deps    Deps
	th      core.Theme
	list    *widgets.List
	colls   []string
	jobs    []jenkins.Job
	collsOK bool  // the Collections arrived
	jobsOK  bool  // the catalog answered (with jobs or an error)
	err     error // the Collections could not be read
	warn    string
	notice  string
	opening bool
	w, h    int
}

// New returns the palette.
func New(deps Deps) core.Screen {
	th := core.NewTheme()
	return &screen{deps: deps, th: th, list: widgets.NewList(th, widgets.Options{NoSelect: true}), w: 80, h: 20}
}

func (s *screen) IsPalette() {}

func (s *screen) Title() string { return "Jump to" }

func (s *screen) Init() tea.Cmd {
	colls, jobs := s.deps.Collections, s.deps.Jobs
	return tea.Batch(s.list.StartFiltering(),
		func() tea.Msg {
			n, err := colls()
			return collectionsMsg{names: n, err: err}
		},
		func() tea.Msg {
			j, err := jobs()
			return jobsMsg{jobs: j, err: err}
		})
}

var keyGo = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "jump"))

func (s *screen) Keys() []key.Binding {
	return []key.Binding{keyGo, core.KeyUp, core.KeyDown, core.KeyBack}
}

func (s *screen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		s.list.SetSize(s.w, max(s.h-2, 1))
	case collectionsMsg:
		s.collsOK, s.err, s.colls = true, m.err, m.names
		s.refill()
	case jobsMsg:
		s.jobsOK, s.jobs = true, m.jobs
		s.warn = ""
		if m.err != nil {
			s.warn = "jobs unavailable: " + m.err.Error()
		}
		s.refill()
	case Opened:
		wait := s.opening
		s.opening, s.notice = false, m.Notice
		if m.Screen != nil && wait {
			return s, core.Cmd(core.Replace{Screen: m.Screen()})
		}
	case tea.KeyMsg:
		return s.key(m)
	}
	return s, nil
}

// refill rebuilds the rows from what has arrived so far.
func (s *screen) refill() {
	var out []widgets.Item
	for _, c := range s.colls {
		out = append(out, widgets.Item{Key: collectionMark + c, Label: collectionMark + c})
	}
	for _, j := range s.jobs {
		r := catalog.Row(j, s.deps.Parent, core.StatusUnknown)
		r.ShowBadge = false
		out = append(out, r)
	}
	s.list.SetItems(out)
}

func (s *screen) key(msg tea.KeyMsg) (core.Screen, tea.Cmd) {
	s.opening, s.notice = false, ""
	switch {
	case key.Matches(msg, keyGo):
		return s, s.jump()
	case key.Matches(msg, core.KeyBack) && (!s.list.Filtering() || s.list.Filter() == ""):
		return s, core.Cmd(core.Pop{})
	}
	return s, s.list.Update(msg)
}

// jump acts on the highlighted row.
func (s *screen) jump() tea.Cmd {
	it, ok := s.list.Cursor()
	if !ok {
		return nil
	}
	if name, isColl := strings.CutPrefix(it.Key, collectionMark); isColl {
		if s.deps.OpenCollection == nil {
			return nil
		}
		s.opening, s.notice = true, "opening "+name+"…"
		return s.deps.OpenCollection(name)
	}
	if s.deps.OpenJob == nil {
		return nil
	}
	return core.Cmd(core.Replace{Screen: s.deps.OpenJob(it.Key)})
}

func (s *screen) View() string {
	switch {
	case s.err != nil:
		return s.th.ErrTitle.Render("Could not load: " + ansi.Truncate(s.err.Error(), max(s.w-14, 10), "…"))
	case !s.collsOK:
		return s.list.View() + "\n" + s.th.MutedTxt.Render("Loading…")
	}
	out := s.list.View()
	if !s.jobsOK {
		out += "\n" + s.th.MutedTxt.Render("loading jobs…")
	}
	if s.warn != "" {
		out += "\n" + s.th.WarnTxt.Render(ansi.Truncate(s.warn, s.w, "…"))
	}
	if s.notice != "" {
		out += "\n" + s.th.WarnTxt.Render(ansi.Truncate(s.notice, s.w, "…"))
	}
	return out
}
