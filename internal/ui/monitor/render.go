package monitor

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

func (m *model) View() string {
	if m.log != nil {
		return m.logView()
	}
	if len(m.runs) == 0 {
		return m.th.NoticeView("Nothing to monitor", "This collection has no jobs. esc to go back.")
	}
	if m.load.Active() && m.unpolled() {
		text := "Loading builds…"
		if m.name != "" {
			text = "Loading " + m.name + "…"
		}
		return m.load.Modal(text, m.w, m.h)
	}
	vis := m.visible()
	if len(vis) == 0 {
		return m.th.NoticeView(fmt.Sprintf("All %d jobs succeeded", len(m.runs)), "Press s to show them, esc to go back.")
	}
	if m.details {
		return m.detailView()
	}
	var blocks [][]string
	for vi, i := range vis {
		blocks = append(blocks, m.block(vi, i))
	}
	// Window the blocks so the cursor stays visible (one line is the blank
	// line under the title; the status line is the Subtitle).
	avail := m.h - 1
	start := 0
	if m.h > 0 {
		for start < m.cursor && lines(blocks[start:m.cursor+1]) > avail {
			start++
		}
	}
	out := []string{""}
	for _, b := range blocks[start:] {
		out = append(out, b...)
	}
	if m.h > 0 && len(out) > m.h {
		out = out[:m.h]
	}
	return strings.Join(out, "\n")
}

// header is the filter line above the list.
// Subtitle is the status line shown on the title row while the runs list is
// on screen (not over the details view, a notice or the first-load modal).
func (m *model) Subtitle() string {
	if len(m.runs) == 0 || m.details || m.log != nil || m.load.Active() && m.unpolled() || len(m.visible()) == 0 {
		return ""
	}
	return m.header()
}

func (m *model) header() string {
	h := m.baseHeader()
	if m.load.Active() {
		h += " · " + m.load.View("refreshing…")
	}
	return h
}

// unpolled reports whether no row has a result yet (the first cycle is in flight).
func (m *model) unpolled() bool {
	for _, r := range m.runs {
		if r.phase != phUnpolled {
			return false
		}
	}
	return true
}

func (m *model) baseHeader() string {
	ok, remaining := 0, 0
	for _, r := range m.runs {
		if r.success() {
			ok++
		} else {
			remaining++
		}
	}
	if m.hideSuccess {
		return fmt.Sprintf("hiding %d successful (s to show) · %d remaining", ok, remaining)
	}
	return fmt.Sprintf("showing all (s to hide successful) · %d remaining", remaining)
}

func lines(bs [][]string) (n int) {
	for _, b := range bs {
		n += len(b)
	}
	return
}

func (m *model) fit(s string) string {
	if m.w <= 0 {
		return s
	}
	return ansi.Truncate(s, m.w, "…")
}

func (m *model) block(vi, i int) []string {
	st := m.runs[i]
	cur := "  "
	if vi == m.cursor {
		cur = m.th.Selected.Render("▸ ")
	}
	parts := []string{m.badge(st), m.jobName(st.run.Job)}
	if st.num > 0 {
		parts = append(parts, fmt.Sprintf("#%d", st.num))
	}
	if d, ok := m.elapsed(st); ok {
		parts = append(parts, m.th.MutedTxt.Render(fmtDur(d)))
	}
	if t := m.note(st); t != "" {
		parts = append(parts, m.th.MutedTxt.Render(t))
	}
	out := []string{m.fit(cur + strings.Join(parts, "  "))}
	if row := m.stageRow(st.stages); row != "" && st.phase == phRunning {
		out = append(out, m.fit("    "+row))
	}
	return out
}

func (m *model) note(st runState) string {
	switch st.phase {
	case phFailedStart:
		return "failed to start: " + errText(st.run.Err)
	case phLeftQueue:
		return "left queue"
	case phQueued:
		return st.why
	case phNotBuilt:
		return "never built"
	case phMissing:
		return "no longer in the catalog"
	}
	if st.why != "" {
		return st.why
	}
	if st.pollErr != nil {
		return "poll error: " + errText(st.pollErr)
	}
	return ""
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}

func (m *model) badge(st runState) string {
	switch st.phase {
	case phFailedStart:
		return m.th.Badge(core.StatusFailed)
	case phLeftQueue:
		return m.th.Badge(core.StatusUnknown)
	case phQueued:
		return m.th.Badge(core.StatusQueued)
	case phMissing:
		return m.th.Badge(core.StatusMissing)
	case phNotBuilt:
		return lipgloss.NewStyle().Bold(true).Foreground(m.th.Muted).Render("● NOT BUILT")
	case phUnpolled:
		return lipgloss.NewStyle().Foreground(m.th.Muted).Render("● …")
	case phRunning:
		return m.th.Badge(core.StatusRunning)
	}
	switch st.result {
	case "SUCCESS":
		return m.th.Badge(core.StatusSuccess)
	case "FAILURE":
		return m.th.Badge(core.StatusFailed)
	case "UNSTABLE":
		return m.th.Badge(core.StatusUnstable)
	case "ABORTED":
		return m.th.Badge(core.StatusAborted)
	}
	return lipgloss.NewStyle().Bold(true).Foreground(m.th.Muted).Render("● " + strings.ToUpper(st.result))
}

