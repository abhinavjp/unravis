package blackbox

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memProgram struct{ data []byte }

func (m memProgram) Open() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(m.data)), nil }

func packCfg() Config {
	c := Default()
	c.Name = "web-b"
	c.Folder = `D:\UnravisBlackbox`
	return c
}

func unzipAll(t *testing.T, b []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		d, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = d
	}
	return out
}

func TestPackHoldsTheProgramTheSettingsAndAReadme(t *testing.T) {
	var buf bytes.Buffer
	info, err := BuildPack(&buf, packCfg(), memProgram{[]byte("MZ-fake-program")})
	if err != nil {
		t.Fatal(err)
	}
	if info.FileName != "unravis-blackbox-web-b.zip" || info.Bytes != int64(buf.Len()) || info.Name != "web-b" {
		t.Fatalf("%+v", info)
	}
	files := unzipAll(t, buf.Bytes())
	if len(files) != 3 || string(files["unravis-blackbox.exe"]) != "MZ-fake-program" {
		t.Fatalf("files: %v", files)
	}
	cfg, err := ParseConfig(files["blackbox.json"])
	if err != nil || cfg.Name != "web-b" || cfg.Folder != `D:\UnravisBlackbox` || cfg.Port != DefaultPort {
		t.Fatalf("settings: %+v %v", cfg, err)
	}
	rd := string(files["README.txt"])
	for _, want := range []string{"web-b", "install", "READ KEY", "not\r\n   in this zip", "Firewall", "snapshot"} {
		if !strings.Contains(rd, want) {
			t.Errorf("README misses %q", want)
		}
	}
}

// The read key is made on the server at install: nothing in the zip can hold one.
func TestPackNeverHoldsAReadKey(t *testing.T) {
	var buf bytes.Buffer
	if _, err := BuildPack(&buf, packCfg(), memProgram{[]byte("MZ")}); err != nil {
		t.Fatal(err)
	}
	for name, d := range unzipAll(t, buf.Bytes()) {
		if name == "unravis-blackbox.exe" {
			continue
		}
		if strings.Contains(string(d), "ubk-") || strings.Contains(strings.ToLower(string(d)), "readkeyhash") {
			t.Errorf("%s looks like it holds a key", name)
		}
	}
}

func TestPackIsTheSameBytesEveryTime(t *testing.T) {
	var a, b bytes.Buffer
	BuildPack(&a, packCfg(), memProgram{[]byte("MZ")})
	BuildPack(&b, packCfg(), memProgram{[]byte("MZ")})
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("not repeatable")
	}
}

func TestPackRefusesSettingsTheRecorderWouldRefuse(t *testing.T) {
	bad := packCfg()
	bad.Folder = `C:\Windows`
	var buf bytes.Buffer
	if _, err := BuildPack(&buf, bad, memProgram{[]byte("MZ")}); err == nil || buf.Len() != 0 {
		t.Fatalf("a bad folder built a pack: %v", err)
	}
	bad = packCfg()
	bad.IntervalSeconds = 7
	if _, err := BuildPack(&buf, bad, memProgram{[]byte("MZ")}); err == nil {
		t.Fatal("a bad interval built a pack")
	}
	bad = packCfg()
	bad.Name = ""
	if _, err := BuildPack(&buf, bad, memProgram{[]byte("MZ")}); err == nil {
		t.Fatal("no name built a pack")
	}
}

func TestPackWithoutTheProgramSaysWhy(t *testing.T) {
	var buf bytes.Buffer
	if _, err := BuildPack(&buf, packCfg(), nil); !errors.Is(err, ErrNoProgram) {
		t.Fatalf("nil: %v", err)
	}
	if _, err := BuildPack(&buf, packCfg(), memProgram{}); !errors.Is(err, ErrNoProgram) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := BuildPack(&buf, packCfg(), FileProgram{Path: filepath.Join(t.TempDir(), "none.exe")}); !errors.Is(err, ErrNoProgram) {
		t.Fatalf("missing: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatal("partial pack written")
	}
}

func TestPackTooBigIsRefused(t *testing.T) {
	var buf bytes.Buffer
	if _, err := BuildPack(&buf, packCfg(), memProgram{make([]byte, MaxPackBytes+10)}); err == nil || buf.Len() != 0 {
		t.Fatalf("an over-size pack was built: %v", err)
	}
}

func TestFindProgramNextToUnravis(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if _, ok := FindProgram(a, b, ""); ok {
		t.Fatal("found nothing but said found")
	}
	if err := os.WriteFile(filepath.Join(b, PackExeName), []byte("MZ"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok := FindProgram(a, "", b)
	if !ok || p.Path != filepath.Join(b, PackExeName) {
		t.Fatalf("%+v %v", p, ok)
	}
	if err := os.Mkdir(filepath.Join(a, PackExeName), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, _ := FindProgram(a, b); p.Path != filepath.Join(b, PackExeName) {
		t.Fatal("a folder named like the program was taken")
	}
}
