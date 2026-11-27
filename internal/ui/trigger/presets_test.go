package trigger

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dursunkoc/jenktui/internal/jenkins"
)

type saveCall struct {
	name   string
	values map[string]map[string]string
}
type delCall struct {
	name string
	jobs []string
}

type fakeStore struct {
	data    map[string]map[string]map[string]string
	loadErr error
	saveErr error
	delErr  error
	loads   int
	saves   []saveCall
	dels    []delCall
}

func (f *fakeStore) Load(jobs []string) (map[string]map[string]map[string]string, error) {
	f.loads++
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	out := map[string]map[string]map[string]string{}
	for _, j := range jobs {
		if p, ok := f.data[j]; ok {
			out[j] = p
		}
	}
	return out, nil
}
func (f *fakeStore) Save(name string, v map[string]map[string]string) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, saveCall{name, v})
	return nil
}
func (f *fakeStore) Delete(name string, jobs []string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.dels = append(f.dels, delCall{name, jobs})
	return nil
}

type pm = map[string]map[string]map[string]string

func appDefs() []jenkins.ParamDef {
	return []jenkins.ParamDef{
		{Name: "ENV", Type: "string", Default: "dev"},
		{Name: "REGION", Type: "choice", Default: "us", Choices: []string{"us", "eu"}},
		{Name: "DRY", Type: "bool", Default: "false"},
		{Name: "PW", Type: "password"},
	}
}

func oneApp(store *fakeStore) Deps {
	return Deps{Client: fakeClient{defs: map[string][]jenkins.ParamDef{"app": appDefs()}}, Presets: store}
}

func (h *harness) final() map[string]map[string]string { return h.s.(*screen).final }

func (h *harness) wantNoView(sub string) {
	h.t.Helper()
	if v := h.view(); strings.Contains(v, sub) {
		h.t.Fatalf("view has %q:\n%s", sub, v)
	}
}

func (h *harness) key(k tea.KeyType) { h.send(tea.KeyMsg{Type: k}) }

func (h *harness) backspaces(n int) {
	for range n {
		h.key(tea.KeyBackspace)
	}
}

func hasKey(h *harness, help string) bool {
	for _, b := range h.s.Keys() {
		if b.Help().Key+" "+b.Help().Desc == help {
			return true
		}
	}
	return false
}

func TestPresetsNoneFlowUnchanged(t *testing.T) {
	st := &fakeStore{}
	h := startWith(t, oneApp(st), "app")
	h.wantView("ENV")
	h.wantNoView("No preset")
	if st.loads != 1 {
		t.Fatalf("loads %d", st.loads)
	}
	defs := map[string][]jenkins.ParamDef{"a": {{Name: "X", Type: "string"}}, "b": {{Name: "X", Type: "string"}}}
	h = startWith(t, Deps{Client: fakeClient{defs: defs}, Presets: &fakeStore{}}, "a", "b")
	h.wantView("Common Parameters")
}

func TestPresetsNilStore(t *testing.T) {
	st := &fakeStore{data: pm{"app": {"p": {"ENV": "x"}}}}
	d := oneApp(st)
	d.Presets = nil
	h := startWith(t, d, "app")
	h.wantNoView("No preset")
	if st.loads != 0 {
		t.Fatal("loaded")
	}
	h.keys("enter", "enter", "enter", "enter") // all four fields
	h.wantView("Ready to trigger")
	if hasKey(h, "s save preset") {
		t.Fatal("s key offered")
	}
	h.keys("s")
	h.wantNoView("Preset name")
	h.wantNoView("s save preset")
}

func oneJobStore() *fakeStore {
	return &fakeStore{data: pm{"app": {
		"staging": {"ENV": "prod", "REGION": "eu", "DRY": "TRUE", "GONE": "x", "PW": "leak"},
		"other":   {"ENV": "o"},
	}}}
}

func TestPresetsOneJob(t *testing.T) {
	h := startWith(t, oneApp(oneJobStore()), "app")
	h.wantView("Preset")
	h.wantView("› No preset (start from the defaults)")
	h.wantView("other")
	h.wantView("staging")
	h.wantNoView("of 1 jobs")
	// No preset: the form with defaults.
	h.keys("enter")
	h.wantView("dev")
	h.keys("esc") // back to the step
	h.wantView("No preset (start from the defaults)")
	h.keys("down", "down", "enter") // staging
	h.wantView("Ready to trigger 1 job")
	h.wantView("ENV = prod")
	want := map[string]string{"ENV": "prod", "REGION": "eu", "DRY": "true", "PW": ""}
	if !reflect.DeepEqual(h.final()["app"], want) {
		t.Fatalf("final %v", h.final()["app"])
	}
	h.wantView("Passwords are not saved in presets. Press n or Esc to go back and enter them.")
	h.keys("n")
	h.wantView("prod")
	h.wantView("‹ eu ›")
	h.wantView("[x]")
	h.keys("tab", "tab", "tab", "pw", "enter") // fill the password
	h.wantView("Ready to trigger 1 job")
	h.wantNoView("Passwords are not saved")
	h.keys("esc", "esc") // back to the form, then the step
	h.wantView("No preset (start from the defaults)")
	h.keys("esc")
	if len(h.nav) == 0 {
		t.Fatal("no pop")
	}
}

