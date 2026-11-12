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
	keyLog     = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "console log"))
	keyLogEnd  = key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "follow"))
	keyLogTop  = key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top"))
	keyLogPage = key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "page"))
	keyLogPgUp = key.NewBinding(key.WithKeys("pgup", "ctrl+b"))
	keyLogPgDn = key.NewBinding(key.WithKeys("pgdown", "ctrl+f", " "))
	keyLogFind = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "find"))
	keyLogNext = key.NewBinding(key.WithKeys("n"), key.WithHelp("n/N", "next/prev match"))
	keyLogPrev = key.NewBinding(key.WithKeys("N"))
	keyLogGoto = key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "go to line"))
	keyLogBack = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to details"))

	keyPromptOK  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "ok"))
	keyPromptEsc = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))

	logHATokens = regexp.MustCompile(`\x1b\[8mha:////[A-Za-z0-9+/=]*\x1b\[0m`)
)

type logPrompt int

const (
	promptNone logPrompt = iota
	promptFind
	promptGoto
)

// logView is the console log of one build, opened from the details view and
// drawn as a bordered modal with line numbers. It follows new output until the
// user scrolls up, and polls Jenkins while the build is still producing output.
type logView struct {
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

// openLog starts the log view of the selected row's build.
func (m *model) openLog() tea.Cmd {
	i := m.selected()
	if i < 0 || m.runs[i].num == 0 {
		return nil
	}
	st := m.runs[i]
	m.logGen++
	m.log = &logView{job: st.run.Job, num: st.num, follow: true, focus: -1}
	return m.fetchLog(0)
}

func (m *model) closeLog() {
	m.logGen++
	m.log = nil
}

func (m *model) fetchLog(start int64) tea.Cmd {
	client, timeout, gen, job, num := m.deps.Client, m.deps.PollTimeout, m.logGen, m.log.job, m.log.num
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
			if errors.Is(msg.err, jenkins.ErrBuildNotFound) {
				lv.more = false
				return nil
			}
			return m.logSchedule(m.deps.SlowInterval)
		}
		lv.err = nil
		lv.append(msg.chunk.Text)
		lv.next, lv.more = msg.chunk.Next, msg.chunk.More
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

// start is the first visible line.
func (lv *logView) start(n, page int) int {
	maxTop := max(n-page, 0)
	if lv.follow {
		return maxTop
	}
	return max(min(lv.top, maxTop), 0)
}

// scrollTo makes line the first visible one (clamped); reaching the end
// resumes following.
func (lv *logView) scrollTo(line, n, page int) {
	maxTop := max(n-page, 0)
	lv.top = max(min(line, maxTop), 0)
	lv.follow = lv.top >= maxTop
}

// reveal scrolls so line is visible, a few rows below the top.
func (lv *logView) reveal(line, n, page int) {
	lv.focus = line
	lv.scrollTo(line-page/3, n, page)
}

// logKey handles the keys of the log view; ok is false for keys it leaves to
// the common handling (quit).
func (m *model) logKey(msg tea.KeyMsg) (cmd tea.Cmd, ok bool) {
	lv := m.log
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
	all := lv.all()
	page := m.logPage()
	first := lv.start(len(all), page)
	last := min(first+page, len(all))

	iw := 0 // inner width; 0 = unbounded (no size known yet)
	if m.w > 0 {
		iw = max(m.w-4, 10)
	}
	pos := ""
	if len(all) > 0 {
		pos = fmt.Sprintf("  lines %d-%d of %d", lv.dropped+first+1, lv.dropped+last, lv.dropped+len(all))
	}
	head := m.badge(st) + "  " + m.jobName(lv.job) + fmt.Sprintf(" #%d", lv.num) + "  " + m.th.MutedTxt.Render(state+pos)
	rows := []string{fit(head, iw)}

	gw := len(strconv.Itoa(lv.dropped + len(all)))
	tw := 0
	if iw > 0 {
		tw = max(iw-gw-3, 1)
	}
	for i := first; i < last; i++ {
		num := fmt.Sprintf("%*d", gw, lv.dropped+i+1)
		if i == lv.focus {
			num = m.th.Selected.Render(num)
		} else {
			num = m.th.MutedTxt.Render(num)
		}
		text := all[i]
		if tw > 0 {
			text = ansi.Truncate(text, tw, "…")
		}
		rows = append(rows, num+m.th.MutedTxt.Render(" │ ")+highlight(text, lv.re))
	}
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
	return m.th.MutedTxt.Render("/ find · : go to line · pgup/pgdn page · g top · G follow · esc back")
}

// highlight marks the matches of re in s in reverse video.
func highlight(s string, re *regexp.Regexp) string {
	if re == nil {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, p := range re.FindAllStringIndex(s, -1) {
		if p[1] == p[0] {
			continue
		}
		b.WriteString(s[prev:p[0]])
		b.WriteString(lipgloss.NewStyle().Reverse(true).Render(s[p[0]:p[1]]))
		prev = p[1]
	}
	b.WriteString(s[prev:])
	return b.String()
}

func fit(s string, w int) string {
	if w <= 0 {
		return s
	}
	return ansi.Truncate(s, w, "…")
}
