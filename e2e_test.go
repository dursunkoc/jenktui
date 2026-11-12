package main

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

// testToken is distinctive on purpose: it must never be a substring of any
// ordinary UI text, so "token never appears" assertions mean something.
const testToken = "tkn-7f3a9c-SECRET"

// secretValue is a password parameter value used to prove masking.
const secretValue = "hunter2-PWD-9z"

// ---- fixtures ----

func okBuild(n int) jenkinstest.Build {
	return jenkinstest.Build{Number: n, Result: "SUCCESS", Timestamp: now().Add(-time.Hour), Duration: 90 * time.Second}
}

func failBuild(n int) jenkinstest.Build {
	return jenkinstest.Build{Number: n, Result: "FAILURE", Timestamp: now().Add(-time.Hour), Duration: 30 * time.Second}
}

func runningBuild(n int) jenkinstest.Build {
	return jenkinstest.Build{Number: n, Building: true, Timestamp: now().Add(-time.Minute)}
}

// newFixture is a small Jenkins: folders, a pipeline and freestyle jobs in
// every state, and job names with a space, underscore and dash.
func newFixture(t *testing.T) *jenkinstest.Server {
	t.Helper()
	f := jenkinstest.New(t).SetAuth("me", testToken)
	f.Job("team/api/build").LastBuild(okBuild(12)).Pipeline(
		jenkinstest.Stage{Name: "Compile", Status: jenkinstest.StageSuccess},
		jenkinstest.Stage{Name: "Package", Status: jenkinstest.StageSuccess})
	f.Job("team/api/deploy").LastBuild(failBuild(7))
	f.Job("team/web/build").LastBuild(okBuild(31))
	f.Job("team/web/test").LastBuild(runningBuild(9))
	f.Job("tools/cleanup")
	f.Job("tools/clean_up").LastBuild(okBuild(2))
	f.Job("tools/clean-up").LastBuild(okBuild(3))
	f.Job("tools/data sync").LastBuild(failBuild(4))
	return f
}

// collectionsTOML renders a collections.toml for tests.
func collectionsTOML(name string, jobs ...string) string {
	var q []string
	for _, j := range jobs {
		q = append(q, quote(j))
	}
	return "[[collection]]\nname = " + quote(name) + "\njobs = [" + strings.Join(q, ", ") + "]\n"
}

func loadCollections(t *testing.T, h *harness) *config.Collections {
	t.Helper()
	cs, err := config.LoadCollections(h.path("collections.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func deadURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u := "http://" + l.Addr().String()
	_ = l.Close()
	return u
}

// ---- harness self-check ----

func TestHarnessDropsTimersAndFollowsBatches(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f)
	start := time.Now()
	h.run(tea.Tick(time.Hour, func(time.Time) tea.Msg { return nil }))
	h.run(tea.Batch(tea.Tick(time.Hour, func(time.Time) tea.Msg { return nil }), nil))
	h.run(tea.Sequence(func() tea.Msg { return nil }, tea.Every(time.Hour, func(time.Time) tea.Msg { return nil })))
	if time.Since(start) > time.Second {
		t.Fatalf("timers were not dropped (%s)", time.Since(start))
	}
	if h.timers != 3 {
		t.Fatalf("dropped %d timers, want 3", h.timers)
	}
	h.send(tea.QuitMsg{})
	if !h.quit {
		t.Fatal("quit not recorded")
	}
}

// ---- 1. first run ----

func TestFirstRunSetupLoginMenu(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withoutConfig())
	h.start()
	h.expect("checking configuration")
	h.skipSplash()
	h.expectTitle("Setup")
	h.expect("Welcome - first-run setup", "Jenkins URL", "Parent job (optional)")
	if _, err := os.Stat(h.path("config.toml")); !os.IsNotExist(err) {
		t.Fatalf("config.toml must not exist before setup: %v", err)
	}

	h.typeText(f.URL)
	h.key("enter") // to the parent job field
	h.typeText("team/")
	h.key("enter") // save and check reachability
	h.expectTitle("Login")

	// config.toml: written with 0600, holds the URL and the (normalised) parent.
	st, err := os.Stat(h.path("config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config.toml mode = %v, want 0600", st.Mode().Perm())
	}
	cfg, err := config.Load(h.path("config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JenkinsURL != f.URL || cfg.ParentJob != "team" {
		t.Fatalf("config = %+v", cfg)
	}
	raw := readFile(t, h.path("config.toml"))
	if contains(raw, "token", "password") || strings.Contains(raw, testToken) {
		t.Fatalf("config.toml holds credentials:\n%s", raw)
	}

	// login: the token is masked while typing and never shown afterwards.
	h.typeText(f.User())
	h.key("enter")
	h.typeText(testToken)
	h.expectNot(testToken)
	h.expect("••••")
	h.key("enter")
	h.expectTitle("jenktui")
	h.expectNot(testToken)
	h.expect("Signed in as Me Myself")

	// the menu has exactly three entries.
	var entries []string
	for _, l := range strings.Split(h.screenText(), "\n") {
		if i := strings.Index(l, "│"); i >= 0 {
			if c := strings.TrimSpace(strings.Trim(strings.TrimSpace(l[i:]), "│")); c != "" && !strings.HasPrefix(c, "Signed in") {
				entries = append(entries, c)
			}
		}
	}
	if want := []string{"> Collections", "Job Catalog", "Quit"}; !reflect.DeepEqual(entries, want) {
		t.Fatalf("menu entries = %q, want %q\n%s", entries, want, h.screenText())
	}

	// The configured parent job reaches the Job Catalog.
	h.openCatalog()
	h.expectTitle("Job Catalog · team")
	h.expect("[ ] api/build") // shown relative to the parent job
	h.expectNot("team/api/build", "tools/cleanup")
}

func TestSetupErrorsStayOnSetup(t *testing.T) {
	cases := []struct {
		name string
		url  func(t *testing.T) string
		want []string
	}{
		{"unreachable", deadURL, []string{"Cannot reach Jenkins", "Check jenkins_url in config.toml"}},
		{"not http", func(*testing.T) string { return "ftp://example.org" }, []string{"must be an http(s) URL"}},
		{"empty", func(*testing.T) string { return "" }, []string{"jenkins_url is required"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newApp(t, newFixture(t), withoutConfig())
			h.boot()
			h.expectTitle("Setup")
			h.typeText(tc.url(t))
			h.key("enter", "enter")
			h.expectTitle("Setup")
			h.expect(tc.want...)
		})
	}
}

func TestMenuQuit(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"q", []string{"q"}},
		{"entry", []string{"down", "down", "enter"}},
		{"ctrl+c", []string{"ctrl+c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newApp(t, newFixture(t))
			h.loginOK()
			h.key(tc.keys...)
			if !h.quit {
				t.Fatal("app did not quit")
			}
		})
	}
}

// ---- 2. login and connection errors ----

func TestLoginWrongTokenThenCorrect(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f)
	h.boot()
	h.submitLogin(f.User(), "not-the-token")
	h.expectTitle("Login")
	h.expect("Authentication failed", "Check your username and password or API token")
	h.expectNot("not-the-token")
	if h.sess.Client != nil {
		t.Fatal("a client was stored after a failed login")
	}
	// fix the token in place: the form is still usable.
	h.replaceField(testToken)
	h.key("enter")
	h.expectTitle("jenktui")
	if h.sess.Client == nil {
		t.Fatal("no client after a successful login")
	}
	h.expectNot("Authentication failed", testToken)
}

func TestLoginFieldValidation(t *testing.T) {
	h := newApp(t, newFixture(t))
	h.boot()
	h.key("enter", "enter") // empty username: first enter only moves to the token
	h.expect("username is required")
	h.typeText("me")
	h.key("enter", "enter") // empty token
	h.expect("password or API token is required")
	h.expectTitle("Login")
}

func TestUnreachableURLShowsHintAndRetry(t *testing.T) {
	f := newFixture(t)
	dead := deadURL(t)
	h := newApp(t, f, withConfigURL(dead))
	h.boot()
	h.expectTitle("jenktui")
	h.expect("Cannot reach Jenkins", "Check jenkins_url in config.toml", "Press r to retry")
	// still unreachable: retry shows the same hint and does not advance.
	h.key("r")
	h.skipSplash()
	h.expect("Cannot reach Jenkins")
	// q quits from the error screen.
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit")
	}
}

func TestInvalidConfigShowsError(t *testing.T) {
	h := newApp(t, newFixture(t), withConfigURL("ftp://nope"))
	h.boot()
	h.expect("Invalid configuration", "config.toml")
}

