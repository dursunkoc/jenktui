package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// MD1: the header spinner shows for the first load and for R only.
func TestSpinnerOnlyForFirstLoadAndManualRefresh(t *testing.T) {
	c := collClient()
	m, _, _ := collMonitor(c)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if strings.Contains(ansi.Strip(shown(m)), "refreshing…") {
		t.Fatal("idle monitor must show no spinner")
	}
	// Automatic tick: work is in flight but the header stays quiet.
	_, cmd := m.Update(tickMsg{gen: m.gen})
	if !m.load.Active() {
		t.Fatal("cycle should be in flight")
	}
	if m.manual || strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatalf("automatic tick must leave no spinner: %q", m.Subtitle())
	}
	exec(m, cmd)
	// R shows it.
	_, cmd = m.Update(keyR())
	if !m.manual || !strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatalf("R must show the spinner: %q", m.Subtitle())
	}
	exec(m, cmd)
	// The next automatic tick clears the manual flag and hides it again.
	m.Update(tickMsg{gen: m.gen})
	if strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatal("automatic tick after R must show no spinner")
	}
}

// The first cycle (m.modal) shows the header spinner once a row has data.
func TestSpinnerShowsDuringFirstLoad(t *testing.T) {
	c := collClient()
	m, _, _ := newColl(c, []string{"a", "c"}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, cmd := m.Update(tickMsg{gen: m.gen})
	var first tea.Msg
	for _, bc := range cmd().(tea.BatchMsg) {
		if r, ok := bc().(resultMsg); ok {
			first = r
			break
		}
	}
	m.Update(first)
	if !strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatalf("first load must show the spinner: %q", m.Subtitle())
	}
}

