package monitor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
	"github.com/dursunkoc/jenktui/internal/ui/widgets"
)

// maxLogLines bounds the lines kept in memory; older lines are dropped (line
// numbers keep counting from the start of the build's log).
const maxLogLines = 10000

var (
	keyLog     = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "stage log"))
	keyLogAll  = key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "full log"))
	keyLogEnd  = key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "follow"))
	keyLogTop  = key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top"))
	keyLogPage = key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "page"))
	keyLogPgUp = key.NewBinding(key.WithKeys("pgup", "ctrl+b"))
	keyLogPgDn = key.NewBinding(key.WithKeys("pgdown", "ctrl+f", " "))
	keyLogFind = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "find"))
	keyLogNext = key.NewBinding(key.WithKeys("n"), key.WithHelp("n/N", "next/prev match"))
	keyLogPrev = key.NewBinding(key.WithKeys("N"))
	keyLogGoto = key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "go to line"))
	keyLogErr  = key.NewBinding(key.WithKeys("e"), key.WithHelp("e/E", "next/prev error"))
	keyLogErrP = key.NewBinding(key.WithKeys("E"))
	keyLogWrap = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wrap"))
	keyLogBack = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to details"))

	keyPromptOK  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "ok"))
	keyPromptEsc = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))

	logHATokens = regexp.MustCompile(`\x1b\[8mha:////[A-Za-z0-9+/=]*\x1b\[0m`)
)

// errorLine matches the lines drawn as errors (case-sensitive). Not
// configurable yet.
var errorLine = regexp.MustCompile(`\bERROR\b|\bFAILED\b|\bFAILURE\b|Exception\b|Caused by:|npm ERR!|error:|Error:|^--- FAIL:`)

func isErrorLine(s string) bool { return errorLine.MatchString(s) }

type logPrompt int

const (
	promptNone logPrompt = iota
	promptFind
	promptGoto
)

// logView is the console log of one build, opened from the details view and
// drawn as a bordered modal with line numbers. It follows new output until the
// user scrolls up, and polls Jenkins while the build is still producing output.
// logNode is one pipeline node whose output makes up a stage or step log.
type logNode struct {
	id, name string
	stage    bool // a stage whose steps are not known yet: look them up first
}

type logView struct {
	nodes   []logNode // non-empty: the log of a stage or step, not the whole build
	what    string    // "stage Build" / "step sh"; empty for the whole build
	job     string
	num     int
	lines   []string // complete lines
	part    string   // trailing text without a newline yet
	dropped int      // lines discarded from the front (numbering continues)
	next    int64    // offset to ask Jenkins for
	more    bool     // Jenkins expects more output
	loaded  bool     // the first chunk arrived
	err     error
	follow  bool
	top     int // first visible line when not following

	prompt logPrompt // an input is open at the bottom
	input  string
	query  string         // the last search text
	re     *regexp.Regexp // compiled query; nil when no search is active
	focus  int            // line of the current match or go-to target; -1 none
	status string         // result of the last find or go-to

	wrap bool // w: long lines continue on further rows instead of being cut
	tw   int  // text width the wrapped layout uses; 0 = no wrapping (set by sync)
}

type (
	logMsg struct {
		gen   int
		start int64
		chunk jenkins.LogChunk
		err   error
	}
	logTickMsg struct{ gen int }
)

// openLog starts the log view of the selected row's build: the whole console
// log, or with stage the log of the stage or step highlighted in the details.
func (m *model) openLog(stage bool) tea.Cmd {
	i := m.selected()
	if i < 0 || m.runs[i].num == 0 {
		return nil
	}
	st := m.runs[i]
	lv := &logView{job: st.run.Job, num: st.num, follow: true, focus: -1}
	if stage {
		if lv.what, lv.nodes = m.stageAt(st); len(lv.nodes) == 0 {
			return nil
		}
	}
	m.logGen++
	m.log = lv
	return m.fetchLog(0)
}

