package collections

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
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

func TestCreateCollectionFlow(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue"), job("b", "red"), job("c", "")}},
		config.Collection{Name: "Existing"})
	s := New(r.deps)
	s, _ = send(s, "n")
	s = typeText(s, "existing")
	s, navs := send(s, "enter")
	if len(navs) != 0 || !strings.Contains(shown(s), "already exists") {
		t.Fatalf("duplicate name (case-insensitive) must be rejected; view=%q", shown(s))
	}
	for range "existing" {
		s, _ = s.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	s = typeText(s, "Mine")
	s, navs = send(s, "enter")
	p := open(pushed(t, navs))
	p, _ = send(p, "space", "down", "down", "space") // a and c
	_, navs = send(p, "enter")
	if _, ok := navs[len(navs)-1].(core.Pop); !ok {
		t.Fatalf("enter should pop: %v", navs)
	}
	cs := r.file()
	if i := cs.Find("Mine"); i < 0 || !slices.Equal(cs.Items[i].Jobs, []string{"a", "c"}) {
		t.Fatalf("saved: %+v", cs)
	}
	if !strings.Contains(shown(s), "Mine") || !strings.Contains(shown(s), "2 jobs") {
		t.Fatalf("list not refreshed: %q", shown(s))
	}
}

func TestRenameAndDelete(t *testing.T) {
	r := newRig(t, jenkins.Catalog{}, config.Collection{Name: "One"}, config.Collection{Name: "Two", Jobs: []string{"x"}})
	s := New(r.deps)
	s, _ = send(s, "r")
	s = typeText(s, "!")
	s, _ = send(s, "enter")
	if r.file().Find("One!") < 0 {
		t.Fatalf("rename failed: %+v", r.file())
	}
	s, _ = send(s, "d")
	if !strings.Contains(shown(s), "Delete") {
		t.Fatal("no confirmation")
	}
	s, _ = send(s, "n")
	if len(r.file().Items) != 2 {
		t.Fatal("n must keep")
	}
	s, _ = send(s, "d", "y")
	if cs := r.file(); len(cs.Items) != 1 || cs.Items[0].Name != "Two" {
		t.Fatalf("after delete: %+v", cs)
	}
	_ = s
}

func TestViewStatusMissingReorderTrigger(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue"), job("b", "red"), job("c", "blue_anime")}},
		config.Collection{Name: "C", Jobs: []string{"a", "gone", "b", "c"}})
	s := New(r.deps)
	s, navs := send(s, "enter")
	v := open(pushed(t, navs))
	out := shown(v)
	if !strings.Contains(out, "MISSING") || !strings.Contains(out, "1 missing") {
		t.Fatalf("missing badge absent: %q", out)
	}

	// Trigger with cursor on a: only a.
	v, navs = send(v, "t")
	if len(r.triggered) != 1 || !slices.Equal(r.triggered[0], []string{"a"}) {
		t.Fatalf("triggered %v", r.triggered)
	}
	if _, ok := navs[0].(core.Push); !ok || !strings.Contains(shown(v), "skipped missing: gone") {
		t.Fatalf("navs=%v view=%q", navs, shown(v))
	}

	// Select b and c via 'a' (all skips missing); triggers b, c in order; a too.
	v, _ = send(v, "a", "t")
	if got := r.triggered[1]; !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("triggered %v", got)
	}
	v, _ = send(v, "n") // none

	// Cursor on missing: nothing to trigger, no push.
	v, _ = send(v, "down")
	v, navs = send(v, "t")
	if len(navs) != 0 || len(r.triggered) != 2 || !strings.Contains(shown(v), "nothing to trigger") {
		t.Fatalf("navs=%v view=%q", navs, shown(v))
	}

	// Remove missing explicitly.
	v, _ = send(v, "x")
	if got := r.file().Items[0].Jobs; !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("after x: %v", got)
	}

	// Reorder: cursor on b (index 1), move up.
	v, _ = send(v, "shift+up")
	if got := r.file().Items[0].Jobs; !slices.Equal(got, []string{"b", "a", "c"}) {
		t.Fatalf("after reorder: %v", got)
	}
	if !strings.Contains(shown(s), "3 jobs") {
		t.Fatalf("list count not refreshed: %q", shown(s))
	}
	_, navs = send(v, "esc")
	if _, ok := navs[0].(core.Pop); !ok {
		t.Fatal("esc should pop")
	}
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

