package collections

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/config"
	"github.com/dursunkoc/jenktui/internal/jenkins"
	"github.com/dursunkoc/jenktui/internal/ui/catalog"
	"github.com/dursunkoc/jenktui/internal/ui/core"
)

type fakeLister struct{ cat jenkins.Catalog }

func (f *fakeLister) ListJobs(context.Context, string) ([]jenkins.Job, error) { return f.cat.Jobs, nil }
func (f *fakeLister) ListCatalog(context.Context, string) (jenkins.Catalog, error) {
	return f.cat, nil
}

type stubScreen struct{ core.Screen }

type monCall struct {
	name    string
	jobs    []string
	missing map[string]bool
}

type rig struct {
	t         *testing.T
	path      string
	deps      Deps
	triggered [][]string
	trigNames []string
	monitored []monCall
	renamed   [][2]string
	saved     [][]string
}

func newRig(t *testing.T, cat jenkins.Catalog, cols ...config.Collection) *rig {
	t.Helper()
	r := &rig{t: t, path: filepath.Join(t.TempDir(), "collections.toml")}
	if len(cols) > 0 {
		if err := config.SaveCollections(r.path, &config.Collections{Items: cols}); err != nil {
			t.Fatal(err)
		}
	}
	r.deps = Deps{Lister: &fakeLister{cat}, Path: r.path,
		Trigger: func(name string, keys []string, _ map[string]core.JobName) core.Screen {
			r.trigNames = append(r.trigNames, name)
			r.triggered = append(r.triggered, keys)
			return stubScreen{}
		},
		Monitor: func(name string, jobs []string, missing map[string]bool, _ map[string]core.JobName) core.Screen {
			r.monitored = append(r.monitored, monCall{name, jobs, missing})
			return stubScreen{}
		},
		Renamed:      func(o, n string) { r.renamed = append(r.renamed, [2]string{o, n}) },
		BranchFilter: catalog.Settings{Save: func(p []string) error { r.saved = append(r.saved, p); return nil }}}
	return r
}

func (r *rig) file() *config.Collections {
	cs, err := config.LoadCollections(r.path)
	if err != nil {
		r.t.Fatal(err)
	}
	return cs
}

// run executes cmd, feeding results back into s; returns navigation messages.
func run(s core.Screen, cmd tea.Cmd) (core.Screen, []tea.Msg) {
	var navs []tea.Msg
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch m := c().(type) {
		case tea.BatchMsg:
			for _, cc := range m {
				walk(cc)
			}
		case loadedMsg:
			var c2 tea.Cmd
			s, c2 = s.Update(m)
			walk(c2)
		case core.Push, core.Pop, core.Quit:
			navs = append(navs, m)
		}
	}
	walk(cmd)
	return s, navs
}

func send(s core.Screen, keys ...string) (core.Screen, []tea.Msg) {
	var all []tea.Msg
	for _, k := range keys {
		var cmd tea.Cmd
		s, cmd = s.Update(kmsg(k))
		var n []tea.Msg
		s, n = run(s, cmd)
		all = append(all, n...)
	}
	return s, all
}

