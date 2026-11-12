package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type fakeClient struct {
	queue     map[int]jenkins.QueueStatus
	queueErr  map[int]error
	builds    map[string]map[int]jenkins.Build // job -> number (0 = last)
	buildErr  error
	stages    []jenkins.Stage
	stageErr  error
	deadline  bool
	log       string // console output served by ConsoleText
	logMore   bool
	logErr    error
	logStarts []int64
	buildN    map[string]int // BuildStatus calls per job
	stageN    map[string]int // Stages calls per job
}

func (f *fakeClient) ResolveQueue(ctx context.Context, r jenkins.QueueRef) (jenkins.QueueStatus, error) {
	if _, ok := ctx.Deadline(); ok {
		f.deadline = true
	}
	if err := f.queueErr[r.ID]; err != nil {
		return jenkins.QueueStatus{}, err
	}
	return f.queue[r.ID], nil
}
func (f *fakeClient) BuildStatus(_ context.Context, job string, n int) (jenkins.Build, error) {
	if f.buildN == nil {
		f.buildN = map[string]int{}
	}
	f.buildN[job]++
	if f.buildErr != nil {
		return jenkins.Build{}, f.buildErr
	}
	b, ok := f.builds[job][n]
	if !ok {
		return jenkins.Build{}, jenkins.ErrBuildNotFound
	}
	return b, nil
}
func (f *fakeClient) Stages(_ context.Context, job string, _ int) ([]jenkins.Stage, error) {
	if f.stageN == nil {
		f.stageN = map[string]int{}
	}
	f.stageN[job]++
	return f.stages, f.stageErr
}

func (f *fakeClient) ConsoleText(_ context.Context, _ string, _ int, start int64) (jenkins.LogChunk, error) {
	if f.logErr != nil {
		return jenkins.LogChunk{}, f.logErr
	}
	f.logStarts = append(f.logStarts, start)
	return jenkins.LogChunk{Text: f.log[min(int(start), len(f.log)):], Next: int64(len(f.log)), More: f.logMore}, nil
}

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type sched struct {
	d   time.Duration
	gen int
}

// clock is a test clock shared by the model.
type clock struct{ t time.Time }

var testClock *clock // the clock of the last newModel

func (c *clock) now() time.Time { return c.t }

func wire(m *model) *[]sched {
	var rec []sched
	m.schedule = func(d time.Duration, gen int) tea.Cmd {
		rec = append(rec, sched{d, gen})
		return func() tea.Msg { return nil }
	}
	return &rec
}

// newModel builds a runs-only monitor (the compatibility New).
func newModel(c *fakeClient, runs ...Run) (*model, *[]sched) {
	testClock = &clock{t: t0.Add(time.Minute)}
	s := New(Deps{Client: c, Now: testClock.now}, runs).(*model)
	s.triggered = t0
	for i := range s.runs {
		s.runs[i].at = t0 // each run remembers when it was added
	}
	return s, wire(s)
}

// newColl builds a Collection monitor on a controllable clock.
func newColl(c *fakeClient, jobs []string, missing map[string]bool) (*model, *clock, *[]sched) {
	clk := &clock{t: t0}
	s := NewCollection(Deps{Client: c, Now: clk.now}, "Nightly", jobs, missing).(*model)
	return s, clk, wire(s)
}

// exec runs cmd (flattening batches) and feeds resulting messages to m,
// except tick/nav messages which are returned.
func exec(m *model, cmd tea.Cmd) (nav []tea.Msg) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			nav = append(nav, exec(m, c)...)
		}
	case resultMsg:
		_, c := m.Update(msg)
		nav = append(nav, exec(m, c)...)
	default:
		nav = append(nav, msg)
	}
	return
}

func cycle(m *model) {
	_, c := m.Update(tickMsg{gen: m.gen})
	exec(m, c)
}

// deliver runs cmd and applies every message it produces (stage fetches too).
func deliver(m *model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			deliver(m, c)
		}
	case resultMsg, stagesMsg:
		_, c := m.Update(msg)
		deliver(m, c)
	}
}

func TestIntervalChoice(t *testing.T) {
	c := &fakeClient{}
	m, _ := newModel(c, Run{Job: "a", Ref: jenkins.QueueRef{ID: 1}})
	if m.interval() != DefaultFastInterval {
		t.Fatalf("queued => fast, got %v", m.interval())
	}
	m.runs[0].phase = phRunning
	if m.interval() != DefaultFastInterval {
		t.Fatal("running => fast")
	}
	m.runs[0].phase = phFinished
	if m.interval() != DefaultSlowInterval {
		t.Fatalf("finished => slow, got %v", m.interval())
	}
	m2, _ := newModel(c, Run{Job: "a", Err: errors.New("x")}, Run{Job: "b", Ref: jenkins.QueueRef{ID: 2}})
	if m2.interval() != DefaultFastInterval {
		t.Fatal("any queued => fast")
	}
	m2.runs[1].phase = phLeftQueue
	if m2.interval() != DefaultSlowInterval {
		t.Fatal("failed start + left queue => slow")
	}
}

