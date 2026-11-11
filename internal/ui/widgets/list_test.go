package widgets

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/muesli/termenv"
)

func k(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "shift+up":
		return tea.KeyMsg{Type: tea.KeyShiftUp}
	case "shift+down":
		return tea.KeyMsg{Type: tea.KeyShiftDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func newList(opts Options) *List {
	l := NewList(core.NewTheme(), opts)
	l.SetItems([]Item{{Key: "team/app/build"}, {Key: "team/app/deploy"}, {Key: "Other/lib"}, {Key: "gone/job", Missing: true}})
	return l
}

func run(l *List, keys ...string) {
	for _, s := range keys {
		l.Update(k(s))
	}
}

func TestListKeys(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		keys []string
		want []string
	}{
		{"toggle", Options{}, []string{"space"}, []string{"team/app/build"}},
		{"toggle twice", Options{}, []string{"space", "space"}, nil},
		{"move and toggle", Options{}, []string{"down", "down", "space"}, []string{"Other/lib"}},
		{"missing not selectable", Options{}, []string{"down", "down", "down", "space"}, nil},
		{"select all skips missing", Options{}, []string{"a"}, []string{"team/app/build", "team/app/deploy", "Other/lib"}},
		{"clear", Options{}, []string{"a", "n"}, nil},
		{"filter case-insensitive then all", Options{}, []string{"/", "T", "E", "A", "M", "enter", "a"}, []string{"team/app/build", "team/app/deploy"}},
		{"selection kept when hidden", Options{}, []string{"space", "/", "l", "i", "b", "enter", "space"}, []string{"team/app/build", "Other/lib"}},
		{"filter matches full path", Options{}, []string{"/", "a", "p", "p", "/", "d", "enter", "space"}, []string{"team/app/deploy"}},
		{"space types in filter", Options{}, []string{"/", "space", "esc", "space"}, []string{"team/app/build"}},
		{"esc clears filter", Options{}, []string{"/", "l", "i", "b", "esc", "a"}, []string{"team/app/build", "team/app/deploy", "Other/lib"}},
		{"reorder off by default", Options{}, []string{"shift+down", "a"}, []string{"team/app/build", "team/app/deploy", "Other/lib"}},
		{"reorder down", Options{Reorderable: true}, []string{"shift+down", "a"}, []string{"team/app/deploy", "team/app/build", "Other/lib"}},
		{"reorder up from bottom", Options{Reorderable: true}, []string{"down", "down", "shift+up", "shift+up", "a"}, []string{"Other/lib", "team/app/build", "team/app/deploy"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := newList(tc.opts)
			run(l, tc.keys...)
			if got := l.Selected(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Selected() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFilteringFlag(t *testing.T) {
	l := newList(Options{})
	run(l, "/")
	if !l.Filtering() {
		t.Fatal("expected filtering")
	}
	run(l, "enter")
	if l.Filtering() || l.Len() != 4 {
		t.Fatalf("filtering=%v len=%d", l.Filtering(), l.Len())
	}
	run(l, "/", "o", "t", "h", "enter")
	if l.Len() != 1 || l.Total() != 4 {
		t.Fatalf("len=%d total=%d", l.Len(), l.Total())
	}
}

func TestViewportScroll(t *testing.T) {
	l := NewList(core.NewTheme(), Options{})
	var items []Item
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8"} {
		items = append(items, Item{Key: n})
	}
	l.SetItems(items)
	l.SetSize(40, 4) // filter row + 3 items
	if v := l.View(); !strings.Contains(v, "a1") || strings.Contains(v, "a4") {
		t.Fatalf("initial view:\n%s", v)
	}
	run(l, "down", "down", "down", "down")
	v := l.View()
	if strings.Contains(v, "a1") || !strings.Contains(v, "a5") || strings.Count(v, "\n") != 3 {
		t.Fatalf("scrolled view:\n%s", v)
	}
	run(l, "up", "up", "up", "up")
	if !strings.Contains(l.View(), "a1") {
		t.Fatal("did not scroll back")
	}
}

func TestMissingBadgeAndSetItems(t *testing.T) {
	l := newList(Options{})
	if v := l.View(); !strings.Contains(v, "MISSING") || !strings.Contains(v, "[-]") {
		t.Fatalf("missing not rendered:\n%s", v)
	}
	l.Select("team/app/build", "gone/job", "nope")
	l.SetItems([]Item{{Key: "team/app/build"}, {Key: "gone/job", Missing: true}})
	if got := l.Selected(); !reflect.DeepEqual(got, []string{"team/app/build"}) {
		t.Fatalf("got %v", got)
	}
}

func TestDisabledItemNotSelectable(t *testing.T) {
	l := NewList(core.NewTheme(), Options{})
	l.SetItems([]Item{{Key: "a"}, {Key: "b", Label: "b (none)", Disabled: true}})
	l.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}) // select all
	l.Select("b")
	if got := l.Selected(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("selected = %v", got)
	}
	l.Update(tea.KeyMsg{Type: tea.KeyDown})
	l.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if got := l.Selected(); len(got) != 1 {
		t.Fatalf("space selected disabled row: %v", got)
	}
}

func TestFilterBoxAndListFocus(t *testing.T) {
	l := newList(Options{})
	run(l, "/", "a", "p", "p")
	// space types into the filter box while it has focus
	run(l, "space")
	if l.Filter() != "app " || !l.Filtering() {
		t.Fatalf("space should type into the filter, got %q editing=%v", l.Filter(), l.Filtering())
	}
	run(l, "down") // focus moves into the list, filter text stays applied
	if l.Filtering() || l.Filter() != "app " {
		t.Fatalf("down should focus the list: %q editing=%v", l.Filter(), l.Filtering())
	}
	run(l, "space") // now space selects the first match
	if got := l.Selected(); !reflect.DeepEqual(got, []string{"team/app/build"}) || l.Filter() != "app " {
		t.Fatalf("selected=%v filter=%q", got, l.Filter())
	}
	run(l, "up") // already on the first row: back to the filter box
	if !l.Filtering() {
		t.Fatal("up at the top should focus the filter box")
	}
	run(l, "space")
	if l.Filter() != "app  " {
		t.Fatalf("space should type again, got %q", l.Filter())
	}
}

func TestStatusFilterCycle(t *testing.T) {
	l := NewList(core.NewTheme(), Options{})
	l.SetItems([]Item{
		{Key: "a/ok1", Badge: core.StatusSuccess, ShowBadge: true},
		{Key: "a/bad", Badge: core.StatusFailed, ShowBadge: true},
		{Key: "b/ok2", Badge: core.StatusSuccess, ShowBadge: true},
		{Key: "b/run", Badge: core.StatusRunning, ShowBadge: true},
		{Key: "gone", Missing: true},
		{Key: "app (no matching branches)", Disabled: true},
	})
	keysOf := func() []string {
		var out []string
		for _, i := range l.visible() {
			out = append(out, l.items[i].Key)
		}
		return out
	}
	if len(keysOf()) != 6 || l.StatusFilter() != "" {
		t.Fatalf("starts unfiltered: %v", keysOf())
	}
	want := []struct {
		name string
		keys []string
	}{
		{"failed", []string{"a/bad"}},
		{"running", []string{"b/run"}},
		{"success", []string{"a/ok1", "b/ok2"}},
		{"missing", []string{"gone"}},
	}
	for _, w := range want {
		run(l, "s")
		if l.StatusFilter() != w.name || !reflect.DeepEqual(keysOf(), w.keys) {
			t.Fatalf("status %q: got %q %v want %v", w.name, l.StatusFilter(), keysOf(), w.keys)
		}
	}
	run(l, "s") // wraps back to off; placeholder rows return
	if l.StatusFilter() != "" || len(keysOf()) != 6 {
		t.Fatalf("expected off: %q %v", l.StatusFilter(), keysOf())
	}

	// combines with the text filter, and typing "s" in the filter box is text
	run(l, "s", "s", "s") // success
	run(l, "/", "o", "k", "2")
	if !reflect.DeepEqual(keysOf(), []string{"b/ok2"}) {
		t.Fatalf("status+text: %v", keysOf())
	}
	run(l, "s")
	if l.Filter() != "ok2s" || l.StatusFilter() != "success" {
		t.Fatalf("s inside the filter box must be text: %q %q", l.Filter(), l.StatusFilter())
	}
}

// The row under the cursor is drawn in reverse video (SGR 7); other rows are not.
func TestCursorRowHighlighted(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	l := newList(Options{})
	rows := strings.Split(l.View(), "\n") // blank, filter row, blank, items
	if !strings.Contains(rows[3], "\x1b[7") && !strings.Contains(rows[3], ";7") {
		t.Fatalf("cursor row not reversed: %q", rows[3])
	}
	if strings.Contains(rows[4], "\x1b[7") || strings.Contains(rows[4], ";7m") {
		t.Fatalf("non-cursor row should not be reversed: %q", rows[4])
	}
	run(l, "down")
	rows = strings.Split(l.View(), "\n")
	if strings.Contains(rows[3], "\x1b[7") || (!strings.Contains(rows[4], "\x1b[7") && !strings.Contains(rows[4], ";7")) {
		t.Fatalf("highlight should follow the cursor: %q / %q", rows[3], rows[4])
	}
}

// col is the column where sub starts in row (rune columns, ANSI stripped).
func col(row, sub string) int {
	i := strings.Index(row, sub)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(row[:i])
}

func rowsOf(l *List) []string {
	return strings.Split(ansi.Strip(l.View()), "\n")[filterRows:]
}

// Name, branch and badge each sit in their own column on every row, wherever
// the cursor is and whichever rows are selected; a placeholder's reason does
// not widen the branch column.
func TestColumnsAligned(t *testing.T) {
	l := NewList(core.NewTheme(), Options{})
	l.SetItems([]Item{
		{Key: "lib", Label: "lib", Branch: "(no matching branches)", Disabled: true},
		{Key: "ui/develop", Label: "ui", Branch: "develop", Badge: core.StatusSuccess, ShowBadge: true},
		{Key: "ui/feature%2Fx", Label: "ui", Branch: "feature/x", Badge: core.StatusFailed, ShowBadge: true},
		{Key: "team/app/build", Label: "team/app/build", Badge: core.StatusRunning, ShowBadge: true},
		{Key: "gone/job", Label: "gone/job", Missing: true},
	})
	l.Select("ui/develop")
	for _, keys := range [][]string{nil, {"down"}, {"down"}, {"down"}, {"down"}} {
		run(l, keys...)
		rows := rowsOf(l)
		box, branch, badge := col(rows[1], "["), col(rows[1], "develop"), col(rows[1], "●")
		if box != 2 {
			t.Fatalf("box at column %d, want 2: %q", box, rows[1])
		}
		for i, r := range rows {
			if c := col(r, "["); c != box {
				t.Errorf("cursor %d: row %d box at %d, want %d: %q", l.cursor, i, c, box, r)
			}
			if c := col(r, "●"); i > 0 && c != badge {
				t.Errorf("cursor %d: row %d badge at %d, want %d: %q", l.cursor, i, c, badge, r)
			}
		}
		if c := col(rows[2], "feature/x"); c != branch {
			t.Errorf("cursor %d: branch column moved: %d vs %d", l.cursor, c, branch)
		}
		if c := col(rows[0], "(no matching"); c != branch {
			t.Errorf("cursor %d: placeholder reason at %d, want the branch column %d", l.cursor, c, branch)
		}
		if !strings.HasPrefix(rows[l.cursor], ">") {
			t.Errorf("cursor row lacks the > marker: %q", rows[l.cursor])
		}
	}
	// the branch column is as wide as the longest real branch, not the reason.
	if rows := rowsOf(l); col(rows[1], "●")-col(rows[1], "develop") != len("feature/x")+len(colGap) {
		t.Errorf("branch column width: %q", rows[1])
	}
}

// Without branches there is no branch column: the badge follows the name
// column directly.
func TestNoBranchColumn(t *testing.T) {
	l := NewList(core.NewTheme(), Options{})
	l.SetItems([]Item{
		{Key: "a/b", Badge: core.StatusSuccess, ShowBadge: true},
		{Key: "a/longer", Badge: core.StatusFailed, ShowBadge: true},
	})
	rows := rowsOf(l)
	if got, want := col(rows[0], "●"), 2+4+len("a/longer")+len(colGap); got != want || col(rows[1], "●") != want {
		t.Fatalf("badge at %d, want %d:\n%s", got, want, strings.Join(rows, "\n"))
	}
}

// On a narrow list the name column is cut with … first; the badge stays.
func TestColumnsShrinkOnNarrowList(t *testing.T) {
	l := NewList(core.NewTheme(), Options{})
	long := strings.Repeat("x", 60)
	l.SetItems([]Item{{Key: long, Label: long, Branch: "main", Badge: core.StatusSuccess, ShowBadge: true}})
	l.SetSize(50, 10)
	row := rowsOf(l)[0]
	if ansi.StringWidth(row) > 50 || !strings.Contains(row, "…") || !strings.Contains(row, "main") || !strings.Contains(row, "● SUCCESS") {
		t.Fatalf("narrow row: %q (%d wide)", row, ansi.StringWidth(row))
	}
}
