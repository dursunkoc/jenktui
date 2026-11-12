package palette

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type stub struct{ title string }

func (*stub) Init() tea.Cmd                           { return nil }
func (s *stub) Update(tea.Msg) (core.Screen, tea.Cmd) { return s, nil }
func (*stub) View() string                            { return "" }
func (s *stub) Title() string                         { return s.title }
func (*stub) Keys() []key.Binding                     { return nil }

type fixture struct {
	s       core.Screen
	opened  []string
	jobsFor []string
}

func newFixture(t *testing.T, load func() (Data, error)) *fixture {
	f := &fixture{}
	f.s = New(Deps{
		Parent: "team",
		Load:   load,
		OpenCollection: func(name string) tea.Cmd {
			f.opened = append(f.opened, name)
			return func() tea.Msg { return Opened{Screen: func() core.Screen { return &stub{"monitor " + name} }} }
		},
		OpenJob: func(j string) core.Screen { f.jobsFor = append(f.jobsFor, j); return &stub{"catalog " + j} },
	})
	f.s.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return f
}

func (f *fixture) run(cmd tea.Cmd) (nav []tea.Msg) {
	if cmd == nil {
		return
	}
	switch m := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			nav = append(nav, f.run(c)...)
		}
	case core.Replace, core.Pop, core.Push:
		nav = append(nav, m)
	default:
		var c tea.Cmd
		f.s, c = f.s.Update(m)
		nav = append(nav, f.run(c)...)
	}
	return
}

func (f *fixture) press(k tea.KeyMsg) []tea.Msg {
	var c tea.Cmd
	f.s, c = f.s.Update(k)
	return f.run(c)
}

func (f *fixture) typ(text string) {
	for _, r := range text {
		f.press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (f *fixture) view() string { return ansi.Strip(f.s.View()) }

func sample() (Data, error) {
	return Data{Collections: []string{"Nightly", "Calm"}, Jobs: []jenkins.Job{{FullName: "team/api/build"}, {FullName: "team/web/test"}}}, nil
}

func TestListsCollectionsAndJobsAndIsAPalette(t *testing.T) {
	f := newFixture(t, sample)
	f.run(f.s.Init())
	if _, ok := f.s.(core.PaletteScreen); !ok {
		t.Fatal("not marked as a palette")
	}
	v := f.view()
	for _, sub := range []string{"◆ Nightly", "◆ Calm", "api › build", "web › test"} {
		if !strings.Contains(v, sub) {
			t.Errorf("missing %q:\n%s", sub, v)
		}
	}
}

func TestEnterOnACollectionReplacesWithItsMonitor(t *testing.T) {
	f := newFixture(t, sample)
	f.run(f.s.Init())
	f.typ("night")
	nav := f.press(tea.KeyMsg{Type: tea.KeyEnter})
	if len(f.opened) != 1 || f.opened[0] != "Nightly" || len(nav) != 1 {
		t.Fatalf("opened=%v nav=%v", f.opened, nav)
	}
	if r, ok := nav[0].(core.Replace); !ok || r.Screen.Title() != "monitor Nightly" {
		t.Fatalf("nav = %#v", nav[0])
	}
}

func TestEnterOnAJobOpensTheCatalogForIt(t *testing.T) {
	f := newFixture(t, sample)
	f.run(f.s.Init())
	f.typ("web/test")
	nav := f.press(tea.KeyMsg{Type: tea.KeyEnter})
	if len(f.jobsFor) != 1 || f.jobsFor[0] != "team/web/test" {
		t.Fatalf("jobs = %v", f.jobsFor)
	}
	if r, ok := nav[0].(core.Replace); !ok || r.Screen.Title() != "catalog team/web/test" {
		t.Fatalf("nav = %#v", nav)
	}
}

func TestEscClearsTheFilterFirstThenLeaves(t *testing.T) {
	f := newFixture(t, sample)
	f.run(f.s.Init())
	f.typ("zz")
	if nav := f.press(tea.KeyMsg{Type: tea.KeyEsc}); len(nav) != 0 {
		t.Fatalf("first esc left: %v", nav)
	}
	if nav := f.press(tea.KeyMsg{Type: tea.KeyEsc}); len(nav) != 1 {
		t.Fatalf("second esc did not pop: %v", nav)
	} else if _, ok := nav[0].(core.Pop); !ok {
		t.Fatalf("nav = %#v", nav[0])
	}
}

func TestAKeyAfterEnterCancelsTheLateOpen(t *testing.T) {
	f := newFixture(t, sample)
	f.run(f.s.Init())
	f.typ("calm")
	var c tea.Cmd
	f.s, c = f.s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // reply not delivered yet
	reply := c()
	f.press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if _, c := f.s.Update(reply); c != nil {
		t.Fatal("a late reply replaced the palette after a key")
	}
}

func TestLoadErrorAndWarning(t *testing.T) {
	f := newFixture(t, func() (Data, error) { return Data{}, errors.New("bad toml") })
	f.run(f.s.Init())
	if !strings.Contains(f.view(), "bad toml") {
		t.Fatalf("view:\n%s", f.view())
	}
	f = newFixture(t, func() (Data, error) { return Data{Collections: []string{"A"}, Warn: "jobs unavailable: 500"}, nil })
	f.run(f.s.Init())
	if v := f.view(); !strings.Contains(v, "◆ A") || !strings.Contains(v, "jobs unavailable") {
		t.Fatalf("view:\n%s", v)
	}
}
