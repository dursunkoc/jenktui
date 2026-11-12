package monitor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestErrorLineMatching(t *testing.T) {
	for _, l := range []string{"ERROR boom", "x FAILED", "FAILURE", "java.lang.NullPointerException", "Caused by: x", "[INFO] BUILD FAILURE", "npm ERR! code 1", "main.go:3: error: bad"} {
		if !isErrorLine(l) {
			t.Errorf("%q should match", l)
		}
	}
	for _, l := range []string{"all good", "error handling docs", "Error", "failed"} {
		if isErrorLine(l) {
			t.Errorf("%q should not match", l)
		}
	}
}

func TestLogErrorJumpWrapsAndCounts(t *testing.T) {
	m := numberedLog(t, "ok", "ERROR one", "ok", "ok", "ok", "ok", "ok", "Caused by: two", "ok", "ok", "ok", "npm ERR! three", "ok")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	press := func(r rune) string { pressKey(m, r); return m.log.status }
	if got := press('e'); got != "error 1/3" || m.log.focus != 1 {
		t.Fatalf("first: %q focus %d", got, m.log.focus)
	}
	if got := press('e'); got != "error 2/3" || m.log.focus != 7 {
		t.Fatalf("second: %q focus %d", got, m.log.focus)
	}
	if got := press('e'); got != "error 3/3" {
		t.Fatalf("third: %q", got)
	}
	if got := press('e'); got != "error 1/3 (wrapped)" || m.log.focus != 1 {
		t.Fatalf("wrap forward: %q", got)
	}
	if got := press('E'); got != "error 3/3 (wrapped)" || m.log.focus != 11 {
		t.Fatalf("wrap back: %q focus %d", got, m.log.focus)
	}
	if got := press('E'); got != "error 2/3" {
		t.Fatalf("back: %q", got)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "error 2/3") {
		t.Fatalf("footer:\n%s", v)
	}
}

func TestLogErrorJumpNothingFound(t *testing.T) {
	m := numberedLog(t, "fine", "also fine")
	pressKey(m, 'e')
	if m.log.status != "no errors found" || m.log.focus != -1 {
		t.Fatalf("status %q focus %d", m.log.status, m.log.focus)
	}
	pressKey(m, 'E')
	if m.log.status != "no errors found" {
		t.Fatalf("status %q", m.log.status)
	}
}

func TestLogErrorLinesUseFailColourAndHelpListsKey(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	m := numberedLog(t, "fine", "ERROR boom")
	v := m.View()
	fail := lipgloss.NewStyle().Foreground(m.th.Fail).Render("ERROR boom")
	if !strings.Contains(v, fail) {
		t.Fatalf("error line should be drawn in the Fail colour:\n%q", v)
	}
	if plain := lipgloss.NewStyle().Foreground(m.th.Fail).Render("fine"); strings.Contains(v, plain) {
		t.Fatal("a normal line must not use the Fail colour")
	}
	found := false
	for _, k := range m.logKeys() {
		if k.Help().Key == "e/E" {
			found = true
		}
	}
	if !found {
		t.Fatal("help bar lists e/E")
	}
}
