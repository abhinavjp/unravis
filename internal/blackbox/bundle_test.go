package blackbox

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type tinyDisk struct{ free uint64 }

func (d tinyDisk) Free(string) (DiskInfo, error) { return DiskInfo{Free: d.free, Total: 1 << 30}, nil }

func bundleRig(t *testing.T) (Config, time.Time) {
	t.Helper()
	cfg := testConfig(t)
	cfg.Name = "web-b"
	if err := os.MkdirAll(cfg.Folder, 0o750); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 26, 12, 30, 0, 0, time.UTC)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(cfg.Folder, name), []byte(body), 0o644); err != nil { //nolint:gosec // test file
			t.Fatal(err)
		}
	}
	write("bb-20261126-08.ndjson", `{"t":"x08"}`+"\n")
	write("bb-20261126-10.ndjson", `{"t":"x10","facts":{"os":"old"}}`+"\n")
	write("bb-20261126-11.ndjson", `{"t":"x11a"}`+"\n"+`{"t":"x11b","facts":{"os":"new"}}`+"\n"+`{"t":"x11c"}`+"\n")
	write("bb-20261126-12.ndjson", `{"t":"x12"}`+"\n")
	write("bb-20261126-13.ndjson", `{"t":"future"}`+"\n") // after now: not part of it
	write(ManifestName, `{"version":1}`)
	write("notes.txt", "not a recording")
	return cfg, now
}

func readZip(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestBundleHoldsTheWindowAndEveryHashIsRight(t *testing.T) {
	cfg, now := bundleRig(t)
	res, err := MakeBundle(cfg, now, BundleOptions{Since: 90 * time.Minute, Out: "case-1"})
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, res.Path)
	for _, want := range []string{"recordings/bb-20261126-11.ndjson", "recordings/bb-20261126-12.ndjson", "blackbox.json", "recorder-manifest.json", "facts.json", "bundle-manifest.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("%s is missing: %v", want, keys(files))
		}
	}
	for _, not := range []string{"recordings/bb-20261126-08.ndjson", "recordings/bb-20261126-10.ndjson", "recordings/bb-20261126-13.ndjson", "notes.txt"} {
		if _, ok := files[not]; ok {
			t.Errorf("%s is in the bundle but outside the window or not a recording", not)
		}
	}
	if files["facts.json"] != `{"os":"new"}` {
		t.Errorf("facts.json: %q (the newest facts block must win)", files["facts.json"])
	}
	// The manifest inside the zip lists every other file with its real hash.
	var inner BundleManifest
	if err := json.Unmarshal([]byte(files["bundle-manifest.json"]), &inner); err != nil {
		t.Fatal(err)
	}
	for _, e := range inner.Files {
		if e.Name == "bundle-manifest.json" {
			continue
		}
		sum := sha256.Sum256([]byte(files[e.Name]))
		if hex.EncodeToString(sum[:]) != e.SHA256 || int64(len(files[e.Name])) != e.Bytes {
			t.Errorf("%s: manifest does not match its content", e.Name)
		}
	}
	// The copy beside the zip carries the zip's own hash.
	raw, _ := os.ReadFile(res.ManifestPath)
	var outer BundleManifest
	if err := json.Unmarshal(raw, &outer); err != nil {
		t.Fatal(err)
	}
	zb, _ := os.ReadFile(res.Path)
	sum := sha256.Sum256(zb)
	if outer.ZipSHA256 != hex.EncodeToString(sum[:]) || outer.ZipBytes != int64(len(zb)) || res.SHA256 != outer.ZipSHA256 {
		t.Error("the manifest beside the zip does not carry the zip's hash")
	}
	if !strings.HasPrefix(res.Path, filepath.Join(cfg.Folder, SnapshotsDir)) || filepath.Base(res.Path) != "case-1.zip" {
		t.Errorf("path %s", res.Path)
	}
	// A second bundle of the same name does not overwrite the first.
	res2, err := MakeBundle(cfg, now, BundleOptions{Since: 90 * time.Minute, Out: "case-1"})
	if err != nil || res2.Path == res.Path {
		t.Fatalf("%v %s", err, res2.Path)
	}
}

func keys(m map[string]string) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	return k
}

