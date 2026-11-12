package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/sysint"
)

// logSavedMsg is the result of saving the log (S). It is dropped when the log
// it belongs to was closed or replaced (logGen moved on).
type logSavedMsg struct {
	gen  int
	path string
	err  error
}

func (s logSavedMsg) apply(m *model) tea.Cmd {
	lv := m.log
	if s.gen != m.logGen || lv == nil {
		return nil
	}
	lv.saving = false
	if s.err != nil {
		lv.status = "save failed: " + errText(s.err)
	} else {
		const label = "saved to "
		lv.status = label + displayPath(s.path, m.w-4-len(label))
	}
	return nil
}

// saveLog starts writing the log to a file. A whole-build log is fetched again
// in full from offset 0 (the view keeps only the last 10,000 lines) and
// streamed to the file; a stage or step log saves what is shown.
func (m *model) saveLog() tea.Cmd {
	lv := m.log
	if lv.saving {
		return nil
	}
	lv.saving = true
	lv.status = "saving…"
	client, timeout, gen := m.deps.Client, m.deps.PollTimeout, m.logGen
	job, num, dir := lv.job, lv.num, m.deps.SaveDir
	var shown string
	stage := len(lv.nodes) > 0
	if stage {
		shown = strings.Join(lv.all(), "\n") + "\n"
	}
	return func() tea.Msg {
		f, path, err := createNew(dir, logFileName(job, num))
		if err != nil {
			return logSavedMsg{gen: gen, err: err}
		}
		if stage {
			_, err = io.WriteString(f, shown)
		} else {
			err = streamWholeLog(client, timeout, job, num, f)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
			return logSavedMsg{gen: gen, err: err}
		}
		return logSavedMsg{gen: gen, path: path}
	}
}

// streamWholeLog copies the console log of a build from offset 0 to w, up to
// the end it had at the first response (a running build is not followed). Each
// chunk is cleaned like the view cleans it and only complete lines are
// written; the partial last line waits for the next chunk.
func streamWholeLog(client Client, timeout time.Duration, job string, num int, w io.Writer) error {
	lc := &lineCleaner{w: w}
	var off, end int64
	for first := true; ; first = false {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		c, err := client.ConsoleText(ctx, job, num, off)
		cancel()
		if err != nil {
			return err
		}
		if first {
			end = c.Next
		}
		if err := lc.write(c.Text); err != nil {
			return err
		}
		if !c.More || c.Next <= off || c.Next >= end {
			break
		}
		off = c.Next
	}
	return lc.flush()
}

// lineCleaner cleans console text chunk by chunk, writing only complete lines
// (escapes and hidden notes never cross a newline) and carrying the partial
// last line to the next chunk.
type lineCleaner struct {
	w     io.Writer
	carry string
}

func (l *lineCleaner) write(chunk string) error {
	text := l.carry + chunk
	i := strings.LastIndex(text, "\n")
	if i < 0 {
		l.carry = text
		return nil
	}
	l.carry = text[i+1:]
	_, err := io.WriteString(l.w, cleanConsole(text[:i+1]))
	return err
}

func (l *lineCleaner) flush() error {
	if l.carry == "" {
		return nil
	}
	_, err := io.WriteString(l.w, cleanConsole(l.carry))
	l.carry = ""
	return err
}

// cleanConsole drops terminal escapes, Jenkins' hidden annotations and the
// earlier versions of rewritten progress lines.
func cleanConsole(text string) string {
	text = ansi.Strip(logHATokens.ReplaceAllString(text, ""))
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if j := strings.LastIndex(l, "\r"); j >= 0 {
			lines[i] = l[j+1:]
		}
	}
	return strings.Join(lines, "\n")
}

// logFileName is <job>-<n>.log with the job name decoded (a branch shows as
// app/feature/x, not app/feature%2Fx) and "/" replaced by "_".
func logFileName(job string, num int) string {
	if d, err := url.PathUnescape(job); err == nil {
		job = d
	}
	return fmt.Sprintf("%s-%d.log", strings.ReplaceAll(job, "/", "_"), num)
}

// createNew creates name in dir (empty: the working directory) and never
// overwrites: when it exists it tries name-1, name-2 and so on. It returns the
// open file and its absolute path.
func createNew(dir, name string) (*os.File, string, error) {
	if dir == "" {
		dir = "."
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		path := filepath.Join(dir, n)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return f, path, nil
	}
	return nil, "", errors.New("no free file name")
}

// displayPath shortens an absolute path for the footer: relative to the
// working directory when inside it, else cut from the left so the file name
// survives. max <= 0 means no limit.
func displayPath(path string, max int) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			path = rel
		}
	}
	if r := []rune(path); max > 1 && len(r) > max {
		path = "…" + string(r[len(r)-(max-1):])
	}
	return path
}

// logSysDone is the outcome of o or y in the log. It shows in the log's footer
// (the title-row status line is hidden while the log is open) and is dropped
// when the log it belongs to was closed or replaced.
type logSysDone struct {
	gen  int
	text string
	err  error
}

func (d logSysDone) apply(m *model) tea.Cmd {
	if d.gen != m.logGen || m.log == nil {
		return nil
	}
	if d.err != nil {
		m.log.status = shortErr(d.err)
	} else {
		m.log.status = d.text
	}
	return nil
}

// copyLogLine copies the current line: the focused match, error or go-to
// target, else the last line while following, else the first visible line.
func (m *model) copyLogLine(cur, n, page int) tea.Cmd {
	lv := m.log
	if n == 0 {
		return nil
	}
	i := cur
	switch {
	case lv.focus >= 0:
		i = lv.focus
	case lv.follow:
		i = n - 1
	}
	text, sys, gen := lv.all()[min(i, n-1)], m.deps.Sys, m.logGen
	return func() tea.Msg {
		route, err := sys.Copy(text)
		switch {
		case err != nil:
			return logSysDone{gen: gen, err: err}
		case route == sysint.RouteTerminal:
			return logSysDone{gen: gen, text: "sent to terminal clipboard"}
		}
		return logSysDone{gen: gen, text: "copied"}
	}
}

// openLogBuild opens the build in the browser, with the URL built from the
// configured base URL (never from the url Jenkins reports).
func (m *model) openLogBuild() tea.Cmd {
	lv, gen, sys := m.log, m.logGen, m.deps.Sys
	u, ok := m.deps.Client.(urler)
	if !ok {
		lv.status = "no build URL available"
		return nil
	}
	url := u.BuildURL(lv.job, lv.num)
	return func() tea.Msg {
		if err := sys.Open(url); err != nil {
			return logSysDone{gen: gen, err: err}
		}
		return logSysDone{gen: gen, text: "opened"}
	}
}