func TestTLSErrorAndFixes(t *testing.T) {
	cases := []struct {
		name  string
		extra func(f *jenkinstest.Server, dir string) string
		ok    bool
	}{
		{"untrusted certificate", func(*jenkinstest.Server, string) string { return "" }, false},
		{"ca_cert trusts it", func(f *jenkinstest.Server, dir string) string {
			p := filepath.Join(dir, "ca.pem")
			if err := os.WriteFile(p, f.CACertPEM(), 0o600); err != nil {
				t.Fatal(err)
			}
			return "ca_cert = " + quote(p)
		}, true},
		{"insecure_skip_verify", func(*jenkinstest.Server, string) string { return "insecure_skip_verify = true" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := jenkinstest.NewTLS(t)
			caDir := t.TempDir()
			h := newApp(t, f, withConfigExtra(tc.extra(f, caDir)))
			h.boot()
			if tc.ok {
				h.expectTitle("Login")
				h.submitLogin(f.User(), f.Token())
				h.expectTitle("jenktui")
				return
			}
			h.expect("TLS error", "The certificate is not trusted", "ca_cert", "insecure_skip_verify")
		})
	}
}

// ---- 3. Job Catalog ----

func TestCatalogListsJobsWithBadges(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f)
	h.loginOK()
	h.openCatalog()
	h.expectTitle("Job Catalog")
	h.expect("8 jobs", "branches: all")
	for job, badge := range map[string]string{
		"team/api/build":  "SUCCESS",
		"team/api/deploy": "FAILED",
		"team/web/build":  "SUCCESS",
		"team/web/test":   "RUNNING",
		"tools/cleanup":   "UNKNOWN",
		"tools/data sync": "FAILED",
	} {
		h.expectRow(job, badge)
	}
	// folders are not Jobs.
	for _, l := range strings.Split(h.screenText(), "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), " team") || strings.Contains(l, "[ ] team/api ") {
			t.Fatalf("a folder is listed: %q", l)
		}
	}
	h.key("esc")
	h.expectTitle("jenktui")
}

func TestCatalogTextFilter(t *testing.T) {
	cases := []struct {
		name, filter string
		count        string
		want, not    []string
	}{
		{"substring", "api", "2 of 8 jobs", []string{"team/api/build", "team/api/deploy"}, []string{"team/web/test"}},
		{"case insensitive", "TEAM/API", "2 of 8 jobs", []string{"team/api/build"}, []string{"tools/cleanup"}},
		{"star", "web*test", "1 of 8 jobs", []string{"team/web/test"}, []string{"team/web/build"}},
		{"underscore is one char", "clean_up", "2 of 8 jobs", []string{"tools/clean-up", "tools/clean_up"}, []string{"tools/cleanup"}},
		{"escaped underscore is literal", `clean\_up`, "1 of 8 jobs", []string{"tools/clean_up"}, []string{"tools/clean-up", "tools/cleanup"}},
		{"escaped star is literal", `\*`, "0 of 8 jobs", []string{"no matches"}, []string{"team/api/build"}},
		{"space is literal", "data sync", "1 of 8 jobs", []string{"tools/data sync"}, []string{"tools/cleanup"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newApp(t, newFixture(t))
			h.loginOK()
			h.openCatalog()
			h.key("/")
			h.typeText(tc.filter)
			h.expect(append(tc.want, tc.count, "↓ to list")...)
			h.expectNot(tc.not...)
			// esc clears the filter and leaves the box.
			h.key("esc")
			h.expect("8 jobs")
			h.expectNot("of 8 jobs")
		})
	}
}

func TestCatalogStatusFilterCycle(t *testing.T) {
	h := newApp(t, newFixture(t))
	h.loginOK()
	h.openCatalog()
	// failed -> running -> success -> unknown -> off (statuses present, in order).
	steps := []struct {
		label     string
		want, not []string
	}{
		{"status: failed", []string{"team/api/deploy", "tools/data sync"}, []string{"team/api/build", "team/web/test", "tools/cleanup"}},
		{"status: running", []string{"team/web/test"}, []string{"team/api/deploy", "team/api/build"}},
		{"status: success", []string{"team/api/build", "team/web/build", "tools/clean-up"}, []string{"team/api/deploy", "team/web/test"}},
		{"status: unknown", []string{"tools/cleanup"}, []string{"team/api/build", "team/api/deploy"}},
	}
	for _, s := range steps {
		h.key("s")
		h.expect(append(s.want, s.label)...)
		h.expectNot(s.not...)
	}
	h.key("s")
	h.expectNot("status:")
	h.expect("team/api/build", "team/api/deploy", "tools/cleanup")

	// the status filter combines with the text filter.
	h.key("s") // failed
	h.key("/")
	h.typeText("api")
	h.expect("team/api/deploy", "status: failed")
	h.expectNot("tools/data sync", "team/api/build")
}

func TestCatalogFilterBoxAndListFocus(t *testing.T) {
	h := newApp(t, newFixture(t))
	h.loginOK()
	h.openCatalog()
	h.key("/")
	h.typeText("data sync") // the space is part of the filter
	h.expect("1 of 8 jobs", "tools/data sync", "↓ to list")
	h.expectNot("1 selected")

	// space in the box is text, so nothing is selected yet.
	h.key("down") // focus moves to the list
	h.expect("↑ to edit")
	h.key("space") // now space selects
	h.expect("1 selected", "[x] tools/data sync")
	if h.hasLine("↓ to list") {
		t.Fatal("filter box still focused after down")
	}

	// up on the first row returns to the box; space is typed again.
	h.key("up")
	h.expect("↓ to list")
	h.typeText(" x")
	h.expect("no matches")
	h.key("backspace", "backspace")
	h.expect("tools/data sync", "1 selected")

	// typing letters that are shortcuts is text while the box has focus.
	h.key("esc")
	h.key("/")
	h.typeText("s")
	h.expectNot("status:")
	h.expect("of 8 jobs")
}

// The help bar shows a few keys and "? all keys"; ? opens a panel with every
// key, grouped. While a text field has focus ? is typed instead.
func TestHelpBarAndPanel(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/web/test", "team/api/build")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	bar := func() string {
		lines := strings.Split(h.screenText(), "\n")
		return lines[len(lines)-1]
	}
	if got := bar(); !strings.HasPrefix(got, "t trigger") || !strings.HasSuffix(got, "? all keys") || strings.Contains(got, "x remove missing") {
		t.Fatalf("help bar = %q", got)
	}

	h.key("?")
	h.expect("Navigation", "Selection", "Filter", "Actions", "General", "x remove missing", "R refresh", "shift+↑", "esc/? close help")
	h.expect("team/web/test") // the Collection stays visible above the panel
	h.key("esc")              // closes the panel, does not leave the Collection
	h.expectTitle("Collection · Ops")
	h.expectNot("x remove missing")

	// in the filter box ? is text and the bar shows the box's keys.
	h.key("/")
	if got := bar(); got != "enter/↓ go to list · esc clear filter" {
		t.Fatalf("filter help bar = %q", got)
	}
	h.typeText("?")
	h.expectNot("Actions")
	h.expect("no matches")
	h.key("esc")

	// the bar never runs past the terminal edge (it used to overflow and wrap).
	for _, w := range []int{60, 80, 100, 120} {
		h.send(tea.WindowSizeMsg{Width: w, Height: 24})
		if got := bar(); ansi.StringWidth(got) > w || !strings.HasSuffix(got, "? all keys") {
			t.Fatalf("width %d: help bar %q (%d wide)", w, got, ansi.StringWidth(got))
		}
	}
}

// The count line sits on the title row, the filter hint has a blank line
// above and below it, then come the column titles.
func TestCatalogHeaderLayout(t *testing.T) {
	h := newApp(t, newFixture(t))
	h.loginOK()
	h.openCatalog()
	rows := strings.Split(h.screenText(), "\n")
	if !strings.HasPrefix(rows[0], h.title()) || !strings.HasSuffix(rows[0], "8 jobs · branches: all") {
		t.Fatalf("title row = %q", rows[0])
	}
	if rows[1] != "" || rows[2] != "press / to filter, s for status" || rows[3] != "" || strings.Fields(rows[4])[0] != "Pipeline" || !strings.Contains(rows[5], "[ ] team › api › build ") {
		t.Fatalf("body rows:\n%s", strings.Join(rows[:6], "\n"))
	}
	h.key("/")
	h.typeText("web")
	if rows = strings.Split(h.screenText(), "\n"); !strings.HasSuffix(rows[0], "2 of 8 jobs · branches: all") {
		t.Fatalf("filtered title row = %q", rows[0])
	}
}

// ---- 4. Collections lifecycle ----

