package overview

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type fakeClient struct {
	mu     sync.Mutex
	builds map[string]jenkins.Build
	errs   map[string]error
	queued map[string]int
	asked  []string
}

func (c *fakeClient) BuildStatus(_ context.Context, job string, _ int) (jenkins.Build, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, job)
	if e := c.errs[job]; e != nil {
		return jenkins.Build{}, e
	}
	b, ok := c.builds[job]
	if !ok {
		return jenkins.Build{}, jenkins.ErrBuildNotFound
	}
	return b, nil
}

func (c *fakeClient) QueuedJobs(context.Context) (map[string]int, error) { return c.queued, nil }

type fixture struct {
	s      *screen
	c      *fakeClient
	timers []tea.Msg
}

func newFixture(c Client, cols []Collection, open func(string) tea.Cmd) *fixture {
	f := &fixture{}
	f.c, _ = c.(*fakeClient)
	f.s = New(Deps{
		Client: c,
		Load:   func() ([]Collection, error) { return cols, nil },
		Open:   open,
		After:  func(_ time.Duration, msg tea.Msg) tea.Cmd { f.timers = append(f.timers, msg); return nil },
	}).(*screen)
	f.s.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return f
}

// run executes cmd and feeds every message back, returning navigation messages.
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
	case core.Push, core.Pop, core.Quit:
		nav = append(nav, m)
	default:
		_, c := f.s.Update(m)
		nav = append(nav, f.run(c)...)
	}
	return
}

func (f *fixture) view() string { return ansi.Strip(f.s.View()) }

func sampleClient() *fakeClient {
	return &fakeClient{
		builds: map[string]jenkins.Build{
			"a": {Number: 3, Result: "FAILURE"},
			"b": {Number: 9, Building: true},
			"c": {Number: 4, Result: "SUCCESS"},
			"d": {Number: 2, Result: "UNSTABLE"},
		},
		errs:   map[string]error{"e": errors.New("boom")},
		queued: map[string]int{"c": 2},
	}
}

var sampleCols = []Collection{
	{Name: "Nightly", Jobs: []string{"a", "b", "c", "d"}},
	{Name: "Calm", Jobs: []string{"c"}},
	{Name: "Empty"},
	{Name: "Odd", Jobs: []string{"e", "never"}},
}

func TestCountsFailedRunningQueuedPerCollection(t *testing.T) {
	f := newFixture(sampleClient(), sampleCols, nil)
	f.run(f.s.Init())
	want := []counts{
		{name: "Nightly", jobs: 4, failed: 2, running: 1, queued: 1},
		{name: "Calm", jobs: 1, queued: 1},
		{name: "Empty"},
		{name: "Odd", jobs: 2, unreadable: 1},
	}
	for i, w := range want {
		if f.s.rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, f.s.rows[i], w)
		}
	}
	v := f.view()
	for _, sub := range []string{"Nightly", "2 failed · 1 running · 1 queued", "1 queued", "no jobs", "1 unreadable"} {
		if !strings.Contains(v, sub) {
			t.Errorf("view missing %q:\n%s", sub, v)
		}
	}
	// a job shared by two Collections is read once per cycle
	n := 0
	for _, j := range f.c.asked {
		if j == "c" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("job c read %d times", n)
	}
}

func TestPollsAgainAfterTheIntervalAndDropsStaleCycles(t *testing.T) {
	f := newFixture(sampleClient(), sampleCols, nil)
	cmd := f.s.Init()
	if len(f.timers) != 0 {
		t.Fatal("timer before the first result")
	}
	f.run(cmd)
	if len(f.timers) != 1 {
		t.Fatalf("timers = %v", f.timers)
	}
	tick := f.timers[0].(tickMsg)
	// a stale tick does nothing
	if _, c := f.s.Update(tickMsg{gen: tick.gen - 1}); c != nil {
		t.Fatal("stale tick started a cycle")
	}
	// leaving drops the in-flight cycle and the scheduled tick
	_, c := f.s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := c().(core.Pop); !ok {
		t.Fatal("esc must pop")
	}
	before := len(f.c.asked)
	if _, c := f.s.Update(tick); c != nil {
		t.Fatal("tick after leaving started a cycle")
	}
	f.c.builds["a"] = jenkins.Build{Number: 4, Result: "SUCCESS"}
	if _, c := f.s.Update(resultMsg{gen: f.s.gen - 1, rows: []counts{{name: "Late"}}}); c != nil || len(f.s.rows) != 4 {
		t.Fatal("a result from before leaving was applied")
	}
	if len(f.c.asked) != before {
		t.Fatal("polled after leaving")
	}
}

