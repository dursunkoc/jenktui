package monitor

import (
	"slices"
	"strconv"

	"github.com/charmbracelet/bubbles/key"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// MA5: ring the bell and send a desktop notification when a build the monitor
// watched running finishes. Only the screen on show receives poll results, so
// this fires only while this monitor is on screen.
//
// A failed or unstable build that notified becomes the jump target: `!` opens
// that build's failed stage log, and so does the terminal getting focus back
// (clicking the notification does that) when the failure notified while the
// terminal was not focused, so a failure the user watched happen does not
// pull them into its log later.

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
		m.markSeen(job)
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
	notify := func() tea.Msg {
		sys.Bell()
		_ = sys.Notify(title, body) // a missing notifier must not disturb the monitor
		return nil
	}
	if !st.failed() || st.num == 0 {
		return notify
	}
	m.ma.jump, m.ma.jumpOnFocus = buildKey{job, st.num}, m.ma.blurred
	return notify
}

var keyJump = key.NewBinding(key.WithKeys("!"), key.WithHelp("!", "log of the last notified failure"))

// focusChanged follows the terminal's focus reports. Regaining focus opens
// the jump target's log when it notified while the terminal was not focused,
// unless the user is busy with a log, an overlay or a question; a target whose
// row was replaced is dropped quietly.
func (m *model) focusChanged(focused bool) tea.Cmd {
	m.ma.blurred = !focused
	if !focused || !m.ma.jumpOnFocus || m.log != nil || m.overlay != nil || m.confirm != nil {
		return nil
	}
	if i := m.index(m.ma.jump.job); i < 0 || m.runs[i].num != m.ma.jump.num {
		m.ma.jump, m.ma.jumpOnFocus = buildKey{}, false
		return nil
	}
	return m.jumpToFailed()
}

// jumpToFailed selects the jump target's row, opens its details and shows the
// end of its failed stage's log, or of the whole log when no stage failed or
// the build has no stages. The target is used once.
func (m *model) jumpToFailed() tea.Cmd {
	j := m.ma.jump
	m.ma.jump, m.ma.jumpOnFocus = buildKey{}, false
	if j.job == "" {
		return m.flash("no failure notified yet")
	}
	i := m.index(j.job)
	if i < 0 || m.runs[i].num != j.num {
		return m.flash(ansi.Strip(m.jobName(j.job)) + " #" + strconv.Itoa(j.num) + " is no longer shown")
	}
	m.cursor = max(slices.Index(m.visible(), i), 0)
	m.details, m.ma.focusFailed = true, false
	cmds := []tea.Cmd{m.detailStages(), m.hooks.detailsOpen(i), m.fetchCounts(i)}
	if l := failedLine(m.runs[i].stages); l >= 0 {
		m.stageCur = l
		cmds = append(cmds, m.openFailedLog())
	} else {
		m.stageCur = 0
		cmds = append(cmds, m.openLog(false))
		if m.log != nil {
			m.log.follow, m.log.top = false, 1<<30 // clamped to the last page
		}
	}
	return tea.Batch(cmds...)
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

func (m *model) markSeen(job string) {
	if m.ma.seen == nil {
		m.ma.seen = map[string]bool{}
	}
	m.ma.seen[job] = true
}
