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
