package releasedash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectDefaultsKeepToday(t *testing.T) {
	p := Project{ID: "x"}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	if p.Name != "x" || strings.Join(p.Channels, ",") != "rc,stable" || len(p.Steps) != 7 || p.Steps[0] != "Preflight" || p.Steps[6] != "Verify" || p.ShadowGate != 20 || !p.HasReleases() {
		t.Fatalf("%+v", p)
	}
	// the defaults are copies: changing one project must not change the next
	p.Steps[0] = "Changed"
	q := Project{ID: "y"}
	_ = q.Normalize()
	if q.Steps[0] != "Preflight" {
		t.Fatal("the default step list is shared")
	}
}

func TestProjectNormalizeRefusesBadRecords(t *testing.T) {
	for _, p := range []Project{{ID: ""}, {ID: "Has Space"}, {ID: "UPPER"}, {ID: strings.Repeat("a", 41)}, {ID: "ok", Channels: []string{"a", "b", "c"}}, {ID: "ok", Channels: []string{""}}, {ID: "ok", Channels: []string{strings.Repeat("c", 21)}}} {
		if err := p.Normalize(); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
	f := false
	p := Project{ID: "ok", Releases: &f}
	_ = p.Normalize()
	if p.HasReleases() {
		t.Fatal("releases:false must hide the releases")
	}
}

func TestLoadProjectsAcceptsBothShapesAndChecksTheList(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	l, err := LoadProjects(write("a.json", `{"projects":[{"id":"one","name":"One","steps":["A","B"],"channels":["beta"]},{"id":"two"}]}`))
	if err != nil || len(l) != 2 || l[0].Steps[1] != "B" || l[0].Channels[0] != "beta" || l[1].Name != "two" {
		t.Fatalf("%+v %v", l, err)
	}
	if l, err = LoadProjects(write("b.json", `[{"id":"solo"}]`)); err != nil || len(l) != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	for name, body := range map[string]string{"c.json": `[]`, "d.json": `{"projects":[]}`, "e.json": `not json`, "f.json": `[{"id":"a"},{"id":"a"}]`, "g.json": `[{"id":"BAD"}]`} {
		if _, err := LoadProjects(write(name, body)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := LoadProjects(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing file is an error")
	}
}

func TestDashWithoutRecordHasOneDefaultProject(t *testing.T) {
	d := New(t.TempDir(), "", nil)
	ps := d.projects()
	if len(ps) != 1 || ps[0].ID != "default" || ps[0].CIStore != "" {
		t.Fatalf("%+v", ps)
	}
	if _, ok := d.project(""); !ok {
		t.Fatal("empty id means the first project")
	}
	if _, ok := d.project("nope"); ok {
		t.Fatal("unknown id")
	}
}