func TestResumeRestartsPolling(t *testing.T) {
	f := newFixture(sampleClient(), sampleCols, nil)
	f.run(f.s.Init())
	f.s.pause()
	f.c.builds["a"] = jenkins.Build{Number: 4, Result: "SUCCESS"}
	f.run(f.s.Resume())
	if f.s.rows[0].failed != 1 {
		t.Fatalf("after resume failed = %d, want 1 (only d)", f.s.rows[0].failed)
	}
}

func TestEnterOpensTheHighlightedCollectionUnlessAKeyIntervenes(t *testing.T) {
	var opened []string
	var reply func() tea.Msg
	open := func(name string) tea.Cmd {
		opened = append(opened, name)
		return reply
	}
	target := &stubScreen{}
	f := newFixture(sampleClient(), sampleCols, open)
	f.run(f.s.Init())
	f.s.Update(tea.KeyMsg{Type: tea.KeyDown})
	reply = func() tea.Msg { return Opened{Screen: func() core.Screen { return target }} }
	nav := f.run(f.s.keyCmd(tea.KeyMsg{Type: tea.KeyEnter}))
	if len(opened) != 1 || opened[0] != "Calm" || len(nav) != 1 || nav[0].(core.Push).Screen != core.Screen(target) {
		t.Fatalf("opened=%v nav=%v", opened, nav)
	}
	genAfter := f.s.gen
	// the push stopped polling: a tick from before it is dropped
	if _, c := f.s.Update(tickMsg{gen: genAfter - 1}); c != nil {
		t.Fatal("polling continued under the monitor")
	}

	// a key between Enter and the reply cancels the push
	f2 := newFixture(sampleClient(), sampleCols, open)
	f2.run(f2.s.Init())
	f2.s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(f2.view(), "opening Nightly…") {
		t.Fatalf("no opening notice:\n%s", f2.view())
	}
	f2.s.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, c := f2.s.Update(Opened{Screen: func() core.Screen { t.Fatal("built after a key"); return nil }})
	if c != nil {
		t.Fatal("late reply pushed")
	}
	// an unknown Collection shows its notice
	f2.s.opening = true
	f2.s.Update(Opened{Notice: `no Collection named "X"`})
	if !strings.Contains(f2.view(), `no Collection named "X"`) {
		t.Fatal("notice not shown")
	}
}

type stubScreen struct{}

func (*stubScreen) Init() tea.Cmd                           { return nil }
func (s *stubScreen) Update(tea.Msg) (core.Screen, tea.Cmd) { return s, nil }
func (*stubScreen) View() string                            { return "" }
func (*stubScreen) Title() string                           { return "stub" }
func (*stubScreen) Keys() []key.Binding                     { return nil }

func (s *screen) keyCmd(m tea.KeyMsg) tea.Cmd { _, c := s.Update(m); return c }

func TestLoadErrorIsShown(t *testing.T) {
	f := newFixture(sampleClient(), nil, nil)
	f.s.deps.Load = func() ([]Collection, error) { return nil, errors.New("bad toml") }
	f.run(f.s.Init())
	if !strings.Contains(f.view(), "bad toml") {
		t.Fatalf("view:\n%s", f.view())
	}
}

func TestNoCollectionsMessage(t *testing.T) {
	f := newFixture(sampleClient(), nil, nil)
	f.run(f.s.Init())
	if !strings.Contains(f.view(), "No Collections yet") {
		t.Fatalf("view:\n%s", f.view())
	}
}

type colorClient struct {
	*fakeClient
	colors map[string]map[string]string // folder -> name -> colour
	errs   map[string]error             // folder -> read error
	calls  []string
}

