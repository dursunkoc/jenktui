package jenkinstest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func req(t *testing.T, s *jenkinstest.Server, method, path string, hdr map[string]string, body string) *http.Response {
	t.Helper()
	r, _ := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	r.SetBasicAuth("me", "tok")
	if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestAuthRequired(t *testing.T) {
	s := jenkinstest.New(t)
	resp, _ := http.Get(s.URL + "/api/json")
	if resp.StatusCode != 401 {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

func TestTriggerAndCrumb(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("a/b").Params(jenkinstest.StringParam("X", "", ""))
	resp := req(t, s, "POST", "/job/a/job/b/buildWithParameters", nil, url.Values{"X": {"1"}}.Encode())
	if resp.StatusCode != 201 || !strings.HasSuffix(resp.Header.Get("Location"), "/queue/item/101/") {
		t.Fatalf("%d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if tr, _ := s.LastTrigger(); tr.Job != "a/b" || tr.Params.Get("X") != "1" {
		t.Fatalf("%+v", tr)
	}
	s.EnableCrumb(true)
	if r := req(t, s, "POST", "/job/a/job/b/build", nil, ""); r.StatusCode != 403 {
		t.Fatalf("want 403, got %d", r.StatusCode)
	}
	if r := req(t, s, "POST", "/job/a/job/b/build", map[string]string{jenkinstest.CrumbHeaderName: s.Crumb()}, ""); r.StatusCode != 201 {
		t.Fatalf("want 201, got %d", r.StatusCode)
	}
	if r := req(t, s, "GET", "/crumbIssuer/api/json", nil, ""); r.StatusCode != 200 {
		t.Fatalf("crumb issuer %d", r.StatusCode)
	}
}

func TestWfapi(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("p").Pipeline(jenkinstest.Stage{Name: "Build", Status: jenkinstest.StageSuccess}).
		LastBuild(jenkinstest.Build{Number: 4, Result: "SUCCESS"})
	s.Job("free")
	if r := req(t, s, "GET", "/job/p/wfapi/runs", nil, ""); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	b, _ := io.ReadAll(req(t, s, "GET", "/job/p/4/wfapi/describe", nil, "").Body)
	if !strings.Contains(string(b), `"Build"`) {
		t.Fatal(string(b))
	}
	if r := req(t, s, "GET", "/job/free/wfapi/runs", nil, ""); r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
}

// getJSON GETs path and decodes the JSON body into a generic value.
func getJSON(t *testing.T, s *jenkinstest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp := req(t, s, "GET", path, nil, "")
	var m map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func TestBuildHistory(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("x").Builds(
		jenkinstest.Build{Number: 1, Result: "FAILURE"},
		jenkinstest.Build{Number: 3, Result: "SUCCESS"},
		jenkinstest.Build{Number: 2, Result: "ABORTED"},
	)
	code, m := getJSON(t, s, "/job/x/api/json")
	if code != 200 {
		t.Fatal(code)
	}
	list := m["builds"].([]any)
	if len(list) != 3 {
		t.Fatalf("builds %v", list)
	}
	for i, want := range []float64{3, 2, 1} {
		if got := list[i].(map[string]any)["number"]; got != want {
			t.Fatalf("builds[%d] = %v, want %v", i, got, want)
		}
	}
	if lb := m["lastBuild"].(map[string]any); lb["number"] != float64(3) {
		t.Fatalf("lastBuild %v", lb)
	}
	if _, b := getJSON(t, s, "/job/x/2/api/json"); b["result"] != "ABORTED" {
		t.Fatalf("build 2: %v", b)
	}
	if code, _ := getJSON(t, s, "/job/x/9/api/json"); code != 404 {
		t.Fatalf("missing build: %d", code)
	}
	// LastBuild still works and replaces the history.
	s.Job("x").LastBuild(jenkinstest.Build{Number: 7, Result: "SUCCESS"})
	if _, m := getJSON(t, s, "/job/x/api/json"); len(m["builds"].([]any)) != 1 {
		t.Fatalf("builds %v", m["builds"])
	}
}

func TestBuildFields(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("x").LastBuild(jenkinstest.Build{
		Number: 5, Result: "SUCCESS", EstimatedDuration: 90 * time.Second,
		Causes:  []jenkinstest.Cause{{Description: "Started by user Ada", User: "ada"}, {Description: "Started by timer"}},
		Changes: []jenkinstest.Change{{Commit: "abc123", Author: "Ada L", Message: "fix it"}},
		Params:  []jenkinstest.BuildParam{{Name: "ENV", Value: "prod"}},
	})
	s.Job("y").LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})

	_, b := getJSON(t, s, "/job/x/5/api/json")
	if b["estimatedDuration"] != float64(90000) {
		t.Fatalf("estimatedDuration %v", b["estimatedDuration"])
	}
	var causes, params []any
	var sawNull, sawEmpty bool
	for _, a := range b["actions"].([]any) {
		switch a := a.(type) {
		case nil:
			sawNull = true
		case map[string]any:
			if len(a) == 0 {
				sawEmpty = true
			}
			if c, ok := a["causes"]; ok {
				causes = c.([]any)
			}
			if p, ok := a["parameters"]; ok {
				params = p.([]any)
			}
		}
	}
	if !sawNull || !sawEmpty {
		t.Fatalf("actions should be mixed, with nulls and empty objects: %v", b["actions"])
	}
	if len(causes) != 2 || causes[0].(map[string]any)["userId"] != "ada" || causes[0].(map[string]any)["shortDescription"] != "Started by user Ada" {
		t.Fatalf("causes %v", causes)
	}
	if _, has := causes[1].(map[string]any)["userId"]; has {
		t.Fatalf("a cause with no user has no userId: %v", causes[1])
	}
	if len(params) != 1 || params[0].(map[string]any)["name"] != "ENV" || params[0].(map[string]any)["value"] != "prod" {
		t.Fatalf("params %v", params)
	}
	item := b["changeSets"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	if item["commitId"] != "abc123" || item["msg"] != "fix it" || item["author"].(map[string]any)["fullName"] != "Ada L" {
		t.Fatalf("change %v", item)
	}
	_, y := getJSON(t, s, "/job/y/1/api/json")
	if y["estimatedDuration"] != float64(-1) {
		t.Fatalf("no estimate should be -1, got %v", y["estimatedDuration"])
	}
}

func TestTestReport(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("x").Builds(jenkinstest.Build{Number: 1, Result: "SUCCESS"}, jenkinstest.Build{Number: 2, Result: "UNSTABLE"}).
		TestReport(jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{{Name: "pkg", Cases: []jenkinstest.TestCase{
			{ClassName: "A", Name: "ok", Status: jenkinstest.CasePassed},
			{ClassName: "A", Name: "bad", Status: jenkinstest.CaseFailed, ErrorDetails: "boom", ErrorStackTrace: "at A.bad"},
			{ClassName: "B", Name: "later", Status: jenkinstest.CaseSkipped},
			{ClassName: "B", Name: "back", Status: jenkinstest.CaseRegression},
		}}}})
	for _, ref := range []string{"2", "lastBuild"} {
		code, m := getJSON(t, s, "/job/x/"+ref+"/testReport/api/json")
		if code != 200 {
			t.Fatalf("%s: %d", ref, code)
		}
		if m["failCount"] != float64(2) || m["passCount"] != float64(1) || m["skipCount"] != float64(1) {
			t.Fatalf("%s counts: %v", ref, m)
		}
		cases := m["suites"].([]any)[0].(map[string]any)["cases"].([]any)
		bad := cases[1].(map[string]any)
		if bad["className"] != "A" || bad["name"] != "bad" || bad["status"] != "FAILED" || bad["errorDetails"] != "boom" || bad["errorStackTrace"] != "at A.bad" {
			t.Fatalf("case %v", bad)
		}
	}
	// Build 1 has no report; neither does a job that has none.
	if code, _ := getJSON(t, s, "/job/x/1/testReport/api/json"); code != 404 {
		t.Fatalf("build without report: %d", code)
	}
	s.Job("y").LastBuild(jenkinstest.Build{Number: 1})
	if code, _ := getJSON(t, s, "/job/y/1/testReport/api/json"); code != 404 {
		t.Fatalf("job without report: %d", code)
	}
	// Explicit counts win over derived ones, and TestReportFor targets a build.
	s.Job("y").TestReportFor(1, jenkinstest.TestReport{FailCount: 3, PassCount: 9})
	if _, m := getJSON(t, s, "/job/y/1/testReport/api/json"); m["failCount"] != float64(3) || m["passCount"] != float64(9) {
		t.Fatalf("%v", m)
	}
}
