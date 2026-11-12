package jenkins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

// Real Jenkins (>= 2.176) ties the crumb to the web session, so the client
// must send the session cookie it received from /crumbIssuer back on the POST.
func TestTriggerSessionBoundCrumb(t *testing.T) {
	s := jenkinstest.New(t).EnableCrumb(true).SessionBoundCrumb(true)
	s.Job("team/app/develop")
	c, err := New(Options{BaseURL: s.URL, Username: s.User(), Token: s.Token()})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := c.Trigger(context.Background(), "team/app/develop", nil)
	if err != nil {
		t.Fatalf("trigger with a session-bound crumb: %v", err)
	}
	if ref.ID == 0 {
		t.Fatalf("no queue id: %+v", ref)
	}
}

// A crumb issuer that refuses us must not abort the trigger: API-token
// requests often do not need a crumb at all, so let the POST decide.
func TestTriggerCrumbIssuerForbiddenIsNotFatal(t *testing.T) {
	s := jenkinstest.New(t).CrumbIssuerForbidden(true)
	s.Job("team/app/develop")
	c, _ := New(Options{BaseURL: s.URL, Username: s.User(), Token: s.Token()})
	if _, err := c.Trigger(context.Background(), "team/app/develop", nil); err != nil {
		t.Fatalf("trigger should proceed without a crumb: %v", err)
	}
}

// A 403 on the POST that is about permissions must not be reported as bad
// credentials, and must carry the server's own explanation.
func TestTriggerForbiddenIsNotAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/crumbIssuer") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "<html><body><h1>HTTP ERROR 403</h1><p>me is missing the Job/Build permission</p></body></html>", http.StatusForbidden)
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, Username: "me", Token: "tok"})
	_, err := c.Trigger(context.Background(), "team/app/develop", nil)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	if errors.Is(err, ErrAuth) {
		t.Fatal("a permission 403 must not look like an authentication failure")
	}
	if !strings.Contains(err.Error(), "Job/Build permission") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("error should carry status and server detail: %v", err)
	}
}

// A 401 on the POST is still an authentication failure.
func TestTriggerUnauthorizedIsAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/crumbIssuer") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, Username: "me", Token: "bad"})
	if _, err := c.Trigger(context.Background(), "x", nil); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestTriggerUnknownJobIsErrJobNotFound(t *testing.T) {
	s := jenkinstest.New(t).EnableCrumb(true)
	c, err := New(Options{BaseURL: s.URL, Username: s.User(), Token: s.Token()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Trigger(context.Background(), "team/nope", nil)
	if !errors.Is(err, ErrJobNotFound) || !strings.Contains(err.Error(), "team/nope") {
		t.Fatalf("err = %v", err)
	}
}