func (c *colorClient) FolderColors(_ context.Context, folder string) (map[string]string, error) {
	c.calls = append(c.calls, folder)
	if e := c.errs[folder]; e != nil {
		return nil, e
	}
	return c.colors[folder], nil
}

func TestCountsFromFolderColorsOneRequestPerFolder(t *testing.T) {
	cc := &colorClient{
		fakeClient: sampleClient(),
		colors: map[string]map[string]string{
			"team/api": {"build": "red", "deploy": "blue_anime", "lint": "yellow", "old": "blue", "off": "disabled", "nb": "notbuilt", "ab": "aborted"},
			"team/web": {"test": "red_anime"},
		},
	}
	cols := []Collection{
		{Name: "One", Jobs: []string{"team/api/build", "team/api/deploy", "team/api/lint", "team/api/old", "team/api/off", "team/api/nb", "team/api/ab", "team/api/gone"}},
		{Name: "Two", Jobs: []string{"team/api/build", "team/web/test"}},
	}
	f := newFixture(cc, cols, nil)
	f.run(f.s.Init())
	if got := f.s.rows[0]; got.failed != 2 || got.running != 1 || got.missing != 1 {
		t.Fatalf("One = %+v", got)
	}
	if got := f.s.rows[1]; got.failed != 1 || got.running != 1 {
		t.Fatalf("Two = %+v", got)
	}
	if len(cc.calls) != 2 {
		t.Fatalf("folder reads = %v, want one per distinct folder", cc.calls)
	}
	if len(cc.asked) != 0 {
		t.Fatalf("BuildStatus used although the folders were readable: %v", cc.asked)
	}
	if !strings.Contains(f.view(), "1 missing") {
		t.Fatalf("missing not shown:\n%s", f.view())
	}
}

func TestUnreadableFolderFallsBackToBuildStatus(t *testing.T) {
	cc := &colorClient{fakeClient: sampleClient(), colors: map[string]map[string]string{}, errs: map[string]error{"": errors.New("403")}}
	f := newFixture(cc, []Collection{{Name: "N", Jobs: []string{"a", "b", "c"}}}, nil)
	f.run(f.s.Init())
	if got := f.s.rows[0]; got.failed != 1 || got.running != 1 || got.missing != 0 {
		t.Fatalf("row = %+v", got)
	}
	if len(cc.asked) != 3 {
		t.Fatalf("fallback reads = %v", cc.asked)
	}
}

func TestRefreshIsIgnoredWhileACycleRunsAndShowsRefreshing(t *testing.T) {
	f := newFixture(sampleClient(), sampleCols, nil)
	cmd := f.s.Init() // a cycle is in flight
	if !strings.Contains(f.view(), "Loading") && !f.s.busy {
		t.Fatal("not busy during the first cycle")
	}
	genBefore := f.s.gen
	if _, c := f.s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}}); c != nil || f.s.gen != genBefore {
		t.Fatal("R started a second cycle")
	}
	f.run(cmd)
	// loaded; a new cycle shows "refreshing…" and R is ignored again
	_, c2 := f.s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if c2 == nil || !strings.Contains(f.view(), "refreshing…") {
		t.Fatalf("no refresh started or no notice:\n%s", f.view())
	}
	if _, c := f.s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}}); c != nil {
		t.Fatal("R piled up a cycle")
	}
	f.run(c2)
	if strings.Contains(f.view(), "refreshing…") {
		t.Fatal("refreshing stays after the result")
	}
}

func TestGoneFolderMakesMembersMissingWithoutFallback(t *testing.T) {
	cc := &colorClient{fakeClient: sampleClient(), errs: map[string]error{"team/old": jenkins.ErrJobNotFound},
		colors: map[string]map[string]string{"app": {"feature%2Fx": "red"}}}
	f := newFixture(cc, []Collection{{Name: "N", Jobs: []string{"team/old/a", "team/old/b", "app/feature%2Fx"}}}, nil)
	f.run(f.s.Init())
	if got := f.s.rows[0]; got.missing != 2 || got.failed != 1 {
		t.Fatalf("row = %+v", got)
	}
	if len(cc.asked) != 0 {
		t.Fatalf("fallback used for a gone folder: %v", cc.asked)
	}
}
