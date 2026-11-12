package jenkins

import "testing"

func TestJobAndBuildURL(t *testing.T) {
	cases := []struct {
		base, job string
		num       int
		jobURL    string
		buildURL  string
	}{
		{"https://ci.example", "team/app build", 3, "https://ci.example/job/team/job/app%20build/", "https://ci.example/job/team/job/app%20build/3/"},
		{"https://ci.example", "team/app/feature%2Fx", 12, "https://ci.example/job/team/job/app/job/feature%2Fx/", "https://ci.example/job/team/job/app/job/feature%2Fx/12/"},
		{"https://h/jenkins", "a/b", 1, "https://h/jenkins/job/a/job/b/", "https://h/jenkins/job/a/job/b/1/"},
		{"https://h/jenkins/", "a", 5, "https://h/jenkins/job/a/", "https://h/jenkins/job/a/5/"}, // trailing slash trimmed by New
	}
	for _, tc := range cases {
		c, err := New(Options{BaseURL: tc.base})
		if err != nil {
			t.Fatal(err)
		}
		if got := c.JobURL(tc.job); got != tc.jobURL {
			t.Errorf("JobURL(%q, %q) = %q, want %q", tc.base, tc.job, got, tc.jobURL)
		}
		if got := c.BuildURL(tc.job, tc.num); got != tc.buildURL {
			t.Errorf("BuildURL(%q, %q, %d) = %q, want %q", tc.base, tc.job, tc.num, got, tc.buildURL)
		}
	}
}
