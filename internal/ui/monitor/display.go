package monitor

import tea "github.com/charmbracelet/bubbletea"

// mdState is the Monitor Display lane's per-monitor state (history dots,
// causes, commits). Owned by lane MD; keyed by job and build number.
type mdState struct{}

// onResult runs after a poll result replaced row i; prev is the row before.
// MD starts its once-per-finished-build history fetch here.
func (m *model) onResult(i int, prev runState) tea.Cmd { return nil }

// onDetailsOpen runs when the details view opens on row i.
func (m *model) onDetailsOpen(i int) tea.Cmd { return nil }
