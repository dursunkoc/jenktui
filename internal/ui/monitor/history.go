package monitor

import (
	"context"
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
)

// histMsg delivers a job's recent results.
type histMsg struct {
	epoch  int
	key    buildKey
	builds []jenkins.Build
	err    error
}

func (h histMsg) apply(m *model) tea.Cmd {
	if h.epoch != m.epoch {
		return nil
	}
	if h.err != nil {
		delete(m.md.histBusy, h.key) // try again on the next poll
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
	epoch int
	key   buildKey
	bc    jenkins.BuildContext
	err   error
}

func (c ctxMsg) apply(m *model) tea.Cmd {
	if c.epoch != m.epoch {
		return nil
	}
	if c.err != nil {
		delete(m.md.ctxBusy, c.key)
		return nil
	}
	if m.md.ctx == nil {
		m.md.ctx = map[buildKey]jenkins.BuildContext{}
	}
	m.md.ctx[c.key] = c.bc
	return nil
}

// fetchHistory asks once per job and finished build number.
func (m *model) fetchHistory(st runState) tea.Cmd {
	h, ok := m.deps.Client.(historian)
	if !ok || st.phase != phFinished || st.num <= 0 {
		return nil
	}
	k := buildKey{st.run.Job, st.num}
	if m.md.histBusy[k] {
		return nil
	}
	if m.md.histBusy == nil {
		m.md.histBusy = map[buildKey]bool{}
	}
	m.md.histBusy[k] = true
	epoch, timeout := m.epoch, m.deps.PollTimeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		b, err := h.BuildHistory(ctx, k.job, dotCount)
		return histMsg{epoch: epoch, key: k, builds: b, err: err}
	}
}

// fetchContext asks once per job and build number.
func (m *model) fetchContext(st runState) tea.Cmd {
	c, ok := m.deps.Client.(contexter)
	if !ok || st.num <= 0 {
		return nil
	}
	k := buildKey{st.run.Job, st.num}
	if m.md.ctxBusy[k] {
		return nil
	}
	if m.md.ctxBusy == nil {
		m.md.ctxBusy = map[buildKey]bool{}
	}
	m.md.ctxBusy[k] = true
	epoch, timeout := m.epoch, m.deps.PollTimeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		bc, err := c.BuildContext(ctx, k.job, k.num)
		return ctxMsg{epoch: epoch, key: k, bc: bc, err: err}
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
	if st.phase == phRunning && st.num > newest {
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
	glyph, col := "●", m.th.Muted
	switch {
	case b.Building:
		col = m.th.Running
	case b.Result == "SUCCESS":
		col = m.th.OK
	case b.Result == "FAILURE":
		col = m.th.Fail
	case b.Result == "UNSTABLE":
		col = m.th.Warn
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
		if c.Text != "" {
			out = append(out, m.th.MutedTxt.Render(c.Text))
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