func TestStaleTickAndResultDropped(t *testing.T) {
	c := &fakeClient{queue: map[int]jenkins.QueueStatus{1: {State: jenkins.QueueWaiting, Why: "busy"}}}
	m, rec := newModel(c, Run{Job: "a", Ref: jenkins.QueueRef{ID: 1}})
	_, cmd := m.Update(tickMsg{gen: 0})
	gen := m.gen
	if gen == 0 || cmd == nil {
		t.Fatal("tick should start a cycle with a new generation")
	}
	if _, c := m.Update(tickMsg{gen: 0}); c != nil || m.gen != gen {
		t.Fatal("stale tick must be dropped")
	}
	m.Update(resultMsg{gen: gen - 1, st: runState{run: Run{Job: "a"}, rev: m.runs[0].rev, phase: phFinished}})
	if m.runs[0].phase == phFinished {
		t.Fatal("stale result applied")
	}
	// Esc invalidates in-flight work.
	_, pop := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := pop().(core.Pop); !ok {
		t.Fatal("esc should pop")
	}
	m.Update(resultMsg{gen: gen, st: runState{run: Run{Job: "a"}, rev: m.runs[0].rev, phase: phFinished}})
	if m.runs[0].phase == phFinished || len(*rec) != 0 {
		t.Fatal("result after pop must be dropped and not reschedule")
	}
	if _, c := m.Update(tickMsg{gen: gen}); c != nil {
		t.Fatal("tick after pop must be dropped")
	}
}

func TestLifecycleAndContext(t *testing.T) {
	c := &fakeClient{
		queue:  map[int]jenkins.QueueStatus{1: {State: jenkins.QueueWaiting, Why: "waiting for executor"}},
		builds: map[string]map[int]jenkins.Build{"job": {}},
		stages: []jenkins.Stage{{Name: "Build", Status: jenkins.StageSuccess}, {Name: "Test", Status: jenkins.StageRunning}},
	}
	m, rec := newModel(c, Run{Job: "job", Ref: jenkins.QueueRef{ID: 1}})
	cycle(m)
	if m.runs[0].phase != phQueued || m.runs[0].why != "waiting for executor" || !c.deadline {
		t.Fatalf("queued: %+v deadline=%v", m.runs[0], c.deadline)
	}
	if (*rec)[0].d != DefaultFastInterval {
		t.Fatalf("next interval %v", (*rec)[0].d)
	}
	c.queue[1] = jenkins.QueueStatus{State: jenkins.QueueStarted, BuildNumber: 7}
	c.builds["job"][7] = jenkins.Build{Number: 7, Building: true, Timestamp: t0}
	cycle(m)
	if m.runs[0].phase != phRunning || m.runs[0].num != 7 || len(m.runs[0].stages) != 2 {
		t.Fatalf("running: %+v", m.runs[0])
	}
	v := ansi.Strip(shown(m))
	for _, w := range []string{"RUNNING", "job", "#7", "1m00s", "✔ Build → ⟳ Test"} {
		if !strings.Contains(v, w) {
			t.Errorf("view missing %q:\n%s", w, v)
		}
	}
	for _, res := range []string{"SUCCESS", "FAILURE", "UNSTABLE", "ABORTED"} {
		b := jenkins.Build{Number: 7, Result: res, Duration: 90 * time.Second, Timestamp: t0}
		c.builds["job"][7], c.builds["job"][0] = b, b
		testClock.t = testClock.t.Add(DefaultSlowInterval)
		cycle(m)
		if m.runs[0].phase != phFinished || m.runs[0].result != res {
			t.Fatalf("%s: %+v", res, m.runs[0])
		}
		if v := ansi.Strip(shown(m)); !strings.Contains(v, map[string]string{"FAILURE": "FAILED"}[res]+strings.Replace(res, "FAILURE", "", 1)) || !strings.Contains(v, "1m30s") {
			t.Errorf("finished view %s: %s", res, v)
		}
	}
	if last := (*rec)[len(*rec)-1]; last.d != DefaultSlowInterval {
		t.Fatalf("all finished => slow, got %v", last.d)
	}
	// Slow polling still catches status changes.
	c.builds["job"][0] = jenkins.Build{Number: 7, Result: "FAILURE"}
	testClock.t = testClock.t.Add(DefaultSlowInterval)
	cycle(m)
	if m.runs[0].result != "FAILURE" {
		t.Fatal("finished run not re-polled in slow mode")
	}
}

func TestQueueGoneFallback(t *testing.T) {
	gone := map[int]error{1: jenkins.ErrQueueItemGone, 2: jenkins.ErrQueueItemGone}
	c := &fakeClient{queueErr: gone, builds: map[string]map[int]jenkins.Build{
		"fresh": {0: {Number: 3, Result: "SUCCESS", Timestamp: t0.Add(2 * time.Second)}, 3: {Number: 3, Result: "SUCCESS", Timestamp: t0.Add(2 * time.Second)}},
		"old":   {0: {Number: 9, Result: "SUCCESS", Timestamp: t0.Add(-time.Hour)}},
	}, stageErr: jenkins.ErrNoStages}
	m, _ := newModel(c, Run{Job: "fresh", Ref: jenkins.QueueRef{ID: 1}}, Run{Job: "old", Ref: jenkins.QueueRef{ID: 2}})
	cycle(m)
	if m.runs[0].phase != phFinished || m.runs[0].num != 3 || c.stageN["fresh"] != 0 {
		t.Fatalf("fresh: %+v", m.runs[0])
	}
	if m.runs[1].phase != phLeftQueue {
		t.Fatalf("old: %+v", m.runs[1])
	}
	v := ansi.Strip(shown(m))
	if !strings.Contains(v, "left queue") || strings.Contains(v, "→") {
		t.Fatalf("view:\n%s", v)
	}
}

