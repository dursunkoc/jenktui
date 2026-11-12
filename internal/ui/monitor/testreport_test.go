package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

// repClient adds the optional reporter to the fake client.
type repClient struct {
	*fakeClient
	counts, full jenkins.TestReport
	err          error
	nCounts      int
	nFull        int
}

func (c *repClient) TestCounts(context.Context, string, int) (jenkins.TestReport, error) {
	c.nCounts++
	return c.counts, c.err
}
func (c *repClient) TestReport(context.Context, string, int) (jenkins.TestReport, error) {
	c.nFull++
	return c.full, c.err
}

func reportModel(c *repClient) *model {
	c.fakeClient = failClient()
	m := NewCollection(Deps{Client: c, Now: (&clock{t: t0}).now}, "N", []string{"a", "b"}, nil).(*model)
	wire(m)
	m.w, m.h = 100, 20
	cycle(m)
	return m
}

// run executes cmd and applies appliers it yields (batches flattened).
func run(m *model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			run(m, c)
		}
	case applier:
		_, next := m.Update(msg)
		run(m, next)
	case stagesMsg:
		m.Update(msg)
	}
}

func openDetails(m *model, row int) {
	m.cursor = row
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	run(m, cmd)
}

func screen(m *model) string { return ansi.Strip(m.View()) }

func TestCountsShownInDetailsOnce(t *testing.T) {
	c := &repClient{counts: jenkins.TestReport{FailCount: 3, PassCount: 812, SkipCount: 4}}
	m := reportModel(c)
	openDetails(m, 0) // a: SUCCESS, finished
	if !strings.Contains(screen(m), "3 failed · 812 passed · 4 skipped") {
		t.Fatalf("counts missing:\n%s", screen(m))
	}
	m.details = false
	openDetails(m, 0)
	if c.nCounts != 1 {
		t.Fatalf("counts fetched %d times, want once", c.nCounts)
	}
}

func TestAllPassedShowsCounts(t *testing.T) {
	c := &repClient{counts: jenkins.TestReport{PassCount: 10}}
	m := reportModel(c)
	openDetails(m, 0)
	if !strings.Contains(screen(m), "0 failed · 10 passed · 0 skipped") {
		t.Fatalf("got:\n%s", screen(m))
	}
}

func TestNoTestReportShowsNothing(t *testing.T) {
	c := &repClient{err: jenkins.ErrNoTestReport}
	m := reportModel(c)
	openDetails(m, 0)
	if strings.Contains(screen(m), "passed") {
		t.Fatal("nothing expected")
	}
	run(m, pressKey(m, 't'))
	if m.overlay != nil || !strings.Contains(m.notice, "no test report") {
		t.Fatalf("t on 404: overlay=%v notice=%q", m.overlay, m.notice)
	}
}

func TestTransientCountsErrorRetriesOnNextOpen(t *testing.T) {
	c := &repClient{err: errors.New("boom")}
	m := reportModel(c)
	openDetails(m, 0)
	m.details = false
	c.err, c.counts = nil, jenkins.TestReport{FailCount: 1}
	openDetails(m, 0)
	if c.nCounts != 2 || !strings.Contains(screen(m), "1 failed") {
		t.Fatalf("calls=%d\n%s", c.nCounts, screen(m))
	}
}

func TestRunningBuildFetchesNoCounts(t *testing.T) {
	c := &repClient{}
	c.fakeClient = failClient()
	c.builds["a"][0] = jenkins.Build{Number: 4, Building: true}
	m := NewCollection(Deps{Client: c, Now: (&clock{t: t0}).now}, "N", []string{"a"}, nil).(*model)
	wire(m)
	cycle(m)
	openDetails(m, 0)
	if c.nCounts != 0 {
		t.Fatal("no counts for a running build")
	}
}

func TestTOpensOverlayWithFailedTestsAndTrace(t *testing.T) {
	c := &repClient{
		counts: jenkins.TestReport{FailCount: 2, PassCount: 5},
		full: jenkins.TestReport{FailCount: 3, PassCount: 5, Truncated: true, Failed: []jenkins.FailedCase{
			{ClassName: "pkg.A", Name: "testOne", ErrorDetails: "expected 1", StackTrace: "at A.java:10\nat B.java:20"},
			{Child: "linux", ClassName: "pkg.B", Name: "testTwo", StackTrace: "at C.java:1"},
		}},
	}
	m := reportModel(c)
	openDetails(m, 1) // b: FAILURE
	run(m, pressKey(m, 't'))
	if m.overlay == nil {
		t.Fatal("overlay expected")
	}
	v := screen(m)
	for _, want := range []string{"pkg.A.testOne", "[linux] pkg.B.testTwo", "more failures than listed"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	// keys other than overlay keys do nothing to the monitor
	pressKey(m, 'L')
	if m.log != nil {
		t.Fatal("overlay takes every key")
	}
	pressKey(m, 'j')
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	v = screen(m)
	if !strings.Contains(v, "expected 1") || !strings.Contains(v, "at B.java:20") {
		t.Fatalf("trace missing:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back to the list
	if m.overlay == nil || strings.Contains(screen(m), "at B.java:20") {
		t.Fatal("esc leaves the trace, not the overlay")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.overlay != nil || !m.details {
		t.Fatal("second esc closes the overlay, back in details")
	}
}

func TestReportResultDroppedWhenStale(t *testing.T) {
	c := &repClient{full: jenkins.TestReport{FailCount: 1, Failed: []jenkins.FailedCase{{Name: "x"}}}}
	m := reportModel(c)
	openDetails(m, 1)
	cmd := pressKey(m, 't')
	m.epoch++ // the monitor was left
	run(m, cmd)
	if m.overlay != nil {
		t.Fatal("stale report must be dropped")
	}
}

func TestTHiddenWithoutReporterAndOutsideDetails(t *testing.T) {
	m := failModel(failClient()) // fakeClient is not a reporter
	m.cursor = 1
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd := pressKey(m, 't'); cmd != nil {
		t.Fatal("t needs a reporter")
	}
	for _, k := range helpKeys(m) {
		if k == "t test report" {
			t.Fatal("t shown without a reporter")
		}
	}
	c := &repClient{}
	m2 := reportModel(c)
	if cmd := pressKey(m2, 't'); cmd != nil {
		t.Fatal("t only in details")
	}
}
