// Package widgets holds reusable UI sub-models that screens embed.
package widgets

import (
	"regexp"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// Item is one row of a List.
type Item struct {
	Key       string      // unique id, e.g. the job full path; also what the filter matches
	Label     string      // text shown; defaults to Key when empty
	Branch    string      // shown in the branch column (Label is then the Multibranch Project)
	Badge     core.Status // shown after the label when ShowBadge is set
	ShowBadge bool
	Missing   bool // renders a MISSING warning badge; the item cannot be selected
	Disabled  bool // dim, badge-less, non-selectable row (e.g. a placeholder)
}

// Options configure a List.
type Options struct {
	// Reorderable enables shift+up / shift+down to move the cursor item.
	Reorderable bool
}

var (
	// KeySelect toggles the row under the cursor; screens show it in their
	// help bar.
	KeySelect   = key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select"))
	keyAll      = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all"))
	keyNone     = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "clear selection"))
	keyStatus   = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "status filter"))
	keyMoveUp   = key.NewBinding(key.WithKeys("shift+up"), key.WithHelp("shift+↑", "move up"))
	keyMoveDown = key.NewBinding(key.WithKeys("shift+down"), key.WithHelp("shift+↓", "move down"))

	// shown while the filter box has focus
	keyFilterDone  = key.NewBinding(key.WithKeys("enter", "down"), key.WithHelp("enter/↓", "go to list"))
	keyFilterClear = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter"))
)

// List is a filterable multi-select list. It is a sub-model, not a
// core.Screen: embed it and forward messages to Update.
type List struct {
	opts     Options
	theme    core.Theme
	items    []Item
	selected map[string]bool
	filter   string
	re       *regexp.Regexp // compiled filter, cached per filter text
	reFor    string
	statusOn bool        // a status filter is active
	status   core.Status // the status shown while statusOn
	editing  bool        // filter box has focus
	cursor   int         // index into the visible slice
	offset   int         // first visible row shown
	w, h     int
}

// NewList returns an empty List.
func NewList(theme core.Theme, opts Options) *List {
	return &List{opts: opts, theme: theme, selected: map[string]bool{}, w: 80, h: 10}
}

// SetItems replaces the items. Selections are kept for keys that still exist
// and are not Missing.
func (l *List) SetItems(items []Item) {
	l.items = append([]Item(nil), items...)
	valid := map[string]bool{}
	for _, it := range l.items {
		if !it.Missing && !it.Disabled && l.selected[it.Key] {
			valid[it.Key] = true
		}
	}
	l.selected = valid
	l.clamp()
}

// Items returns a copy of all items in current order (unfiltered).
func (l *List) Items() []Item { return append([]Item(nil), l.items...) }

// Select marks keys selected (unknown and Missing keys are ignored).
func (l *List) Select(keys ...string) {
	for _, k := range keys {
		for _, it := range l.items {
			if it.Key == k && !it.Missing && !it.Disabled {
				l.selected[k] = true
			}
		}
	}
}

// SetSize sets the total area (width, rows) the list may draw, including the
// filter row.
func (l *List) SetSize(w, h int) {
	l.w, l.h = w, h
	l.clamp()
}

// Selected returns the selected keys in current list order, including those
// hidden by the filter.
func (l *List) Selected() []string {
	var out []string
	for _, it := range l.items {
		if l.selected[it.Key] {
			out = append(out, it.Key)
		}
	}
	return out
}

// Filtering reports whether the filter box has focus; screens should not
// treat esc, q or letters as their own while it is true.
func (l *List) Filtering() bool { return l.editing }

// Filter returns the current filter text.
func (l *List) Filter() string { return l.filter }

// Len returns the number of items matching the filter.
func (l *List) Len() int { return len(l.visible()) }

// Total returns the number of items regardless of filter.
func (l *List) Total() int { return len(l.items) }

// Cursor returns the item under the cursor, if any.
func (l *List) Cursor() (Item, bool) {
	v := l.visible()
	if l.cursor < 0 || l.cursor >= len(v) {
		return Item{}, false
	}
	return l.items[v[l.cursor]], true
}

// Keys returns bindings for the help bar.
func (l *List) Keys() []key.Binding {
	ks := append(l.FilterKeys(), l.SelectKeys()...)
	return append(ks, l.MoveKeys()...)
}