func TestCollectionsLifecycle(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f)
	h.loginOK()
	h.openCollections()
	h.expect("No collections yet")

	// n: new collection, pick jobs.
	h.key("n")
	h.expect("New collection")
	h.typeText("Release")
	h.key("enter")
	h.expectTitle("New Release")
	h.expect("8 jobs", "0 selected")
	h.key("/")
	h.typeText("team/web")
	h.key("enter") // leave the box, filter stays
	h.expect("team/web/build", "team/web/test")
	h.expectNot("team/api/build")
	h.key("space") // team/web/build
	h.expect("1 selected")
	h.key("down", "space") // team/web/test
	h.expect("2 selected", "[x] team/web/build", "[x] team/web/test")
	h.key("esc") // leaving the picker without enter saves nothing
	h.expectTitle("Collections")
	h.expect("No collections yet")
	if got := loadCollections(t, h).Items; len(got) != 0 {
		t.Fatalf("esc must not save: %+v", got)
	}

	// again, and this time confirm with enter.
	h.key("n")
	h.typeText("Release")
	h.key("enter")
	h.key("/")
	h.typeText("team/web")
	h.key("enter", "space", "down", "space")
	h.key("enter")
	h.expectTitle("Collections")
	h.expect("Release", "2 jobs")
	cs := loadCollections(t, h)
	if len(cs.Items) != 1 || cs.Items[0].Name != "Release" ||
		!reflect.DeepEqual(cs.Items[0].Jobs, []string{"team/web/build", "team/web/test"}) {
		t.Fatalf("collections.toml = %+v", cs)
	}
	if st, err := os.Stat(h.path("collections.toml")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("collections.toml: %v %v", st, err)
	}
	// duplicate names (case-insensitive) are refused.
	h.key("n")
	h.typeText("release")
	h.key("enter")
	h.expect(`collection "release" already exists`)
	h.key("esc")

	// open it: jobs and badges in order.
	h.openCollection("Release")
	h.expect("2 jobs")
	b, tst := strings.Index(h.screenText(), "team › web › build"), strings.Index(h.screenText(), "team › web › test")
	if b < 0 || tst < 0 || b > tst {
		t.Fatalf("members out of order:\n%s", h.screenText())
	}
	h.expectRow("team/web/build", "SUCCESS")
	h.expectRow("team/web/test", "RUNNING")

	// shift+down reorders and persists.
	h.key("shift+down")
	cs = loadCollections(t, h)
	if want := []string{"team/web/test", "team/web/build"}; !reflect.DeepEqual(cs.Items[0].Jobs, want) {
		t.Fatalf("after reorder jobs = %v, want %v", cs.Items[0].Jobs, want)
	}
	h.key("shift+up")
	if want := []string{"team/web/build", "team/web/test"}; !reflect.DeepEqual(loadCollections(t, h).Items[0].Jobs, want) {
		t.Fatalf("reorder back: %v", loadCollections(t, h).Items[0].Jobs)
	}
	h.key("shift+down")
	h.key("esc")
	h.expectTitle("Collections")

	// rename.
	h.key("r")
	h.expect("Rename collection")
	h.replaceField("Release 2")
	h.key("enter")
	h.expect("Release 2", "2 jobs")
	cs = loadCollections(t, h)
	if cs.Items[0].Name != "Release 2" || cs.Items[0].Jobs[0] != "team/web/test" {
		t.Fatalf("after rename: %+v", cs.Items)
	}

	// delete asks first: n keeps it, y deletes.
	h.key("d")
	h.expect(`Delete "Release 2"?`)
	h.key("n")
	h.expect("Release 2")
	if len(loadCollections(t, h).Items) != 1 {
		t.Fatal("n deleted the collection")
	}
	h.key("d", "y")
	h.expect("No collections yet", `deleted "Release 2"`)
	if len(loadCollections(t, h).Items) != 0 {
		t.Fatal("collection still on disk")
	}
}

// The Collections list starts one blank line under the title.
func TestCollectionsListBlankLineUnderTitle(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/web/test", "team/api/build")))
	h.loginOK()
	h.openCollections()
	rows := strings.Split(h.screenText(), "\n")
	if rows[0] != "Collections" || rows[1] != "" || !strings.HasPrefix(rows[2], "> Ops") {
		t.Fatalf("collections rows:\n%s", strings.Join(rows[:4], "\n"))
	}
}

// With a parent job set, job names are shown relative to it in a Collection,
// the member picker and the Run Monitor; a member outside the parent keeps
// its full name, and the filter still matches the full name.
func TestJobNamesRelativeToParent(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withConfigExtra(`parent_job = "team"`),
		withCollections(collectionsTOML("Ops", "team/web/test", "team/api/build", "tools/cleanup")))
	h.loginOK()
	h.openCollections()
	h.expectTitle("Collections · team")
	h.openCollection("Ops")
	h.expectTitle("Collection · team · Ops")
	h.expect("[ ] web/test", "[ ] api/build", "tools/cleanup")
	h.expectNot("team/web/test", "team/api/build")

	h.key("/")
	h.typeText("team/web") // matches the full name, though "team/" is not shown
	h.expect("[ ] web/test")
	h.expectNot("api/build")
	h.key("esc")

	h.key("m")
	h.expectTitle("Run Monitor · team · Ops")
	h.key("s") // show successful jobs too
	h.expect("web/test", "api/build")
	h.expectNot("team/web/test", "team/api/build")
	h.key("esc")

	h.key("e")
	h.expectTitle("Edit · team · Ops")
	h.expect("[ ] api/deploy", "[x] web/test")
	h.expectNot("team/")
}

func TestCollectionEditMembers(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/web/test", "team/api/build")))
	h.loginOK()
	h.openCollections()
	h.key("e") // edit members from the list
	h.expectTitle("Edit Ops")
	h.expect("2 selected")
	h.key("/")
	h.typeText("team/api/build")
	h.key("enter", "space") // unselect team/api/build
	h.key("enter")
	h.expectTitle("Collections")
	if want := []string{"team/web/test"}; !reflect.DeepEqual(loadCollections(t, h).Items[0].Jobs, want) {
		t.Fatalf("jobs = %v", loadCollections(t, h).Items[0].Jobs)
	}
	// edit again from inside the opened Collection: newly checked jobs follow alphabetically.
	h.openCollection("Ops")
	h.key("e")
	h.expectTitle("Edit Ops")
	h.key("/")
	h.typeText("tools/clean")
	h.key("enter", "space", "down", "space")
	h.key("enter")
	h.expectTitle("Collection · Ops")
	want := []string{"team/web/test", "tools/clean-up", "tools/clean_up"}
	if got := loadCollections(t, h).Items[0].Jobs; !reflect.DeepEqual(got, want) {
		t.Fatalf("jobs = %v, want %v", got, want)
	}
	h.expect("3 jobs", "tools/clean-up", "tools/clean_up")
}

func TestMissingMemberBadgeSkippedByTriggerAndRemovable(t *testing.T) {
	f := newFixture(t)
	toml := collectionsTOML("Ops", "team/gone/job", "team/api/build", "team/web/test")
	h := newApp(t, f, withCollections(toml))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.expect("3 jobs", "1 missing")
	h.expectRow("team/gone/job", "MISSING")
	h.expectRow("team/api/build", "SUCCESS")

	// Only the missing job under the cursor: nothing to trigger.
	h.key("t")
	h.expectTitle("Collection · Ops")
	h.expect("nothing to trigger", "team/gone/job")

	// Select a real job and trigger: the missing one is skipped, with a notice.
	h.key("down", "space", "t")
	h.expectTitle("Trigger")
	h.expect("Trigger team/api/build?")
	h.key("n") // cancel
	h.expectTitle("Collection · Ops")
	h.expect("skipped missing: team/gone/job")
	if n := len(f.Triggers()); n != 0 {
		t.Fatalf("cancelled trigger posted %d builds", n)
	}

	// Select everything that can be selected, trigger: the missing job never reaches Jenkins.
	h.key("a", "t")
	h.expectTitle("Trigger")
	h.key("n")
	h.expectTitle("Collection · Ops")
	if n := len(f.Triggers()); n != 0 {
		t.Fatalf("triggers = %d", n)
	}

	// x on a non-missing row refuses, x on the missing row removes it.
	h.key("x")
	h.expect("x removes a missing job")
	h.key("up", "x")
	h.expect("removed team/gone/job", "2 jobs")
	h.expectNot("MISSING", "1 missing")
	if want := []string{"team/api/build", "team/web/test"}; !reflect.DeepEqual(loadCollections(t, h).Items[0].Jobs, want) {
		t.Fatalf("jobs = %v", loadCollections(t, h).Items[0].Jobs)
	}
}

func TestMissingMemberShownInMonitor(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/gone/job", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.key("m")
	h.expectTitle("Run Monitor: Ops")
	h.expectRow("team/gone/job", "MISSING", "no longer in the catalog")
	h.expectRow("team/api/deploy", "FAILED")
}

// ---- 5. Branch Filter ----

