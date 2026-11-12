package monitor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/sysint"
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
	build := func(n int) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			if i == maxLogLines+100 {
				sb.WriteString("ERROR target\n")
				continue
			}
			sb.WriteString("ok\n")
		}
		return sb.String()
	}
	deliver := func(text string) {
		m.updateLog(logMsg{gen: m.logGen, start: lv.next, chunk: jenkins.LogChunk{Text: text}})
	}
	deliver(build(maxLogLines + 500))
	pressKey(m, 'e')
	if lv.all()[lv.focus] != "ERROR target" {
		t.Fatalf("focus on %q", lv.all()[lv.focus])
	}
	first := lv.focus
	deliver(build(maxLogLines + 500))
	if lv.focus != first || lv.all()[lv.focus] != "ERROR target" {
		t.Fatalf("same-size refetch: focus %d -> %d", first, lv.focus)
	}
	// The log grew: more lines were trimmed, so the buffer index shifts by 100.
	deliver(build(maxLogLines + 600))
	if lv.focus != first-100 || lv.all()[lv.focus] != "ERROR target" {
		t.Fatalf("grown refetch: focus %d (%q), want %d", lv.focus, lv.all()[lv.focus], first-100)
	}
}

func TestLogWrapRevealKeepsTargetOnScreen(t *testing.T) {
	long := strings.Repeat("y", 300) // 6 rows at text width 52, more than the 5-row page
	m := numberedLog(t, "a", "b", long, "ERROR target", "c", "d", "e", "f")
	pressKey(m, 'w')
	pressKey(m, 'g')
	pressKey(m, 'e')
	if v := ansi.Strip(m.View()); !strings.Contains(v, "4 │ ERROR target") {
		t.Fatalf("target not on screen:\n%s", v)
	}
}

func TestLogWrapCountsRowsLikeTheRender(t *testing.T) {
	cjk := strings.Repeat("漢字かな", 15) // 60 wide characters
	m := numberedLog(t, cjk, cjk, cjk, cjk, "LASTLINE")
	m.w = 61 // odd text width
	pressKey(m, 'w')
	if v := ansi.Strip(m.View()); !strings.Contains(v, "5 │ LASTLINE") || !m.log.follow {
		t.Fatalf("follow shows the last line:\n%s", v)
	}
	// rows() equals the number of rendered rows for every line.
	for _, l := range m.log.all() {
		if got, want := m.log.rows(l), strings.Count(ansi.Hardwrap(l, m.log.tw, true), "\n")+1; got != want {
			t.Fatalf("rows(%q)=%d want %d", l, got, want)
		}
	}
}

