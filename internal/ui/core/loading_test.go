package core

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
)

func TestLoadingTicksOnlyWhileActive(t *testing.T) {
	l := NewLoading(NewTheme())
	l.SetMin(0)
	if l.Active() {
		t.Fatal("new Loading must be idle")
	}
	first := l.Start()
	if first == nil || !l.Active() {
		t.Fatal("Start must activate and return a tick")
	}
	if l.Start() != nil {
		t.Fatal("Start while active must not start a second tick chain")
	}
	tick := first()
	if _, ok := tick.(spinner.TickMsg); !ok {
		t.Fatalf("first cmd yields %T", tick)
	}
	if l.Update(tick) == nil {
		t.Fatal("active spinner must schedule the next tick")
	}
	l.Stop()
	if l.Update(tick) != nil {
		t.Fatal("stopped spinner must not schedule ticks")
	}
}

func TestLoadingDropsForeignTicks(t *testing.T) {
	a, b := NewLoading(NewTheme()), NewLoading(NewTheme())
	a.Start()
	foreign := b.Start()()
	if a.Update(foreign) != nil {
		t.Fatal("tick of another spinner must be ignored")
	}
	if a.Update("not a tick") != nil {
		t.Fatal("non-tick message must be ignored")
	}
}

func TestLoadingViews(t *testing.T) {
	l := NewLoading(NewTheme())
	if v := l.View("refreshing…"); !strings.Contains(v, "refreshing…") {
		t.Fatalf("inline: %q", v)
	}
	m := l.Modal("Loading jobs…", 40, 9)
	if !strings.Contains(m, "Loading jobs…") || !strings.Contains(m, "╭") {
		t.Fatalf("modal: %q", m)
	}
	if rows := strings.Count(m, "\n") + 1; rows != 9 {
		t.Fatalf("modal rows = %d, want 9", rows)
	}
	if strings.Contains(l.Modal("x", 0, 0), "\n\n") {
		t.Fatal("bare box expected without size")
	}
}

func TestLoadingMinimumVisibleTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewLoading(NewTheme())
	l.SetNow(func() time.Time { return now })
	l.SetMin(500 * time.Millisecond)
	tick := l.Start()()
	l.Stop() // fast work: finished at once
	if !l.Active() || !strings.Contains(l.View("x"), "x") {
		t.Fatal("must stay visible until the minimum has elapsed")
	}
	now = now.Add(200 * time.Millisecond)
	next := l.Update(tick)
	if next == nil || !l.Active() {
		t.Fatal("tick chain must continue until the minimum elapsed")
	}
	now = now.Add(300 * time.Millisecond)
	if l.Update(next()) != nil || l.Active() {
		t.Fatal("chain must end and indicator hide once the minimum elapsed")
	}
	// Slow work: stays up while working even past the minimum, then hides at once.
	tick = l.Start()()
	now = now.Add(time.Second)
	if l.Update(tick) == nil || !l.Active() {
		t.Fatal("must keep ticking while working")
	}
	l.Stop()
	if l.Active() {
		t.Fatal("Stop after the minimum hides immediately")
	}
	// StopNow skips the minimum.
	l.Start()
	l.StopNow()
	if l.Active() {
		t.Fatal("StopNow must hide immediately")
	}
	// Restart while lingering keeps the single chain.
	l.Start()
	l.Stop()
	if l.Start() != nil || !l.Active() {
		t.Fatal("Start while lingering must not start a second chain")
	}
}