// newBranchFixture adds Multibranch Projects to the standard fixture.
func newBranchFixture(t *testing.T) *jenkinstest.Server {
	t.Helper()
	f := newFixture(t)
	proj := f.Multibranch("proj")
	for _, b := range []string{"main", "develop", "feature/x", "release/1.0"} {
		proj.Branch(b).LastBuild(okBuild(1))
	}
	f.Multibranch("legacy").Branch("old").LastBuild(okBuild(1))
	f.Multibranch("empty")
	return f
}

const branchFilterConfig = `branch_filter = ["main", "feature/*"]`

// A branch of a Multibranch Project is shown with the project and the decoded
// branch in their own columns in a Collection, and as "project › branch" in
// its Run Monitor, opened with m.
func TestBranchNamesInCollectionAndMonitor(t *testing.T) {
	f := newBranchFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Branches", "proj/main", "proj/feature%2Fx")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Branches")
	h.expectBranch("proj", "main", "proj", "feature/x")
	h.expectNot("proj/main", "feature%2Fx")
	h.key("m")
	h.expectTitle("Run Monitor: Branches")
	h.key("s") // the builds succeeded: show them
	h.expectRow("proj › main", "SUCCESS")
	h.expectRow("proj › feature/x", "SUCCESS")
	h.expectNot("feature%2Fx")
}

func TestBranchFilterCatalog(t *testing.T) {
	f := newBranchFixture(t)
	h := newApp(t, f, withConfigExtra(branchFilterConfig))
	h.loginOK()
	h.openCatalog()
	h.expect("branches: filtered (2 patterns)")
	// only matching branches, names stay URL-encoded in keys; other jobs untouched.
	h.expect("team/api/build", "tools/cleanup")
	h.expectBranch("proj", "main", "proj", "feature/x")
	h.expectNoBranch("proj", "develop", "proj", "release/1.0", "legacy", "old")
	// projects with no matching branch get a placeholder row; the project itself is never a Job.
	h.expectBranch("legacy", "(no matching branches)", "empty", "(no matching branches)")
	if regexp.MustCompile(`\[ \] proj\s+●`).MatchString(h.screenText()) { // a row with no branch
		t.Fatal("the Multibranch Project itself is listed as a job")
	}
	// placeholders cannot be selected.
	h.key("/")
	h.typeText("legacy")
	h.key("enter", "space")
	h.expectNot("1 selected")
	h.key("/", "esc") // esc in the filter box clears it
	h.expectTitle("Job Catalog")

	// b switches the filter off: every branch, "(no branches)" only for the empty project.
	h.key("b")
	h.expect("branches: all")
	h.expectBranch("proj", "develop", "proj", "release/1.0", "legacy", "old", "empty", "(no branches)")
	h.expectNot("(no matching branches)")
	// b again turns it back on.
	h.key("b")
	h.expect("branches: filtered (2 patterns)")
	h.expectNoBranch("proj", "develop")
}

// When parent_job is itself a Multibranch Project its branches are listed as
// branches, the Branch Filter applies, and the project shows by its own name.
func TestMultibranchParentJob(t *testing.T) {
	f := newFixture(t)
	mb := f.Multibranch("team/app")
	for _, b := range []string{"main", "feature/x"} {
		mb.Branch(b).LastBuild(okBuild(1))
	}
	h := newApp(t, f, withConfigExtra("parent_job = \"team/app\"\nbranch_filter = [\"main\"]"),
		withCollections(collectionsTOML("Ops", "team/app/main")))
	h.loginOK()
	h.openCatalog()
	h.expectTitle("Job Catalog · team/app")
	h.expect("branches: filtered (1 pattern)")
	h.expectBranch("app", "main")
	h.expectNoBranch("app", "feature/x")
	h.key("esc", "up") // back to the menu, cursor on Collections again

	h.openCollections()
	h.openCollection("Ops")
	h.expectBranch("app", "main")
	h.key("m")
	h.key("s") // the build succeeded: show it
	h.expectRow("app › main", "SUCCESS")
	h.expectNot("team/app/main", "%2F")
}

