package collections

import (
	"fmt"
	"slices"
	"sort"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
)

var keySave = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm"))

// pickerScreen is the filterable multi-select over the whole Job Catalog,
// shared by create and edit. Selection is tracked by key in chosen, so members
// hidden by the Branch Filter (or gone from the catalog) are never dropped.
type pickerScreen struct {
	*env
	title   string
	members []string        // initial members, in order
	chosen  map[string]bool // key -> selected, authoritative for hidden keys
	onSave  func(keys []string) error
	bp      catalog.BranchPanel
	load    core.Loading
	list    *widgets.List
	cat     jenkins.Catalog
	loaded  bool
	loading bool
	err     error
	seq     int
	notice  string
	w, h    int
}

func newPicker(e *env, title string, members []string, onSave func([]string) error) *pickerScreen {
	s := &pickerScreen{env: e, title: title, members: slices.Clone(members), chosen: map[string]bool{},
		onSave: onSave, bp: catalog.NewBranchPanel(e.theme, e.branchSettings()), load: core.NewLoading(e.theme), loading: true,
		w: 80, h: 20, list: widgets.NewList(e.theme, widgets.Options{})}
	for _, k := range members {
		s.chosen[k] = true
	}
	return s
}

// sync copies the list's selection state for visible rows into chosen.
func (s *pickerScreen) sync() {
	sel := map[string]bool{}
	for _, k := range s.list.Selected() {
		sel[k] = true
	}
	for _, it := range s.list.Items() {
		if !it.Disabled && !it.Missing {
			s.chosen[it.Key] = sel[it.Key]
		}
	}
}

// rebuild recomputes rows from the catalog and Branch Filter, re-checking
// everything chosen.
func (s *pickerScreen) rebuild() {
	items := s.bp.Filter().Apply(s.cat.Jobs, s.cat.MultibranchProjects)
	rows := make([]widgets.Item, len(items))
	for i, it := range items {
		if it.Placeholder {
			rows[i] = catalog.PlaceholderRow(it, s.deps.Parent)
			continue
		}
		rows[i] = catalog.Row(it.Job, s.deps.Parent, badge(it.Job.Color))
	}
	s.list.SetItems(rows)
	for k, v := range s.chosen {
		if v {
			s.list.Select(k)
		}
	}
}

// result is the confirmed membership: existing members keep their order unless
// unchecked, newly checked jobs follow alphabetically.
func (s *pickerScreen) result() []string {
	s.sync()
	var out []string
	in := map[string]bool{}
	for _, k := range s.members {
		if s.chosen[k] {
			out = append(out, k)
			in[k] = true
		}
	}
	var added []string
	for k, v := range s.chosen {
		if v && !in[k] {
			added = append(added, k)
		}
	}
	sort.Strings(added)
	return append(out, added...)
}

// Busy is true while the catalog loads (its reply would be lost under the
// jump palette).
func (s *pickerScreen) Busy() bool { return s.loading }

func (s *pickerScreen) Init() tea.Cmd { return tea.Batch(s.load.Start(), s.loadCatalog(s.seq)) }

func (s *pickerScreen) Title() string { return s.title }

func (s *pickerScreen) Keys() []key.Binding {
	switch {
	case s.bp.Editing():
		return s.bp.EditorKeys()
	case s.err != nil:
		return []key.Binding{keyRetry, core.KeyBack}
	case !s.loaded:
		return []key.Binding{core.KeyBack}
	}
	if s.list.Filtering() {
		return s.list.FilterEditKeys()
	}
	return []key.Binding{widgets.KeySelect, core.KeyFilter, keySave, core.KeyBack}
}

// FullHelp groups every picker key for the help panel. It is nil while
// loading, on an error, or while a text field has focus.
func (s *pickerScreen) FullHelp() []core.HelpGroup {
	if s.bp.Editing() || s.err != nil || !s.loaded || s.list.Filtering() {
		return nil
	}
	return []core.HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{core.KeyUp, core.KeyDown}},
		{Title: "Selection", Keys: s.list.SelectKeys()},
		{Title: "Filter", Keys: append(s.list.FilterKeys(), catalog.KeyBranch, catalog.KeyEditor)},
		{Title: "General", Keys: []key.Binding{keySave, core.KeyBack}},
	}
}

var keyRetry = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "retry"))

func (s *pickerScreen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		s.list.SetSize(s.w, max(s.h, 1))
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
	case tea.KeyMsg:
		return s.key(m)
	}
	return s, nil
}

func (s *pickerScreen) key(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	if s.bp.Editing() {
		s.sync()
		if _, changed := s.bp.HandleKey(m); changed {
			s.rebuild()
		}
		return s, nil
	}
	if s.loaded && s.list.Filtering() {
		return s, s.list.Update(m)
	}
	switch {
	case key.Matches(m, core.KeyBack):
		return s, core.Cmd(core.Pop{})
	case s.err != nil && key.Matches(m, keyRetry):
		s.seq++
		s.loading, s.err = true, nil
		return s, tea.Batch(s.load.Start(), s.loadCatalog(s.seq))
	case !s.loaded:
		return s, nil
	case key.Matches(m, keySave):
		if err := s.onSave(s.result()); err != nil {
			s.notice = "could not save: " + err.Error()
			return s, nil
		}
		return s, core.Cmd(core.Pop{})
	}
	s.sync() // before the filter toggle can hide selected rows
	if handled, changed := s.bp.HandleKey(m); handled {
		if changed {
			s.rebuild()
		}
		return s, nil
	}
	return s, s.list.Update(m)
}

func (s *pickerScreen) View() string {
	switch {
	case s.err != nil:
		return s.theme.ErrorView("Could not load jobs", s.err, "press r to retry, esc to go back")
	case !s.loaded:
		return s.load.Modal("Loading jobs…", s.w, s.h)
	case s.bp.Editing():
		return s.bp.EditorView()
	}
	s.sync()
	return s.list.View()
}

// Subtitle is the count and filter line shown next to the title.
func (s *pickerScreen) Subtitle() string {
	if s.err != nil || !s.loaded || s.bp.Editing() {
		return ""
	}
	s.sync()
	n := 0
	for _, v := range s.chosen {
		if v {
			n++
		}
	}
	_, total := s.list.JobCounts()
	head := fmt.Sprintf("%d jobs · %d selected · %s", total, n, s.bp.Status())
	if s.bp.Notice() != "" {
		head += " · " + s.bp.Notice()
	}
	if s.notice != "" {
		head += " · " + s.notice
	}
	return head
}