func TestMonitorKeyFromOpenedCollection(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue"), job("b", "red")}},
		config.Collection{Name: "C", Jobs: []string{"a", "gone", "b"}})
	s := New(r.deps)
	s, navs := send(s, "enter")
	v := open(pushed(t, navs))
	if _, navs = send(v, "m"); len(navs) != 1 || len(r.monitored) != 1 {
		t.Fatalf("navs=%v monitored=%v", navs, r.monitored)
	}
	got := r.monitored[0]
	if got.name != "C" || !slices.Equal(got.jobs, []string{"a", "gone", "b"}) || !got.missing["gone"] || got.missing["a"] {
		t.Fatalf("monitor args %+v", got)
	}
	// Triggering carries the Collection's name.
	send(v, "t")
	if len(r.trigNames) != 1 || r.trigNames[0] != "C" {
		t.Fatalf("trigger collection %v", r.trigNames)
	}
	_ = s
}

func TestMonitorKeyFromCollectionsList(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue"), job("b", "red")}},
		config.Collection{Name: "One", Jobs: []string{"a"}},
		config.Collection{Name: "Two", Jobs: []string{"b", "gone"}})
	s := New(r.deps)
	s, _ = send(s, "down")
	_, navs := send(s, "m")
	if len(navs) != 1 || len(r.monitored) != 1 {
		t.Fatalf("navs=%v monitored=%v", navs, r.monitored)
	}
	got := r.monitored[0]
	if got.name != "Two" || !slices.Equal(got.jobs, []string{"b", "gone"}) || !got.missing["gone"] || got.missing["b"] {
		t.Fatalf("monitor args %+v", got)
	}
}

func TestMonitorUnavailableAndRenameHook(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue")}}, config.Collection{Name: "C", Jobs: []string{"a"}})
	r.deps.Monitor = nil
	s := New(r.deps)
	s, navs := send(s, "m")
	if len(navs) != 0 || !strings.Contains(shown(s), "monitor unavailable") {
		t.Fatalf("list: %v %q", navs, shown(s))
	}
	s, navs = send(s, "enter")
	v := open(pushed(t, navs))
	v, navs = send(v, "m")
	if len(navs) != 0 || !strings.Contains(shown(v), "monitor unavailable") {
		t.Fatalf("view: %v %q", navs, shown(v))
	}
	s, _ = send(s, "r")
	s = typeText(s, "2")
	send(s, "enter")
	if len(r.renamed) != 1 || r.renamed[0] != [2]string{"C", "C2"} {
		t.Fatalf("renamed %v", r.renamed)
	}
}

func TestViewLoadingModalNamesCollection(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue")}},
		config.Collection{Name: "Nightly", Jobs: []string{"a"}})
	s := New(r.deps)
	s, navs := send(s, "enter")
	v := pushed(t, navs)
	v.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	cmd := v.Init()
	if out := shown(v); !strings.Contains(out, "Loading Nightly") || !strings.Contains(out, "╭") {
		t.Fatalf("first load: %q", out)
	}
	v, _ = run(v, cmd)
	if out := shown(v); strings.Contains(out, "Loading") || strings.Contains(out, "refreshing") {
		t.Fatalf("loaded: %q", out)
	}
	// R over existing data: rows stay, header shows the spinner.
	now := time.Unix(1000, 0)
	v.(*viewScreen).load.SetNow(func() time.Time { return now })
	v, cmd = v.Update(kmsg("R"))
	if out := shown(v); !strings.Contains(out, "refreshing…") || !strings.Contains(out, "a") || strings.Contains(out, "Refreshing") {
		t.Fatalf("refresh with data: %q", out)
	}
	v, _ = run(v, cmd)
	if !strings.Contains(shown(v), "refreshing") {
		t.Fatal("fast refresh must keep the spinner until the minimum elapsed")
	}
	now = now.Add(time.Second)
	v.Update(spinner.TickMsg{})
	if strings.Contains(shown(v), "refreshing") || v.(*viewScreen).load.Active() {
		t.Fatal("spinner must stop")
	}
	// R with no data (after a failed load): blocking modal with the name.
	vs := v.(*viewScreen)
	vs.loaded, vs.err = false, fmt.Errorf("boom")
	v, _ = v.Update(kmsg("R"))
	if out := shown(v); !strings.Contains(out, "Refreshing Nightly…") {
		t.Fatalf("R without data: %q", out)
	}
	_ = s
}

