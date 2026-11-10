package jenkins

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func newTestClient(t *testing.T, s *jenkinstest.Server) *Client {
	t.Helper()
	c, err := New(Options{BaseURL: s.URL, Username: "me", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTrigger(t *testing.T) {
	tests := []struct {
		name         string
		crumb        bool
		params       map[string]string
		wantEndpoint string
	}{
		{"no crumb, no params", false, nil, "build"},
		{"crumb, no params", true, nil, "build"},
		{"no crumb, params", false, map[string]string{"ENV": "stg", "MSG": "a b&c"}, "buildWithParameters"},
		{"crumb, params", true, map[string]string{"ENV": "prod"}, "buildWithParameters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := jenkinstest.New(t).EnableCrumb(tc.crumb)
			s.Job("team/app")
			c := newTestClient(t, s)
			ref, err := c.Trigger(context.Background(), "team/app", tc.params)
			if err != nil {
				t.Fatal(err)
			}
			tr, ok := s.LastTrigger()
			if !ok || tr.Endpoint != tc.wantEndpoint || tr.Job != "team/app" || ref.ID != tr.QueueID || ref.URL == "" {
				t.Fatalf("trigger %+v ref %+v", tr, ref)
			}
			got := map[string]string{}
			for k := range tr.Params {
				got[k] = tr.Params.Get(k)
			}
			want := tc.params
			if want == nil {
				want = map[string]string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("params got %v want %v", got, want)
			}
		})
	}
}

func TestTriggerStaleCrumbRetriesOnce(t *testing.T) {
	s := jenkinstest.New(t).EnableCrumb(true)
	s.Job("j")
	c := newTestClient(t, s)
	if _, err := c.Trigger(context.Background(), "j", nil); err != nil {
		t.Fatal(err)
	}
	s.RotateCrumb("rotated")
	if _, err := c.Trigger(context.Background(), "j", nil); err != nil {
		t.Fatalf("retry after stale crumb: %v", err)
	}
	if n := len(s.Triggers()); n != 2 {
		t.Fatalf("triggers = %d", n)
	}
}

func TestTriggerErrors(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("j")
	c := newTestClient(t, s)
	if _, err := c.Trigger(context.Background(), "missing", nil); err == nil {
		t.Fatal("want error for missing job")
	}
	bad, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "x"})
	if _, err := bad.Trigger(context.Background(), "j", nil); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestResolveQueue(t *testing.T) {
	s := jenkinstest.New(t)
	c := newTestClient(t, s)
	why := "Waiting for next available executor"
	wait := s.AddQueueItem(jenkinstest.QueueItem{Job: "a", Why: why})
	blocked := s.AddQueueItem(jenkinstest.QueueItem{Job: "a", Why: "blocked by a", Blocked: true})
	buildable := s.AddQueueItem(jenkinstest.QueueItem{Job: "a", Why: "no executor", Buildable: true})
	cancelled := s.AddQueueItem(jenkinstest.QueueItem{Job: "a", Cancelled: true})
	started := s.AddQueueItem(jenkinstest.QueueItem{Job: "a", Executable: 7})
	gone := s.AddQueueItem(jenkinstest.QueueItem{Job: "a"})
	s.RemoveQueueItem(gone)

	tests := []struct {
		name string
		id   int
		want QueueStatus
		err  error
	}{
		{"waiting", wait, QueueStatus{State: QueueWaiting, Why: why}, nil},
		{"blocked", blocked, QueueStatus{State: QueueBlocked, Why: "blocked by a"}, nil},
		{"buildable", buildable, QueueStatus{State: QueueBuildable, Why: "no executor"}, nil},
		{"cancelled", cancelled, QueueStatus{State: QueueCancelled}, nil},
		{"started", started, QueueStatus{State: QueueStarted, BuildNumber: 7}, nil},
		{"gone", gone, QueueStatus{}, ErrQueueItemGone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.ResolveQueue(context.Background(), QueueRef{ID: tc.id})
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v want %v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
	// Resolving via URL only, and the trigger -> started flow.
	s.Job("flow")
	ref, err := c.Trigger(context.Background(), "flow", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := c.ResolveQueue(context.Background(), QueueRef{URL: ref.URL}); err != nil || st.State != QueueWaiting {
		t.Fatalf("%+v %v", st, err)
	}
	s.ResolveQueueItem(ref.ID, 12)
	if st, err := c.ResolveQueue(context.Background(), ref); err != nil || st.State != QueueStarted || st.BuildNumber != 12 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestBuildStatus(t *testing.T) {
	ts := time.UnixMilli(1_700_000_000_000)
	s := jenkinstest.New(t)
	s.Job("team/done").LastBuild(jenkinstest.Build{Number: 5, Result: "FAILURE", Timestamp: ts, Duration: 90 * time.Second})
	s.Job("team/run").LastBuild(jenkinstest.Build{Number: 9, Building: true, Result: "SUCCESS", Timestamp: ts})
	s.Job("team/never")
	c := newTestClient(t, s)

	tests := []struct {
		name   string
		job    string
		number int
		want   Build
		err    error
	}{
		{"finished last", "team/done", 0, Build{Number: 5, Result: "FAILURE", Timestamp: ts, Duration: 90 * time.Second}, nil},
		{"finished by number", "team/done", 5, Build{Number: 5, Result: "FAILURE", Timestamp: ts, Duration: 90 * time.Second}, nil},
		{"in progress", "team/run", 0, Build{Number: 9, Building: true, Timestamp: ts}, nil},
		{"unknown number", "team/done", 99, Build{}, ErrBuildNotFound},
		{"no builds", "team/never", 0, Build{}, ErrBuildNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.BuildStatus(context.Background(), tc.job, tc.number)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v want %v", err, tc.err)
			}
			if err != nil {
				return
			}
			if got.URL == "" {
				t.Fatal("empty URL")
			}
			got.URL = ""
			if !got.Timestamp.Equal(tc.want.Timestamp) {
				t.Fatalf("timestamp %v", got.Timestamp)
			}
			got.Timestamp, tc.want.Timestamp = time.Time{}, time.Time{}
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestStages(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("pipe").LastBuild(jenkinstest.Build{Number: 4, Building: true}).Pipeline(
		jenkinstest.Stage{Name: "Build", Status: jenkinstest.StageSuccess, Duration: 2 * time.Second},
		jenkinstest.Stage{Name: "Test", Status: jenkinstest.StageInProgress, Children: []jenkinstest.Stage{
			{Name: "unit", Status: jenkinstest.StageSuccess, Duration: time.Second},
			{Name: "integ", Status: jenkinstest.StageInProgress},
		}},
		jenkinstest.Stage{Name: "Approve", Status: jenkinstest.StagePausedInput},
		jenkinstest.Stage{Name: "Deploy", Status: jenkinstest.StageNotExecuted},
		jenkinstest.Stage{Name: "Odd", Status: "SOMETHING_NEW"},
		jenkinstest.Stage{Name: "Bad", Status: jenkinstest.StageFailed},
	)
	s.Job("free").LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})
	c := newTestClient(t, s)

	want := []Stage{
		{Name: "Build", Status: StageSuccess, Duration: 2 * time.Second},
		{Name: "Test", Status: StageRunning, Children: []Stage{
			{Name: "unit", Status: StageSuccess, Duration: time.Second},
			{Name: "integ", Status: StageRunning},
		}},
		{Name: "Approve", Status: StagePaused},
		{Name: "Deploy", Status: StageNotExecuted},
		{Name: "Odd", Status: StageUnknown},
		{Name: "Bad", Status: StageFailed},
	}
	for _, num := range []int{0, 4} {
		got, err := c.Stages(context.Background(), "pipe", num)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("number %d:\ngot  %+v\nwant %+v", num, got, want)
		}
	}
	for _, job := range []string{"free", "missing"} {
		if _, err := c.Stages(context.Background(), job, 0); !errors.Is(err, ErrNoStages) {
			t.Fatalf("%s: want ErrNoStages, got %v", job, err)
		}
	}
}

func TestStatusFromColor(t *testing.T) {
	tests := map[string]JobStatus{
		"blue": JobSuccess, "blue_anime": JobRunning, "red": JobFailed, "red_anime": JobRunning,
		"yellow": JobUnstable, "disabled": JobDisabled, "notbuilt": JobNotBuilt, "grey": JobNotBuilt,
		"aborted": JobAborted, "": JobUnknown, "weird": JobUnknown,
	}
	for in, want := range tests {
		if got := StatusFromColor(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