func TestCancelledAndFailedStart(t *testing.T) {
	c := &fakeClient{queue: map[int]jenkins.QueueStatus{1: {State: jenkins.QueueCancelled}}}
	m, _ := newModel(c, Run{Job: "a", Ref: jenkins.QueueRef{ID: 1}}, Run{Job: "b", Err: errors.New("boom")})
	cycle(m)
	if m.runs[0].result != "ABORTED" || m.runs[1].phase != phFailedStart {
		t.Fatalf("%+v", m.runs)
	}
	if v := ansi.Strip(shown(m)); !strings.Contains(v, "failed to start: boom") {
		t.Fatal(v)
	}
}

func TestTransientErrorKeepsState(t *testing.T) {
	c := &fakeClient{queueErr: map[int]error{1: errors.New("net down")}}
	m, rec := newModel(c, Run{Job: "a", Ref: jenkins.QueueRef{ID: 1}})
	cycle(m)
	if m.runs[0].phase != phQueued || m.runs[0].pollErr == nil || len(*rec) != 1 {
		t.Fatalf("%+v", m.runs[0])
	}
}

func TestNavigationDetailsAndTruncation(t *testing.T) {
	c := &fakeClient{
		queue:  map[int]jenkins.QueueStatus{1: {State: jenkins.QueueStarted, BuildNumber: 1}, 2: {State: jenkins.QueueStarted, BuildNumber: 1}},
		builds: map[string]map[int]jenkins.Build{"a": {1: {Number: 1, Building: true}}, "b": {1: {Number: 1, Building: true}}},
		stages: []jenkins.Stage{
			{Name: "Build", Status: jenkins.StageSuccess, Duration: 5 * time.Second},
			{Name: "Verify", Status: jenkins.StageRunning, Children: []jenkins.Stage{{Name: "unit", Status: jenkins.StageSuccess}, {Name: "it", Status: jenkins.StageRunning}}},
			{Name: "Deploy-to-production-environment", Status: jenkins.StageNotExecuted},
		},
	}
	m, _ := newModel(c, Run{Job: "a", Ref: jenkins.QueueRef{ID: 1}}, Run{Job: "b", Ref: jenkins.QueueRef{ID: 2}})
	cycle(m)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("cursor %d", m.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Fatal("up")
	}
	v := m.View() // the body; the Router fits the subtitle to the title row
	if !strings.Contains(ansi.Strip(v), "Verify [✔ unit ‖ ⟳ it]") {
		t.Fatalf("parallel rendering:\n%s", ansi.Strip(v))
	}
	for _, l := range strings.Split(v, "\n") {
		if ansi.StringWidth(l) > 60 {
			t.Fatalf("line wider than 60: %q", l)
		}
	}
	if !strings.Contains(ansi.Strip(v), "…") {
		t.Fatal("expected ellipsis on narrow terminal")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if d := ansi.Strip(shown(m)); !m.details || !strings.Contains(d, "5s") || !strings.Contains(d, "Deploy-to-production") {
		t.Fatalf("details:\n%s", d)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.details {
		t.Fatal("enter should toggle details off")
	}
	_, q := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if _, ok := q().(core.Quit); !ok {
		t.Fatal("q should quit")
	}
}

func TestEmptyRunsKeepsScheduling(t *testing.T) {
	m, rec := newModel(&fakeClient{})
	cycle(m)
	if len(*rec) != 1 || (*rec)[0].d != DefaultSlowInterval {
		t.Fatalf("%v", *rec)
	}
	_ = shown(m)
}

// One long-lived monitor can be left and reopened: its runs and resolved build
// numbers survive, and runs added later are tracked alongside them.
func TestReopenKeepsStateAndAddsRuns(t *testing.T) {
	c := &fakeClient{
		queue:    map[int]jenkins.QueueStatus{1: {State: jenkins.QueueStarted, BuildNumber: 7}},
		builds:   map[string]map[int]jenkins.Build{"a": {7: {Number: 7, Result: "", Building: true, Timestamp: t0}}},
		stageErr: jenkins.ErrNoStages,
	}
	m, _ := newModel(c, Run{Job: "a", Ref: jenkins.QueueRef{ID: 1}})
	cycle(m)
	if m.runs[0].num != 7 || m.runs[0].phase != phRunning {
		t.Fatalf("first cycle: %+v", m.runs[0])
	}

	// Leave the screen (esc), then come back: state is intact and Init polls again.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.runs[0].num != 7 {
		t.Fatal("leaving must not lose the resolved build number")
	}
	if cmd := m.Init(); cmd == nil {
		t.Fatal("reopening must restart polling")
	}

	// A later trigger adds its runs to the same monitor.
	if !Add(m, []Run{{Job: "b", Ref: jenkins.QueueRef{ID: 2}}, {Job: "c", Err: errors.New("boom")}}) {
		t.Fatal("Add should accept a monitor")
	}
	if len(m.runs) != 3 || m.runs[2].phase != phFailedStart || m.runs[1].at.IsZero() {
		t.Fatalf("added runs: %+v", m.runs)
	}
	if Add(nil, nil) {
		t.Fatal("Add must reject non-monitors")
	}
}

// In the details view esc returns to the runs list; only from the list does
// it leave the monitor.
func TestEscInDetailsReturnsToList(t *testing.T) {
	c := &fakeClient{stageErr: jenkins.ErrNoStages}
	m, _ := newModel(c, Run{Job: "a", Err: errors.New("x")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.details {
		t.Fatal("enter should open details")
	}
	if got := m.Keys(); !strings.Contains(got[len(got)-2].Help().Desc, "list") {
		t.Fatalf("details help should say esc goes back to the list: %v", got)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.details || cmd != nil {
		t.Fatalf("esc in details should only close details (details=%v cmd=%v)", m.details, cmd != nil)
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc in the list should leave the monitor")
	}
	if _, ok := cmd().(core.Pop); !ok {
		t.Fatal("expected Pop")
	}
}

func ok(n int) jenkins.Build {
	return jenkins.Build{Number: n, Result: "SUCCESS", Duration: time.Second, Timestamp: t0}
}

// collClient: a=SUCCESS, b=FAILURE, c=running, d=never built, e=SUCCESS.
func collClient() *fakeClient {
	return &fakeClient{
		builds: map[string]map[int]jenkins.Build{
			"a": {0: ok(1)},
			"b": {0: {Number: 4, Result: "FAILURE", Timestamp: t0}, 4: {Number: 4, Result: "FAILURE", Timestamp: t0}},
			"c": {0: {Number: 2, Building: true, Timestamp: t0}},
			"e": {0: ok(5)},
		},
		stages: []jenkins.Stage{{Name: "Build", Status: jenkins.StageFailed}},
	}
}

var members = []string{"a", "b", "c", "d", "e", "gone"}

func collMonitor(c *fakeClient) (*model, *clock, *[]sched) {
	m, clk, rec := newColl(c, members, map[string]bool{"gone": true})
	cycle(m)
	return m, clk, rec
}

func TestCollectionInitialSnapshot(t *testing.T) {
	c := collClient()
	m, _, rec := collMonitor(c)
	if m.Title() != "Run Monitor: Nightly" {
		t.Fatal(m.Title())
	}
	want := map[string]phase{"a": phFinished, "b": phFinished, "c": phRunning, "d": phNotBuilt, "e": phFinished, "gone": phMissing}
	for _, r := range m.runs {
		if r.phase != want[r.run.Job] {
			t.Errorf("%s: phase %v want %v", r.run.Job, r.phase, want[r.run.Job])
		}
	}
	if c.buildN["gone"] != 0 || c.stageN["gone"] != 0 {
		t.Fatal("missing job must not be polled")
	}
	if (*rec)[0].d != DefaultFastInterval {
		t.Fatalf("a running row => fast, got %v", (*rec)[0].d)
	}
	v := ansi.Strip(shown(m))
	for _, w := range []string{"NOT BUILT", "MISSING", "showing all", "4 remaining", "RUNNING", "FAILED"} {
		if !strings.Contains(v, w) {
			t.Errorf("view missing %q:\n%s", w, v)
		}
	}
}

func TestSuccessFilterToggleAndCursorMapping(t *testing.T) {
	m, _, _ := collMonitor(collClient())
	HideSuccess(m, true)
	vis := m.visible()
	var names []string
	for _, i := range vis {
		names = append(names, m.runs[i].run.Job)
	}
	if strings.Join(names, ",") != "b,c,d,gone" {
		t.Fatalf("visible %v", names)
	}
	v := ansi.Strip(shown(m))
	if !strings.Contains(v, "hiding 2 successful (s to show)") || !strings.Contains(v, "4 remaining") || strings.Contains(v, "  a  ") {
		t.Fatalf("filtered view:\n%s", v)
	}
	// Cursor 1 is "c" on the filtered view, and details follow it.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.runs[m.selected()].run.Job != "c" {
		t.Fatalf("cursor maps to %q", m.runs[m.selected()].run.Job)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if d := ansi.Strip(shown(m)); !strings.Contains(d, "c #2") {
		t.Fatalf("details of wrong row:\n%s", d)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Down never goes past the visible rows.
	for range 10 {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != 3 {
		t.Fatalf("cursor %d", m.cursor)
	}
	// Toggle shows everything and keeps the cursor on the same job.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if m.hideSuccess || len(m.visible()) != 6 || m.runs[m.selected()].run.Job != "gone" {
		t.Fatalf("toggle: hide=%v vis=%d", m.hideSuccess, len(m.visible()))
	}
	if v := ansi.Strip(shown(m)); !strings.Contains(v, "showing all (s to hide successful)") {
		t.Fatal(v)
	}
}

func TestAllSucceededNotice(t *testing.T) {
	c := &fakeClient{builds: map[string]map[int]jenkins.Build{"a": {0: ok(1)}, "b": {0: ok(2)}}}
	m, _, _ := newColl(c, []string{"a", "b"}, nil)
	HideSuccess(m, true)
	cycle(m)
	v := ansi.Strip(shown(m))
	if !strings.Contains(v, "All 2 jobs succeeded") || !strings.Contains(v, "s to show") {
		t.Fatalf("notice:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // nothing selected: no details
	if m.details {
		t.Fatal("no details on an empty view")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if v := ansi.Strip(shown(m)); !strings.Contains(v, "a") || strings.Contains(v, "succeeded") {
		t.Fatalf("after s:\n%s", v)
	}
}

func TestTriggerAddReplacesRowAndShowsAll(t *testing.T) {
	c := collClient()
	c.queue = map[int]jenkins.QueueStatus{9: {State: jenkins.QueueWaiting, Why: "busy"}}
	m, _, _ := collMonitor(c)
	HideSuccess(m, true)
	if !Add(m, []Run{{Job: "a", Ref: jenkins.QueueRef{ID: 9}}}) || !HideSuccess(m, false) {
		t.Fatal("Add/HideSuccess should accept a monitor")
	}
	if len(m.runs) != 6 || m.runs[0].run.Job != "a" || m.runs[0].phase != phQueued || m.runs[0].num != 0 {
		t.Fatalf("row not replaced in place: %+v", m.runs[0])
	}
	cycle(m)
	if m.runs[0].phase != phQueued || m.runs[0].why != "busy" {
		t.Fatalf("queue ref not resolved: %+v", m.runs[0])
	}
	if v := ansi.Strip(shown(m)); !strings.Contains(v, "QUEUED") {
		t.Fatal(v)
	}
	// Triggering a job that is not a row (yet) appends one; a failed start shows.
	Add(m, []Run{{Job: "z", Err: errors.New("boom")}})
	if m.runs[len(m.runs)-1].phase != phFailedStart {
		t.Fatal("appended row")
	}
}

func TestSetJobsKeepsStateAddsAndDrops(t *testing.T) {
	c := collClient()
	m, _, _ := collMonitor(c)
	rev := m.runs[1].rev // b
	SetJobs(m, []string{"c", "b", "new", "gone"}, map[string]bool{"gone": true})
	var names []string
	for _, r := range m.runs {
		names = append(names, r.run.Job)
	}
	if strings.Join(names, ",") != "c,b,new,gone" {
		t.Fatalf("rows %v", names)
	}
	if m.runs[1].rev != rev || m.runs[1].result != "FAILURE" || m.runs[0].phase != phRunning {
		t.Fatal("existing rows must keep their state")
	}
	if m.runs[2].phase != phUnpolled {
		t.Fatal("new member starts unpolled")
	}
	// A previously missing job that is back in the catalog becomes pollable.
	SetJobs(m, []string{"c", "b", "new", "gone"}, nil)
	if m.runs[3].phase != phUnpolled || !m.due(m.runs[3], t0) {
		t.Fatal("job no longer missing should be polled again")
	}
	// A result in flight for a dropped row does not break the cycle.
	_, cmd := m.Update(tickMsg{gen: m.gen})
	SetJobs(m, []string{"c"}, nil)
	deliver(m, cmd)
	if m.pending != 0 || len(m.runs) != 1 {
		t.Fatalf("pending=%d rows=%d", m.pending, len(m.runs))
	}
	if !SetJobs(m, nil, nil) || len(m.runs) != 0 || m.cursor != 0 {
		t.Fatal("empty collection")
	}
	_ = shown(m)
}

func TestSlowAndFastPollingDueness(t *testing.T) {
	c := collClient()
	m, clk, rec := collMonitor(c)
	base := map[string]int{}
	for k, v := range c.buildN {
		base[k] = v
	}
	// 3s later: only the running row is due.
	clk.t = clk.t.Add(DefaultFastInterval)
	cycle(m)
	for _, j := range []string{"a", "b", "d", "e"} {
		if c.buildN[j] != base[j] {
			t.Errorf("%s polled early (%d -> %d)", j, base[j], c.buildN[j])
		}
	}
	if c.buildN["c"] != base["c"]+1 {
		t.Fatal("running row must be polled at the fast interval")
	}
	// Slow interval elapsed: everything pollable is polled; missing never.
	clk.t = clk.t.Add(DefaultSlowInterval)
	cycle(m)
	for _, j := range []string{"a", "b", "d", "e"} {
		if c.buildN[j] != base[j]+1 {
			t.Errorf("%s not polled after slow interval", j)
		}
	}
	if c.buildN["gone"] != 0 {
		t.Fatal("missing polled")
	}
	// Once nothing runs the next tick is slow.
	c.builds["c"][0] = ok(2)
	clk.t = clk.t.Add(DefaultFastInterval)
	cycle(m)
	if last := (*rec)[len(*rec)-1]; last.d != DefaultSlowInterval {
		t.Fatalf("nothing live => slow, got %v", last.d)
	}
	// A failed build running again flips the interval back to fast.
	c.builds["b"][0] = jenkins.Build{Number: 5, Building: true, Timestamp: t0}
	clk.t = clk.t.Add(DefaultSlowInterval)
	cycle(m)
	if last := (*rec)[len(*rec)-1]; last.d != DefaultFastInterval || m.runs[1].num != 5 {
		t.Fatalf("new build => fast, got %v", last.d)
	}
	// Reopening polls every row immediately.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	n := c.buildN["a"]
	m.Init()
	cycle(m)
	if c.buildN["a"] != n+1 {
		t.Fatal("Init must poll every row")
	}
}

func TestStagesOnlyForRunningAndFailedDetails(t *testing.T) {
	c := collClient()
	m, _, _ := collMonitor(c)
	if c.stageN["c"] != 1 || c.stageN["a"] != 0 || c.stageN["b"] != 0 || c.stageN["e"] != 0 {
		t.Fatalf("stage calls %v", c.stageN)
	}
	// Opening details of the failed row (b, index 1) fetches its stages once.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliver(m, cmd)
	if c.stageN["b"] != 1 || len(m.runs[1].stages) != 1 {
		t.Fatalf("details of failed row: %v %+v", c.stageN, m.runs[1].stages)
	}
	if d := ansi.Strip(shown(m)); !strings.Contains(d, "✘ Build") {
		t.Fatalf("details:\n%s", d)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || c.stageN["b"] != 1 {
		t.Fatal("stages of a finished row are fetched once")
	}
	// Successful rows never fetch stages.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || c.stageN["a"] != 0 {
		t.Fatal("success row fetched stages")
	}
	// ErrNoStages falls back silently.
	c2 := collClient()
	c2.stageErr = jenkins.ErrNoStages
	m2, _, _ := collMonitor(c2)
	if !m2.runs[2].noStages || m2.runs[2].pollErr != nil {
		t.Fatalf("ErrNoStages: %+v", m2.runs[2])
	}
	m2.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliver(m2, cmd)
	if d := ansi.Strip(shown(m2)); !strings.Contains(d, "No pipeline stages") {
		t.Fatalf("details:\n%s", d)
	}
}

func TestStaleTicksDroppedInCollection(t *testing.T) {
	c := collClient()
	m, _, rec := newColl(c, members, nil)
	_, cmd := m.Update(tickMsg{gen: 0})
	g := m.gen
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // leaving invalidates the cycle
	deliver(m, cmd)                        // results from the old generation
	if len(*rec) != 0 || m.runs[0].phase != phUnpolled {
		t.Fatalf("stale results applied: %+v", m.runs[0])
	}
	if _, c := m.Update(tickMsg{gen: g}); c != nil {
		t.Fatal("stale tick")
	}
}

func TestLoadingModalThenHeaderSpinner(t *testing.T) {
	c := collClient()
	m, _, _ := newColl(c, []string{"a", "c"}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, cmd := m.Update(tickMsg{gen: m.gen})
	if !m.load.Active() {
		t.Fatal("cycle in flight must activate the spinner")
	}
	v := ansi.Strip(shown(m))
	if !strings.Contains(v, "Loading Nightly") || strings.Contains(v, "showing all") {
		t.Fatalf("first cycle must show the modal only:\n%s", v)
	}
	// Deliver one of the two results: data is visible, cycle still pending.
	msgs := cmd().(tea.BatchMsg)
	var results []tea.Msg
	for _, bc := range msgs {
		if r, ok := bc().(resultMsg); ok {
			results = append(results, r)
		}
	}
	m.Update(results[0])
	v = ansi.Strip(shown(m))
	if strings.Contains(v, "Loading Nightly") || !strings.Contains(v, "refreshing…") {
		t.Fatalf("pending cycle with data must show the header spinner:\n%s", v)
	}
	m.Update(results[1])
	if m.load.Active() || strings.Contains(ansi.Strip(shown(m)), "refreshing…") {
		t.Fatal("spinner must stop when the cycle ends")
	}
	if cmd := m.load.Update(spinner.TickMsg{}); cmd != nil {
		t.Fatal("idle spinner must not tick")
	}
	// A later cycle over existing data: header spinner, rows never hidden.
	m.Update(tickMsg{gen: m.gen})
	v = ansi.Strip(shown(m))
	if !strings.Contains(v, "refreshing…") || !strings.Contains(v, "RUNNING") {
		t.Fatalf("refresh must keep rows and show the spinner:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.load.Active() {
		t.Fatal("leaving must stop the spinner")
	}
}

func keyR() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}} }

func TestRefreshKeyPollsAllPollableRowsAndShowsSpinner(t *testing.T) {
	c := collClient()
	m, _, rec := collMonitor(c)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if !strings.Contains(ansi.Strip(strings.Join(helpKeys(m), " ")), "R refresh") {
		t.Fatalf("help bar lacks R refresh: %v", helpKeys(m))
	}
	c.buildN = nil // forget the first cycle; no time has passed, so slow rows are not due
	before := len(*rec)
	_, cmd := m.Update(keyR())
	if !m.load.Active() || !strings.Contains(ansi.Strip(shown(m)), "refreshing…") {
		t.Fatal("R must show the header spinner")
	}
	exec(m, cmd)
	for _, j := range []string{"a", "b", "c", "d", "e"} {
		if c.buildN[j] != 1 {
			t.Fatalf("row %s polled %d times, want 1 (R ignores due-ness)", j, c.buildN[j])
		}
	}
	if c.buildN["gone"] != 0 {
		t.Fatal("missing row must not be polled")
	}
	if len(*rec) != before+1 || (*rec)[len(*rec)-1].gen != m.gen {
		t.Fatalf("exactly one tick must be scheduled after the manual cycle: %v", *rec)
	}
}

func TestRefreshSkipsRowsThatMustNotBeRepolled(t *testing.T) {
	c := collClient()
	m, _, _ := collMonitor(c)
	m.runs[0].phase = phLeftQueue
	m.runs[1].phase, m.runs[1].triggered, m.runs[1].num = phFinished, true, 0 // cancelled
	m.runs[2].phase = phFailedStart
	c.buildN = nil
	_, cmd := m.Update(keyR())
	exec(m, cmd)
	for _, j := range []string{"a", "b", "c"} {
		if c.buildN[j] != 0 {
			t.Fatalf("row %s must not be re-polled", j)
		}
	}
}

func TestRefreshIgnoredInDetailsAndBehindFirstLoadModal(t *testing.T) {
	c := collClient()
	m, _, _ := collMonitor(c)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.details {
		t.Fatal("details must be open")
	}
	g := m.gen
	if _, cmd := m.Update(keyR()); cmd != nil || m.gen != g {
		t.Fatal("R must be ignored while details are open")
	}
	m2, _, _ := newColl(c, members, nil)
	m2.Update(tickMsg{gen: m2.gen}) // first cycle in flight: modal up
	g = m2.gen
	if _, cmd := m2.Update(keyR()); cmd != nil || m2.gen != g {
		t.Fatal("R must be ignored behind the first-load modal")
	}
}

func TestRefreshDropsStaleCycleAndKeepsOneTickChain(t *testing.T) {
	c := collClient()
	m, _, rec := collMonitor(c)
	oldGen := m.gen
	_, oldCmd := m.Update(tickMsg{gen: m.gen}) // automatic cycle in flight
	_, cmd := m.Update(keyR())
	n := len(*rec)
	if _, c2 := m.Update(tickMsg{gen: oldGen}); c2 != nil {
		t.Fatal("old tick must be dropped")
	}
	exec(m, oldCmd) // stale results
	if len(*rec) != n {
		t.Fatal("stale cycle must not schedule a tick")
	}
	exec(m, cmd)
	if len(*rec) != n+1 || (*rec)[n].gen != m.gen {
		t.Fatalf("one tick chain expected: %v", *rec)
	}
}

func TestRefreshWithNothingPollableSchedulesOnce(t *testing.T) {
	m, _, rec := newColl(collClient(), []string{"x"}, map[string]bool{"x": true})
	m.Update(tickMsg{gen: m.gen})
	n := len(*rec)
	if _, cmd := m.Update(keyR()); cmd == nil || len(*rec) != n+1 {
		t.Fatalf("empty manual cycle must schedule exactly one tick: %v", *rec)
	}
}

func TestFastAutomaticCycleKeepsSpinnerUntilMinimum(t *testing.T) {
	c := collClient()
	m, clk, _ := collMonitor(c)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, cmd := m.Update(tickMsg{gen: m.gen})
	exec(m, cmd) // cycle finishes instantly
	if !m.load.Active() || !strings.Contains(ansi.Strip(shown(m)), "refreshing…") {
		t.Fatal("a fast cycle must still flash the spinner")
	}
	clk.t = clk.t.Add(time.Second)
	if c := m.load.Update(spinner.TickMsg{}); c != nil || m.load.Active() {
		t.Fatal("spinner must end after the minimum")
	}
}

func helpKeys(m *model) []string {
	var out []string
	for _, k := range m.Keys() {
		out = append(out, k.Help().Key+" "+k.Help().Desc)
	}
	return out
}

// shown is what the monitor puts on the terminal: its subtitle on the title
// row, then the body.
func shown(s core.Screen) string {
	if st, ok := s.(core.Subtitler); ok {
		return st.Subtitle() + "\n" + s.View()
	}
	return s.View()
}

func TestTitleNamesParent(t *testing.T) {
	for _, tc := range []struct{ parent, name, want string }{
		{"", "", "Run Monitor"},
		{"", "Ops", "Run Monitor: Ops"},
		{"team", "Ops", "Run Monitor · team · Ops"},
		{"team/", "", "Run Monitor · team"},
	} {
		m := NewCollection(Deps{Parent: tc.parent}, tc.name, nil, nil)
		if got := m.Title(); got != tc.want {
			t.Errorf("Title(parent %q, name %q) = %q, want %q", tc.parent, tc.name, got, tc.want)
		}
	}
}

// In the details view the arrow keys move between stages, not jobs.
func TestDetailsArrowsMoveStages(t *testing.T) {
	m := &model{th: core.NewTheme(), details: true, cursor: 0}
	m.runs = []runState{{run: Run{Job: "a"}, phase: phFinished, result: "FAILURE", stages: []jenkins.Stage{
		{Name: "Build", Status: jenkins.StageSuccess},
		{Name: "Test", Status: jenkins.StageFailed, Children: []jenkins.Stage{{Name: "unit", Status: jenkins.StageFailed}}},
	}}, {run: Run{Job: "b"}, phase: phFinished, result: "FAILURE"}}
	down := tea.KeyMsg{Type: tea.KeyDown}
	for range 5 {
		m.Update(down)
	}
	if m.cursor != 0 || m.stageCur != 2 {
		t.Fatalf("job cursor %d, stage cursor %d", m.cursor, m.stageCur)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "▸     ✘ unit") {
		t.Fatalf("marker missing:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.stageCur != 1 {
		t.Fatalf("stage cursor %d", m.stageCur)
	}
}

func logTail(t *testing.T) (*model, *fakeClient) {
	t.Helper()
	c := collClient()
	m, _, _ := collMonitor(c)
	m.Update(tea.KeyMsg{Type: tea.KeyDown}) // b: failed build with stages
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliver(m, cmd)
	return m, c
}

func pressL(m *model) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	return cmd
}

func TestLogOpensFromDetailsOnly(t *testing.T) {
	c := collClient()
	m, _, _ := collMonitor(c)
	if pressL(m) != nil || m.log != nil {
		t.Fatal("l does nothing in the runs list")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	c.log = "line one\nline two\n"
	cmd := pressL(m)
	if m.log == nil || cmd == nil {
		t.Fatal("l in details opens the log and fetches it")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "loading…") {
		t.Fatalf("before the first chunk:\n%s", v)
	}
	m.Update(cmd())
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "line one") || !strings.Contains(v, "line two") || !strings.Contains(v, "finished") {
		t.Fatalf("log:\n%s", v)
	}
	if st := m.Subtitle(); st != "" {
		t.Fatalf("no subtitle over the log: %q", st)
	}
	// esc closes the log and stays in the details view; a second esc leaves details.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.log != nil || !m.details {
		t.Fatalf("esc should return to details (log=%v details=%v)", m.log != nil, m.details)
	}
}

func TestLogFollowsGrowingOutput(t *testing.T) {
	m, c := logTail(t)
	c.log, c.logMore = "a\nb", true
	m.Update(pressL(m)())
	if !m.log.more || m.log.part != "b" || len(m.log.lines) != 1 {
		t.Fatalf("partial line must wait for its newline: %+v", m.log)
	}
	// A later chunk continues from the returned offset and completes the line.
	c.log, c.logMore = "a\nbc\nd\n", false
	m.Update(logTickMsg{gen: m.logGen})
	_, _ = m.Update(m.fetchLog(m.log.next)())
	if got := strings.Join(m.log.all(), "|"); got != "a|bc|d" || m.log.more {
		t.Fatalf("lines %q more=%v", got, m.log.more)
	}
	if want := []int64{0, 3}; !(len(c.logStarts) >= 2 && c.logStarts[0] == want[0] && c.logStarts[1] == want[1]) {
		t.Fatalf("offsets %v", c.logStarts)
	}
}

func TestLogScrollPausesFollowAndGResumes(t *testing.T) {
	m, c := logTail(t)
	m.h = 7 // 5 log lines
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&sb, "row %d\n", i)
	}
	c.log, c.logMore = sb.String(), true
	m.Update(pressL(m)())
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "row 20") || strings.Contains(v, "row 15\n") || !strings.Contains(v, "following") {
		t.Fatalf("follows the tail:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if v = ansi.Strip(m.View()); m.log.follow || !strings.Contains(v, "row 15") || strings.Contains(v, "row 20") || !strings.Contains(v, "paused") {
		t.Fatalf("up pauses:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if v = ansi.Strip(m.View()); !strings.Contains(v, "row 1\n") {
		t.Fatalf("g goes to the top:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if v = ansi.Strip(m.View()); !m.log.follow || !strings.Contains(v, "row 20") {
		t.Fatalf("G follows again:\n%s", v)
	}
	// Scrolling down to the end resumes following too.
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if !m.log.follow {
		t.Fatal("paging back to the end follows")
	}
}

func TestLogCleansConsoleText(t *testing.T) {
	lv := &logView{}
	lv.append("\x1b[32mgreen\x1b[0m\r\n\x1b[8mha:////4abc==\x1b[0mx ok\nprogress 10%\rprogress 90%")
	if got := strings.Join(lv.all(), "|"); got != "green|x ok|progress 90%" {
		t.Fatalf("got %q", got)
	}
}

func TestLogErrorsAndLifecycle(t *testing.T) {
	m, c := logTail(t)
	c.logErr = jenkins.ErrBuildNotFound
	cmd := pressL(m)
	if _, next := m.Update(cmd()); next != nil {
		t.Fatal("a vanished build is not retried")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "error:") {
		t.Fatalf("error shown:\n%s", v)
	}
	// Messages of a closed log are dropped.
	gen := m.logGen
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	c.logErr = nil
	m.Update(logMsg{gen: gen, chunk: jenkins.LogChunk{Text: "late\n"}})
	if m.log != nil {
		t.Fatal("stale chunk must not reopen the log")
	}
	// Leaving the monitor while the log is open closes it.
	pressL(m)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})                            // closes log
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil { // leaves details
		t.Fatal("esc from details does not pop")
	}
}