func TestLogWrapBackwardErrorStartsBelowLastDrawnLine(t *testing.T) {
	long := strings.Repeat("x", 100) // 2 rows
	var ls []string
	for i := 0; i < 30; i++ {
		l := long
		if i == 2 || i == 4 {
			l = "ERROR " + long
		}
		ls = append(ls, l)
	}
	m := numberedLog(t, ls...)
	pressKey(m, 'w')
	pressKey(m, 'g')
	pressKey(m, 'E')
	if m.log.focus != 2 || m.log.status != "error 1/2" {
		t.Fatalf("focus %d status %q: the partly drawn line 3 is the last drawn", m.log.focus, m.log.status)
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

func TestLogWrapShowsLongLineOnSeveralRows(t *testing.T) {
	long := strings.Repeat("abcdefghij", 12) // 120 chars; text width is 60-4-1-3 = 52
	m := numberedLog(t, "short", long, "tail")
	if v := ansi.Strip(m.View()); strings.Contains(v, long[60:]) {
		t.Fatalf("unwrapped line is cut:\n%s", v)
	}
	pressKey(m, 'w')
	v := ansi.Strip(m.View())
	rows := strings.Split(v, "\n")
	if len(rows) != m.h {
		t.Fatalf("modal keeps its height (%d rows):\n%s", len(rows), v)
	}
	for _, want := range []string{"2 │ " + long[:52], "┆ " + long[52:104], "┆ " + long[104:], "3 │ tail", "wrapped"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	// Continuation rows carry no number: line 2 is numbered exactly once.
	if strings.Count(v, " 2 │ ") != 1 {
		t.Fatalf("number only on the first row:\n%s", v)
	}
	pressKey(m, 'w')
	if m.log.wrap || strings.Contains(ansi.Strip(m.View()), "┆") {
		t.Fatal("w toggles wrapping off")
	}
}

func TestLogWrapGoToAndFindWorkOnLogLines(t *testing.T) {
	long := strings.Repeat("x", 200)
	var ls []string
	for i := 1; i <= 30; i++ {
		l := "row"
		if i%10 == 0 {
			l = long + " needle"
		}
		ls = append(ls, l)
	}
	m := numberedLog(t, ls...)
	pressKey(m, 'w')
	if !m.log.follow {
		t.Fatal("wrapping keeps following")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "lines 28-30 of 30") && !strings.Contains(v, "30 │ ") {
		t.Fatalf("follow shows the end:\n%s", v)
	}
	pressKey(m, ':')
	typeKeys(m, "10")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.log.focus != 9 || m.log.follow {
		t.Fatalf("go to line 10: focus %d follow %v", m.log.focus, m.log.follow)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "10 │ xxx") {
		t.Fatalf("line 10 visible:\n%s", v)
	}
	pressKey(m, '/')
	typeKeys(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.log.focus != 9 {
		t.Fatalf("find stays on the visible match, got line index %d", m.log.focus)
	}
	pressKey(m, 'n')
	if m.log.focus != 19 {
		t.Fatalf("n: focus %d", m.log.focus)
	}
	pressKey(m, 'n')
	if m.log.focus != 29 {
		t.Fatalf("n: focus %d", m.log.focus)
	}
	pressKey(m, 'G')
	if !m.log.follow {
		t.Fatal("G follows")
	}
}

// saveTo points S at a fresh directory and returns it.
func saveTo(t *testing.T, m *model) string {
	t.Helper()
	dir := t.TempDir()
	m.deps.SaveDir = dir
	return dir
}

func TestLogSaveWritesWholeLogNotTheBuffer(t *testing.T) {
	m, c := logTail(t)
	dir := saveTo(t, m)
	var sb strings.Builder
	total := maxLogLines + 500
	for i := 1; i <= total; i++ {
		fmt.Fprintf(&sb, "\x1b[32mline %d\x1b[0m\n", i)
	}
	c.log = sb.String()
	m.Update(pressL(m)())
	if m.log.dropped != 500 {
		t.Fatalf("the view keeps only the last lines (dropped %d)", m.log.dropped)
	}
	m.Update(pressKey(m, 'S')())
	want := filepath.Join(dir, "b-4.log")
	if !strings.Contains(m.log.status, "saved to "+want) {
		t.Fatalf("status %q", m.log.status)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "line 1\nline 2\n") || !strings.HasSuffix(got, fmt.Sprintf("line %d\n", total)) || strings.Count(got, "\n") != total {
		t.Fatalf("saved %d lines, starts %q", strings.Count(got, "\n"), got[:20])
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "saved to") {
		t.Fatalf("footer shows the path:\n%s", v)
	}
}

func TestLogSaveNeverOverwrites(t *testing.T) {
	m, c := logTail(t)
	dir := saveTo(t, m)
	c.log = "one\n"
	m.Update(pressL(m)())
	for _, name := range []string{"b-4.log", "b-4-1.log", "b-4-2.log"} {
		m.Update(pressKey(m, 'S')())
		if !strings.HasSuffix(m.log.status, filepath.Join(dir, name)) {
			t.Fatalf("want %s, status %q", name, m.log.status)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "b-4.log")); string(data) != "one\n" {
		t.Fatalf("first file changed: %q", data)
	}
}

func TestLogSaveStageLogSavesWhatIsShown(t *testing.T) {
	m, c := stagedLog(t)
	dir := saveTo(t, m)
	m.Update(pressKey(m, 'l')())
	shown := strings.Join(m.log.all(), "\n") + "\n"
	cmd := pressKey(m, 'S')
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("nothing is written before the command runs")
	}
	m.Update(cmd())
	data, err := os.ReadFile(filepath.Join(dir, "b-4.log"))
	if err != nil || string(data) != shown || !strings.Contains(shown, "step one out") {
		t.Fatalf("saved %q (%v), shown %q", data, err, shown)
	}
	if len(c.logStarts) != 0 {
		t.Fatalf("a stage log is not refetched from consoleText: %v", c.logStarts)
	}
}

func TestLogSaveErrorIsShownAndStaleResultDropped(t *testing.T) {
	m, c := logTail(t)
	m.deps.SaveDir = filepath.Join(t.TempDir(), "missing")
	c.log = "one\n"
	m.Update(pressL(m)())
	m.Update(pressKey(m, 'S')())
	if !strings.HasPrefix(m.log.status, "save failed: ") || m.log.saving {
		t.Fatalf("status %q saving %v", m.log.status, m.log.saving)
	}
	// A result for a log that was closed is ignored.
	cmd := pressKey(m, 'S')
	m.closeLog()
	m.Update(cmd())
	if m.log != nil {
		t.Fatal("log stays closed")
	}
}

func TestLogFileNameReplacesSlashes(t *testing.T) {
	if got := logFileName("team/api/deploy", 57); got != "team_api_deploy-57.log" {
		t.Fatal(got)
	}
	if got := logFileName("app/feature%2Fx", 12); got != "app_feature_x-12.log" {
		t.Fatal(got)
	}
}

// growClient serves a console log that grows with every request and always
// says there is more, like a running build.
type growClient struct {
	*fakeClient
	calls int
}

func (g *growClient) ConsoleText(_ context.Context, _ string, _ int, start int64) (jenkins.LogChunk, error) {
	g.calls++
	return jenkins.LogChunk{Text: "x\n", Next: start + int64(g.calls)*2, More: true}, nil
}

func TestSaveDoesNotFollowARunningBuild(t *testing.T) {
	g := &growClient{fakeClient: &fakeClient{}}
	var buf bytes.Buffer
	if err := streamWholeLog(g, time.Second, "j", 1, &buf); err != nil {
		t.Fatal(err)
	}
	if g.calls > 2 || buf.String() != "x\n" {
		t.Fatalf("%d requests, wrote %q", g.calls, buf.String())
	}
}

func TestLineCleanerWritesOnlyCompleteLines(t *testing.T) {
	var buf bytes.Buffer
	lc := &lineCleaner{w: &buf}
	lc.write("\x1b[32mgre")
	if buf.Len() != 0 {
		t.Fatalf("partial line written: %q", buf.String())
	}
	lc.write("en\x1b[0m\r\nsecond\x1b[8mha:////4abc==\x1b[0m\nthi")
	if got := buf.String(); got != "green\nsecond\n" {
		t.Fatalf("got %q", got)
	}
	lc.write("rd")
	lc.flush()
	if got := buf.String(); got != "green\nsecond\nthird" {
		t.Fatalf("got %q", got)
	}
}

func TestLogSaveFileModeAndFooterPath(t *testing.T) {
	m, c := logTail(t)
	dir := saveTo(t, m)
	c.log = "one\n"
	m.Update(pressL(m)())
	m.Update(pressKey(m, 'S')())
	fi, err := os.Stat(filepath.Join(dir, "b-4.log"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v err %v", fi, err)
	}
	// A narrow terminal cuts the path from the left so the file name stays.
	m.w = 40
	m.log.saving = false
	m.Update(pressKey(m, 'S')())
	if st := m.log.status; !strings.HasPrefix(st, "saved to …") || !strings.HasSuffix(st, "b-4-1.log") || len([]rune(st)) > 36 {
		t.Fatalf("status %q", st)
	}
}

func TestDisplayPathRelativeInsideWorkingDirectory(t *testing.T) {
	wd, _ := os.Getwd()
	if got := displayPath(filepath.Join(wd, "b-4.log"), 0); got != "b-4.log" {
		t.Fatal(got)
	}
}

func logWithSys(t *testing.T, sys Sys, lines ...string) *model {
	t.Helper()
	m, c := logTail(t)
	m.deps.Sys = sys
	m.deps.Client = urlClient{c}
	m.h, m.w = 9, 60
	c.log = strings.Join(lines, "\n") + "\n"
	m.Update(pressL(m)())
	return m
}

// sysKey presses r and feeds the result back, returning the log footer status.
func sysKey(m *model, r rune) string {
	if cmd := pressKey(m, r); cmd != nil {
		m.Update(cmd())
	}
	return m.log.status
}

func TestLogCopyCurrentLineOrMatch(t *testing.T) {
	sys := &fakeSys{route: sysint.RouteTool}
	m := logWithSys(t, sys, "one", "two needle", "three", "four", "five", "six", "seven", "eight")
	if got := sysKey(m, 'y'); got != "copied" || sys.copied[0] != "eight" {
		t.Fatalf("following copies the last line: %q %v", got, sys.copied)
	}
	pressKey(m, 'g')
	sysKey(m, 'y')
	if sys.copied[1] != "one" {
		t.Fatalf("copies the first visible line: %v", sys.copied)
	}
	pressKey(m, '/')
	typeKeys(m, "needle")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sysKey(m, 'y')
	if sys.copied[2] != "two needle" {
		t.Fatalf("copies the current match: %v", sys.copied)
	}
}

func TestLogCopyRouteAndErrorTexts(t *testing.T) {
	m := logWithSys(t, &fakeSys{route: sysint.RouteTerminal}, "one")
	if got := sysKey(m, 'y'); got != "sent to terminal clipboard" {
		t.Fatal(got)
	}
	m = logWithSys(t, &fakeSys{err: errors.New("sysint: no clipboard tool")}, "one")
	if got := sysKey(m, 'y'); got != "no clipboard tool" {
		t.Fatal(got)
	}
}

func TestLogOpenUsesBuildURLAndSysNilHidesKeys(t *testing.T) {
	sys := &fakeSys{}
	m := logWithSys(t, sys, "one")
	if got := sysKey(m, 'o'); got != "opened" || len(sys.opened) != 1 || sys.opened[0] != "https://ci/job/b/4/" {
		t.Fatalf("%q %v", got, sys.opened)
	}
	hasKey := func(m *model, k string) bool {
		for _, b := range m.logKeys() {
			if b.Help().Key == k {
				return true
			}
		}
		return false
	}
	if !hasKey(m, "y") || !hasKey(m, "o") {
		t.Fatal("help lists y and o")
	}
	m = logWithSys(t, nil, "one")
	m.deps.Sys = nil
	if cmd := pressKey(m, 'o'); cmd != nil || hasKey(m, "o") || hasKey(m, "y") {
		t.Fatal("nil Sys hides and ignores o and y")
	}
}

func TestLogSysResultDroppedWhenLogClosed(t *testing.T) {
	m := logWithSys(t, &fakeSys{route: sysint.RouteTool}, "one")
	cmd := pressKey(m, 'y')
	m.closeLog()
	m.Update(cmd())
	if m.log != nil {
		t.Fatal("stays closed")
	}
}
