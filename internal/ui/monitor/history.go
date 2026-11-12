package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

// historian and contexter are optional Client capabilities; a Client without
// them simply shows no dots and no build context.
type historian interface {
	BuildHistory(ctx context.Context, job string, n int) ([]jenkins.Build, error)
}

type contexter interface {
	BuildContext(ctx context.Context, job string, n int) (jenkins.BuildContext, error)
}

const (
	dotCount   = 5
	maxCommits = 5
	maxTries   = 3 // fetch attempts per build after transient errors
)

// permanent reports errors that retrying cannot fix.
func permanent(err error) bool {
	return errors.Is(err, jenkins.ErrAuth) || errors.Is(err, jenkins.ErrForbidden)
}

// histMsg delivers a job's recent results.
type histMsg struct {
	key    buildKey
	builds []jenkins.Build
	err    error
}

// apply stores the result whatever the epoch: the results of a finished
// build number never change, so a fetch that outlived the screen is still good.
func (h histMsg) apply(m *model) tea.Cmd {
	if h.err != nil {
		if m.md.histTries == nil {
			m.md.histTries = map[buildKey]int{}
		}
		m.md.histTries[h.key]++
		if permanent(h.err) {
			m.md.histTries[h.key] = maxTries
		}
		delete(m.md.histBusy, h.key) // try again on the next poll, up to maxTries
		return nil
	}
	if m.md.hist == nil {
		m.md.hist = map[buildKey][]jenkins.Build{}
	}
	m.md.hist[h.key] = h.builds
	return nil
}

// ctxMsg delivers a build's causes and changes.
type ctxMsg struct {
	key     buildKey
	bc      jenkins.BuildContext
	running bool // fetched while the build ran: commits may still be missing
	err     error
}

func (c ctxMsg) apply(m *model) tea.Cmd {
	if c.err != nil {
		if m.md.ctxTries == nil {
			m.md.ctxTries = map[buildKey]int{}
		}
		m.md.ctxTries[c.key]++
		if permanent(c.err) {
			m.md.ctxTries[c.key] = maxTries
		}
		delete(m.md.ctxBusy, c.key)
		return nil
	}
	if m.md.ctx == nil {
		m.md.ctx = map[buildKey]jenkins.BuildContext{}
	}
	m.md.ctx[c.key] = c.bc
	delete(m.md.ctxBusy, c.key)
	if !c.running { // a running build's context is not final: the next open or the finish refetches
		if m.md.ctxDone == nil {
			m.md.ctxDone = map[buildKey]bool{}
		}
		m.md.ctxDone[c.key] = true
	}
	return nil
}

// fetchHistory asks once per job and finished build number.
func (m *model) fetchHistory(st runState) tea.Cmd {
	h, ok := m.deps.Client.(historian)
	if !ok || st.phase != phFinished || st.num <= 0 {
		return nil
	}
	k := buildKey{st.run.Job, st.num}
	if _, have := m.md.hist[k]; have || m.md.histTries[k] >= maxTries {
		return nil
	}
	// A busy entry from an earlier epoch is a reply lost when the screen was left.
	if e, busy := m.md.histBusy[k]; busy && e == m.epoch {
		return nil
	}
	if m.md.histBusy == nil {
		m.md.histBusy = map[buildKey]int{}
	}
	m.md.histBusy[k] = m.epoch
	timeout := m.deps.PollTimeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		b, err := h.BuildHistory(ctx, k.job, dotCount)
		return histMsg{key: k, builds: b, err: err}
	}
}

// fetchContext asks once per job and build number.
func (m *model) fetchContext(st runState) tea.Cmd {
	c, ok := m.deps.Client.(contexter)
	if !ok || st.num <= 0 {
		return nil
	}
	k := buildKey{st.run.Job, st.num}
	if m.md.ctxDone[k] || m.md.ctxTries[k] >= maxTries {
		return nil
	}
	if e, busy := m.md.ctxBusy[k]; busy && e == m.epoch {
		return nil
	}
	if m.md.ctxBusy == nil {
		m.md.ctxBusy = map[buildKey]int{}
	}
	m.md.ctxBusy[k] = m.epoch
	timeout, running := m.deps.PollTimeout, st.phase != phFinished
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		bc, err := c.BuildContext(ctx, k.job, k.num)
		return ctxMsg{key: k, bc: bc, running: running, err: err}
	}
}

// dots renders the job's last results, newest on the right. While a newer
// build runs, the dots of the fetched history shift left to make room for it.
func (m *model) dots(st runState) string {
	var hist []jenkins.Build // newest first
	newest := 0
	for k, b := range m.md.hist {
		if k.job == st.run.Job && k.num > newest {
			newest, hist = k.num, b
		}
	}
	if len(hist) == 0 {
		return ""
	}
	if st.phase == phRunning && (hist[0].Number < st.num) {
		hist = append([]jenkins.Build{{Number: st.num, Building: true}}, hist...)
	}
	if len(hist) > dotCount {
		hist = hist[:dotCount]
	}
	var sb strings.Builder
	for i := len(hist) - 1; i >= 0; i-- {
		sb.WriteString(m.dot(hist[i]))
	}
	return sb.String()
}

func (m *model) dot(b jenkins.Build) string {
	// The glyph carries the meaning too, for terminals without colour.
	glyph, col := "●", m.th.Muted
	switch {
	case b.Building:
		col = m.th.Running
	case b.Result == "SUCCESS":
		col = m.th.OK
	case b.Result == "FAILURE":
		glyph, col = "✕", m.th.Fail
	case b.Result == "UNSTABLE":
		glyph, col = "◐", m.th.Warn
	default: // aborted, not built, unknown
		glyph = "○"
	}
	return lipgloss.NewStyle().Foreground(col).Render(glyph)
}

// contextLines is the details-header section: why the build started and what
// changed. It is empty until the context arrives.
func (m *model) contextLines(st runState) []string {
	bc, ok := m.md.ctx[buildKey{st.run.Job, st.num}]
	if !ok {
		return nil
	}
	var out []string
	for _, c := range bc.Causes {
		if t := strings.Join(strings.Fields(c.Text), " "); t != "" {
			out = append(out, m.th.MutedTxt.Render(t))
		}
	}
	for i, c := range bc.Changes {
		if i == maxCommits {
			out = append(out, m.th.MutedTxt.Render(fmt.Sprintf("… and %d more commits", len(bc.Changes)-maxCommits)))
			break
		}
		msg, _, _ := strings.Cut(strings.TrimSpace(c.Message), "\n")
		id := c.ID
		if len(id) > 7 {
			id = id[:7]
		}
		line := strings.TrimSpace(fmt.Sprintf("%s %s: %s", id, c.Author, msg))
		out = append(out, m.th.MutedTxt.Render(line))
	}
	return out
}
