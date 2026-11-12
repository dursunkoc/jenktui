package catalog

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type fakeLister struct {
	jobs  []jenkins.Job
	errs  []error // consumed one per call
	calls int
	got   string
}

func (f *fakeLister) ListJobs(_ context.Context, parent string) ([]jenkins.Job, error) {
	f.calls++
	f.got = parent
	if len(f.errs) > 0 {
		e := f.errs[0]
		f.errs = f.errs[1:]
		if e != nil {
			return nil, e
		}
	}
	return f.jobs, nil
}

// feed runs cmd and delivers loadedMsg results back to s.
func feed(t *testing.T, s *Screen, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch m := cmd().(type) {
	case loadedMsg:
		s.Update(m)
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if lm, ok := c().(loadedMsg); ok {
				s.Update(lm)
			}
		}
	}
}

func kmsg(s string) tea.KeyMsg {
	if s == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestLoadSuccess(t *testing.T) {
	f := &fakeLister{jobs: []jenkins.Job{{FullName: "team/a", Color: "blue"}, {FullName: "team/b", Color: "red_anime"}, {FullName: "x/c", Color: "red"}}}
	s := New(f, "team")
	if v := shown(s); !strings.Contains(v, "Loading") {
		t.Fatalf("loading view: %q", v)
	}
	feed(t, s, s.Init())
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	v := shown(s)
	// names are shown relative to the parent; one outside it stays whole.
	for _, want := range []string{"3 jobs", "[ ] a ", "[ ] b ", "[ ] x › c ", "SUCCESS", "RUNNING", "FAILED"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "team/") {
		t.Errorf("parent prefix shown:\n%s", v)
	}
	if f.got != "team" {
		t.Errorf("parent = %q", f.got)
	}
	s.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if got := s.Selected(); len(got) != 1 || got[0] != "team/a" {
		t.Errorf("selected = %v", got)
	}
	s.Update(kmsg("/"))
	s.Update(kmsg("x"))
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v := shown(s); !strings.Contains(v, "1 of 3 jobs") {
		t.Errorf("filtered view:\n%s", v)
	}
}

func TestErrorAndRetry(t *testing.T) {
	f := &fakeLister{errs: []error{errors.New("boom")}, jobs: []jenkins.Job{{FullName: "a"}}}
	s := New(f, "")
	feed(t, s, s.Init())
	v := shown(s)
	if !strings.Contains(v, "boom") || !strings.Contains(v, "retry") {
		t.Fatalf("error view:\n%s", v)
	}
	_, cmd := s.Update(kmsg("r"))
	if !strings.Contains(shown(s), "Loading") {
		t.Fatal("retry should show spinner")
	}
	feed(t, s, cmd)
	if f.calls != 2 || !strings.Contains(shown(s), "1 jobs") {
		t.Fatalf("calls=%d view:\n%s", f.calls, shown(s))
	}
}

func TestEscPopsAndFilterEscDoesNot(t *testing.T) {
	f := &fakeLister{jobs: []jenkins.Job{{FullName: "a"}}}
	s := New(f, "")
	feed(t, s, s.Init())
	s.Update(kmsg("/"))
	_, cmd := s.Update(kmsg("esc")) // closes filter only
	if cmd != nil {
		if _, ok := cmd().(core.Pop); ok {
			t.Fatal("esc in filter must not pop")
		}
	}
	_, cmd = s.Update(kmsg("esc"))
	if _, ok := cmd().(core.Pop); !ok {
		t.Fatal("esc should pop")
	}
}

func TestStatusFromColor(t *testing.T) {
	for c, want := range map[string]core.Status{"blue": core.StatusSuccess, "red": core.StatusFailed, "blue_anime": core.StatusRunning, "yellow": core.StatusUnstable, "yellow_anime": core.StatusRunning, "aborted": core.StatusAborted, "notbuilt": core.StatusUnknown, "": core.StatusUnknown} {
		if got := statusFromColor(c); got != want {
			t.Errorf("%q = %v want %v", c, got, want)
		}
	}
}

type fakeCatalog struct{ cat jenkins.Catalog }

func (f *fakeCatalog) ListJobs(context.Context, string) ([]jenkins.Job, error) {
	return f.cat.Jobs, nil
}
func (f *fakeCatalog) ListCatalog(context.Context, string) (jenkins.Catalog, error) {
	return f.cat, nil
}

func mbJob(project, branch string) jenkins.Job {
	return jenkins.Job{FullName: project + "/" + branch, Multibranch: project, Branch: branch}
}

func newBranchScreen(t *testing.T, patterns []string, save func([]string) error) *Screen {
	t.Helper()
	f := &fakeCatalog{cat: jenkins.Catalog{
		Jobs:                []jenkins.Job{{FullName: "plain"}, mbJob("app", "main"), mbJob("app", "feature/x"), mbJob("lib", "dev")},
		MultibranchProjects: []string{"app", "lib"},
	}}
	s := New(f, "", WithBranchFilter(Settings{Patterns: patterns, Save: save}))
	feed(t, s, s.Init())
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return s
}

func typeText(s *Screen, text string) {
	for _, r := range text {
		s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestBranchFilterToggleAndPlaceholder(t *testing.T) {
	s := newBranchScreen(t, []string{"main", "release/*"}, nil)
	v := shown(s)
	for _, want := range []string{"branches: filtered (2 patterns)", "plain"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	for _, want := range [][2]string{{"app", "main"}, {"lib", "(no matching branches)"}} {
		if !branchRow(want[0], want[1]).MatchString(v) {
			t.Errorf("missing row %q / %q:\n%s", want[0], want[1], v)
		}
	}
	if strings.Contains(v, "feature/x") || branchRow("lib", "dev").MatchString(v) {
		t.Errorf("filtered branches visible:\n%s", v)
	}
	// Placeholder is not selectable ("a" selects all).
	s.Update(kmsg("a"))
	if got := s.Selected(); len(got) != 2 {
		t.Errorf("selected = %v (placeholder must be skipped)", got)
	}
	s.Update(kmsg("b"))
	v = shown(s)
	if !strings.Contains(v, "branches: all") || !branchRow("app", "feature/x").MatchString(v) || !branchRow("lib", "dev").MatchString(v) {
		t.Errorf("filter off, missing rows:\n%s", v)
	}
	if strings.Contains(v, "no matching") {
		t.Errorf("no placeholder expected when off:\n%s", v)
	}
}

func TestBranchFilterNoPatternsShowsAll(t *testing.T) {
	s := newBranchScreen(t, nil, nil)
	v := shown(s)
	if !strings.Contains(v, "branches: all") || !branchRow("lib", "dev").MatchString(v) {
		t.Errorf("view:\n%s", v)
	}
	s.Update(kmsg("b")) // turning "on" with no patterns must not hide anything
	if !branchRow("lib", "dev").MatchString(shown(s)) {
		t.Errorf("empty filter hid rows:\n%s", shown(s))
	}
}

func TestBranchFilterEditor(t *testing.T) {
	var saved [][]string
	s := newBranchScreen(t, nil, func(p []string) error { saved = append(saved, p); return nil })

	s.Update(kmsg("B"))
	if !strings.Contains(shown(s), "no patterns") {
		t.Fatalf("editor view:\n%s", shown(s))
	}
	s.Update(kmsg("a"))
	typeText(s, "[bad")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(shown(s), "unclosed") || len(s.patterns) != 0 {
		t.Fatalf("invalid pattern should be rejected:\n%s", shown(s))
	}
	s.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	s.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	s.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	s.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // empty -> rejected
	if len(s.patterns) != 0 {
		t.Fatalf("empty pattern accepted: %v", s.patterns)
	}
	typeText(s, "main")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s.Update(kmsg("a"))
	typeText(s, "tmp/*")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(s.patterns) != 2 {
		t.Fatalf("patterns = %v", s.patterns)
	}
	// esc while typing cancels only the input
	s.Update(kmsg("a"))
	typeText(s, "zzz")
	s.Update(kmsg("esc"))
	if !s.editing || len(s.patterns) != 2 {
		t.Fatal("esc in input should only cancel input")
	}
	// delete the selected (second) pattern
	s.Update(kmsg("d"))
	if len(s.patterns) != 1 || s.patterns[0] != "main" {
		t.Fatalf("after delete: %v", s.patterns)
	}
	if len(saved) != 0 {
		t.Fatal("saved before close")
	}
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		if _, ok := cmd().(core.Pop); ok {
			t.Fatal("closing the editor must not pop")
		}
	}
	if s.editing || len(saved) != 1 || len(saved[0]) != 1 || saved[0][0] != "main" {
		t.Fatalf("editing=%v saved=%v", s.editing, saved)
	}
	v := shown(s)
	if !strings.Contains(v, "branches: filtered (1 pattern)") || branchRow("lib", "dev").MatchString(v) || !branchRow("lib", "(no matching branches)").MatchString(v) {
		t.Errorf("filter not applied after edit:\n%s", v)
	}
}

func TestBranchFilterEditorSaveErrorAndNoChange(t *testing.T) {
	calls := 0
	s := newBranchScreen(t, []string{"main"}, func([]string) error { calls++; return errors.New("disk full") })
	s.Update(kmsg("B"))
	s.Update(tea.KeyMsg{Type: tea.KeyEsc}) // no change: no save
	if calls != 0 {
		t.Fatal("saved without changes")
	}
	s.Update(kmsg("B"))
	s.Update(kmsg("d"))
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if calls != 1 || !strings.Contains(shown(s), "disk full") {
		t.Fatalf("calls=%d view:\n%s", calls, shown(s))
	}
}

func TestLoadingModalReplacedByData(t *testing.T) {
	f := &fakeLister{jobs: []jenkins.Job{{FullName: "a", Color: "blue"}}}
	s := New(f, "")
	s.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	if v := shown(s); !strings.Contains(v, "Loading jobs…") || !strings.Contains(v, "╭") {
		t.Fatalf("modal expected: %q", v)
	}
	cmd := s.Init()
	if !s.load.Active() {
		t.Fatal("spinner must run while loading")
	}
	feed(t, s, cmd)
	v := shown(s)
	if strings.Contains(v, "Loading") || !strings.Contains(v, "1 jobs") {
		t.Fatalf("data expected: %q", v)
	}
	if s.load.Active() {
		t.Fatal("spinner must stop after load")
	}
}

func TestRetryShowsModalThenStops(t *testing.T) {
	f := &fakeLister{jobs: []jenkins.Job{{FullName: "a"}}, errs: []error{errors.New("boom")}}
	s := New(f, "")
	feed(t, s, s.Init())
	_, cmd := s.Update(kmsg("r"))
	if v := shown(s); !strings.Contains(v, "Loading jobs…") || !s.load.Active() {
		t.Fatalf("retry must show the modal: %q", v)
	}
	feed(t, s, cmd)
	if s.load.Active() || !strings.Contains(shown(s), "1 jobs") {
		t.Fatal("retry did not complete")
	}
}

// shown is what the screen puts on the terminal: the subtitle on the title
// row (if any), then the body.
func shown(s core.Screen) string {
	if st, ok := s.(core.Subtitler); ok {
		return st.Subtitle() + "\n" + s.View()
	}
	return s.View()
}

func TestJobName(t *testing.T) {
	for _, tc := range []struct {
		job    jenkins.Job
		parent string
		want   string
	}{
		{jenkins.Job{FullName: "team/app/build"}, "team", "app/build"},
		{jenkins.Job{FullName: "team/ui/feature%2Fx", Multibranch: "team/ui", Branch: "feature/x"}, "team", "ui › feature/x"},
		{jenkins.Job{FullName: "ui/PR-2454", Multibranch: "ui", Branch: "PR-2454"}, "", "ui › PR-2454"},
		// the parent is the Multibranch Project itself: keep its name
		{jenkins.Job{FullName: "ui/develop", Multibranch: "ui", Branch: "develop"}, "ui", "ui › develop"},
		{jenkins.Job{FullName: "team/ui/develop", Multibranch: "team/ui", Branch: "develop"}, "team/ui", "ui › develop"},
	} {
		if got := JobName(tc.job, tc.parent).String(); got != tc.want {
			t.Errorf("JobName(%q, %q) = %q, want %q", tc.job.FullName, tc.parent, got, tc.want)
		}
	}
	r := Row(jenkins.Job{FullName: "ui/feature%2Fx", Multibranch: "ui", Branch: "feature/x"}, "", core.StatusSuccess)
	if r.Key != "ui/feature%2Fx" || r.Label != "ui" || r.Branch != "feature/x" {
		t.Errorf("Row = %+v (the key must stay the full, encoded name)", r)
	}
}

// branchRow matches a row showing name and branch in their own columns.
func branchRow(name, branch string) *regexp.Regexp {
	return regexp.MustCompile(`\[.\] ` + regexp.QuoteMeta(name) + `\s{2,}` + regexp.QuoteMeta(branch) + `(\s|$)`)
}

func TestSubtitleCountSkipsPlaceholder(t *testing.T) {
	// "lib" has no matching branch, so it is a placeholder row.
	s := newBranchScreen(t, []string{"main"}, nil)
	if sub := s.Subtitle(); !strings.Contains(sub, "2 jobs") {
		t.Errorf("no text filter: want 2 jobs (plain, app/main), got %q", sub)
	}
	s.Update(kmsg("/"))
	typeText(s, "lib")
	if v := shown(s); !strings.Contains(v, "no matching branches") {
		t.Fatalf("placeholder should match the filter:\n%s", v)
	}
	if sub := s.Subtitle(); !strings.Contains(sub, "0 of 2 jobs") {
		t.Errorf("text filter on placeholder only: want 0 of 2 jobs, got %q", sub)
	}
}

func TestMultibranchParentRowsAndFilter(t *testing.T) {
	mk := func(b string) jenkins.Job {
		return jenkins.Job{FullName: "team/app/" + b, Multibranch: "team/app", Branch: b}
	}
	f := &fakeCatalog{cat: jenkins.Catalog{
		Jobs:                []jenkins.Job{mk("main"), mk("feature%2Fx")},
		MultibranchProjects: []string{"team/app"},
	}}
	f.cat.Jobs[1].Branch = "feature/x"
	s := New(f, "team/app", WithBranchFilter(Settings{Patterns: []string{"main"}}))
	feed(t, s, s.Init())
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	v := shown(s)
	if !branchRow("app", "main").MatchString(v) || strings.Contains(v, "feature/x") {
		t.Errorf("want only app / main:\n%s", v)
	}
	s.Update(kmsg("b"))
	if v = shown(s); !branchRow("app", "feature/x").MatchString(v) {
		t.Errorf("filter off should list feature/x:\n%s", v)
	}
	s.Update(kmsg("b"))

	// No branch matches: the placeholder names the project, not an empty row.
	f2 := &fakeCatalog{cat: jenkins.Catalog{Jobs: []jenkins.Job{mk("dev")}, MultibranchProjects: []string{"team/app"}}}
	s2 := New(f2, "team/app", WithBranchFilter(Settings{Patterns: []string{"main"}}))
	feed(t, s2, s2.Init())
	s2.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if v := shown(s2); !branchRow("app", "(no matching branches)").MatchString(v) {
		t.Errorf("missing placeholder:\n%s", v)
	}
}

type recOpener struct {
	urls []string
	err  error
}

func (r *recOpener) Open(u string) error { r.urls = append(r.urls, u); return r.err }

func openScreen(t *testing.T, o Opener, jobs ...jenkins.Job) *Screen {
	t.Helper()
	f := &fakeCatalog{cat: jenkins.Catalog{Jobs: jobs, MultibranchProjects: []string{"empty"}}}
	s := New(f, "", WithOpener(o))
	feed(t, s, s.Init())
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return s
}

// press sends key k and runs the returned command, delivering its result.
func press(s *Screen, k string) {
	_, cmd := s.Update(kmsg(k))
	if cmd != nil {
		s.Update(cmd())
	}
}

func TestOpenKeyOpensHighlightedJob(t *testing.T) {
	o := &recOpener{}
	s := openScreen(t, o, jenkins.Job{FullName: "a", URL: "http://j/job/a/"}, jenkins.Job{FullName: "b", URL: "http://j/job/b/"})
	press(s, "o")
	press(s, "down")
	press(s, "o")
	if len(o.urls) != 2 || o.urls[0] != "http://j/job/a/" || o.urls[1] != "http://j/job/b/" {
		t.Fatalf("opened %v", o.urls)
	}
	if v := shown(s); !strings.Contains(v, "opened in browser") {
		t.Errorf("no success notice:\n%s", v)
	}
	if !strings.Contains(helpText(s), "open in browser") {
		t.Error("help lacks o")
	}
}

func TestOpenKeyPlaceholderAndMissingURLDoNothing(t *testing.T) {
	o := &recOpener{}
	s := openScreen(t, o, jenkins.Job{FullName: "nourl"})
	press(s, "o") // job without URL
	for i := 0; i < 3; i++ {
		if it, ok := s.list.Cursor(); ok && it.Disabled {
			break
		}
		press(s, "down")
	}
	if it, ok := s.list.Cursor(); !ok || !it.Disabled {
		t.Fatalf("cursor not on the placeholder row: %+v", it)
	}
	press(s, "o")
	if len(o.urls) != 0 {
		t.Fatalf("opened %v", o.urls)
	}
}

func TestOpenKeyErrorShowsNotice(t *testing.T) {
	o := &recOpener{err: errors.New("sysint: no display")}
	s := openScreen(t, o, jenkins.Job{FullName: "a", URL: "http://j/job/a/"})
	press(s, "o")
	if v := shown(s); !strings.Contains(v, "could not open: no display") {
		t.Errorf("no error notice:\n%s", v)
	}
}

func TestOpenKeyHiddenWithoutOpener(t *testing.T) {
	f := &fakeCatalog{cat: jenkins.Catalog{Jobs: []jenkins.Job{{FullName: "a", URL: "http://j/"}}}}
	s := New(f, "")
	feed(t, s, s.Init())
	if strings.Contains(helpText(s), "open in browser") {
		t.Error("o shown without an opener")
	}
	_, cmd := s.Update(kmsg("o"))
	if cmd != nil {
		t.Error("o acted without an opener")
	}
}

func helpText(s *Screen) string {
	var b strings.Builder
	for _, k := range s.Keys() {
		b.WriteString(k.Help().Desc + "\n")
	}
	for _, g := range s.FullHelp() {
		for _, k := range g.Keys {
			b.WriteString(k.Help().Desc + "\n")
		}
	}
	return b.String()
}

func TestOpenKeyTypesInFilterBox(t *testing.T) {
	o := &recOpener{}
	s := openScreen(t, o, jenkins.Job{FullName: "foo", URL: "http://j/job/foo/"})
	press(s, "/")
	press(s, "o")
	if len(o.urls) != 0 || s.list.Filter() != "o" {
		t.Fatalf("opened %v, filter %q", o.urls, s.list.Filter())
	}
}
