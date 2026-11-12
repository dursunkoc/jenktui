package monitor

import (
	"time"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// MD1: the header spinner shows for the first load and for R only.
func TestSpinnerOnlyForFirstLoadAndManualRefresh(t *testing.T) {
	c := collClient()
	m, _, _ := collMonitor(c)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if strings.Contains(ansi.Strip(shown(m)), "refreshing…") {
		t.Fatal("idle monitor must show no spinner")
	}
	// Automatic tick: work is in flight but the header stays quiet.
	_, cmd := m.Update(tickMsg{gen: m.gen})
	if !m.load.Active() {
		t.Fatal("cycle should be in flight")
	}
	if m.manual || strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatalf("automatic tick must leave no spinner: %q", m.Subtitle())
	}
	exec(m, cmd)
	// R shows it.
	_, cmd = m.Update(keyR())
	if !m.manual || !strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatalf("R must show the spinner: %q", m.Subtitle())
	}
	exec(m, cmd)
	// The next automatic tick clears the manual flag and hides it again.
	m.Update(tickMsg{gen: m.gen})
	if strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatal("automatic tick after R must show no spinner")
	}
}

// The first cycle (m.modal) shows the header spinner once a row has data.
func TestSpinnerShowsDuringFirstLoad(t *testing.T) {
	c := collClient()
	m, _, _ := newColl(c, []string{"a", "c"}, nil)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	_, cmd := m.Update(tickMsg{gen: m.gen})
	var first tea.Msg
	for _, bc := range cmd().(tea.BatchMsg) {
		if r, ok := bc().(resultMsg); ok {
			first = r
			break
		}
	}
	m.Update(first)
	if !strings.Contains(ansi.Strip(m.Subtitle()), "refreshing…") {
		t.Fatalf("first load must show the spinner: %q", m.Subtitle())
	}
}

func TestFmtAgeBoundaries(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:                "just now",
		time.Minute:                     "1m ago",
		59*time.Minute + 59*time.Second: "59m ago",
		time.Hour:                       "1h ago",
		23 * time.Hour:                  "23h ago",
		24 * time.Hour:                  "yesterday",
		47 * time.Hour:                  "yesterday",
		48 * time.Hour:                  "2d ago",
		7 * 24 * time.Hour:              "7d ago",
	} {
		if got := fmtAge(d); got != want {
			t.Errorf("fmtAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestProgressAndAgeInRows(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m, _ := newModel(&fakeClient{}, Run{Job: "a"})
	m.deps.Now = func() time.Time { return now }
	row := func(st runState) string {
		m.runs[0] = st
		return ansi.Strip(m.block(0, 0)[0])
	}
	run := func(started, est time.Duration) runState {
		return runState{run: Run{Job: "a"}, phase: phRunning, num: 1,
			build: jenkins.Build{Number: 1, Building: true, Timestamp: now.Add(-started), EstimatedDuration: est}}
	}
	if r := row(run(96*time.Second, 4*time.Minute)); !strings.Contains(r, "1m36s / ~4m") {
		t.Errorf("known estimate: %q", r)
	}
	if r := row(run(96*time.Second, 0)); !strings.Contains(r, "1m36s") || strings.Contains(r, "~") {
		t.Errorf("unknown estimate: %q", r)
	}
	if r := row(run(252*time.Second, 4*time.Minute)); !strings.Contains(r, "4m12s (over)") || strings.Contains(r, "~") {
		t.Errorf("overrun: %q", r)
	}
	fin := func(ago time.Duration) runState {
		return runState{run: Run{Job: "a"}, phase: phFinished, num: 1, result: "SUCCESS",
			build: jenkins.Build{Number: 1, Result: "SUCCESS", Duration: time.Minute, Timestamp: now.Add(-ago)}}
	}
	if r := row(fin(2 * time.Minute)); !strings.Contains(r, "2m ago") {
		t.Errorf("finished age: %q", r)
	}
	if r := row(fin(3 * time.Hour)); !strings.Contains(r, "3h ago") {
		t.Errorf("finished hours: %q", r)
	}
	if r := row(fin(30 * time.Hour)); !strings.Contains(r, "yesterday") {
		t.Errorf("finished day: %q", r)
	}
}