func TestPresetsStaleValues(t *testing.T) {
	st := &fakeStore{data: pm{"app": {"p": {"REGION": "mars", "DRY": "yes", "ENV": "e", "GONE": "x"}}}}
	h := startWith(t, oneApp(st), "app")
	h.keys("down", "enter")
	h.wantView("app: REGION = mars is no longer a choice, using the default")
	h.wantView("app: DRY = yes is no longer a choice, using the default")
	f := h.final()["app"]
	if f["REGION"] != "us" || f["DRY"] != "false" || f["ENV"] != "e" {
		t.Fatalf("final %v", f)
	}
	if _, ok := f["GONE"]; ok {
		t.Fatal("removed parameter kept")
	}
}

func threeJobs(store *fakeStore) Deps {
	mk := func(extra ...jenkins.ParamDef) []jenkins.ParamDef {
		return append([]jenkins.ParamDef{
			{Name: "ENV", Type: "string", Default: "dev"},
			{Name: "REGION", Type: "choice", Default: "us", Choices: []string{"us", "eu"}},
		}, extra...)
	}
	defs := map[string][]jenkins.ParamDef{
		"team/a": mk(jenkins.ParamDef{Name: "ONLYA", Type: "string", Default: "x"}),
		"team/b": mk(jenkins.ParamDef{Name: "ONLYB", Type: "bool", Default: "false"}),
		"team/c": mk(),
	}
	return Deps{Client: fakeClient{defs: defs}, Presets: store, Parent: "team"}
}

func threeStore() *fakeStore {
	return &fakeStore{data: pm{
		"team/a": {"Deploy": {"ENV": "prod", "REGION": "eu", "ONLYA": "zz"}},
		"team/b": {"deploy": {"ENV": "prod2", "REGION": "eu", "ONLYB": "true"}},
		"team/c": {"DEPLOY": {"ENV": "prod3", "REGION": "eu"}, "solo": {"ENV": "s"}},
	}}
}

var threeWant = map[string]map[string]string{
	"team/a": {"ENV": "prod", "REGION": "eu", "ONLYA": "zz"},
	"team/b": {"ENV": "prod2", "REGION": "eu", "ONLYB": "true"},
	"team/c": {"ENV": "prod3", "REGION": "eu"},
}

func (h *harness) toSummary() {
	for range 12 {
		if strings.Contains(h.view(), "Ready to trigger") {
			return
		}
		h.keys("enter")
	}
	h.t.Fatalf("no summary:\n%s", h.view())
}

func TestPresetsSeveralJobsApply(t *testing.T) {
	h := startWith(t, threeJobs(threeStore()), "team/a", "team/b", "team/c")
	h.wantView("1 of 3 jobs")
	h.wantView("3 of 3 jobs")
	h.wantView("Deploy") // first job's spelling
	h.keys("down", "enter")
	h.wantView("Ready to trigger 3 job")
	if !reflect.DeepEqual(h.final(), threeWant) {
		t.Fatalf("final %v", h.final())
	}
	// e on c (no per-job step) shows the preset value.
	h.keys("down", "down", "e")
	h.wantView("Editing from the summary")
	h.wantView("prod3")
	h.keys("esc")
	// Back walks b, a, then Common (REGION is shared, ENV differs).
	h.keys("esc")
	h.wantView("b (2/2)")
	h.wantView("prod2")
	h.wantView("[x]")
	h.keys("esc")
	h.wantView("a (1/2)")
	h.wantView("prod")
	h.keys("esc")
	h.wantView("Common Parameters")
	h.wantView("‹ eu ›")
	h.wantView("(differs)")
	// Enter without changes keeps every per-job value.
	h.toSummary()
	if !reflect.DeepEqual(h.final(), threeWant) {
		t.Fatalf("final after back %v", h.final())
	}
	// Changing a common value resets the per-job values.
	h.keys("esc", "esc", "esc") // b, a, common
	h.wantView("Common Parameters")
	h.keys("h")
	h.toSummary()
	f := h.final()
	if f["team/a"]["ENV"] != "dev" || f["team/a"]["REGION"] != "us" || f["team/b"]["ONLYB"] != "false" {
		t.Fatalf("final after change %v", f)
	}
}

