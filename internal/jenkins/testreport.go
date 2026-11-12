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

// TestReport returns the test report of a build; number 0 means the last
// build. A build without a report returns ErrNoTestReport.
//
// Size: Jenkins cannot filter cases by status, and a {M,N} range on cases
// only slices by position, so the failed cases cannot be asked for alone.
// Every case of every suite is fetched (with only the fields named in tree=)
// and the failures are picked out here. On a huge report that response can be
// many MB; there is no cheaper standard endpoint.
func (c *Client) TestReport(ctx context.Context, fullName string, number int) (TestReport, error) {
	const tree = "failCount,passCount,skipCount,suites[name,cases[className,name,status,errorDetails,errorStackTrace]]"
	var r struct {
		FailCount int `json:"failCount"`
		PassCount int `json:"passCount"`
		SkipCount int `json:"skipCount"`
		Suites    []struct {
			Name  string `json:"name"`
			Cases []struct {
				ClassName       string  `json:"className"`
				Name            string  `json:"name"`
				Status          string  `json:"status"`
				ErrorDetails    *string `json:"errorDetails"`
				ErrorStackTrace *string `json:"errorStackTrace"`
			} `json:"cases"`
		} `json:"suites"`
	}
	resp, err := c.do(ctx, http.MethodGet, jobPath(fullName)+"/"+buildRef(number)+"/testReport/api/json?tree="+url.QueryEscape(tree), &r)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return TestReport{}, ErrNoTestReport
		}
		return TestReport{}, err
	}
	out := TestReport{FailCount: r.FailCount, PassCount: r.PassCount, SkipCount: r.SkipCount}
	for _, su := range r.Suites {
		for _, tc := range su.Cases {
			if tc.Status != "FAILED" && tc.Status != "REGRESSION" {
				continue
			}
			if len(out.Failed) >= MaxFailedCases {
				out.Truncated = true
				continue
			}
			fc := FailedCase{Suite: su.Name, ClassName: tc.ClassName, Name: tc.Name}
			if tc.ErrorDetails != nil {
				fc.ErrorDetails = *tc.ErrorDetails
			}
			if tc.ErrorStackTrace != nil {
				fc.StackTrace = capLines(*tc.ErrorStackTrace, MaxStackTraceLines)
			}
			out.Failed = append(out.Failed, fc)
		}
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
