package collections

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
)

// SelectionName names the Run Monitor that takes builds triggered from jobs
// that no single folder holds together.
const SelectionName = "Selection"

var (
	keyNew      = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new folder"))
	keyNewTop   = key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "new top-level folder"))
	keyEdit     = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit jobs"))
	keyRename   = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rename"))
	keyDelete   = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete / remove"))
	keyYes      = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes"))
	keyNo       = key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n/esc", "no"))
	keyMonitor  = key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "monitor"))
	keyConfirm  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "ok"))
	keyCancel   = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	keyExpand   = key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand"))
	keyInto     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "go into folder"))
	keyCollapse = key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse"))
	keyMove     = key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "move to…"))
	keyCopy     = key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "copy to…"))
	keyTrigger  = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "trigger"))
	keyRefresh  = key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh"))
	keyUp1      = key.NewBinding(key.WithKeys("shift+up"), key.WithHelp("shift+↑", "move up"))
	keyDown1    = key.NewBinding(key.WithKeys("shift+down"), key.WithHelp("shift+↓", "move down"))
	keyFilterOK = key.NewBinding(key.WithKeys("enter", "down"), key.WithHelp("enter/↓", "go to list"))
	keyFilterNo = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter"))
	keyOpenJob  = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser"))
)

type mode int

const (
	modeList   mode = iota
	modeNew         // typing the name of a new folder
	modeRename      // typing a folder's new name
	modeDelete      // confirming the deletion of a folder
	modeRemove      // confirming the removal of a job from its folder
)

const (
	colGap    = "  "
	folderSep = " › "
)

// trow is one line of the tree: a folder, or a job inside the folder at path.
type trow struct {
	folder  bool
	path    string // a folder's own path; for a job, the folder that holds it
	key     string // a job's full name
	depth   int
	name    string
	branch  string
	badge   core.Status
	missing bool
	folders int // a folder's sub-folders, at any depth
	jobs    int // a folder's jobs, in its whole subtree
	open    bool
}

func (r trow) id() string {
	if r.folder {
		return "f:" + strings.ToLower(r.path)
	}
	return "j:" + strings.ToLower(r.path) + "\x00" + r.key
}

// treeScreen is the Collections screen: every folder with its sub-folders and
// jobs, expandable in place.
type treeScreen struct {
	*env
	cs       *config.Collections
	err      error
	rows     []trow
	cursor   int
	focus    []string // folders gone into with f, outermost first; the tree shows only what is in the last
	cursorID string   // identity of the row the cursor is on, to find it again after a rebuild
	offset   int
	selected map[string]bool // job full names
	expanded map[string]bool // lower-cased folder paths
	mode     mode
	input    textinput.Model
	parent   string // new folder: where it goes; rename/delete: the folder; remove: the job's folder
	rmKey    string // remove: the job
	filter   textinput.Model
	editing  bool // the filter box has focus
	cat      jenkins.Catalog
	jobs     map[string]jenkins.Job
	loaded   bool
	loading  bool
	loadErr  error
	seq      int
	notice   string
	w, h     int
}

func newTree(e *env) *treeScreen {
	s := &treeScreen{env: e, selected: map[string]bool{}, expanded: e.deps.Expanded, jobs: map[string]jenkins.Job{}, w: 80, h: 20}
	if s.expanded == nil {
		s.expanded = map[string]bool{}
	}
	s.filter = textinput.New()
	s.filter.Prompt = "/ "
	s.filter.PromptStyle, s.filter.TextStyle = e.theme.Selected, e.theme.Selected
	s.filter.CharLimit = 0
	s.reload()
	return s
}

func (s *treeScreen) reload() {
	cs, err := config.LoadCollections(s.deps.Path)
	s.cs, s.err = cs, err
	if cs == nil {
		s.cs = &config.Collections{}
	}
	s.build()
}

func lower(p string) string { return strings.ToLower(p) }

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + config.Sep + name
}

// jobRow is how a job is shown: its name and branch, status and missing mark.
func (s *treeScreen) jobRow(k string) (name, branch string, st core.Status, missing bool) {
	j, ok := s.jobs[k]
	if !ok {
		j = jenkins.Job{FullName: k}
	}
	it := catalog.Row(j, s.deps.Parent, badge(j.Color))
	if ok {
		st = it.Badge
	}
	return strings.ReplaceAll(it.Label, "/", folderSep), it.Branch, st, s.loaded && !ok
}

func (s *treeScreen) matchesJob(k, f string) bool {
	n, b, _, _ := s.jobRow(k)
	return strings.Contains(lower(n+" "+b+" "+k), f)
}

