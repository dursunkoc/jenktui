package monitor

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/sysint"
)

type fakeSys struct {
	opened, copied []string
	route          sysint.Route
	err            error
}

func (s *fakeSys) Open(u string) error { s.opened = append(s.opened, u); return s.err }
func (s *fakeSys) Copy(t string) (sysint.Route, error) {
	s.copied = append(s.copied, t)
	return s.route, s.err
}
func (s *fakeSys) Notify(string, string) error { return nil }
func (s *fakeSys) Bell()                       {}

// urlClient adds the optional JobURL to the fake client.
type urlClient struct{ *fakeClient }

func (urlClient) JobURL(job string) string { return "https://ci/job/" + job + "/" }
func (urlClient) BuildURL(job string, n int) string {
	return "https://ci/job/" + job + "/" + strconv.Itoa(n) + "/"
}

// actionModel: job "a" has build 7, job "b" was never built, job "c" is missing.
func actionModel(sys Sys) *model {
	c := urlClient{&fakeClient{builds: map[string]map[int]jenkins.Build{
		"a": {0: {Number: 7, Result: "SUCCESS", URL: "http://internal/job/a/7/"}},
	}}}
	s := NewCollection(Deps{Client: c, Now: (&clock{t: t0}).now, Sys: sys}, "N", []string{"a", "b", "c"}, map[string]bool{"c": true}).(*model)
	wire(s)
	cycle(s)
	return s
}

// run presses r and feeds the resulting message back, returning the flash.
func press(m *model, r rune) string {
	cmd := pressKey(m, r)
	if cmd != nil {
		if msg, ok := cmd().(sysDone); ok {
			m.Update(msg)
		}
	}
	return m.notice
}

func TestOpenAndCopyBuildURL(t *testing.T) {
	sys := &fakeSys{route: sysint.RouteTool}
	m := actionModel(sys)
	if got := press(m, 'o'); got != "opened" || len(sys.opened) != 1 || sys.opened[0] != "https://ci/job/a/7/" {
		t.Fatalf("open: %q %v", got, sys.opened)
	}
	if got := press(m, 'y'); got != "copied" || sys.copied[0] != "https://ci/job/a/7/" {
		t.Fatalf("copy: %q %v", got, sys.copied)
	}
	// also from the details view
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	press(m, 'o')
	if len(sys.opened) != 2 {
		t.Fatal("o works in details")
	}
}

func TestCopyViaTerminalSaysSo(t *testing.T) {
	m := actionModel(&fakeSys{route: sysint.RouteTerminal})
	if got := press(m, 'y'); got != "sent to terminal clipboard" {
		t.Fatalf("got %q", got)
	}
}

func TestSysErrorIsFlashed(t *testing.T) {
	m := actionModel(&fakeSys{err: errors.New("no browser")})
	if got := press(m, 'o'); got != "no browser" {
		t.Fatalf("got %q", got)
	}
}

func TestNoBuildOpensJobURL(t *testing.T) {
	sys := &fakeSys{route: sysint.RouteTool}
	m := actionModel(sys)
	m.cursor = 1 // b, never built
	press(m, 'o')
	if len(sys.opened) != 1 || sys.opened[0] != "https://ci/job/b/" {
		t.Fatalf("opened %v", sys.opened)
	}
}

func TestMissingJobDoesNothing(t *testing.T) {
	sys := &fakeSys{route: sysint.RouteTool}
	m := actionModel(sys)
	m.cursor = 2 // c, missing
	if cmd := pressKey(m, 'o'); cmd != nil {
		t.Fatal("o on a missing job must do nothing")
	}
	if cmd := pressKey(m, 'y'); cmd != nil {
		t.Fatal("y on a missing job must do nothing")
	}
	if len(sys.opened)+len(sys.copied) != 0 {
		t.Fatal("nothing opened or copied")
	}
}

func TestNilSysHidesKeys(t *testing.T) {
	m := actionModel(nil)
	if cmd := pressKey(m, 'o'); cmd != nil {
		t.Fatal("o ignored without Sys")
	}
	for _, k := range helpKeys(m) {
		if k == "o open in browser" || k == "y copy URL" {
			t.Fatalf("key %q shown without Sys", k)
		}
	}
	m2 := actionModel(&fakeSys{})
	found := 0
	for _, k := range helpKeys(m2) {
		if k == "o open in browser" || k == "y copy URL" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("keys shown with Sys: %v", helpKeys(m2))
	}
}

