package core

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// colourSGR matches an SGR sequence that sets a foreground or background colour.
var colourSGR = regexp.MustCompile("\x1b\\[(?:[0-9]+;)*(?:3[0-8]|4[0-8]|9[0-7])(?:;|m)")

// setProfile forces the default renderer and restores it afterwards.
func setProfile(t *testing.T, p termenv.Profile, dark bool) {
	t.Helper()
	oldP, oldD := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(p)
	lipgloss.SetHasDarkBackground(dark)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(oldP)
		lipgloss.SetHasDarkBackground(oldD)
	})
}

func TestAsciiProfileDropsAttributes(t *testing.T) {
	// Documents why NewTheme switches profile under NO_COLOR: termenv's Ascii
	// profile writes no escape sequences at all, bold and reverse included.
	setProfile(t, termenv.Ascii, true)
	th := newTheme(false)
	for name, got := range map[string]string{
		"badge":   th.Badge(StatusFailed),
		"reverse": lipgloss.NewStyle().Reverse(true).Render("x"),
		"bold":    th.Selected.Render("x"),
	} {
		if strings.Contains(got, "\x1b") {
			t.Errorf("%s under Ascii has escapes: %q", name, got)
		}
	}
}

func TestAdaptiveColorFollowsBackground(t *testing.T) {
	setProfile(t, termenv.TrueColor, false)
	light := newTheme(false).Badge(StatusFailed)
	if !strings.Contains(light, "38;2;201;42;42") { // #C92A2A
		t.Errorf("light background: want the Light Fail colour, got %q", light)
	}
	lipgloss.SetHasDarkBackground(true)
	dark := newTheme(false).Badge(StatusFailed)
	if !strings.Contains(dark, "38;2;255;107;107") { // #FF6B6B
		t.Errorf("dark background: want the Dark Fail colour, got %q", dark)
	}
}

func TestNoColorKeepsAttributesDropsColours(t *testing.T) {
	setProfile(t, termenv.ANSI, true) // what NewTheme switches to under NO_COLOR
	th := newTheme(true)
	for name, got := range map[string]string{
		"badge":    th.Badge(StatusFailed),
		"selected": th.Selected.Render("x"),
		"title":    th.Title.Render("x"),
		"errtitle": th.ErrTitle.Render("x"),
		"failtxt":  th.FailTxt.Render("x"),
		"muted":    th.MutedTxt.Render("x"),
		"border":   lipgloss.NewStyle().Foreground(th.Border).Render("x"),
		"box":      th.Box.Render("x"),
	} {
		if colourSGR.MatchString(got) {
			t.Errorf("%s carries a colour: %q", name, got)
		}
	}
	if got := th.Badge(StatusFailed); !strings.Contains(got, "\x1b[1m") || !strings.Contains(got, "FAILED") {
		t.Errorf("badge should stay bold with its label: %q", got)
	}
	if got := th.FailTxt.Render("x"); !strings.Contains(got, "\x1b[1m") {
		t.Errorf("FailTxt must be bold without colour: %q", got)
	}
	if got := lipgloss.NewStyle().Reverse(true).Render("x"); !strings.Contains(got, "\x1b[7m") {
		t.Errorf("reverse video must survive: %q", got)
	}
}

func TestFailTxtWithColourIsOnlyColoured(t *testing.T) {
	setProfile(t, termenv.TrueColor, true)
	got := newTheme(false).FailTxt.Render("x")
	if !strings.Contains(got, "38;2;255;107;107") || strings.Contains(got, "\x1b[1") {
		t.Errorf("FailTxt with colour should be the Fail colour, not bold: %q", got)
	}
}

func TestApplyColorEnv(t *testing.T) {
	// Under go test stdout is not a terminal, so the profile stays as set and
	// only the colours are emptied.
	setProfile(t, termenv.Ascii, true)
	old := noColor
	t.Cleanup(func() { noColor = old })

	t.Setenv("NO_COLOR", "1")
	ApplyColorEnv()
	if th := NewTheme(); th.Fail != (lipgloss.AdaptiveColor{}) || th.Placeholder.GetForeground() != (lipgloss.AdaptiveColor{}) {
		t.Error("NO_COLOR: colours should be empty")
	}
	t.Setenv("NO_COLOR", "")
	ApplyColorEnv()
	if NewTheme().Fail == (lipgloss.AdaptiveColor{}) {
		t.Error("without NO_COLOR the theme keeps its colours")
	}
}

func TestNewThemeIsPure(t *testing.T) {
	setProfile(t, termenv.TrueColor, true)
	old := noColor
	t.Cleanup(func() { noColor = old })
	noColor = true
	NewTheme()
	if lipgloss.ColorProfile() != termenv.TrueColor {
		t.Error("NewTheme must not change the renderer profile")
	}
}

func TestPlaceholderFaintOnlyWithoutColour(t *testing.T) {
	setProfile(t, termenv.ANSI, true)
	if got := newTheme(true).Placeholder.Render("x"); !strings.Contains(got, "\x1b[2m") || colourSGR.MatchString(got) {
		t.Errorf("NO_COLOR placeholder should be faint and uncoloured: %q", got)
	}
	if got := newTheme(false).Placeholder.Render("x"); strings.Contains(got, "\x1b[2m") || !colourSGR.MatchString(got) {
		t.Errorf("coloured placeholder should be muted, not faint: %q", got)
	}
}
