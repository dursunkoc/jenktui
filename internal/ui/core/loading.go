package core

import (
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Loading is the shared loading indicator: a spinner that only ticks while
// loading is active. Embed it as a field of a screen.
//
// Usage:
//
//	load := core.NewLoading(theme)
//	cmd := load.Start()            // begin; batch cmd with the fetch (nil if already active)
//	case spinner.TickMsg: cmd = load.Update(msg) // forward ticks; stale/idle ticks are dropped
//	load.Stop()                    // fetch finished; the tick chain ends by itself
//	load.View("refreshing…")       // inline form, for a header/status line
//	load.Modal("Loading jobs…", w, h) // blocking form, a centered Box
//
// Every Start tags its first tick, so a replayed or older tick never starts a
// second chain (the spinner would animate at double speed).
//
// Ticks are plain tea.Cmds (no goroutines). A tick that arrives while the
// indicator is stopped, or that belongs to another spinner or an older tick
// chain, is ignored and schedules nothing, so idle screens never redraw.
//
// Minimum visible time: once started, the indicator stays Active until at
// least DefaultMinLoading has elapsed, even when Stop is called sooner, so a
// fast refresh still visibly flashes. The tick chain keeps running until the
// minimum has elapsed AND the work is done, then ends by itself.
type Loading struct {
	th      Theme
	sp      spinner.Model
	on      bool // indicator visible (working, or lingering for the minimum)
	working bool // the work has not finished yet
	since   time.Time
	min     time.Duration
	now     func() time.Time
}

// DefaultMinLoading is the default minimum visible time of the indicator.
const DefaultMinLoading = 500 * time.Millisecond

// NewLoading returns an idle Loading styled by th.
func NewLoading(th Theme) Loading {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = th.MutedTxt
	return Loading{th: th, sp: sp, min: DefaultMinLoading, now: time.Now}
}

// SetMin sets the minimum visible time (0 disables it).
func (l *Loading) SetMin(d time.Duration) { l.min = max(d, 0) }

// SetNow injects the clock used for the minimum visible time (for tests).
func (l *Loading) SetNow(now func() time.Time) {
	if now != nil {
		l.now = now
	}
}

func (l *Loading) elapsed() bool { return l.now().Sub(l.since) >= l.min }

// Active reports whether the indicator is visible: loading is in progress, or
// finished sooner than the minimum visible time.
func (l *Loading) Active() bool { return l.on }

// Start marks loading active and returns the first tick command. It returns
// nil when already active (the running tick chain continues).
func (l *Loading) Start() tea.Cmd {
	l.working = true
	if l.on {
		return nil
	}
	l.on, l.since = true, l.now()
	// The spinner's own first tick carries tag 0, which it accepts any number
	// of times, so a replayed one would start a second chain. Advance the tag
	// first: the tick we hand out is tagged, and a replay is rejected.
	var c tea.Cmd
	l.sp, c = l.sp.Update(l.sp.Tick())
	return c
}

// Stop marks the work finished. The indicator stays Active until the minimum
// visible time has elapsed; the next tick after that ends the chain.
func (l *Loading) Stop() {
	l.working = false
	if l.on && l.elapsed() {
		l.on = false
	}
}

// StopNow marks the work finished and hides the indicator immediately,
// skipping the minimum visible time. Use it when a blocking Modal was shown
// (there is nothing to flash behind it) or when the screen is left.
func (l *Loading) StopNow() { l.working, l.on = false, false }

// Update handles a spinner.TickMsg and returns the next tick, or nil when the
// tick is stale or loading is not active. Other messages are ignored.
func (l *Loading) Update(msg tea.Msg) tea.Cmd {
	tm, ok := msg.(spinner.TickMsg)
	if !ok || !l.on {
		return nil
	}
	if !l.working && l.elapsed() {
		l.on = false
		return nil
	}
	var c tea.Cmd
	l.sp, c = l.sp.Update(tm)
	return c
}

// View renders the spinner followed by text on one line, for headers.
func (l *Loading) View(text string) string { return l.sp.View() + " " + text }

// Modal renders the spinner and text in the theme's Box, centered in a w x h
// area. With a non-positive size the bare box is returned.
func (l *Loading) Modal(text string, w, h int) string {
	box := l.th.Box.Render(l.View(text))
	if w <= 0 || h <= 0 {
		return box
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}
