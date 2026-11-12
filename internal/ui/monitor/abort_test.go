package monitor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

type stopClient struct {
	*fakeClient
	stopped   []string
	cancelled []int
	err       error
}

func (c *stopClient) StopBuild(_ context.Context, job string, n int) error {
	c.stopped = append(c.stopped, fmt.Sprintf("%s#%d", job, n))
	return c.err
}
func (c *stopClient) CancelQueue(_ context.Context, id int) error {
	c.cancelled = append(c.cancelled, id)
	return c.err
}

// abortModel: row "api/deploy" runs build 57, row "api/queued" waits as queue item 42.
func abortModel(c *stopClient) *model {
	c.fakeClient = &fakeClient{
		builds: map[string]map[int]jenkins.Build{
			"api/deploy": {0: {Number: 57, Building: true}, 57: {Number: 57, Building: true}},
		},
		queue: map[int]jenkins.QueueStatus{42: {State: jenkins.QueueWaiting, Why: "busy"}},
	}
	m := New(Deps{Client: c, Now: (&clock{t: t0}).now}, []Run{
		{Job: "api/deploy", Ref: jenkins.QueueRef{ID: 41}},
		{Job: "api/queued", Ref: jenkins.QueueRef{ID: 42}},
	}).(*model)
	wire(m)
	m.runs[0].triggered, m.runs[0].num = true, 57
	cycle(m)
	return m
}

func askedText(m *model) string {
	if m.confirm == nil {
		return ""
	}
	return m.confirm.text
}

func TestAbortRunningBuildAfterConfirm(t *testing.T) {
	c := &stopClient{}
	m := abortModel(c)
	m.cursor = 0
	pressKey(m, 'X')
	if got := askedText(m); got != "Abort api/deploy #57?" {
		t.Fatalf("asked %q", got)
	}
	if len(c.stopped) != 0 {
		t.Fatal("nothing before the answer")
	}
	m.cursor = 1 // the cursor moving does not change what is aborted
	cmd := pressKey(m, 'y')
	if cmd == nil {
		t.Fatal("y runs the stop")
	}
	msg := cmd()
	if len(c.stopped) != 1 || c.stopped[0] != "api/deploy#57" {
		t.Fatalf("stopped %v", c.stopped)
	}
	gen := m.gen
	_, next := m.Update(msg)
	if m.notice != "abort requested" || m.gen == gen || next == nil {
		t.Fatalf("notice %q, gen %d->%d: the row must be polled at once", m.notice, gen, m.gen)
	}
}

func TestCancelQueuedItem(t *testing.T) {
	c := &stopClient{}
	m := abortModel(c)
	m.cursor = 1
	pressKey(m, 'X')
	if got := askedText(m); !strings.Contains(got, "api/queued") {
		t.Fatalf("asked %q", got)
	}
	pressKey(m, 'y')()
	if len(c.cancelled) != 1 || c.cancelled[0] != 42 || len(c.stopped) != 0 {
		t.Fatalf("cancelled %v stopped %v", c.cancelled, c.stopped)
	}
}

func TestAbortRefusedIsFlashed(t *testing.T) {
	c := &stopClient{err: fmt.Errorf("%w (stop)", jenkins.ErrForbidden)}
	m := abortModel(c)
	m.cursor = 0
	pressKey(m, 'X')
	msg := pressKey(m, 'y')()
	m.Update(msg)
	if m.notice != "not allowed to abort" {
		t.Fatalf("notice %q", m.notice)
	}
}

func TestAbortDeclinedDoesNothing(t *testing.T) {
	c := &stopClient{}
	m := abortModel(c)
	m.cursor = 0
	pressKey(m, 'X')
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.confirm != nil || len(c.stopped)+len(c.cancelled) != 0 {
		t.Fatal("esc dismisses without stopping")
	}
	pressKey(m, 'X')
	pressKey(m, 'n')
	if m.confirm != nil || len(c.stopped) != 0 {
		t.Fatal("n dismisses")
	}
}

func TestAbortGoneIsReportedAndPolled(t *testing.T) {
	c := &stopClient{err: jenkins.ErrBuildNotFound}
	m := abortModel(c)
	m.cursor = 0
	pressKey(m, 'X')
	_, next := m.Update(pressKey(m, 'y')())
	if !strings.Contains(m.notice, "already finished") || next == nil {
		t.Fatalf("notice %q", m.notice)
	}
}

func TestAbortStaleResultDropped(t *testing.T) {
	c := &stopClient{}
	m := abortModel(c)
	m.cursor = 0
	pressKey(m, 'X')
	msg := pressKey(m, 'y')()
	m.epoch++ // the monitor was left
	gen := m.gen
	if _, next := m.Update(msg); next != nil || m.gen != gen || m.notice != "" {
		t.Fatal("stale result must change nothing")
	}
}

func TestXOnlyOnRunningOrQueuedWithStopper(t *testing.T) {
	c := &stopClient{}
	m := abortModel(c)
	m.cursor = 0
	m.runs[0].phase = phFinished
	pressKey(m, 'X')
	if m.confirm != nil {
		t.Fatal("finished build: no prompt")
	}
	for _, k := range helpKeys(m) {
		if k == "X abort" {
			t.Fatal("X hidden for a finished build")
		}
	}
	m.runs[0].phase = phRunning
	found := false
	for _, k := range helpKeys(m) {
		found = found || k == "X abort"
	}
	if !found {
		t.Fatal("X shown for a running build")
	}
	// no stopper on the client
	m2 := failModel(failClient())
	m2.runs[1].phase = phRunning
	m2.cursor = 1
	pressKey(m2, 'X')
	if m2.confirm != nil {
		t.Fatal("no stopper: no prompt")
	}
}

func TestAbortFromDetails(t *testing.T) {
	c := &stopClient{}
	m := abortModel(c)
	m.cursor = 0
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pressKey(m, 'X')
	if askedText(m) == "" {
		t.Fatal("X works in the details view")
	}
}

func TestAbortQueueItemGoneAndCrumbMessages(t *testing.T) {
	c := &stopClient{err: jenkins.ErrQueueItemNotFound}
	m := abortModel(c)
	m.cursor = 1
	pressKey(m, 'X')
	_, next := m.Update(pressKey(m, 'y')())
	if m.notice != "no longer queued (started or cancelled)" || next == nil {
		t.Fatalf("notice %q", m.notice)
	}
	c2 := &stopClient{err: fmt.Errorf("%w (bad CSRF crumb)", jenkins.ErrForbidden)}
	m = abortModel(c2)
	m.cursor = 0
	pressKey(m, 'X')
	m.Update(pressKey(m, 'y')())
	if !strings.Contains(m.notice, "crumb") {
		t.Fatalf("notice %q", m.notice)
	}
}
