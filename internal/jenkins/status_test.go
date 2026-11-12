package jenkins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func TestBuildStatusAndContext(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("rich").LastBuild(jenkinstest.Build{
		Number: 7, Result: "SUCCESS", Duration: time.Minute, EstimatedDuration: 2 * time.Minute,
		Causes: []jenkinstest.Cause{
			{Description: "Started by user Ada", User: "ada", UserName: "Ada Lovelace"},
			{Description: "Started by user bob", User: "bob"},
			{Description: "Started by timer", Class: jenkinstest.CauseTimer},
		},
		Changes: []jenkinstest.Change{
			{Commit: "abc123", Author: "Ada", Message: "fix it"},
			{Commit: "def456", Author: "Bob", Message: "more"},
		},
		Params: []jenkinstest.BuildParam{
			jenkinstest.StringBuildParam("BRANCH", "main"),
			jenkinstest.BoolBuildParam("DRY", true),
			jenkinstest.BoolBuildParam("FAST", false),
			jenkinstest.PasswordBuildParam("SECRET"),
			jenkinstest.StringBuildParam("EMPTY", ""),
		},
	})
	s.Job("running").LastBuild(jenkinstest.Build{Number: 3, Building: true})
	s.Job("plain").LastBuild(jenkinstest.Build{Number: 1, Result: "FAILURE"})
	c := newTestClient(t, s)
	ctx := context.Background()

	b, err := c.BuildStatus(ctx, "rich", 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.EstimatedDuration != 2*time.Minute || b.Duration != time.Minute || b.Number != 7 {
		t.Fatalf("status %+v", b)
	}
	bc, err := c.BuildContext(ctx, "rich", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantCauses := []Cause{{"Started by user Ada", "Ada Lovelace"}, {"Started by user bob", "bob"}, {"Started by timer", ""}}
	if !reflect.DeepEqual(bc.Causes, wantCauses) {
		t.Fatalf("causes %+v", bc.Causes)
	}
	wantChanges := []Change{{"abc123", "Ada", "fix it"}, {"def456", "Bob", "more"}}
	if !reflect.DeepEqual(bc.Changes, wantChanges) {
		t.Fatalf("changes %+v", bc.Changes)
	}
	wantParams := []BuildParam{{"BRANCH", "main", false}, {"DRY", "true", false}, {"FAST", "false", false}, {"SECRET", "", true}, {"EMPTY", "", false}}
	if !reflect.DeepEqual(bc.Params, wantParams) {
		t.Fatalf("params %+v", bc.Params)
	}

	// Running build: result null, nothing else known.
	b, err = c.BuildStatus(ctx, "running", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Building || b.Result != "" || b.EstimatedDuration != 0 {
		t.Fatalf("running %+v", b)
	}
	if bc, err = c.BuildContext(ctx, "running", 0); err != nil || len(bc.Causes) != 0 {
		t.Fatalf("running context %+v %v", bc, err)
	}

	// No causes, changes or params: actions still holds {} and null.
	bc, err = c.BuildContext(ctx, "plain", 1)
	if err != nil {
		t.Fatal(err)
	}
	if bc.Causes != nil || bc.Changes != nil || bc.Params != nil {
		t.Fatalf("plain %+v", bc)
	}
	if _, err = c.BuildContext(ctx, "plain", 99); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func rawContext(t *testing.T, body string) BuildContext {
	t.Helper()
	c, err := New(Options{BaseURL: newRawServer(t, body)})
	if err != nil {
		t.Fatal(err)
	}
	bc, err := c.BuildContext(context.Background(), "j", 4)
	if err != nil {
		t.Fatal(err)
	}
	return bc
}

func TestBuildContextPrefersChangeSets(t *testing.T) {
	bc := rawContext(t, `{"actions":[null,{},{"causes":[null,{"shortDescription":"x","userId":"u"}]}],
	  "changeSets":[null,{"items":[{"commitId":"a","author":{"fullName":"A"},"msg":"m\nbody"}]}],
	  "changeSet":{"items":[{"commitId":"z","msg":"other"}]}}`)
	if want := []Change{{"a", "A", "m\nbody"}}; !reflect.DeepEqual(bc.Changes, want) {
		t.Fatalf("changes %+v", bc.Changes)
	}
	if len(bc.Causes) != 1 || bc.Causes[0].User != "u" {
		t.Fatalf("causes %+v", bc.Causes)
	}
}

func TestBuildContextFreestyleFallback(t *testing.T) {
	// Pipeline builds serve an empty changeSets; freestyle serves changeSet.
	bc := rawContext(t, `{"changeSets":[],"changeSet":{"items":[{"commitId":"b","msg":"n"},{"commitId":"b","msg":"n"}]}}`)
	// Whole-value dedup is gone: two identical items are two changes.
	if want := []Change{{"b", "", "n"}, {"b", "", "n"}}; !reflect.DeepEqual(bc.Changes, want) {
		t.Fatalf("changes %+v", bc.Changes)
	}
}

func TestBuildContextParamClasses(t *testing.T) {
	bc := rawContext(t, `{"actions":[null,{"parameters":[
	  {"_class":"hudson.model.FileParameterValue","name":"F"},
	  {"_class":"hudson.model.StringParameterValue","name":"S","value":null},
	  {"_class":"hudson.plugins.git.GitParameterValue","name":"G","value":"v1"},
	  {"_class":"hudson.model.RunParameterValue","name":"R","value":{"jobName":"x","number":3}},
	  {"_class":"hudson.model.PasswordParameterValue","name":"P","value":"leaked"},
	  {"_class":"hudson.model.StringParameterValue","name":"N","value":3}]}]}`)
	want := []BuildParam{{"F", "", false}, {"S", "", false}, {"G", "v1", false}, {"R", "", false}, {"P", "", true}, {"N", "3", false}}
	if !reflect.DeepEqual(bc.Params, want) {
		t.Fatalf("params %+v", bc.Params)
	}
}

func TestBuildHistory(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("h").Builds(
		jenkinstest.Build{Number: 3, Building: true},
		jenkinstest.Build{Number: 2, Result: "FAILURE"},
		jenkinstest.Build{Number: 1, Result: "SUCCESS"},
	)
	s.Job("empty")
	c := newTestClient(t, s)
	ctx := context.Background()

	got, err := c.BuildHistory(ctx, "h", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []Build{{Number: 3, Building: true}, {Number: 2, Result: "FAILURE"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	got, err = c.BuildHistory(ctx, "h", 50)
	if err != nil || len(got) != 3 || got[2].Number != 1 || got[2].Result != "SUCCESS" {
		t.Fatalf("n > history: %+v %v", got, err)
	}
	if got, err = c.BuildHistory(ctx, "empty", 5); err != nil || len(got) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
	if _, err = c.BuildHistory(ctx, "nope", 5); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func newRawServer(t *testing.T, body string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}
