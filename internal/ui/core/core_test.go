package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type fake struct {
	name  string
	inits int
	size  tea.WindowSizeMsg
	got   []tea.Msg
}

func (f *fake) Init() tea.Cmd { f.inits++; return nil }
func (f *fake) Update(m tea.Msg) (Screen, tea.Cmd) {
	if s, ok := m.(tea.WindowSizeMsg); ok {
		f.size = s
	} else {
		f.got = append(f.got, m)
	}
	return f, nil
}
func (f *fake) View() string        { return "body-" + f.name }
func (f *fake) Title() string       { return f.name }
func (f *fake) Keys() []key.Binding { return []key.Binding{KeyBack, KeyQuit} }

func TestRouterNavigation(t *testing.T) {
	a, b, c := &fake{name: "a"}, &fake{name: "b"}, &fake{name: "c"}
	r := NewRouter(&Session{}, a)
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if a.size.Width != 80 || a.size.Height != 22 {
		t.Fatalf("body size = %+v", a.size)
	}
	r.Update(Push{b})
	if r.Depth() != 2 || r.Top() != Screen(b) || b.inits != 1 || b.size.Height != 22 {
		t.Fatalf("push failed: depth=%d inits=%d size=%+v", r.Depth(), b.inits, b.size)
	}
	r.Update(Replace{c})
	if r.Depth() != 2 || r.Top() != Screen(c) {
		t.Fatal("replace failed")
	}
	r.Update(Pop{})
	r.Update(Pop{}) // no-op at root
	if r.Depth() != 1 || r.Top() != Screen(a) {
		t.Fatal("pop failed")
	}
	r.Update("hello")
	if len(a.got) != 1 {
		t.Fatal("message not forwarded to top screen")
	}
	if _, cmd := r.Update(Quit{}); cmd == nil {
		t.Fatal("quit returned no cmd")
	}
	if _, cmd := r.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c returned no cmd")
	}
}

func TestRouterView(t *testing.T) {
	r := NewRouter(&Session{}, &fake{name: "a"})
	if v := r.View(); !strings.Contains(v, "too small") {
		t.Fatalf("zero size should be too small: %q", v)
	}
	r.Update(tea.WindowSizeMsg{Width: 59, Height: 30})
	if v := r.View(); !strings.Contains(v, "need at least 60x16") {
		t.Fatalf("narrow: %q", v)
	}
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	v := r.View()
	for _, want := range []string{"a", "body-a", "back", "quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if n := strings.Count(v, "\n") + 1; n != 24 {
		t.Errorf("view rows = %d, want 24", n)
	}
}

func TestBadgeAndErrorView(t *testing.T) {
	th := NewTheme()
	for s, want := range map[Status]string{
		StatusSuccess: "SUCCESS", StatusFailed: "FAILED", StatusRunning: "RUNNING",
		StatusQueued: "QUEUED", StatusUnknown: "UNKNOWN", StatusMissing: "MISSING",
	} {
		if !strings.Contains(th.Badge(s), want) {
			t.Errorf("badge %d missing %s", s, want)
		}
	}
	v := th.ErrorView("Login failed", errors.New("401"), "check your API token")
	for _, want := range []string{"Login failed", "401", "check your API token"} {
		if !strings.Contains(v, want) {
			t.Errorf("error view missing %q", want)
		}
	}
}

// helpFake is a FullHelper; typing toggles whether a text field has focus.
type helpFake struct {
	fake
	typing bool
}

func (f *helpFake) FullHelp() []HelpGroup {
	if f.typing {
		return nil
	}
	return []HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{KeyUp, KeyDown}},
		{Title: "General", Keys: []key.Binding{KeyBack, KeyQuit}},
	}
}

func (f *helpFake) Update(m tea.Msg) (Screen, tea.Cmd) { f.fake.Update(m); return f, nil }

