package entry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

func runCmd(c tea.Cmd) tea.Msg {
	if c == nil {
		return nil
	}
	return c()
}

func typeText(s core.Screen, text string) core.Screen {
	for _, r := range text {
		s, _ = s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return s
}

func enter(s core.Screen) (core.Screen, tea.Cmd) { return s.Update(tea.KeyMsg{Type: tea.KeyEnter}) }

func writeCfg(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSplashStateMachine(t *testing.T) {
	srv := jenkinstest.New(t)
	dir := t.TempDir()
	writeCfg(t, dir, `jenkins_url = "`+srv.URL+`"`+"\nusername = \"me\"\n")
	sess := &core.Session{}
	s := NewSplash(sess, Deps{ConfigDir: dir, MinSplash: time.Second})

	res := s.check()
	// check finishes before animation: must wait.
	_, cmd := s.Update(res)
	if cmd != nil {
		t.Fatal("must not advance before animation is done")
	}
	// Any key skips animation, then advances to login.
	_, cmd = s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	rep, ok := runCmd(cmd).(core.Replace)
	if !ok {
		t.Fatalf("want Replace, got %T", runCmd(cmd))
	}
	if _, ok := rep.Screen.(*Login); !ok {
		t.Fatalf("want Login, got %T", rep.Screen)
	}
	if sess.Config.JenkinsURL != srv.URL {
		t.Fatal("session config not set")
	}
}

func TestSplashSkipDoesNotSkipCheck(t *testing.T) {
	srv := jenkinstest.New(t)
	dir := t.TempDir()
	writeCfg(t, dir, `jenkins_url = "`+srv.URL+`"`)
	s := NewSplash(&core.Session{}, Deps{ConfigDir: dir})
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("skip alone must not advance: check not done")
	}
	_, cmd = s.Update(s.check())
	if _, ok := runCmd(cmd).(core.Replace); !ok {
		t.Fatal("expected Replace after check done")
	}
}

func TestSplashTicksFinishAnimation(t *testing.T) {
	srv := jenkinstest.New(t)
	dir := t.TempDir()
	writeCfg(t, dir, `jenkins_url = "`+srv.URL+`"`)
	s := NewSplash(&core.Session{}, Deps{ConfigDir: dir, MinSplash: 3 * tickInterval})
	s.Update(s.check())
	var cmd tea.Cmd
	for i := 0; i < 3; i++ {
		_, cmd = s.Update(splashTick{})
	}
	if _, ok := runCmd(cmd).(core.Replace); !ok {
		t.Fatal("expected Replace once min duration elapsed")
	}
}

func TestSplashMissingConfigGoesToSetup(t *testing.T) {
	s := NewSplash(&core.Session{}, Deps{ConfigDir: t.TempDir(), MinSplash: tickInterval})
	s.Update(s.check())
	_, cmd := s.Update(splashTick{})
	rep, _ := runCmd(cmd).(core.Replace)
	if _, ok := rep.Screen.(*Setup); !ok {
		t.Fatalf("want Setup, got %T", rep.Screen)
	}
}

func TestSplashInvalidConfigShowsError(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, `jenkins_url = "ftp://nope"`)
	s := NewSplash(&core.Session{}, Deps{ConfigDir: dir, MinSplash: tickInterval})
	s.Update(s.check())
	_, cmd := s.Update(splashTick{})
	if cmd != nil || s.phase != phaseConfigErr {
		t.Fatalf("want config error phase, got %v", s.phase)
	}
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if v := s.View(); !strings.Contains(v, "Invalid configuration") || !strings.Contains(v, "http(s)") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestSplashUnreachable(t *testing.T) {
	dir := t.TempDir()
	writeCfg(t, dir, `jenkins_url = "http://127.0.0.1:1"`)
	s := NewSplash(&core.Session{}, Deps{ConfigDir: dir, MinSplash: tickInterval})
	s.Update(s.check())
	s.Update(splashTick{})
	if s.phase != phaseUnreachable || !errors.Is(s.err, jenkins.ErrNetwork) {
		t.Fatalf("phase=%v err=%v", s.phase, s.err)
	}
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil || s.phase != phaseChecking {
		t.Fatal("r should retry")
	}
}

func TestFirstRunSetupWritesConfig(t *testing.T) {
	srv := jenkinstest.New(t)
	dir := t.TempDir()
	t.Setenv("JENKTUI_CONFIG_DIR", dir)
	d, err := config.Dir()
	if err != nil || d != dir {
		t.Fatalf("config dir %q %v", d, err)
	}
	sess := &core.Session{}
	var s core.Screen = NewSetup(sess, Deps{ConfigDir: d})

	// invalid URL: error, nothing written
	s = typeText(s, "ftp://bad")
	s, _ = enter(s)
	s, cmd := enter(s)
	if cmd != nil || !strings.Contains(s.(*Setup).errMsg, "http(s)") {
		t.Fatalf("expected validation error, got %q", s.(*Setup).errMsg)
	}
	if _, err := os.Stat(filepath.Join(d, "config.toml")); err == nil {
		t.Fatal("config must not be written when invalid")
	}

	// fix URL, add parent
	st := s.(*Setup)
	st.url.SetValue(srv.URL + "/")
	s = typeText(s, "/team/app/")
	s, cmd = enter(s)
	if cmd == nil {
		t.Fatal("expected reachability cmd")
	}
	cfg, err := config.Load(filepath.Join(d, "config.toml"))
	if err != nil || cfg.JenkinsURL != srv.URL || cfg.ParentJob != "team/app" {
		t.Fatalf("saved cfg %+v %v", cfg, err)
	}
	_, cmd = s.Update(runCmd(cmd))
	rep, ok := runCmd(cmd).(core.Replace)
	if !ok {
		t.Fatal("expected Replace to Login")
	}
	if _, ok := rep.Screen.(*Login); !ok {
		t.Fatalf("want Login got %T", rep.Screen)
	}
	if sess.Config.ParentJob != "team/app" {
		t.Fatal("session config not updated")
	}
}

func newLogin(t *testing.T, cfg config.Config) (*Login, *core.Session) {
	sess := &core.Session{Config: cfg}
	return NewLogin(sess, Deps{ConfigDir: t.TempDir()}), sess
}

func TestLoginValidationAndPrefill(t *testing.T) {
	l, _ := newLogin(t, config.Config{JenkinsURL: "http://x", Username: "me"})
	if l.user.Value() != "me" || l.focus != 1 {
		t.Fatal("username should be prefilled and token focused")
	}
	_, cmd := enter(l)
	if cmd != nil || l.msg != "API token is required" {
		t.Fatalf("msg=%q", l.msg)
	}
	l.user.SetValue("")
	l.token.SetValue("x")
	if _, cmd = l.submit(); cmd != nil || l.msg != "username is required" {
		t.Fatalf("msg=%q", l.msg)
	}
	if l.token.EchoMode != textinput.EchoPassword {
		t.Fatal("token must be masked")
	}
}

func TestLoginSuccessSetsClient(t *testing.T) {
	srv := jenkinstest.New(t)
	l, sess := newLogin(t, config.Config{JenkinsURL: srv.URL, Username: "me"})
	var s core.Screen = typeText(l, srv.Token())
	s, cmd := enter(s)
	msg := runCmd(cmd)
	_, cmd = s.Update(msg)
	rep, ok := runCmd(cmd).(core.Replace)
	if !ok {
		t.Fatalf("want Replace, got %T", msg)
	}
	if _, ok := rep.Screen.(*Menu); !ok {
		t.Fatalf("want Menu got %T", rep.Screen)
	}
	if _, ok := sess.Client.(*jenkins.Client); !ok {
		t.Fatalf("Client = %T", sess.Client)
	}
}

func TestLoginErrorMapping(t *testing.T) {
	srv := jenkinstest.New(t)
	l, sess := newLogin(t, config.Config{JenkinsURL: srv.URL, Username: "me"})
	var s core.Screen = typeText(l, "wrong")
	s, cmd := enter(s)
	s.Update(runCmd(cmd))
	if !errors.Is(l.err, jenkins.ErrAuth) || sess.Client != nil {
		t.Fatalf("err=%v client=%v", l.err, sess.Client)
	}
	l.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if v := l.View(); !strings.Contains(v, "Authentication failed") || !strings.Contains(v, "API token") {
		t.Fatalf("view:\n%s", v)
	}

	l2, _ := newLogin(t, config.Config{JenkinsURL: "http://127.0.0.1:1", Username: "me"})
	l2.token.SetValue("t")
	_, cmd = l2.submit()
	l2.Update(runCmd(cmd))
	if !errors.Is(l2.err, jenkins.ErrNetwork) {
		t.Fatalf("err=%v", l2.err)
	}
}

func TestErrHints(t *testing.T) {
	if h := errHint(jenkins.ErrTLS); !strings.Contains(h, "ca_cert") || !strings.Contains(h, "insecure_skip_verify") {
		t.Fatal(h)
	}
	if !strings.Contains(errHint(jenkins.ErrNetwork), "jenkins_url") {
		t.Fatal("network hint")
	}
	if !strings.Contains(errHint(jenkins.ErrAuth), "token") {
		t.Fatal("auth hint")
	}
}

func TestMenuNavigation(t *testing.T) {
	sess := &core.Session{}
	custom := &notice{title: "custom", th: core.NewTheme()}
	m := NewMenu(sess, Deps{
		Catalog: func(*core.Session) core.Screen { return custom },
	}, "Me")
	_, cmd := enter(m)
	p := runCmd(cmd).(core.Push)
	if p.Screen.Title() != "Collections" {
		t.Fatal("default placeholder expected")
	}
	if !strings.Contains(p.Screen.View(), "coming soon") {
		t.Fatal("notice text")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = enter(m)
	if runCmd(cmd).(core.Push).Screen != core.Screen(custom) {
		t.Fatal("injected factory not used")
	}
	if len(menuItems) != 3 || menuItems[2] != "Quit" {
		t.Fatalf("menu entries: %v", menuItems)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = enter(m)
	if _, ok := runCmd(cmd).(core.Quit); !ok {
		t.Fatal("Quit item")
	}
}

func TestRouterIntegration(t *testing.T) {
	sess := &core.Session{}
	r := core.NewRouter(sess, NewSplash(sess, Deps{ConfigDir: t.TempDir()}))
	r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if v := r.View(); !strings.Contains(v, "checking") {
		t.Fatalf("splash view:\n%s", v)
	}
}