// stageAt resolves the highlighted details line to the nodes that hold its
// output: a stage is the concatenation of its steps, a step is itself.
func (m *model) stageAt(st runState) (string, []logNode) {
	n := 0
	for _, s := range st.stages {
		if n == m.stageCur {
			if len(s.Children) == 0 {
				return "stage " + s.Name, []logNode{{s.ID, s.Name, true}}
			}
			var nodes []logNode
			for _, c := range s.Children {
				nodes = append(nodes, logNode{id: c.ID, name: c.Name})
			}
			return "stage " + s.Name, nodes
		}
		n++
		for _, c := range s.Children {
			if n == m.stageCur {
				return "step " + c.Name + " (" + s.Name + ")", []logNode{{id: c.ID, name: c.Name}}
			}
			n++
		}
	}
	return "", nil
}

func (m *model) closeLog() {
	m.logGen++
	m.log = nil
}

func (m *model) fetchLog(start int64) tea.Cmd {
	client, timeout, gen, job, num := m.deps.Client, m.deps.PollTimeout, m.logGen, m.log.job, m.log.num
	if nodes := m.log.nodes; len(nodes) > 0 {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			// The run-level describe has no steps: ask the stage for them, and
			// fall back to the stage's own node when it has none.
			if len(nodes) == 1 && nodes[0].stage {
				if steps, err := client.StageSteps(ctx, job, num, nodes[0].id); err == nil && len(steps) > 0 {
					nodes = nil
					for _, s := range steps {
						nodes = append(nodes, logNode{id: s.ID, name: s.Name})
					}
				}
			}
			var sb strings.Builder
			for _, n := range nodes {
				text, err := client.StageLog(ctx, job, num, n.id)
				if err != nil {
					return logMsg{gen: gen, start: start, err: err}
				}
				if len(nodes) > 1 {
					fmt.Fprintf(&sb, "── %s ──\n", n.name)
				}
				sb.WriteString(text)
				if !strings.HasSuffix(text, "\n") {
					sb.WriteString("\n")
				}
			}
			return logMsg{gen: gen, start: start, chunk: jenkins.LogChunk{Text: sb.String()}}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		c, err := client.ConsoleText(ctx, job, num, start)
		return logMsg{gen: gen, start: start, chunk: c, err: err}
	}
}

func (m *model) updateLog(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logTickMsg:
		if msg.gen == m.logGen && m.log != nil {
			return m.fetchLog(m.log.next)
		}
	case logMsg:
		lv := m.log
		if msg.gen != m.logGen || lv == nil || msg.start != lv.next {
			return nil
		}
		lv.loaded = true
		if msg.err != nil {
			lv.err = msg.err
			if errors.Is(msg.err, jenkins.ErrBuildNotFound) || errors.Is(msg.err, jenkins.ErrNoStages) {
				lv.more = false
				return nil
			}
			return m.logSchedule(m.deps.SlowInterval)
		}
		lv.err = nil
		if len(lv.nodes) > 0 {
			// A stage log has no offset: refetch it whole while the build runs.
			top, focus, oldDropped := lv.top, lv.focus, lv.dropped
			lv.lines, lv.part, lv.dropped = nil, "", 0
			lv.append(msg.chunk.Text)
			// The refetch trims again from the front: shift by the change.
			d := lv.dropped - oldDropped
			lv.top = max(min(top-d, len(lv.lines)), 0)
			if focus >= 0 {
				focus = max(focus-d, -1)
			}
			lv.focus = min(focus, len(lv.lines)-1)
			if len(lv.lines) == 0 && lv.part == "" {
				lv.lines = []string{"(Jenkins has no output for this " + strings.SplitN(lv.what, " ", 2)[0] + "; L shows the whole build)"}
			}
			lv.more = false
			if i := m.index(lv.job); i >= 0 && m.runs[i].phase == phRunning && m.runs[i].num == lv.num {
				lv.more = true
			}
		} else {
			lv.append(msg.chunk.Text)
			lv.next, lv.more = msg.chunk.Next, msg.chunk.More
		}
		if lv.more {
			return m.logSchedule(m.deps.FastInterval)
		}
	}
	return nil
}

