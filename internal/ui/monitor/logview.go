package monitor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

// maxLogLines bounds the lines kept in memory; older lines are dropped.
const maxLogLines = 10000

var (
	keyLog      = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "console log"))
	keyLogEnd   = key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "follow"))
	keyLogTop   = key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top"))
	keyLogPgUp  = key.NewBinding(key.WithKeys("pgup"))
	keyLogPgDn  = key.NewBinding(key.WithKeys("pgdown"))
	keyLogBack  = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to details"))
	logHATokens = regexp.MustCompile(`\x1b\[8mha:////[A-Za-z0-9+/=]*\x1b\[0m`)
)

// logView is the console log of one build, opened from the details view. It
// follows new output until the user scrolls up, and polls Jenkins while the
// build is still producing output.
type logView struct {
	job    string
	num    int
	lines  []string // complete lines
	part   string   // trailing text without a newline yet
	next   int64    // offset to ask Jenkins for
	more   bool     // Jenkins expects more output
	loaded bool     // the first chunk arrived
	err    error
	follow bool
	top    int // first visible line when not following
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
	m.log = &logView{job: st.run.Job, num: st.num, follow: true}
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
		lv.top = max(lv.top-drop, 0)
	}
}

func (lv *logView) all() []string {
	if lv.part == "" {
		return lv.lines
	}
	return append(append([]string{}, lv.lines...), lv.part)
}

// page is how many log lines fit under the two header lines.
func (m *model) logPage() int {
	if m.h <= 0 {
		return 20
	}
	return max(m.h-2, 1)
}

// logKey handles the keys of the log view; ok is false for keys it leaves to
// the common handling (quit).
func (m *model) logKey(msg tea.KeyMsg) (cmd tea.Cmd, ok bool) {
	lv := m.log
	n, page := len(lv.all()), m.logPage()
	maxTop := max(n-page, 0)
	scroll := func(to int) {
		if lv.follow {
			lv.top = maxTop
		}
		lv.top = max(min(to, maxTop), 0)
		lv.follow = lv.top >= maxTop
	}
	cur := lv.top
	if lv.follow {
		cur = maxTop
	}
	switch {
	case key.Matches(msg, keyLogBack):
		m.closeLog()
	case keyUpDown(msg) < 0:
		scroll(cur - 1)
	case keyUpDown(msg) > 0:
		scroll(cur + 1)
	case key.Matches(msg, keyLogPgUp):
		scroll(cur - page)
	case key.Matches(msg, keyLogPgDn):
		scroll(cur + page)
	case key.Matches(msg, keyLogTop):
		scroll(0)
	case key.Matches(msg, keyLogEnd):
		lv.follow = true
	default:
		return nil, false
	}
	return nil, true
}

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
	head := m.badge(st) + "  " + m.jobName(lv.job) + fmt.Sprintf(" #%d", lv.num) + "  " + m.th.MutedTxt.Render(state)
	out := []string{m.fit(head), ""}
	all := lv.all()
	page := m.logPage()
	start := lv.top
	if lv.follow || start > len(all)-page {
		start = max(len(all)-page, 0)
	}
	for _, l := range all[start:min(start+page, len(all))] {
		out = append(out, m.fit(l))
	}
	return strings.Join(out, "\n")
}

func keyUpDown(msg tea.KeyMsg) int {
	switch {
	case key.Matches(msg, core.KeyUp):
		return -1
	case key.Matches(msg, core.KeyDown):
		return 1
	}
	return 0
}