func kmsg(k string) tea.KeyMsg {
	switch k {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "shift+up":
		return tea.KeyMsg{Type: tea.KeyShiftUp}
	case "shift+down":
		return tea.KeyMsg{Type: tea.KeyShiftDown}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func typeText(s core.Screen, text string) core.Screen {
	for _, r := range text {
		s, _ = s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return s
}

// open initialises a screen and delivers its load result.
func open(s core.Screen) core.Screen {
	s, _ = run(s, s.Init())
	s, _ = s.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return s
}

func job(name, color string) jenkins.Job { return jenkins.Job{FullName: name, Color: color} }

func pushed(t *testing.T, navs []tea.Msg) core.Screen {
	t.Helper()
	for _, n := range navs {
		if p, ok := n.(core.Push); ok {
			return p.Screen
		}
	}
	t.Fatalf("no Push in %v", navs)
	return nil
}

func TestMissingNeverDroppedOnEdit(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", ""), job("b", "")}},
		config.Collection{Name: "C", Jobs: []string{"gone", "a"}})
	s := New(r.deps)
	s, navs := send(s, "e")
	p := open(pushed(t, navs))
	p, _ = send(p, "down", "space") // list: a, b -> cursor b; check b
	send(p, "enter")
	if got := r.file().Items[0].Jobs; !slices.Equal(got, []string{"gone", "a", "b"}) {
		t.Fatalf("got %v", got)
	}
}

func TestPickerBranchFilterKeepsHiddenMembers(t *testing.T) {
	br := func(b string) jenkins.Job {
		return jenkins.Job{FullName: "app/" + strings.ReplaceAll(b, "/", "%2F"), Multibranch: "app", Branch: b}
	}
	cat := jenkins.Catalog{
		Jobs:                []jenkins.Job{br("main"), br("feature/x"), br("feature/y"), job("zzz", "")},
		MultibranchProjects: []string{"app"},
	}
	r := newRig(t, cat, config.Collection{Name: "C", Jobs: []string{"app/feature%2Fx"}})
	r.deps.BranchFilter.Patterns = []string{"main"}
	s := New(r.deps)
	s, navs := send(s, "e")
	p := open(pushed(t, navs))
	if v := shown(p); strings.Contains(v, "feature/x") || !strings.Contains(v, "branches: filtered") {
		t.Fatalf("filter not applied: %q", v)
	}
	// rows: app/main, zzz. Select main, then save: hidden member must remain.
	p, _ = send(p, "space")
	p, _ = send(p, "enter")
	_ = p
	if got := r.file().Items[0].Jobs; !slices.Equal(got, []string{"app/feature%2Fx", "app/main"}) {
		t.Fatalf("hidden member dropped: %v", got)
	}

	// Toggle off: member is shown pre-checked; uncheck it and save.
	s, navs = send(s, "e")
	p = open(pushed(t, navs))
	p, _ = send(p, "b")
	if v := shown(p); !regexp.MustCompile(`\[.\] app\s+feature/x\s`).MatchString(v) || !strings.Contains(v, "branches: all") {
		t.Fatalf("toggle off: %q", v)
	}
	// rows sorted: app/feature%2Fx, app/feature%2Fy, app/main, zzz
	p, _ = send(p, "space")
	send(p, "enter")
	if got := r.file().Items[0].Jobs; !slices.Equal(got, []string{"app/main"}) {
		t.Fatalf("after uncheck: %v", got)
	}
}

func TestPickerSelectionSurvivesFilterToggle(t *testing.T) {
	cat := jenkins.Catalog{Jobs: []jenkins.Job{
		{FullName: "app/main", Multibranch: "app", Branch: "main"},
		{FullName: "app/dev", Multibranch: "app", Branch: "dev"},
	}, MultibranchProjects: []string{"app"}}
	r := newRig(t, cat, config.Collection{Name: "C"})
	r.deps.BranchFilter.Patterns = []string{"main"}
	s := New(r.deps)
	s, navs := send(s, "e")
	p := open(pushed(t, navs))
	p, _ = send(p, "b") // all branches: app/dev, app/main
	p, _ = send(p, "space")
	p, _ = send(p, "b") // hides dev again
	p, _ = send(p, "b")
	send(p, "enter")
	if got := r.file().Items[0].Jobs; !slices.Equal(got, []string{"app/dev"}) {
		t.Fatalf("got %v", got)
	}
}

func TestPickerCancelAndBranchEditorSave(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "")}}, config.Collection{Name: "C"})
	s := New(r.deps)
	s, navs := send(s, "e")
	p := open(pushed(t, navs))
	p, _ = send(p, "space")
	_, navs = send(p, "esc")
	if _, ok := navs[0].(core.Pop); !ok || len(r.file().Items[0].Jobs) != 0 {
		t.Fatal("esc must cancel without saving")
	}
	p = open(pushed(t, func() []tea.Msg { _, n := send(s, "e"); return n }()))
	p, _ = send(p, "B", "a")
	p = typeText(p, "main")
	p, _ = send(p, "enter", "enter")
	if len(r.saved) != 1 || r.saved[0][0] != "main" {
		t.Fatalf("saved patterns: %v", r.saved)
	}
	// A later picker sees the saved patterns.
	_, navs = send(s, "e")
	p2 := open(pushed(t, navs))
	if !strings.Contains(shown(p2), "filtered (1 pattern)") {
		t.Fatalf("patterns not shared: %q", shown(p2))
	}
}