func TestPresetsSeveralJobsPartialAndPasswords(t *testing.T) {
	defs := map[string][]jenkins.ParamDef{
		"a": {{Name: "ENV", Type: "string", Default: "dev"}, {Name: "PW", Type: "password"}},
		"b": {{Name: "ENV", Type: "string", Default: "dev"}, {Name: "PW", Type: "password"}},
		"c": {{Name: "ENV", Type: "string", Default: "dev"}, {Name: "PW", Type: "password"}},
		"d": nil,
	}
	st := &fakeStore{data: pm{"a": {"p": {"ENV": "A", "PW": "no"}}, "b": {"p": {"ENV": "B"}}}}
	h := startWith(t, Deps{Client: fakeClient{defs: defs}, Presets: st}, "a", "b", "c", "d")
	h.wantView("2 of 4 jobs")
	h.keys("down", "enter")
	want := map[string]map[string]string{
		"a": {"ENV": "A", "PW": ""}, "b": {"ENV": "B", "PW": ""}, "c": {"ENV": "dev", "PW": ""}, "d": {},
	}
	if !reflect.DeepEqual(h.final(), want) {
		t.Fatalf("final %v", h.final())
	}
	h.wantView("Passwords are not saved in presets. Press e to enter them.")
	h.keys("e") // a
	h.keys("enter", "pw1", "enter")
	h.wantView("PW = ••••")
	h.wantView("Passwords are not saved") // b and c are still empty
	for _, j := range []string{"down", "e"} {
		h.keys(j)
	}
	h.keys("enter", "pw2", "enter", "down", "e", "enter", "pw3", "enter")
	h.wantNoView("Passwords are not saved")
	// Save never carries a password; the job without parameters is left out.
	h.keys("s")
	h.keys("enter")
	if len(st.saves) != 1 {
		t.Fatalf("saves %v", st.saves)
	}
	for j, v := range st.saves[0].values {
		if _, ok := v["PW"]; ok {
			t.Fatalf("password saved for %s", j)
		}
	}
	if _, ok := st.saves[0].values["d"]; ok || len(st.saves[0].values) != 3 {
		t.Fatalf("values %v", st.saves[0].values)
	}
	h.wantView("Saved preset p for 3 jobs")
}

func TestPresetsSummaryHint(t *testing.T) {
	h := startWith(t, threeJobs(&fakeStore{}), "team/a", "team/b", "team/c")
	h.toSummary()
	h.wantView("y/enter trigger, e edit job, s save preset, up/down select job, n/esc back")
	if !hasKey(h, "s save preset") {
		t.Fatal("no s in key bar")
	}
	got := []string{}
	for _, b := range h.s.Keys() {
		got = append(got, b.Help().Key)
	}
	if got[2] != "e" || got[3] != "s" {
		t.Fatalf("s must follow e: %v", got)
	}
}

func TestPresetsSaveOneJob(t *testing.T) {
	st := oneJobStore()
	h := startWith(t, oneApp(st), "app")
	h.keys("down", "down", "enter") // staging
	h.keys("s")
	h.wantView("Preset name: staging")
	if !hasKey(h, "enter save") {
		t.Fatal("no enter save key")
	}
	if hasKey(h, "y trigger") {
		t.Fatal("trigger keys while naming")
	}
	h.backspaces(7)
	h.keys("  New ", "y", "n", "e")
	h.wantView("New yne")
	h.keys("enter")
	if len(st.saves) != 1 || st.saves[0].name != "New yne" {
		t.Fatalf("saves %+v", st.saves)
	}
	v := st.saves[0].values["app"]
	if v["ENV"] != "prod" || v["REGION"] != "eu" || v["DRY"] != "true" {
		t.Fatalf("values %v", v)
	}
	if _, ok := v["PW"]; ok {
		t.Fatal("password saved")
	}
	h.wantView("Saved preset New yne for app")
	h.wantView("Ready to trigger")
	h.keys("j")
	h.wantNoView("Saved preset") // flash clears on the next key
}

func TestPresetsSaveEmptyRefusedAndLimit(t *testing.T) {
	st := oneJobStore()
	h := startWith(t, oneApp(st), "app")
	h.keys("enter")
	h.toSummary()
	h.keys("s")
	h.keys("   ", "enter")
	h.wantView("preset name is required")
	if len(st.saves) != 0 {
		t.Fatal("saved")
	}
	h.keys("x")
	h.wantView("Preset name:")
	h.backspaces(4)
	h.keys(strings.Repeat("a", 45), "enter")
	if len(st.saves) != 1 || st.saves[0].name != strings.Repeat("a", 40) {
		t.Fatalf("saves %+v", st.saves)
	}
}

