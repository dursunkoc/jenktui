// Package collections implements the Collections feature: the Collections
// screen, an opened Collection (status, reorder, trigger) and the shared Job
// picker used to create and edit Collections.
package collections

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Deps is everything the Collections screens need; all of it is injected.
type Deps struct {
	// Lister fetches the Job Catalog. If it also implements
	// catalog.CatalogLister (as *jenkins.Client does) Multibranch Projects are
	// reported too; otherwise ListJobs is used.
	Lister catalog.JobLister
	// Parent is the Parent Job ("" means the Jenkins root).
	Parent string
	// Path is the collections.toml file; read and written on every change.
	Path string
	// Trigger builds the screen that triggers builds of the given Job keys
	// (jenkins.Job.FullName, branch names stay URL-encoded). It is pushed by
	// the Collection view's `t`. collection is the Collection's name, so the
	// started builds can be added to that Collection's Run Monitor; names
	// says how the Collection's members are shown there (see Monitor). May be
	// nil (triggering is then unavailable).
	Trigger func(collection string, jobKeys []string, names map[string]core.JobName) core.Screen
	// Monitor returns the Run Monitor screen of a Collection (get-or-create),
	// synced to jobKeys and with the missing members flagged, ready to push
	// with successful rows hidden. Pushed by `m` in the Collections list and
	// in an opened Collection. names says how each known job is shown (it
	// may be nil or lack a job: the monitor then shows the plain name). May
	// be nil (the monitor is then unavailable).
	Monitor func(name string, jobKeys []string, missing map[string]bool, names map[string]core.JobName) core.Screen
	// Renamed, when set, is told after a Collection was renamed so per-name
	// state (such as its Run Monitor) can follow it.
	Renamed func(oldName, newName string)
	// Opener opens the highlighted job in the browser (`o` in an opened
	// Collection). May be nil: the key is then hidden.
	Opener catalog.Opener
	// JobURL builds a job's web URL from its full name (the client's JobURL).
	// `o` needs both Opener and JobURL.
	JobURL func(fullName string) string
	// BranchFilter wires the Branch Filter patterns and their persistence into
	// the picker, exactly as for the Job Catalog.
	BranchFilter catalog.Settings
}

// New returns the Collections screen (the menu entry).
func New(deps Deps) core.Screen {
	e := &env{deps: deps, theme: core.NewTheme(), patterns: append([]string(nil), deps.BranchFilter.Patterns...)}
	s := &listScreen{env: e, w: 80, h: 20, load: core.NewLoading(e.theme)}
	s.reload()
	return s
}

// env is state shared by the screens of one Collections session.
type env struct {
	deps     Deps
	theme    core.Theme
	patterns []string // latest Branch Filter patterns, so later pickers see saved edits
}

// titled appends the Parent Job to a title, "Collections · team", so the
// screen says which project its Collections belong to. Under the Jenkins
// root the title is unchanged.
func (e *env) titled(title string) string {
	if p := strings.Trim(e.deps.Parent, "/"); p != "" {
		return title + " · " + p
	}
	return title
}

// pickerTitle is the member picker's title: "Edit Ops", or with a Parent Job
// "Edit · team · Ops", like the Collection screens.
func (e *env) pickerTitle(verb, name string) string {
	if p := strings.Trim(e.deps.Parent, "/"); p != "" {
		return verb + " · " + p + " · " + name
	}
	return verb + " " + name
}

func (e *env) branchSettings() catalog.Settings {
	return catalog.Settings{
		Patterns: append([]string(nil), e.patterns...),
		Save: func(p []string) error {
			e.patterns = append([]string(nil), p...)
			if e.deps.BranchFilter.Save != nil {
				return e.deps.BranchFilter.Save(p)
			}
			return nil
		},
	}
}

// mutate loads the file, applies fn and saves, so concurrent edits are not
// overwritten with stale data.
func (e *env) mutate(fn func(*config.Collections) error) error {
	cs, err := config.LoadCollections(e.deps.Path)
	if err != nil {
		return err
	}
	if err := fn(cs); err != nil {
		return err
	}
	return config.SaveCollections(e.deps.Path, cs)
}

type loadedMsg struct {
	seq int
	cat jenkins.Catalog
	err error
}

// loadCatalog fetches the Job Catalog like internal/ui/catalog does.
func (e *env) loadCatalog(seq int) tea.Cmd {
	l, p := e.deps.Lister, e.deps.Parent
	return func() tea.Msg {
		if cl, ok := l.(catalog.CatalogLister); ok {
			cat, err := cl.ListCatalog(context.Background(), p)
			return loadedMsg{seq: seq, cat: cat, err: err}
		}
		jobs, err := l.ListJobs(context.Background(), p)
		return loadedMsg{seq: seq, cat: jenkins.Catalog{Jobs: jobs}, err: err}
	}
}