func (m *model) logSchedule(d time.Duration) tea.Cmd {
	gen := m.logGen
	return tea.Tick(d, func(time.Time) tea.Msg { return logTickMsg{gen} })
}

// append adds raw console text: carriage returns and terminal escapes are
// dropped, as are Jenkins' hidden console annotations.
func (lv *logView) append(text string) {
	text = ansi.Strip(logHATokens.ReplaceAllString(text, ""))
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	text = lv.part + text
	parts := strings.Split(text, "\n")
	for i, p := range parts {
		// A progress line rewritten in place keeps only its last version.
		if j := strings.LastIndex(p, "\r"); j >= 0 {
			parts[i] = p[j+1:]
		}
	}
	lv.part = parts[len(parts)-1]
	lv.lines = append(lv.lines, parts[:len(parts)-1]...)
	if drop := len(lv.lines) - maxLogLines; drop > 0 {
		lv.lines = lv.lines[drop:]
		lv.dropped += drop
		lv.top = max(lv.top-drop, 0)
		if lv.focus >= 0 {
			lv.focus = max(lv.focus-drop, -1)
		}
	}
}

func (lv *logView) all() []string {
	if lv.part == "" {
		return lv.lines
	}
	return append(append([]string{}, lv.lines...), lv.part)
}

// logPage is how many log lines fit in the modal (its border, title and
// bottom line take four rows).
func (m *model) logPage() int {
	if m.h <= 0 {
		return 20
	}
	return max(m.h-4, 1)
}

// rows is how many screen rows line takes (1 unless wrapping).
func (lv *logView) rows(line string) int {
	if lv.tw <= 0 {
		return 1
	}
	if ansi.StringWidth(line) > lv.tw {
		return strings.Count(ansi.Hardwrap(line, lv.tw, true), "\n") + 1
	}
	return 1
}

// maxTop is the highest first line that still fills the page: the last lines
// whose rows fit in page rows.
func (lv *logView) maxTop(n, page int) int {
	if lv.tw <= 0 {
		return max(n-page, 0)
	}
	all := lv.all()
	rows, i := 0, n
	for i > 0 {
		h := lv.rows(all[i-1])
		if rows+h > page {
			break
		}
		rows += h
		i--
	}
	return max(min(i, n-1), 0)
}

// start is the first visible line.
func (lv *logView) start(n, page int) int {
	maxTop := lv.maxTop(n, page)
	if lv.follow {
		return maxTop
	}
	return max(min(lv.top, maxTop), 0)
}

// scrollTo makes line the first visible one (clamped); reaching the end
// resumes following.
func (lv *logView) scrollTo(line, n, page int) {
	maxTop := lv.maxTop(n, page)
	lv.top = max(min(line, maxTop), 0)
	lv.follow = lv.top >= maxTop
}

// syncWrap sets the wrapped text width for the current terminal size; it must run
// before anything that measures lines (keys and rendering).
func (m *model) syncWrap() {
	lv := m.log
	lv.tw = 0
	if lv.wrap && m.w > 0 {
		gw := len(strconv.Itoa(lv.dropped + len(lv.all())))
		lv.tw = max(max(m.w-4, 10)-gw-3, 1)
	}
}

// reveal scrolls so line is visible, a few rows below the top.
func (lv *logView) reveal(line, n, page int) {
	lv.focus = line
	if lv.tw <= 0 {
		lv.scrollTo(line-page/3, n, page)
		return
	}
	// Wrapped: put the target about a third of the page down, in rows.
	all := lv.all()
	top, used := line, 0
	for top > 0 && line < len(all) {
		h := lv.rows(all[top-1])
		if used+h > page/3 {
			break
		}
		used += h
		top--
	}
	lv.scrollTo(top, n, page)
}

