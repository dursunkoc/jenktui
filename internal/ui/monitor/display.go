package monitor

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

// mdState is the Monitor Display lane's per-monitor state (history dots,
// causes, commits), keyed by job and build number.
type mdState struct {
	hist     map[buildKey][]jenkins.Build // last results, newest first
	histBusy map[buildKey]int             // epoch a fetch started in; older ones were lost with the screen
	ctx      map[buildKey]jenkins.BuildContext
	ctxBusy  map[buildKey]int
	ctxDone  map[buildKey]bool // a context fetched after the build finished (final)
	// failed attempts per key; permanent errors and maxTries stop the retries
	histTries, ctxTries map[buildKey]int
}

type buildKey struct {
	job string
	num int
}

// onResult runs after a poll result replaced row i; prev is the row before.
// MD starts its once-per-finished-build history fetch here.
func (m *model) onResult(i int, prev runState) tea.Cmd {
	st := m.runs[i]
	if prev.phase != phFinished && st.phase == phFinished {
		// The build just finished: a context fetched while it ran may lack its commits.
		k := buildKey{st.run.Job, st.num}
		if _, had := m.md.ctx[k]; had {
			delete(m.md.ctxBusy, k)
			return tea.Batch(m.fetchHistory(st), m.fetchContext(st))
		}
	}
	return m.fetchHistory(st)
}

// onDetailsOpen runs when the details view opens on row i.
func (m *model) onDetailsOpen(i int) tea.Cmd {
	return m.fetchContext(m.runs[i])
}

// durText is the elapsed or final duration of a row. A running build with a
// known estimate reads `1m36s / ~4m`, or `4m12s (over)` once it overruns.
func (m *model) durText(st runState, d time.Duration) string {
	t := fmtDur(d)
	if st.phase != phRunning || st.build.EstimatedDuration <= 0 {
		return t
	}
	if d > st.build.EstimatedDuration {
		return t + " (over)"
	}
	return t + " / ~" + fmtEst(st.build.EstimatedDuration)
}

// fmtEst is an estimate in whole minutes without trailing zeros: 4m, 1h, 1h05m, 45s.
func fmtEst(d time.Duration) string {
	if d >= time.Minute {
		// Jenkins' estimate is a millisecond average; whole minutes read better.
		s := fmtDur(d.Round(time.Minute))
		return strings.TrimSuffix(strings.TrimSuffix(s, "00s"), "00m")
	}
	return fmtDur(d)
}

// ageText says how long ago a finished build started (empty otherwise).
func (m *model) ageText(st runState) string {
	if st.phase != phFinished || st.build.Timestamp.IsZero() {
		return ""
	}
	return fmtAge(m.deps.Now().Sub(st.build.Timestamp))
}

func fmtAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 48*time.Hour:
		return "yesterday"
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}
