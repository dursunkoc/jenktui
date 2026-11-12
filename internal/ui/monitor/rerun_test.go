package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type rerunClient struct {
	*fakeClient
	bc  jenkins.BuildContext
	err error
	got []int // build numbers asked for
}

func (c *rerunClient) BuildContext(_ context.Context, _ string, n int) (jenkins.BuildContext, error) {
	c.got = append(c.got, n)
	return c.bc, c.err
}

type pushedForm struct{ core.Screen }

// rerunModel: row "api/deploy" with finished build 57.
func rerunModel(c *rerunClient, withRerun bool, calls *[]map[string]string) *model {
	c.fakeClient = &fakeClient{builds: map[string]map[int]jenkins.Build{
		"api/deploy": {0: {Number: 57, Result: "FAILURE"}, 57: {Number: 57, Result: "FAILURE"}},
	}}
	d := Deps{Client: c, Now: (&clock{t: t0}).now}
	if withRerun {
		d.Rerun = func(job string, p map[string]string) core.Screen {
			*calls = append(*calls, p)
			return pushedForm{}
		}
	}
	m := NewCollection(d, "Nightly", []string{"api/deploy"}, nil).(*model)
	wire(m)
	cycle(m)
	return m
}

// deliverAll runs cmd, applies every message to m, and returns the navigation
// messages. Timers are no-ops (wire), so every command returns at once.
func deliverAll(m *model, cmd tea.Cmd) (nav []tea.Msg) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			nav = append(nav, deliverAll(m, c)...)
		}
	case core.Push, core.Pop, core.Replace:
		nav = append(nav, msg)
	default:
		_, c := m.Update(msg)
		nav = append(nav, deliverAll(m, c)...)
	}
	return
}

func TestRerunPushesTheFormPrefilledWithoutPasswords(t *testing.T) {
	c := &rerunClient{bc: jenkins.BuildContext{Params: []jenkins.BuildParam{
		{Name: "ENV", Value: "prod", HasValue: true}, {Name: "DRY", Value: "true", HasValue: true}, {Name: "KEY", Password: true},
		{Name: "UPLOAD"}, {Name: "EMPTY", HasValue: true}, // a file parameter has no value; a real empty string is kept
	}}}
	var calls []map[string]string
	m := rerunModel(c, true, &calls)
	cmd := pressKey(m, 'r')
	if !strings.Contains(m.Subtitle(), "loading parameters") {
		t.Fatalf("subtitle %q", m.Subtitle())
	}
	nav := deliverAll(m, cmd)
	if len(c.got) != 1 || c.got[0] != 57 {
		t.Fatalf("BuildContext asked for %v", c.got)
	}
	if len(calls) != 1 || calls[0]["ENV"] != "prod" || calls[0]["DRY"] != "true" {
		t.Fatalf("prefill = %v", calls)
	}
	if _, has := calls[0]["KEY"]; has {
		t.Fatalf("password carried over: %v", calls[0])
	}
	if _, has := calls[0]["UPLOAD"]; has {
		t.Fatalf("a parameter without a value was prefilled: %v", calls[0])
	}
	if v, has := calls[0]["EMPTY"]; !has || v != "" {
		t.Fatalf("an empty string value must be kept: %v", calls[0])
	}
	if len(nav) != 1 {
		t.Fatalf("nav = %v", nav)
	}
	if p, ok := nav[0].(core.Push); !ok || p.Screen != (pushedForm{}) {
		t.Fatalf("nav = %#v", nav[0])
	}
}

func TestRerunHiddenWithoutDepsOrBuild(t *testing.T) {
	c := &rerunClient{}
	var calls []map[string]string
	m := rerunModel(c, false, &calls)
	if m.rerunnable() || pressKey(m, 'r') != nil {
		t.Fatal("r works without Deps.Rerun")
	}
	for _, k := range m.Keys() {
		if k.Help().Key == "r" {
			t.Fatal("r shown without Deps.Rerun")
		}
	}
	m = rerunModel(c, true, &calls)
	found := false
	for _, k := range m.Keys() {
		found = found || k.Help().Key == "r"
	}
	if !found {
		t.Fatal("r not shown")
	}
	// a row with no build yet
	m.runs[0].num, m.runs[0].phase = 0, phNotBuilt
	if m.rerunnable() || pressKey(m, 'r') != nil {
		t.Fatal("r works on a row with no build")
	}
}

func TestRerunWorksInDetailsToo(t *testing.T) {
	c := &rerunClient{bc: jenkins.BuildContext{Params: []jenkins.BuildParam{{Name: "A", Value: "1", HasValue: true}}}}
	var calls []map[string]string
	m := rerunModel(c, true, &calls)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nav := deliverAll(m, pressKey(m, 'r'))
	if len(nav) != 1 || len(calls) != 1 || calls[0]["A"] != "1" {
		t.Fatalf("nav=%v calls=%v", nav, calls)
	}
}

func TestRerunErrorIsFlashedAndNothingPushed(t *testing.T) {
	c := &rerunClient{err: errors.New("boom")}
	var calls []map[string]string
	m := rerunModel(c, true, &calls)
	nav := deliverAll(m, pressKey(m, 'r'))
	if len(nav) != 0 || len(calls) != 0 || !strings.Contains(m.Subtitle(), "cannot rerun: boom") {
		t.Fatalf("nav=%v calls=%v subtitle=%q", nav, calls, m.Subtitle())
	}
}

func TestRerunReplyAfterLeavingIsDropped(t *testing.T) {
	c := &rerunClient{bc: jenkins.BuildContext{}}
	var calls []map[string]string
	m := rerunModel(c, true, &calls)
	cmd := pressKey(m, 'r')
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // leave: epoch moves on
	nav := deliverAll(m, cmd)
	if len(nav) != 0 || len(calls) != 0 {
		t.Fatalf("late reply pushed a form: nav=%v calls=%v", nav, calls)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestResumeRestartsPollingAfterTheFormIsPopped(t *testing.T) {
	c := &rerunClient{bc: jenkins.BuildContext{}}
	var calls []map[string]string
	m := rerunModel(c, true, &calls)
	deliverAll(m, pressKey(m, 'r'))
	gen := m.gen
	msg := m.Resume()()
	if tk, ok := msg.(tickMsg); !ok || tk.gen != gen {
		t.Fatalf("Resume gave %#v, want a tick for gen %d", msg, gen)
	}
}

func TestRerunStopsTheSpinnerAndDropsAPollFromBeforeTheForm(t *testing.T) {
	c := &rerunClient{bc: jenkins.BuildContext{}}
	var calls []map[string]string
	m := rerunModel(c, true, &calls)
	// A poll starts, then r pushes the form; the poll's result arrives later.
	_, pollCmd := m.Update(tickMsg{gen: m.gen})
	staleGen := m.gen
	deliverAll(m, pressKey(m, 'r'))
	if m.gen == staleGen || m.load.Active() {
		t.Fatalf("gen %d (was %d), spinner active %v", m.gen, staleGen, m.load.Active())
	}
	before := m.runs[0]
	c.fakeClient.builds["api/deploy"] = map[int]jenkins.Build{0: {Number: 99, Result: "SUCCESS"}}
	deliverAll(m, pollCmd)
	if m.runs[0].num != before.num {
		t.Fatalf("a poll result from before the form was applied: #%d", m.runs[0].num)
	}
}
