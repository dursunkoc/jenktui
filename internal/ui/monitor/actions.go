package monitor

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/sysint"
)

// maState is the Monitor Actions lane's per-monitor state (test reports,
// notified builds). Owned by lane MA; keyed by job and build number.
type maState struct{}

var (
	keyOpen = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser"))
	keyCopy = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy URL"))
)

// urler is the optional client part that gives browser URLs built from the
// configured base URL (*jenkins.Client has it). Jenkins' own reported url is
// never used: its root URL can be internal or wrong behind a proxy.
type urler interface {
	JobURL(fullName string) string
	BuildURL(fullName string, num int) string
}

// actionKeys are the keys MA adds to the list and details help; none without Sys.
func (m *model) actionKeys() []key.Binding {
	if m.deps.Sys == nil {
		return nil
	}
	return []key.Binding{keyOpen, keyCopy}
}

// targetURL is the URL of the highlighted build, or of its job when it has no
// build yet; "" when there is nothing to open (no row, job missing, no URL).
func (m *model) targetURL() string {
	i := m.selected()
	if i < 0 {
		return ""
	}
	st := m.runs[i]
	if st.phase == phMissing {
		return ""
	}
	u, ok := m.deps.Client.(urler)
	if !ok {
		return ""
	}
	if st.num > 0 {
		return u.BuildURL(st.run.Job, st.num)
	}
	return u.JobURL(st.run.Job)
}

// sysDone reports the outcome of an open or copy.
type sysDone struct {
	text string
	err  error
}

func (d sysDone) apply(m *model) tea.Cmd {
	if d.err != nil {
		return m.flash(shortErr(d.err))
	}
	return m.flash(d.text)
}

// shortErr is an error text fit for the status line: no "sysint: " prefix, at
// most 60 characters.
func shortErr(err error) string {
	t := strings.TrimPrefix(err.Error(), "sysint: ")
	if r := []rune(t); len(r) > 60 {
		t = string(r[:59]) + "…"
	}
	return t
}

// actionKey handles o and y; ok is false for any other key.
func (m *model) actionKey(msg tea.KeyMsg) (cmd tea.Cmd, ok bool) {
	if m.deps.Sys == nil {
		return nil, false
	}
	if m.load.Active() && m.unpolled() {
		return nil, true // behind the first-load modal, like R
	}
	switch {
	case key.Matches(msg, keyOpen):
		url, sys := m.targetURL(), m.deps.Sys
		if url == "" {
			return nil, true
		}
		return func() tea.Msg {
			if err := sys.Open(url); err != nil {
				return sysDone{err: err}
			}
			return sysDone{text: "opened"}
		}, true
	case key.Matches(msg, keyCopy):
		url, sys := m.targetURL(), m.deps.Sys
		if url == "" {
			return nil, true
		}
		return func() tea.Msg {
			route, err := sys.Copy(url)
			switch {
			case err != nil:
				return sysDone{err: err}
			case route == sysint.RouteTerminal:
				return sysDone{text: "sent to terminal clipboard"}
			}
			return sysDone{text: "copied"}
		}, true
	}
	return nil, false
}