// hasMatchBelow reports whether anything under c matches the filter f.
func (s *treeScreen) hasMatchBelow(c *config.Collection, p, f string) bool {
	for _, k := range c.Jobs {
		if s.matchesJob(k, f) {
			return true
		}
	}
	for i := range c.Folders {
		sub := &c.Folders[i]
		if strings.Contains(lower(sub.Name), f) || s.hasMatchBelow(sub, joinPath(p, sub.Name), f) {
			return true
		}
	}
	return false
}

// build recomputes the visible rows from the file, the open folders and the
// filter, and keeps the cursor on the same row when it is still there.
func (s *treeScreen) build() {
	f := lower(strings.TrimSpace(s.filter.Value()))
	var out []trow
	jobRows := func(c *config.Collection, p string, depth int) {
		for _, k := range c.Jobs {
			if f != "" && !s.matchesJob(k, f) {
				continue
			}
			n, b, st, miss := s.jobRow(k)
			out = append(out, trow{path: p, key: k, depth: depth, name: n, branch: b, badge: st, missing: miss})
		}
	}
	var walk func(list []config.Collection, parent string, depth int)
	walk = func(list []config.Collection, parent string, depth int) {
		for i := range list {
			c := &list[i]
			p := joinPath(parent, c.Name)
			below := f != "" && s.hasMatchBelow(c, p, f)
			if f != "" && !below && !strings.Contains(lower(c.Name), f) {
				continue
			}
			open := s.expanded[lower(p)]
			if f != "" {
				open = below // while filtering, the way to a match is open
			}
			out = append(out, trow{folder: true, path: p, depth: depth, name: c.Name, folders: c.CountFolders(), jobs: len(c.Subtree()), open: open})
			if !open {
				continue
			}
			walk(c.Folders, p, depth+1)
			jobRows(c, p, depth+1)
		}
	}
	// A folder gone into since removed, renamed or moved: back out to the
	// nearest one that still exists.
	for len(s.focus) > 0 && s.cs.Get(s.focusPath()) == nil {
		s.focus = s.focus[:len(s.focus)-1]
	}
	if fp := s.focusPath(); fp == "" {
		walk(s.cs.Items, "", 0)
	} else {
		c := s.cs.Get(fp)
		walk(c.Folders, fp, 0)
		jobRows(c, fp, 0)
	}
	s.rows = out
	if i := slices.IndexFunc(out, func(r trow) bool { return r.id() == s.cursorID }); i >= 0 {
		s.cursor = i
	}
	s.cursor = max(min(s.cursor, len(out)-1), 0)
	s.syncCursor()
}

// focusPath is the folder gone into, "" for the whole tree.
func (s *treeScreen) focusPath() string {
	if len(s.focus) == 0 {
		return ""
	}
	return s.focus[len(s.focus)-1]
}

// newParent is the folder a new folder goes into when the cursor says
// nothing: the folder gone into, else the top level.
func (s *treeScreen) newParent() string { return s.focusPath() }

func (s *treeScreen) syncCursor() {
	if s.cursor < len(s.rows) {
		s.cursorID = s.rows[s.cursor].id()
	}
}

func (s *treeScreen) current() (trow, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return trow{}, false
	}
	return s.rows[s.cursor], true
}

func (s *treeScreen) setOpen(path string, v bool) { s.expanded[lower(path)] = v }

// openAncestors expands every folder above path.
func (s *treeScreen) openAncestors(path string) {
	names := config.SplitPath(path)
	for i := 1; i < len(names); i++ {
		s.setOpen(config.JoinPath(names[:i]...), true)
	}
}

// moveExpanded carries the open state of the folders at or under from to to.
func (s *treeScreen) moveExpanded(from, to string) {
	lf := lower(from)
	for k, v := range s.expanded {
		if k == lf || strings.HasPrefix(k, lf+config.Sep) {
			delete(s.expanded, k)
			if v {
				s.expanded[lower(to)+strings.TrimPrefix(k, lf)] = true
			}
		}
	}
}

func (s *treeScreen) dropExpanded(path string) {
	lp := lower(path)
	for k := range s.expanded {
		if k == lp || strings.HasPrefix(k, lp+config.Sep) {
			delete(s.expanded, k)
		}
	}
}

func (s *treeScreen) Init() tea.Cmd {
	if s.deps.Lister == nil {
		return nil
	}
	s.seq++
	s.loading = true
	return s.loadCatalog(s.seq)
}

