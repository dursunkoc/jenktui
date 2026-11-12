package jenkinstest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

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
