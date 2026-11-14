package blackbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// BBX-6-02 crash-dump catalogue: temp-dir tests. The catalogue stats files
// only (DirEntry.Info); these tests prove content never enters the output.

// writeDump creates a fake dump file with the given bytes and mtime.
func writeDump(t *testing.T, path string, content []byte, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestDumpsCatalogue(t *testing.T) {
	root := t.TempDir()
	t0 := time.Unix(1700000000, 0).UTC()
	writeDump(t, filepath.Join(root, "w3wp.exe.1234.dmp"), []byte("aaa"), t0.Add(2*time.Second))
	writeDump(t, filepath.Join(root, "old.dmp"), []byte("bb"), t0)
	writeDump(t, filepath.Join(root, "sub", "iexplore.exe.99.dmp"), []byte("c"), t0.Add(time.Second))
	writeDump(t, filepath.Join(root, "CASE.DMP"), []byte("dddd"), t0.Add(3*time.Second))
	writeDump(t, filepath.Join(root, "notes.txt"), []byte("x"), t0)                  // not a dump
	writeDump(t, filepath.Join(root, "sub", "deeper", "hidden.dmp"), []byte("x"), t0) // depth 3: skipped

	got := catalogueDumps([]string{root})
	want := []DumpEntry{
		{Name: "CASE.DMP", Size: 4, MTime: t0.Add(3 * time.Second)},
		{Name: "w3wp.exe.1234.dmp", Size: 3, MTime: t0.Add(2 * time.Second), Target: "w3wp"},
		{Name: "iexplore.exe.99.dmp", Size: 1, MTime: t0.Add(time.Second), Target: "iexplore"},
		{Name: "old.dmp", Size: 2, MTime: t0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogueDumps = %+v, want %+v (newest first, base names, depth 2, *.dmp only)", got, want)
	}
}

func TestDumpsNoContent(t *testing.T) {
	root := t.TempDir()
	secret := "TOP-SECRET-DUMP-CONTENT-0123456789"
	writeDump(t, filepath.Join(root, "w3wp.exe.1.dmp"), []byte(secret), time.Unix(1000, 0))

	entries := catalogueDumps([]string{root})
	if len(entries) != 1 {
		t.Fatalf("len = %d, want 1", len(entries))
	}
	if entries[0].Size != int64(len(secret)) {
		t.Fatalf("Size = %d, want %d (stat only)", entries[0].Size, len(secret))
	}
	line, err := json.Marshal(&DumpsGroup{Dumps: entries})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(line), secret) {
		t.Fatalf("dump content leaked into the output: %s", line)
	}
	for _, e := range entries {
		if strings.Contains(e.Name, secret) || strings.Contains(e.Target, secret) {
			t.Fatalf("dump content leaked into a field: %+v", e)
		}
	}
}

func TestDumpsCaps(t *testing.T) {
	t.Run("listed capped at 200 newest first", func(t *testing.T) {
		root := t.TempDir()
		t0 := time.Unix(1000, 0)
		for i := 0; i < 250; i++ {
			writeDump(t, filepath.Join(root, fmt.Sprintf("d%03d.dmp", i)), []byte("x"), t0.Add(time.Duration(i)*time.Second))
		}
		got := catalogueDumps([]string{root})
		if len(got) != dumpMaxListed {
			t.Fatalf("len = %d, want capped at %d", len(got), dumpMaxListed)
		}
		if got[0].Name != "d249.dmp" {
			t.Fatalf("newest dump dropped: first = %s, want d249.dmp", got[0].Name)
		}
	})
	t.Run("visited capped at 500", func(t *testing.T) {
		root := t.TempDir()
		t0 := time.Unix(1000, 0)
		for i := 0; i < 550; i++ {
			writeDump(t, filepath.Join(root, fmt.Sprintf("a%03d.txt", i)), []byte("x"), t0)
		}
		writeDump(t, filepath.Join(root, "zzz.dmp"), []byte("x"), t0)
		got := catalogueDumps([]string{root})
		if len(got) != 0 {
			t.Fatalf("walk must stop after %d visited entries; zzz.dmp was reached: %d dumps", dumpMaxVisited, len(got))
		}
	})
}

func TestDumpsCadence(t *testing.T) {
	root := t.TempDir()
	writeDump(t, filepath.Join(root, "a.dmp"), []byte("x"), time.Unix(1000, 0))
	calls := 0
	g := &dumpsGroup{roots: []string{root}, scan: func(roots []string) []DumpEntry {
		calls++
		return catalogueDumps(roots)
	}}
	now := time.Unix(2000, 0)
	v1, err := g.Collect(now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("first Collect: scans = %d, want 1", calls)
	}
	// Within 5 min: cached result, no re-scan even when the tree changes.
	writeDump(t, filepath.Join(root, "b.dmp"), []byte("y"), time.Unix(1001, 0))
	v2, err := g.Collect(now.Add(4*time.Minute), 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("re-scanned within 5 min: scans = %d, want 1", calls)
	}
	if !reflect.DeepEqual(v1, v2) {
		t.Fatalf("cached result changed: %+v vs %+v", v1, v2)
	}
	if dg, ok := v2.(*DumpsGroup); !ok || len(dg.Dumps) != 1 {
		t.Fatalf("cached result = %+v, want the single cached dump", v2)
	}
	// At 5 min: one re-scan picks up the new dump.
	v3, err := g.Collect(now.Add(5*time.Minute), 0)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("at 5 min: scans = %d, want 2", calls)
	}
	if dg, ok := v3.(*DumpsGroup); !ok || len(dg.Dumps) != 2 {
		t.Fatalf("re-scan result = %+v, want 2 dumps", v3)
	}
}

func TestDumpsEmptyNil(t *testing.T) {
	for _, roots := range [][]string{{t.TempDir()}, {filepath.Join(t.TempDir(), "absent")}} {
		g := &dumpsGroup{roots: roots, scan: catalogueDumps}
		v, err := g.Collect(time.Unix(1000, 0), 0)
		if err != nil {
			t.Fatal(err)
		}
		if v != nil {
			t.Fatalf("no dumps must be a nil group (never 0), got %+v", v)
		}
	}
}

func TestDumpsTargetParse(t *testing.T) {
	cases := []struct{ name, want string }{
		{"w3wp.exe.1234.dmp", "w3wp"},
		{"iexplore.exe_5678.dmp", "iexplore"},
		{"svchost.exe.dmp", "svchost"},
		{"W3WP.EXE.1.DMP", "W3WP"},
		{"Mini010120-01.dmp", ""},
		{"plain.dmp", ""},
		{"notes.txt", ""},
		{"weird.exebar.dmp", ""},
	}
	for _, c := range cases {
		if got := dumpTarget(c.name); got != c.want {
			t.Errorf("dumpTarget(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}