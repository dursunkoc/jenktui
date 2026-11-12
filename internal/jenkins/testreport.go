package jenkins

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ErrNoTestReport means the build has no test report (Jenkins answers 404).
var ErrNoTestReport = errors.New("no test report for this build")

// Caps on what TestReport returns, so a huge report cannot flood the UI.
const (
	MaxFailedCases     = 200
	MaxStackTraceLines = 200
)

// FailedCase is one failed test (status FAILED or REGRESSION).
type FailedCase struct {
	Suite        string
	Child        string // matrix cell or module name for aggregated reports; "" otherwise
	ClassName    string
	Name         string
	ErrorDetails string
	StackTrace   string // at most MaxStackTraceLines lines
}

// TestReport is a build's test counts plus its failed cases.
type TestReport struct {
	FailCount int // the true count from Jenkins, even when Failed is capped
	PassCount int
	SkipCount int
	Failed    []FailedCase // at most MaxFailedCases
	Truncated bool         // more failed cases existed than Failed holds
}

type reportCase struct {
	ClassName       string  `json:"className"`
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	ErrorDetails    *string `json:"errorDetails"`
	ErrorStackTrace *string `json:"errorStackTrace"`
}

type reportSuite struct {
	Name  string       `json:"name"`
	Cases []reportCase `json:"cases"`
}

type reportJSON struct {
	FailCount    int           `json:"failCount"`
	PassCount    int           `json:"passCount"`
	SkipCount    int           `json:"skipCount"`
	TotalCount   int           `json:"totalCount"`
	Suites       []reportSuite `json:"suites"`
	ChildReports []struct {
		Child struct {
			Name string `json:"name"`
		} `json:"child"`
		Result struct {
			Suites []reportSuite `json:"suites"`
		} `json:"result"`
	} `json:"childReports"`
}

func (c *Client) fetchReport(ctx context.Context, fullName string, number int, tree string) (reportJSON, error) {
	var r reportJSON
	resp, err := c.do(ctx, http.MethodGet, jobPath(fullName)+"/"+buildRef(number)+"/testReport/api/json?tree="+url.QueryEscape(tree), &r)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return r, ErrNoTestReport
		}
		return r, err
	}
	return r, nil
}

func (r reportJSON) counts() TestReport {
	out := TestReport{FailCount: r.FailCount, PassCount: r.PassCount, SkipCount: r.SkipCount}
	if out.PassCount == 0 && r.TotalCount > 0 {
		out.PassCount = max(0, r.TotalCount-r.FailCount-r.SkipCount)
	}
	return out
}

// TestCounts returns only the counts of a build's test report (cheap: no
// cases are fetched); number 0 means the last build. Failed is empty.
// A build without a report returns ErrNoTestReport.
func (c *Client) TestCounts(ctx context.Context, fullName string, number int) (TestReport, error) {
	r, err := c.fetchReport(ctx, fullName, number, "failCount,passCount,skipCount,totalCount")
	if err != nil {
		return TestReport{}, err
	}
	return r.counts(), nil
}

// TestReport returns the test report of a build; number 0 means the last
// build. A build without a report returns ErrNoTestReport.
//
// Aggregated reports (matrix, Maven modules) have no top-level suites; their
// suites live under childReports[].result and are walked too, with Child set
// to the cell or module name. tree= omits missing fields, so one query fits
// both shapes.
//
// Size: Jenkins cannot filter cases by status, and a {M,N} range on cases
// only slices by position, so the failed cases cannot be asked for alone.
// Every case of every suite is fetched (with only the fields named in tree=)
// and the failures are picked out here. On a huge report that response can be
// many MB; there is no cheaper standard endpoint (see TestCounts for counts).
func (c *Client) TestReport(ctx context.Context, fullName string, number int) (TestReport, error) {
	const cases = "cases[className,name,status,errorDetails,errorStackTrace]"
	const tree = "failCount,passCount,skipCount,totalCount,suites[name," + cases + "],childReports[child[name,url],result[suites[name," + cases + "]]]"
	r, err := c.fetchReport(ctx, fullName, number, tree)
	if err != nil {
		return TestReport{}, err
	}
	out := r.counts()
	add := func(child string, suites []reportSuite) {
		for _, su := range suites {
			for _, tc := range su.Cases {
				if tc.Status != "FAILED" && tc.Status != "REGRESSION" {
					continue
				}
				if len(out.Failed) >= MaxFailedCases {
					out.Truncated = true
					continue
				}
				fc := FailedCase{Suite: su.Name, Child: child, ClassName: tc.ClassName, Name: tc.Name}
				if tc.ErrorDetails != nil {
					fc.ErrorDetails = *tc.ErrorDetails
				}
				if tc.ErrorStackTrace != nil {
					fc.StackTrace = capLines(*tc.ErrorStackTrace, MaxStackTraceLines)
				}
				out.Failed = append(out.Failed, fc)
			}
		}
	}
	add("", r.Suites)
	for _, ch := range r.ChildReports {
		add(ch.Child.Name, ch.Result.Suites)
	}
	if out.FailCount > len(out.Failed) {
		out.Truncated = true
	}
	return out, nil
}

func capLines(s string, n int) string {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.TrimRight(strings.Join(lines[:n], ""), "\n")
}