func TestBundleRefusalsLeaveNothingBehind(t *testing.T) {
	cfg, now := bundleRig(t)
	snaps := filepath.Join(cfg.Folder, SnapshotsDir)
	check := func(name string, o BundleOptions) {
		t.Helper()
		if _, err := MakeBundle(cfg, now, o); err == nil {
			t.Errorf("%s: no error", name)
		}
		if ents, _ := os.ReadDir(snaps); len(ents) != 0 {
			t.Errorf("%s: left behind %d entries", name, len(ents))
		}
	}
	for _, out := range []string{"../x", "a/b", `a\b`, "x.zip", ".hidden", "", " ", "NUL", "con", "COM1", "lpt9", "nul.txt"} {
		if out == "" {
			continue // the default name is fine
		}
		check("out "+out, BundleOptions{Out: out})
	}
	check("too short", BundleOptions{Since: time.Second})
	check("too long", BundleOptions{Since: 30 * 24 * time.Hour})
	check("drive full", BundleOptions{Disk: tinyDisk{free: 3}})
	old := bundleMaxBytes
	bundleMaxBytes = 10
	check("too big", BundleOptions{})
	bundleMaxBytes = old
	cfg.Folder = filepath.Join(cfg.Folder, "nope")
	if _, err := MakeBundle(cfg, now, BundleOptions{}); err == nil {
		t.Error("a missing recordings folder was accepted")
	}
	// A window with no recordings.
	cfg2, _ := bundleRig(t)
	if _, err := MakeBundle(cfg2, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), BundleOptions{Since: time.Hour}); err == nil {
		t.Error("an empty window was accepted")
	}
}

func TestBundleWithoutFactsSaysSo(t *testing.T) {
	cfg, now := bundleRig(t)
	_ = os.Remove(filepath.Join(cfg.Folder, "bb-20261126-10.ndjson"))
	if err := os.WriteFile(filepath.Join(cfg.Folder, "bb-20261126-11.ndjson"), []byte(`{"t":"x"}`+"\n"), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	res, err := MakeBundle(cfg, now, BundleOptions{Since: 90 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readZip(t, res.Path)["facts.json"]; ok {
		t.Error("facts.json written without facts")
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "no server facts") {
		t.Errorf("notes %v", res.Notes)
	}
}

func TestParseBundleSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 2 * time.Hour, "90m": 90 * time.Minute, "2h": 2 * time.Hour, "1d": 24 * time.Hour, "7d": 7 * 24 * time.Hour} {
		if got, err := ParseBundleSince(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"0m", "8d", "soon", "-1h", "10s", "1.5d", "213504d"} {
		if _, err := ParseBundleSince(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestBundleCoverageStartsAtTheFirstWholeHourAndNoTempFilesRemain(t *testing.T) {
	cfg, now := bundleRig(t)
	res, err := MakeBundle(cfg, now, BundleOptions{Since: 90 * time.Minute, Out: "cov"})
	if err != nil {
		t.Fatal(err)
	}
	// 12:30 minus 90 minutes is 11:00, and the 11:00 hour file is the first one in.
	if want := time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC); !res.From.Equal(want) {
		t.Errorf("from %v, want %v", res.From, want)
	}
	// 10:30 would start inside the 10:00 hour: the whole hour comes in and the coverage says so.
	res2, err := MakeBundle(cfg, now, BundleOptions{Since: 2 * time.Hour, Out: "cov2"})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC); !res2.From.Equal(want) {
		t.Errorf("from %v, want %v", res2.From, want)
	}
	ents, _ := os.ReadDir(filepath.Join(cfg.Folder, SnapshotsDir))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestBundleNeverOverwritesAnExistingManifestOrZip(t *testing.T) {
	cfg, now := bundleRig(t)
	snaps := filepath.Join(cfg.Folder, SnapshotsDir)
	if err := os.MkdirAll(snaps, 0o750); err != nil {
		t.Fatal(err)
	}
	man := filepath.Join(snaps, "case.manifest.json")
	if err := os.WriteFile(man, []byte("keep me"), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	res, err := MakeBundle(cfg, now, BundleOptions{Since: 90 * time.Minute, Out: "case"})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(man); string(b) != "keep me" { //nolint:gosec // test file
		t.Errorf("the existing manifest was overwritten: %q", b)
	}
	if filepath.Base(res.Path) != "case-2.zip" {
		t.Errorf("path %s", res.Path)
	}
}