func TestPickerShowsModalWhileLoading(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue")}})
	p := newPicker(&env{deps: r.deps, theme: core.NewTheme()}, "New x", nil, nil)
	if out := shown(p); !strings.Contains(out, "Loading jobs…") {
		t.Fatalf("%q", out)
	}
	if !p.Busy() {
		t.Fatal("a loading picker must report Busy")
	}
	p2 := open(p)
	if strings.Contains(shown(p2), "Loading") || p.load.Active() {
		t.Fatal("picker still loading")
	}
	if p.Busy() {
		t.Fatal("a loaded picker is not Busy")
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

func TestBadgeUnstableAndAborted(t *testing.T) {
	for c, want := range map[string]core.Status{"yellow": core.StatusUnstable, "aborted": core.StatusAborted, "red": core.StatusFailed,
		"yellow_anime": core.StatusRunning, "aborted_anime": core.StatusRunning, "grey": core.StatusUnknown, "disabled": core.StatusUnknown} {
		if got := badge(c); got != want {
			t.Errorf("badge(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestPickerCountSkipsPlaceholder(t *testing.T) {
	cat := jenkins.Catalog{
		Jobs: []jenkins.Job{
			{FullName: "app/dev", Multibranch: "app", Branch: "dev"},
			job("zzz", ""),
		},
		MultibranchProjects: []string{"app"},
	}
	r := newRig(t, cat, config.Collection{Name: "C"})
	r.deps.BranchFilter.Patterns = []string{"main"}
	s := New(r.deps)
	_, navs := send(s, "e")
	p := open(pushed(t, navs))
	v := shown(p)
	if !strings.Contains(v, "no matching branches") || !strings.Contains(v, "1 jobs ·") {
		t.Fatalf("want placeholder row and 1 jobs: %q", v)
	}
}

type recOpener struct {
	urls []string
	err  error
}

func (r *recOpener) Open(u string) error { r.urls = append(r.urls, u); return r.err }

// pressOpen sends o and delivers the opener's result to the view.
func pressOpen(s core.Screen) core.Screen {
	s, cmd := s.Update(kmsg("o"))
	if cmd != nil {
		s, _ = s.Update(cmd())
	}
	return s
}

// ---- the tree ----

func nested() []config.Collection {
	return []config.Collection{{
		Name: "backend", Jobs: []string{"own"},
		Folders: []config.Collection{
			{Name: "develop", Jobs: []string{"a", "b"}, Folders: []config.Collection{{Name: "deep", Jobs: []string{"c"}}}},
			{Name: "empty"},
		},
	}, {Name: "libs", Jobs: []string{"l"}}}
}

func nestedCat() jenkins.Catalog {
	return jenkins.Catalog{Jobs: []jenkins.Job{job("own", "blue"), job("a", "blue"), job("b", "red"), job("c", "blue_anime"), job("l", "blue")}}
}

func tree(t *testing.T, r *rig) *treeScreen {
	t.Helper()
	return open(New(r.deps)).(*treeScreen)
}

func rowNames(s *treeScreen) []string {
	var out []string
	for _, r := range s.rows {
		out = append(out, strings.Repeat(".", r.depth)+r.name)
	}
	return out
}

func TestTreeStartsCollapsedAndExpandsInPlace(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := tree(t, r)
	if got := rowNames(s); !slices.Equal(got, []string{"backend", "libs"}) {
		t.Fatalf("collapsed: %v", got)
	}
	if v := shown(s); !strings.Contains(v, "▸ backend") || !strings.Contains(v, "3 folders · 4 jobs") {
		t.Fatalf("view: %q", v)
	}
	// right opens; sub-folders first, then the folder's own jobs
	var scr core.Screen = s
	scr, _ = send(scr, "right")
	if got := rowNames(scr.(*treeScreen)); !slices.Equal(got, []string{"backend", ".develop", ".empty", ".own", "libs"}) {
		t.Fatalf("expanded: %v", got)
	}
	if !strings.Contains(shown(scr), "▾ backend") {
		t.Fatal("no open marker")
	}
	// right on an open folder goes to its first child; right opens it
	scr, _ = send(scr, "right", "right")
	if got := rowNames(scr.(*treeScreen)); !slices.Equal(got, []string{"backend", ".develop", "..deep", "..a", "..b", ".empty", ".own", "libs"}) {
		t.Fatalf("deep: %v", got)
	}
}

func TestTreeLeftGoesToParent(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := tree(t, r)
	s.expanded["backend"], s.expanded["backend/develop"] = true, true
	s.build()
	var scr core.Screen = s
	scr, _ = send(scr, "down", "down", "down") // develop, deep, a
	if r, _ := scr.(*treeScreen).current(); r.key != "a" {
		t.Fatalf("cursor %+v", r)
	}
	scr, _ = send(scr, "left")
	if r, _ := scr.(*treeScreen).current(); !r.folder || r.path != "backend/develop" {
		t.Fatalf("left on a job: %+v", r)
	}
	scr, _ = send(scr, "left") // closes develop
	if len(scr.(*treeScreen).rows) != 5 {
		t.Fatalf("rows %v", rowNames(scr.(*treeScreen)))
	}
	scr, _ = send(scr, "left") // closed: to the parent
	if r, _ := scr.(*treeScreen).current(); r.path != "backend" {
		t.Fatalf("left on a closed folder: %+v", r)
	}
}

func TestTreeExpansionLivesInTheSharedMap(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	r.deps.Expanded = map[string]bool{}
	s := open(New(r.deps))
	send(s, "right")
	again := open(New(r.deps)).(*treeScreen)
	if len(again.rows) != 5 {
		t.Fatalf("a new visit lost the open folder: %v", rowNames(again))
	}
}

func TestTreeShowsStatusAndMissing(t *testing.T) {
	r := newRig(t, nestedCat(), config.Collection{Name: "C", Jobs: []string{"a", "gone", "b"}})
	s := tree(t, r)
	send(s, "right")
	v := shown(s)
	if !strings.Contains(v, "SUCCESS") || !strings.Contains(v, "FAILED") || !strings.Contains(v, "MISSING") {
		t.Fatalf("badges: %q", v)
	}
}

func TestNewFolderIsEmptyAndTakesASubFolderStraightAway(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := open(New(r.deps))
	s, _ = send(s, "N")
	s = typeText(s, "ops")
	s, navs := send(s, "enter")
	if len(navs) != 0 {
		t.Fatal("creating a folder must not open the job picker")
	}
	if c := r.file().Get("ops"); c == nil || len(c.Jobs) != 0 {
		t.Fatalf("file: %v", r.file().Paths())
	}
	if cur, _ := s.(*treeScreen).current(); cur.path != "ops" {
		t.Fatalf("cursor should land on the new folder: %+v", cur)
	}
	// and now a folder inside it, with the cursor still on ops
	s, _ = send(s, "n")
	s = typeText(s, "child")
	s, _ = send(s, "enter")
	if r.file().Get("ops/child") == nil {
		t.Fatalf("sub-folder: %v", r.file().Paths())
	}
	if cur, _ := s.(*treeScreen).current(); cur.path != "ops/child" || !strings.Contains(shown(s), "child") {
		t.Fatalf("new sub-folder should show, open under its parent: %+v", cur)
	}
	// a duplicate sibling is refused: back on ops, "CHILD" clashes with child
	s, _ = send(s, "up", "n")
	s = typeText(s, "CHILD")
	s, _ = send(s, "enter")
	if !strings.Contains(shown(s), "already exists") {
		t.Fatalf("dup: %q", shown(s))
	}
}

func TestNewOnAJobCreatesInItsFolder(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := tree(t, r)
	s.expanded["backend"] = true
	s.build()
	var scr core.Screen = s
	scr, _ = send(scr, "down", "down", "down") // develop, empty, own
	if cur, _ := scr.(*treeScreen).current(); cur.key != "own" {
		t.Fatalf("cursor %+v", cur)
	}
	scr, _ = send(scr, "n")
	scr = typeText(scr, "x")
	send(scr, "enter")
	if r.file().Get("backend/x") == nil {
		t.Fatalf("%v", r.file().Paths())
	}
}

func TestSpaceOnAFolderSelectsItsSubtreeAndTriggersIt(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := open(New(r.deps))
	s, _ = send(s, "space")
	if v := shown(s); !strings.Contains(v, "[x] ▸ backend") || !strings.Contains(v, "4 selected") {
		t.Fatalf("view: %q", v)
	}
	s, navs := send(s, "t")
	if len(navs) != 1 || len(r.triggered) != 1 {
		t.Fatalf("navs=%v trig=%v", navs, r.triggered)
	}
	if got := r.triggered[0]; len(got) != 4 || r.trigNames[0] != "backend" {
		t.Fatalf("triggered %v for %v", got, r.trigNames)
	}
	s, _ = send(s, "space") // again clears
	if strings.Contains(shown(s), "selected") {
		t.Fatalf("not cleared: %q", shown(s))
	}
}

func TestTriggerTargetIsTheDeepestCommonFolderElseSelection(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := tree(t, r)
	s.expanded["backend"], s.expanded["backend/develop"] = true, true
	s.build()
	var scr core.Screen = s
	// a and b are both in develop
	scr, _ = send(scr, "down", "down", "down", "down", "space", "down", "space", "t") // develop, deep, c... see below
	_ = scr
	if len(r.triggered) == 0 {
		t.Fatal("no trigger")
	}
	if r.trigNames[0] != "backend/develop" {
		t.Fatalf("target %q for %v", r.trigNames[0], r.triggered[0])
	}
	// a (backend) and l (libs): no common folder
	s2 := tree(t, r)
	s2.selected["a"], s2.selected["l"] = true, true
	send(s2, "t")
	if got := r.trigNames[len(r.trigNames)-1]; got != SelectionName {
		t.Fatalf("target %q", got)
	}
}

func TestTriggerOnAJobUsesItsFolderAndSkipsMissing(t *testing.T) {
	r := newRig(t, nestedCat(), config.Collection{Name: "C", Jobs: []string{"gone", "a"}})
	var s core.Screen = tree(t, r)
	s, _ = send(s, "right", "down") // open C, cursor on gone
	s, navs := send(s, "t")
	if len(navs) != 0 || !strings.Contains(shown(s), "nothing to trigger") {
		t.Fatalf("navs=%v %q", navs, shown(s))
	}
	s, navs = send(s, "down", "t")
	if len(navs) != 1 || r.trigNames[0] != "C" || !slices.Equal(r.triggered[0], []string{"a"}) {
		t.Fatalf("%v %v %v", navs, r.trigNames, r.triggered)
	}
}

func TestMonitorCoversTheSubtree(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := open(New(r.deps))
	_, navs := send(s, "m")
	if len(navs) != 1 || len(r.monitored) != 1 {
		t.Fatalf("navs=%v", navs)
	}
	if got := r.monitored[0]; got.name != "backend" || !slices.Equal(got.jobs, []string{"c", "a", "b", "own"}) {
		t.Fatalf("monitor %+v", got)
	}
}

func TestMonitorUnavailable(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	r.deps.Monitor = nil
	s := open(New(r.deps))
	s, navs := send(s, "m")
	if len(navs) != 0 || !strings.Contains(shown(s), "monitor unavailable") {
		t.Fatalf("%v %q", navs, shown(s))
	}
}

func TestRenameFolderAndDeleteWithCounts(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := open(New(r.deps))
	s, _ = send(s, "r")
	s = typeText(s, "2")
	s, _ = send(s, "enter")
	if r.file().Get("backend2") == nil || r.renamed[len(r.renamed)-1] != [2]string{"backend", "backend2"} {
		t.Fatalf("rename: %v %v", r.file().Paths(), r.renamed)
	}
	s, _ = send(s, "d")
	if v := shown(s); !strings.Contains(v, `Delete "backend2" with 3 folders and 4 jobs?`) {
		t.Fatalf("question: %q", v)
	}
	s, _ = send(s, "n")
	if len(r.file().Items) != 2 {
		t.Fatal("n must keep")
	}
	send(s, "d", "y")
	if len(r.file().Items) != 1 {
		t.Fatalf("subtree not deleted: %v", r.file().Paths())
	}
}

func TestRemoveAJobFromItsFolder(t *testing.T) {
	r := newRig(t, nestedCat(), config.Collection{Name: "C", Jobs: []string{"gone", "a"}})
	var s core.Screen = tree(t, r)
	s, _ = send(s, "right", "down", "d")
	if !strings.Contains(shown(s), "Remove gone from C?") {
		t.Fatalf("%q", shown(s))
	}
	s, _ = send(s, "y")
	if got := r.file().Get("C").Jobs; !slices.Equal(got, []string{"a"}) {
		t.Fatalf("%v", got)
	}
}

func TestReorderStaysInsideTheFolder(t *testing.T) {
	r := newRig(t, nestedCat(), config.Collection{Name: "C", Jobs: []string{"a", "b", "c"}}, config.Collection{Name: "D"})
	var s core.Screen = tree(t, r)
	s, _ = send(s, "right", "down", "down", "shift+up")
	if got := r.file().Get("C").Jobs; !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Fatalf("%v", got)
	}
	s, _ = send(s, "shift+up", "shift+up") // at the top already: stays in the folder
	if got := r.file().Get("C").Jobs; len(got) != 3 || got[0] != "b" && got[0] != "a" || got[1] == "c" && got[2] != "c" {
		t.Fatalf("%v", got)
	}
	s, _ = send(s, "up", "up", "up", "shift+down") // the folder C moves below D
	if got := r.file().Paths(); !slices.Equal(got, []string{"D", "C"}) {
		t.Fatalf("%v", got)
	}
	_ = s
}

func TestMoveAndCopyJobs(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := tree(t, r)
	s.expanded["backend"], s.expanded["backend/develop"] = true, true
	s.build()
	var scr core.Screen = s
	scr, _ = send(scr, "down", "down", "down", "space") // a selected
	_, navs := send(scr, "C")
	dest := open(pushed(t, navs))
	// rows: backend, develop, deep, empty, libs
	send(dest, "down", "down", "down", "down", "enter")
	if !slices.Equal(r.file().Get("libs").Jobs, []string{"l", "a"}) || !slices.Contains(r.file().Get("backend/develop").Jobs, "a") {
		t.Fatalf("copy: %+v", r.file())
	}
	_, navs = send(scr, "M")
	dest = open(pushed(t, navs))
	send(dest, "down", "down", "down", "enter") // empty
	if !slices.Equal(r.file().Get("backend/empty").Jobs, []string{"a"}) || slices.Contains(r.file().Get("backend/develop").Jobs, "a") {
		t.Fatalf("move: %+v", r.file())
	}
}

func TestMoveFolderRefusesItsOwnSubtree(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := open(New(r.deps))
	_, navs := send(s, "M")
	dest := open(pushed(t, navs))
	dest, _ = send(dest, "down", "down", "enter") // develop: under backend
	if !strings.Contains(shown(dest), "cannot") || r.file().Get("backend/develop/backend") != nil {
		t.Fatalf("not refused: %q", shown(dest))
	}
	dest, _ = send(dest, "up", "up", "up", "up") // (top level) ... then libs
	_ = dest
	_, navs = send(s, "down", "M")
	d2 := open(pushed(t, navs))
	send(d2, "down", "enter") // into backend
	if r.file().Get("backend/libs") == nil {
		t.Fatalf("move: %v", r.file().Paths())
	}
}

func TestFilterShowsMatchesWithTheirParentsAndRestoresState(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	s := open(New(r.deps))
	s, _ = send(s, "/")
	s = typeText(s, "deep")
	if got := rowNames(s.(*treeScreen)); !slices.Equal(got, []string{"backend", ".develop", "..deep"}) {
		t.Fatalf("filtered: %v", got)
	}
	s = typeText(s, "x") // nothing matches
	if !strings.Contains(shown(s), "no matches") {
		t.Fatalf("%q", shown(s))
	}
	s, _ = send(s, "esc")
	if got := rowNames(s.(*treeScreen)); !slices.Equal(got, []string{"backend", "libs"}) {
		t.Fatalf("not restored: %v", got)
	}
	// a job match shows the job under open parents
	s, _ = send(s, "/")
	s = typeText(s, "own")
	if got := rowNames(s.(*treeScreen)); !slices.Equal(got, []string{"backend", ".own"}) {
		t.Fatalf("job match: %v", got)
	}
}

func TestOpenKeyOnAJob(t *testing.T) {
	o := &recOpener{}
	r := newRig(t, nestedCat(), config.Collection{Name: "C", Jobs: []string{"a", "gone"}})
	r.deps.Opener = o
	r.deps.JobURL = func(n string) string { return "http://j/job/" + n + "/" }
	var s core.Screen = tree(t, r)
	s, _ = send(s, "right", "down")
	scr := s
	scr = pressOpen(scr)
	if len(o.urls) != 1 || o.urls[0] != "http://j/job/a/" {
		t.Fatalf("opened %v", o.urls)
	}
	scr, _ = send(scr, "down")
	scr = pressOpen(scr) // missing
	if len(o.urls) != 1 || !strings.Contains(shown(scr), "not in Jenkins") {
		t.Fatalf("%v %q", o.urls, shown(scr))
	}
	o.err = errors.New("sysint: no display")
	scr, _ = send(scr, "up")
	scr = pressOpen(scr)
	if !strings.Contains(shown(scr), "could not open: no display") {
		t.Fatalf("%q", shown(scr))
	}
}

func TestFolderRowsHaveNoOpenKey(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	r.deps.Opener = &recOpener{}
	r.deps.JobURL = func(n string) string { return n }
	s := open(New(r.deps))
	if _, cmd := s.Update(kmsg("o")); cmd != nil {
		t.Fatal("o acted on a folder")
	}
}

func TestGoIntoAFolderShowsOnlyItsContentsAndEscLeaves(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	var s core.Screen = tree(t, r)
	s, _ = send(s, "enter") // into backend: its sub-folders, then its own jobs, from the top
	ts := s.(*treeScreen)
	if got := rowNames(ts); !slices.Equal(got, []string{"develop", "empty", "own"}) {
		t.Fatalf("inside backend: %v", got)
	}
	if got := ts.Title(); !strings.HasSuffix(got, "backend") {
		t.Fatalf("title %q", got)
	}
	// expanding still works inside, and a deeper go-into stacks
	s, _ = send(s, "right")
	if got := rowNames(s.(*treeScreen)); !slices.Equal(got, []string{"develop", ".deep", ".a", ".b", "empty", "own"}) {
		t.Fatalf("expanded inside: %v", got)
	}
	s, _ = send(s, "enter")
	ts = s.(*treeScreen)
	if got := rowNames(ts); !slices.Equal(got, []string{"deep", "a", "b"}) || !strings.HasSuffix(ts.Title(), "backend › develop") {
		t.Fatalf("inside develop: %v %q", got, ts.Title())
	}
	// esc leaves one level at a time, landing on the folder left, then goes back
	s, navs := send(s, "esc")
	if cur, _ := s.(*treeScreen).current(); cur.path != "backend/develop" || len(navs) != 0 {
		t.Fatalf("cursor %+v navs %v", cur, navs)
	}
	s, navs = send(s, "esc")
	if got := rowNames(s.(*treeScreen)); !slices.Equal(got, []string{"backend", "libs"}) || len(navs) != 0 {
		t.Fatalf("top: %v", got)
	}
	_, navs = send(s, "esc")
	if _, ok := navs[len(navs)-1].(core.Pop); !ok {
		t.Fatal("esc at the top should leave the screen")
	}
}

func TestInsideAFolderNewGoesThereAndAnEmptyOneTakesJobs(t *testing.T) {
	r := newRig(t, nestedCat(), nested()...)
	var s core.Screen = tree(t, r)
	s, _ = send(s, "enter", "down", "enter") // into backend, then into its empty folder
	if got := rowNames(s.(*treeScreen)); len(got) != 0 {
		t.Fatalf("empty folder shows rows: %v", got)
	}
	if !strings.Contains(shown(s), "This folder is empty") {
		t.Fatalf("%q", shown(s))
	}
	s, _ = send(s, "n")
	s = typeText(s, "kid")
	s, _ = send(s, "enter")
	if r.file().Get("backend/empty/kid") == nil {
		t.Fatalf("%v", r.file().Paths())
	}
	s, _ = send(s, "esc", "esc") // out of empty, out of backend; then N at the top level
	s, _ = send(s, "N")
	s = typeText(s, "top")
	send(s, "enter")
	if r.file().Get("top") == nil {
		t.Fatalf("%v", r.file().Paths())
	}
}

func TestBreadcrumbCollapsesMiddle(t *testing.T) {
	names := []string{"one", "two", "three", "four", "five"}
	if got := Breadcrumb(names, 100); got != "one › two › three › four › five" {
		t.Fatal(got)
	}
	if got := Breadcrumb(names, 10); got != "one › … › four › five" {
		t.Fatal(got)
	}
	if got := Breadcrumb(names[:2], 1); got != "one › two" {
		t.Fatal(got)
	}
}
