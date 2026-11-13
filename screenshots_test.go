//go:build screenshots

package main

// Generates the ANSI frames behind the README screenshots. Everything shown
// comes from the in-process fake Jenkins and the invented "shop" data below;
// nothing is read from a real server.
//
//	SHOT_DIR=docs/screenshots/frames go test -tags screenshots -run TestScreenshots .
//	uv run --with pillow --with fonttools docs/screenshots/render.py
//
// See docs/screenshots/README.md.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dursunkoc/jenktui/internal/jenkins/jenkinstest"
)

func init() {
	// Colour output without a terminal.
	os.Setenv("CLICOLOR_FORCE", "1")
	os.Setenv("COLORTERM", "truecolor")
	lipgloss.SetHasDarkBackground(true)
}

const (
	shotW = 112
)

func shotBuild(n int, result string, ago, d time.Duration) jenkinstest.Build {
	return jenkinstest.Build{Number: n, Result: result, Timestamp: time.Now().Add(-ago), Duration: d}
}

// shotHistory builds a job's history: results oldest first, ending with the
// last build number, each build 30 minutes apart, the newest "ago" old and lastDur long.
func shotHistory(last int, ago, lastDur time.Duration, results ...string) []jenkinstest.Build {
	var bs []jenkinstest.Build
	for i, r := range results {
		n := last - len(results) + 1 + i
		bs = append(bs, shotBuild(n, r, ago+time.Duration(len(results)-1-i)*30*time.Minute, 2*time.Minute))
	}
	if len(bs) > 0 {
		bs[len(bs)-1].Duration = lastDur
	}
	return bs
}

func shopFixture(t *testing.T) *jenkinstest.Server {
	f := jenkinstest.New(t).SetAuth("demo", "demo-token")
	f.Job("shop/api/build").LastBuild(shotBuild(128, "SUCCESS", 2*time.Hour, 94*time.Second))
	f.Job("shop/api/unit-tests").Builds(shotHistory(412, 2*time.Hour, 3*time.Minute+12*time.Second, "SUCCESS", "SUCCESS", "SUCCESS", "FAILURE", "SUCCESS")...)
	deploy := shotHistory(57, 40*time.Minute, 2*time.Minute+8*time.Second, "SUCCESS", "SUCCESS", "FAILURE", "SUCCESS", "SUCCESS", "FAILURE")
	deploy[len(deploy)-1].Causes = []jenkinstest.Cause{{Description: "Started by user Dana Ortiz", User: "dana", UserName: "Dana Ortiz"}}
	deploy[len(deploy)-1].Changes = []jenkinstest.Change{
		{Commit: "a1b2c3d4e5f6", Author: "Dana Ortiz", Message: "Retry the rollout on 503"},
		{Commit: "9f8e7d6c5b4a", Author: "Sam Lee", Message: "Bump api image to 57"},
	}
	f.Job("shop/api/deploy").Builds(deploy...).
		TestReport(jenkinstest.TestReport{Suites: []jenkinstest.TestSuite{{Name: "smoke", Cases: []jenkinstest.TestCase{
			{ClassName: "smoke.Health", Name: "TestHealthEndpoint", Status: jenkinstest.CasePassed},
			{ClassName: "smoke.Catalog", Name: "TestListProducts", Status: jenkinstest.CasePassed},
			{ClassName: "smoke.Cart", Name: "TestCheckoutSmoke", Status: jenkinstest.CaseFailed, ErrorDetails: "expected 200, got 503"},
		}}}}).
		Log("Started by user Dana Ortiz\n[Pipeline] stage\n[Pipeline] { (Build)\n+ make build\nok\n[Pipeline] { (Deploy)\n+ docker pull registry.example.com/shop/api:57\n+ kubectl apply -f k8s/\nerror: the server is currently unable to handle the request\nERROR: deploy failed after 3 attempts\nsmoke.Cart.TestCheckoutSmoke FAILED: expected 200, got 503\nFinished: FAILURE\n").
		Pipeline(
			jenkinstest.Stage{Name: "Build", Status: jenkinstest.StageSuccess, Duration: 70 * time.Second, Children: []jenkinstest.Stage{
				{Name: "git checkout", Status: jenkinstest.StageSuccess, Duration: 4 * time.Second, Log: "Cloning https://git.example.com/shop/api.git\nChecked out main (a1b2c3d)\n"},
				{Name: "make build", Status: jenkinstest.StageSuccess, Duration: 66 * time.Second, Log: "+ make build\ncompiling 212 files\nok\n"},
			}},
			jenkinstest.Stage{Name: "Test", Status: jenkinstest.StageSuccess, Duration: 31 * time.Second, Children: []jenkinstest.Stage{
				{Name: "unit", Status: jenkinstest.StageSuccess, Duration: 31 * time.Second, Log: "412 passed\n"},
			}},
			jenkinstest.Stage{Name: "Deploy", Status: jenkinstest.StageFailed, Duration: 27 * time.Second, Children: []jenkinstest.Stage{
				{Name: "docker pull", Status: jenkinstest.StageSuccess, Duration: 9 * time.Second, Log: "+ docker pull registry.example.com/shop/api:57\nDigest: sha256:3f1c…\nStatus: Downloaded newer image\n"},
				{Name: "kubectl apply", Status: jenkinstest.StageFailed, Duration: 18 * time.Second, Log: "+ kubectl apply -f k8s/\nerror: the server is currently unable to handle the request\nERROR: deploy failed after 3 attempts\n"},
			}},
		)
	integ := shotHistory(300, 20*time.Minute, 3*time.Minute+50*time.Second, "SUCCESS", "FAILURE", "SUCCESS", "SUCCESS", "SUCCESS")
	integ = append(integ, jenkinstest.Build{Number: 301, Building: true, Timestamp: time.Now().Add(-96 * time.Second), EstimatedDuration: 4 * time.Minute})
	f.Job("shop/api/integration").Builds(integ...).
		Pipeline(
			jenkinstest.Stage{Name: "Provision", Status: jenkinstest.StageSuccess, Duration: 20 * time.Second},
			jenkinstest.Stage{Name: "Run suites", Status: jenkinstest.StageInProgress},
			jenkinstest.Stage{Name: "Teardown", Status: jenkinstest.StageNotExecuted},
		)
	f.Job("shop/web/build").LastBuild(shotBuild(92, "SUCCESS", 3*time.Hour, 2*time.Minute+5*time.Second))
	f.Job("shop/web/e2e").Builds(shotHistory(33, time.Hour, 8*time.Minute+40*time.Second, "SUCCESS", "SUCCESS", "UNSTABLE", "SUCCESS", "UNSTABLE")...)
	f.Job("shop/docs/publish").LastBuild(shotBuild(14, "SUCCESS", 26*time.Hour, 41*time.Second))
	pay := f.Multibranch("shop/payments")
	pay.Branch("main").LastBuild(shotBuild(76, "SUCCESS", 5*time.Hour, 4*time.Minute))
	pay.Branch("feature/refund-flow").Builds(shotHistory(9, 20*time.Minute, 2*time.Minute+30*time.Second, "FAILURE", "SUCCESS", "FAILURE", "FAILURE")...)
	pay.Branch("release/2.4").LastBuild(shotBuild(5, "SUCCESS", 2*24*time.Hour, 4*time.Minute+2*time.Second))
	return f
}

