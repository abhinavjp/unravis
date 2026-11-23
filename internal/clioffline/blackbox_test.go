package clioffline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/clicore"
)

var t0 = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

// record writes a real recording with the recorder's own store: n samples, 20 s apart, from t0 + 59 min (so it spans two hourly files).
func record(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	now := t0
	s, err := blackbox.OpenStore(dir, blackbox.StoreOptions{Name: "box", Version: "test", Host: "HOST1", IntervalSeconds: 20, RunID: "r1", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	at := t0.Add(59*time.Minute + 30*time.Second)
	for i := 0; i < n; i++ {
		now = at
		cpu := float64(i)
		if err := s.Append(blackbox.Sample{Run: "r1", Seq: uint64(i + 1), T: at, CPU: &cpu}, ""); err != nil {
			t.Fatal(err)
		}
		at = at.Add(20 * time.Second)
	}
	if err := s.Close("stopped"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// tree lists every file with its hash and mode time, so a change of any kind shows.
func tree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, p)
		info, _ := d.Info()
		line := rel + "|" + info.ModTime().UTC().Format(time.RFC3339Nano) + "|" + info.Mode().String()
		if d.Type().IsRegular() {
			b, _ := os.ReadFile(p) //nolint:gosec // a temp tree of this test
			h := sha256.Sum256(b)
			line += "|" + hex.EncodeToString(h[:])
		}
		lines = append(lines, line)
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestInspectRealRecording(t *testing.T) {
	dir := record(t, 6)
	res, e := Inspect(context.Background(), dir, Options{Samples: 2})
	if e != nil {
		t.Fatal(e)
	}
	if res.Kind != "folder" || res.Input != dir || res.Manifest == nil {
		t.Fatalf("%+v", res)
	}
	if res.Manifest.State != "stopped" || res.Manifest.Host != "HOST1" || res.Manifest.Files != 2 || res.Manifest.Samples != 6 || res.Manifest.Version < 1 {
		t.Errorf("manifest: %+v", res.Manifest)
	}
	if res.Samples.Count != 6 || res.Samples.From == nil || res.Samples.To == nil || !res.Samples.To.After(*res.Samples.From) {
		t.Errorf("samples: %+v", res.Samples)
	}
	if len(res.Samples.First) != 2 || res.Samples.First[0].Seq != 1 || res.Samples.First[1].Seq != 2 {
		t.Errorf("first: %+v", res.Samples.First)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings: %v", res.Warnings)
	}
	b, err := clicore.ASCIIJSON(res)
	if err != nil || !json.Valid(b) {
		t.Fatalf("json: %v %q", err, b)
	}
	for _, c := range b {
		if c >= 0x80 {
			t.Fatal("not 7-bit")
		}
	}
	// without --samples, no sample is printed
	res, e = Inspect(context.Background(), dir, Options{})
	if e != nil || res.Samples.First != nil || strings.Contains(string(mustJSON(t, res)), `"first"`) {
		t.Errorf("samples printed without --samples: %v", e)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOfflineDoesNotWrite(t *testing.T) {
	dir := record(t, 8)
	before := tree(t, dir)
	for _, opt := range []Options{{}, {Samples: 5}, {MaxBytes: 1}} {
		_, _ = Inspect(context.Background(), dir, opt)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "bb-*.ndjson"))
	for _, f := range files {
		_, _ = Inspect(context.Background(), f, Options{})
	}
	if after := tree(t, dir); after != before {
		t.Errorf("the input changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestInspectOneFile(t *testing.T) {
	dir := record(t, 6)
	res, e := Inspect(context.Background(), filepath.Join(dir, "bb-20261027-10.ndjson"), Options{})
	if e != nil {
		t.Fatal(e)
	}
	if res.Kind != "file" || res.Manifest != nil || res.Samples.Count != 2 {
		t.Errorf("%+v", res)
	}
	if _, e := Inspect(context.Background(), filepath.Join(dir, "manifest.json"), Options{}); e == nil || e.Code != clicore.CodeInvalidArgument {
		t.Errorf("manifest.json is not an hourly file: %v", e)
	}
}

func TestOfflineRefusesLiveCaseDir(t *testing.T) {
	for _, name := range []string{"case.db", "case.key", "x.sqlite", "Case.DB", "a.unravis-case", "case.key.1.tmp"} {
		dir := record(t, 3)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		before := tree(t, dir)
		_, e := Inspect(context.Background(), dir, Options{})
		if e == nil || e.Code != clicore.CodeLiveCaseRefused || clicore.ExitCode(e.Code) != 2 {
			t.Errorf("%s: %v", name, e)
		}
		if tree(t, dir) != before {
			t.Errorf("%s: the directory changed", name)
		}
		if e != nil && strings.Contains(e.Message+e.Action, dir) {
			t.Errorf("the path is in the message: %s", e.Message)
		}
	}
	dir := record(t, 3)
	if err := os.Mkdir(filepath.Join(dir, "cases"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, e := Inspect(context.Background(), dir, Options{}); e == nil || e.Code != clicore.CodeLiveCaseRefused {
		t.Errorf("a cases folder: %v", e)
	}
}

func TestOfflineOversizeAndMalformed(t *testing.T) {
	ctx := context.Background()
	dir := record(t, 6)
	if _, e := Inspect(ctx, dir, Options{MaxBytes: 10}); e == nil || e.Code != clicore.CodeRequestTooLarge || clicore.ExitCode(e.Code) != 2 {
		t.Errorf("oversize folder: %v", e)
	}
	if _, e := Inspect(ctx, filepath.Join(dir, "bb-20261027-10.ndjson"), Options{MaxBytes: 10}); e == nil || e.Code != clicore.CodeRequestTooLarge {
		t.Errorf("oversize file: %v", e)
	}
	for _, o := range []Options{{Samples: -1}, {Samples: 1001}} {
		if _, e := Inspect(ctx, dir, o); e == nil || e.Code != clicore.CodeInvalidArgument {
			t.Errorf("%+v: %v", o, e)
		}
	}
	for _, in := range []string{"", "a\x00b"} {
		if _, e := Inspect(ctx, in, Options{}); e == nil || e.Code != clicore.CodeInvalidArgument {
			t.Errorf("%q: %v", in, e)
		}
	}
	if _, e := Inspect(ctx, filepath.Join(dir, "nope"), Options{}); e == nil || e.Code != clicore.CodeNotFound {
		t.Errorf("missing: %v", e)
	}
	// a folder with no manifest
	if _, e := Inspect(ctx, t.TempDir(), Options{}); e == nil || e.Code != clicore.CodeNotFound {
		t.Errorf("no manifest: %v", e)
	}
	// a manifest that is not JSON
	bad := t.TempDir()
	_ = os.WriteFile(filepath.Join(bad, "manifest.json"), []byte("{not json"), 0o600)
	if _, e := Inspect(ctx, bad, Options{}); e == nil || clicore.ExitCode(e.Code) != 2 {
		t.Errorf("bad manifest: %v", e)
	}
	// a manifest from a newer format
	newer := t.TempDir()
	_ = os.WriteFile(filepath.Join(newer, "manifest.json"), []byte(`{"format":999}`), 0o600)
	if _, e := Inspect(ctx, newer, Options{}); e == nil || e.Code != clicore.CodeVersionIncompatible {
		t.Errorf("newer: %v", e)
	}
	// a truncated last line: counted up to the cut, with a warning
	torn := record(t, 6)
	f := filepath.Join(torn, "bb-20261027-11.ndjson")
	b, _ := os.ReadFile(f)                    //nolint:gosec // a temp tree of this test
	_ = os.WriteFile(f, b[:len(b)-15], 0o600) //nolint:gosec // a temp tree of this test
	res, e := Inspect(ctx, torn, Options{})
	if e != nil || res.Samples.Count != 5 || len(res.Warnings) == 0 {
		t.Errorf("torn: %v %+v", e, res)
	}
	// a line longer than the reader allows: no panic, no hang, a warning or an error
	huge := record(t, 3)
	hf := filepath.Join(huge, "bb-20261027-11.ndjson")
	hb, _ := os.ReadFile(hf) //nolint:gosec // a temp tree of this test
	_ = os.WriteFile(hf, append(append([]byte(nil), hb...), []byte(strings.Repeat("a", 5<<20)+"\n")...), 0o600) //nolint:gosec // a temp tree of this test
	if res, e := Inspect(ctx, huge, Options{}); e == nil && len(res.Warnings) == 0 && res.Samples.Count > 3 {
		t.Errorf("huge line: %+v", res)
	}
	// a hand-made file named like a recording, full of garbage
	junk := t.TempDir()
	_ = os.WriteFile(filepath.Join(junk, "bb-20261027-10.ndjson"), []byte("\x00\xff\n{}\n[[[\n"), 0o600)
	if res, e := Inspect(ctx, filepath.Join(junk, "bb-20261027-10.ndjson"), Options{}); e != nil || res.Samples.Count > 1 {
		t.Errorf("junk: %v %+v", e, res)
	}
}

func TestOfflineRefusesLinks(t *testing.T) {
	dir := record(t, 3)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if _, e := Inspect(context.Background(), link, Options{}); e == nil || e.Code != clicore.CodeInvalidArgument {
		t.Errorf("a link as input: %v", e)
	}
	// a link inside the folder, named like a recording, that points outside
	outside := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outside, []byte(`{"v":1,"run":"x","seq":1,"t":"2026-10-27T10:00:00Z"}`+"\n"), 0o600)
	l2 := filepath.Join(dir, "bb-20261027-09.ndjson")
	if err := os.Symlink(outside, l2); err != nil {
		t.Skip(err)
	}
	res, e := Inspect(context.Background(), dir, Options{})
	if e != nil && e.Code == clicore.CodeInvalidArgument {
		return // refused outright: fine
	}
	if e == nil && res.Samples.Count != 3 {
		t.Errorf("a linked file was read: %+v", res.Samples)
	}
}

func FuzzOfflineManifest(f *testing.F) {
	f.Add([]byte(`{"format":1,"state":"stopped","files":[{"name":"bb-20261027-10.ndjson","hour":"2026-10-27T10:00:00Z","bytes":10,"samples":1}]}`))
	f.Add([]byte(`{"format":1,"files":[{"name":"../../etc/passwd"}]}`))
	f.Add([]byte(`{"format":1,"files":null,"gaps":[1],"events":"x"}`))
	f.Add([]byte(`[]`))
	f.Add([]byte("\xef\xbb\xbf{\"format\":1}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
			t.Skip()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, e := Inspect(ctx, dir, Options{Samples: 3})
		if (res == nil) == (e == nil) {
			t.Fatalf("exactly one of result and error: %v %v", res, e)
		}
		if e != nil && (e.Code == "" || strings.Contains(e.Message, dir)) {
			t.Fatalf("bad error: %+v", e)
		}
	})
}

// listInManifest adds a file to the manifest of a recording, as a hostile or damaged manifest would.
func listInManifest(t *testing.T, dir string, entries ...blackbox.FileEntry) {
	t.Helper()
	m, err := blackbox.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Files = append(m.Files, entries...)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLinkListedInManifestIsNotRead(t *testing.T) {
	dir := record(t, 3)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	line := `{"v":1,"run":"x","seq":1,"t":"2026-10-27T09:00:10Z"}` + "\n"
	_ = os.WriteFile(outside, []byte(line), 0o600)
	name := "bb-20261027-09.ndjson"
	if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
		t.Skip("no symlinks here:", err)
	}
	listInManifest(t, dir, blackbox.FileEntry{Name: name, Hour: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), Samples: 1})
	res, e := Inspect(context.Background(), dir, Options{})
	if e != nil {
		return // refused outright is fine
	}
	if res.Samples.Count != 3 {
		t.Fatalf("a file behind a link was read: %+v", res.Samples)
	}
	if len(res.Warnings) == 0 {
		t.Error("the unreadable file should be reported")
	}
}

func TestInputLinkIsRefusedAsALink(t *testing.T) {
	dir := record(t, 3)
	base := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	for _, in := range []string{link, link + "/", link + "/."} {
		_, e := Inspect(context.Background(), in, Options{})
		if e == nil || !strings.Contains(e.Message, "link") {
			t.Errorf("%q: %v", in, e)
		}
	}
	flink := filepath.Join(base, "bb-20261027-10.ndjson")
	if err := os.Symlink(filepath.Join(dir, "bb-20261027-10.ndjson"), flink); err == nil {
		if _, e := Inspect(context.Background(), flink, Options{}); e == nil || !strings.Contains(e.Message, "link") {
			t.Errorf("a linked file: %v", e)
		}
	}
}

func TestSizeCapFollowsTheManifest(t *testing.T) {
	dir := record(t, 6)
	// the manifest lists one real file many times: it is read once
	e0 := blackbox.FileEntry{Name: "bb-20261027-10.ndjson", Hour: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)}
	many := make([]blackbox.FileEntry, 150)
	for i := range many {
		many[i] = e0
	}
	listInManifest(t, dir, many...)
	res, e := Inspect(context.Background(), dir, Options{})
	if e != nil || res.Samples.Count != 6 {
		t.Fatalf("duplicates: %v %+v", e, res)
	}
	// a file that is larger than the cap when it is read, whatever the listing said: the cap is on the bytes taken in
	var size int64
	for _, n := range []string{"bb-20261027-10.ndjson", "bb-20261027-11.ndjson"} {
		fi, _ := os.Stat(filepath.Join(dir, n))
		size += fi.Size()
	}
	if _, e := Inspect(context.Background(), dir, Options{MaxBytes: size - 1}); e == nil || e.Code != clicore.CodeRequestTooLarge {
		t.Errorf("cap just under the size: %v", e)
	}
	if _, e := Inspect(context.Background(), dir, Options{MaxBytes: size}); e != nil {
		t.Errorf("cap at the size: %v", e)
	}
}

func TestCaseDataNames(t *testing.T) {
	for _, name := range []string{"case.db", "case.db-wal", "case.db-shm", "case.key", "case.wrap", "x.sqlite3", "x.db", "x.db-wal", "a.unravis-case", "a.unravis-case.part", "manifest.dat", "unresolved.dat", "CASE.KEY"} {
		if !liveCaseName(name) {
			t.Errorf("%s should count as case data", name)
		}
	}
	for _, name := range []string{"manifest.json", "bb-20261027-10.ndjson", "readme.txt", "bb-20261027-10.ndjson.tmp", "database.txt"} {
		if liveCaseName(name) {
			t.Errorf("%s is not case data", name)
		}
	}
	// a case file in a subfolder, and an hourly file read from a folder that holds case data
	dir := record(t, 3)
	sub := filepath.Join(dir, "old")
	_ = os.Mkdir(sub, 0o700)
	_ = os.WriteFile(filepath.Join(sub, "case.db"), []byte("x"), 0o600)
	if _, e := Inspect(context.Background(), dir, Options{}); e == nil || e.Code != clicore.CodeLiveCaseRefused {
		t.Errorf("subfolder: %v", e)
	}
	if _, e := Inspect(context.Background(), filepath.Join(dir, "bb-20261027-10.ndjson"), Options{}); e != nil && e.Code != clicore.CodeLiveCaseRefused {
		t.Errorf("file: %v", e)
	}
	dir2 := record(t, 3)
	_ = os.WriteFile(filepath.Join(dir2, "case.db"), []byte("x"), 0o600)
	if _, e := Inspect(context.Background(), filepath.Join(dir2, "bb-20261027-10.ndjson"), Options{}); e == nil || e.Code != clicore.CodeLiveCaseRefused {
		t.Errorf("one file next to case data: %v", e)
	}
}

func TestErrorKindsAndWarnings(t *testing.T) {
	dir := record(t, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Inspect(ctx, dir, Options{}); e == nil || e.Code != clicore.CodeCanceled {
		t.Errorf("canceled: %v", e)
	}
	dctx, dcancel := context.WithTimeout(context.Background(), -time.Second)
	defer dcancel()
	if _, e := Inspect(dctx, dir, Options{}); e == nil || e.Code != clicore.CodeTimeout {
		t.Errorf("deadline: %v", e)
	}
	// hours in the manifest that do not match the files: nothing found, and it says so
	bad := record(t, 3)
	m, _ := blackbox.ReadManifest(bad)
	for i := range m.Files {
		m.Files[i].Hour = m.Files[i].Hour.Add(48 * time.Hour)
	}
	b, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(bad, "manifest.json"), b, 0o600)
	res, e := Inspect(context.Background(), bad, Options{})
	if e != nil || res.Samples.Count != 0 || len(res.Warnings) == 0 {
		t.Errorf("wrong hours: %v %+v", e, res)
	}
}

func FuzzOfflineFile(f *testing.F) {
	f.Add([]byte(`{"v":1,"run":"r","seq":1,"t":"2026-10-27T10:00:00Z","cpu":3}` + "\n"))
	f.Add([]byte("{\n\x00\xff[[[\n"))
	f.Add([]byte(strings.Repeat("{", 5000)))
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		p := filepath.Join(dir, "bb-20261027-10.ndjson")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Skip()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		res, e := Inspect(ctx, p, Options{Samples: 3})
		if (res == nil) == (e == nil) {
			t.Fatalf("exactly one of result and error: %v %v", res, e)
		}
		if res != nil && res.Samples.Count < 0 {
			t.Fatal("negative count")
		}
	})
}