func TestFmtAgeBoundaries(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:                "just now",
		time.Minute:                     "1m ago",
		59*time.Minute + 59*time.Second: "59m ago",
		time.Hour:                       "1h ago",
		23 * time.Hour:                  "23h ago",
		24 * time.Hour:                  "yesterday",
		47 * time.Hour:                  "yesterday",
		48 * time.Hour:                  "2d ago",
		7 * 24 * time.Hour:              "7d ago",
	} {
		if got := fmtAge(d); got != want {
			t.Errorf("fmtAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestProgressAndAgeInRows(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m, _ := newModel(&fakeClient{}, Run{Job: "a"})
	m.deps.Now = func() time.Time { return now }
	row := func(st runState) string {
		m.runs[0] = st
		return ansi.Strip(m.block(0, 0)[0])
	}
	run := func(started, est time.Duration) runState {
		return runState{run: Run{Job: "a"}, phase: phRunning, num: 1,
			build: jenkins.Build{Number: 1, Building: true, Timestamp: now.Add(-started), EstimatedDuration: est}}
	}
	if r := row(run(96*time.Second, 4*time.Minute)); !strings.Contains(r, "1m36s / ~4m") {
		t.Errorf("known estimate: %q", r)
	}
	if r := row(run(96*time.Second, 0)); !strings.Contains(r, "1m36s") || strings.Contains(r, "~") {
		t.Errorf("unknown estimate: %q", r)
	}
	if r := row(run(252*time.Second, 4*time.Minute)); !strings.Contains(r, "4m12s (over)") || strings.Contains(r, "~") {
		t.Errorf("overrun: %q", r)
	}
	fin := func(ago time.Duration) runState {
		return runState{run: Run{Job: "a"}, phase: phFinished, num: 1, result: "SUCCESS",
			build: jenkins.Build{Number: 1, Result: "SUCCESS", Duration: time.Minute, Timestamp: now.Add(-ago)}}
	}
	if r := row(fin(2 * time.Minute)); !strings.Contains(r, "2m ago") {
		t.Errorf("finished age: %q", r)
	}
	if r := row(fin(3 * time.Hour)); !strings.Contains(r, "3h ago") {
		t.Errorf("finished hours: %q", r)
	}
	if r := row(fin(30 * time.Hour)); !strings.Contains(r, "yesterday") {
		t.Errorf("finished day: %q", r)
	}
}

func TestFmtEstRoundsToMinutes(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second:                           "45s",
		4*time.Minute + 23*time.Second:             "4m",
		4*time.Minute + 40*time.Second:             "5m",
		10 * time.Minute:                           "10m",
		time.Hour:                                  "1h",
		time.Hour + 5*time.Minute:                  "1h05m",
		time.Hour + 5*time.Minute + 20*time.Second: "1h05m",
	} {
		if got := fmtEst(d); got != want {
			t.Errorf("fmtEst(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRowEdgeCases(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m, _ := newModel(&fakeClient{}, Run{Job: "a"})
	m.deps.Now = func() time.Time { return now }
	row := func(st runState) string {
		st.run, st.num = Run{Job: "a"}, 1
		m.runs[0] = st
		return ansi.Strip(m.block(0, 0)[0])
	}
	r := row(runState{phase: phFinished, result: "SUCCESS", build: jenkins.Build{Duration: time.Minute}})
	if strings.Contains(r, "ago") || strings.Contains(r, "just now") {
		t.Errorf("no timestamp must show no age: %q", r)
	}
	r = row(runState{phase: phFinished, result: "SUCCESS", build: jenkins.Build{Duration: time.Minute, Timestamp: now.Add(time.Hour)}})
	if !strings.Contains(r, "just now") {
		t.Errorf("clock skew finished: %q", r)
	}
	r = row(runState{phase: phRunning, build: jenkins.Build{Building: true, Timestamp: now.Add(time.Hour)}})
	if !strings.Contains(r, "0s") {
		t.Errorf("clock skew running: %q", r)
	}
	r = row(runState{phase: phRunning, build: jenkins.Build{Building: true, Timestamp: now.Add(-10 * time.Minute), EstimatedDuration: time.Hour + 5*time.Minute}})
	if !strings.Contains(r, "10m00s / ~1h05m") {
		t.Errorf("hour estimate: %q", r)
	}
}

// mdClient adds the optional history and context calls to fakeClient.
type mdClient struct {
	*fakeClient
	hist  map[string][]jenkins.Build
	bc    map[string]jenkins.BuildContext
	histN map[string]int
	ctxN  map[string]int
}

func (c *mdClient) BuildHistory(_ context.Context, job string, n int) ([]jenkins.Build, error) {
	c.histN[job]++
	return c.hist[job], nil
}

func (c *mdClient) BuildContext(_ context.Context, job string, _ int) (jenkins.BuildContext, error) {
	c.ctxN[job]++
	return c.bc[job], nil
}

// run executes cmd (flattening batches) and feeds every message to m, so
// applier messages take effect like in the real program.
func run(m *model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			run(m, c)
		}
	default:
		_, c := m.Update(msg)
		run(m, c)
	}
}

func mdModel(hist []jenkins.Build, bc jenkins.BuildContext) (*model, *mdClient) {
	fc := &fakeClient{builds: map[string]map[int]jenkins.Build{"a": {0: ok(5)}}}
	c := &mdClient{fakeClient: fc, histN: map[string]int{}, ctxN: map[string]int{},
		hist: map[string][]jenkins.Build{"a": hist}, bc: map[string]jenkins.BuildContext{"a": bc}}
	m, _, _ := newColl(fc, []string{"a"}, nil)
	m.deps.Client = c
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return m, c
}

func TestHistoryDotsFetchedOncePerFinishedBuild(t *testing.T) {
	hist := []jenkins.Build{ // newest first
		{Number: 5, Result: "SUCCESS"}, {Number: 4, Result: "FAILURE"}, {Number: 3, Result: "UNSTABLE"},
		{Number: 2, Result: "ABORTED"}, {Number: 1, Result: "SUCCESS"},
	}
	m, c := mdModel(hist, jenkins.BuildContext{})
	poll := func() { _, cmd := m.Update(tickMsg{gen: m.gen}); run(m, cmd) }
	poll()
	run(m, m.onResult(0, runState{})) // a second hook call for the same build must not refetch
	if c.histN["a"] != 1 {
		t.Fatalf("history fetched %d times, want 1", c.histN["a"])
	}
	got := ""
	for _, r := range ansi.Strip(m.dots(m.runs[0])) {
		got += string(r)
	}
	if got != "●○◐✕●" {
		t.Fatalf("dots = %q, want oldest-to-newest ●○◐✕●", got)
	}
	poll()
	poll()
	if c.histN["a"] != 1 {
		t.Fatalf("polls must not refetch history: %d", c.histN["a"])
	}
}

func TestDotColoursAndRunning(t *testing.T) {
	m, _ := mdModel(nil, jenkins.BuildContext{})
	type dd struct {
		col   lipgloss.AdaptiveColor
		glyph string
	}
	for res, d := range map[string]dd{
		"SUCCESS": {m.th.OK, "●"}, "FAILURE": {m.th.Fail, "✕"}, "UNSTABLE": {m.th.Warn, "◐"},
	} {
		want := lipgloss.NewStyle().Foreground(d.col).Render(d.glyph)
		if got := m.dot(jenkins.Build{Result: res}); got != want || ansi.Strip(got) != d.glyph {
			t.Errorf("%s dot = %q", res, got)
		}
	}
	if ansi.Strip(m.dot(jenkins.Build{Building: true})) != "●" || ansi.Strip(m.dot(jenkins.Build{Result: "ABORTED"})) != "○" {
		t.Error("running is a filled dot, aborted a hollow one")
	}
	// A newer running build is appended on the right of the fetched history.
	m.md.hist = map[buildKey][]jenkins.Build{{"a", 5}: {{Number: 5, Result: "FAILURE"}, {Number: 4, Result: "SUCCESS"}}}
	m.runs[0].phase, m.runs[0].num = phRunning, 6
	if got := ansi.Strip(m.dots(m.runs[0])); got != "●✕●" {
		t.Fatalf("dots with running = %q", got)
	}
	// History that already lists the running build must not get a second dot.
	m.md.hist = map[buildKey][]jenkins.Build{{"a", 5}: {{Number: 6, Building: true}, {Number: 5, Result: "SUCCESS"}, {Number: 4, Result: "FAILURE"}}}
	m.runs[0].num = 6
	if got := ansi.Strip(m.dots(m.runs[0])); got != "✕●●" {
		t.Fatalf("running build listed in history: %q", got)
	}
}

func TestContextSectionCausesAndCommits(t *testing.T) {
	bc := jenkins.BuildContext{
		Causes: []jenkins.Cause{{Text: "Started by alice", User: "alice"}},
		Changes: []jenkins.Change{
			{ID: "abc1234567", Author: "bob", Message: "fix login\n\nlong body"},
			{ID: "d1", Author: "eve", Message: "two"}, {ID: "d2", Author: "eve", Message: "three"},
			{ID: "d3", Author: "eve", Message: "four"}, {ID: "d4", Author: "eve", Message: "five"},
			{ID: "d5", Author: "eve", Message: "six"},
		},
	}
	m, c := mdModel(nil, bc)
	m.runs[0] = runState{run: Run{Job: "a"}, phase: phFinished, num: 5, result: "FAILURE", build: jenkins.Build{Number: 5}}
	m.hideSuccess = false
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // opens the details; the hook fetches
	run(m, cmd)
	run(m, m.onDetailsOpen(0)) // reopening does not refetch
	if c.ctxN["a"] != 1 {
		t.Fatalf("context fetched %d times, want 1", c.ctxN["a"])
	}
	v := ansi.Strip(m.detailView())
	for _, want := range []string{"Started by alice", "abc1234 bob: fix login", "… and 1 more commits"} {
		if !strings.Contains(v, want) {
			t.Errorf("details lack %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "long body") || strings.Contains(v, "abc12345") || strings.Contains(v, "six") {
		t.Errorf("only first line, 7-char ids, five commits:\n%s", v)
	}
}

func TestNoChangesShowsNoCommitBlock(t *testing.T) {
	m, _ := mdModel(nil, jenkins.BuildContext{Causes: []jenkins.Cause{{Text: "Branch indexing"}}})
	m.runs[0] = runState{run: Run{Job: "a"}, phase: phFinished, num: 5, result: "SUCCESS", build: jenkins.Build{Number: 5}}
	run(m, m.onDetailsOpen(0))
	lines := m.contextLines(m.runs[0])
	if len(lines) != 1 || !strings.Contains(ansi.Strip(lines[0]), "Branch indexing") {
		t.Fatalf("lines = %q", lines)
	}
}

func TestFetchOutlivingTheScreenStillApplies(t *testing.T) {
	m, c := mdModel([]jenkins.Build{{Number: 5, Result: "SUCCESS"}}, jenkins.BuildContext{Causes: []jenkins.Cause{{Text: "x"}}})
	st := runState{run: Run{Job: "a"}, phase: phFinished, num: 5, result: "SUCCESS", build: jenkins.Build{Number: 5}}
	m.runs[0] = st
	h, cx := m.fetchHistory(st), m.fetchContext(st)
	m.epoch++ // the screen was left and reopened mid-fetch
	run(m, h)
	run(m, cx)
	if len(m.md.hist) != 1 || len(m.md.ctx) != 1 {
		t.Fatal("results of a finished build are valid whatever the epoch")
	}
	if m.fetchHistory(st) != nil || m.fetchContext(st) != nil || c.histN["a"] != 1 {
		t.Fatal("applied data must not be fetched again")
	}
}

func TestRunningContextRefetchedWhenBuildFinishes(t *testing.T) {
	m, c := mdModel(nil, jenkins.BuildContext{})
	running := runState{run: Run{Job: "a"}, phase: phRunning, num: 5, build: jenkins.Build{Number: 5, Building: true}}
	m.runs[0] = running
	run(m, m.fetchContext(running))
	if c.ctxN["a"] != 1 {
		t.Fatal("first fetch")
	}
	// Opening the details of the still-running build fetches again.
	run(m, m.onDetailsOpen(0))
	if c.ctxN["a"] != 2 {
		t.Fatalf("running context is not final: %d fetches", c.ctxN["a"])
	}
	// The poll that sees it finish refetches once more, then it is final.
	c.bc["a"] = jenkins.BuildContext{Changes: []jenkins.Change{{ID: "abc1234", Author: "bob", Message: "late"}}}
	fin := runState{run: Run{Job: "a"}, phase: phFinished, num: 5, result: "SUCCESS", build: jenkins.Build{Number: 5}}
	m.runs[0] = fin
	run(m, m.onResult(0, running))
	if c.ctxN["a"] != 3 || len(m.contextLines(fin)) != 1 {
		t.Fatalf("finish must refetch: %d fetches, lines %v", c.ctxN["a"], m.contextLines(fin))
	}
	run(m, m.onDetailsOpen(0))
	run(m, m.onResult(0, fin))
	if c.ctxN["a"] != 3 {
		t.Fatal("a finished build's context is final")
	}
}

type errClient struct {
	*mdClient
	err error
	n   int
}

func (e *errClient) BuildHistory(context.Context, string, int) ([]jenkins.Build, error) {
	e.n++
	return nil, e.err
}

func TestHistoryRetriesStop(t *testing.T) {
	st := runState{run: Run{Job: "a"}, phase: phFinished, num: 5, result: "SUCCESS", build: jenkins.Build{Number: 5}}
	for _, tc := range []struct {
		err  error
		want int
	}{{jenkins.ErrAuth, 1}, {jenkins.ErrForbidden, 1}, {errors.New("boom"), maxTries}} {
		m, c := mdModel(nil, jenkins.BuildContext{})
		e := &errClient{mdClient: c, err: tc.err}
		m.deps.Client = e
		m.runs[0] = st
		for i := 0; i < 10; i++ {
			run(m, m.onResult(0, st))
		}
		if e.n != tc.want {
			t.Errorf("%v: %d attempts, want %d", tc.err, e.n, tc.want)
		}
	}
}

func TestCauseNewlinesBecomeSpaces(t *testing.T) {
	m, _ := mdModel(nil, jenkins.BuildContext{Causes: []jenkins.Cause{{Text: "Started by\nupstream\n"}}})
	st := runState{run: Run{Job: "a"}, phase: phFinished, num: 5, result: "SUCCESS", build: jenkins.Build{Number: 5}}
	m.runs[0] = st
	run(m, m.fetchContext(st))
	l := m.contextLines(st)
	if len(l) != 1 || ansi.Strip(l[0]) != "Started by upstream" {
		t.Fatalf("%q", l)
	}
}

// pane is a stand-in root screen for the Router test.
type pane struct{ got []tea.Msg }

func (p *pane) Init() tea.Cmd { return nil }
func (p *pane) Update(msg tea.Msg) (core.Screen, tea.Cmd) {
	p.got = append(p.got, msg)
	return p, nil
}
func (p *pane) View() string        { return "" }
func (p *pane) Title() string       { return "root" }
func (p *pane) Keys() []key.Binding { return nil }

func TestReplyLostWhileLeftIsFetchedAgain(t *testing.T) {
	m, c := mdModel([]jenkins.Build{{Number: 5, Result: "SUCCESS"}}, jenkins.BuildContext{})
	r := core.NewRouter(&core.Session{}, &pane{})
	r.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	r.Update(core.Push{Screen: m})
	st := runState{run: Run{Job: "a"}, phase: phFinished, num: 5, result: "SUCCESS", build: jenkins.Build{Number: 5}}
	m.runs[0] = st
	reply := m.fetchHistory(st) // a fetch in flight
	_, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	r.Update(cmd().(tea.Msg)) // core.Pop
	if r.Depth() != 1 {
		t.Fatal("esc must pop the monitor")
	}
	r.Update(reply()) // the router sends it to the root screen: lost
	if len(m.md.hist) != 0 {
		t.Fatal("the reply must not reach the popped monitor")
	}
	r.Update(core.Push{Screen: m})
	_, cmd = r.Update(tickMsg{gen: m.gen})
	feed := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		var walk func(tea.Cmd)
		walk = func(c tea.Cmd) {
			if c == nil {
				return
			}
			switch msg := c().(type) {
			case nil:
			case tea.BatchMsg:
				for _, x := range msg {
					walk(x)
				}
			case resultMsg, histMsg, ctxMsg: // spinner ticks would chain forever
				_, next := r.Update(msg)
				walk(next)
			}
		}
		walk(cmd)
	}
	feed(cmd)
	if c.histN["a"] != 2 || len(m.md.hist) != 1 {
		t.Fatalf("fetches = %d, hist = %v: the lost fetch must be repeated", c.histN["a"], m.md.hist)
	}
	if ansi.Strip(m.dots(m.runs[0])) != "●" {
		t.Fatal("dots must appear")
	}
}
