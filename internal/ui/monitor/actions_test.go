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
	pressKey(m, 'o')
	pressKey(m, 'y')

	// While the log is open o and y act on the log (LV4; see logview_test.go).

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
