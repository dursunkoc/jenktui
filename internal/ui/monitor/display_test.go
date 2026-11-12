package monitor

import (
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