func TestBranchFilterEditorSavesToConfig(t *testing.T) {
	f := newBranchFixture(t)
	h := newApp(t, f, withConfigExtra(branchFilterConfig))
	h.loginOK()
	h.openCatalog()

	h.key("B")
	h.expect("Branch Filter", "> main", "feature/*")
	// add a pattern; an empty one is refused with a message.
	h.key("a", "enter")
	h.expect("empty pattern")
	h.typeText("develop")
	h.key("enter")
	h.expect("develop")
	h.key("enter") // close: saves
	h.expectTitle("Job Catalog")
	h.expect("branches: filtered (3 patterns)")
	h.expectBranch("proj", "develop", "proj", "main", "proj", "feature/x")
	h.expectNoBranch("proj", "release/1.0")
	cfg, err := config.Load(h.path("config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"main", "feature/*", "develop"}; !reflect.DeepEqual(cfg.BranchFilter, want) {
		t.Fatalf("branch_filter = %v, want %v", cfg.BranchFilter, want)
	}
	if cfg.JenkinsURL != f.URL {
		t.Fatalf("saving the filter changed the config: %+v", cfg)
	}

	// delete the first pattern (main); esc closes and saves too.
	h.key("B", "d", "esc")
	h.expectNoBranch("proj", "main")
	h.expect("branches: filtered (2 patterns)")
	h.expectBranch("proj", "develop", "proj", "feature/x")
	cfg, _ = config.Load(h.path("config.toml"))
	if want := []string{"feature/*", "develop"}; !reflect.DeepEqual(cfg.BranchFilter, want) {
		t.Fatalf("branch_filter = %v, want %v", cfg.BranchFilter, want)
	}
	if st, _ := os.Stat(h.path("config.toml")); st.Mode().Perm() != 0o600 {
		t.Fatalf("config.toml mode = %v", st.Mode().Perm())
	}

	// deleting every pattern shows all branches and drops the key.
	h.key("B", "d", "d", "enter")
	h.expect("branches: all")
	h.expectBranch("proj", "main", "proj", "release/1.0")
	cfg, _ = config.Load(h.path("config.toml"))
	if len(cfg.BranchFilter) != 0 || strings.Contains(readFile(t, h.path("config.toml")), "branch_filter") {
		t.Fatalf("branch_filter not cleared: %q", readFile(t, h.path("config.toml")))
	}
}

func TestBranchFilterIsCaseSensitiveAndFromScratch(t *testing.T) {
	f := newBranchFixture(t)
	h := newApp(t, f) // no branch_filter in config
	h.loginOK()
	h.openCatalog()
	h.expect("branches: all")
	h.expectBranch("proj", "main", "proj", "develop", "proj", "release/1.0", "legacy", "old")
	h.key("B", "a")
	h.typeText("Main") // wrong case: matches nothing
	h.key("enter", "enter")
	h.expect("branches: filtered (1 pattern)")
	h.expectBranch("proj", "(no matching branches)")
	h.expectNoBranch("proj", "main")
	h.key("B", "d", "esc") // remove it again
	h.expect("branches: all")
	h.expectBranch("proj", "main")
}

func TestBranchFilterKeepsBranchesAlreadyInCollection(t *testing.T) {
	f := newBranchFixture(t)
	toml := collectionsTOML("Branches", "proj/release%2F1.0", "proj/main")
	h := newApp(t, f, withConfigExtra(branchFilterConfig), withCollections(toml))
	h.loginOK()
	h.openCollections()
	h.openCollection("Branches")
	// the filter hides release/1.0 when browsing, but the member stays and exists.
	h.expect("2 jobs")
	h.expectBranch("proj", "release/1.0", "proj", "main")
	h.expectNot("MISSING")

	// the picker applies the filter but does not drop hidden members on save.
	h.key("e")
	h.expectTitle("Edit Branches")
	h.expect("2 selected", "branches: filtered (2 patterns)")
	h.expectNoBranch("proj", "release/1.0", "proj", "develop")
	h.key("enter")
	h.expectTitle("Collection · Branches")
	if want := []string{"proj/release%2F1.0", "proj/main"}; !reflect.DeepEqual(loadCollections(t, h).Items[0].Jobs, want) {
		t.Fatalf("jobs = %v, want %v", loadCollections(t, h).Items[0].Jobs, want)
	}

	// adding a visible branch keeps the hidden one too; the key stays URL-encoded.
	h.key("e")
	h.key("/")
	h.typeText("feature")
	h.key("enter", "space")
	h.key("enter")
	want := []string{"proj/release%2F1.0", "proj/main", "proj/feature%2Fx"}
	if got := loadCollections(t, h).Items[0].Jobs; !reflect.DeepEqual(got, want) {
		t.Fatalf("jobs = %v, want %v", got, want)
	}
	// and the branch can be triggered through its encoded URL.
	h.key("down", "down", "t")
	h.expect("Trigger proj › feature/x?")
	h.key("y")
	tr := triggerFor(t, f, "proj/feature%2Fx")
	if tr.Endpoint != "build" {
		t.Fatalf("endpoint = %q", tr.Endpoint)
	}
	h.expectTitle("Run Monitor: Branches")
	h.expectRow("proj › feature/x", "QUEUED")
}

func TestBranchFilterEditedInPickerPersists(t *testing.T) {
	f := newBranchFixture(t)
	h := newApp(t, f, withConfigExtra(branchFilterConfig), withCollections(collectionsTOML("Ops", "team/api/build")))
	h.loginOK()
	h.openCollections()
	h.key("e")
	h.key("B", "a")
	h.typeText("release/*")
	h.key("enter", "enter")
	h.expectBranch("proj", "release/1.0")
	cfg, _ := config.Load(h.path("config.toml"))
	if want := []string{"main", "feature/*", "release/*"}; !reflect.DeepEqual(cfg.BranchFilter, want) {
		t.Fatalf("branch_filter = %v", cfg.BranchFilter)
	}
	h.key("esc")
	// a picker opened afterwards, and the catalog, see the saved patterns.
	h.key("e")
	h.expect("branches: filtered (3 patterns)")
	h.expectBranch("proj", "release/1.0")
	h.key("esc", "esc", "down")
	h.key("enter")
	h.expect("branches: filtered (3 patterns)")
}

// ---- 6. Trigger a single job without parameters ----

// The trigger screen shows job names like the lists: relative to the parent
// job, and a step starts one blank line under the title.
func TestTriggerShowsShortNames(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withConfigExtra(`parent_job = "team"`),
		withCollections(collectionsTOML("Ops", "team/api/build", "team/web/build")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("a", "t") // both jobs
	h.expectTitle("Trigger")
	rows := strings.Split(h.screenText(), "\n")
	if rows[1] != "" || rows[2] == "" {
		t.Fatalf("trigger step should start one blank line under the title:\n%s", strings.Join(rows[:4], "\n"))
	}
	for i := 0; i < 6 && !contains(h.screenText(), "Ready to trigger"); i++ {
		h.key("enter") // through any parameter forms, keeping the defaults
	}
	h.expect("Ready to trigger 2 job(s)", "api/build", "web/build")
	h.expectNot("team/")
}

func TestTriggerSingleJobNoParamsThroughMonitor(t *testing.T) {
	stages := func(second string) []jenkinstest.Stage {
		return []jenkinstest.Stage{
			{Name: "Compile", Status: jenkinstest.StageSuccess},
			{Name: "Package", Status: second},
			{Name: "Deploy", Status: jenkinstest.StageNotExecuted},
		}
	}
	for _, tc := range []struct {
		name     string
		job      string
		pipeline bool
		failure  bool
	}{
		{"pipeline fails", "team/api/build", true, true},
		{"pipeline succeeds", "team/api/build", true, false},
		{"freestyle fails", "team/web/build", false, true},
		{"freestyle succeeds", "team/web/build", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			h := newApp(t, f, withCollections(collectionsTOML("Ops", tc.job)))
			h.loginOK()
			h.openCollections()
			h.openCollection("Ops")

			h.key("t")
			h.expectTitle("Trigger")
			h.expect("Trigger "+tc.job+"?", "no parameters")
			if len(f.Triggers()) != 0 {
				t.Fatal("triggered before confirming")
			}
			h.key("y")

			// the fake saw exactly one POST to /build, without parameters.
			tr := triggerFor(t, f, tc.job)
			if tr.Endpoint != "build" || len(tr.Params) != 0 {
				t.Fatalf("trigger = %+v", tr)
			}
			// the monitor opens showing the new run, queued.
			h.expectTitle("Run Monitor: Ops")
			h.expectRow(tc.job, "QUEUED", "Waiting for next available executor")

			// it starts running as build 40.
			f.ResolveQueueItem(tr.QueueID, 40)
			job := f.Job(tc.job).LastBuild(jenkinstest.Build{Number: 40, Building: true, Timestamp: now()})
			if tc.pipeline {
				job.Pipeline(stages(jenkinstest.StageInProgress)...)
			}
			h.key("R")
			h.expectRow(tc.job, "RUNNING", "#40")
			h.expectNot("QUEUED")
			if tc.pipeline {
				h.expect("✔ Compile", "⟳ Package", "○ Deploy")
			} else {
				h.expectNot("Compile", "⟳")
			}

			// and finishes.
			if tc.failure {
				job = f.Job(tc.job).LastBuild(jenkinstest.Build{Number: 40, Result: "FAILURE", Timestamp: now(), Duration: 45 * time.Second})
				if tc.pipeline {
					job.Pipeline(stages(jenkinstest.StageFailed)...)
				}
				h.key("R")
				h.expectRow(tc.job, "FAILED", "#40", "45s")
				h.expect("1 remaining")
				h.expectNot("RUNNING")
				h.key("enter") // details
				if tc.pipeline {
					h.expect("✘ Package", "✔ Compile")
				} else {
					h.expect("No pipeline stages for this job.")
				}
				h.key("esc")
				h.expectTitle("Run Monitor: Ops")
				return
			}
			f.Job(tc.job).LastBuild(jenkinstest.Build{Number: 40, Result: "SUCCESS", Timestamp: now(), Duration: 45 * time.Second})
			h.key("R")
			// successful jobs are hidden by default: the all-success notice shows.
			h.expect("All 1 jobs succeeded", "Press s to show them")
			h.key("s")
			h.expectRow(tc.job, "SUCCESS", "#40", "45s")
			h.expect("showing all")
		})
	}
}

func TestTriggerCancelledPostsNothing(t *testing.T) {
	f := newFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/build")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("t")
	h.expect("Trigger team/api/build?")
	h.key("esc")
	h.expectTitle("Collection · Ops")
	h.key("t", "n")
	h.expectTitle("Collection · Ops")
	if n := len(f.Triggers()); n != 0 {
		t.Fatalf("%d builds posted", n)
	}
}

// ---- 7. Trigger with parameters ----

func paramJob(f *jenkinstest.Server) {
	f.Job("team/api/deploy").Params(
		jenkinstest.StringParam("ENV", "target environment", "staging"),
		jenkinstest.BoolParam("DRY_RUN", "do not deploy", false),
		jenkinstest.ChoiceParam("REGION", "where", "eu", "us", "ap"),
		jenkinstest.PasswordParam("API_KEY", "deploy key"),
		jenkinstest.TextParam("NOTES", "free text", "line"),
	)
}

// fillDeployForm walks the five-field form of team/api/deploy.
func fillDeployForm(h *harness) {
	h.t.Helper()
	h.expect("team/api/deploy", "(string)", "(bool)", "(choice)", "(password)", "(text)")
	h.expect("staging", "‹ eu ›", "[ ]")
	h.replaceField("prod") // ENV
	h.key("enter")
	h.key("space") // DRY_RUN -> true
	h.expect("[x]")
	h.key("enter")
	h.key("right") // REGION eu -> us
	h.expect("‹ us ›")
	h.key("enter")
	h.typeText(secretValue) // API_KEY
	h.expectNot(secretValue)
	h.expect("•")
	h.key("enter")
	h.typeText("-2") // NOTES: line-2
	h.key("enter")
}

func TestTriggerSingleJobWithAllParameterTypes(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("t")
	h.expectTitle("Trigger")
	fillDeployForm(h)

	// summary: values listed, the password masked.
	h.expect("Ready to trigger 1 job(s)", "team/api/deploy")
	h.expect("  ENV = prod", "  DRY_RUN = true", "  REGION = us", "  API_KEY = ••••", "  NOTES = line-2")
	h.expectNot(secretValue)

	// n goes back to the form (values kept), enter submits again.
	h.key("n")
	h.expect("(password)")
	h.expectNot(secretValue)
	h.key("enter")
	h.expect("Ready to trigger 1 job(s)", "  ENV = prod")
	if len(f.Triggers()) != 0 {
		t.Fatal("triggered before confirming")
	}
	h.key("y")

	tr := triggerFor(t, f, "team/api/deploy")
	if tr.Endpoint != "buildWithParameters" {
		t.Fatalf("endpoint = %q", tr.Endpoint)
	}
	want := vals("ENV", "prod", "DRY_RUN", "true", "REGION", "us", "API_KEY", secretValue, "NOTES", "line-2")
	if !reflect.DeepEqual(tr.Params, want) {
		t.Fatalf("params = %v, want %v", tr.Params, want)
	}
	h.expectTitle("Run Monitor: Ops")
	h.expectNot(secretValue)
}

func TestTriggerParamDefaultsAreSentAsIs(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("t")
	h.key("enter", "enter", "enter", "enter", "enter") // accept every default
	h.expect("  ENV = staging", "  DRY_RUN = false", "  REGION = eu", "  NOTES = line")
	h.key("enter") // enter also confirms the summary
	tr := triggerFor(t, f, "team/api/deploy")
	want := vals("ENV", "staging", "DRY_RUN", "false", "REGION", "eu", "API_KEY", "", "NOTES", "line")
	if !reflect.DeepEqual(tr.Params, want) {
		t.Fatalf("params = %v, want %v", tr.Params, want)
	}
}

func TestTriggerPasswordNeverInErrorText(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	// A server that echoes the offending value in its refusal.
	f.RejectTrigger("team/api/deploy", 403, "Forbidden: invalid value "+secretValue)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("t")
	fillDeployForm(h)
	h.key("y")
	h.expectTitle("Run Monitor: Ops")
	h.expectRow("team/api/deploy", "FAILED", "failed to start", "Jenkins refused the request", "••••")
	h.expectNot(secretValue)
	if n := len(f.Triggers()); n != 0 {
		t.Fatalf("rejected trigger was recorded: %d", n)
	}
	// R must not poll a failed-to-start row or reveal anything.
	h.key("R")
	h.expectNot(secretValue)
	h.expectRow("team/api/deploy", "failed to start")
}

func TestTriggerFormEscReturnsToCollection(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("t")
	h.expect("(password)")
	h.key("esc")
	h.expectTitle("Collection · Ops")
	if len(f.Triggers()) != 0 {
		t.Fatal("esc triggered a build")
	}
}

// ---- 8. Multi-job trigger with Common Parameters ----

func fleetFixture(t *testing.T) *jenkinstest.Server {
	t.Helper()
	f := newFixture(t)
	// REGION and VERSION are common to all three. BRANCH is a string in two
	// jobs but a choice in the third, so it is per-job everywhere.
	f.Job("team/api/build").Params(
		jenkinstest.ChoiceParam("REGION", "region", "eu", "us"),
		jenkinstest.StringParam("VERSION", "version", "1.0"),
		jenkinstest.StringParam("BRANCH", "branch", "main"))
	f.Job("team/web/build").Params(
		jenkinstest.ChoiceParam("REGION", "region", "eu", "us"),
		jenkinstest.StringParam("VERSION", "version", "2.0"),
		jenkinstest.StringParam("BRANCH", "branch", "main"),
		jenkinstest.BoolParam("CLEAN", "clean first", false))
	f.Job("team/web/test").Params(
		jenkinstest.ChoiceParam("REGION", "region", "eu", "us"),
		jenkinstest.StringParam("VERSION", "version", "3.0"),
		jenkinstest.ChoiceParam("BRANCH", "branch", "stable", "canary"))
	return f
}

var fleetJobs = []string{"team/api/build", "team/web/build", "team/web/test"}

// driveFleet goes from the Collection to the summary and returns the
// per-job parameters the summary shows.
func driveFleet(h *harness) map[string]map[string]string {
	h.t.Helper()
	h.loginOK()
	h.openCollections()
	h.openCollection("Fleet")
	h.key("a", "t") // select all three, trigger
	h.expectTitle("Trigger")

	// Common step: asked once.
	h.expect("Common Parameters (applied to all 3 jobs)", "REGION", "VERSION", "(differs)")
	h.expectNot("BRANCH")
	h.key("right") // REGION eu -> us
	h.key("enter")
	h.typeText("9.9") // VERSION, differing defaults: set for everyone
	h.key("enter")

	// Per-job steps: BRANCH (and CLEAN) plus the overridable common values.
	h.expect("team/api/build (1/3)", "BRANCH", "[common: change to override for this job]")
	h.replaceField("hotfix")
	h.expect("us", "9.9")
	h.key("enter", "enter", "enter")
	h.expect("team/web/build (2/3)", "CLEAN")
	h.key("enter", "space", "enter") // keep BRANCH, CLEAN = true, on to REGION
	h.key("left")                    // override REGION for this job only: us -> eu
	h.expect("‹ eu ›")
	h.key("enter", "enter")
	h.expect("team/web/test (3/3)", "‹ stable ›")
	h.key("right") // BRANCH stable -> canary
	h.key("enter", "enter", "enter")
	h.expect("Ready to trigger 3 job(s)")
	return parseSummary(h.screenText(), fleetJobs)
}

// parseSummary reads "job" title lines followed by "  NAME = value" lines.
func parseSummary(text string, jobs []string) map[string]map[string]string {
	out := map[string]map[string]string{}
	var cur map[string]string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "›")) // › marks the highlighted job
		if slicesContains(jobs, l) {
			cur = map[string]string{}
			out[l] = cur
			continue
		}
		if cur != nil {
			if k, v, ok := strings.Cut(l, " = "); ok {
				cur[k] = v
			}
		}
	}
	return out
}

func slicesContains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

var fleetWant = map[string]map[string]string{
	"team/api/build": {"BRANCH": "hotfix", "REGION": "us", "VERSION": "9.9"},
	"team/web/build": {"BRANCH": "main", "CLEAN": "true", "REGION": "eu", "VERSION": "9.9"},
	"team/web/test":  {"BRANCH": "canary", "REGION": "us", "VERSION": "9.9"},
}

func TestTriggerMultipleJobsWithCommonParameters(t *testing.T) {
	f := fleetFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Fleet", fleetJobs...)))
	got := driveFleet(h)
	if !reflect.DeepEqual(got, fleetWant) {
		t.Fatalf("summary = %v\nwant %v\n%s", got, fleetWant, h.screenText())
	}
	h.key("y")
	h.expectTitle("Run Monitor: Fleet")
	for job, want := range fleetWant {
		tr := triggerFor(t, f, job)
		if tr.Endpoint != "buildWithParameters" {
			t.Fatalf("%s endpoint = %q", job, tr.Endpoint)
		}
		wv := url.Values{}
		for k, v := range want {
			wv.Set(k, v)
		}
		if !reflect.DeepEqual(tr.Params, wv) {
			t.Fatalf("%s got %v, want %v", job, tr.Params, wv)
		}
		h.expectRow(job, "QUEUED")
	}
}

func TestTriggerMultipleJobsOneRefusedOthersContinue(t *testing.T) {
	f := fleetFixture(t)
	f.RejectTrigger("team/web/build", 403, "no permission to build team/web/build")
	h := newApp(t, f, withCollections(collectionsTOML("Fleet", fleetJobs...)))
	got := driveFleet(h)
	if !reflect.DeepEqual(got, fleetWant) {
		t.Fatalf("summary = %v", got)
	}
	h.key("y")
	h.expectTitle("Run Monitor: Fleet")

	// the other two were triggered with the right parameters.
	for _, job := range []string{"team/api/build", "team/web/test"} {
		tr := triggerFor(t, f, job)
		for k, v := range fleetWant[job] {
			if tr.Params.Get(k) != v {
				t.Fatalf("%s: %s = %q, want %q", job, k, tr.Params.Get(k), v)
			}
		}
		h.expectRow(job, "QUEUED")
	}
	for _, tr := range f.Triggers() {
		if tr.Job == "team/web/build" {
			t.Fatal("the refused job was recorded as triggered")
		}
	}
	// and the monitor says why the third one did not start.
	h.expectRow("team/web/build", "FAILED", "failed to start", "Jenkins refused the request")
	h.expect("no permission to build")
	h.expectNot("●  QUEUED  team/web/build")
}

func TestTriggerMultipleJobsNoCommonParameters(t *testing.T) {
	// Jobs without any shared parameter skip the common step; jobs without
	// parameters at all are listed as "(no parameters)" in the summary.
	f := newFixture(t)
	f.Job("team/api/build").Params(jenkinstest.StringParam("A_ONLY", "", "a"))
	h := newApp(t, f, withCollections(collectionsTOML("Mix", "team/api/build", "team/web/build")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Mix")
	h.key("a", "t")
	h.expectTitle("Trigger")
	h.expectNot("Common Parameters")
	h.expect("team/api/build (1/1)", "A_ONLY")
	h.key("enter")
	h.expect("Ready to trigger 2 job(s)", "  A_ONLY = a", "(no parameters)")
	h.key("y")
	if a := triggerFor(t, f, "team/api/build"); a.Endpoint != "buildWithParameters" || a.Params.Get("A_ONLY") != "a" {
		t.Fatalf("api trigger = %+v", a)
	}
	if w := triggerFor(t, f, "team/web/build"); w.Endpoint != "build" || len(w.Params) != 0 {
		t.Fatalf("web trigger = %+v", w)
	}
}

// ---- 9. Run Monitor from a Collection ----

func mixFixture(t *testing.T) *jenkinstest.Server {
	t.Helper()
	return newFixture(t) // api/build OK, api/deploy FAILED, web/build OK, web/test RUNNING
}

var mixJobs = []string{"team/api/build", "team/api/deploy", "team/web/build", "team/web/test"}

func TestMonitorOpensHidingSuccessAndToggles(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginOK()
	h.openCollections()
	h.openCollection("Mix")
	h.key("m")
	h.expectTitle("Run Monitor: Mix")
	h.expect("hiding 2 successful (s to show)", "2 remaining")
	h.expectRow("team/api/deploy", "FAILED", "#7")
	h.expectRow("team/web/test", "RUNNING", "#9")
	h.expectNot("team/api/build", "team/web/build")

	h.key("s")
	h.expect("showing all (s to hide successful)", "2 remaining")
	h.expectRow("team/api/build", "SUCCESS", "#12")
	h.expectRow("team/web/build", "SUCCESS", "#31")
	h.key("s")
	h.expect("hiding 2 successful")
	h.expectNot("team/web/build")

	// Enter opens details, esc returns to the LIST, a second esc to the Collection.
	h.key("enter")
	h.expect("FAILED", "team/api/deploy #7", "No pipeline stages for this job.")
	h.expectNot("hiding 2 successful")
	h.key("esc")
	h.expectTitle("Run Monitor: Mix")
	h.expect("hiding 2 successful", "team/web/test")
	h.key("esc")
	h.expectTitle("Collection · Mix")
	h.expect("4 jobs")
}

// In the details view l shows the log of the highlighted stage or step, L the whole build.
func TestMonitorStageLog(t *testing.T) {
	f := mixFixture(t)
	f.Job("team/api/deploy").Log("whole build output\n").Pipeline(
		jenkinstest.Stage{Name: "Build", Status: jenkinstest.StageSuccess, Children: []jenkinstest.Stage{
			{Name: "compile", Status: jenkinstest.StageSuccess, Log: "compiled fine\n"}}},
		jenkinstest.Stage{Name: "Deploy", Status: jenkinstest.StageFailed, Children: []jenkinstest.Stage{
			{Name: "sh", Status: jenkinstest.StageFailed, Log: "kubectl: connection refused\n"}}})
	h := newApp(t, f, withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginOK()
	h.openCollections()
	h.openCollection("Mix")
	h.key("m")
	h.key("enter")
	h.expect("team/api/deploy #7", "Build", "Deploy")
	// The marker starts on the failed step (sh); up goes to the Deploy stage.
	h.key("up")
	h.key("l")
	h.expect("stage Deploy", "kubectl: connection refused")
	h.expectNot("compiled fine", "whole build output")
	h.key("esc")
	h.key("up") // compile
	h.key("l")
	h.expect("step compile (Build)", "compiled fine")
	h.expectNot("kubectl")
	h.key("esc")
	h.key("L")
	h.expect("whole build output")
	h.expectNot("stage ", "compiled fine")
}

// l in the details view shows the build's console log; esc returns to details.
func TestMonitorConsoleLog(t *testing.T) {
	f := mixFixture(t)
	f.Job("team/api/deploy").Log("Started by user me\n[Pipeline] sh\n+ ./deploy.sh\nERROR: deploy failed\n")
	h := newApp(t, f, withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginOK()
	h.openCollections()
	h.openCollection("Mix")
	h.key("m")
	h.expectTitle("Run Monitor: Mix")
	h.key("l") // no effect in the list
	h.expectNot("Started by user me")
	h.key("enter")
	h.key("l")
	h.expect("team/api/deploy #7", "1 │ Started by user me", "4 │ ERROR: deploy failed", "finished")
	h.key("/")
	h.typeText("deploy_f*d")
	h.key("enter")
	h.expect(`match 1 of 1 for "deploy_f*d"`)
	h.key(":")
	h.typeText("2")
	h.key("enter")
	h.expect("line 2")
	h.key("esc")
	h.expect("team/api/deploy #7", "No pipeline stages for this job.")
	h.expectNot("Started by user me")
	h.key("esc")
	h.expect("hiding 2 successful")
}

// The Run Monitor's status line is on the title row, and the job list starts
// one blank line under it.
func TestMonitorBlankLineAboveJobs(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginOK()
	h.openCollections()
	h.openCollection("Mix")
	h.key("m")
	h.expectTitle("Run Monitor: Mix")
	rows := strings.Split(h.screenText(), "\n")
	if !strings.HasPrefix(rows[0], "Run Monitor: Mix") || !strings.Contains(rows[0], "hiding 2 successful (s to show) · 2 remaining") ||
		rows[1] != "" || !strings.Contains(rows[2], "team/") {
		t.Fatalf("monitor rows:\n%s", strings.Join(rows[:4], "\n"))
	}
}

func TestMonitorFromCollectionsListAndBack(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginOK()
	h.openCollections()
	h.key("m")
	h.expectTitle("Run Monitor: Mix")
	h.expect("hiding 2 successful")
	h.key("esc")
	h.expectTitle("Collections")
}

func TestMonitorRefreshAndAllSuccessNotice(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginOK()
	h.openCollections()
	h.openCollection("Mix")
	h.key("m")
	h.expect("hiding 2 successful", "2 remaining")

	// the failed job is fixed: R picks it up and hides it.
	f.Job("team/api/deploy").LastBuild(okBuild(8))
	h.key("R")
	h.expect("hiding 3 successful", "1 remaining")
	h.expectNot("team/api/deploy")

	// the running one finishes OK: nothing left to show.
	f.Job("team/web/test").LastBuild(okBuild(10))
	h.key("R")
	h.expect("All 4 jobs succeeded", "Press s to show them")
	h.expectNot("RUNNING")
	h.key("s")
	h.expect("showing all", "0 remaining")
	h.expectRow("team/web/test", "SUCCESS", "#10")
	h.expectRow("team/api/deploy", "SUCCESS", "#8")

	// a fresh failure appears again once the successes are hidden.
	f.Job("team/web/build").LastBuild(failBuild(32))
	h.key("R", "s")
	h.expectRow("team/web/build", "FAILED", "#32")
	h.expect("hiding 3 successful", "1 remaining")
}

func TestMonitorAllSuccessNoticeOnOpen(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Green", "team/api/build", "team/web/build")))
	h.loginOK()
	h.openCollections()
	h.key("m")
	h.expectTitle("Run Monitor: Green")
	h.expect("All 2 jobs succeeded")
	h.key("s")
	h.expectRow("team/api/build", "SUCCESS")
	h.expectRow("team/web/build", "SUCCESS")
}

func TestMonitorKeptAcrossLeavingTriggerAndRename(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/build", "team/web/test")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")

	// trigger the first job; the monitor opens with the queued run.
	h.key("t", "y")
	h.expectTitle("Run Monitor: Ops")
	mon := h.router.Top()
	h.expectRow("team/api/build", "QUEUED")
	qid := lastQueueID(t, f, "team/api/build")

	// leave and reopen from the Collection: the same monitor, still tracking the queued run
	// (a fresh monitor would show the job's previous SUCCESS build instead).
	h.key("esc")
	h.expectTitle("Collection · Ops")
	h.key("m")
	if h.router.Top() != mon {
		t.Fatal("reopening created a new monitor")
	}
	h.expectRow("team/api/build", "QUEUED")

	// the build starts; state keeps following the triggered build across a leave.
	f.ResolveQueueItem(qid, 13)
	f.Job("team/api/build").LastBuild(jenkinstest.Build{Number: 13, Building: true, Timestamp: now()})
	h.key("R")
	h.expectRow("team/api/build", "RUNNING", "#13")
	h.key("esc", "m")
	if h.router.Top() != mon {
		t.Fatal("reopening created a new monitor")
	}
	h.expectRow("team/api/build", "RUNNING", "#13")

	// triggering from the Collection adds to the same monitor.
	h.key("esc")
	h.key("down", "t", "y") // team/web/test
	if h.router.Top() != mon {
		t.Fatal("triggering from the Collection opened a different monitor")
	}
	h.expectRow("team/web/test", "QUEUED")
	h.expectRow("team/api/build", "RUNNING", "#13")
	if len(f.Triggers()) != 2 {
		t.Fatalf("triggers = %d", len(f.Triggers()))
	}

	// rename keeps the monitor.
	h.key("esc", "esc")
	h.expectTitle("Collections")
	h.key("r")
	h.replaceField("Ops Renamed")
	h.key("enter")
	h.expect("Ops Renamed")
	h.key("m")
	h.expectTitle("Run Monitor: Ops Renamed")
	if h.router.Top() != mon {
		t.Fatal("rename lost the monitor")
	}
	h.expectRow("team/api/build", "RUNNING", "#13")
	h.expectRow("team/web/test", "QUEUED")
}

func TestMonitorCollectionsAreIndependent(t *testing.T) {
	f := mixFixture(t)
	toml := collectionsTOML("One", "team/api/deploy") + collectionsTOML("Two", "team/web/test")
	h := newApp(t, f, withCollections(toml))
	h.loginOK()
	h.openCollections()
	h.key("m")
	h.expectTitle("Run Monitor: One")
	h.expect("team/api/deploy")
	h.expectNot("team/web/test")
	first := h.router.Top()
	h.key("esc", "down", "m")
	h.expectTitle("Run Monitor: Two")
	h.expect("team/web/test")
	h.expectNot("team/api/deploy")
	if h.router.Top() == first {
		t.Fatal("two collections share a monitor")
	}
}

// ---- 10. credentials never on disk ----

func TestCredentialsNeverOnDisk(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	h := newApp(t, f, withoutConfig())
	h.start()
	h.skipSplash()
	h.typeText(f.URL)
	h.key("enter", "enter")
	// a failed login first.
	const wrong = "wrong-token-ZZ91"
	h.submitLogin(f.User(), wrong)
	h.expect("Authentication failed")
	h.replaceField(testToken)
	h.key("enter")
	h.expectTitle("jenktui")

	// create a Collection, trigger a parameterised job with a password, edit the branch filter.
	h.openCollections()
	h.key("n")
	h.typeText("Release")
	h.key("enter")
	h.key("/")
	h.typeText("api/deploy")
	h.key("enter", "space", "enter")
	h.expectTitle("Collections")
	h.openCollection("Release")
	h.key("t")
	fillDeployForm(h)
	h.key("y")
	h.expectTitle("Run Monitor: Release")
	h.key("esc", "esc", "esc")
	h.openCatalog()
	h.key("B", "a")
	h.typeText("main")
	h.key("enter", "enter")

	entries, err := os.ReadDir(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		b, err := os.ReadFile(filepath.Join(h.dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{testToken, wrong, secretValue} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("%s contains a secret (%q):\n%s", e.Name(), secret, b)
			}
		}
		if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", e.Name(), info.Mode().Perm())
		}
	}
	// nothing but the two settings files, and no leftover temp files.
	if want := []string{"collections.toml", "config.toml"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("files = %v, want %v", names, want)
	}
	// the secrets are not on screen either.
	h.expectNot(testToken, wrong, secretValue)
}

// ---- 11. CSRF ----

func TestTriggerWithCSRFCrumbEndToEnd(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *jenkinstest.Server)
		// before the second trigger
		between func(f *jenkinstest.Server)
	}{
		{"session-bound crumb", func(f *jenkinstest.Server) { f.EnableCrumb(true).SessionBoundCrumb(true) }, nil},
		{"plain crumb", func(f *jenkinstest.Server) { f.EnableCrumb(true) }, nil},
		{"stale crumb is refreshed", func(f *jenkinstest.Server) { f.EnableCrumb(true).SessionBoundCrumb(true) },
			func(f *jenkinstest.Server) { f.RotateCrumb("rotated-crumb") }},
		{"crumb issuer refuses, POST decides", func(f *jenkinstest.Server) { f.CrumbIssuerForbidden(true) }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/build", "team/web/build")))
			h.loginOK() // the real client: default HTTP client with a cookie jar
			h.openCollections()
			h.openCollection("Ops")
			h.key("t", "y")
			h.expectTitle("Run Monitor: Ops")
			h.expectRow("team/api/build", "QUEUED")
			if n := len(f.Triggers()); n != 1 {
				t.Fatalf("triggers = %d, want 1 (a 403 would show 'failed to start')\n%s", n, h.screenText())
			}
			if tc.between != nil {
				tc.between(f)
			}
			h.key("esc")
			h.key("down", "t", "y")
			h.expectRow("team/web/build", "QUEUED")
			h.expectNot("failed to start", "Jenkins refused")
			if n := len(f.Triggers()); n != 2 {
				t.Fatalf("triggers = %d, want 2\n%s", n, h.screenText())
			}
		})
	}
}

// ---- Rerun from the Run Monitor (CORE1) ----

func TestRerunFromMonitorPrefillsFormAndTracksNewBuild(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	f.Job("team/api/deploy").LastBuild(jenkinstest.Build{Number: 7, Result: "FAILURE", Timestamp: now(),
		Params: []jenkinstest.BuildParam{
			jenkinstest.StringBuildParam("ENV", "prod"),
			jenkinstest.BoolBuildParam("DRY_RUN", true),
			jenkinstest.StringBuildParam("REGION", "ap"),
			jenkinstest.PasswordBuildParam("API_KEY"),
			jenkinstest.StringBuildParam("NOTES", "line-9"),
			jenkinstest.StringBuildParam("REMOVED", "gone"), // no longer defined by the job
		}})
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("m")
	h.expectTitle("Run Monitor: Ops")
	h.expectRow("team/api/deploy", "FAILED", "#7")
	mon := h.router.Top()
	depth := h.router.Depth()

	h.key("r")
	h.expectTitle("Trigger")
	h.expect("team/api/deploy", "prod", "[x]", "‹ ap ›", "line-9")
	h.expectNot("gone")
	h.key("enter", "enter", "enter") // ENV, DRY_RUN, REGION
	h.typeText(secretValue)          // the password is asked for again
	h.key("enter")
	h.key("enter") // NOTES -> submit
	h.expect("Ready to trigger 1 job(s)", "  ENV = prod", "  DRY_RUN = true", "  REGION = ap", "  NOTES = line-9")
	h.expectNot(secretValue)
	h.key("y")

	tr := triggerFor(t, f, "team/api/deploy")
	want := vals("ENV", "prod", "DRY_RUN", "true", "REGION", "ap", "API_KEY", secretValue, "NOTES", "line-9")
	if !reflect.DeepEqual(tr.Params, want) {
		t.Fatalf("params = %v, want %v", tr.Params, want)
	}
	// The same monitor, once on the stack, now tracks the new build.
	h.expectTitle("Run Monitor: Ops")
	if h.router.Top() != mon || h.router.Depth() != depth {
		t.Fatalf("monitor is not the single top screen: depth %d, want %d", h.router.Depth(), depth)
	}
	h.expectRow("team/api/deploy", "QUEUED")

	// The router kept one monitor, so one esc leaves it.
	h.key("esc")
	h.expectTitle("Collection · Ops")
}

func TestRerunCancelReturnsToMonitorAndResumesPolling(t *testing.T) {
	f := newFixture(t)
	paramJob(f)
	h := newApp(t, f, withCollections(collectionsTOML("Ops", "team/api/deploy")))
	h.loginOK()
	h.openCollections()
	h.openCollection("Ops")
	h.key("m")
	mon := h.router.Top()
	h.expectRow("team/api/deploy", "FAILED", "#7")
	h.key("r")
	h.expectTitle("Trigger")

	// A build starts on the server while the form covers the monitor.
	f.Job("team/api/deploy").LastBuild(jenkinstest.Build{Number: 8, Building: true, Timestamp: now()})
	h.key("esc")
	if h.router.Top() != mon {
		t.Fatal("esc did not return to the monitor")
	}
	// No R: uncovering the monitor restarts its polling, which sees build 8.
	h.expectTitle("Run Monitor: Ops")
	h.expectRow("team/api/deploy", "RUNNING", "#8")
	if n := len(f.Triggers()); n != 0 {
		t.Fatalf("%d builds posted", n)
	}
}

// ---- Open a Collection from the command line (CORE2) ----

func TestStartCollectionLandsOnItsMonitorAfterLogin(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withStart("mIx"), withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginNoMenu()
	h.expectTitle("Run Monitor: Mix")
	h.expect("hiding 2 successful (s to show)")
	h.expectRow("team/api/deploy", "FAILED", "#7")
	// Esc goes back to the menu, and the name is not applied again.
	h.key("esc")
	h.expectTitle("jenktui")
	if h.sess.StartCollection != "" {
		t.Fatal("StartCollection was not cleared")
	}
}

func TestStartCollectionUnknownShowsMenuWithNotice(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withStart("Nope"), withCollections(collectionsTOML("Mix", mixJobs...)))
	h.loginNoMenu()
	h.expectTitle("jenktui")
	h.expect("no Collection named \"Nope\"", "Collections", "Job Catalog")
	h.key("down")
	h.expectNot("no Collection named")
}

func TestStartCollectionMarksMissingJobs(t *testing.T) {
	f := mixFixture(t)
	h := newApp(t, f, withStart("Mix"), withCollections(collectionsTOML("Mix", "team/api/deploy", "team/gone")))
	h.loginNoMenu()
	h.expectTitle("Run Monitor: Mix")
	h.expectRow("team/gone", "MISSING")
}