func keyRune(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestHelpPanelOpensAndCloses(t *testing.T) {
	s := &helpFake{fake: fake{name: "a"}}
	r := NewRouter(&Session{}, s)
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if v := ansi.Strip(r.View()); !strings.Contains(v, "? all keys") || strings.Contains(v, "Navigation") {
		t.Fatalf("closed panel:\n%s", v)
	}
	r.Update(keyRune('?'))
	v := ansi.Strip(r.View())
	for _, want := range []string{"body-a", "Navigation", "General", "↑/k up", "esc back", "esc/? close help"} {
		if !strings.Contains(v, want) {
			t.Errorf("open panel missing %q:\n%s", want, v)
		}
	}
	if n := strings.Count(v, "\n") + 1; n != 24 {
		t.Errorf("view rows = %d, want 24", n)
	}
	// the panel takes every key; esc closes it without reaching the screen.
	r.Update(keyRune('x'))
	r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(s.got) != 0 {
		t.Fatalf("keys reached the screen behind the panel: %v", s.got)
	}
	if strings.Contains(ansi.Strip(r.View()), "Navigation") {
		t.Fatal("esc should close the panel")
	}
	r.Update(keyRune('?'))
	r.Update(keyRune('?'))
	if strings.Contains(ansi.Strip(r.View()), "Navigation") {
		t.Fatal("? should close the panel")
	}
	// navigation closes it too, so it never shows over another screen.
	r.Update(keyRune('?'))
	r.Update(Push{&fake{name: "b"}})
	r.Update(Pop{})
	if strings.Contains(ansi.Strip(r.View()), "Navigation") {
		t.Fatal("panel survived navigation")
	}
}

func TestHelpKeyIsTypedWhileTextFieldHasFocus(t *testing.T) {
	s := &helpFake{fake: fake{name: "a"}, typing: true}
	r := NewRouter(&Session{}, s)
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r.Update(keyRune('?'))
	if len(s.got) != 1 {
		t.Fatalf("? should reach the screen, got %v", s.got)
	}
	if v := ansi.Strip(r.View()); strings.Contains(v, "all keys") || strings.Contains(v, "Navigation") {
		t.Fatalf("no panel while typing:\n%s", v)
	}
	// a screen that is not a FullHelper also gets ?.
	f := &fake{name: "plain"}
	r = NewRouter(&Session{}, f)
	r.Update(keyRune('?'))
	if len(f.got) != 1 {
		t.Fatal("? should reach a plain screen")
	}
}

func TestHelpBarFitsWidth(t *testing.T) {
	keys := []key.Binding{KeyUp, KeyDown, KeyFilter, KeyEnter, KeyBack, KeyQuit}
	h := NewHelp()
	full := ansi.Strip(h.View(keys, true, 0))
	if !strings.HasSuffix(full, "? all keys") || strings.Contains(full, "…") {
		t.Fatalf("unlimited: %q", full)
	}
	for w := 20; w <= lipgloss.Width(full)+2; w++ {
		v := ansi.Strip(h.View(keys, true, w))
		if lipgloss.Width(v) > w {
			t.Fatalf("width %d: %q is %d wide", w, v, lipgloss.Width(v))
		}
		if !strings.HasSuffix(v, "? all keys") {
			t.Fatalf("width %d: ? all keys was cut: %q", w, v)
		}
		if lipgloss.Width(v) < lipgloss.Width(full) && !strings.Contains(v, "…") {
			t.Fatalf("width %d: dropped keys without …: %q", w, v)
		}
	}
	if v := ansi.Strip(h.View(keys, false, 30)); lipgloss.Width(v) > 30 || !strings.HasSuffix(v, "…") {
		t.Fatalf("no tail: %q", v)
	}
}

func TestHelpPanelColumnsFitWidth(t *testing.T) {
	groups := []HelpGroup{
		{Title: "Navigation", Keys: []key.Binding{KeyUp, KeyDown}},
		{Title: "Filter", Keys: []key.Binding{KeyFilter}},
		{Title: "Empty", Keys: []key.Binding{key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "off"), key.WithDisabled())}},
		{Title: "General", Keys: []key.Binding{KeyBack, KeyQuit}},
	}
	h := NewHelp()
	wide := ansi.Strip(h.Panel(groups, 120))
	if lipgloss.Height(wide) != 4 { // rule, title, two keys: all side by side
		t.Fatalf("wide panel should be one row of columns:\n%s", wide)
	}
	if strings.Contains(wide, "Empty") {
		t.Fatal("a group with only disabled keys must be skipped")
	}
	narrow := ansi.Strip(h.Panel(groups, 14))
	for _, line := range strings.Split(narrow, "\n") {
		if lipgloss.Width(line) > 14 {
			t.Fatalf("line wider than 14: %q\n%s", line, narrow)
		}
	}
	for _, want := range []string{"Navigation", "Filter", "General", "esc back", "q   quit"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("narrow panel missing %q:\n%s", want, narrow)
		}
	}
}

type subFake struct {
	fake
	sub string
}

func (f *subFake) Subtitle() string                   { return f.sub }
func (f *subFake) Update(m tea.Msg) (Screen, tea.Cmd) { f.fake.Update(m); return f, nil }

func TestHeaderShowsSubtitleOnTitleRow(t *testing.T) {
	s := &subFake{fake: fake{name: "Job Catalog · team"}, sub: "149 jobs · branches: all"}
	r := NewRouter(&Session{}, s)
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	rows := strings.Split(ansi.Strip(r.View()), "\n")
	if rows[0] != "Job Catalog · team   149 jobs · branches: all" {
		t.Fatalf("title row = %q", rows[0])
	}
	if n := len(rows); n != 24 {
		t.Fatalf("rows = %d, want 24", n)
	}

	// too long: the subtitle is cut with …, the title stays whole.
	s.sub = strings.Repeat("x", 100)
	rows = strings.Split(ansi.Strip(r.View()), "\n")
	if lipgloss.Width(rows[0]) > 80 || !strings.HasPrefix(rows[0], "Job Catalog · team   x") || !strings.HasSuffix(rows[0], "…") {
		t.Fatalf("long subtitle: %q", rows[0])
	}
	// no room at all: only the (cut) title.
	s.name = strings.Repeat("T", 90)
	rows = strings.Split(ansi.Strip(r.View()), "\n")
	if lipgloss.Width(rows[0]) > 80 || strings.Contains(rows[0], "x") {
		t.Fatalf("long title: %q", rows[0])
	}
	// empty subtitle: the title alone.
	s.name, s.sub = "Job Catalog", ""
	if rows = strings.Split(ansi.Strip(r.View()), "\n"); rows[0] != "Job Catalog" {
		t.Fatalf("empty subtitle: %q", rows[0])
	}
}
