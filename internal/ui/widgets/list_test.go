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

// The cursor row must not shift its text or badge: on every row the box
// starts in the same column and the badge is one column after the label.
func TestCursorRowKeepsAlignment(t *testing.T) {
	l := NewList(core.NewTheme(), Options{})
	l.SetItems([]Item{
		{Key: "team/app/build", Badge: core.StatusSuccess, ShowBadge: true},
		{Key: "team/app/deploy", Badge: core.StatusFailed, ShowBadge: true},
		{Key: "gone/job", Missing: true},
	})
	for _, keys := range [][]string{nil, {"down"}, {"down", "down"}} {
		run(l, keys...)
		rows := strings.Split(ansi.Strip(l.View()), "\n")[filterRows:]
		for i, row := range rows {
			if c := strings.Index(row, "["); c != 2 {
				t.Fatalf("after %v: row %d box at column %d, want 2: %q", keys, i, c, row)
			}
			label := strings.Fields(row[strings.Index(row, "]")+1:])[0]
			end := strings.Index(row, label) + len(label)
			if !strings.HasPrefix(row[end:], " ● ") && !strings.HasPrefix(row[end:], " ") {
				t.Fatalf("after %v: row %d badge not one column after the label: %q", keys, i, row)
			}
			if strings.HasPrefix(row[end:], "  ") {
				t.Fatalf("after %v: row %d has two spaces before the badge: %q", keys, i, row)
			}
		}
		if !strings.HasPrefix(rows[l.cursor], ">") {
			t.Fatalf("after %v: cursor row lacks the > marker: %q", keys, rows[l.cursor])
		}
	}
}