// Busy is true while the catalog loads (its reply would be lost under the
// jump palette).
func (s *treeScreen) Busy() bool { return s.loading }

func (s *treeScreen) Title() string {
	t := s.titled("Collections")
	if fp := s.focusPath(); fp != "" {
		t += " · " + Breadcrumb(config.SplitPath(fp), max(s.w-len(t)-12, 20))
	}
	return t
}

// Breadcrumb joins folder names with " › ". When that is wider than max, the
// middle collapses to "…", keeping the first name and the last two.
func Breadcrumb(names []string, max int) string {
	const sep = " › "
	full := strings.Join(names, sep)
	if len(names) <= 3 || len([]rune(full)) <= max {
		return full
	}
	return names[0] + sep + "…" + sep + strings.Join(names[len(names)-2:], sep)
}

func (s *treeScreen) Keys() []key.Binding {
	switch {
	case s.mode == modeNew || s.mode == modeRename:
		return []key.Binding{keyConfirm, keyCancel}
	case s.mode == modeDelete || s.mode == modeRemove:
		return []key.Binding{keyYes, keyNo}
	case s.editing:
		return []key.Binding{keyFilterOK, keyFilterNo}
	}
	return []key.Binding{keyInto, keyNew, keyTrigger, keyMonitor, core.KeyFilter, core.KeyBack}
}

// FullHelp groups every key of the tree for the help panel. It is nil while a
// name or the filter is typed or a question waits.
func (s *treeScreen) FullHelp() []core.HelpGroup {
	if s.mode != modeList || s.editing {
		return nil
	}
	actions := []key.Binding{keyTrigger, keyMonitor, keyEdit, keyRefresh}
	if s.canOpen() {
		actions = append(actions, keyOpenJob)
	}
	return []core.HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{core.KeyUp, core.KeyDown, keyExpand, keyCollapse, keyInto}},
		{Title: "Selection", Keys: []key.Binding{widgets.KeySelect}},
		{Title: "Folders", Keys: []key.Binding{keyNew, keyNewTop, keyRename, keyDelete, keyMove, keyCopy, keyUp1, keyDown1}},
		{Title: "Actions", Keys: actions},
		{Title: "Filter", Keys: []key.Binding{core.KeyFilter}},
		{Title: "General", Keys: []key.Binding{core.KeyBack, core.KeyQuit}},
	}
}

func (s *treeScreen) canOpen() bool { return s.deps.Opener != nil && s.deps.JobURL != nil }

func (s *treeScreen) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = m.Width, m.Height
		return s, nil
	case loadedMsg:
		if m.seq != s.seq {
			return s, nil
		}
		s.loading, s.loadErr = false, m.err
		if m.err == nil {
			s.cat, s.loaded = m.cat, true
			s.jobs = map[string]jenkins.Job{}
			for _, j := range m.cat.Jobs {
				s.jobs[j.FullName] = j
			}
			s.build()
		}
		return s, nil
	case catalog.OpenedMsg:
		s.notice = catalog.OpenNotice(m)
		return s, nil
	case tea.KeyMsg:
		return s.key(m)
	}
	if s.mode == modeNew || s.mode == modeRename {
		var c tea.Cmd
		s.input, c = s.input.Update(msg)
		return s, c
	}
	return s, nil
}

// deleteQuestion states what deleting a folder takes with it.
func deleteQuestion(c config.Collection) string {
	if n := c.CountFolders(); n > 0 {
		return fmt.Sprintf("Delete %q with %s and %s?", c.Name, plural(n, "folder"), plural(len(c.Subtree()), "job"))
	}
	return fmt.Sprintf("Delete %q?", c.Name)
}

