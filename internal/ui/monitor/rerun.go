package monitor

import (
	"context"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// CORE1: rerun the highlighted build with `r`. The build's parameters are read
// with BuildContext and handed to Deps.Rerun, which builds the trigger screen
// with those values prefilled. Password parameters cannot be read back, so
// they are left out and the form asks for them.

var keyRerun = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rerun"))

// rerunnable reports whether `r` applies: Rerun is wired, the client can read
// a build's parameters, and the highlighted row has a build.
func (m *model) rerunnable() bool {
	i := m.selected()
	if m.deps.Rerun == nil || i < 0 {
		return false
	}
	if _, ok := m.deps.Client.(contexter); !ok {
		return false
	}
	st := m.runs[i]
	return st.num > 0 && (st.phase == phRunning || st.phase == phFinished)
}

// rerunKeys is `r` for the help panel, listed whenever rerun is wired and the
// client can read parameters (the bar shows it only when the row has a build).
func (m *model) rerunKeys() []key.Binding {
	if _, ok := m.deps.Client.(contexter); ok && m.deps.Rerun != nil {
		return []key.Binding{keyRerun}
	}
	return nil
}

// rerunKey handles `r`. The job and build number are captured now.
func (m *model) rerunKey() tea.Cmd {
	if !m.rerunnable() || m.load.Active() && m.unpolled() {
		return nil
	}
	st := m.runs[m.selected()]
	c := m.deps.Client.(contexter)
	job, num := st.run.Job, st.num
	timeout, epoch := m.deps.PollTimeout, m.epoch
	load := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		bc, err := c.BuildContext(ctx, job, num)
		return rerunLoaded{epoch: epoch, job: job, params: bc.Params, err: err}
	}
	return tea.Batch(m.flash("loading parameters…"), load)
}

// rerunLoaded is the build's parameters. A result that arrives after the
// monitor was left is dropped.
type rerunLoaded struct {
	epoch  int
	job    string
	params []jenkins.BuildParam
	err    error
}

func (r rerunLoaded) apply(m *model) tea.Cmd {
	if r.epoch != m.epoch || m.deps.Rerun == nil {
		return nil
	}
	if r.err != nil {
		return m.flash("cannot rerun: " + shortErr(r.err))
	}
	scr := m.deps.Rerun(r.job, prefillOf(r.params))
	// The screen pushed over the monitor swallows its messages, so stop the
	// polling as when leaving; Resume restarts it if the user comes back.
	m.pause()
	m.notice = ""
	return core.Cmd(core.Push{Screen: scr})
}

// prefillOf turns build parameters into form values, leaving out passwords.
func prefillOf(ps []jenkins.BuildParam) map[string]string {
	out := make(map[string]string, len(ps))
	for _, p := range ps {
		if !p.Password && p.HasValue {
			out[p.Name] = p.Value
		}
	}
	return out
}

// Resume restarts polling when the screen pushed by `r` is popped.
func (m *model) Resume() tea.Cmd {
	return m.Init()
}
