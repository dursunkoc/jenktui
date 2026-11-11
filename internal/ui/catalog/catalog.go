// Package catalog implements the Job Catalog screen.
package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
)

// JobLister is the slice of the Jenkins client the catalog needs.
type JobLister interface {
	ListJobs(ctx context.Context, parent string) ([]jenkins.Job, error)
}

// CatalogLister is optionally implemented by a JobLister that also reports
// Multibranch Projects (needed for "no matching branches" rows).
type CatalogLister interface {
	ListCatalog(ctx context.Context, parent string) (jenkins.Catalog, error)
}

// Option customises New.
type Option func(*Screen)

// WithBranchFilter supplies the Branch Filter patterns and persistence. The
// filter starts on when there are patterns.
func WithBranchFilter(st Settings) Option {
	return func(s *Screen) {
		s.BranchPanel = NewBranchPanel(s.theme, st)
	}
}

var (
	keyRetry = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry"))
)

type loadedMsg struct {
	seq int
	cat jenkins.Catalog
	err error
}

// Screen is the Job Catalog: a flat, filterable, multi-select list of Jobs.
type Screen struct {
	lister  JobLister
	parent  string
	theme   core.Theme
	load    core.Loading
	list    *widgets.List
	loading bool
	loaded  bool // a load succeeded; data stays visible during a reload
	err     error
	seq     int
	w, h    int

	cat jenkins.Catalog
	BranchPanel
}

// New returns the catalog for jobs under parent ("" means the Jenkins root).
func New(lister JobLister, parent string, opts ...Option) *Screen {
	th := core.NewTheme()
	s := &Screen{lister: lister, parent: parent, theme: th, load: core.NewLoading(th), BranchPanel: NewBranchPanel(th, Settings{}),
		list: widgets.NewList(th, widgets.Options{}), loading: true, w: 80, h: 20}
	for _, o := range opts {
		o(s)
	}
	return s
}

// rebuild recomputes list rows from the loaded catalog and the Branch Filter.
func (s *Screen) rebuild() {
	items := s.Filter().Apply(s.cat.Jobs, s.cat.MultibranchProjects)
	rows := make([]widgets.Item, len(items))
	for i, it := range items {
		if it.Placeholder {
			rows[i] = widgets.Item{Key: it.Key(), Label: it.Label, Disabled: true}
			continue
		}
		rows[i] = widgets.Item{Key: it.Key(), Badge: statusFromColor(it.Job.Color), ShowBadge: true}
	}
	s.list.SetItems(rows)
}

// Selected returns the selected job full paths in list order.
func (s *Screen) Selected() []string { return s.list.Selected() }

func (s *Screen) fetch() tea.Cmd {
	seq, l, p := s.seq, s.lister, s.parent
	return func() tea.Msg {
		if cl, ok := l.(CatalogLister); ok {
			cat, err := cl.ListCatalog(context.Background(), p)
			return loadedMsg{seq: seq, cat: cat, err: err}
		}
		jobs, err := l.ListJobs(context.Background(), p)
		return loadedMsg{seq: seq, cat: jenkins.Catalog{Jobs: jobs}, err: err}
	}
}

func (s *Screen) Init() tea.Cmd { return tea.Batch(s.load.Start(), s.fetch()) }

func (s *Screen) Title() string {
	if s.parent == "" {
		return "Job Catalog"
	}
	return "Job Catalog · " + s.parent
}

func (s *Screen) Keys() []key.Binding {
	switch {
	case s.editing:
		return s.EditorKeys()
	case s.err != nil:
		return []key.Binding{keyRetry, core.KeyBack, core.KeyQuit}
	case s.loading:
		return []key.Binding{core.KeyBack, core.KeyQuit}
	case s.list.Filtering():
		return s.list.FilterEditKeys()
	}
	return []key.Binding{core.KeyFilter, widgets.KeySelect, KeyBranch, core.KeyBack}
}

// FullHelp groups every catalog key for the help panel (? key). It is nil
// while loading, on an error, or while a text field has focus.
func (s *Screen) FullHelp() []core.HelpGroup {
	if s.editing || s.err != nil || s.loading || s.list.Filtering() {
		return nil
	}
	return []core.HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{core.KeyUp, core.KeyDown}},
		{Title: "Selection", Keys: s.list.SelectKeys()},
		{Title: "Filter", Keys: append(s.list.FilterKeys(), KeyBranch, KeyEditor)},
		{Title: "General", Keys: []key.Binding{core.KeyBack, core.KeyQuit}},
	}
}

func (s *Screen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		s.list.SetSize(s.w, max(s.h, 1))
		return s, nil
	case spinner.TickMsg:
		return s, s.load.Update(m)
	case loadedMsg:
		if m.seq != s.seq {
			return s, nil // stale
		}
		s.loading, s.err = false, m.err
		if s.loaded {
			s.load.Stop()
		} else {
			s.load.StopNow() // blocking modal was shown: nothing to flash
		}
		if m.err == nil {
			s.cat, s.loaded = m.cat, true
			s.rebuild()
		}
		return s, nil
	case tea.KeyMsg:
		return s.key(m)
	}
	return s, nil
}

func (s *Screen) key(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	if s.editing {
		if _, changed := s.HandleKey(m); changed {
			s.rebuild()
		}
		return s, nil
	}
	if !s.loading && s.err == nil {
		if s.list.Filtering() {
			return s, s.list.Update(m)
		}
	}
	switch {
	case key.Matches(m, core.KeyBack):
		return s, core.Cmd(core.Pop{})
	case key.Matches(m, core.KeyQuit):
		return s, core.Cmd(core.Quit{})
	case s.err != nil && key.Matches(m, keyRetry):
		s.seq++
		s.loading, s.err = true, nil
		return s, tea.Batch(s.load.Start(), s.fetch())
	case s.loading || s.err != nil:
		return s, nil
	}
	if handled, changed := s.HandleKey(m); handled {
		if changed {
			s.rebuild()
		}
		return s, nil
	}
	return s, s.list.Update(m)
}

func (s *Screen) View() string {
	switch {
	case s.err != nil:
		return s.theme.ErrorView("Could not load jobs", s.err, "press r to retry, esc to go back")
	case s.loading && !s.loaded:
		return s.load.Modal("Loading jobs…", s.w, s.h)
	}
	if s.editing {
		return s.EditorView()
	}
	return s.list.View()
}

// Subtitle is the count and filter line shown next to the title.
func (s *Screen) Subtitle() string {
	if s.err != nil || s.loading && !s.loaded || s.editing {
		return ""
	}
	count := fmt.Sprintf("%d jobs", s.list.Total())
	if s.list.Filter() != "" {
		count = fmt.Sprintf("%d of %d jobs", s.list.Len(), s.list.Total())
	}
	if n := len(s.list.Selected()); n > 0 {
		count += fmt.Sprintf(" · %d selected", n)
	}
	count += " · " + s.Status()
	if s.loading || s.load.Active() {
		count += " · " + s.load.View("refreshing…")
	}
	if s.notice != "" {
		count += " · " + s.notice
	}
	return count
}

// statusFromColor maps a Jenkins "color" hint to a badge status.
// TODO: switch to jenkins.StatusFromColor once it lands.
func statusFromColor(color string) core.Status {
	base, anime := strings.CutSuffix(color, "_anime")
	if anime {
		return core.StatusRunning
	}
	switch base {
	case "blue", "green":
		return core.StatusSuccess
	case "red", "yellow":
		return core.StatusFailed
	}
	return core.StatusUnknown
}
