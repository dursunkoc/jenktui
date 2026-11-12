package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/ui/core"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func lastLine(v string) string {
	rows := strings.Split(ansi.Strip(v), "\n")
	return strings.TrimSpace(rows[len(rows)-1])
}

func TestFlashShownThenExpires(t *testing.T) {
	m, clk, _ := newColl(&fakeClient{}, []string{"a"}, nil)
	m.runs[0].phase, m.runs[0].result = phFinished, "FAILURE"
	if m.noticeShown() {
		t.Fatal("no notice yet")
	}
	var asked []time.Duration
	m.after = func(d time.Duration, msg tea.Msg) tea.Cmd {
		asked = append(asked, d)
		if _, ok := msg.(noticeExpiredMsg); !ok {
			t.Errorf("expiry message is %T", msg)
		}
		return func() tea.Msg { return msg }
	}
	if cmd := m.flash("copied"); cmd == nil || len(asked) != 1 || asked[0] != noticeFor {
		t.Fatalf("flash must schedule its expiry after %v: %v", noticeFor, asked)
	}
	if got := m.Subtitle(); got != "copied" {
		t.Fatalf("at t: %q", got)
	}
	clk.t = clk.t.Add(3900 * time.Millisecond)
	if got := m.Subtitle(); got != "copied" {
		t.Fatalf("at t+3.9s: %q", got)
	}
	clk.t = clk.t.Add(100 * time.Millisecond)
	if got := m.Subtitle(); got == "copied" || got == "" {
		t.Fatalf("at t+4s the header returns, got %q", got)
	}
}

func TestNoticeExpiredMsgIsAnApplier(t *testing.T) {
	m, _, _ := newColl(&fakeClient{}, []string{"a"}, nil)
	if _, cmd := m.Update(noticeExpiredMsg{}); cmd != nil {
		t.Fatal("expiry only re-renders")
	}
}

type dummyApply struct{ hit *bool }

func (d dummyApply) apply(m *model) tea.Cmd { *d.hit = true; m.name = "applied"; return nil }

func TestApplierSeam(t *testing.T) {
	m, _, _ := newColl(&fakeClient{}, []string{"a"}, nil)
	hit := false
	m.Update(dummyApply{&hit})
	if !hit || m.name != "applied" {
		t.Fatal("applier not applied")
	}
}

type ranMsg struct{}

func asked(t *testing.T) *model {
	t.Helper()
	m, _, _ := newColl(&fakeClient{}, []string{"a"}, nil)
	m.runs[0].phase = phFinished
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m.ask("Abort a #1?", func() tea.Msg { return ranMsg{} })
	return m
}

func TestConfirmYesRunsCommand(t *testing.T) {
	m := asked(t)
	_, cmd := m.Update(runeKey('y'))
	if m.confirm != nil || cmd == nil {
		t.Fatal("y clears and returns the command")
	}
	if _, ok := cmd().(ranMsg); !ok {
		t.Fatal("wrong command")
	}
}

func TestConfirmNoAndEscCancel(t *testing.T) {
	for _, k := range []tea.KeyMsg{runeKey('n'), {Type: tea.KeyEsc}} {
		m := asked(t)
		if _, cmd := m.Update(k); cmd != nil || m.confirm != nil {
			t.Fatalf("%v should clear without a command", k)
		}
	}
}

func TestConfirmSwallowsOtherKeys(t *testing.T) {
	m := asked(t)
	gen := m.gen
	for _, k := range []tea.KeyMsg{runeKey('q'), runeKey('R'), {Type: tea.KeyEnter}} {
		if _, cmd := m.Update(k); cmd != nil || m.confirm == nil {
			t.Fatalf("%v must be swallowed", k)
		}
	}
	if m.gen != gen || m.details {
		t.Fatal("swallowed keys must not start a cycle or open details")
	}
}

func TestConfirmHelpAndDrawing(t *testing.T) {
	m := asked(t)
	ks := m.Keys()
	if len(ks) != 2 || !key.Matches(runeKey('y'), ks[0]) || !key.Matches(tea.KeyMsg{Type: tea.KeyEsc}, ks[1]) {
		t.Fatalf("keys: %v", ks)
	}
	if m.FullHelp() != nil {
		t.Fatal("no full help while asking")
	}
	if got := lastLine(m.View()); !strings.HasPrefix(got, "Abort a #1?") {
		t.Fatalf("last line %q", got)
	}
	if n := len(strings.Split(m.View(), "\n")); n != 10 {
		t.Fatalf("body is %d rows, want 10", n)
	}
}