// elapsed is time running (live) or the final duration (finished).
func (m *model) elapsed(st runState) (time.Duration, bool) {
	switch st.phase {
	case phFinished:
		if st.build.Duration > 0 {
			return st.build.Duration, true
		}
	case phRunning:
		if !st.build.Timestamp.IsZero() {
			return m.deps.Now().Sub(st.build.Timestamp), true
		}
		return m.deps.Now().Sub(m.since(st)), true
	case phQueued:
		return m.deps.Now().Sub(m.since(st)), true
	}
	return 0, false
}

// since is when the run was added to the monitor.
func (m *model) since(st runState) time.Time {
	if st.at.IsZero() {
		return m.triggered
	}
	return st.at
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
	case s >= 60:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

func icon(s jenkins.StageStatus) string {
	switch s {
	case jenkins.StageSuccess:
		return "✔"
	case jenkins.StageFailed:
		return "✘"
	case jenkins.StageRunning:
		return "⟳"
	case jenkins.StagePaused:
		return "⏸"
	case jenkins.StageAborted:
		return "⊘"
	case jenkins.StageUnstable:
		return "⚠"
	case jenkins.StageNotExecuted:
		return "○"
	}
	return "?"
}

// stageText renders one stage; parallel children are shown compactly as
// `Name [✔ a ‖ ⟳ b]`. The running stage is highlighted.
func (m *model) stageText(s jenkins.Stage) string {
	t := icon(s.Status) + " " + s.Name
	if len(s.Children) > 0 {
		var ch []string
		for _, c := range s.Children {
			ch = append(ch, icon(c.Status)+" "+c.Name)
		}
		t += " [" + strings.Join(ch, " ‖ ") + "]"
	}
	switch s.Status {
	case jenkins.StageRunning:
		return lipgloss.NewStyle().Bold(true).Foreground(m.th.Running).Render(t)
	case jenkins.StageFailed:
		return lipgloss.NewStyle().Foreground(m.th.Fail).Render(t)
	}
	return t
}

func (m *model) stageRow(stages []jenkins.Stage) string {
	var p []string
	for _, s := range stages {
		p = append(p, m.stageText(s))
	}
	return strings.Join(p, " → ")
}

func (m *model) detailView() string {
	st := m.runs[m.selected()]
	head := []string{m.fit(m.badge(st) + "  " + m.jobName(st.run.Job) + numSuffix(st))}
	if t := m.note(st); t != "" {
		head = append(head, m.fit(m.th.MutedTxt.Render(t)))
	}
	head = append(head, "")
	switch {
	case len(st.stages) > 0:
		n := 0
		for _, s := range st.stages {
			head = append(head, m.fit(m.detailLine(s, "", n == m.stageCur)))
			n++
			for _, c := range s.Children {
				head = append(head, m.fit(m.detailLine(c, "    ", n == m.stageCur)))
				n++
			}
		}
	case st.noStages:
		head = append(head, m.th.MutedTxt.Render("No pipeline stages for this job."))
	case st.success():
		head = append(head, m.th.MutedTxt.Render("Stages are not fetched for successful builds."))
	default:
		head = append(head, m.th.MutedTxt.Render("No stage information yet."))
	}
	if m.h > 0 && len(head) > m.h {
		// Keep the highlighted stage on screen: drop lines after the header.
		top := len(head) - m.stageLines()
		if top < 0 {
			top = 0
		}
		off := max(top+m.stageCur-(m.h-top-1), top)
		off = min(off, len(head)-1)
		if off > top {
			head = append(append([]string{}, head[:top]...), head[off:]...)
		}
		head = head[:min(len(head), m.h)]
	}
	return strings.Join(head, "\n")
}

func numSuffix(st runState) string {
	if st.num > 0 {
		return fmt.Sprintf(" #%d", st.num)
	}
	return ""
}

func (m *model) detailLine(s jenkins.Stage, indent string, cur bool) string {
	d := ""
	if s.Duration > 0 {
		d = "  " + m.th.MutedTxt.Render(fmtDur(s.Duration))
	}
	lead := "  "
	if cur {
		lead = m.th.Selected.Render("▸ ")
	}
	return lead + indent + m.stageText(jenkins.Stage{Name: s.Name, Status: s.Status}) + d
}

// jobName is how job is shown: from SetNames when known, otherwise relative
// to the Parent Job.
func (m *model) jobName(job string) string {
	if n, ok := m.names[job]; ok {
		return n.Render(m.th)
	}
	return core.RelName(job, m.deps.Parent)
}