func TestYIsYesInConfirm(t *testing.T) {
	sys := &fakeSys{}
	m := actionModel(sys)
	ran := false
	m.ask("Abort?", func() tea.Msg { ran = true; return nil })
	cmd := pressKey(m, 'y')
	if cmd == nil {
		t.Fatal("y confirms")
	}
	cmd()
	if !ran || len(sys.copied) != 0 {
		t.Fatal("y answers the prompt, not copy")
	}
}

func TestErrorFlashIsShort(t *testing.T) {
	if got := shortErr(errors.New("sysint: no browser")); got != "no browser" {
		t.Fatalf("got %q", got)
	}
	got := shortErr(errors.New(strings.Repeat("x", 100)))
	if len([]rune(got)) != 60 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %q", got)
	}
}

func TestKeysIgnoredBehindModalLogAndOverlay(t *testing.T) {
	sys := &fakeSys{route: sysint.RouteTool}
	// first-load modal: cycle started, nothing delivered
	c := urlClient{&fakeClient{}}
	m := NewCollection(Deps{Client: c, Now: (&clock{t: t0}).now, Sys: sys}, "N", []string{"a"}, nil).(*model)
	wire(m)
	m.Update(tickMsg{gen: m.gen})
	if !(m.load.Active() && m.unpolled()) {
		t.Fatal("setup: modal expected")
	}
	if pressKey(m, 'o') != nil || pressKey(m, 'y') != nil {
		t.Fatal("o and y must return no command behind the modal")
	}

	m = actionModel(sys)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pressKey(m, 'L')
	if m.log == nil {
		t.Fatal("setup: log expected")
	}
	if cmd := pressKey(m, 'o'); cmd != nil {
		cmd()
	}
	if cmd := pressKey(m, 'y'); cmd != nil {
		cmd()
	}

	m = actionModel(sys)
	m.overlay = stubOverlay{}
	if cmd := pressKey(m, 'o'); cmd != nil {
		cmd()
	}
	if len(sys.opened)+len(sys.copied) != 0 {
		t.Fatalf("keys must be ignored: %v %v", sys.opened, sys.copied)
	}
}

type stubOverlay struct{}

func (stubOverlay) view(int, int) string           { return "" }
func (stubOverlay) key(tea.KeyMsg) (tea.Cmd, bool) { return nil, false }
func (stubOverlay) keys() []key.Binding            { return nil }

func failClient() *fakeClient {
	return &fakeClient{
		builds: map[string]map[int]jenkins.Build{
			"a": {0: {Number: 3, Result: "SUCCESS"}},
			"b": {0: {Number: 9, Result: "FAILURE"}},
			"c": {0: {Number: 4, Result: "FAILURE"}},
		},
		stages: []jenkins.Stage{
			{ID: "1", Name: "Build", Status: jenkins.StageSuccess},
			{ID: "2", Name: "Test", Status: jenkins.StageFailed},
			{ID: "3", Name: "Deploy", Status: jenkins.StageNotExecuted},
		},
		log: "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n",
	}
}

func failModel(c *fakeClient) *model {
	m := NewCollection(Deps{Client: c, Now: (&clock{t: t0}).now}, "N", []string{"a", "b", "c"}, nil).(*model)
	wire(m)
	m.w, m.h = 80, 10
	cycle(m)
	return m
}

func TestFNextFailedRowWraps(t *testing.T) {
	m := failModel(failClient())
	pressKey(m, 'f')
	if m.cursor != 1 {
		t.Fatalf("cursor %d", m.cursor)
	}
	pressKey(m, 'f')
	pressKey(m, 'f') // wraps past a (success) to b
	if m.cursor != 1 {
		t.Fatalf("cursor %d after wrap", m.cursor)
	}
	m.cursor = 0
	m2 := failModel(&fakeClient{builds: map[string]map[int]jenkins.Build{"a": {0: ok(1)}, "b": {0: ok(1)}, "c": {0: ok(1)}}})
	pressKey(m2, 'f')
	if m2.cursor != 0 {
		t.Fatal("no failed row: cursor stays")
	}
}

func TestEnterOnFailedBuildMarksFailedStage(t *testing.T) {
	m := failModel(failClient())
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliver(m, cmd) // stages arrive after the details opened
	if m.stageCur != 1 {
		t.Fatalf("marker on line %d, want the Test stage (1)", m.stageCur)
	}
}