// badge maps a Jenkins color to a badge status via jenkins.StatusFromColor.
func badge(color string) core.Status {
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

func newInput(placeholder, value string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 80
	ti.SetValue(value)
	ti.Focus()
	return ti
}

var (
	keyNew     = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new"))
	keyEdit    = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit members"))
	keyRename  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rename"))
	keyDelete  = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	keyYes     = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes"))
	keyNo      = key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n/esc", "no"))
	keyMonitor = key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "monitor"))
	keyConfirm = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "ok"))
	keyCancel  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	keyOpen    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open"))
)

type mode int

const (
	modeList mode = iota
	modeNew
	modeRename
	modeDelete
)

// listScreen lists the Collections.
type listScreen struct {
	*env
	cs     *config.Collections
	err    error
	cursor int
	mode   mode
	input  textinput.Model
	notice string
	w, h   int
	load   core.Loading

	monSeq  int      // identifies the catalog load a pending `m` waits for
	monName string   // Collection waiting for that load ("" = none)
	monJobs []string // its members when `m` was pressed
}

func (s *listScreen) reload() {
	cs, err := config.LoadCollections(s.deps.Path)
	s.cs, s.err = cs, err
	if cs == nil {
		s.cs = &config.Collections{}
	}
	s.cursor = max(min(s.cursor, len(s.cs.Items)-1), 0)
}

func (s *listScreen) Init() tea.Cmd { return nil }

func (s *listScreen) Title() string { return s.titled("Collections") }

func (s *listScreen) Keys() []key.Binding {
	if s.monName != "" {
		return []key.Binding{keyCancel}
	}
	switch s.mode {
	case modeNew, modeRename:
		return []key.Binding{keyConfirm, keyCancel}
	case modeDelete:
		return []key.Binding{keyYes, keyNo}
	}
	return []key.Binding{keyOpen, keyNew, keyMonitor, core.KeyBack}
}

// FullHelp groups every key of the Collections list for the help panel. It
// is nil while a name is typed, a delete is confirmed or a monitor waits.
func (s *listScreen) FullHelp() []core.HelpGroup {
	if s.monName != "" || s.mode != modeList {
		return nil
	}
	return []core.HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{core.KeyUp, core.KeyDown}},
		{Title: "Collections", Keys: []key.Binding{keyOpen, keyNew, keyEdit, keyRename, keyDelete, keyMonitor}},
		{Title: "General", Keys: []key.Binding{core.KeyBack, core.KeyQuit}},
	}
}

func (s *listScreen) current() (config.Collection, bool) {
	if s.cursor < 0 || s.cursor >= len(s.cs.Items) {
		return config.Collection{}, false
	}
	return s.cs.Items[s.cursor], true
}

func (s *listScreen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		return s, nil
	case spinner.TickMsg:
		return s, s.load.Update(m)
	case tea.KeyMsg:
		return s.key(m)
	case loadedMsg:
		return s.monitorLoaded(m)
	}
	if s.mode == modeNew || s.mode == modeRename {
		var c tea.Cmd
		s.input, c = s.input.Update(msg)
		return s, c
	}
	return s, nil
}

func (s *listScreen) key(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	if s.monName != "" { // waiting for the catalog behind the loading modal
		if key.Matches(m, core.KeyBack) {
			s.monName = ""
			s.monSeq++ // drop the pending result
			s.load.StopNow()
		}
		return s, nil
	}
	switch s.mode {
	case modeNew, modeRename:
		return s.inputKey(m)
	case modeDelete:
		switch {
		case key.Matches(m, keyYes):
			if c, ok := s.current(); ok {
				if err := s.mutate(func(cs *config.Collections) error { return cs.Delete(c.Name) }); err != nil {
					s.notice = "could not delete: " + err.Error()
				} else {
					s.notice = fmt.Sprintf("deleted %q", c.Name)
				}
				s.reload()
			}
			s.mode = modeList
		case key.Matches(m, keyNo):
			s.mode = modeList
		}
		return s, nil
	}
	c, have := s.current()
	switch {
	case key.Matches(m, core.KeyBack):
		return s, core.Cmd(core.Pop{})
	case key.Matches(m, core.KeyQuit):
		return s, core.Cmd(core.Quit{})
	case key.Matches(m, core.KeyUp):
		s.cursor = max(s.cursor-1, 0)
	case key.Matches(m, core.KeyDown):
		s.cursor = max(min(s.cursor+1, len(s.cs.Items)-1), 0)
	case key.Matches(m, keyNew):
		s.mode, s.notice = modeNew, ""
		s.input = newInput("collection name", "")
		return s, textinput.Blink
	case !have:
	case key.Matches(m, core.KeyEnter):
		s.notice = ""
		return s, core.Cmd(core.Push{Screen: newView(s.env, c.Name, s.reload)})
	case key.Matches(m, keyMonitor):
		return s.openMonitor(c)
	case key.Matches(m, keyEdit):
		s.notice = ""
		return s, core.Cmd(core.Push{Screen: newPicker(s.env, s.pickerTitle("Edit", c.Name), c.Jobs, func(keys []string) error {
			err := s.mutate(func(cs *config.Collections) error { return cs.SetJobs(c.Name, keys) })
			s.reload()
			return err
		})})
	case key.Matches(m, keyRename):
		s.mode, s.notice = modeRename, ""
		s.input = newInput("collection name", c.Name)
		return s, textinput.Blink
	case key.Matches(m, keyDelete):
		s.mode, s.notice = modeDelete, ""
	}
	return s, nil
}

