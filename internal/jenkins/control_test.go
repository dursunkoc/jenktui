package jenkins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

// crumb setups every control test runs under.
var controlModes = []struct {
	name  string
	setup func(*jenkinstest.Server)
}{
	{"no crumb", func(s *jenkinstest.Server) { s.EnableCrumb(false) }},
	{"crumb", func(s *jenkinstest.Server) { s.EnableCrumb(true) }},
	{"session-bound crumb", func(s *jenkinstest.Server) { s.EnableCrumb(true).SessionBoundCrumb(true) }},
	{"crumb issuer forbidden", func(s *jenkinstest.Server) { s.CrumbIssuerForbidden(true) }},
}

func TestStopBuildRunning(t *testing.T) {
	for _, m := range controlModes {
		t.Run(m.name, func(t *testing.T) {
			s := jenkinstest.New(t)
			m.setup(s)
			s.Job("team/app").LastBuild(jenkinstest.Build{Number: 7, Building: true})
			c := newTestClient(t, s)
			if err := c.StopBuild(context.Background(), "team/app", 7); err != nil {
				t.Fatal(err)
			}
			if got, want := s.Stops(), []jenkinstest.Stop{{Job: "team/app", Number: 7}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("stops %v want %v", got, want)
			}
			b, err := c.BuildStatus(context.Background(), "team/app", 7)
			if err != nil || b.Building || b.Result != "ABORTED" {
				t.Fatalf("build after stop: %+v, %v", b, err)
			}
		})
	}
}

func TestStopBuildFinishedIsSuccess(t *testing.T) {
	s := jenkinstest.New(t).EnableCrumb(true)
	s.Job("j").LastBuild(jenkinstest.Build{Number: 3, Result: "SUCCESS"})
	c := newTestClient(t, s)
	if err := c.StopBuild(context.Background(), "j", 3); err != nil {
		t.Fatal(err)
	}
	if len(s.Stops()) != 1 {
		t.Fatalf("stops %v", s.Stops())
	}
	if b, _ := c.BuildStatus(context.Background(), "j", 3); b.Result != "SUCCESS" {
		t.Fatalf("result changed: %+v", b)
	}
}

func TestStopBuildUnknownBuild(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("j").LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})
	c := newTestClient(t, s)
	if err := c.StopBuild(context.Background(), "j", 99); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("want ErrBuildNotFound, got %v", err)
	}
	if err := c.StopBuild(context.Background(), "nope", 1); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("unknown job: want ErrBuildNotFound, got %v", err)
	}
	if err := c.StopBuild(context.Background(), "j", 0); err == nil {
		t.Fatal("number 0 must be rejected")
	}
}

func TestStopBuildRotatedCrumbRetries(t *testing.T) {
	s := jenkinstest.New(t).EnableCrumb(true)
	s.Job("j").Builds(jenkinstest.Build{Number: 2, Building: true}, jenkinstest.Build{Number: 1, Result: "SUCCESS"})
	c := newTestClient(t, s)
	if err := c.StopBuild(context.Background(), "j", 1); err != nil { // primes the crumb cache
		t.Fatal(err)
	}
	s.RotateCrumb("rotated")
	if err := c.StopBuild(context.Background(), "j", 2); err != nil {
		t.Fatalf("retry after stale crumb: %v", err)
	}
	if len(s.Stops()) != 2 {
		t.Fatalf("stops %v", s.Stops())
	}
}

func TestCancelQueue(t *testing.T) {
	for _, m := range controlModes {
		t.Run(m.name, func(t *testing.T) {
			s := jenkinstest.New(t)
			m.setup(s)
			s.Job("j")
			id := s.AddQueueItem(jenkinstest.QueueItem{Job: "j", Why: "waiting"})
			c := newTestClient(t, s)
			if err := c.CancelQueue(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if got := s.Cancels(); !reflect.DeepEqual(got, []int{id}) {
				t.Fatalf("cancels %v", got)
			}
			st, err := c.ResolveQueue(context.Background(), QueueRef{ID: id})
			if err != nil || st.State != QueueCancelled {
				t.Fatalf("after cancel: %+v, %v", st, err)
			}
		})
	}
}

func TestCancelQueueRotatedCrumbRetries(t *testing.T) {
	s := jenkinstest.New(t).EnableCrumb(true)
	a := s.AddQueueItem(jenkinstest.QueueItem{Job: "j"})
	b := s.AddQueueItem(jenkinstest.QueueItem{Job: "j"})
	c := newTestClient(t, s)
	if err := c.CancelQueue(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	s.RotateCrumb("rotated")
	if err := c.CancelQueue(context.Background(), b); err != nil {
		t.Fatalf("retry after stale crumb: %v", err)
	}
}

func TestCancelQueueUnknown(t *testing.T) {
	s := jenkinstest.New(t)
	c := newTestClient(t, s)
	if err := c.CancelQueue(context.Background(), 4242); !errors.Is(err, ErrQueueItemNotFound) {
		t.Fatalf("want ErrQueueItemNotFound, got %v", err)
	}
	if err := c.CancelQueue(context.Background(), 0); err == nil {
		t.Fatal("id 0 must be rejected")
	}
}

// A 403 about permissions carries Jenkins' text and is not an auth failure.
func TestControlForbiddenCarriesText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/crumbIssuer") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "<html><body><h1>HTTP ERROR 403</h1><p>me is missing the Job/Cancel permission</p></body></html>", http.StatusForbidden)
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, Username: "me", Token: "tok"})
	for name, err := range map[string]error{
		"stop":   c.StopBuild(context.Background(), "j", 1),
		"cancel": c.CancelQueue(context.Background(), 1),
	} {
		if !errors.Is(err, ErrForbidden) || errors.Is(err, ErrAuth) || !strings.Contains(err.Error(), "Job/Cancel permission") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestControlUnauthorizedIsAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/crumbIssuer") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, Username: "me", Token: "bad"})
	if err := c.StopBuild(context.Background(), "j", 1); !errors.Is(err, ErrAuth) {
		t.Fatalf("stop: %v", err)
	}
	if err := c.CancelQueue(context.Background(), 1); !errors.Is(err, ErrAuth) {
		t.Fatalf("cancel: %v", err)
	}
}

// 204 (newer Jenkins) and a 302 whose target errors are both success: the
// redirect is not followed.
func TestControlRedirectsAndNoContent(t *testing.T) {
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/crumbIssuer"):
			http.NotFound(w, r)
		case r.Method == http.MethodGet:
			gets++
			http.Error(w, "boom", http.StatusInternalServerError)
		case strings.HasSuffix(r.URL.Path, "/stop"):
			http.Redirect(w, r, "/job/j/1/", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, Username: "me", Token: "tok"})
	if err := c.StopBuild(context.Background(), "j", 1); err != nil {
		t.Fatalf("stop with failing redirect target: %v", err)
	}
	if err := c.CancelQueue(context.Background(), 5); err != nil {
		t.Fatalf("cancel with 204: %v", err)
	}
	if gets != 0 {
		t.Fatalf("redirect was followed (%d GETs)", gets)
	}
}
