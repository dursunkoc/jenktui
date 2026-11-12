package monitor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

func TestErrorLineMatching(t *testing.T) {
	for _, l := range []string{"> Task :test FAILED", "Finished: FAILURE", "ERROR: script returned exit code 1", "[ERROR] Failed", "hudson.AbortException: x", "java.lang.NullPointerException", "TypeError: x", "Error: Cannot find module", "--- FAIL: TestFoo", "Caused by: x", "npm ERR! code 1", "main.go:3: error: bad", "[INFO] BUILD FAILURE"} {
		if !isErrorLine(l) {
			t.Errorf("%q should match", l)
		}
	}
	for _, l := range []string{"MAX_ERRORS=10", "[INFO] Running com.acme.GlobalExceptionHandlerTest", "Tests run: 12, Failures: 0, Errors: 0", "[INFO] BUILD SUCCESS", "-Werror", "ErrorProne", "all good"} {
		if isErrorLine(l) {
			t.Errorf("%q should not match", l)
		}
	}
}

func TestLogErrorBackwardFromFollowStartsBelowScreen(t *testing.T) {
	var ls []string
	for i := 0; i < 30; i++ {
		ls = append(ls, "ok")
	}
	ls[2], ls[26], ls[28] = "ERROR a", "ERROR b", "ERROR c"
	m := numberedLog(t, ls...)
	if !m.log.follow {
		t.Fatal("expected follow mode")
	}
	pressKey(m, 'E')
	if m.log.status != "error 3/3" || m.log.focus != 28 {
		t.Fatalf("status %q focus %d", m.log.status, m.log.focus)
	}
}

func TestStageLogRefetchKeepsFocusOverCap(t *testing.T) {
	m, _ := stagedLog(t)
	m.Update(pressKey(m, 'l')())
	lv := m.log
	var sb strings.Builder
	for i := 0; i < maxLogLines+500; i++ {
		if i == maxLogLines+100 {
			sb.WriteString("ERROR target\n")
			continue
		}
		sb.WriteString("ok\n")
	}
	text := sb.String()
	deliverChunk := func() {
		m.updateLog(logMsg{gen: m.logGen, start: lv.next, chunk: jenkins.LogChunk{Text: text}})
	}
	deliverChunk()
	pressKey(m, 'e')
	want := lv.all()[lv.focus]
	if want != "ERROR target" {
		t.Fatalf("focus on %q", want)
	}
	focus := lv.focus
	deliverChunk()
	deliverChunk()
	if lv.focus != focus || lv.all()[lv.focus] != "ERROR target" {
		t.Fatalf("focus drifted from %d to %d (%q)", focus, lv.focus, lv.all()[lv.focus])
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
