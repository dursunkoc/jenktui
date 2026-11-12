package jenkinstest_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

// noRedirect lets tests see the 302 answers of stop and cancelItem.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

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
	resp, err := noRedirect.Do(r)
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
		Causes:  []jenkinstest.Cause{{Description: "Started by user Ada", User: "ada", UserName: "Ada L"}, {Description: "Started by timer", Class: jenkinstest.CauseTimer}, {Description: "other"}},
		Changes: []jenkinstest.Change{{Commit: "abc123", Author: "Ada L", Message: "fix it"}},
		Params: []jenkinstest.BuildParam{jenkinstest.StringBuildParam("ENV", "prod"), jenkinstest.BoolBuildParam("DRY", true),
			jenkinstest.PasswordBuildParam("PW"), {Name: "LEGACY", Value: "x"}},
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
	if len(causes) != 3 || causes[0].(map[string]any)["userId"] != "ada" || causes[0].(map[string]any)["shortDescription"] != "Started by user Ada" {
		t.Fatalf("causes %v", causes)
	}
	c0, c1, c2 := causes[0].(map[string]any), causes[1].(map[string]any), causes[2].(map[string]any)
	if c0["_class"] != "hudson.model.Cause$UserIdCause" || c0["userName"] != "Ada L" {
		t.Fatalf("user cause %v", c0)
	}
	if c1["_class"] != jenkinstest.CauseTimer || c2["_class"] != "hudson.model.Cause" {
		t.Fatalf("classes %v %v", c1, c2)
	}
	for _, c := range []map[string]any{c1, c2} {
		if _, has := c["userId"]; has {
			t.Fatalf("a cause with no user has no userId: %v", c)
		}
		if _, has := c["userName"]; has {
			t.Fatalf("a cause with no user has no userName: %v", c)
		}
	}
	if len(params) != 4 {
		t.Fatalf("params %v", params)
	}
	p := func(i int) map[string]any { return params[i].(map[string]any) }
	if p(0)["_class"] != "hudson.model.StringParameterValue" || p(0)["name"] != "ENV" || p(0)["value"] != "prod" {
		t.Fatalf("string param %v", p(0))
	}
	if p(1)["_class"] != "hudson.model.BooleanParameterValue" || p(1)["value"] != true {
		t.Fatalf("bool param %v", p(1))
	}
	if _, has := p(2)["value"]; has || p(2)["_class"] != "hudson.model.PasswordParameterValue" {
		t.Fatalf("password param %v", p(2))
	}
	if p(3)["_class"] != "hudson.model.StringParameterValue" || p(3)["value"] != "x" {
		t.Fatalf("untyped param is a string: %v", p(3))
	}
	item := b["changeSets"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
	if item["commitId"] != "abc123" || item["msg"] != "fix it" || item["author"].(map[string]any)["fullName"] != "Ada L" {
		t.Fatalf("change %v", item)
	}
	_, y := getJSON(t, s, "/job/y/1/api/json")
	if cs, ok := y["changeSets"].([]any); !ok || len(cs) != 0 {
		t.Fatalf("no changes means no change sets: %v", y["changeSets"])
	}
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

func TestStopAndCancel(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("x").Builds(jenkinstest.Build{Number: 1, Result: "SUCCESS"}, jenkinstest.Build{Number: 2, Building: true})
	id := s.AddQueueItem(jenkinstest.QueueItem{Job: "x", Why: "waiting"})
	other := s.AddQueueItem(jenkinstest.QueueItem{Job: "x", Why: "waiting"})

	if r := req(t, s, "POST", "/job/x/2/stop", nil, ""); r.StatusCode != 302 || r.Header.Get("Location") != s.URL+"/job/x/2/" {
		t.Fatalf("stop %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	_, b := getJSON(t, s, "/job/x/2/api/json")
	if b["result"] != "ABORTED" || b["building"] != false {
		t.Fatalf("after stop: %v", b)
	}
	if got := s.Stops(); len(got) != 1 || got[0] != (jenkinstest.Stop{Job: "x", Number: 2}) {
		t.Fatalf("stops %v", got)
	}
	// Stopping a finished build is recorded but leaves its result alone.
	if r := req(t, s, "POST", "/job/x/1/stop", nil, ""); r.StatusCode != 302 {
		t.Fatalf("stop finished %d", r.StatusCode)
	}
	if _, b := getJSON(t, s, "/job/x/1/api/json"); b["result"] != "SUCCESS" {
		t.Fatalf("finished build changed: %v", b)
	}
	if len(s.Stops()) != 2 {
		t.Fatalf("stops %v", s.Stops())
	}
	if r := req(t, s, "POST", "/job/x/9/stop", nil, ""); r.StatusCode != 404 {
		t.Fatalf("stop missing build: %d", r.StatusCode)
	}
	if r := req(t, s, "GET", "/job/x/2/stop", nil, ""); r.StatusCode != 404 {
		t.Fatalf("GET stop: %d", r.StatusCode)
	}

	path := "/queue/cancelItem?id=" + strconv.Itoa(id)
	if r := req(t, s, "POST", path, nil, ""); r.StatusCode != 302 || r.Header.Get("Location") == "" {
		t.Fatalf("cancel %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if got := s.Cancels(); len(got) != 1 || got[0] != id {
		t.Fatalf("cancels %v", got)
	}
	// The item stays and answers as a cancelled LeftItem, but leaves the queue list.
	code, it := getJSON(t, s, "/queue/item/"+strconv.Itoa(id)+"/api/json")
	if code != 200 || it["cancelled"] != true || it["_class"] != "hudson.model.Queue$LeftItem" {
		t.Fatalf("cancelled item: %d %v", code, it)
	}
	_, ql := getJSON(t, s, "/queue/api/json")
	if items := ql["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != float64(other) {
		t.Fatalf("queue list %v", ql)
	}
	if _, j := getJSON(t, s, "/job/x/api/json"); j["inQueue"] != true { // the other item is still queued
		t.Fatalf("inQueue %v", j["inQueue"])
	}
	if r := req(t, s, "GET", "/queue/item/"+strconv.Itoa(other)+"/api/json", nil, ""); r.StatusCode != 200 {
		t.Fatalf("other item gone: %d", r.StatusCode)
	}
	if r := req(t, s, "POST", path, nil, ""); r.StatusCode != 404 {
		t.Fatalf("cancel twice: %d", r.StatusCode)
	}
}

func TestStopAndCancelNeedCrumb(t *testing.T) {
	s := jenkinstest.New(t).EnableCrumb(true)
	s.Job("x").LastBuild(jenkinstest.Build{Number: 1, Building: true})
	id := s.AddQueueItem(jenkinstest.QueueItem{Job: "x"})
	cancel := "/queue/cancelItem?id=" + strconv.Itoa(id)
	for _, p := range []string{"/job/x/1/stop", cancel} {
		if r := req(t, s, "POST", p, nil, ""); r.StatusCode != 403 {
			t.Fatalf("%s without crumb: %d", p, r.StatusCode)
		}
	}
	if len(s.Stops()) != 0 || len(s.Cancels()) != 0 {
		t.Fatal("rejected calls must not be recorded")
	}
	h := map[string]string{jenkinstest.CrumbHeaderName: s.Crumb()}
	for _, p := range []string{"/job/x/1/stop", cancel} {
		if r := req(t, s, "POST", p, h, ""); r.StatusCode != 302 {
			t.Fatalf("%s with crumb: %d", p, r.StatusCode)
		}
	}
	if len(s.Stops()) != 1 || len(s.Cancels()) != 1 {
		t.Fatal("calls with a crumb must be recorded")
	}
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestTreeFilter(t *testing.T) {
	s := jenkinstest.New(t)
	ts := time.UnixMilli(1700000000123)
	s.Job("f/x").Builds(
		jenkinstest.Build{Number: 1, Result: "SUCCESS", Timestamp: ts},
		jenkinstest.Build{Number: 2, Result: "FAILURE", Timestamp: ts},
		jenkinstest.Build{Number: 3, Result: "SUCCESS", Timestamp: ts,
			Causes:  []jenkinstest.Cause{{Description: "by Ada", User: "ada"}},
			Changes: []jenkinstest.Change{{Commit: "c1", Author: "Ada", Message: "m"}}},
	)
	q := func(tree string) map[string]any {
		t.Helper()
		code, m := getJSON(t, s, "/job/f/job/x/api/json?tree="+url.QueryEscape(tree))
		if code != 200 {
			t.Fatalf("%s: %d", tree, code)
		}
		return m
	}

	t.Run("unnamed fields are omitted but _class stays", func(t *testing.T) {
		m := q("name,color")
		if got := strings.Join(keys(m), ","); got != "_class,color,name" {
			t.Fatalf("keys %s", got)
		}
	})
	t.Run("a field named without [...] is only a stub", func(t *testing.T) {
		m := q("lastBuild,builds,name")
		if got := fmt.Sprint(m["lastBuild"]); got != "map[_class:hudson.model.FreeStyleBuild]" {
			t.Fatalf("lastBuild %s", got)
		}
		for _, b := range m["builds"].([]any) {
			if got := strings.Join(keys(b.(map[string]any)), ","); got != "_class" {
				t.Fatalf("build stub keys %s", got)
			}
		}
		if m["name"] != "x" {
			t.Fatalf("scalars stay whole: %v", m["name"])
		}
		w := q("*")
		if lb := w["lastBuild"].(map[string]any); len(lb) != 1 {
			t.Fatalf("* must stub objects too: %v", lb)
		}
		// Lists of scalars stay whole.
		s.Job("c").Params(jenkinstest.ChoiceParam("P", "", "a", "b"))
		_, c := getJSON(t, s, "/job/c/api/json?tree="+url.QueryEscape("property[parameterDefinitions[choices]]"))
		defs := c["property"].([]any)[0].(map[string]any)["parameterDefinitions"].([]any)[0].(map[string]any)
		if fmt.Sprint(defs["choices"]) != "[a b]" {
			t.Fatalf("choices %v", defs)
		}
	})
	t.Run("unknown fields are ignored", func(t *testing.T) {
		m := q("name,nosuchfield,builds[nosuch]")
		if _, has := m["nosuchfield"]; has {
			t.Fatalf("%v", m)
		}
		for _, b := range m["builds"].([]any) {
			if got := strings.Join(keys(b.(map[string]any)), ","); got != "_class" {
				t.Fatalf("build keys %s", got)
			}
		}
	})
	t.Run("nested lists and objects", func(t *testing.T) {
		m := q("builds[number,actions[causes[shortDescription,userId]],changeSets[items[commitId,author[fullName],msg]]]")
		b := m["builds"].([]any)[0].(map[string]any) // newest first: #3
		if b["number"] != float64(3) {
			t.Fatalf("%v", b)
		}
		if _, has := b["result"]; has {
			t.Fatalf("result not asked for: %v", b)
		}
		var causes map[string]any
		for _, a := range b["actions"].([]any) {
			if am, ok := a.(map[string]any); ok && am["causes"] != nil {
				causes = am["causes"].([]any)[0].(map[string]any)
			}
		}
		if causes == nil || causes["userId"] != "ada" || causes["shortDescription"] != "by Ada" {
			t.Fatalf("actions %v", b["actions"])
		}
		if _, has := causes["_class"]; !has {
			t.Fatalf("_class should stay: %v", causes)
		}
		item := b["changeSets"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
		if item["commitId"] != "c1" || item["author"].(map[string]any)["fullName"] != "Ada" {
			t.Fatalf("%v", item)
		}
		if _, has := item["kind"]; has {
			t.Fatalf("%v", item)
		}
		// null entries in a list stay null.
		sawNull := false
		for _, a := range b["actions"].([]any) {
			sawNull = sawNull || a == nil
		}
		if !sawNull {
			t.Fatalf("null action dropped: %v", b["actions"])
		}
	})
	t.Run("ranges", func(t *testing.T) {
		nums := func(tree string) []float64 {
			var out []float64
			for _, b := range q(tree)["builds"].([]any) {
				out = append(out, b.(map[string]any)["number"].(float64))
			}
			return out
		}
		for tree, want := range map[string][]float64{
			"builds[number]":       {3, 2, 1},
			"builds[number]{0,2}":  {3, 2},
			"builds[number]{1,3}":  {2, 1},
			"builds[number]{1,}":   {2, 1},
			"builds[number]{,1}":   {3},
			"builds[number]{2}":    {1},
			"builds[number]{0,99}": {3, 2, 1},
			"builds[number]{5,9}":  {},
		} {
			if got := nums(tree); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s: got %v want %v", tree, got, want)
			}
		}
	})
	t.Run("large numbers stay exact", func(t *testing.T) {
		resp := req(t, s, "GET", "/job/f/job/x/3/api/json?tree=timestamp", nil, "")
		b, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(b), "1700000000123") {
			t.Fatal(string(b))
		}
	})
	t.Run("folders and other endpoints", func(t *testing.T) {
		code, m := getJSON(t, s, "/job/f/api/json?tree=jobs[name]")
		if code != 200 || len(m["jobs"].([]any)) != 1 {
			t.Fatalf("%d %v", code, m)
		}
		if _, has := m["fullName"]; has {
			t.Fatalf("%v", m)
		}
		if _, m := getJSON(t, s, "/me/api/json?tree=fullName"); strings.Join(keys(m), ",") != "_class,fullName" {
			t.Fatalf("%v", m)
		}
		if _, m := getJSON(t, s, "/queue/api/json?tree=items[id]"); strings.Join(keys(m), ",") != "_class,items" {
			t.Fatalf("%v", m)
		}
	})
	t.Run("without tree everything is served; malformed tree is a 400", func(t *testing.T) {
		if _, m := getJSON(t, s, "/job/f/job/x/api/json"); m["color"] == nil || m["builds"] == nil {
			t.Fatalf("%v", keys(m))
		}
		for _, bad := range []string{"builds[number", "a,,b", "builds[number]{x}", "]"} {
			if code, _ := getJSON(t, s, "/job/f/job/x/api/json?tree="+url.QueryEscape(bad)); code != 400 {
				t.Errorf("%q: %d", bad, code)
			}
		}
	})
	t.Run("wfapi ignores tree like Jenkins", func(t *testing.T) {
		s.Job("p").Pipeline(jenkinstest.Stage{Name: "Build", Status: jenkinstest.StageSuccess}).LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})
		_, m := getJSON(t, s, "/job/p/1/wfapi/describe?tree=id")
		if m["stages"] == nil {
			t.Fatalf("%v", m)
		}
	})
}
