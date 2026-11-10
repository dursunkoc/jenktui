package jenkins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func fakeJenkins(t *testing.T) *jenkinstest.Server {
	t.Helper()
	s := jenkinstest.New(t)
	s.Job("top").LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})
	s.Job("team/build").LastBuild(jenkinstest.Build{Number: 1, Result: "FAILURE"})
	s.Job("team/deploy").LastBuild(jenkinstest.Build{Number: 2, Building: true, Result: "SUCCESS"})
	s.Job("team/app/main").LastBuild(jenkinstest.Build{Number: 3, Result: "SUCCESS"})
	return s
}

func TestPingAndAuthFailure(t *testing.T) {
	s := fakeJenkins(t)
	c, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "tok"})
	if n, err := c.Ping(context.Background()); err != nil || n != "Me Myself" {
		t.Fatalf("ping: %q %v", n, err)
	}
	bad, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "wrong"})
	if _, err := bad.Ping(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestListJobsRecursesFolders(t *testing.T) {
	s := fakeJenkins(t)
	c, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "tok"})
	jobs, err := c.ListJobs(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, j := range jobs {
		names = append(names, j.FullName)
	}
	want := []string{"team/app/main", "team/build", "team/deploy", "top"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v want %v", names, want)
	}
}

func TestListJobsUnderParent(t *testing.T) {
	s := fakeJenkins(t)
	c, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "tok"})
	jobs, err := c.ListJobs(context.Background(), "team/app")
	if err != nil || len(jobs) != 1 || jobs[0].FullName != "team/app/main" {
		t.Fatalf("got %v %v", jobs, err)
	}
}

func TestNetworkAndTLSClassification(t *testing.T) {
	c, _ := New(Options{BaseURL: "http://127.0.0.1:1"})
	if _, err := c.Ping(context.Background()); !errors.Is(err, ErrNetwork) {
		t.Fatalf("want ErrNetwork, got %v", err)
	}
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()
	strict, _ := New(Options{BaseURL: tlsSrv.URL})
	if _, err := strict.Ping(context.Background()); !errors.Is(err, ErrTLS) {
		t.Fatalf("want ErrTLS, got %v", err)
	}
}

func TestJobPath(t *testing.T) {
	if got := jobPath("a/b c"); got != "/job/a/job/b%20c" {
		t.Fatal(got)
	}
}

func TestJobParameters(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("team/all").Params(
		jenkinstest.StringParam("NAME", "who", "bob"),
		jenkinstest.BoolParam("DRY", "dry run", true),
		jenkinstest.ChoiceParam("ENV", "target", "dev", "stg", "prod"),
		jenkinstest.PasswordParam("SECRET", "shh"),
		jenkinstest.TextParam("NOTES", "multi", "a\nb"),
	)
	s.Job("team/none").LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})
	c, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "tok"})

	tests := []struct {
		name string
		job  string
		want []ParamDef
		err  bool
	}{
		{"all five types", "team/all", []ParamDef{
			{Name: "NAME", Type: "string", Description: "who", Default: "bob"},
			{Name: "DRY", Type: "bool", Description: "dry run", Default: "true"},
			{Name: "ENV", Type: "choice", Description: "target", Default: "dev", Choices: []string{"dev", "stg", "prod"}},
			{Name: "SECRET", Type: "password", Description: "shh"},
			{Name: "NOTES", Type: "text", Description: "multi", Default: "a\nb"},
		}, false},
		{"no parameters", "team/none", []ParamDef{}, false},
		{"not found", "team/missing", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.JobParameters(context.Background(), tc.job)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, wantErr %v", err, tc.err)
			}
			if tc.err {
				return
			}
			if got == nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
	bad, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "x"})
	if _, err := bad.JobParameters(context.Background(), "team/all"); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestListCatalogMultibranch(t *testing.T) {
	s := jenkinstest.New(t)
	s.Job("team/plain").LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})
	mb := s.Multibranch("team/app")
	mb.Branch("main").LastBuild(jenkinstest.Build{Number: 1, Result: "SUCCESS"})
	mb.Branch("feature/x y").LastBuild(jenkinstest.Build{Number: 2, Result: "FAILURE"})
	s.Multibranch("team/empty")
	c, _ := New(Options{BaseURL: s.URL, Username: "me", Token: "tok"})

	cat, err := c.ListCatalog(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Job{
		{FullName: "team/app/feature%2Fx%20y", Color: "red", Multibranch: "team/app", Branch: "feature/x y"},
		{FullName: "team/app/main", Color: "blue", Multibranch: "team/app", Branch: "main"},
		{FullName: "team/plain", Color: "blue"},
	}
	if !reflect.DeepEqual(cat.Jobs, want) {
		t.Fatalf("jobs:\n got %+v\nwant %+v", cat.Jobs, want)
	}
	if !reflect.DeepEqual(cat.MultibranchProjects, []string{"team/app", "team/empty"}) {
		t.Fatalf("projects: %v", cat.MultibranchProjects)
	}
	jobs, err := c.ListJobs(context.Background(), "")
	if err != nil || len(jobs) != 3 {
		t.Fatalf("ListJobs: %v %v", jobs, err)
	}
	// An encoded branch FullName is addressable (no double encoding).
	if _, err := c.JobParameters(context.Background(), "team/app/feature%2Fx%20y"); err != nil {
		t.Fatalf("addressing encoded branch: %v", err)
	}
}