func TestConfirmDrawnOverLogView(t *testing.T) {
	m, _ := logTail(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	pressL(m)
	m.ask("Sure?", nil)
	if got := lastLine(m.View()); !strings.HasPrefix(got, "Sure?") {
		t.Fatalf("last line %q", got)
	}
	if _, cmd := m.Update(runeKey('y')); cmd != nil || m.confirm != nil || m.log == nil {
		t.Fatal("y closes the prompt and leaves the log open")
	}
}

func TestDetailSectionShowsInDetails(t *testing.T) {
	m, _ := logTail(t)
	m.addDetailSection(func(st runState) []string { return []string{"SECTION " + st.run.Job} })
	m.addDetailSection(func(runState) []string { return nil })
	if v := ansi.Strip(m.View()); !strings.Contains(v, "SECTION b") {
		t.Fatalf("section missing:\n%s", v)
	}
}

func TestManualFlagSetByRNotByTick(t *testing.T) {
	m, _, _ := newColl(&fakeClient{}, []string{"a"}, nil)
	m.Update(tickMsg{gen: m.gen})
	if m.manual {
		t.Fatal("automatic tick is not manual")
	}
	m.runs[0].phase = phFinished
	m.Update(keyR())
	if !m.manual {
		t.Fatal("R is manual")
	}
	m.Update(tickMsg{gen: m.gen})
	if m.manual {
		t.Fatal("next tick clears it")
	}
}

type dummyOverlay struct{ keysSeen []string }

func (o *dummyOverlay) view(w, h int) string { return "OVERLAY" }
func (o *dummyOverlay) key(msg tea.KeyMsg) (tea.Cmd, bool) {
	o.keysSeen = append(o.keysSeen, msg.String())
	return nil, msg.Type == tea.KeyEsc
}
func (o *dummyOverlay) keys() []key.Binding { return []key.Binding{core.KeyBack} }

func TestOverlaySeam(t *testing.T) {
	m, _, _ := newColl(&fakeClient{}, []string{"a"}, nil)
	m.runs[0].phase = phFinished
	o := &dummyOverlay{}
	m.overlay = o
	if m.View() != "OVERLAY" || len(m.Keys()) != 1 || m.FullHelp() != nil {
		t.Fatal("overlay draws, lists its keys, hides full help")
	}
	if _, cmd := m.Update(runeKey('q')); cmd != nil || m.overlay == nil {
		t.Fatal("q goes to the overlay, not Quit")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != nil || strings.Join(o.keysSeen, ",") != "q,esc" {
		t.Fatalf("done closes it: %v", o.keysSeen)
	}
	// confirm sits above the overlay
	m.overlay = o
	m.ask("ok?", nil)
	m.Update(runeKey('n'))
	if m.confirm != nil || m.overlay == nil || len(o.keysSeen) != 2 {
		t.Fatal("confirm takes keys before the overlay")
	}
}

func TestLogSwallowsUnhandledKeys(t *testing.T) {
	m, _ := logTail(t)
	pressL(m)
	if m.log == nil || !m.details {
		t.Fatal("setup")
	}
	for _, k := range []tea.KeyMsg{{Type: tea.KeyEnter}, runeKey('l'), runeKey('L'), runeKey('R'), runeKey('s')} {
		if _, cmd := m.Update(k); cmd != nil || m.log == nil || !m.details {
			t.Fatalf("%v leaked out of the log (log=%v details=%v)", k, m.log != nil, m.details)
		}
	}
	if _, cmd := m.Update(runeKey('q')); cmd == nil {
		t.Fatal("q still quits")
	} else if _, ok := cmd().(core.Quit); !ok {
		t.Fatal("q should return Quit")
	}
}

type hookMsg struct{ s string }

func TestResultAndDetailsHooksCmdsComeBack(t *testing.T) {
	m, clk, rec := newColl(&fakeClient{}, []string{"a"}, nil)
	var gotPrev runState
	m.hooks.result = func(i int, prev runState) tea.Cmd {
		gotPrev = prev
		return func() tea.Msg { return hookMsg{"result"} }
	}
	m.hooks.detailsOpen = func(i int) tea.Cmd {
		return func() tea.Msg { return hookMsg{"details"} }
	}
	m.startCycle(false)
	clk.t = clk.t.Add(time.Second)
	_, cmd := m.Update(resultMsg{gen: m.gen, st: runState{run: Run{Job: "a"}, rev: m.runs[0].rev, phase: phFinished, result: "FAILURE", num: 3}})
	if gotPrev.phase != phUnpolled {
		t.Fatalf("prev should be the row before the result: %+v", gotPrev)
	}
	if cmd == nil || len(*rec) != 1 {
		t.Fatalf("cmd=%v scheduled=%d: the hook cmd and the next tick are both kept", cmd != nil, len(*rec))
	}
	found := false
	for _, c := range cmd().(tea.BatchMsg) {
		if c != nil {
			if h, ok := c().(hookMsg); ok && h.s == "result" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("result hook cmd missing from the batch")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	found = false
	for _, c := range cmd().(tea.BatchMsg) {
		if c != nil {
			if h, ok := c().(hookMsg); ok && h.s == "details" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("details hook cmd missing")
	}
}
