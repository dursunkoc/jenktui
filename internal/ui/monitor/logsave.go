package monitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
		lv.status = "saved to " + s.path
	}
	return nil
}

// saveLog starts writing the log to a file. A whole-build log is fetched again
// in full from offset 0 (the view keeps only the last 10,000 lines); a stage
// or step log saves what is shown.
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
		text := shown
		if !stage {
			var err error
			if text, err = fetchWholeLog(client, timeout, job, num); err != nil {
				return logSavedMsg{gen: gen, err: err}
			}
		}
		path, err := writeNewFile(dir, logFileName(job, num), text)
		return logSavedMsg{gen: gen, path: path, err: err}
	}
}

// fetchWholeLog reads the console log of a build from offset 0 to its current
// end and cleans it like the view does (no limit on the line count).
func fetchWholeLog(client Client, timeout time.Duration, job string, num int) (string, error) {
	var sb strings.Builder
	var off int64
	for {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		c, err := client.ConsoleText(ctx, job, num, off)
		cancel()
		if err != nil {
			return "", err
		}
		sb.WriteString(c.Text)
		if !c.More || c.Next <= off {
			break
		}
		off = c.Next
	}
	return cleanConsole(sb.String()), nil
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

// logFileName is <job>-<n>.log with "/" in the job name replaced by "_".
func logFileName(job string, num int) string {
	return fmt.Sprintf("%s-%d.log", strings.ReplaceAll(job, "/", "_"), num)
}

// writeNewFile creates name in dir (empty: the working directory) and never
// overwrites: when it exists it tries name-1, name-2 and so on. It returns the
// absolute path written.
func writeNewFile(dir, name, text string) (string, error) {
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
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.WriteString(text)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return "", werr
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return path, nil
	}
	return "", errors.New("no free file name")
}
