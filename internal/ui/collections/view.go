package collections

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
)

var (
	keyTrigger = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "trigger"))
	keyRefresh = key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))
	keyRemove  = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove missing"))
)

// viewScreen is one opened Collection.
type viewScreen struct {
	*env
	name     string
	members  []string
	onChange func() // tells the Collections screen the file changed
	load     core.Loading
	list     *widgets.List
	cat      jenkins.Catalog
	loaded   bool
	loading  bool
	err      error
	seq      int
	notice   string
	w, h     int
}

func newView(e *env, name string, onChange func()) *viewScreen {
	s := &viewScreen{env: e, name: name, onChange: onChange, load: core.NewLoading(e.theme), loading: true, w: 80, h: 20,
		list: widgets.NewList(e.theme, widgets.Options{Reorderable: true})}
	s.readMembers()
	return s
}

// readMembers refreshes the member keys from the file.
func (s *viewScreen) readMembers() {
	cs, err := config.LoadCollections(s.deps.Path)
	if err != nil {
		s.notice = "could not read collections: " + err.Error()
		return
	}
	if i := cs.Find(s.name); i >= 0 {
		s.members = slices.Clone(cs.Items[i].Jobs)
	}
}

func (s *viewScreen) rebuild() {
	jobs := map[string]jenkins.Job{}
	for _, j := range s.cat.Jobs {
		jobs[j.FullName] = j
	}
	items := make([]widgets.Item, len(s.members))
	for i, k := range s.members {
		j, ok := jobs[k]
		if !ok {
			j = jenkins.Job{FullName: k} // missing: no branch info, shown by name
		}
		items[i] = catalog.Row(j, s.deps.Parent, badge(j.Color))
		items[i].Missing = s.loaded && !ok
	}
	s.list.SetItems(items)
}

func (s *viewScreen) Init() tea.Cmd {
	s.rebuild()
	return tea.Batch(s.load.Start(), s.loadCatalog(s.seq))
}

func (s *viewScreen) Title() string { return s.titled("Collection") + " · " + s.name }

func (s *viewScreen) Keys() []key.Binding {
	if s.err != nil || s.loading && !s.loaded {
		return []key.Binding{keyRefresh, core.KeyBack, core.KeyQuit}
	}
	if s.list.Filtering() {
		return s.list.FilterEditKeys()
	}
	keys := []key.Binding{keyTrigger, keyMonitor, widgets.KeySelect, core.KeyFilter}
	if s.canOpen() {
		keys = append(keys, catalog.KeyOpen)
	}
	return append(keys, core.KeyBack)
}

// FullHelp groups every key of an opened Collection for the help panel. It
// is nil before the first load, on an error, or while the filter has focus.
func (s *viewScreen) FullHelp() []core.HelpGroup {
	if s.err != nil || s.loading && !s.loaded || s.list.Filtering() {
		return nil
	}
	actions := []key.Binding{keyTrigger, keyMonitor, keyEdit, keyRemove, keyRefresh}
	if s.canOpen() {
		actions = append(actions, catalog.KeyOpen)
	}
	return []core.HelpGroup{
		{Title: "Navigation", Keys: append([]key.Binding{core.KeyUp, core.KeyDown}, s.list.MoveKeys()...)},
		{Title: "Selection", Keys: s.list.SelectKeys()},
		{Title: "Filter", Keys: s.list.FilterKeys()},
		{Title: "Actions", Keys: actions},
		{Title: "General", Keys: []key.Binding{core.KeyBack, core.KeyQuit}},
	}
}

func (s *viewScreen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		s.list.SetSize(s.w, max(s.h-1, 1))
	case spinner.TickMsg:
		return s, s.load.Update(m)
	case loadedMsg:
		if m.seq != s.seq {
			return s, nil
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
	case catalog.OpenedMsg:
		s.notice = catalog.OpenNotice(m)
	case tea.KeyMsg:
		return s.key(m)
	}
	return s, nil
}

func (s *viewScreen) missing() []string {
	var out []string
	for _, it := range s.list.Items() {
		if it.Missing {
			out = append(out, it.Key)
		}
	}
	return out
}

func (s *viewScreen) persist(keys []string) bool {
	err := s.mutate(func(cs *config.Collections) error { return cs.SetJobs(s.name, keys) })
	if err != nil {
		s.notice = "could not save: " + err.Error()
		return false
	}
	s.members = slices.Clone(keys)
	if s.onChange != nil {
		s.onChange()
	}
	return true
}

