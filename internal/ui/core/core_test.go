package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
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