// drawnEnd is the index after the last line (partly) drawn from first.
func (lv *logView) drawnEnd(first, page int) int {
	all := lv.all()
	rows, i := 0, first
	for i < len(all) && rows < page {
		rows += lv.rows(all[i])
		i++
	}
	return i
}

// logKeys is the help bar of the console log.
func (m *model) logKeys() []key.Binding {
	if m.log.prompt != promptNone {
		return []key.Binding{keyPromptOK, keyPromptEsc}
	}
	return []key.Binding{core.KeyUp, core.KeyDown, keyLogPage, keyLogFind, keyLogNext, keyLogErr, keyLogWrap, keyLogGoto, keyLogEnd, keyLogBack, core.KeyQuit}
}

// logKey handles the keys of the log view; ok is false for keys it leaves to
// the common handling (quit).
func (m *model) logKey(msg tea.KeyMsg) (cmd tea.Cmd, ok bool) {
	lv := m.log
	m.syncWrap()
	if lv.prompt != promptNone {
		m.promptKey(msg)
		return nil, true
	}
	all := lv.all()
	n, page := len(all), m.logPage()
	cur := lv.start(n, page)
	switch {
	case key.Matches(msg, keyLogBack):
		m.closeLog()
	case key.Matches(msg, core.KeyUp):
		lv.scrollTo(cur-1, n, page)
	case key.Matches(msg, core.KeyDown):
		lv.scrollTo(cur+1, n, page)
	case key.Matches(msg, keyLogPgUp):
		lv.scrollTo(cur-page, n, page)
	case key.Matches(msg, keyLogPgDn):
		lv.scrollTo(cur+page, n, page)
	case key.Matches(msg, keyLogTop):
		lv.scrollTo(0, n, page)
	case key.Matches(msg, keyLogEnd):
		lv.follow = true
	case key.Matches(msg, keyLogFind):
		lv.prompt, lv.input = promptFind, ""
	case key.Matches(msg, keyLogGoto):
		lv.prompt, lv.input = promptGoto, ""
	case key.Matches(msg, keyLogNext):
		lv.findNext(+1, cur, page)
	case key.Matches(msg, keyLogPrev):
		lv.findNext(-1, cur, page)
	case key.Matches(msg, keyLogErr):
		lv.nextError(+1, cur, page)
	case key.Matches(msg, keyLogWrap):
		follow := lv.follow
		lv.wrap = !lv.wrap
		m.syncWrap()
		lv.scrollTo(cur, n, page)
		lv.follow = follow || lv.follow
	case key.Matches(msg, keyLogErrP):
		lv.nextError(-1, cur, page)
	default:
		return nil, false
	}
	return nil, true
}

// promptKey edits the open input; it takes every key, so q and ? are text.
func (m *model) promptKey(msg tea.KeyMsg) {
	lv := m.log
	switch msg.Type {
	case tea.KeyEsc:
		lv.prompt, lv.input = promptNone, ""
	case tea.KeyEnter:
		kind, text := lv.prompt, lv.input
		lv.prompt, lv.input = promptNone, ""
		n, page := len(lv.all()), m.logPage()
		if kind == promptGoto {
			lv.gotoLine(text, n, page)
		} else {
			lv.find(text, n, page)
		}
	case tea.KeyBackspace:
		if r := []rune(lv.input); len(r) > 0 {
			lv.input = string(r[:len(r)-1])
		}
	case tea.KeyCtrlU:
		lv.input = ""
	case tea.KeySpace:
		if lv.prompt == promptFind {
			lv.input += " "
		}
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if lv.prompt == promptFind || r >= '0' && r <= '9' {
				lv.input += string(r)
			}
		}
	}
}

