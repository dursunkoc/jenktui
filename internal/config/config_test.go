package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSaveLoadRoundTripAndNormalise(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	in := &Config{JenkinsURL: "https://j.example.com/", ParentJob: "/team/app/", Username: "me"}
	if err := Save(p, in); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{JenkinsURL: "https://j.example.com", ParentJob: "team/app", Username: "me"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestValidateRejectsBadURL(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "not a url", "https://"} {
		c := &Config{JenkinsURL: u}
		if c.Validate() == nil {
			t.Errorf("expected error for %q", u)
		}
	}
}

func TestCollectionsLifecycle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "collections.toml")
	cs, err := LoadCollections(p)
	if err != nil || len(cs.Items) != 0 {
		t.Fatalf("empty load: %v %v", cs, err)
	}
	if err := cs.Add("Release", []string{"a/x", "a/y", "b/z"}); err != nil {
		t.Fatal(err)
	}
	if cs.Add("release", nil) == nil {
		t.Fatal("duplicate name (case-insensitive) should fail")
	}
	if cs.Add("  ", nil) == nil {
		t.Fatal("empty name should fail")
	}
	if err := cs.MoveJob("Release", 2, 0); err != nil {
		t.Fatal(err)
	}
	if want := []string{"b/z", "a/x", "a/y"}; !reflect.DeepEqual(cs.Items[0].Jobs, want) {
		t.Fatalf("after move: %v", cs.Items[0].Jobs)
	}
	if err := cs.SetJobs("Release", []string{"a/x", "a/x", "c/q"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a/x", "c/q"}; !reflect.DeepEqual(cs.Items[0].Jobs, want) {
		t.Fatalf("after SetJobs: %v", cs.Items[0].Jobs)
	}
	if err := cs.Rename("Release", "Nightly"); err != nil {
		t.Fatal(err)
	}
	if err := SaveCollections(p, cs); err != nil {
		t.Fatal(err)
	}
	back, err := LoadCollections(p)
	if err != nil || !reflect.DeepEqual(back, cs) {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if err := back.Delete("Nightly"); err != nil || len(back.Items) != 0 {
		t.Fatalf("delete: %v %v", back, err)
	}
}

func TestBranchFilterRoundTripAndValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	in := &Config{JenkinsURL: "https://j.example.com", BranchFilter: []string{"main", "release/*"}}
	if err := Save(p, in); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.BranchFilter, []string{"main", "release/*"}) {
		t.Fatalf("got %v", got.BranchFilter)
	}
	// omitted when empty
	if err := Save(p, &Config{JenkinsURL: "https://j.example.com"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "branch_filter") {
		t.Fatalf("empty filter should be omitted:\n%s", b)
	}
	for _, bad := range [][]string{{""}, {"main", ""}, {"[abc"}, {"x\\"}} {
		c := &Config{JenkinsURL: "https://j.example.com", BranchFilter: bad}
		if c.Validate() == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
