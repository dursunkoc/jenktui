package monitor

import (
	"context"
	"errors"
	"strconv"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

// MA4: abort a running build or cancel a queued item with X, after a y/n
// question.

var keyAbort = key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "abort"))

// stopper is the optional client part that stops builds (*jenkins.Client has
// it).
type stopper interface {
	StopBuild(ctx context.Context, fullName string, number int) error
	CancelQueue(ctx context.Context, id int) error
}

// abortable reports whether X applies to the selected row: a running build
// with a number, or a queued item with a queue id.
func (m *model) abortable() bool {
	i := m.selected()
	if _, ok := m.deps.Client.(stopper); !ok || i < 0 {
		return false
	}
	st := m.runs[i]
	return st.phase == phRunning && st.num > 0 || st.phase == phQueued && st.num == 0 && st.run.Ref.ID > 0
}

// abortKey handles X: it asks first and stops only after y. The job and the
// build number or queue id are captured now, not when the answer comes.
func (m *model) abortKey() tea.Cmd {
	if !m.abortable() || m.load.Active() && m.unpolled() {
		return nil
	}
	st := m.runs[m.selected()]
	s := m.deps.Client.(stopper)
	job, num, id := st.run.Job, st.num, st.run.Ref.ID
	queued := st.phase == phQueued
	timeout, epoch := m.deps.PollTimeout, m.epoch
	text := "Abort " + m.jobName(job) + " #" + strconv.Itoa(num) + "?"
	if queued {
		text = "Abort " + m.jobName(job) + " (queued)?"
	}
	m.ask(text, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var err error
		if queued {
			err = s.CancelQueue(ctx, id)
		} else {
			err = s.StopBuild(ctx, job, num)
		}
		return abortDone{epoch: epoch, err: err}
	})
	return nil
}

// abortDone is the result of a stop or cancel. A result that arrives after
// the monitor was left is dropped: nothing is left waiting for it.
type abortDone struct {
	epoch int
	err   error
}

func (d abortDone) apply(m *model) tea.Cmd {
	if d.epoch != m.epoch {
		return nil
	}
	switch {
	case errors.Is(d.err, jenkins.ErrForbidden):
		return m.flash("not allowed to abort")
	case errors.Is(d.err, jenkins.ErrBuildNotFound), errors.Is(d.err, jenkins.ErrQueueItemNotFound):
		return tea.Batch(m.flash("already finished or gone"), m.pollNow())
	case d.err != nil:
		return m.flash(shortErr(d.err))
	}
	return tea.Batch(m.flash("abort requested"), m.pollNow())
}

// pollNow polls every pollable row at once (the manual refresh), so the
// aborted row shows its new state without waiting for the next tick.
func (m *model) pollNow() tea.Cmd {
	if m.load.Active() && m.unpolled() {
		return nil
	}
	return m.startCycle(true)
}

// abortKeys is X for the help panel, which lists it whenever the client can
// stop builds (the bar shows it only when the row allows it).
func (m *model) abortKeys() []key.Binding {
	if _, ok := m.deps.Client.(stopper); ok {
		return []key.Binding{keyAbort}
	}
	return nil
}
