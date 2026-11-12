package monitor

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Seams: the hooks that let the v0.2 lanes add features without editing each
// other's files. Each is inert until a feature uses it.
//
//   - applier (any lane): a message that knows how to change the model. Update
//     has one `case applier`, so a lane's async result (a fetched test report,
//     a stop result) is a type in its own file with an apply method and needs
//     no case in monitor.go. Drop stale results inside apply with the existing
//     gen/epoch counters.
//   - flash (MA, LV): m.flash(text) (its expiry goes through m.after, which wire() makes a no-op in tests) shows a notice in Subtitle for noticeFor on
//     the injected clock and returns a cmd that re-renders when it expires;
//     callers batch it with their own cmd.
//   - detail sections (MD, MA): addDetailSection registers a func(runState)
//     []string whose lines go under the details header. MD owns initHooks and
//     registers its sections there; MA registers its own in newBase after the
//     initHooks call.
//   - confirm prompt (MA4, abort): m.ask(text, yes) draws a y/n question on the
//     last body line; key handles it before anything else. The yes cmd runs
//     later, so capture the job and build number when asking, not when it runs.
//   - overlay (MA3, failed tests): a modal that replaces the body and takes
//     every key until its key method reports it is done.
//   - mdState / md (MD) and maState / ma (MA): per-lane state on the model.
//     runState is replaced whole by every poll result, so feature data keyed
//     by job and build number lives in md or ma, never on runState.
//   - onResult / onDetailsOpen (MD3): hooks in display.go. onResult(i, prev)
//     runs after a poll result replaced row i (prev is the old row), and
//     onDetailsOpen(i) when the details view opens; each returns a cmd that is
//     batched with the rest. MD starts its history fetch there.
//   - manual (MD1): m.manual is true while the running cycle was started by R.
//   - logKeys (LV): the log view's help bar and key handling live in
//     logview.go (logKeys, logKey).
//   - Deps.Sys, SaveDir, Notify, Rerun: optional; nil Sys or Rerun hides the
//     keys that need them.

// applier is implemented by messages that change the model themselves.
type applier interface{ apply(m *model) tea.Cmd }

// noticeFor is how long a flash stays in the Subtitle.
const noticeFor = 4 * time.Second

// noticeExpiredMsg only causes a render, which drops an expired notice.
type noticeExpiredMsg struct{}

func (noticeExpiredMsg) apply(*model) tea.Cmd { return nil }

// flash shows text in the title-row status line for noticeFor. The returned
// cmd fires when it expires so an idle monitor redraws.
func (m *model) flash(text string) tea.Cmd {
	m.notice, m.noticeUntil = text, m.deps.Now().Add(noticeFor)
	return m.after(noticeFor, noticeExpiredMsg{})
}

func (m *model) noticeShown() bool {
	return m.notice != "" && m.deps.Now().Before(m.noticeUntil)
}

// detailSection returns the lines one feature adds under the details header
// of a row (nil for none).
type detailSection func(runState) []string

// addDetailSection registers a section; sections show in registration order.
func (m *model) addDetailSection(s detailSection) { m.sections = append(m.sections, s) }

// detailHeader joins the registered sections' lines for st.
func (m *model) detailHeader(st runState) []string {
	var out []string
	for _, s := range m.sections {
		out = append(out, s(st)...)
	}
	return out
}

// confirmPrompt is a yes/no question drawn on the last body line. y runs yes;
// n and esc dismiss it; every other key is ignored.
type confirmPrompt struct {
	text string  // e.g. "Abort api/deploy #57?"
	yes  tea.Cmd // may be nil
}

var (
	keyConfirmYes = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes"))
	keyConfirmNo  = key.NewBinding(key.WithKeys("n", "esc"), key.WithHelp("n/esc", "no"))
)

// ask opens the confirm prompt. yes runs after the user answers, so it must
// capture the job and build number now rather than read the cursor later.
func (m *model) ask(text string, yes tea.Cmd) { m.confirm = &confirmPrompt{text: text, yes: yes} }

// confirmKey handles a key while the prompt is open.
func (m *model) confirmKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keyConfirmYes):
		yes := m.confirm.yes
		m.confirm = nil
		return yes
	case key.Matches(msg, keyConfirmNo):
		m.confirm = nil
	}
	return nil
}

// overlay is a modal screen part drawn instead of the body. It receives every
// key; done reports that it should be closed.
type overlay interface {
	view(w, h int) string
	key(msg tea.KeyMsg) (cmd tea.Cmd, done bool)
	keys() []key.Binding
}
