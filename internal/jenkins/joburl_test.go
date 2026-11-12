package jenkins

import "testing"

func TestJobURL(t *testing.T) {
	c := &Client{base: "https://ci.example"}
	if got := c.JobURL("team/app build"); got != "https://ci.example/job/team/job/app%20build" {
		t.Fatalf("got %q", got)
	}
}
