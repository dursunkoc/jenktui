package core

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// Shared bindings. Screens reuse these so keys stay consistent; screens
// handle them with key.Matches and emit Pop/Quit themselves.
var (
	KeyUp     = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	KeyDown   = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	KeyEnter  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select"))
	KeyBack   = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	KeyFilter = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	KeyQuit   = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit"))

	// KeyHelp opens the help panel and KeyCloseHelp closes it. The Router
	// handles both; screens never see them.
	KeyHelp      = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "all keys"))
	KeyCloseHelp = key.NewBinding(key.WithKeys("?", "esc"), key.WithHelp("esc/?", "close help"))
)

// HelpGroup is a titled set of keys in the help panel.
type HelpGroup struct {
	Title string
	Keys  []key.Binding
}

// FullHelper is implemented by screens with more keys than the help bar
// should hold. Their Keys() lists only the few most useful ones; the Router
// adds "? all keys" to the bar and ? opens a panel with every group from
// FullHelp. FullHelp returns nil while a text field has focus (or the screen
// has nothing more to show): ? is then passed to the screen as typed text.
type FullHelper interface {
	FullHelp() []HelpGroup
}

const (
	barSep   = " · " // between items in the help bar, drawn in the border colour
	panelGap = 3     // columns between groups in the help panel
)

// Help renders the one-line help bar and the help panel.
type Help struct {
	key, desc, ellipsis, title lipgloss.Style
	rule                       lipgloss.Style
	sep                        string // barSep, styled
}

// NewHelp returns a Help using the default theme.
func NewHelp() Help {
	th := NewTheme()
	return Help{
		key:      lipgloss.NewStyle().Bold(true).Foreground(th.Accent),
		desc:     lipgloss.NewStyle().Foreground(th.Muted),
		ellipsis: lipgloss.NewStyle().Foreground(th.Muted),
		title:    th.Title,
		rule:     lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(th.Border).PaddingLeft(1),
		sep:      lipgloss.NewStyle().Foreground(th.Border).Render(barSep),
	}
}

func (h Help) item(b key.Binding) string {
	return h.key.Render(b.Help().Key) + " " + h.desc.Render(b.Help().Desc)
}

// View renders the enabled bindings on one line no wider than width. When
// more is set, "? all keys" ends the line and is never cut; bindings that do
// not fit are dropped from the end and replaced by "…".
func (h Help) View(keys []key.Binding, more bool, width int) string {
	var items []string
	for _, b := range keys {
		if b.Enabled() {
			items = append(items, h.item(b))
		}
	}
	tail := ""
	if more {
		tail = h.item(KeyHelp)
	}
	return fitLine(items, tail, h.sep, h.ellipsis.Render("…"), width)
}

// fitLine joins items with sep, keeping as many as fit in width together
// with tail (always last when not empty). Dropped items are marked with
// ellipsis. width <= 0 means no limit.
func fitLine(items []string, tail, sep, ellipsis string, width int) string {
	all := items
	if tail != "" {
		all = append(append([]string(nil), items...), tail)
	}
	line := strings.Join(all, sep)
	if width <= 0 || lipgloss.Width(line) <= width {
		return line
	}
	reserve := lipgloss.Width(sep + ellipsis)
	if tail != "" {
		reserve += lipgloss.Width(sep + tail)
	}
	var kept []string
	used := 0
	for _, it := range items {
		w := lipgloss.Width(it)
		if len(kept) > 0 {
			w += lipgloss.Width(sep)
		}
		if used+w+reserve > width {
			break
		}
		kept = append(kept, it)
		used += w
	}
	kept = append(kept, ellipsis)
	if tail != "" {
		kept = append(kept, tail)
	}
	return strings.Join(kept, sep)
}