// SelectKeys, FilterKeys and MoveKeys are Keys split into groups for the
// help panel. MoveKeys is empty unless the list is Reorderable.
func (l *List) SelectKeys() []key.Binding { return []key.Binding{KeySelect, keyAll, keyNone} }
func (l *List) FilterKeys() []key.Binding { return []key.Binding{core.KeyFilter, keyStatus} }
func (l *List) MoveKeys() []key.Binding {
	if !l.opts.Reorderable {
		return nil
	}
	return []key.Binding{keyMoveUp, keyMoveDown}
}

// FilterEditKeys are the keys that work while the filter box has focus
// (every other key is typed into it).
func (l *List) FilterEditKeys() []key.Binding {
	return []key.Binding{keyFilterDone, keyFilterClear}
}

// visible returns indices into items that match the filter.
func (l *List) visible() []int {
	if l.reFor != l.filter {
		l.re, l.reFor = compileFilter(l.filter), l.filter
	}
	out := make([]int, 0, len(l.items))
	for i, it := range l.items {
		if l.re != nil && !l.re.MatchString(it.Key) {
			continue
		}
		if l.statusOn {
			if st, ok := itemStatus(it); !ok || st != l.status {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

// statusOrder is the order the status filter cycles through.
var statusOrder = []core.Status{
	core.StatusFailed, core.StatusUnstable, core.StatusRunning, core.StatusQueued,
	core.StatusSuccess, core.StatusAborted, core.StatusUnknown, core.StatusMissing,
}

// StatusName is the lower-case name of a status, as shown in the filter row.
func StatusName(s core.Status) string {
	switch s {
	case core.StatusSuccess:
		return "success"
	case core.StatusFailed:
		return "failed"
	case core.StatusRunning:
		return "running"
	case core.StatusQueued:
		return "queued"
	case core.StatusMissing:
		return "missing"
	case core.StatusUnstable:
		return "unstable"
	case core.StatusAborted:
		return "aborted"
	}
	return "unknown"
}

// itemStatus returns the status an item is filtered by. Placeholder rows
// (Disabled) have none; an item without a badge counts as unknown.
func itemStatus(it Item) (core.Status, bool) {
	switch {
	case it.Disabled:
		return 0, false
	case it.Missing:
		return core.StatusMissing, true
	case it.ShowBadge:
		return it.Badge, true
	}
	return core.StatusUnknown, true
}

// StatusFilter returns the active status filter's name, or "" when off.
func (l *List) StatusFilter() string {
	if !l.statusOn {
		return ""
	}
	return StatusName(l.status)
}

// cycleStatus advances off -> each status present among the items -> off.
func (l *List) cycleStatus() {
	present := map[core.Status]bool{}
	for _, it := range l.items {
		if st, ok := itemStatus(it); ok {
			present[st] = true
		}
	}
	var cycle []core.Status
	for _, st := range statusOrder {
		if present[st] {
			cycle = append(cycle, st)
		}
	}
	if !l.statusOn {
		if len(cycle) > 0 {
			l.statusOn, l.status = true, cycle[0]
		}
	} else {
		next := -1
		for i, st := range cycle {
			if st == l.status {
				next = i + 1
				break
			}
		}
		if next < 0 {
			next = 0 // current status vanished from the items: restart the cycle
		}
		if next >= len(cycle) {
			l.statusOn = false
		} else {
			l.status = cycle[next]
		}
	}
	l.cursor, l.offset = 0, 0
}

// filterRows is the height of the filter block: a blank line, the filter
// row, a blank line and the column titles (blank when nothing matches).
const filterRows = 4

func (l *List) rows() int { return max(l.h-filterRows, 1) }

func (l *List) clamp() {
	n := len(l.visible())
	l.cursor = max(min(l.cursor, n-1), 0)
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+l.rows() {
		l.offset = l.cursor - l.rows() + 1
	}
	l.offset = max(min(l.offset, max(n-l.rows(), 0)), 0)
}

// Update handles key messages. The returned command is currently always nil
// and exists so the widget can grow async behaviour without API churn.
func (l *List) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if l.editing {
		return l.updateFilter(km)
	}
	v := l.visible()
	switch {
	case key.Matches(km, core.KeyFilter):
		l.editing = true
		return nil
	case key.Matches(km, core.KeyUp):
		if l.cursor == 0 && km.String() == "up" {
			l.editing = true // reached the top: back to the filter box
			return nil
		}
		l.cursor--
	case key.Matches(km, core.KeyDown):
		l.cursor++
	case key.Matches(km, KeySelect):
		if it, ok := l.Cursor(); ok && !it.Missing && !it.Disabled {
			l.selected[it.Key] = !l.selected[it.Key]
			if !l.selected[it.Key] {
				delete(l.selected, it.Key)
			}
		}
	case key.Matches(km, keyAll):
		for _, i := range v {
			if !l.items[i].Missing && !l.items[i].Disabled {
				l.selected[l.items[i].Key] = true
			}
		}
	case key.Matches(km, keyStatus):
		l.cycleStatus()
	case key.Matches(km, keyNone):
		l.selected = map[string]bool{}
	case l.opts.Reorderable && key.Matches(km, keyMoveUp):
		l.move(v, -1)
	case l.opts.Reorderable && key.Matches(km, keyMoveDown):
		l.move(v, 1)
	}
	l.clamp()
	return nil
}

// move swaps the cursor item with its visible neighbour in direction d.
func (l *List) move(v []int, d int) {
	j := l.cursor + d
	if l.cursor >= len(v) || j < 0 || j >= len(v) {
		return
	}
	l.items[v[l.cursor]], l.items[v[j]] = l.items[v[j]], l.items[v[l.cursor]]
	l.cursor = j
}

func (l *List) updateFilter(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "enter":
		l.editing = false
		return nil
	case "esc":
		l.editing = false
		l.filter = ""
		l.clamp()
		return nil
	case "up":
		return nil
	case "down":
		l.editing = false // focus the list; space now selects
		l.clamp()
		return nil
	}
	switch km.Type {
	case tea.KeyBackspace:
		if r := []rune(l.filter); len(r) > 0 {
			l.filter = string(r[:len(r)-1])
		}
	case tea.KeySpace:
		l.filter += " "
	case tea.KeyRunes:
		l.filter += string(km.Runes)
	default:
		return nil
	}
	l.cursor, l.offset = 0, 0
	l.clamp()
	return nil
}

// View renders a blank line, the filter row, a blank line and the column
// titles, followed by the visible window of items.
func (l *List) View() string {
	var b strings.Builder
	b.WriteString("\n")
	switch {
	case l.editing || l.filter != "":
		cur := ""
		if l.editing {
			cur = "█"
		}
		hint := ""
		if l.editing {
			hint = l.theme.MutedTxt.Render("  ↓ to list")
		} else {
			hint = l.theme.MutedTxt.Render("  ↑ to edit")
		}
		b.WriteString(l.theme.Selected.Render("/ "+l.filter+cur) + hint)
	default:
		b.WriteString(l.theme.MutedTxt.Render("press / to filter, s for status"))
	}
	if l.statusOn {
		b.WriteString("  " + l.theme.Selected.Render("status: "+StatusName(l.status)) + l.theme.MutedTxt.Render(" (s to change)"))
	}
	v := l.visible()
	if len(v) == 0 {
		b.WriteString("\n\n\n" + l.theme.MutedTxt.Render("no matches"))
		return b.String()
	}
	end := min(l.offset+l.rows(), len(v))
	nameW, branchW, branchCol := l.columns()
	b.WriteString("\n\n" + l.header(nameW, branchW, branchCol))
	for r := l.offset; r < end; r++ {
		it := l.items[v[r]]
		box := "[ ]"
		switch {
		case it.Missing || it.Disabled:
			box = "[-]"
		case l.selected[it.Key]:
			box = "[x]"
		}
		// Columns: box, name, branch (only when some row has one), badge.
		name := cell(nameOf(it), nameW)
		line := box + " " + name
		switch {
		case branchCol && hasBadge(it):
			line += colGap + cell(it.Branch, branchW)
		case branchCol && it.Branch != "":
			line += colGap + it.Branch // nothing after it: no need to cut or pad
		}
		// Style the text once, so nested ANSI resets cannot cut a highlight short.
		style := lipgloss.NewStyle()
		switch {
		case it.Disabled:
			style = l.theme.MutedTxt
		case l.selected[it.Key] && !it.Missing:
			// Selected rows stand out in bold accent colour; unselecting
			// returns them to the plain style.
			style = l.theme.Selected
		}
		// Every row is a 2-column gutter, the columns, then colGap and the
		// badge. The cursor row is drawn in reverse video (readable on any
		// terminal theme) with one column of padding on each side; the
		// padding takes the place of the gutter's second column and of one
		// column of the gap before the badge, so every column stays where it
		// is on the other rows.
		prefix, gap := "  ", colGap
		plain := !it.Disabled && !(l.selected[it.Key] && !it.Missing) // no style of its own
		if r == l.cursor {
			style = style.Reverse(true).Bold(true)
			line = " " + line + " "
			prefix, gap, plain = l.theme.Selected.Render(">"), colGap[1:], false
		}
		if plain {
			// Plain rows draw the folder separators muted. A styled row keeps
			// them in its own style: a muted part inside it would reset, and
			// so cut short, the row's highlight.
			line = box + " " + strings.ReplaceAll(name, folderSep, l.theme.MutedTxt.Render(folderSep)) + line[len(box)+1+len(name):]
		} else {
			line = style.Render(line)
		}
		switch {
		case it.Missing:
			line += gap + l.theme.Badge(core.StatusMissing)
		case it.ShowBadge:
			line += gap + l.theme.Badge(it.Badge)
		}
		line = lipgloss.NewStyle().MaxWidth(max(l.w-lipgloss.Width(prefix), 1)).Render(line)
		b.WriteString("\n" + prefix + line)
	}
	return b.String()
}

// colGap separates the columns of a row.
const colGap = "  "

// folderSep replaces "/" between the folders of a name in the name column:
// "team/api/build" is shown as "team › api › build".
const folderSep = " › "

// nameOf is the name column's text for it: its label with the folders
// separated by folderSep. Only the display changes; Key keeps the "/".
func nameOf(it Item) string { return strings.ReplaceAll(labelOf(it), "/", folderSep) }

// Narrowest the name and branch columns get on a narrow terminal.
const (
	minNameW   = 12
	minBranchW = 8
)

func labelOf(it Item) string {
	if it.Label != "" {
		return it.Label
	}
	return it.Key
}

func hasBadge(it Item) bool { return it.Missing || it.ShowBadge }

// Column titles, shown muted on the row under the filter row.
const (
	headName   = "Pipeline"
	headBranch = "Branch"
	headStatus = "Status"
)

// header is the column titles, each over its column (the gutter and the
// box take the first 6 columns).
func (l *List) header(nameW, branchW int, branchCol bool) string {
	h := strings.Repeat(" ", 6) + cell(headName, nameW)
	if branchCol {
		h += colGap + cell(headBranch, branchW)
	}
	if slices.ContainsFunc(l.items, hasBadge) {
		h += colGap + headStatus
	}
	return l.theme.MutedTxt.Render(strings.TrimRight(h, " "))
}

// columns returns the widths of the name and branch columns, and whether
// there is a branch column at all (some item has a Branch). Widths fit the
// longest of all items, not only the visible ones, so columns do not move
// while scrolling or filtering. A branch with no badge after it (such as a
// placeholder's "(no branches)") does not widen its column. When a row would
// be wider than the list the name column shrinks first, then the branch
// column; the badge column never does.
func (l *List) columns() (nameW, branchW int, branchCol bool) {
	badgeW := 0
	for _, it := range l.items {
		nameW = max(nameW, lipgloss.Width(nameOf(it)))
		branchCol = branchCol || it.Branch != ""
		if !hasBadge(it) {
			continue
		}
		branchW = max(branchW, lipgloss.Width(it.Branch))
		st := it.Badge
		if it.Missing {
			st = core.StatusMissing
		}
		badgeW = max(badgeW, lipgloss.Width(l.theme.Badge(st)))
	}
	fixed := 2 + 4 // gutter, box and its space
	if badgeW > 0 {
		fixed += len(colGap) + max(badgeW, len(headStatus))
	}
	if branchCol {
		fixed += len(colGap)
	}
	// Columns are at least as wide as their titles.
	nameW = max(nameW, len(headName))
	if branchCol {
		branchW = max(branchW, len(headBranch))
	}
	avail := l.w - fixed
	if nameW+branchW > avail {
		nameW = max(avail-branchW, min(nameW, minNameW))
	}
	if nameW+branchW > avail && branchW > 0 {
		branchW = max(avail-nameW, min(branchW, minBranchW))
	}
	return nameW, branchW, branchCol
}

// cell is s cut with "…" or padded with spaces to exactly w columns.
func cell(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}
