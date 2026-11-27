package monitor

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

type noteSys struct {
	fakeSys
	notes []string
	bells int
}

func (s *noteSys) Notify(title, body string) error {
	s.notes = append(s.notes, title+"|"+body)
	return nil
}
func (s *noteSys) Bell() { s.bells++ }

// feed delivers one poll result for row 0 (as the monitor's cycle would) and
// runs whatever command it returns.
func feed(m *model, phase phase, result string, num int) {
	st := m.runs[0]
	st.phase, st.result, st.num = phase, result, num
	m.pending = 1
	_, cmd := m.Update(resultMsg{gen: m.gen, st: st})
	msgs(cmd)
}

func noteModel(mode string, sys Sys) *model {
	m := NewCollection(Deps{Client: &fakeClient{}, Now: (&clock{t: t0}).now, Sys: sys, Notify: mode}, "N", []string{"api/deploy"}, nil).(*model)
	wire(m)
	m.gen = 1
	return m
}

func TestNotifyFirstLoadNeverNotifies(t *testing.T) {
	s := &noteSys{}
	m := noteModel("all", s)
	feed(m, phFinished, "FAILURE", 5) // the first result is already finished
	feed(m, phFinished, "FAILURE", 5)
	if len(s.notes) != 0 || s.bells != 0 {
		t.Fatalf("notes %v bells %d", s.notes, s.bells)
	}
}

func TestNotifyFailureRingsAndNotifiesOnce(t *testing.T) {
	s := &noteSys{}
	m := noteModel("failures", s)
	feed(m, phRunning, "", 57)
	feed(m, phRunning, "", 57)
	feed(m, phFinished, "FAILURE", 57)
	feed(m, phFinished, "FAILURE", 57) // later polls of a finished row stay quiet
	if len(s.notes) != 1 || s.notes[0] != "jenktui|FAILED api/deploy #57" || s.bells != 1 {
		t.Fatalf("notes %v bells %d", s.notes, s.bells)
	}
}

func TestNotifyFailuresModeSkipsSuccessAndAborted(t *testing.T) {
	s := &noteSys{}
	m := noteModel("", s) // empty means failures
	feed(m, phRunning, "", 1)
	feed(m, phFinished, "SUCCESS", 1)
	feed(m, phRunning, "", 2)
	feed(m, phFinished, "ABORTED", 2)
	if len(s.notes) != 0 || s.bells != 0 {
		t.Fatalf("notes %v", s.notes)
	}
	feed(m, phRunning, "", 3)
	feed(m, phFinished, "UNSTABLE", 3)
	if len(s.notes) != 1 || !strings.HasPrefix(s.notes[0], "jenktui|UNSTABLE") {
		t.Fatalf("notes %v", s.notes)
	}
}

func TestNotifyAllIncludesSuccessAndQueuedRows(t *testing.T) {
	s := &noteSys{}
	m := noteModel("all", s)
	feed(m, phQueued, "", 0)
	feed(m, phFinished, "SUCCESS", 9)
	if len(s.notes) != 1 || s.notes[0] != "jenktui|SUCCESS api/deploy #9" {
		t.Fatalf("notes %v", s.notes)
	}
}

func TestNotifyOffNeverAndNilSysIsSafe(t *testing.T) {
	s := &noteSys{}
	m := noteModel("off", s)
	feed(m, phRunning, "", 1)
	feed(m, phFinished, "FAILURE", 1)
	if len(s.notes) != 0 || s.bells != 0 {
		t.Fatal("off must be silent")
	}
	m = noteModel("all", nil)
	feed(m, phRunning, "", 1)
	feed(m, phFinished, "FAILURE", 1)
}

func TestNotifyRunsInsideACommand(t *testing.T) {
	s := &noteSys{}
	m := noteModel("failures", s)
	feed(m, phRunning, "", 4)
	st := m.runs[0]
	st.phase, st.result, st.num = phFinished, "FAILURE", 4
	_, cmd := m.Update(resultMsg{gen: m.gen, st: st})
	if len(s.notes) != 0 {
		t.Fatal("Update itself must not call Sys")
	}
	var _ tea.Cmd = cmd
	msgs(cmd)
	if len(s.notes) != 1 {
		t.Fatal("the command notifies")
	}
}

func TestNotifyForgetsSeenRowsWhenLeft(t *testing.T) {
	s := &noteSys{}
	m := noteModel("failures", s)
	feed(m, phRunning, "", 1)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // leaves the monitor
	if len(m.ma.seen) != 0 {
		t.Fatal("seen must be cleared")
	}
	m.gen++
	feed(m, phFinished, "FAILURE", 1) // finished while away: no burst on return
	if len(s.notes) != 0 {
		t.Fatalf("notes %v", s.notes)
	}
}

