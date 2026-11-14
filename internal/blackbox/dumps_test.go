package blackbox

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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

	got, _ := catalogueDumps([]string{root})
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

	entries, _ := catalogueDumps([]string{root})
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
		got, st := catalogueDumps([]string{root})
		if len(got) != dumpMaxListed {
			t.Fatalf("len = %d, want capped at %d", len(got), dumpMaxListed)
		}
		if got[0].Name != "d249.dmp" {
			t.Fatalf("newest dump dropped: first = %s, want d249.dmp", got[0].Name)
		}
		if st.Truncated {
			t.Fatalf("250 entries are under the visit budget: %+v", st)
		}
	})
	t.Run("visited capped at 500", func(t *testing.T) {
		root := t.TempDir()
		t0 := time.Unix(1000, 0)
		for i := 0; i < 550; i++ {
			writeDump(t, filepath.Join(root, fmt.Sprintf("a%03d.txt", i)), []byte("x"), t0)
		}
		writeDump(t, filepath.Join(root, "zzz.dmp"), []byte("x"), t0)
		got, st := catalogueDumps([]string{root})
		if len(got) != 0 {
			t.Fatalf("walk must stop after %d visited entries; zzz.dmp was reached: %d dumps", dumpMaxVisited, len(got))
		}
		if !st.Truncated || st.Visited > dumpMaxVisited+dumpReadBatch {
			t.Fatalf("scan status not honest about the cap: %+v", st)
		}
	})
}