// openMonitor fetches the Job Catalog (to know which members are missing) and
// then pushes the Collection's Run Monitor.
func (s *listScreen) openMonitor(c config.Collection) (core.Screen, tea.Cmd) {
	if s.deps.Monitor == nil {
		s.notice = "monitor unavailable"
		return s, nil
	}
	s.monSeq++
	s.monName, s.monJobs = c.Name, slices.Clone(c.Jobs)
	s.notice = ""
	return s, tea.Batch(s.load.Start(), s.loadCatalog(s.monSeq))
}

func (s *listScreen) monitorLoaded(m loadedMsg) (core.Screen, tea.Cmd) {
	if m.seq != s.monSeq || s.monName == "" {
		return s, nil
	}
	name, jobs := s.monName, s.monJobs
	s.load.StopNow()
	s.monName, s.notice = "", ""
	var missing map[string]bool
	if m.err != nil {
		s.notice = "could not check for missing jobs: " + m.err.Error()
	} else {
		missing = missingOf(jobs, m.cat)
	}
	return s, core.Cmd(core.Push{Screen: s.deps.Monitor(name, jobs, missing, namesOf(jobs, m.cat, s.deps.Parent))})
}

// namesOf says how each member found in the catalog is shown.
func namesOf(members []string, cat jenkins.Catalog, parent string) map[string]core.JobName {
	in := map[string]bool{}
	for _, k := range members {
		in[k] = true
	}
	names := map[string]core.JobName{}
	for _, j := range cat.Jobs {
		if in[j.FullName] {
			names[j.FullName] = catalog.JobName(j, parent)
		}
	}
	return names
}

// missingOf flags the members that are not in the catalog.
func missingOf(members []string, cat jenkins.Catalog) map[string]bool {
	have := map[string]bool{}
	for _, j := range cat.Jobs {
		have[j.FullName] = true
	}
	missing := map[string]bool{}
	for _, k := range members {
		if !have[k] {
			missing[k] = true
		}
	}
	return missing
}

func (s *listScreen) inputKey(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	switch {
	case key.Matches(m, keyCancel):
		s.mode, s.notice = modeList, ""
		return s, nil
	case key.Matches(m, keyConfirm):
		name := strings.TrimSpace(s.input.Value())
		if name == "" {
			s.notice = "name is required"
			return s, nil
		}
		if s.mode == modeRename {
			c, _ := s.current()
			if err := s.mutate(func(cs *config.Collections) error { return cs.Rename(c.Name, name) }); err != nil {
				s.notice = err.Error()
				return s, nil
			}
			s.mode, s.notice = modeList, ""
			s.reload()
			if s.deps.Renamed != nil {
				s.deps.Renamed(c.Name, name)
			}
			return s, nil
		}
		if s.cs.Find(name) >= 0 {
			s.notice = fmt.Sprintf("collection %q already exists", name)
			return s, nil
		}
		s.mode, s.notice = modeList, ""
		return s, core.Cmd(core.Push{Screen: newPicker(s.env, s.pickerTitle("New", name), nil, func(keys []string) error {
			err := s.mutate(func(cs *config.Collections) error { return cs.Add(name, keys) })
			s.reload()
			return err
		})})
	}
	var c tea.Cmd
	s.input, c = s.input.Update(m)
	return s, c
}

func (s *listScreen) View() string {
	if s.err != nil {
		return s.theme.ErrorView("Could not read collections", s.err, "check "+s.deps.Path+", esc to go back")
	}
	if s.monName != "" {
		return s.load.Modal("Loading jobs for "+s.monName+"…", s.w, s.h)
	}
	var b strings.Builder
	b.WriteString("\n") // a blank line under the title
	switch s.mode {
	case modeNew:
		b.WriteString(s.theme.Title.Render("New collection") + "\n" + s.input.View() + "\n")
	case modeRename:
		b.WriteString(s.theme.Title.Render("Rename collection") + "\n" + s.input.View() + "\n")
	case modeDelete:
		c, _ := s.current()
		b.WriteString(s.theme.Title.Render(fmt.Sprintf("Delete %q?", c.Name)) + "\n" + s.theme.MutedTxt.Render("y to delete, n to keep") + "\n")
	}
	if s.mode != modeList {
		b.WriteString("\n")
	}
	if s.notice != "" {
		b.WriteString(s.theme.ErrTitle.Render(s.notice) + "\n")
	}
	if len(s.cs.Items) == 0 {
		b.WriteString(s.theme.MutedTxt.Render("No collections yet. Press n to create one."))
		return b.String()
	}
	for i, c := range s.cs.Items {
		line := fmt.Sprintf("%s  %s", c.Name, s.theme.MutedTxt.Render(plural(len(c.Jobs), "job")))
		if i == s.cursor {
			b.WriteString(s.theme.Selected.Render("> ") + line + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
