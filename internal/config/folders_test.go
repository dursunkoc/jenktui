package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func tree(t *testing.T) *Collections {
	t.Helper()
	cs := &Collections{}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cs.Add("backend", []string{"a"}))
	must(cs.AddIn("backend", "develop", []string{"b", "a"}))
	must(cs.AddIn("backend/develop", "deep", []string{"c"}))
	must(cs.Add("libs", nil))
	return cs
}

func TestNestedAddGetDepth(t *testing.T) {
	cs := tree(t)
	if c := cs.Get("BACKEND/Develop/deep"); c == nil || c.Name != "deep" {
		t.Fatalf("get: %+v", c)
	}
	if cs.AddIn("backend", "DEVELOP", nil) == nil {
		t.Fatal("sibling duplicate accepted")
	}
	if err := cs.AddIn("libs", "develop", nil); err != nil {
		t.Fatal("same name at another level refused:", err)
	}
	if cs.Add("a/b", nil) == nil {
		t.Fatal("slash in name accepted")
	}
	if cs.AddIn("nope", "x", nil) == nil {
		t.Fatal("missing parent accepted")
	}
	p := ""
	for i := 0; i < 200; i++ { // no depth bound
		if err := cs.AddIn(p, "n", nil); err != nil {
			t.Fatal(err)
		}
		p = JoinPath(p, "n")
		p = trimLead(p)
	}
	if cs.Get(p) == nil {
		t.Fatal("deep folder lost")
	}
}

func trimLead(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}

func TestSubtreeDedupsAndOrdersFoldersFirst(t *testing.T) {
	cs := tree(t)
	got := cs.Get("backend").Subtree()
	if want := []string{"c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("subtree %v want %v", got, want)
	}
	if n := cs.Get("backend").CountFolders(); n != 2 {
		t.Fatalf("folders %d", n)
	}
	if got := cs.Paths(); !reflect.DeepEqual(got, []string{"backend", "backend/develop", "backend/develop/deep", "libs"}) {
		t.Fatalf("paths %v", got)
	}
}

func TestRenameDeleteInNested(t *testing.T) {
	cs := tree(t)
	if err := cs.Rename("backend/develop", "dev"); err != nil || cs.Get("backend/dev") == nil {
		t.Fatalf("rename: %v", err)
	}
	if err := cs.AddIn("backend", "x", nil); err != nil {
		t.Fatal(err)
	}
	if cs.Rename("backend/x", "DEV") == nil {
		t.Fatal("rename onto sibling accepted")
	}
	if err := cs.Rename("backend/x", "X"); err != nil {
		t.Fatal("rename to own name refused:", err)
	}
	if err := cs.Delete("backend/dev"); err != nil || cs.Get("backend/dev/deep") != nil {
		t.Fatalf("delete: %v", err)
	}
	if cs.Delete("backend/dev") == nil {
		t.Fatal("delete of missing accepted")
	}
}

func TestMoveFolder(t *testing.T) {
	cs := tree(t)
	if _, err := cs.MoveFolder("backend", "backend/develop"); err == nil {
		t.Fatal("moved into own descendant")
	}
	if _, err := cs.MoveFolder("backend", "backend"); err == nil {
		t.Fatal("moved into itself")
	}
	cs.AddIn("libs", "develop", nil)
	if _, err := cs.MoveFolder("backend/develop", "libs"); err == nil {
		t.Fatal("name clash accepted")
	}
	np, err := cs.MoveFolder("backend/develop/deep", "libs")
	if err != nil || np != "libs/deep" || cs.Get("libs/deep").Jobs[0] != "c" || cs.Get("backend/develop/deep") != nil {
		t.Fatalf("move: %q %v", np, err)
	}
	np, err = cs.MoveFolder("libs/deep", "")
	if err != nil || np != "deep" || cs.Get("deep") == nil {
		t.Fatalf("to top: %q %v", np, err)
	}
}

func TestTransferJobs(t *testing.T) {
	cs := tree(t)
	if err := cs.TransferJobs("backend/develop", "libs", []string{"b"}, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cs.Get("libs").Jobs, []string{"b"}) || len(cs.Get("backend/develop").Jobs) != 2 {
		t.Fatal("copy wrong")
	}
	if err := cs.TransferJobs("backend/develop", "libs", []string{"b", "a"}, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cs.Get("libs").Jobs, []string{"b", "a"}) || len(cs.Get("backend/develop").Jobs) != 0 {
		t.Fatalf("move wrong: %v / %v", cs.Get("libs").Jobs, cs.Get("backend/develop").Jobs)
	}
	if cs.TransferJobs("libs", "libs", nil, false) == nil {
		t.Fatal("same folder accepted")
	}
}

func TestNestedRoundTripAndLegacyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "collections.toml")
	cs := tree(t)
	if err := SaveCollections(p, cs); err != nil {
		t.Fatal(err)
	}
	back, err := LoadCollections(p)
	if err != nil || !reflect.DeepEqual(cs.Paths(), back.Paths()) || back.Get("backend/develop/deep").Jobs[0] != "c" {
		t.Fatalf("round trip: %v %v", err, back)
	}
	writeFile(filepath.Join(dir, "old.toml"), []byte("[[collection]]\nname = 'Ops'\njobs = ['a/x',\n 'a/y']\n\n[[collection]]\nname = 'a/b'\njobs = []\n"))
	old, err := LoadCollections(filepath.Join(dir, "old.toml"))
	if err != nil || old.Get("Ops") == nil || len(old.Get("Ops").Jobs) != 2 {
		t.Fatalf("legacy: %v", err)
	}
	if old.Get("a/b") == nil { // a legacy name holding a slash is still reachable
		t.Fatal("legacy slash name unreachable")
	}
}
