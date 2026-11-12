package trigger

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type harness struct {
	t   *testing.T
	s   core.Screen
	nav []tea.Msg
}

// isBlink reports whether c is a cursor-blink command, which only fires after
// its blink interval and would stall the harness.
func isBlink(c tea.Cmd) bool {
	n := runtime.FuncForPC(reflect.ValueOf(c).Pointer()).Name()
	return n == "github.com/charmbracelet/bubbles/textinput.Blink" ||
		strings.HasPrefix(n, "github.com/charmbracelet/bubbles/cursor.(*Model).BlinkCmd")
}

// pump runs cmd, feeding resulting messages back to the screen. Cursor blinks
// are skipped without running and spinner ticks are dropped, so the loop
// terminates. A command that does not return fails the test.
func (h *harness) pump(cmd tea.Cmd) {
	if cmd == nil || isBlink(cmd) {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(5 * time.Second):
		h.t.Fatalf("command did not return")
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h.pump(c)
		}
	case spinner.TickMsg:
	case core.Push, core.Pop, core.Replace, core.Quit:
		h.nav = append(h.nav, m)
	default:
		var c tea.Cmd
		h.s, c = h.s.Update(m)
		h.pump(c)
	}
}

func (h *harness) send(msg tea.Msg) {
	var c tea.Cmd
	h.s, c = h.s.Update(msg)
	h.pump(c)
}