// Panel renders the groups as columns under a rule, as many columns side by
// side as fit in width, with the groups split across columns so the panel is
// as short as possible. Disabled keys and empty groups are skipped.
func (h Help) Panel(groups []HelpGroup, width int) string {
	var blocks []string
	for _, g := range groups {
		if b := h.group(g); b != "" {
			blocks = append(blocks, b)
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	cols := layoutColumns(blocks, max(width-1, 1)) // 1: the rule's left padding
	rendered := make([]string, len(cols))
	for i, c := range cols {
		col := strings.Join(c, "\n\n")
		if i < len(cols)-1 {
			col = lipgloss.NewStyle().PaddingRight(panelGap).Render(col)
		}
		rendered[i] = col
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
	return h.rule.Width(width).Render(body)
}

// group renders one group: its title, then one key per line with the keys
// padded to a common width so the descriptions line up.
func (h Help) group(g HelpGroup) string {
	var ks []key.Binding
	kw := 0
	for _, b := range g.Keys {
		if b.Enabled() {
			ks = append(ks, b)
			kw = max(kw, lipgloss.Width(b.Help().Key))
		}
	}
	if len(ks) == 0 {
		return ""
	}
	lines := []string{h.title.Render(g.Title)}
	for _, b := range ks {
		k := b.Help().Key
		lines = append(lines, h.key.Render(k)+strings.Repeat(" ", kw-lipgloss.Width(k)+1)+b.Help().Desc)
	}
	return strings.Join(lines, "\n")
}

// layoutColumns splits blocks, in order, into columns no wider than width
// in total, choosing the split with the shortest tallest column (fewer
// columns on a tie). If nothing fits, every block gets its own row.
func layoutColumns(blocks []string, width int) [][]string {
	var best [][]string
	bestH := -1
	var try func(start int, cols [][]string)
	try = func(start int, cols [][]string) {
		if start == len(blocks) {
			if panelWidth(cols) > width {
				return
			}
			h := 0
			for _, c := range cols {
				ch := len(c) - 1 // blank lines between stacked groups
				for _, b := range c {
					ch += lipgloss.Height(b)
				}
				h = max(h, ch)
			}
			if bestH < 0 || h < bestH || h == bestH && len(cols) < len(best) {
				bestH, best = h, append([][]string(nil), cols...)
			}
			return
		}
		for end := start + 1; end <= len(blocks); end++ {
			try(end, append(cols, blocks[start:end]))
		}
	}
	try(0, nil)
	if best == nil {
		return [][]string{blocks}
	}
	return best
}

func panelWidth(cols [][]string) int {
	w := panelGap * (len(cols) - 1)
	for _, c := range cols {
		cw := 0
		for _, b := range c {
			cw = max(cw, lipgloss.Width(b))
		}
		w += cw
	}
	return w
}

// MinWidth and MinHeight are the smallest supported terminal size.
const (
	MinWidth  = 60
	MinHeight = 16
)

// TooSmall reports whether w x h is below the minimum; if so it returns the
// message to render instead of the UI. A zero size (before the first
// WindowSizeMsg) is treated as too small.
func TooSmall(w, h int) (string, bool) {
	if w >= MinWidth && h >= MinHeight {
		return "", false
	}
	return "terminal too small, need at least 60x16", true
}

// ErrorView renders a titled error with a hint line, for failed loads and
// actions. err may be nil (only title and hint are shown). Pass any error;
// callers map domain errors to a hint themselves.
func (t Theme) ErrorView(title string, err error, hint string) string {
	lines := []string{t.ErrTitle.Render(title)}
	if err != nil {
		lines = append(lines, err.Error())
	}
	if hint != "" {
		lines = append(lines, "", t.MutedTxt.Render(hint))
	}
	return t.Box.Render(strings.Join(lines, "\n"))
}

// NoticeView renders a neutral titled notice with an optional hint.
func (t Theme) NoticeView(title, hint string) string {
	lines := []string{t.Title.Render(title)}
	if hint != "" {
		lines = append(lines, "", t.MutedTxt.Render(hint))
	}
	return t.Box.Render(strings.Join(lines, "\n"))
}
