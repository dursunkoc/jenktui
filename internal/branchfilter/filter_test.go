package branchfilter

import (
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pat, name string
		want      bool
	}{
		{"main", "main", true},
		{"main", "Main", false}, // case-sensitive
		{"main", "mainx", false},
		{"main", "xmain", false},
		{"*", "", true},
		{"*", "anything/at/all", true},
		{"release/*", "release/1.2", true},
		{"release/*", "release/1.2/hotfix", true}, // * crosses '/'
		{"release/*", "release/", true},
		{"release/*", "release", false},
		{"release/*", "xrelease/1", false},
		{"feature/*-wip", "feature/a/b-wip", true},
		{"feature/*-wip", "feature/a-wip-no", false},
		{"*-wip", "a-wip", true},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "abc", true},
		{"a*b*c", "acb", false},
		{"a**b", "ab", true},
		{"v?.0", "v1.0", true},
		{"v?.0", "v10.0", false},
		{"v?.0", "v/.0", true},
		{"v[0-9].0", "v7.0", true},
		{"v[0-9].0", "va.0", false},
		{"v[!0-9].0", "va.0", true},
		{"v[^0-9].0", "v1.0", false},
		{"[abc]x", "bx", true},
		{"[]a]x", "]x", true},
		{"a\\*b", "a*b", true},
		{"a\\*b", "aXb", false},
		{"héllo*", "héllo/wörld", true},
		{"?", "é", true},
		{"", "x", false},     // malformed never matches
		{"[abc", "a", false}, // malformed never matches
	}
	for _, tc := range tests {
		if got := MatchPattern(tc.pat, tc.name); got != tc.want {
			t.Errorf("MatchPattern(%q, %q) = %v, want %v", tc.pat, tc.name, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, p := range []string{"main", "release/*", "v[0-9]?", "a\\*", "[!a]", "[a-c-]", "*"} {
		if err := Validate(p); err != nil {
			t.Errorf("Validate(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"", "[abc", "[", "[!", "abc\\", "[z-a]", "[a-"} {
		if err := Validate(p); err == nil {
			t.Errorf("Validate(%q) should fail", p)
		}
	}
}

func TestFilterMatch(t *testing.T) {
	pats := []string{"main", "release/*"}
	tests := []struct {
		name string
		f    Filter
		in   string
		want bool
	}{
		{"on exact", Filter{pats, true}, "main", true},
		{"on glob", Filter{pats, true}, "release/1.0", true},
		{"on miss", Filter{pats, true}, "feature/x", false},
		{"off shows all", Filter{pats, false}, "feature/x", true},
		{"on no patterns shows all", Filter{nil, true}, "feature/x", true},
		{"on empty slice shows all", Filter{[]string{}, true}, "feature/x", true},
	}
	for _, tc := range tests {
		if got := tc.f.Match(tc.in); got != tc.want {
			t.Errorf("%s: Match(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func mb(project, branch string) jenkins.Job {
	return jenkins.Job{FullName: project + "/" + branch, Multibranch: project, Branch: branch}
}

func keys(items []Item) []string {
	var out []string
	for _, i := range items {
		out = append(out, i.Key())
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestApply(t *testing.T) {
	jobs := []jenkins.Job{
		{FullName: "a-plain"}, // never filtered
		mb("app", "feature%2Fx"),
		mb("app", "main"),
		mb("lib", "dev"),
		{FullName: "z/main-like"}, // plain job inside a folder, never filtered
	}
	jobs[1].Branch = "feature/x"
	projects := []string{"app", "empty", "lib"}

	f := Filter{Patterns: []string{"main"}, Enabled: true}
	got := f.Apply(jobs, projects)
	want := []string{"a-plain", "app/main", "empty", "lib", "z/main-like"}
	if !eq(keys(got), want) {
		t.Fatalf("keys = %v, want %v", keys(got), want)
	}
	for _, it := range got {
		switch it.Key() {
		case "lib", "empty":
			if !it.Placeholder || it.Label != it.Project+" (no matching branches)" {
				t.Errorf("%s: placeholder = %+v", it.Key(), it)
			}
		default:
			if it.Placeholder {
				t.Errorf("%s should not be a placeholder", it.Key())
			}
		}
	}

	// Filter off: every branch shown; only truly empty projects get a placeholder.
	off := Filter{Patterns: []string{"main"}}
	got = off.Apply(jobs, projects)
	want = []string{"a-plain", "app/feature%2Fx", "app/main", "empty", "lib/dev", "z/main-like"}
	if !eq(keys(got), want) {
		t.Fatalf("off keys = %v, want %v", keys(got), want)
	}
	if got[3].Label != "empty (no branches)" {
		t.Errorf("label = %q", got[3].Label)
	}

	// Enabled but no patterns: show all.
	if got := (Filter{Enabled: true}).Apply(jobs, projects); len(got) != 6 {
		t.Errorf("empty-pattern filter hid rows: %v", keys(got))
	}

	// Glob against decoded name.
	g := Filter{Patterns: []string{"feature/*"}, Enabled: true}.Apply(jobs, []string{"app"})
	if !eq(keys(g), []string{"a-plain", "app/feature%2Fx", "z/main-like"}) {
		t.Errorf("decoded glob keys = %v", keys(g))
	}
}
