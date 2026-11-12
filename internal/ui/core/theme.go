package core

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Status is a build/job state shown as a badge.
type Status int

const (
	StatusUnknown Status = iota
	StatusSuccess
	StatusFailed
	StatusRunning
	StatusQueued
	StatusMissing // job no longer exists on the server
	StatusUnstable
	StatusAborted
)

// Theme holds every style screens should use; do not build ad-hoc colors.
type Theme struct {
	Accent, OK, Warn, Fail, Running, Muted, Border lipgloss.AdaptiveColor

	Title    lipgloss.Style // header bar
	Selected lipgloss.Style // highlighted list row
	MutedTxt lipgloss.Style
	Box      lipgloss.Style // bordered panel
	ErrTitle lipgloss.Style
	// FailTxt is for text drawn in the Fail colour (error lines, failed
	// counts, error messages). With colour it is only the Fail colour; with
	// NO_COLOR it is bold, so the text still stands out.
	FailTxt lipgloss.Style
}

// NewTheme returns the default theme, adaptive to light/dark terminals.
//
// NO_COLOR (and CLICOLOR=0) are honoured by lipgloss/termenv on their own, but
// termenv's Ascii profile also drops bold and reverse video, which the cursor
// row, the log find highlight and the status badges rely on. When the
// environment asks for no colour and the terminal could show colour, NewTheme
// therefore makes every theme colour empty (no colour sequence is written)
// and switches the default renderer to the ANSI profile so that bold and
// reverse still come out. Output that is not a terminal stays plain.
func NewTheme() Theme {
	noColor := termenv.EnvNoColor()
	if noColor && termenv.NewOutput(os.Stdout).ColorProfile() != termenv.Ascii {
		lipgloss.SetColorProfile(termenv.ANSI)
	}
	return newTheme(noColor)
}

func newTheme(noColor bool) Theme {
	t := Theme{
		Accent:  lipgloss.AdaptiveColor{Light: "#5F3DC4", Dark: "#B197FC"},
		OK:      lipgloss.AdaptiveColor{Light: "#2B8A3E", Dark: "#69DB7C"},
		Warn:    lipgloss.AdaptiveColor{Light: "#E67700", Dark: "#FFD43B"},
		Fail:    lipgloss.AdaptiveColor{Light: "#C92A2A", Dark: "#FF6B6B"},
		Running: lipgloss.AdaptiveColor{Light: "#1864AB", Dark: "#74C0FC"},
		Muted:   lipgloss.AdaptiveColor{Light: "#868E96", Dark: "#868E96"},
		Border:  lipgloss.AdaptiveColor{Light: "#CED4DA", Dark: "#495057"},
	}
	if noColor {
		// An empty AdaptiveColor renders no colour sequence at all.
		none := lipgloss.AdaptiveColor{}
		t.Accent, t.OK, t.Warn, t.Fail, t.Running, t.Muted, t.Border = none, none, none, none, none, none, none
	}
	t.Title = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	t.Selected = lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	t.MutedTxt = lipgloss.NewStyle().Foreground(t.Muted)
	t.Box = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Border).Padding(0, 1)
	t.ErrTitle = lipgloss.NewStyle().Bold(true).Foreground(t.Fail)
	t.FailTxt = lipgloss.NewStyle().Foreground(t.Fail).Bold(noColor)
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
	case StatusUnstable:
		label, c = "UNSTABLE", t.Warn
	case StatusAborted:
		label, c = "ABORTED", t.Muted
	}
	return lipgloss.NewStyle().Bold(true).Foreground(c).Render("● " + label)
}
