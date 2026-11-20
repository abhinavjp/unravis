package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAssets(t *testing.T, dir, ver string, withLite bool) {
	t.Helper()
	files := []string{
		"unravis-setup-v" + ver + "-windows-x64.exe", "unravis-v" + ver + "-windows-x64.zip",
		"unravis-blackbox-v" + ver + "-windows-x64.zip", "unravis-blackbox.exe",
	}
	if withLite {
		files = append(files, "unravis-lite-v"+ver+"-windows-x64.zip")
	}
	for i, f := range files {
		body := []byte(strings.Repeat("x", i+1))
		if strings.HasPrefix(f, "unravis-blackbox-v") { // a real zip holding the same exe bytes as dist/unravis-blackbox.exe
			body = zipOf(t, "unravis-blackbox.exe", []byte(strings.Repeat("x", 4)))
		}
		if err := os.WriteFile(filepath.Join(dir, f), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func zipOf(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(body)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestKeyFileRoundTripAndWrongPassphrase(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	t.Setenv("UNRAVIS_KEY_PASSPHRASE", "correct horse battery")
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	if err := run([]string{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, nil, &out); err == nil {
		t.Fatal("keygen overwrote an existing key")
	}
	b, _ := os.ReadFile(kp)
	if _, _, err := openKey(b, "wrong passphrase!!"); err == nil {
		t.Fatal("wrong passphrase opened the key")
	}
	if _, _, err := openKey(bytes.Replace(b, []byte(`"role": "rc"`), []byte(`"role": "stable"`), 1), "correct horse battery"); err == nil {
		t.Fatal("edited role was accepted")
	}
	if _, err := sealKey("rc", nil, "short", 1); err == nil {
		t.Fatal("short passphrase accepted")
	}
}

func TestManifestSignVerifyEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UNRAVIS_KEY_PASSPHRASE", "correct horse battery")
	var out bytes.Buffer
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	sp, pp2 := filepath.Join(dir, "s.json"), filepath.Join(dir, "s.pub")
	if err := run([]string{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"keygen", "-role", "stable", "-out", sp, "-pub", pp2}, nil, &out); err != nil {
		t.Fatal(err)
	}
	writeAssets(t, dir, "0.1.0-rc.48", false)
	mf, sg := filepath.Join(dir, "update-manifest.json"), filepath.Join(dir, "update-manifest.json.sig")
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.48", "-seq", "48", "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	// The stable key must refuse to sign an rc manifest.
	if err := run([]string{"sign", "-key", sp, "-in", mf, "-out", sg}, nil, &out); err == nil {
		t.Fatal("stable key signed an rc manifest")
	}
	if err := run([]string{"sign", "-key", kp, "-in", mf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp + "," + pp2, "-channel", "rc"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	// A stable build must refuse it.
	if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp + "," + pp2, "-channel", "stable"}, nil, &out); err == nil {
		t.Fatal("stable build accepted an rc manifest")
	}
	// Changing one byte of an asset hash breaks the signature.
	b, _ := os.ReadFile(mf)
	if err := os.WriteFile(mf, bytes.Replace(b, []byte(`"size": 1`), []byte(`"size": 2`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp, "-channel", "rc"}, nil, &out); err == nil {
		t.Fatal("tampered manifest verified")
	}
}

func TestManifestNeedsRealAssets(t *testing.T) {
	dir := t.TempDir()
	if _, err := buildManifest(dir, "0.1.0", 1, 30, "", time.Now()); err == nil {
		t.Fatal("manifest built from an empty folder")
	}
	writeAssets(t, dir, "0.1.0", true)
	m, err := buildManifest(dir, "0.1.0", 1, 30, "", time.Now())
	if err != nil || m.Channel != "stable" || len(m.Assets) != 4 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := buildManifest(dir, "v0.1.0", 1, 30, "", time.Now()); err == nil {
		t.Fatal("v prefix accepted")
	}
}

func TestCheckAssets(t *testing.T) {
	dir := t.TempDir()
	writeAssets(t, dir, "0.1.0", false)
	m, err := buildManifest(dir, "0.1.0", 1, 30, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := checkAssets(m, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unravis-v0.1.0-windows-x64.zip"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkAssets(m, dir); err == nil {
		t.Fatal("changed file passed")
	}
}

func TestCheckAssetsBlackboxExeHash(t *testing.T) {
	dir := t.TempDir()
	writeAssets(t, dir, "0.1.0", false) // dist exe is "xxxx" (4th file), the zip holds "xxxx"
	m, err := buildManifest(dir, "0.1.0", 1, 30, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := checkAssets(m, dir); err != nil {
		t.Fatal(err)
	}
	m.BlackboxExeSHA256 = strings.Repeat("0", 64)
	if err := checkAssets(m, dir); err == nil {
		t.Fatal("wrong blackboxExeSha256 passed")
	}
}

func TestVerifyNeedsRealChannel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UNRAVIS_KEY_PASSPHRASE", "correct horse battery")
	var out bytes.Buffer
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	if err := run([]string{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, nil, &out); err != nil {
		t.Fatal(err)
	}
	writeAssets(t, dir, "0.1.0-rc.1", false)
	mf, sg := filepath.Join(dir, "m.json"), filepath.Join(dir, "m.sig")
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.1", "-seq", "1", "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-key", kp, "-in", mf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []string{"", "Stable", "beta"} {
		if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp, "-channel", ch}, nil, &out); err == nil {
			t.Errorf("channel %q accepted", ch)
		}
	}
	if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp, "-channel", "rc", "-want", "stable"}, nil, &out); err == nil {
		t.Error("rc manifest passed -want stable")
	}
}

func TestPreflight(t *testing.T) {
	dir := t.TempDir()
	writeAssets(t, dir, "0.1.0-rc.48", false)
	mf := filepath.Join(dir, "m.json")
	var out bytes.Buffer
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.48", "-seq", "48", "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v0.1.0-rc.48", "", &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v99.0.0-rc.1", "", &out); err == nil {
		t.Fatal("wrong tag passed")
	}
	// the feed already holds a newer one
	writeAssets(t, dir, "0.1.0-rc.60", false)
	feed := filepath.Join(dir, "feed.json")
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.60", "-seq", "60", "-out", feed}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v0.1.0-rc.48", feed, &out); err == nil {
		t.Fatal("older manifest allowed past a newer feed")
	}
	if err := preflight(feed, "v0.1.0-rc.60", mf, &out); err != nil {
		t.Fatal(err)
	}
}
