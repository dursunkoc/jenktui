package main

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func TestPresetSavedAndAppliedOnNextTrigger(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")

	// No presets file yet: no step, straight to the form.
	h.key("t")
	h.expectTitle("Trigger")
	h.expectNot("No preset (start from the defaults)")
	fillDeployForm(h)
	h.expect("Ready to trigger 1 job(s)")
	h.key("s")
	h.expect("Preset name:")
	h.typeText("staging deploy")
	h.key("enter")
	h.expect("Saved preset staging deploy for team/api/deploy")

	saved := readFile(t, h.path("presets.toml"))
	if !strings.Contains(saved, `ENV = 'prod'`) && !strings.Contains(saved, `ENV = "prod"`) {
		t.Fatalf("presets.toml lacks ENV:\n%s", saved)
	}
	if strings.Contains(saved, "API_KEY") || strings.Contains(saved, secretValue) {
		t.Fatalf("presets.toml holds the password:\n%s", saved)
	}

	h.key("y")
	h.expectTitle("Run Monitor: Ops")
	h.key("esc")
	h.expectTitle("Collections")

	// Second trigger: the step lists the preset; choosing it goes to the summary.
	h.key("t")
	h.expect("Preset", "staging deploy")
	h.key("down", "enter")
	h.expect("Ready to trigger 1 job(s)", "  ENV = prod", "Passwords are not saved in presets")
	h.expectNot(secretValue)
	h.key("y")

	var posts []url.Values
	for _, tr := range f.Triggers() {
		if tr.Job == "team/api/deploy" {
			posts = append(posts, tr.Params)
		}
	}
	if len(posts) != 2 {
		t.Fatalf("%d builds posted, want 2", len(posts))
	}
	want := vals("ENV", "prod", "DRY_RUN", "true", "REGION", "us", "API_KEY", "", "NOTES", "line-2")
	if !reflect.DeepEqual(posts[1], want) {
		t.Fatalf("params = %v, want %v", posts[1], want)
	}
	after := readFile(t, h.path("presets.toml"))
	if strings.Contains(after, "API_KEY") || strings.Contains(after, secretValue) {
		t.Fatalf("presets.toml holds the password:\n%s", after)
	}
}

func TestPresetOnOneOfSeveralJobsKeepsTheOthersDefaults(t *testing.T) {
	f := newFixture(t)
	f.Job("team/api/build").Params(jenkinstest.StringParam("A_ONLY", "", "a"))
	f.Job("team/web/build").Params(jenkinstest.StringParam("B_ONLY", "", "b"))
	h := newApp(t, f, withCollections(collectionsTOML("Mix", "team/api/build", "team/web/build")))
	writeFile(t, h.path("presets.toml"), "[[preset]]\njob = \"team/api/build\"\nname = \"nightly\"\n\n[preset.values]\nA_ONLY = \"from-preset\"\n")
	h.loginOK()
	h.openCollections()
	h.openCollection("Mix")
	h.key("left", "space", "t")
	h.expectTitle("Trigger")
	h.expect("Preset", "nightly", "1 of 2 jobs")
	h.key("down", "enter")
	h.expect("Ready to trigger 2 job(s)", "  A_ONLY = from-preset", "  B_ONLY = b")
	h.key("y")
	if a := triggerFor(t, f, "team/api/build"); a.Params.Get("A_ONLY") != "from-preset" {
		t.Fatalf("api trigger = %+v", a)
	}
	if w := triggerFor(t, f, "team/web/build"); w.Params.Get("B_ONLY") != "b" {
		t.Fatalf("web trigger = %+v", w)
	}
}