func (s *treeScreen) key(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	switch s.mode {
	case modeNew, modeRename:
		return s.inputKey(m)
	case modeDelete, modeRemove:
		return s.confirmKey(m)
	}
	if s.editing {
		return s.filterKey(m)
	}
	r, have := s.current()
	switch {
	case key.Matches(m, core.KeyBack):
		if s.filter.Value() != "" {
			s.filter.SetValue("")
			s.build()
			return s, nil
		}
		if n := len(s.focus); n > 0 { // out of the folder gone into, onto its row
			left := s.focus[n-1]
			s.focus = s.focus[:n-1]
			s.cursorID = trow{folder: true, path: left}.id()
			s.build()
			return s, nil
		}
		return s, core.Cmd(core.Pop{})
	case key.Matches(m, core.KeyQuit):
		return s, core.Cmd(core.Quit{})
	case key.Matches(m, core.KeyFilter):
		s.editing = true
		s.filter.Focus()
		return s, textinput.Blink
	case key.Matches(m, core.KeyUp):
		s.cursor = max(s.cursor-1, 0)
		s.syncCursor()
	case key.Matches(m, core.KeyDown):
		s.cursor = max(min(s.cursor+1, len(s.rows)-1), 0)
		s.syncCursor()
	case key.Matches(m, keyNewTop):
		return s.startNew(s.newParent())
	case !have && key.Matches(m, keyEdit) && s.focusPath() != "":
		return s.editJobs(s.focusPath())
	case key.Matches(m, keyNew):
		parent := s.newParent()
		if have {
			parent = r.path
		}
		return s.startNew(parent)
	case key.Matches(m, keyRefresh):
		if s.deps.Lister == nil {
			return s, nil
		}
		s.seq++
		s.loading, s.loadErr, s.notice = true, nil, ""
		return s, s.loadCatalog(s.seq)
	case !have:
	case key.Matches(m, keyInto):
		if r.folder {
			s.focus = append(s.focus, r.path)
			s.cursorID = ""
			s.cursor = 0
			s.build()
		}
	case key.Matches(m, keyExpand):
		switch {
		case !r.folder:
		case !r.open:
			s.setOpen(r.path, true)
			s.build()
		case s.cursor+1 < len(s.rows) && s.rows[s.cursor+1].depth > r.depth:
			s.cursor++
			s.syncCursor()
		}
	case key.Matches(m, keyCollapse):
		switch {
		case r.folder && r.open:
			s.setOpen(r.path, false)
			s.build()
		case r.folder && r.depth == 0:
		case r.folder:
			s.goTo(parentOf(r.path))
		default:
			s.goTo(r.path)
		}
	case key.Matches(m, widgets.KeySelect):
		s.toggleSelect(r)
	case key.Matches(m, keyEdit):
		return s.editJobs(r.path)
	case key.Matches(m, keyRename):
		if !r.folder {
			s.notice = "a job's name comes from Jenkins"
			return s, nil
		}
		s.mode, s.parent, s.notice = modeRename, r.path, ""
		s.input = newInput("collection name", r.name)
		return s, textinput.Blink
	case key.Matches(m, keyDelete):
		s.notice = ""
		if r.folder {
			s.mode, s.parent = modeDelete, r.path
		} else {
			s.mode, s.parent, s.rmKey = modeRemove, r.path, r.key
		}
	case key.Matches(m, keyMove):
		return s.transfer(r, false)
	case key.Matches(m, keyCopy):
		return s.transfer(r, true)
	case key.Matches(m, keyUp1):
		s.reorder(r, -1)
	case key.Matches(m, keyDown1):
		s.reorder(r, 1)
	case key.Matches(m, keyTrigger):
		return s.trigger(r)
	case key.Matches(m, keyMonitor):
		return s.monitor(r.path)
	case key.Matches(m, keyOpenJob):
		if !s.canOpen() || r.folder {
			return s, nil
		}
		if r.missing {
			s.notice = r.key + " is not in Jenkins"
			return s, nil
		}
		s.notice = ""
		return s, catalog.OpenCmd(s.deps.Opener, s.deps.JobURL(r.key))
	}
	return s, nil
}

func parentOf(path string) string {
	if i := strings.LastIndex(path, config.Sep); i >= 0 {
		return path[:i]
	}
	return ""
}

// goTo puts the cursor on the folder at path, if it is shown.
func (s *treeScreen) goTo(path string) {
	if i := slices.IndexFunc(s.rows, func(r trow) bool { return r.folder && lower(r.path) == lower(path) }); i >= 0 {
		s.cursor = i
		s.syncCursor()
	}
}

func (s *treeScreen) startNew(parent string) (core.Screen, tea.Cmd) {
	s.mode, s.parent, s.notice = modeNew, parent, ""
	s.input = newInput("folder name", "")
	return s, textinput.Blink
}

func (s *treeScreen) filterKey(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	switch {
	case key.Matches(m, keyFilterNo):
		s.editing = false
		s.filter.SetValue("")
		s.filter.Blur()
		s.build()
		return s, nil
	case key.Matches(m, keyFilterOK):
		s.editing = false
		s.filter.Blur()
		return s, nil
	}
	var c tea.Cmd
	s.filter, c = s.filter.Update(m)
	s.build()
	return s, c
}