func (s *viewScreen) key(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	if s.list.Filtering() && s.loaded {
		return s, s.list.Update(m)
	}
	switch {
	case key.Matches(m, core.KeyBack):
		return s, core.Cmd(core.Pop{})
	case key.Matches(m, core.KeyQuit):
		return s, core.Cmd(core.Quit{})
	case key.Matches(m, keyRefresh):
		s.seq++
		s.loading, s.err, s.notice = true, nil, ""
		return s, tea.Batch(s.load.Start(), s.loadCatalog(s.seq))
	case !s.loaded:
		return s, nil
	case key.Matches(m, keyEdit):
		return s, core.Cmd(core.Push{Screen: newPicker(s.env, s.pickerTitle("Edit", s.name), s.members, func(keys []string) error {
			if !s.persist(keys) {
				return fmt.Errorf("%s", s.notice)
			}
			s.rebuild()
			return nil
		})})
	case s.canOpen() && key.Matches(m, catalog.KeyOpen):
		it, ok := s.list.Cursor()
		if !ok || it.Missing {
			return s, nil
		}
		s.notice = ""
		return s, catalog.OpenCmd(s.deps.Opener, s.deps.JobURL(it.Key))
	case key.Matches(m, keyTrigger):
		return s.trigger()
	case key.Matches(m, keyMonitor):
		if s.deps.Monitor == nil {
			s.notice = "monitor unavailable"
			return s, nil
		}
		s.notice = ""
		missing := map[string]bool{}
		for _, k := range s.missing() {
			missing[k] = true
		}
		return s, core.Cmd(core.Push{Screen: s.deps.Monitor(s.name, slices.Clone(s.members), missing, namesOf(s.members, s.cat, s.deps.Parent))})
	case key.Matches(m, keyRemove):
		if it, ok := s.list.Cursor(); ok && it.Missing {
			keys := slices.DeleteFunc(slices.Clone(s.members), func(k string) bool { return k == it.Key })
			if s.persist(keys) {
				s.notice = fmt.Sprintf("removed %s", it.Key)
				s.rebuild()
			}
		} else {
			s.notice = "x removes a missing job; the cursor job still exists"
		}
		return s, nil
	}
	before := keysOf(s.list.Items())
	cmd := s.list.Update(m)
	if after := keysOf(s.list.Items()); !slices.Equal(before, after) {
		if !s.persist(after) { // roll the on-screen order back
			s.rebuild()
		}
	}
	return s, cmd
}

func keysOf(items []widgets.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key
	}
	return out
}

// trigger pushes the Trigger screen for the selected Jobs (or the cursor Job).
// Missing members can never be selected, so they are always skipped, and the
// notice names them.
func (s *viewScreen) trigger() (core.Screen, tea.Cmd) {
	keys := s.list.Selected()
	if len(keys) == 0 {
		if it, ok := s.list.Cursor(); ok && !it.Missing {
			keys = []string{it.Key}
		}
	}
	skipped := s.missing()
	switch {
	case s.deps.Trigger == nil:
		s.notice = "triggering is not available"
		return s, nil
	case len(keys) == 0:
		s.notice = "nothing to trigger"
		if len(skipped) > 0 {
			s.notice += " (missing: " + strings.Join(skipped, ", ") + ")"
		}
		return s, nil
	}
	s.notice = ""
	if len(skipped) > 0 {
		s.notice = "skipped missing: " + strings.Join(skipped, ", ")
	}
	return s, core.Cmd(core.Push{Screen: s.deps.Trigger(s.name, keys, namesOf(s.members, s.cat, s.deps.Parent))})
}

func (s *viewScreen) View() string {
	switch {
	case s.err != nil:
		return s.theme.ErrorView("Could not load jobs", s.err, "press R to retry, esc to go back")
	case !s.loaded:
		text := "Loading " + s.name + "…"
		if s.seq > 0 {
			text = "Refreshing " + s.name + "…"
		}
		return s.load.Modal(text, s.w, s.h)
	}
	if len(s.members) == 0 {
		return "\n" + s.theme.MutedTxt.Render("Empty collection. Press e to add jobs.")
	}
	return s.list.View()
}

// Subtitle is the count line shown next to the title.
func (s *viewScreen) Subtitle() string {
	if s.err != nil || !s.loaded {
		return ""
	}
	head := plural(len(s.members), "job")
	if n := len(s.list.Selected()); n > 0 {
		head += fmt.Sprintf(" · %d selected", n)
	}
	if n := len(s.missing()); n > 0 {
		head += fmt.Sprintf(" · %d missing", n)
	}
	if s.loading || s.load.Active() {
		head += " · " + s.load.View("refreshing…")
	}
	if s.notice != "" {
		head += " · " + s.notice
	}
	return head
}

func (s *viewScreen) canOpen() bool { return s.deps.Opener != nil && s.deps.JobURL != nil }