// gotoLine scrolls to a line number as shown in the gutter.
func (lv *logView) gotoLine(text string, n, page int) {
	v, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n == 0 {
		return
	}
	total := lv.dropped + n
	line := max(min(v, total), 1)
	lv.status = fmt.Sprintf("line %d", line)
	if v > total {
		lv.status = fmt.Sprintf("line %d is past the end (%d lines)", v, total)
	}
	if line <= lv.dropped {
		lv.status = fmt.Sprintf("line %d was dropped; showing from %d", line, lv.dropped+1)
		line = lv.dropped + 1
	}
	lv.reveal(line-1-lv.dropped, n, page)
}

// find starts a search for text (wildcards * and _) from the first visible line.
func (lv *logView) find(text string, n, page int) {
	lv.query, lv.re, lv.focus, lv.status = text, widgets.Glob(text), -1, ""
	if lv.re == nil {
		return
	}
	lv.findNext(+1, lv.start(n, page)-1, page)
}

// findNext moves to the next (dir > 0) or previous match, wrapping around.
// from is the line to search away from.
func (lv *logView) findNext(dir, cur, page int) {
	if lv.re == nil {
		lv.status = "nothing to find: press / first"
		return
	}
	all := lv.all()
	n := len(all)
	from := cur
	if lv.focus >= 0 {
		from = lv.focus
	}
	for step := 1; step <= n; step++ {
		i := ((from+dir*step)%n + n) % n
		if lv.re.MatchString(all[i]) {
			wrapped := dir > 0 && i <= from || dir < 0 && i >= from
			lv.reveal(i, n, page)
			lv.status = fmt.Sprintf("match %d of %d for %q", lv.matchIndex(all, i), lv.matches(all), lv.query)
			if wrapped {
				lv.status += " (wrapped)"
			}
			return
		}
	}
	lv.focus = -1
	lv.status = fmt.Sprintf("no match for %q", lv.query)
}

// nextError moves to the next (dir > 0) or previous error line, wrapping
// around, and reports "error i/total" in the status.
func (lv *logView) nextError(dir, cur, page int) {
	all := lv.all()
	n := len(all)
	total := 0
	for _, l := range all {
		if isErrorLine(l) {
			total++
		}
	}
	if total == 0 {
		lv.status = "no errors found"
		return
	}
	from := cur - 1
	if dir < 0 {
		from = min(cur+page, n)
		if lv.tw > 0 {
			from = lv.drawnEnd(cur, page)
		}
	}
	if lv.focus >= 0 {
		from = lv.focus
	}
	for step := 1; step <= n; step++ {
		i := ((from+dir*step)%n + n) % n
		if !isErrorLine(all[i]) {
			continue
		}
		wrapped := dir > 0 && i <= from || dir < 0 && i >= from
		lv.reveal(i, n, page)
		rank := 0
		for _, l := range all[:i+1] {
			if isErrorLine(l) {
				rank++
			}
		}
		lv.status = fmt.Sprintf("error %d/%d", rank, total)
		if wrapped {
			lv.status += " (wrapped)"
		}
		return
	}
}

func (lv *logView) matches(all []string) (c int) {
	for _, l := range all {
		if lv.re.MatchString(l) {
			c++
		}
	}
	return
}

// matchIndex is the 1-based rank of line i among the matching lines.
func (lv *logView) matchIndex(all []string, i int) (c int) {
	for _, l := range all[:i+1] {
		if lv.re.MatchString(l) {
			c++
		}
	}
	return
}

