package monitor

import (
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// MA5: ring the bell and send a desktop notification when a build the monitor
// watched running finishes. Only the screen on show receives poll results, so
// this fires only while this monitor is on screen.

// Values of Deps.Notify (the config's notify).
const (
	notifyFailures = "failures"
	notifyAll      = "all"
	notifyOff      = "off"
)

// notifyOnFinish returns the command that notifies when the poll result st
// replaced prev with a finished build that the monitor saw running or queued.
// It also keeps track of which rows have been seen live. A row whose first
// result is already finished (the first load) never notifies.
func (m *model) notifyOnFinish(prev, st runState) tea.Cmd {
	job := st.run.Job
	seen := m.ma.seen[job]
	if st.live() {
		if m.ma.seen == nil {
			m.ma.seen = map[string]bool{}
		}
		m.ma.seen[job] = true
	}
	if st.phase != phFinished || !prev.live() || !seen {
		return nil
	}
	delete(m.ma.seen, job)
	sys := m.deps.Sys
	if sys == nil || !m.wantsNotice(st.result) {
		return nil
	}
	title, body := "jenktui", noticeWord(st.result)+" "+ansi.Strip(m.jobName(job))
	if st.num > 0 {
		body += " #" + strconv.Itoa(st.num)
	}
	return func() tea.Msg {
		sys.Bell()
		_ = sys.Notify(title, body) // a missing notifier must not disturb the monitor
		return nil
	}
}

// wantsNotice applies Deps.Notify: failures (the default) notify for a failed
// or unstable build, all for every finished build, off never.
func (m *model) wantsNotice(result string) bool {
	switch m.deps.Notify {
	case notifyOff:
		return false
	case notifyAll:
		return true
	}
	return result == "FAILURE" || result == "UNSTABLE"
}

func noticeWord(result string) string {
	switch result {
	case "FAILURE":
		return "FAILED"
	case "":
		return "FINISHED"
	}
	return result
}