func (s *treeScreen) toggleSelect(r trow) {
	if !r.folder {
		if r.missing {
			return
		}
		s.selected[r.key] = !s.selected[r.key]
		return
	}
	c := s.cs.Get(r.path)
	if c == nil {
		return
	}
	keys := s.selectable(c.Subtree())
	all := len(keys) > 0
	for _, k := range keys {
		all = all && s.selected[k]
	}
	for _, k := range keys {
		s.selected[k] = !all
	}
}

// selectable drops the jobs that no longer exist in Jenkins, which can never
// be selected.
func (s *treeScreen) selectable(keys []string) []string {
	if !s.loaded {
		return keys
	}
	return slices.DeleteFunc(slices.Clone(keys), func(k string) bool { _, ok := s.jobs[k]; return !ok })
}

// selection is the selected jobs that still exist as rows of the file, in the
// order the file lists them.
func (s *treeScreen) selection() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range s.cs.Paths() {
		for _, k := range s.cs.Get(p).Jobs {
			if s.selected[k] && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

func (s *treeScreen) inputKey(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	switch {
	case key.Matches(m, keyCancel):
		s.mode, s.notice = modeList, ""
		return s, nil
	case key.Matches(m, keyConfirm):
		name := strings.TrimSpace(s.input.Value())
		if err := config.ValidName(name); err != nil {
			s.notice = err.Error()
			return s, nil
		}
		if s.mode == modeRename {
			old := s.parent
			np := joinPath(parentOf(old), name)
			if err := s.mutate(func(cs *config.Collections) error { return cs.Rename(old, name) }); err != nil {
				s.notice = err.Error()
				return s, nil
			}
			s.moveExpanded(old, np)
			s.cursorID = trow{folder: true, path: np}.id()
			s.mode, s.notice = modeList, ""
			s.reload()
			if s.deps.Renamed != nil {
				s.deps.Renamed(old, np)
			}
			return s, nil
		}
		parent := s.parent
		if err := s.mutate(func(cs *config.Collections) error { return cs.AddIn(parent, name, nil) }); err != nil {
			s.notice = err.Error()
			return s, nil
		}
		np := joinPath(parent, name)
		if parent != "" {
			s.setOpen(parent, true)
			s.openAncestors(parent + config.Sep + name)
		}
		s.cursorID = trow{folder: true, path: np}.id()
		s.mode, s.notice = modeList, ""
		s.filter.SetValue("") // a filter could hide the new folder
		s.reload()
		return s, nil
	}
	var c tea.Cmd
	s.input, c = s.input.Update(m)
	return s, c
}

func (s *treeScreen) confirmKey(m tea.KeyMsg) (core.Screen, tea.Cmd) {
	switch {
	case key.Matches(m, keyYes):
		if s.mode == modeDelete {
			path := s.parent
			if err := s.mutate(func(cs *config.Collections) error { return cs.Delete(path) }); err != nil {
				s.notice = "could not delete: " + err.Error()
			} else {
				s.notice = fmt.Sprintf("deleted %q", path)
				s.dropExpanded(path)
				if s.deps.Deleted != nil {
					s.deps.Deleted(path)
				}
			}
		} else {
			folder, k := s.parent, s.rmKey
			err := s.mutate(func(cs *config.Collections) error {
				c := cs.Get(folder)
				if c == nil {
					return fmt.Errorf("collection %q not found", folder)
				}
				return cs.SetJobs(folder, slices.DeleteFunc(slices.Clone(c.Jobs), func(j string) bool { return j == k }))
			})
			if err != nil {
				s.notice = "could not remove: " + err.Error()
			} else {
				s.notice = "removed " + k
			}
		}
		s.mode = modeList
		s.reload()
	case key.Matches(m, keyNo):
		s.mode = modeList
	}
	return s, nil
}

func (s *treeScreen) editJobs(path string) (core.Screen, tea.Cmd) {
	c := s.cs.Get(path)
	if c == nil {
		return s, nil
	}
	s.notice = ""
	return s, core.Cmd(core.Push{Screen: newPicker(s.env, s.pickerTitle("Edit", path), c.Jobs, func(keys []string) error {
		err := s.mutate(func(cs *config.Collections) error { return cs.SetJobs(path, keys) })
		if err == nil {
			s.setOpen(path, true)
			s.openAncestors(path)
		}
		s.reload()
		return err
	})})
}

// reorder moves a folder among its sibling folders, or a job among its
// folder's jobs, one place; nothing crosses a folder boundary.
func (s *treeScreen) reorder(r trow, delta int) {
	var err error
	if r.folder {
		err = s.mutate(func(cs *config.Collections) error { return cs.MoveFolderBy(r.path, delta) })
	} else {
		err = s.mutate(func(cs *config.Collections) error {
			c := cs.Get(r.path)
			if c == nil {
				return fmt.Errorf("collection %q not found", r.path)
			}
			i := slices.Index(c.Jobs, r.key)
			if j := i + delta; i >= 0 && j >= 0 && j < len(c.Jobs) {
				return cs.MoveJob(r.path, i, j)
			}
			return nil
		})
	}
	if err != nil {
		s.notice = "could not reorder: " + err.Error()
	}
	s.reload()
}

// transfer pushes the folder picker to move (or copy) the selected jobs, the
// job under the cursor, or (move only) the folder under it.
func (s *treeScreen) transfer(r trow, keep bool) (core.Screen, tea.Cmd) {
	s.notice = ""
	sel := s.selection()
	verb := "Move"
	if keep {
		verb = "Copy"
	}
	switch {
	case len(sel) > 0 || !r.folder:
		keys, from := sel, ""
		if len(keys) == 0 {
			keys, from = []string{r.key}, r.path
		}
		return s, core.Cmd(core.Push{Screen: newDest(s.env, fmt.Sprintf("%s %s to", verb, plural(len(keys), "job")), false, nil, func(dest string) error {
			err := s.mutate(func(cs *config.Collections) error { return moveJobs(cs, keys, from, dest, keep) })
			if err == nil {
				s.setOpen(dest, true)
				s.openAncestors(dest)
				if len(sel) > 0 {
					clear(s.selected)
				}
				s.notice = fmt.Sprintf("%s %s to %s", strings.ToLower(verb)+"d", plural(len(keys), "job"), dest)
				s.reload()
			}
			return err
		})})
	case keep:
		s.notice = "select jobs to copy; folders cannot be copied"
		return s, nil
	}
	return s, core.Cmd(core.Push{Screen: newDest(s.env, "Move "+r.path+" to", true, subtreePaths(s.cs, r.path), func(dest string) error {
		var np string
		err := s.mutate(func(cs *config.Collections) (e error) {
			np, e = cs.MoveFolder(r.path, dest)
			return e
		})
		if err != nil {
			return err
		}
		s.moveExpanded(r.path, np)
		s.openAncestors(np)
		if dest != "" {
			s.setOpen(dest, true)
		}
		if s.deps.Deleted != nil {
			s.deps.Deleted(r.path) // its Run Monitors, and those under it, start fresh
		}
		s.cursorID = trow{folder: true, path: np}.id()
		s.notice = fmt.Sprintf("moved %q to %q", r.path, np)
		s.reload()
		return nil
	})})
}

// moveJobs moves or copies keys into dest. from names the one folder they
// leave; "" takes each job out of every folder that holds it (a selection).
func moveJobs(cs *config.Collections, keys []string, from, dest string, keep bool) error {
	if cs.Get(dest) == nil {
		return fmt.Errorf("collection %q not found", dest)
	}
	if from != "" {
		return cs.TransferJobs(from, dest, keys, keep)
	}
	moved := false
	for _, p := range cs.Paths() {
		if strings.EqualFold(p, dest) {
			continue
		}
		var own []string
		for _, k := range cs.Get(p).Jobs {
			if slices.Contains(keys, k) {
				own = append(own, k)
			}
		}
		if len(own) == 0 {
			continue
		}
		if err := cs.TransferJobs(p, dest, own, keep); err != nil {
			return err
		}
		moved = true
	}
	if !moved {
		return fmt.Errorf("choose a different folder")
	}
	return nil
}

// subtreePaths lists the folder at path and every folder under it.
func subtreePaths(cs *config.Collections, path string) map[string]bool {
	out := map[string]bool{}
	lp := lower(path)
	for _, p := range cs.Paths() {
		if lq := lower(p); lq == lp || strings.HasPrefix(lq, lp+config.Sep) {
			out[p] = true
		}
	}
	return out
}

// monitor pushes the Run Monitor of the folder at path over its whole subtree.
func (s *treeScreen) monitor(path string) (core.Screen, tea.Cmd) {
	if s.deps.Monitor == nil {
		s.notice = "monitor unavailable"
		return s, nil
	}
	c := s.cs.Get(path)
	if c == nil {
		return s, nil
	}
	s.notice = ""
	jobs := c.Subtree()
	var missing map[string]bool
	if s.loaded {
		missing = catalog.MissingOf(jobs, s.cat)
	}
	return s, core.Cmd(core.Push{Screen: s.deps.Monitor(path, jobs, missing, catalog.NamesOf(jobs, s.cat, s.deps.Parent))})
}

// target names the Run Monitor that takes builds of keys: the deepest folder
// whose subtree holds every one of them, else the Selection monitor.
func (s *treeScreen) target(keys []string) string {
	best, depth := "", -1
	for _, p := range s.cs.Paths() {
		sub := s.cs.Get(p).Subtree()
		if !slices.ContainsFunc(keys, func(k string) bool { return !slices.Contains(sub, k) }) {
			if d := strings.Count(p, config.Sep); d > depth {
				best, depth = p, d
			}
		}
	}
	if depth < 0 {
		return SelectionName
	}
	return best
}

// trigger pushes the Trigger screen for the selected jobs, else for the job
// under the cursor, else for the whole subtree of the folder under it.
func (s *treeScreen) trigger(r trow) (core.Screen, tea.Cmd) {
	keys, folder := s.selection(), ""
	switch {
	case len(keys) > 0:
	case !r.folder:
		keys, folder = []string{r.key}, r.path
	default:
		if c := s.cs.Get(r.path); c != nil {
			keys, folder = c.Subtree(), r.path
		}
	}
	var run, skipped []string
	for _, k := range keys {
		if _, ok := s.jobs[k]; s.loaded && !ok {
			skipped = append(skipped, k)
		} else {
			run = append(run, k)
		}
	}
	switch {
	case s.deps.Trigger == nil:
		s.notice = "triggering is not available"
		return s, nil
	case len(run) == 0:
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
	if folder == "" {
		folder = s.target(run)
	}
	known := run
	if c := s.cs.Get(folder); c != nil {
		known = append(c.Subtree(), run...)
	}
	return s, core.Cmd(core.Push{Screen: s.deps.Trigger(folder, run, catalog.NamesOf(known, s.cat, s.deps.Parent))})
}

// ---- view ----

func (s *treeScreen) selState(r trow) string {
	if !r.folder {
		switch {
		case r.missing:
			return "[-]"
		case s.selected[r.key]:
			return "[x]"
		}
		return "[ ]"
	}
	c := s.cs.Get(r.path)
	if c == nil {
		return "[ ]"
	}
	n := 0
	keys := s.selectable(c.Subtree())
	for _, k := range keys {
		if s.selected[k] {
			n++
		}
	}
	switch {
	case n == 0:
		return "[ ]"
	case n == len(keys):
		return "[x]"
	}
	return "[-]"
}

func (s *treeScreen) rowsHeight() int {
	h := s.h - 6
	if s.mode != modeList {
		h -= 3
	}
	return max(h, 3)
}

func (s *treeScreen) View() string {
	if s.err != nil {
		return s.theme.ErrorView("Could not read collections", s.err, "check "+s.deps.Path+", esc to go back")
	}
	var b strings.Builder
	b.WriteString("\n") // a blank line under the title
	switch s.mode {
	case modeNew:
		where := "at the top level"
		if s.parent != "" {
			where = "in " + s.parent
		}
		b.WriteString(s.theme.Title.Render("New folder "+where) + "\n" + s.input.View() + "\n\n")
	case modeRename:
		b.WriteString(s.theme.Title.Render("Rename folder") + "\n" + s.input.View() + "\n\n")
	case modeDelete:
		c := s.cs.Get(s.parent)
		q := fmt.Sprintf("Delete %q?", s.parent)
		if c != nil {
			q = deleteQuestion(*c)
		}
		b.WriteString(s.theme.Title.Render(q) + "\n" + s.theme.MutedTxt.Render("y to delete, n to keep") + "\n\n")
	case modeRemove:
		n, _, _, _ := s.jobRow(s.rmKey)
		b.WriteString(s.theme.Title.Render(fmt.Sprintf("Remove %s from %s?", n, s.parent)) + "\n" + s.theme.MutedTxt.Render("y to remove, n to keep") + "\n\n")
	}
	switch {
	case s.editing || s.filter.Value() != "":
		hint := s.theme.MutedTxt.Render("  ↑ to edit")
		if s.editing {
			hint = s.theme.MutedTxt.Render("  ↓ to list")
		}
		b.WriteString(s.filter.View() + hint)
	default:
		b.WriteString(s.theme.MutedTxt.Render("press / to filter"))
	}
	if len(s.rows) == 0 {
		msg := "No folders yet. Press N to create one."
		if s.focusPath() != "" {
			msg = "This folder is empty. Press n for a folder or e for jobs."
		}
		if s.filter.Value() != "" {
			msg = "no matches"
		}
		b.WriteString("\n\n" + s.theme.MutedTxt.Render(msg))
		return b.String()
	}
	// scroll so the cursor stays on screen
	rh := s.rowsHeight()
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+rh {
		s.offset = s.cursor - rh + 1
	}
	s.offset = max(min(s.offset, len(s.rows)-1), 0)
	end := min(s.offset+rh, len(s.rows))

	nameW, branchW, badgeW := 0, 0, 0
	for _, r := range s.rows {
		w := 2*r.depth + 2 + lipgloss.Width(r.name)
		nameW = max(nameW, w)
		if !r.folder {
			branchW = max(branchW, lipgloss.Width(r.branch))
			if r.missing {
				badgeW = max(badgeW, lipgloss.Width(s.theme.Badge(core.StatusMissing)))
			} else if r.badge != core.StatusUnknown || s.loaded {
				badgeW = max(badgeW, lipgloss.Width(s.theme.Badge(r.badge)))
			}
		}
	}
	avail := s.w - 6 // gutter and checkbox
	if badgeW > 0 {
		avail -= len(colGap) + badgeW
	}
	if branchW > 0 {
		avail -= len(colGap) + branchW
	}
	nameW = max(min(nameW, avail), 12)
	b.WriteString("\n")
	for i := s.offset; i < end; i++ {
		r := s.rows[i]
		b.WriteString("\n" + s.renderRow(r, i == s.cursor, nameW, branchW))
	}
	return b.String()
}

func (s *treeScreen) renderRow(r trow, cursor bool, nameW, branchW int) string {
	box := s.selState(r)
	marker := "  "
	if r.folder {
		marker = "▸ "
		if r.open {
			marker = "▾ "
		}
	}
	head := box + " " + cell(strings.Repeat("  ", r.depth)+marker+r.name, nameW)
	extra := "" // the folder's summary, or the job's branch
	if r.folder {
		switch {
		case r.folders > 0:
			extra = plural(r.folders, "folder") + " · " + plural(r.jobs, "job")
		case r.jobs > 0:
			extra = plural(r.jobs, "job")
		}
	} else if branchW > 0 {
		extra = cell(r.branch, branchW)
	}
	style := lipgloss.NewStyle()
	switch {
	case !r.folder && r.missing:
		style = s.theme.MutedTxt
	case box == "[x]":
		style = s.theme.Selected
	}
	prefix, gap := "  ", colGap
	var line string
	if cursor {
		// Reverse video is readable on any terminal theme; its padding takes
		// the place of the gutter's second column and of one column of the
		// gap, so the columns stay where they are on the other rows.
		plain := head
		if extra != "" {
			plain += colGap + extra
		}
		line = style.Reverse(true).Bold(true).Render(" " + plain + " ")
		prefix, gap = s.theme.Selected.Render(">"), colGap[1:]
	} else if r.folder {
		line = style.Render(head)
		if extra != "" {
			line += colGap + s.theme.MutedTxt.Render(extra)
		}
	} else {
		plain := head
		if extra != "" {
			plain += colGap + extra
		}
		line = style.Render(plain)
	}
	if !r.folder {
		switch {
		case r.missing:
			line += gap + s.theme.Badge(core.StatusMissing)
		case s.loaded:
			line += gap + s.theme.Badge(r.badge)
		}
	}
	return lipgloss.NewStyle().MaxWidth(max(s.w, 1)).Render(prefix + line)
}

func cell(str string, w int) string {
	str = ansi.Truncate(str, w, "…")
	return str + strings.Repeat(" ", max(w-lipgloss.Width(str), 0))
}

// Subtitle is the count line shown next to the title.
func (s *treeScreen) Subtitle() string {
	if s.err != nil {
		return ""
	}
	head := plural(len(s.cs.Paths()), "folder")
	if n := len(s.selection()); n > 0 {
		head += fmt.Sprintf(" · %d selected", n)
	}
	if s.loaded {
		seen, missing := map[string]bool{}, 0
		for _, p := range s.cs.Paths() {
			for _, k := range s.cs.Get(p).Jobs {
				if _, ok := s.jobs[k]; !ok && !seen[k] {
					seen[k] = true
					missing++
				}
			}
		}
		if missing > 0 {
			head += fmt.Sprintf(" · %d missing", missing)
		}
	}
	if s.loading {
		head += " · loading statuses…"
	}
	if s.loadErr != nil {
		head += " · statuses unavailable: " + ansi.Truncate(s.loadErr.Error(), 40, "…")
	}
	if s.notice != "" {
		head += " · " + s.notice
	}
	return head
}
