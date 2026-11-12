package monitor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// MA3: the test report of a finished build. The counts are fetched once when
// the details view opens and shown under the details header; `t` fetches the
// full report and opens an overlay listing the failed tests.

var keyReport = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test report"))

// reporter is the optional client part that serves test reports
// (*jenkins.Client has it).
type reporter interface {
	TestCounts(ctx context.Context, fullName string, number int) (jenkins.TestReport, error)
	TestReport(ctx context.Context, fullName string, number int) (jenkins.TestReport, error)
}

// buildKey identifies one build for feature data kept in maState.
type buildKey struct {
	job string
	num int
}

// countsEntry is what is known about a build's test counts. have is false
// while the fetch is in flight or when the build has no report.
type countsEntry struct {
	have bool
	rep  jenkins.TestReport
}

func (m *model) reporter() (reporter, bool) {
	r, ok := m.deps.Client.(reporter)
	return r, ok
}

// fetchCounts asks for the counts of row i's finished build once.
func (m *model) fetchCounts(i int) tea.Cmd {
	st := m.runs[i]
	r, ok := m.reporter()
	k := buildKey{st.run.Job, st.num}
	if !ok || st.phase != phFinished || st.num == 0 {
		return nil
	}
	if _, asked := m.ma.counts[k]; asked {
		return nil
	}
	if m.ma.counts == nil {
		m.ma.counts = map[buildKey]countsEntry{}
	}
	m.ma.counts[k] = countsEntry{}
	timeout, epoch := m.deps.PollTimeout, m.epoch
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		rep, err := r.TestCounts(ctx, k.job, k.num)
		return countsDone{epoch: epoch, key: k, rep: rep, err: err}
	}
}

type countsDone struct {
	epoch int
	key   buildKey
	rep   jenkins.TestReport
	err   error
}

func (d countsDone) apply(m *model) tea.Cmd {
	if d.epoch != m.epoch {
		return nil
	}
	switch {
	case d.err == nil:
		m.ma.counts[d.key] = countsEntry{have: true, rep: d.rep}
	case errors.Is(d.err, jenkins.ErrNoTestReport):
		// No report: show nothing, and do not ask again.
	default:
		delete(m.ma.counts, d.key) // transient: ask again the next time details open
	}
	return nil
}

// testSection is the details-header line `3 failed · 812 passed · 4 skipped`.
func (m *model) testSection(st runState) []string {
	e, ok := m.ma.counts[buildKey{st.run.Job, st.num}]
	if !ok || !e.have {
		return nil
	}
	return []string{m.countsLine(e.rep)}
}

func (m *model) countsLine(r jenkins.TestReport) string {
	failed := fmt.Sprintf("%d failed", r.FailCount)
	if r.FailCount > 0 {
		failed = lipgloss.NewStyle().Foreground(m.th.Fail).Render(failed)
	}
	return failed + m.th.MutedTxt.Render(fmt.Sprintf(" · %d passed · %d skipped", r.PassCount, r.SkipCount))
}

// reportKey handles `t` in the details view of a finished build.
func (m *model) reportKey() tea.Cmd {
	i := m.selected()
	r, ok := m.reporter()
	if i < 0 || !ok || !m.details || m.runs[i].phase != phFinished || m.runs[i].num == 0 {
		return nil
	}
	k := buildKey{m.runs[i].run.Job, m.runs[i].num}
	timeout, epoch := m.deps.PollTimeout, m.epoch
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		rep, err := r.TestReport(ctx, k.job, k.num)
		return reportDone{epoch: epoch, key: k, rep: rep, err: err}
	}
}

type reportDone struct {
	epoch int
	key   buildKey
	rep   jenkins.TestReport
	err   error
}

func (d reportDone) apply(m *model) tea.Cmd {
	i := m.selected()
	// Drop it when the monitor was left, the details moved to another build,
	// or something else is already on screen.
	if d.epoch != m.epoch || !m.details || m.overlay != nil || m.log != nil || m.confirm != nil ||
		i < 0 || (buildKey{m.runs[i].run.Job, m.runs[i].num}) != d.key {
		return nil
	}
	switch {
	case errors.Is(d.err, jenkins.ErrNoTestReport):
		return m.flash("no test report for this build")
	case d.err != nil:
		return m.flash(shortErr(d.err))
	}
	m.overlay = &reportOverlay{m: m, bk: d.key, rep: d.rep}
	return nil
}