func TestMovingFirstKeepsTheUsersMarker(t *testing.T) {
	c := failClient()
	c.stages = []jenkins.Stage{
		{ID: "1", Name: "Build", Status: jenkins.StageSuccess},
		{ID: "2", Name: "Test", Status: jenkins.StageSuccess},
		{ID: "3", Name: "Deploy", Status: jenkins.StageFailed},
	}
	m := failModel(c)
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.ma.focusFailed || m.stageCur != 0 {
		t.Fatal("waiting for stages")
	}
	m.runs[1].stages = c.stages // loaded, marker not placed yet
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.stageCur != 1 || m.ma.focusFailed {
		t.Fatalf("down moved to %d, focusFailed=%v", m.stageCur, m.ma.focusFailed)
	}
	deliver(m, cmd) // the stages arrive late
	if m.stageCur != 1 {
		t.Fatalf("marker jumped to %d after the user moved", m.stageCur)
	}
}

func TestUnstableBuildMarksUnstableStageAndLogEnds(t *testing.T) {
	c := failClient()
	c.builds["b"][0] = jenkins.Build{Number: 9, Result: "UNSTABLE"}
	c.stages = []jenkins.Stage{
		{ID: "1", Name: "Build", Status: jenkins.StageSuccess},
		{ID: "2", Name: "Test", Status: jenkins.StageUnstable},
	}
	c.stageLogs = map[string]string{"2": "s1\ns2\ns3\ns4\ns5\ns6\ns7\ns8\ns9\ns10\ns11\ns12\n"}
	m := failModel(c)
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliver(m, cmd)
	if m.stageCur != 1 {
		t.Fatalf("marker %d, want the unstable stage", m.stageCur)
	}
	if cmd := pressKey(m, 'l'); cmd != nil {
		m.Update(cmd())
	}
	if m.log == nil || m.log.follow {
		t.Fatal("log at end, not following")
	}
	all := m.log.all()
	if len(all) == 0 || m.log.start(len(all), m.logPage()) != len(all)-m.logPage() {
		t.Fatalf("not at the end: %d lines", len(all))
	}
}

func TestFailedStageWinsOverUnstable(t *testing.T) {
	st := []jenkins.Stage{
		{Name: "A", Status: jenkins.StageUnstable},
		{Name: "B", Status: jenkins.StageFailed},
	}
	if got := failedLine(st); got != 1 {
		t.Fatalf("got %d", got)
	}
}

func TestFailedChildIsTheDeepestMarker(t *testing.T) {
	st := []jenkins.Stage{
		{Name: "Build", Status: jenkins.StageSuccess},
		{Name: "Test", Status: jenkins.StageFailed, Children: []jenkins.Stage{
			{Name: "unit", Status: jenkins.StageSuccess}, {Name: "it", Status: jenkins.StageFailed}}},
	}
	if got := failedLine(st); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
	if failedLine(st[:1]) != -1 {
		t.Fatal("no failed stage")
	}
}

func TestLOpensFailedBuildLogAtEndNotFollowing(t *testing.T) {
	c := failClient()
	c.stages[1].Children = nil
	c.steps = map[string][]jenkins.Stage{}
	c.stageLogs = map[string]string{"2": "s1\ns2\ns3\ns4\ns5\ns6\ns7\ns8\ns9\ns10\ns11\ns12\n"}
	m := failModel(c)
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliver(m, cmd)
	if cmd := pressKey(m, 'l'); cmd != nil {
		m.Update(cmd())
	}
	if m.log == nil || m.log.follow {
		t.Fatal("log open and not following")
	}
	all := m.log.all()
	if got := m.log.start(len(all), m.logPage()); got != len(all)-m.logPage() {
		t.Fatalf("start %d of %d", got, len(all))
	}
}

func TestFreestyleFailureOpensFullLogAtEnd(t *testing.T) {
	c := failClient()
	c.stageErr = jenkins.ErrNoStages
	c.stages = nil
	m := failModel(c)
	m.cursor = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliver(m, cmd)
	pressKey(m, 'l')
	if m.log == nil || m.log.nodes != nil {
		t.Fatal("full build log expected")
	}
	m.Update(m.fetchLog(0)())
	all := m.log.all()
	if m.log.follow || m.log.start(len(all), m.logPage()) != max(len(all)-m.logPage(), 0) {
		t.Fatalf("not at end: follow=%v", m.log.follow)
	}
}

func TestLOnSuccessKeepsFollowing(t *testing.T) {
	m := failModel(failClient())
	m.cursor = 0
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pressKey(m, 'L')
	if m.log == nil || !m.log.follow {
		t.Fatal("whole-log L unchanged")
	}
}
