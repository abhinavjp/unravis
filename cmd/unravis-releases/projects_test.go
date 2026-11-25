package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultProjectKeepsTheOldLabels(t *testing.T) {
	list, err := projectList("", "", env(map[string]string{"LOCALAPPDATA": `C:\L`}))
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v err %v", list, err)
	}
	p := list[0]
	if p.ID != "unravis" || strings.Join(p.Channels, ",") != "rc,stable" || len(p.Steps) != 7 || p.Steps[0] != "Preflight" || p.Steps[6] != "Verify" {
		t.Fatalf("the default must keep rc, stable and the 7 steps: %+v", p)
	}
	if strings.Join(p.Branches, ",") != "develop,master" || p.ShadowGate != 20 {
		t.Fatalf("branches/gate: %+v", p)
	}
	want := filepath.Join(`C:\L`, "Unravis", "ci", "store")
	if p.CIStore != want {
		t.Fatalf("store %q want %q", p.CIStore, want)
	}
}

func TestCIStoreFlagOverridesTheDefault(t *testing.T) {
	list, err := projectList("", "D:/store", env(nil))
	if err != nil || list[0].CIStore != "D:/store" {
		t.Fatalf("%v %v", list, err)
	}
}

func TestProjectsFileWinsAndStoreFlagOverridesTheFirst(t *testing.T) {
	f := filepath.Join(t.TempDir(), "p.json")
	body := `{"projects":[{"id":"a","name":"A","ciStore":"x","steps":["One","Two"]},{"id":"b"}]}`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := projectList(f, "", env(nil))
	if err != nil || len(list) != 2 || list[0].CIStore != "x" || len(list[0].Steps) != 2 || len(list[1].Steps) != 7 {
		t.Fatalf("%+v %v", list, err)
	}
	list, _ = projectList(f, "y", env(nil))
	if list[0].CIStore != "y" || list[1].CIStore != "" {
		t.Fatalf("%+v", list)
	}
	if _, err := projectList(filepath.Join(t.TempDir(), "none.json"), "", env(nil)); err == nil {
		t.Fatal("a missing projects file must be an error")
	}
}
