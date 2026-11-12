package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func TestTestReport(t *testing.T) {
	ctx := context.Background()
	b := func(n int) jenkinstest.Build { return jenkinstest.Build{Number: n, Result: "UNSTABLE"} }

	t.Run("failures", func(t *testing.T) {
		s := jenkinstest.New(t)
		s.Job("api/x").LastBuild(b(3)).TestReport(jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{
			{Name: "S1", Cases: []jenkinstest.TestCase{
				{ClassName: "a.A", Name: "ok", Status: jenkinstest.CasePassed},
				{ClassName: "a.A", Name: "boom", Status: jenkinstest.CaseFailed, ErrorDetails: "bad", ErrorStackTrace: "l1\nl2"},
				{ClassName: "a.B", Name: "fixed", Status: jenkinstest.CaseFixed},
				{ClassName: "a.B", Name: "skip", Status: jenkinstest.CaseSkipped},
			}},
			{Name: "S2", Cases: []jenkinstest.TestCase{
				{ClassName: "b.C", Name: "reg", Status: jenkinstest.CaseRegression, ErrorDetails: "worse"},
			}},
		}})
		r, err := newTestClient(t, s).TestReport(ctx, "api/x", 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.FailCount != 2 || r.PassCount != 2 || r.SkipCount != 1 || r.Truncated || len(r.Failed) != 2 {
			t.Fatalf("%+v", r)
		}
		f := r.Failed[0]
		if f.Suite != "S1" || f.ClassName != "a.A" || f.Name != "boom" || f.ErrorDetails != "bad" || f.StackTrace != "l1\nl2" {
			t.Fatalf("%+v", f)
		}
		if r.Failed[1].Name != "reg" || r.Failed[1].StackTrace != "" {
			t.Fatalf("%+v", r.Failed[1])
		}
	})

	t.Run("all passed", func(t *testing.T) {
		s := jenkinstest.New(t)
		s.Job("x").LastBuild(b(1)).TestReport(jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{
			{Name: "S", Cases: []jenkinstest.TestCase{{Name: "a", Status: jenkinstest.CasePassed}, {Name: "b", Status: jenkinstest.CasePassed}}},
		}})
		r, err := newTestClient(t, s).TestReport(ctx, "x", 1)
		if err != nil || r.FailCount != 0 || r.PassCount != 2 || len(r.Failed) != 0 {
			t.Fatalf("%+v %v", r, err)
		}
	})

	t.Run("skipped", func(t *testing.T) {
		s := jenkinstest.New(t)
		s.Job("x").LastBuild(b(1)).TestReport(jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{
			{Name: "S", Cases: []jenkinstest.TestCase{{Name: "a", Status: jenkinstest.CaseSkipped}}},
		}})
		r, err := newTestClient(t, s).TestReport(ctx, "x", 0)
		if err != nil || r.SkipCount != 1 || r.PassCount != 0 || len(r.Failed) != 0 {
			t.Fatalf("%+v %v", r, err)
		}
	})

	t.Run("no report", func(t *testing.T) {
		s := jenkinstest.New(t)
		s.Job("x").LastBuild(b(1))
		_, err := newTestClient(t, s).TestReport(ctx, "x", 0)
		if !errors.Is(err, ErrNoTestReport) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("truncation", func(t *testing.T) {
		var cases []jenkinstest.TestCase
		for i := 0; i < MaxFailedCases+5; i++ {
			cases = append(cases, jenkinstest.TestCase{ClassName: "c", Name: fmt.Sprintf("t%d", i), Status: jenkinstest.CaseFailed})
		}
		long := make([]string, MaxStackTraceLines+50)
		for i := range long {
			long[i] = fmt.Sprintf("line %d", i)
		}
		cases[0].ErrorStackTrace = strings.Join(long, "\n") + "\n"
		s := jenkinstest.New(t)
		s.Job("x").LastBuild(b(1)).TestReport(jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{{Name: "S", Cases: cases}}})
		r, err := newTestClient(t, s).TestReport(ctx, "x", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Failed) != MaxFailedCases || !r.Truncated || r.FailCount != MaxFailedCases+5 {
			t.Fatalf("failed=%d truncated=%v fail=%d", len(r.Failed), r.Truncated, r.FailCount)
		}
		lines := strings.Split(r.Failed[0].StackTrace, "\n")
		if len(lines) != MaxStackTraceLines || lines[len(lines)-1] != fmt.Sprintf("line %d", MaxStackTraceLines-1) {
			t.Fatalf("stack lines %d, last %q", len(lines), lines[len(lines)-1])
		}
	})

	t.Run("null error fields", func(t *testing.T) {
		s := jenkinstest.New(t)
		s.Job("x").LastBuild(b(1)).TestReport(jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{
			{Name: "S", Cases: []jenkinstest.TestCase{{ClassName: "c", Name: "n", Status: jenkinstest.CaseFailed}}},
		}})
		r, err := newTestClient(t, s).TestReport(ctx, "x", 0)
		if err != nil || len(r.Failed) != 1 || r.Failed[0].ErrorDetails != "" || r.Failed[0].StackTrace != "" {
			t.Fatalf("%+v %v", r, err)
		}
	})

	t.Run("specific build and last build", func(t *testing.T) {
		s := jenkinstest.New(t)
		s.Job("x").Builds(b(1), b(2)).
			TestReportFor(1, jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{{Name: "S", Cases: []jenkinstest.TestCase{{Name: "old", Status: jenkinstest.CaseFailed}}}}}).
			TestReportFor(2, jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{{Name: "S", Cases: []jenkinstest.TestCase{{Name: "new", Status: jenkinstest.CasePassed}}}}})
		c := newTestClient(t, s)
		r1, err := c.TestReport(ctx, "x", 1)
		if err != nil || len(r1.Failed) != 1 || r1.Failed[0].Name != "old" {
			t.Fatalf("%+v %v", r1, err)
		}
		r2, err := c.TestReport(ctx, "x", 0)
		if err != nil || r2.PassCount != 1 || len(r2.Failed) != 0 {
			t.Fatalf("%+v %v", r2, err)
		}
	})
}

func rawClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Shape of matrix and Maven-module reports: no top-level suites or passCount.
// Check on a real Jenkins (O3).
func TestTestReportAggregated(t *testing.T) {
	var gotTree string
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotTree = r.URL.Query().Get("tree")
		fmt.Fprint(w, `{"failCount":2,"skipCount":1,"totalCount":10,"childReports":[
		 {"child":{"name":"linux","url":"u"},"result":{"suites":[{"name":"S","cases":[
		   {"className":"a.A","name":"x","status":"FAILED","errorDetails":"d","errorStackTrace":"st"},
		   {"className":"a.A","name":"y","status":"PASSED","errorDetails":null,"errorStackTrace":null}]}]}},
		 {"child":{"name":"win","url":"u"},"result":{"suites":[{"name":"S","cases":[
		   {"className":"b.B","name":"z","status":"REGRESSION","errorDetails":null,"errorStackTrace":null}]}]}}]}`)
	})
	r, err := c.TestReport(context.Background(), "x", 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.FailCount != 2 || r.PassCount != 7 || r.SkipCount != 1 || r.Truncated || len(r.Failed) != 2 {
		t.Fatalf("%+v", r)
	}
	if r.Failed[0].Child != "linux" || r.Failed[0].Name != "x" || r.Failed[1].Child != "win" || r.Failed[1].Name != "z" {
		t.Fatalf("%+v", r.Failed)
	}
	if !strings.Contains(gotTree, "childReports[child[name,url],result[suites[") || !strings.Contains(gotTree, "totalCount") {
		t.Fatalf("tree %q", gotTree)
	}
}

func TestTestReportBackstopTruncated(t *testing.T) {
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"failCount":3,"passCount":1,"skipCount":0,"suites":[]}`)
	})
	r, err := c.TestReport(context.Background(), "x", 0)
	if err != nil || !r.Truncated || r.FailCount != 3 || len(r.Failed) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestTestReportHTTPErrors(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusForbidden} {
		c := rawClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
		if _, err := c.TestReport(context.Background(), "x", 0); err == nil || errors.Is(err, ErrNoTestReport) {
			t.Fatalf("%d: %v", code, err)
		}
		if _, err := c.TestCounts(context.Background(), "x", 0); err == nil || errors.Is(err, ErrNoTestReport) {
			t.Fatalf("%d counts: %v", code, err)
		}
	}
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	if _, err := c.TestReport(context.Background(), "x", 0); !errors.Is(err, ErrAuth) {
		t.Fatalf("403: %v", err)
	}
}

func TestTestCounts(t *testing.T) {
	var gotTree string
	c := rawClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotTree = r.URL.Query().Get("tree")
		if strings.Contains(r.URL.Path, "/9/") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"failCount":2,"skipCount":1,"totalCount":10}`)
	})
	r, err := c.TestCounts(context.Background(), "x", 4)
	if err != nil || r.FailCount != 2 || r.PassCount != 7 || r.SkipCount != 1 || len(r.Failed) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if gotTree != "failCount,passCount,skipCount,totalCount" {
		t.Fatalf("tree %q", gotTree)
	}
	if _, err := c.TestCounts(context.Background(), "x", 9); !errors.Is(err, ErrNoTestReport) {
		t.Fatalf("got %v", err)
	}
	// against the fake
	s := jenkinstest.New(t)
	s.Job("x").LastBuild(jenkinstest.Build{Number: 1}).TestReport(jenkinstest.TestReport{FailCount: 1, PassCount: 5, SkipCount: 2})
	fr, err := newTestClient(t, s).TestCounts(context.Background(), "x", 0)
	if err != nil || fr.FailCount != 1 || fr.PassCount != 5 || fr.SkipCount != 2 {
		t.Fatalf("%+v %v", fr, err)
	}
}