func TestPresetsSaveSeveralJobs(t *testing.T) {
	st := threeStore()
	h := startWith(t, threeJobs(st), "team/a", "team/b", "team/c")
	h.keys("down", "enter") // Deploy
	h.keys("s")
	h.wantView("Preset name: Deploy")
	h.keys("enter")
	if len(st.saves) != 1 || !reflect.DeepEqual(st.saves[0].values, threeWant) {
		t.Fatalf("saves %+v", st.saves)
	}
	h.wantView("Saved preset Deploy for 3 jobs")
	// esc closes the prompt without saving.
	h.keys("s", "esc")
	h.wantNoView("Preset name:")
	if len(st.saves) != 1 {
		t.Fatal("saved on esc")
	}
}

func TestPresetsSaveError(t *testing.T) {
	st := oneJobStore()
	st.saveErr = errors.New("disk full")
	d := oneApp(st)
	d.Next = nil
	h := startWith(t, d, "app")
	h.keys("enter")
	h.toSummary()
	h.keys("s", "x", "enter")
	h.wantView("save failed: disk full")
	h.wantView("Ready to trigger")
	h.keys("y")
	h.wantView("queued  app")
}

func TestPresetsDelete(t *testing.T) {
	st := threeStore()
	h := startWith(t, threeJobs(st), "team/a", "team/b", "team/c")
	h.keys("j") // Deploy (3 jobs): rows sort deploy, solo
	h.keys("d")
	h.wantView("Delete preset Deploy for 3 jobs? y/n")
	if !hasKey(h, "y delete") || !hasKey(h, "n cancel") {
		t.Fatal("confirm keys")
	}
	h.keys("n")
	h.wantNoView("Delete preset")
	if len(st.dels) != 0 {
		t.Fatal("deleted")
	}
	h.keys("j", "d")
	h.wantView("Delete preset solo for c? y/n")
	h.keys("y")
	if len(st.dels) != 1 || st.dels[0].name != "solo" || !reflect.DeepEqual(st.dels[0].jobs, []string{"team/c"}) {
		t.Fatalf("dels %+v", st.dels)
	}
	h.wantNoView("solo")
	h.wantView("Deploy")
	h.keys("d", "y")
	if len(st.dels) != 2 || !reflect.DeepEqual(st.dels[1].jobs, []string{"team/a", "team/b", "team/c"}) {
		t.Fatalf("dels %+v", st.dels)
	}
	h.wantView("Common Parameters") // last preset gone: continue as No preset
	h.keys("esc")
	if len(h.nav) != 1 {
		t.Fatalf("esc on the first form must pop: %v", h.nav)
	}
}

func TestPresetsDeleteSingleLastAndError(t *testing.T) {
	st := &fakeStore{data: pm{"app": {"p": {"ENV": "x"}}}}
	st.delErr = errors.New("ro")
	h := startWith(t, oneApp(st), "app")
	h.keys("j", "d")
	h.wantView("Delete preset p for app? y/n")
	h.keys("y")
	h.wantView("delete failed: ro")
	h.keys("d")
	st.delErr = nil
	h.keys("y")
	h.wantView("ENV")
	h.wantNoView("No preset")
}

func TestPresetsRerunSkipsStep(t *testing.T) {
	st := oneJobStore()
	d := oneApp(st)
	d.Prefill = map[string]string{"ENV": "rerun"}
	h := startWith(t, d, "app")
	if st.loads != 0 {
		t.Fatal("Load called on a rerun")
	}
	h.wantNoView("No preset")
	h.wantView("rerun")
	h.keys("enter", "enter", "enter", "enter")
	h.wantView("Ready to trigger")
	h.keys("s")
	h.wantView("Preset name:")
}

func TestPresetsLoadError(t *testing.T) {
	st := oneJobStore()
	st.loadErr = errors.New("bad toml")
	h := startWith(t, oneApp(st), "app")
	h.wantNoView("No preset")
	h.wantView("Presets are unavailable: bad toml")
	h.keys("enter", "enter", "enter", "enter")
	h.wantView("Ready to trigger")
	h.wantView("Presets are unavailable: bad toml")
	h.keys("s", "n", "enter")
	h.wantView("Presets are unavailable: bad toml")
	if len(st.saves) != 0 {
		t.Fatal("saved")
	}
}
