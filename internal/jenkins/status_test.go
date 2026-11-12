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

func TestBuildStatusContext(t *testing.T) {
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
	if b.EstimatedDuration != 2*time.Minute || b.Duration != time.Minute {
		t.Fatalf("durations %v %v", b.EstimatedDuration, b.Duration)
	}
	wantCauses := []Cause{{"Started by user Ada", "Ada Lovelace"}, {"Started by user bob", "bob"}, {"Started by timer", ""}}
	if !reflect.DeepEqual(b.Causes, wantCauses) {
		t.Fatalf("causes %+v", b.Causes)
	}
	wantChanges := []Change{{"abc123", "Ada", "fix it"}, {"def456", "Bob", "more"}}
	if !reflect.DeepEqual(b.Changes, wantChanges) {
		t.Fatalf("changes %+v", b.Changes)
	}
	wantParams := []BuildParam{{"BRANCH", "main", false}, {"DRY", "true", false}, {"FAST", "false", false}, {"SECRET", "", true}, {"EMPTY", "", false}}
	if !reflect.DeepEqual(b.Params, wantParams) {
		t.Fatalf("params %+v", b.Params)
	}

	// Running build: result null, nothing else known.
	b, err = c.BuildStatus(ctx, "running", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Building || b.Result != "" || b.EstimatedDuration != 0 {
		t.Fatalf("running %+v", b)
	}

	// No causes, changes or params: actions still holds {} and null.
	b, err = c.BuildStatus(ctx, "plain", 1)
	if err != nil {
		t.Fatal(err)
	}
	if b.Causes != nil || b.Changes != nil || b.Params != nil || b.EstimatedDuration != 0 {
		t.Fatalf("plain %+v", b)
	}
}

func TestBuildStatusFreestyleChangeSetAndDedup(t *testing.T) {
	// The fake serves only changeSets, so exercise the freestyle shape with
	// a hand-written server.
	srv := newRawServer(t, `{"number":4,"result":"SUCCESS","building":false,"estimatedDuration":-1,
	  "actions":[null,{},{"causes":[null,{"shortDescription":"x","userId":"u"}]},{"parameters":[null,{"name":"N","value":3}]}],
	  "changeSets":[null,{"items":[{"commitId":"a","author":{"fullName":"A"},"msg":"m"}]}],
	  "changeSet":{"items":[{"commitId":"a","author":{"fullName":"A"},"msg":"m"},{"commitId":"b","msg":"n"}]}}`)
	c, err := New(Options{BaseURL: srv})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.BuildStatus(context.Background(), "j", 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"a", "A", "m"}, {"b", "", "n"}}
	if !reflect.DeepEqual(b.Changes, want) {
		t.Fatalf("changes %+v", b.Changes)
	}
	if len(b.Causes) != 1 || b.Causes[0].User != "u" {
		t.Fatalf("causes %+v", b.Causes)
	}
	if len(b.Params) != 1 || b.Params[0].Value != "3" || b.EstimatedDuration != 0 {
		t.Fatalf("params %+v est %v", b.Params, b.EstimatedDuration)
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