// logView renders the modal: a bordered box over the whole body with the
// build and position on top, numbered lines, and the input or status below.
func (m *model) logView() string {
	lv := m.log
	st := runState{}
	if i := m.index(lv.job); i >= 0 {
		st = m.runs[i]
	}
	state := "finished"
	switch {
	case lv.err != nil:
		state = "error: " + errText(lv.err)
	case !lv.loaded:
		state = "loading…"
	case lv.more && lv.follow:
		state = "following"
	case lv.more:
		state = "paused (G to follow)"
	}
	m.syncWrap()
	all := lv.all()
	page := m.logPage()
	first := lv.start(len(all), page)

	iw := 0 // inner width; 0 = unbounded (no size known yet)
	if m.w > 0 {
		iw = max(m.w-4, 10)
	}
	gw := len(strconv.Itoa(lv.dropped + len(all)))
	tw := 0
	if iw > 0 {
		tw = max(iw-gw-3, 1)
	}
	var body []string
	last := first
	for i := first; i < len(all) && len(body) < page; i++ {
		text := all[i]
		pieces := []string{text}
		switch {
		case lv.tw > 0:
			pieces = strings.Split(ansi.Hardwrap(text, lv.tw, true), "\n")
		case tw > 0:
			pieces = []string{ansi.Truncate(text, tw, "…")}
		}
		base := lipgloss.NewStyle()
		if isErrorLine(text) {
			base = base.Foreground(m.th.Fail)
		}
		for j, piece := range pieces {
			if len(body) >= page {
				break
			}
			num := strings.Repeat(" ", gw)
			if j == 0 {
				num = fmt.Sprintf("%*d", gw, lv.dropped+i+1)
				if i == lv.focus {
					num = m.th.Selected.Render(num)
				} else {
					num = m.th.MutedTxt.Render(num)
				}
			}
			sep := " │ "
			if j > 0 {
				sep = " ┆ "
			}
			body = append(body, num+m.th.MutedTxt.Render(sep)+highlightWith(piece, lv.re, base))
		}
		last = i + 1
	}
	pos := ""
	if len(all) > 0 {
		pos = fmt.Sprintf("  lines %d-%d of %d", lv.dropped+first+1, lv.dropped+last, lv.dropped+len(all))
	}
	if lv.wrap {
		pos += "  wrapped"
	}
	what := ""
	if lv.what != "" {
		what = " › " + lv.what
	}
	head := m.badge(st) + "  " + m.jobName(lv.job) + fmt.Sprintf(" #%d", lv.num) + what + "  " + m.th.MutedTxt.Render(state+pos)
	rows := append([]string{fit(head, iw)}, body...)
	for m.h > 0 && len(rows) < page+1 {
		rows = append(rows, "")
	}
	rows = append(rows, fit(m.logFooter(), iw))

	box := m.th.Box
	if m.w > 0 {
		box = box.Width(m.w - 2)
	}
	if m.h > 0 {
		box = box.Height(m.h - 2)
	}
	return box.Render(strings.Join(rows, "\n"))
}

// logFooter is the open input, else the last find or go-to result, else a hint.
func (m *model) logFooter() string {
	lv := m.log
	switch lv.prompt {
	case promptFind:
		return m.th.Selected.Render("find (* any text, _ one char): ") + lv.input + "▏"
	case promptGoto:
		return m.th.Selected.Render("go to line: ") + lv.input + "▏"
	}
	if lv.status != "" {
		return m.th.MutedTxt.Render(lv.status)
	}
	return m.th.MutedTxt.Render("/ find · e/E error · w wrap · : go to line · pgup/pgdn page · g top · G follow · esc back")
}

// highlight marks the matches of re in s in reverse video.
func highlight(s string, re *regexp.Regexp) string {
	return highlightWith(s, re, lipgloss.NewStyle())
}

// highlightWith is highlight with the rest of s drawn in base.
func highlightWith(s string, re *regexp.Regexp, base lipgloss.Style) string {
	plain := func(t string) string {
		if t == "" {
			return t
		}
		return base.Render(t)
	}
	if re == nil {
		return plain(s)
	}
	var b strings.Builder
	prev := 0
	for _, p := range re.FindAllStringIndex(s, -1) {
		if p[1] == p[0] {
			continue
		}
		b.WriteString(plain(s[prev:p[0]]))
		b.WriteString(base.Reverse(true).Render(s[p[0]:p[1]]))
		prev = p[1]
	}
	b.WriteString(plain(s[prev:]))
	return b.String()
}

func fit(s string, w int) string {
	if w <= 0 {
		return s
	}
	return ansi.Truncate(s, w, "…")
}