func TestDumpsCadence(t *testing.T) {
	root := t.TempDir()
	writeDump(t, filepath.Join(root, "a.dmp"), []byte("x"), time.Unix(1000, 0))
	calls := 0
	g := &dumpsGroup{roots: []string{root}, scan: func(roots []string) []DumpEntry {
		calls++
		entries, _ := catalogueDumps(roots)
		return entries
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
		g := &dumpsGroup{roots: roots, scanStatus: catalogueDumps}
		v, err := g.Collect(time.Unix(1000, 0), 0)
		if err != nil {
			t.Fatal(err)
		}
		if v != nil {
			t.Fatalf("no dumps must be a nil group (never 0), got %+v", v)
		}
	}
}

// An incomplete scan that found nothing must not read as "no dumps": the
// coverage flags travel with the result (W12-06).
func TestDumpsIncompleteScanIsNotNoDumps(t *testing.T) {
	for _, st := range []DumpScanStatus{
		{Truncated: true},
		{Unreadable: 2},
		{Truncated: true, Unreadable: 1},
	} {
		g := &dumpsGroup{roots: []string{t.TempDir()}, scanStatus: func([]string) ([]DumpEntry, DumpScanStatus) {
			return nil, st
		}}
		v, err := g.Collect(time.Unix(1000, 0), 0)
		if err != nil {
			t.Fatal(err)
		}
		if v == nil {
			t.Fatalf("status %+v: an incomplete scan was reported as no dumps", st)
		}
		dg := v.(*DumpsGroup)
		if dg.Truncated != st.Truncated || dg.Unreadable != st.Unreadable {
			t.Fatalf("status %+v: wire flags = truncated %v unreadable %d", st, dg.Truncated, dg.Unreadable)
		}
	}
	// A complete scan with nothing found is still a nil group, never a zero row.
	g := &dumpsGroup{roots: []string{t.TempDir()}, scanStatus: func([]string) ([]DumpEntry, DumpScanStatus) {
		return nil, DumpScanStatus{Visited: 3}
	}}
	if v, err := g.Collect(time.Unix(1000, 0), 0); err != nil || v != nil {
		t.Fatalf("complete empty scan = (%v, %v), want (nil, nil)", v, err)
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

// W12-06: a huge directory is enumerated in bounded batches, not read whole
// before the cap can stop it. The seam counts every entry the scan fetched.
func TestDumpsScanBoundsEnumeration(t *testing.T) {
	root := t.TempDir()
	t0 := time.Unix(1000, 0)
	for i := 0; i < 5000; i++ {
		writeDump(t, filepath.Join(root, fmt.Sprintf("e%04d.dmp", i)), []byte("x"), t0.Add(time.Duration(i)*time.Second))
	}
	fetched := 0
	batches := 0
	old := dumpReadDir
	dumpReadDir = func(f *os.File, n int) ([]fs.DirEntry, error) {
		if n > dumpReadBatch {
			t.Errorf("directory read asked for %d entries, want batches of at most %d", n, dumpReadBatch)
		}
		ents, err := old(f, n)
		fetched += len(ents)
		batches++
		return ents, err
	}
	t.Cleanup(func() { dumpReadDir = old })
	got, st := catalogueDumps([]string{root})
	if !st.Truncated {
		t.Fatalf("5000 entries must truncate the scan: %+v", st)
	}
	// The visit budget stops between batches: at most one batch past the cap.
	if fetched > dumpMaxVisited+dumpReadBatch {
		t.Fatalf("scan fetched %d entries, want at most %d (budget + one batch)", fetched, dumpMaxVisited+dumpReadBatch)
	}
	if batches > (dumpMaxVisited+dumpReadBatch)/dumpReadBatch+1 {
		t.Fatalf("scan made %d directory reads, want bounded batches", batches)
	}
	if len(got) == 0 || len(got) > dumpMaxListed {
		t.Fatalf("listed %d dumps, want 1..%d of the visited newest", len(got), dumpMaxListed)
	}
}

// W12-06: once the shared visit budget is exhausted, no further root is opened.
// The second root is a FILE: opening it as a directory would fail and count as
// unreadable, so Unreadable == 0 proves it was never attempted.
func TestDumpsStopsBeforeFurtherRoots(t *testing.T) {
	root := t.TempDir()
	t0 := time.Unix(1000, 0)
	for i := 0; i < 600; i++ {
		writeDump(t, filepath.Join(root, fmt.Sprintf("a%03d.txt", i)), []byte("x"), t0)
	}
	notADir := filepath.Join(t.TempDir(), "file-root")
	writeDump(t, notADir, []byte("x"), t0)
	got, st := catalogueDumps([]string{root, notADir})
	if !st.Truncated || st.Unreadable != 0 {
		t.Fatalf("exhausted scan touched a later root: %+v", st)
	}
	if len(got) != 0 {
		t.Fatalf("dumps from a truncated visit: %d", len(got))
	}
}

// W12-06: the kept set is the newest 200 OVER ALL visited roots, not the newest
// of each root separately: a late root's newer dumps displace the first root's
// older ones.
func TestDumpsNewestHeapAcrossRoots(t *testing.T) {
	root1, root2 := t.TempDir(), t.TempDir()
	t0 := time.Unix(1000, 0)
	for i := 0; i < 250; i++ {
		writeDump(t, filepath.Join(root1, fmt.Sprintf("old%03d.dmp", i)), []byte("x"), t0.Add(time.Duration(i)*time.Second))
	}
	for i := 0; i < 10; i++ {
		writeDump(t, filepath.Join(root2, fmt.Sprintf("new%02d.dmp", i)), []byte("x"), t0.Add(time.Duration(1000+i)*time.Second))
	}
	got, st := catalogueDumps([]string{root1, root2})
	if st.Truncated || len(got) != dumpMaxListed {
		t.Fatalf("status %+v, listed %d, want 200 untruncated", st, len(got))
	}
	for i := 0; i < 10; i++ {
		if got[i].Name != fmt.Sprintf("new%02d.dmp", 9-i) {
			t.Fatalf("newest heap lost a late root's dump at %d: %s", i, got[i].Name)
		}
	}
}

// W12-06: an unreadable root is reported, a missing root is normal.
func TestDumpsUnreadableStatus(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file-root")
	writeDump(t, notADir, []byte("x"), time.Unix(1000, 0))
	_, st := catalogueDumps([]string{notADir, filepath.Join(t.TempDir(), "absent")})
	if st.Unreadable != 1 || st.Truncated {
		t.Fatalf("status %+v, want one unreadable root and no truncation", st)
	}
	g := &dumpsGroup{roots: []string{notADir}, scanStatus: catalogueDumps}
	if _, err := g.Collect(time.Unix(2000, 0), 0); err != nil {
		t.Fatal(err)
	}
	if g.ScanStatus().Unreadable != 1 {
		t.Fatalf("group did not keep the scan status: %+v", g.ScanStatus())
	}
}

// W12-06: a junction (Windows) or symlink directory under a root is not
// followed, so a dump behind the alias never enters the catalogue. Only fake
// temp-dir data is used; no real private target is read.
func TestDumpsSkipsLinkDirectories(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()
	writeDump(t, filepath.Join(real, "behind.dmp"), []byte("x"), time.Unix(2000, 0))
	alias := filepath.Join(root, "alias")
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, real).CombinedOutput()
		if err != nil {
			t.Skipf("junction not creatable here: %v %s", err, out)
		}
	} else {
		if err := os.Symlink(real, alias); err != nil {
			t.Skipf("symlink not creatable here: %v", err)
		}
	}
	writeDump(t, filepath.Join(root, "plain.dmp"), []byte("x"), time.Unix(1000, 0))
	got, _ := catalogueDumps([]string{root})
	if len(got) != 1 || got[0].Name != "plain.dmp" {
		t.Fatalf("link directory was followed: %+v", got)
	}
	// The alias root itself is refused too.
	got2, st2 := catalogueDumps([]string{alias})
	if len(got2) != 0 || st2.Unreadable != 1 {
		t.Fatalf("alias root followed or not reported: %+v %+v", got2, st2)
	}
}