func TestScreenshots(t *testing.T) {
	dir := os.Getenv("SHOT_DIR")
	if dir == "" {
		t.Skip("set SHOT_DIR to write frames")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := shopFixture(t)
	h := newApp(t, f,
		withConfigExtra(`parent_job = "shop"`),
		withCollections(collectionsTOML("Release checklist",
			"shop/api/build", "shop/api/unit-tests", "shop/api/deploy", "shop/api/integration",
			"shop/web/build", "shop/web/e2e", "shop/payments/main", "shop/payments/feature%2Frefund-flow")+
			collectionsTOML("Docs and web", "shop/docs/publish", "shop/web/build")+
			collectionsTOML("Payments", "shop/payments/main", "shop/payments/feature%2Frefund-flow")))
	snap := func(name string, rows int) {
		t.Helper()
		h.router.Update(tea.WindowSizeMsg{Width: shotW, Height: rows})
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(h.router.View()), 0o644); err != nil {
			t.Fatal(err)
		}
		h.router.Update(tea.WindowSizeMsg{Width: shotW, Height: 30})
	}
	h.router.Update(tea.WindowSizeMsg{Width: shotW, Height: 30})
	h.loginOK()
	h.router.Update(tea.WindowSizeMsg{Width: shotW, Height: 30})

	h.menuTo(2)
	h.expectTitle("Overview")
	h.expect("Release checklist")
	snap("overview", 16)
	h.key("esc")
	h.expectTitle("jenktui")
	h.key("up", "up") // back on Collections, where openCatalog expects the cursor

	h.openCatalog()
	h.expect("shop")
	snap("catalog", 19)
	for h.title() != "jenktui" {
		h.key("esc")
	}

	h.key("up")
	h.openCollections()
	h.openCollection("Release checklist")
	h.expect("8 jobs")
	snap("collection", 17)

	h.key("m")
	h.expectTitle("Run Monitor · shop · Release checklist")
	h.key("s") // show the successful jobs too
	time.Sleep(600 * time.Millisecond)
	h.send(spinner.TickMsg{}) // let the refresh spinner's minimum visible time end
	snap("monitor", 16)

	// Details of the failed deploy: highlight its failing step.
	h.key("enter")
	h.expect("kubectl apply")
	h.key("down", "down", "down", "down", "down", "down", "down") // kubectl apply
	snap("details", 20)

	h.key("L")
	h.expect("deploy failed")
	h.key("/")
	h.typeText("error*")
	h.key("enter")
	snap("log", 16)
}
