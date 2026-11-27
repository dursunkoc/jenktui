package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func presetFile(t *testing.T) PresetFile {
	t.Helper()
	return PresetFile{Path: filepath.Join(t.TempDir(), "sub", "presets.toml")}
}

func mustSave(t *testing.T, p PresetFile, name string, v map[string]map[string]string) {
	t.Helper()
	if err := p.Save(name, v); err != nil {
		t.Fatalf("Save(%q): %v", name, err)
	}
}

func TestPresetsMissingFileIsEmpty(t *testing.T) {
	p := presetFile(t)
	got, err := p.Load([]string{"a"})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestPresetsRoundTrip(t *testing.T) {
	p := presetFile(t)
	mustSave(t, p, "staging", map[string]map[string]string{
		"team/api":  {"ENV": "staging", "NOTE": ""},
		"team/web":  {"ENV": "stg"},
		"team/none": {},
	})
	got, err := p.Load([]string{"team/api", "team/web", "team/other"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]map[string]string{
		"team/api": {"staging": {"ENV": "staging", "NOTE": ""}},
		"team/web": {"staging": {"ENV": "stg"}},
	}
	// team/none was saved with empty values; it is present but not asked for.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	fi, err := os.Stat(p.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestPresetsLoadOnlyAskedJobsAndNonNilValues(t *testing.T) {
	p := presetFile(t)
	mustSave(t, p, "x", map[string]map[string]string{"a": {}, "b": {"K": "v"}})
	got, err := p.Load([]string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["a"]["x"] == nil {
		t.Fatalf("got %#v", got)
	}
	if _, ok := got["b"]; ok {
		t.Error("b should not be loaded")
	}
}

func TestPresetsSaveReplacesCaseInsensitively(t *testing.T) {
	p := presetFile(t)
	mustSave(t, p, "Staging", map[string]map[string]string{"a": {"E": "1"}, "b": {"E": "1"}})
	mustSave(t, p, "other", map[string]map[string]string{"a": {"E": "o"}})
	mustSave(t, p, "staging deploy", map[string]map[string]string{"a": {"E": "2"}})
	mustSave(t, p, "STAGING DEPLOY", map[string]map[string]string{"a": {"E": "3"}})
	got, err := p.Load([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]map[string]string{
		"a": {"Staging": {"E": "1"}, "other": {"E": "o"}, "STAGING DEPLOY": {"E": "3"}},
		"b": {"Staging": {"E": "1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}

	// Same name, different case: replaced in place, new spelling, order kept.
	mustSave(t, p, "STAGING", map[string]map[string]string{"a": {"E": "9"}})
	d, err := p.read()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, e := range d.Items {
		if e.Job == "a" {
			order = append(order, e.Name)
		}
	}
	if !reflect.DeepEqual(order, []string{"STAGING", "other", "STAGING DEPLOY"}) {
		t.Fatalf("a entries (spelling, position): %v", order)
	}
	got, _ = p.Load([]string{"b"})
	if _, ok := got["b"]["Staging"]; !ok {
		t.Fatalf("b untouched expected: %v", got)
	}
}

func TestPresetsSaveDropsCaseDuplicates(t *testing.T) {
	p := presetFile(t)
	doc := `[[preset]]
job = "a"
name = "Dup"
[preset.values]
E = "1"

[[preset]]
job = "a"
name = "dup"
[preset.values]
E = "2"
`
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := p.Load([]string{"a"})
	if got["a"]["dup"]["E"] != "2" || len(got["a"]) != 1 {
		t.Fatalf("later wins: %v", got)
	}
	mustSave(t, p, "DUP", map[string]map[string]string{"a": {"E": "3"}})
	b, _ := os.ReadFile(p.Path)
	if strings.Count(string(b), "[[preset]]") != 1 {
		t.Fatalf("duplicates not dropped:\n%s", b)
	}
}

func TestPresetsSaveEmptyNameRefused(t *testing.T) {
	p := presetFile(t)
	if err := p.Save("", map[string]map[string]string{"a": {"E": "1"}}); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(p.Path); err == nil {
		t.Fatal("file written")
	}
}

func TestPresetsDelete(t *testing.T) {
	p := presetFile(t)
	mustSave(t, p, "keep", map[string]map[string]string{"a": {"E": "k"}})
	mustSave(t, p, "Gone", map[string]map[string]string{"a": {"E": "1"}, "b": {"E": "1"}, "c": {"E": "1"}})
	if err := p.Delete("gone", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	got, _ := p.Load([]string{"a", "b", "c"})
	want := map[string]map[string]map[string]string{
		"a": {"keep": {"E": "k"}},
		"c": {"Gone": {"E": "1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// Nothing to delete: no write, no error.
	if err := p.Delete("nope", []string{"a"}); err != nil {
		t.Fatal(err)
	}
}

func TestPresetsBrokenFile(t *testing.T) {
	p := presetFile(t)
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []byte("[[preset\nnot toml")
	if err := os.WriteFile(p.Path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load([]string{"a"}); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("Load err = %v", err)
	}
	if err := p.Save("n", map[string]map[string]string{"a": {"E": "1"}}); err == nil {
		t.Error("Save should fail")
	}
	if err := p.Delete("n", []string{"a"}); err == nil {
		t.Error("Delete should fail")
	}
	after, _ := os.ReadFile(p.Path)
	if string(after) != string(bad) {
		t.Errorf("file changed: %q", after)
	}
}

func TestPresetsHandWrittenFile(t *testing.T) {
	p := presetFile(t)
	doc := `[[preset]]
job = "team/api/deploy"
name = "staging deploy"

[preset.values]
ENV = "staging"
DRY_RUN = "false"
`
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := p.Load([]string{"team/api/deploy"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]map[string]string{
		"team/api/deploy": {"staging deploy": {"ENV": "staging", "DRY_RUN": "false"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}
