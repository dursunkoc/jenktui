package monitor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

var _ interface{ Pause() } = (*model)(nil)

func TestPauseDropsAPollInFlightAndResumePollsAgain(t *testing.T) {
	c := &rerunClient{}
	m := rerunModel(c, true, new([]map[string]string))
	_, inflight := m.Update(tickMsg{gen: m.gen}) // a poll starts
	m.Pause()                                    // a palette covers the monitor
	c.fakeClient.builds["api/deploy"] = map[int]jenkins.Build{0: {Number: 58, Result: "SUCCESS"}}
	deliverAll(m, inflight)
	if m.runs[0].num != 57 {
		t.Fatalf("a result from before the pause was applied: #%d", m.runs[0].num)
	}
	deliverAll(m, m.Resume())
	if m.runs[0].num != 58 {
		t.Fatalf("polling did not resume: #%d", m.runs[0].num)
	}
}

func TestPauseKeepsAnOpenLogAndResumeRestartsItsPolling(t *testing.T) {
	c := &rerunClient{}
	m := rerunModel(c, true, new([]map[string]string))
	c.fakeClient.log = "line one\n"
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	deliverAll(m, pressKey(m, 'L'))
	if m.log == nil {
		t.Fatal("log did not open")
	}
	genBefore := m.logGen
	m.Pause()
	if m.log == nil {
		t.Fatal("pause closed the log")
	}
	c.fakeClient.log = "line one\nline two\n"
	deliverAll(m, m.Resume())
	if m.log == nil || m.logGen == genBefore {
		t.Fatal("resume must keep the log open and restart its fetch")
	}
	if got := len(m.log.lines); got != 2 {
		t.Fatalf("log lines = %d, want the new line picked up", got)
	}
}

func TestTwoMonitorsDoNotAcceptEachOthersMessages(t *testing.T) {
	ca, cb := &rerunClient{}, &rerunClient{}
	a := rerunModel(ca, true, new([]map[string]string))
	b := rerunModel(cb, true, new([]map[string]string))
	if a.gen == b.gen || a.epoch == b.epoch || a.logGen == b.logGen || a.runs[0].rev == b.runs[0].rev {
		t.Fatalf("counters overlap: gen %d/%d rev %d/%d", a.gen, b.gen, a.runs[0].rev, b.runs[0].rev)
	}
	// A's poll result for the same job reaches B: dropped by its generation/rev guard.
	_, c := a.Update(tickMsg{gen: a.gen})
	before := b.runs[0].num
	cb.fakeClient.builds["api/deploy"] = map[int]jenkins.Build{0: {Number: 99, Result: "SUCCESS"}}
	ca.fakeClient.builds["api/deploy"] = map[int]jenkins.Build{0: {Number: 98, Result: "SUCCESS"}}
	deliverAll(b, c) // B receives what A's cycle produced
	if b.runs[0].num != before {
		t.Fatalf("B applied A's result: #%d", b.runs[0].num)
	}
}