func TestStaleSpinnerTickIgnoredAfterLoad(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue")}}, config.Collection{Name: "C", Jobs: []string{"a"}})
	_, navs := send(New(r.deps), "enter")
	v := open(pushed(t, navs))
	vs := v.(*viewScreen)
	if _, c := v.Update(spinner.TickMsg{}); c != nil || vs.load.Active() {
		t.Fatal("idle screen must ignore ticks")
	}
}

func TestMonitorFromListShowsModalUntilLoaded(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue")}},
		config.Collection{Name: "One", Jobs: []string{"a"}})
	s := New(r.deps)
	s, cmd := s.Update(kmsg("m"))
	if out := shown(s); !strings.Contains(out, "Loading jobs for One…") {
		t.Fatalf("modal expected: %q", out)
	}
	if !s.(*listScreen).load.Active() {
		t.Fatal("spinner must run")
	}
	s, navs := run(s, cmd)
	if len(navs) != 1 || s.(*listScreen).load.Active() || strings.Contains(shown(s), "Loading") {
		t.Fatalf("navs=%v view=%q", navs, shown(s))
	}
	// esc cancels a pending load and drops its result.
	s, cmd = s.Update(kmsg("m"))
	s, _ = s.Update(kmsg("esc"))
	_, navs = run(s, cmd)
	if len(navs) != 0 || strings.Contains(shown(s), "Loading") {
		t.Fatalf("cancel failed: navs=%v", navs)
	}
}

func TestPickerShowsModalWhileLoading(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{job("a", "blue")}})
	p := newPicker(&env{deps: r.deps, theme: core.NewTheme()}, "New x", nil, nil)
	if out := shown(p); !strings.Contains(out, "Loading jobs…") {
		t.Fatalf("%q", out)
	}
	p2 := open(p)
	if strings.Contains(shown(p2), "Loading") || p.load.Active() {
		t.Fatal("picker still loading")
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

func TestViewOpenKey(t *testing.T) {
	o := &recOpener{}
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{
		{FullName: "a", Color: "blue", URL: "http://j/job/a/"}, {FullName: "nourl", Color: "blue"}}},
		config.Collection{Name: "C", Jobs: []string{"a", "nourl", "gone"}})
	r.deps.Opener = o
	s, navs := send(New(r.deps), "enter")
	v := open(pushed(t, navs))

	v = pressOpen(v)
	if len(o.urls) != 1 || o.urls[0] != "http://j/job/a/" || !strings.Contains(shown(v), "opened http://j/job/a/") {
		t.Fatalf("opened %v\n%s", o.urls, shown(v))
	}
	v, _ = send(v, "down")
	v = pressOpen(v) // no URL
	v, _ = send(v, "down")
	v = pressOpen(v) // missing member
	if len(o.urls) != 1 {
		t.Fatalf("opened %v", o.urls)
	}
	_ = s

	o.err = errors.New("no display")
	v, _ = send(v, "up", "up")
	v = pressOpen(v)
	if !strings.Contains(shown(v), "could not open: no display") {
		t.Fatalf("no error notice:\n%s", shown(v))
	}
}

func TestViewOpenKeyHiddenWithoutOpener(t *testing.T) {
	r := newRig(t, jenkins.Catalog{Jobs: []jenkins.Job{{FullName: "a", URL: "http://j/"}}}, config.Collection{Name: "C", Jobs: []string{"a"}})
	_, navs := send(New(r.deps), "enter")
	v := open(pushed(t, navs))
	if _, cmd := v.Update(kmsg("o")); cmd != nil {
		t.Error("o acted without an opener")
	}
	for _, k := range v.(core.Screen).Keys() {
		if k.Help().Desc == "open in browser" {
			t.Error("o shown without an opener")
		}
	}
}