func TestNotifyTriggeredRunFailingBeforeFirstPoll(t *testing.T) {
	s := &noteSys{}
	m := noteModel("failures", s)
	Add(m, []Run{{Job: "api/deploy", Ref: jenkins.QueueRef{ID: 3}}})
	feed(m, phFinished, "FAILURE", 8) // the first poll already sees it failed
	if len(s.notes) != 1 || s.notes[0] != "jenktui|FAILED api/deploy #8" {
		t.Fatalf("notes %v", s.notes)
	}
	// a trigger that failed to start is not watched
	s2 := &noteSys{}
	m = noteModel("all", s2)
	Add(m, []Run{{Job: "api/deploy", Err: errors.New("boom")}})
	if len(m.ma.seen) != 0 {
		t.Fatal("failed start is not seen")
	}
}

func jumpModel(t *testing.T) (*model, *noteSys) {
	t.Helper()
	s := &noteSys{}
	m := noteModel("failures", s)
	m.deps.Client = &fakeClient{
		stages: []jenkins.Stage{
			{ID: "1", Name: "Build", Status: jenkins.StageSuccess},
			{ID: "2", Name: "Deploy", Status: jenkins.StageFailed},
		},
		stageLogs: map[string]string{"2": "error: connection refused\n"},
		log:       "whole log\n",
	}
	return m, s
}

func failBuild(m *model, num int) {
	m.Update(tea.BlurMsg{}) // the user is in another window
	feed(m, phRunning, "", num)
	st := m.runs[0]
	st.stages = m.deps.Client.(*fakeClient).stages
	m.runs[0] = st
	feed(m, phFinished, "FAILURE", num)
}

func TestFocusAfterFailureOpensFailedStageLog(t *testing.T) {
	m, _ := jumpModel(t)
	failBuild(m, 57)
	_, cmd := m.Update(tea.FocusMsg{})
	runMD(m, cmd)
	if !m.details || m.log == nil || m.log.what != "stage Deploy" || m.log.num != 57 {
		t.Fatalf("details %v log %+v", m.details, m.log)
	}
	if !strings.Contains(strings.Join(m.log.lines, "\n"), "connection refused") {
		t.Fatalf("log lines %q", m.log.lines)
	}
	// used once: closing the log and focusing again stays put
	m.closeLog()
	m.Update(tea.FocusMsg{})
	if m.log != nil {
		t.Fatal("jumped twice")
	}
}

func TestFocusWhileReadingALogKeepsTheTarget(t *testing.T) {
	m, _ := jumpModel(t)
	failBuild(m, 3)
	m.log = &logView{job: "other", num: 1}
	m.Update(tea.FocusMsg{})
	if m.log.job != "other" || m.ma.jump.job == "" {
		t.Fatal("focus must not replace an open log")
	}
	m.closeLog()
	_, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	runMD(m, cmd)
	if m.log == nil || m.log.num != 3 {
		t.Fatalf("! did not jump: %+v", m.log)
	}
}

func TestJumpWithoutStagesShowsTheWholeLog(t *testing.T) {
	m, _ := jumpModel(t)
	m.deps.Client.(*fakeClient).stages = nil
	m.Update(tea.BlurMsg{})
	feed(m, phRunning, "", 4)
	feed(m, phFinished, "UNSTABLE", 4)
	_, cmd := m.Update(tea.FocusMsg{})
	runMD(m, cmd)
	if m.log == nil || len(m.log.nodes) != 0 || m.log.follow {
		t.Fatalf("log %+v", m.log)
	}
}

func TestNoJumpTargetWithoutAFailure(t *testing.T) {
	s := &noteSys{}
	m := noteModel("all", s)
	feed(m, phRunning, "", 1)
	feed(m, phFinished, "SUCCESS", 1)
	m.Update(tea.FocusMsg{})
	if m.log != nil || m.details {
		t.Fatal("a success must not jump")
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	if !strings.Contains(m.Subtitle(), "no failure notified yet") {
		t.Fatalf("subtitle %q", m.Subtitle())
	}
}

func TestFailureSeenWhileFocusedDoesNotJumpOnFocus(t *testing.T) {
	m, _ := jumpModel(t)
	feed(m, phRunning, "", 8)
	st := m.runs[0]
	st.stages = m.deps.Client.(*fakeClient).stages
	m.runs[0] = st
	feed(m, phFinished, "FAILURE", 8) // the terminal never lost focus
	m.Update(tea.BlurMsg{})
	m.Update(tea.FocusMsg{})
	if m.log != nil || m.details {
		t.Fatal("a failure seen while focused must not jump on a later focus")
	}
	_, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	runMD(m, cmd)
	if m.log == nil || m.log.num != 8 {
		t.Fatalf("! must still jump: %+v", m.log)
	}
}

func TestLeavingTheMonitorDropsTheJumpTarget(t *testing.T) {
	m, _ := jumpModel(t)
	failBuild(m, 9)
	m.pause()
	m.Update(tea.FocusMsg{})
	if m.log != nil || m.ma.jump.job != "" {
		t.Fatal("a target from before leaving must not jump")
	}
}

func TestFocusDropsAReplacedTargetQuietly(t *testing.T) {
	m, _ := jumpModel(t)
	failBuild(m, 10)
	m.runs[0].num = 11 // a newer build took the row
	m.Update(tea.FocusMsg{})
	if m.log != nil || m.ma.jump.job != "" || m.noticeShown() {
		t.Fatalf("log %v jump %v notice %q", m.log, m.ma.jump, m.notice)
	}
}
