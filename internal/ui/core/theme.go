package core

import "github.com/charmbracelet/lipgloss"

// Status is a build/job state shown as a badge.
type Status int

const (
	StatusUnknown Status = iota
	StatusSuccess
	StatusFailed
	StatusRunning
	StatusQueued
	StatusMissing // job no longer exists on the server
)

// Theme holds every style screens should use; do not build ad-hoc colors.
type Theme struct {
	Accent, OK, Warn, Fail, Running, Muted, Border lipgloss.AdaptiveColor

	Title    lipgloss.Style // header bar
	Selected lipgloss.Style // highlighted list row
	MutedTxt lipgloss.Style
	Box      lipgloss.Style // bordered panel
	ErrTitle lipgloss.Style
}

// NewTheme returns the default theme, adaptive to light/dark terminals.
func NewTheme() Theme {
	t := Theme{
		Accent:  lipgloss.AdaptiveColor{Light: "#5F3DC4", Dark: "#B197FC"},
		OK:      lipgloss.AdaptiveColor{Light: "#2B8A3E", Dark: "#69DB7C"},
		Warn:    lipgloss.AdaptiveColor{Light: "#E67700", Dark: "#FFD43B"},
		Fail:    lipgloss.AdaptiveColor{Light: "#C92A2A", Dark: "#FF6B6B"},
		Running: lipgloss.AdaptiveColor{Light: "#1864AB", Dark: "#74C0FC"},
		Muted:   lipgloss.AdaptiveColor{Light: "#868E96", Dark: "#868E96"},
		Border:  lipgloss.AdaptiveColor{Light: "#CED4DA", Dark: "#495057"},
	}
	t.Title = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	t.Selected = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	t.MutedTxt = lipgloss.NewStyle().Foreground(t.Muted)
	t.Box = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Border).Padding(0, 1)
	t.ErrTitle = lipgloss.NewStyle().Bold(true).Foreground(t.Fail)
	return t
}

// Badge renders s as a short colored label.
func (t Theme) Badge(s Status) string {
	label, c := "UNKNOWN", t.Muted
	switch s {
	case StatusSuccess:
		label, c = "SUCCESS", t.OK
	case StatusFailed:
		label, c = "FAILED", t.Fail
	case StatusRunning:
		label, c = "RUNNING", t.Running
	case StatusQueued:
		label, c = "QUEUED", t.Warn
	case StatusMissing:
		label, c = "MISSING", t.Warn
	}
	return lipgloss.NewStyle().Bold(true).Foreground(c).Render("● " + label)
}
