package core

import (
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
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
)

// Help renders a one-line help bar from key bindings.
type Help struct{ m help.Model }

// NewHelp returns a Help bar.
func NewHelp() Help { return Help{m: help.New()} }

// View renders bindings (disabled ones are skipped) truncated to width.
func (h Help) View(keys []key.Binding, width int) string {
	h.m.Width = width
	return h.m.ShortHelpView(keys)
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