func (h *harness) keys(ks ...string) {
	for _, k := range ks {
		switch k {
		case "enter":
			h.send(tea.KeyMsg{Type: tea.KeyEnter})
		case "tab":
			h.send(tea.KeyMsg{Type: tea.KeyTab})
		case "esc":
			h.send(tea.KeyMsg{Type: tea.KeyEsc})
		case "down":
			h.send(tea.KeyMsg{Type: tea.KeyDown})
		default:
			for _, r := range k {
				h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
		}
	}
}

func start(t *testing.T, c Client, next func([]Started) core.Screen, jobs ...string) *harness {
	h := &harness{t: t, s: New(Deps{Client: c, Next: next}, jobs)}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	h.pump(h.s.Init())
	return h
}

func (h *harness) view() string { return h.s.View() }

func (h *harness) wantView(sub string) {
	h.t.Helper()
	if v := h.view(); !strings.Contains(v, sub) {
		h.t.Fatalf("view missing %q:\n%s", sub, v)
	}
}

func realClient(t *testing.T, srv *jenkinstest.Server) *jenkins.Client {
	c, err := jenkins.New(jenkins.Options{BaseURL: srv.URL, Username: srv.User(), Token: srv.Token(), HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSingleJobNoParamsConfirm(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("plain")
	var got []Started
	h := start(t, realClient(t, srv), func(s []Started) core.Screen { got = s; return nil }, "plain")
	h.wantView("Trigger plain?")
	h.keys("y")
	if len(got) != 1 || got[0].Err != nil || got[0].Ref.ID == 0 {
		t.Fatalf("started: %+v", got)
	}
	if tr := srv.Triggers(); len(tr) != 1 || tr[0].Endpoint != "build" {
		t.Fatalf("triggers: %+v", tr)
	}
	if len(h.nav) != 1 {
		t.Fatalf("nav: %v", h.nav)
	}
	if _, ok := h.nav[0].(core.Replace); !ok {
		t.Fatalf("want Replace, got %T", h.nav[0])
	}
}

func TestSingleJobNoParamsCancel(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("plain")
	h := start(t, realClient(t, srv), nil, "plain")
	h.keys("n")
	if _, ok := h.nav[0].(core.Pop); !ok || len(srv.Triggers()) != 0 {
		t.Fatalf("nav=%v triggers=%v", h.nav, srv.Triggers())
	}
}

func TestSingleJobFormSummaryConfirm(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("app").Params(jenkinstest.StringParam("BRANCH", "", "main"), jenkinstest.PasswordParam("PW", ""))
	h := start(t, realClient(t, srv), nil, "app")
	h.wantView("BRANCH")
	h.keys("enter") // BRANCH -> PW
	h.keys("hunter2")
	if strings.Contains(h.view(), "hunter2") {
		t.Fatal("password echoed in form")
	}
	h.keys("enter") // submit
	h.wantView("Ready to trigger 1 job")
	v := h.view()
	if !strings.Contains(v, "BRANCH = main") || !strings.Contains(v, "PW = ••••") || strings.Contains(v, "hunter2") {
		t.Fatalf("summary:\n%s", v)
	}
	// esc goes back to the form, enter twice returns to summary.
	h.keys("esc")
	h.wantView("BRANCH")
	h.keys("enter") // form keeps its focus on the last field
	h.wantView("Ready to trigger")
	h.keys("y")
	h.wantView("queued  app")
	tr := srv.Triggers()
	if len(tr) != 1 || tr[0].Endpoint != "buildWithParameters" || tr[0].Params.Get("BRANCH") != "main" || tr[0].Params.Get("PW") != "hunter2" {
		t.Fatalf("triggers: %+v", tr)
	}
	h.keys("esc")
	if _, ok := h.nav[len(h.nav)-1].(core.Pop); !ok {
		t.Fatalf("nav: %v", h.nav)
	}
}

func TestEscAtFirstStepPops(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("app").Params(jenkinstest.StringParam("A", "", ""))
	h := start(t, realClient(t, srv), nil, "app")
	h.keys("esc")
	if len(h.nav) != 1 {
		t.Fatalf("nav %v", h.nav)
	}
	if _, ok := h.nav[0].(core.Pop); !ok {
		t.Fatalf("nav %v", h.nav)
	}
}

func TestManyJobsCommonPerJobOverride(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("a").Params(jenkinstest.StringParam("ENV", "", "dev"), jenkinstest.StringParam("ONLYA", "", "x"), jenkinstest.StringParam("V", "", "1"))
	srv.Job("b").Params(jenkinstest.StringParam("ENV", "", "dev"), jenkinstest.BoolParam("ONLYB", "", false), jenkinstest.StringParam("V", "", "2"))
	h := start(t, realClient(t, srv), nil, "a", "b")
	h.wantView("Common Parameters")
	h.wantView("(differs)")
	// ENV: replace "dev" with "prod"; V left unset.
	h.keys("\x7f")
	for range 3 {
		h.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	h.keys("prod")
	h.keys("enter", "enter") // ENV -> V -> submit
	h.wantView("a (1/2)")
	h.wantView("ONLYA")
	h.wantView("[common")
	// job a: accept ONLYA default, override ENV for this job. Fields: ONLYA, ENV, V.
	h.keys("tab") // to ENV
	for range 4 {
		h.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	h.keys("qa")
	h.keys("enter", "enter") // ENV -> V -> submit
	h.wantView("b (2/2)")
	h.keys("tab", "tab") // ONLYB, ENV, V -> focus last
	h.keys("enter")      // submit
	h.wantView("Ready to trigger 2 job")
	v := h.view()
	for _, want := range []string{"ENV = qa", "ENV = prod", "ONLYA = x", "ONLYB = false", "V = 1", "V = 2"} {
		if !strings.Contains(v, want) {
			t.Errorf("summary missing %q:\n%s", want, v)
		}
	}
	h.keys("y")
	byJob := map[string]jenkinstest.Trigger{}
	for _, tr := range srv.Triggers() {
		byJob[tr.Job] = tr
	}
	a, b := byJob["a"].Params, byJob["b"].Params
	if a.Get("ENV") != "qa" || a.Get("ONLYA") != "x" || a.Get("V") != "1" || len(a) != 3 {
		t.Errorf("a: %v", a)
	}
	if b.Get("ENV") != "prod" || b.Get("ONLYB") != "false" || b.Get("V") != "2" || len(b) != 3 {
		t.Errorf("b: %v", b)
	}
}

func TestManyJobsNoCommonSkipsCommonStep(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("a").Params(jenkinstest.StringParam("X", "", "1"))
	srv.Job("b").Params(jenkinstest.StringParam("Y", "", "2"))
	h := start(t, realClient(t, srv), nil, "a", "b")
	h.wantView("a (1/2)") // no Common Parameters: straight to the per-job steps
	h.keys("enter")
	h.wantView("b (2/2)")
	h.keys("enter")
	h.wantView("Ready to trigger 2 job")
	h.keys("esc", "esc")
	h.wantView("a (1/2)")
	h.keys("esc")
	if _, ok := h.nav[len(h.nav)-1].(core.Pop); !ok {
		t.Fatalf("nav %v", h.nav)
	}
}

// A job whose parameters are all common still gets a per-job step, so it can
// override a Common Parameter.
func TestManyJobsOnlyCommonStillOverridable(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("a").Params(jenkinstest.StringParam("ENV", "", "dev"))
	srv.Job("b").Params(jenkinstest.StringParam("ENV", "", "dev"))
	h := start(t, realClient(t, srv), nil, "a", "b")
	h.wantView("Common Parameters")
	h.keys("enter") // accept dev
	h.wantView("a (1/2)")
	h.wantView("[common")
	h.keys("enter") // a keeps dev
	h.wantView("b (2/2)")
	for range 3 {
		h.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	h.keys("qa", "enter")
	h.wantView("Ready to trigger 2 job")
	// back goes through the per-job steps to common, esc there pops.
	h.keys("esc", "esc", "esc")
	h.wantView("Common Parameters")
	h.keys("esc")
	if _, ok := h.nav[len(h.nav)-1].(core.Pop); !ok {
		t.Fatalf("nav %v", h.nav)
	}
}

// One job has only Common Parameters, the other has its own as well; the
// first overrides a Common Parameter.
func TestManyJobsJobWithOnlyCommonOverrides(t *testing.T) {
	srv := jenkinstest.New(t)
	srv.Job("a").Params(jenkinstest.StringParam("ENV", "", "dev"))
	srv.Job("b").Params(jenkinstest.StringParam("ENV", "", "dev"), jenkinstest.StringParam("ONLYB", "", "x"))
	h := start(t, realClient(t, srv), nil, "a", "b")
	h.keys("enter") // common ENV = dev
	h.wantView("a (1/2)")
	for range 3 {
		h.send(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	h.keys("qa", "enter")
	h.wantView("b (2/2)")
	h.keys("enter", "enter") // ONLYB -> ENV -> submit
	h.wantView("Ready to trigger 2 job")
	h.keys("y")
	byJob := map[string]jenkinstest.Trigger{}
	for _, tr := range srv.Triggers() {
		byJob[tr.Job] = tr
	}
	a, b := byJob["a"].Params, byJob["b"].Params
	if a.Get("ENV") != "qa" || len(a) != 1 {
		t.Errorf("a: %v", a)
	}
	if b.Get("ENV") != "dev" || b.Get("ONLYB") != "x" || len(b) != 2 {
		t.Errorf("b: %v", b)
	}
}

type fakeClient struct {
	defs      map[string][]jenkins.ParamDef
	lookupErr map[string]error
	trigErr   map[string]error
}

func (f fakeClient) JobParameters(_ context.Context, n string) ([]jenkins.ParamDef, error) {
	return f.defs[n], f.lookupErr[n]
}
func (f fakeClient) Trigger(_ context.Context, n string, _ map[string]string) (jenkins.QueueRef, error) {
	if e := f.trigErr[n]; e != nil {
		return jenkins.QueueRef{}, e
	}
	return jenkins.QueueRef{ID: len(n)}, nil
}

func TestLookupFailureExcludedWithNotice(t *testing.T) {
	c := fakeClient{lookupErr: map[string]error{"bad": errors.New("boom")}}
	var got []Started
	h := start(t, c, func(s []Started) core.Screen { got = s; return nil }, "ok", "bad", "ok2")
	h.wantView("bad excluded: boom")
	h.wantView("Ready to trigger 2 job")
	h.keys("y")
	if len(got) != 2 || got[0].Job != "ok" || got[1].Job != "ok2" {
		t.Fatalf("got %+v", got)
	}
}

func TestAllLookupsFail(t *testing.T) {
	c := fakeClient{lookupErr: map[string]error{"bad": errors.New("boom")}}
	h := start(t, c, nil, "bad")
	h.wantView("boom")
	h.keys("esc")
	if _, ok := h.nav[0].(core.Pop); !ok {
		t.Fatal("no pop")
	}
}

func TestTriggerErrorsCollectedAndRedacted(t *testing.T) {
	c := fakeClient{
		defs:    map[string][]jenkins.ParamDef{"a": {{Name: "PW", Type: "password"}}, "b": {{Name: "PW", Type: "password"}}},
		trigErr: map[string]error{"a": errors.New("rejected value s3cr3t")},
	}
	h := start(t, c, nil, "a", "b")
	h.keys("s3cr3t", "enter") // common form: PW
	h.keys("enter", "enter")  // per-job steps for a and b: keep PW
	h.keys("y")
	v := h.view()
	if strings.Contains(v, "s3cr3t") {
		t.Fatalf("secret leaked:\n%s", v)
	}
	if !strings.Contains(v, "FAILED  a: rejected value ••••") || !strings.Contains(v, "queued  b") {
		t.Fatalf("result:\n%s", v)
	}
	if !strings.Contains(v, "esc to go back") {
		t.Fatal("no hint")
	}
	h.keys("esc")
	if _, ok := h.nav[len(h.nav)-1].(core.Pop); !ok {
		t.Fatal("no pop")
	}
}

func TestSurvivorsSingleAfterExclusionUsesSingleFlow(t *testing.T) {
	c := fakeClient{lookupErr: map[string]error{"bad": errors.New("x")}}
	h := start(t, c, nil, "ok", "bad")
	h.wantView("Trigger ok?")
}