// reportOverlay lists the failed tests; Enter shows the highlighted one's
// error and stack trace.
type reportOverlay struct {
	m      *model
	bk     buildKey
	rep    jenkins.TestReport
	cur    int  // highlighted failed test
	detail bool // showing the stack trace of cur
	scroll int  // first visible line of the trace
}

var (
	keyReportTrace = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "stack trace"))
	keyReportList  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to list"))
	keyReportBack  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to details"))
)

func (o *reportOverlay) keys() []key.Binding {
	if o.detail {
		return []key.Binding{core.KeyUp, core.KeyDown, keyReportList}
	}
	return []key.Binding{core.KeyUp, core.KeyDown, keyReportTrace, keyReportBack}
}

func (o *reportOverlay) page() int {
	if o.m.h <= 0 {
		return 1 << 20
	}
	return max(o.m.h-5, 1)
}

func (o *reportOverlay) traceLines() []string {
	if o.cur >= len(o.rep.Failed) {
		return nil
	}
	c := o.rep.Failed[o.cur]
	var l []string
	if c.ErrorDetails != "" {
		l = append(l, strings.Split(c.ErrorDetails, "\n")...)
		l = append(l, "")
	}
	if c.StackTrace != "" {
		l = append(l, strings.Split(strings.ReplaceAll(c.StackTrace, "\t", "    "), "\n")...)
	}
	if len(l) == 0 {
		l = []string{"No error details."}
	}
	return l
}

func (o *reportOverlay) key(msg tea.KeyMsg) (tea.Cmd, bool) {
	up, down := key.Matches(msg, core.KeyUp), key.Matches(msg, core.KeyDown)
	switch {
	case o.detail && up:
		o.scroll = max(o.scroll-1, 0)
	case o.detail && down:
		o.scroll = max(min(o.scroll+1, len(o.traceLines())-o.page()), 0)
	case o.detail && key.Matches(msg, keyReportList):
		o.detail = false
	case up:
		o.cur = max(o.cur-1, 0)
	case down:
		o.cur = max(min(o.cur+1, len(o.rep.Failed)-1), 0)
	case key.Matches(msg, keyReportTrace):
		if len(o.rep.Failed) > 0 {
			o.detail, o.scroll = true, 0
		}
	case key.Matches(msg, keyReportBack):
		return nil, true
	}
	return nil, false
}

// caseName is how a failed test is listed: its matrix cell when it has one,
// then class and name.
func caseName(c jenkins.FailedCase) string {
	n := c.Name
	if c.ClassName != "" {
		n = c.ClassName + "." + c.Name
	}
	if c.Child != "" {
		n = "[" + c.Child + "] " + n
	}
	return n
}

func (o *reportOverlay) view(w, h int) string {
	m := o.m
	iw := 0
	if w > 0 {
		iw = max(w-4, 10)
	}
	title := m.jobName(o.bk.job) + " #" + strconv.Itoa(o.bk.num) + "  " + m.countsLine(o.rep)
	rows := []string{fit(title, iw)}
	page := o.page()
	switch {
	case o.detail:
		c := o.rep.Failed[o.cur]
		rows = append(rows, fit(m.th.Selected.Render(caseName(c)), iw))
		lines := o.traceLines()
		end := min(o.scroll+page, len(lines))
		for _, l := range lines[min(o.scroll, end):end] {
			rows = append(rows, fit(l, iw))
		}
	case len(o.rep.Failed) == 0:
		rows = append(rows, m.th.MutedTxt.Render("No failed tests."))
	default:
		first := max(min(o.cur-page/2, len(o.rep.Failed)-page), 0)
		last := min(first+page, len(o.rep.Failed))
		for i := first; i < last; i++ {
			line := "  " + caseName(o.rep.Failed[i])
			if i == o.cur {
				line = m.th.Selected.Render("▸ " + caseName(o.rep.Failed[i]))
			}
			rows = append(rows, fit(line, iw))
		}
		if o.rep.Truncated {
			rows = append(rows, m.th.MutedTxt.Render(fmt.Sprintf("more failures than listed (%d of %d shown)", len(o.rep.Failed), o.rep.FailCount)))
		}
	}
	if h > 0 {
		for len(rows) < page+1 {
			rows = append(rows, "")
		}
	}
	box := m.th.Box
	if w > 0 {
		box = box.Width(w - 2)
	}
	if h > 0 {
		box = box.Height(h - 2)
	}
	return box.Render(strings.Join(rows, "\n"))
}
