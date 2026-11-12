// Package catalog implements the Job Catalog screen.
package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/branchfilter"
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

// Opener opens a URL in the browser. sysint.Sys satisfies it; tests fake it.
type Opener interface {
	Open(url string) error
}

// OpenedMsg is the result of an Opener call started by OpenCmd.
type OpenedMsg struct {
	URL string
	Err error
}

// OpenCmd opens url off the UI goroutine (the opener can block).
func OpenCmd(o Opener, url string) tea.Cmd {
	return func() tea.Msg { return OpenedMsg{URL: url, Err: o.Open(url)} }
}

// OpenNotice is the status text for the result of an open.
func OpenNotice(m OpenedMsg) string {
	if m.Err != nil {
		return "could not open: " + strings.TrimPrefix(m.Err.Error(), "sysint: ")
	}
	return "opened in browser"
}

// KeyOpen opens the highlighted job in the browser.
var KeyOpen = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser"))

// WithOpener enables `o` (open the highlighted job in the browser) together
// with WithJobURL; without both the key is hidden.
func WithOpener(o Opener) Option {
	return func(s *Screen) { s.opener = o }
}

// WithJobURL says how a job's web URL is built from its full name, e.g.
// (*jenkins.Client).JobURL (from the configured base URL).
func WithJobURL(f func(fullName string) string) Option {
	return func(s *Screen) { s.jobURL = f }
}

func (s *Screen) canOpen() bool { return s.opener != nil && s.jobURL != nil }

// Option customises New.
type Option func(*Screen)

// WithFilter starts the catalog with text typed into the filter box (a
// literal match: wildcard characters are escaped by the caller if needed).
func WithFilter(text string) Option {
	return func(s *Screen) { s.list.SetFilter(text) }
}

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

	opener Opener
	jobURL func(fullName string) string
	cat    jenkins.Catalog
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
			rows[i] = PlaceholderRow(it, s.parent)
			continue
		}
		rows[i] = Row(it.Job, s.parent, statusFromColor(it.Job.Color))
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
	keys := []key.Binding{core.KeyFilter, widgets.KeySelect, KeyBranch}
	if s.canOpen() {
		keys = append(keys, KeyOpen)
	}
	return append(keys, core.KeyBack)
}

// FullHelp groups every catalog key for the help panel (? key). It is nil
// while loading, on an error, or while a text field has focus.
func (s *Screen) FullHelp() []core.HelpGroup {
	if s.editing || s.err != nil || s.loading || s.list.Filtering() {
		return nil
	}
	general := []key.Binding{core.KeyBack, core.KeyQuit}
	if s.canOpen() {
		general = append([]key.Binding{KeyOpen}, general...)
	}
	return []core.HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{core.KeyUp, core.KeyDown}},
		{Title: "Selection", Keys: s.list.SelectKeys()},
		{Title: "Filter", Keys: append(s.list.FilterKeys(), KeyBranch, KeyEditor)},
		{Title: "General", Keys: general},
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
	case OpenedMsg:
		if !s.editing { // the editor draws the notice as an error
			s.notice = OpenNotice(m)
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
	if s.canOpen() && key.Matches(m, KeyOpen) {
		it, ok := s.list.Cursor()
		if !ok || it.Disabled {
			return s, nil
		}
		s.notice = ""
		return s, OpenCmd(s.opener, s.jobURL(it.Key))
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
	shown, total := s.list.JobCounts()
	count := fmt.Sprintf("%d jobs", total)
	if s.list.Filter() != "" {
		count = fmt.Sprintf("%d of %d jobs", shown, total)
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

// JobName is how job j is shown under parent: relative to the parent, and
// for a branch of a Multibranch Project as the project plus the decoded
// branch name ("digital-ui › feature/x" rather than "digital-ui/feature%2Fx").
func JobName(j jenkins.Job, parent string) core.JobName {
	if j.Multibranch != "" && j.Branch != "" {
		return core.JobName{Name: projectName(j.Multibranch, parent), Branch: j.Branch}
	}
	return core.JobName{Name: core.RelName(j.FullName, parent)}
}

// projectName is a Multibranch Project's name relative to parent. When the
// project is the parent itself the relative name would be empty, so it shows
// the project's own (last) segment: "app › main" rather than just "main".
func projectName(project, parent string) string {
	if project == strings.Trim(parent, "/") {
		return project[strings.LastIndex(project, "/")+1:]
	}
	return core.RelName(project, parent)
}

// Row is the list row for job j: keyed by its full name, labelled with
// JobName.
func Row(j jenkins.Job, parent string, badge core.Status) widgets.Item {
	n := JobName(j, parent)
	return widgets.Item{Key: j.FullName, Label: n.Name, Branch: n.Branch, Badge: badge, ShowBadge: true}
}

// PlaceholderRow is the disabled row of a Multibranch Project with no
// (matching) branches: the project in the name column, the reason, e.g.
// "(no matching branches)", in the branch column.
func PlaceholderRow(it branchfilter.Item, parent string) widgets.Item {
	why := strings.TrimSpace(strings.TrimPrefix(it.Label, it.Project))
	return widgets.Item{Key: it.Key(), Label: projectName(it.Project, parent), Branch: why, Disabled: true}
}

// statusFromColor maps a Jenkins "color" hint to a badge status via
// jenkins.StatusFromColor.
func statusFromColor(color string) core.Status {
	switch jenkins.StatusFromColor(color) {
	case jenkins.JobSuccess:
		return core.StatusSuccess
	case jenkins.JobFailed:
		return core.StatusFailed
	case jenkins.JobUnstable:
		return core.StatusUnstable
	case jenkins.JobAborted:
		return core.StatusAborted
	case jenkins.JobRunning:
		return core.StatusRunning
	}
	return core.StatusUnknown
}
