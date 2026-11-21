package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/update"
)

const commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

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
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.48", "-seq", "48", "-commit", commitA, "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	// The stable key must refuse to sign an rc manifest.
	if err := run([]string{"sign", "-key", sp, "-in", mf, "-out", sg}, nil, &out); err == nil {
		t.Fatal("stable key signed an rc manifest")
	}
	if err := run([]string{"sign", "-key", kp, "-in", mf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp + "," + pp2, "-channel", "rc", "-want", "rc"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	// A stable build must refuse it.
	if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp + "," + pp2, "-channel", "stable", "-want", "rc"}, nil, &out); err == nil {
		t.Fatal("stable build accepted an rc manifest")
	}
	// Changing one byte of an asset hash breaks the signature.
	b, _ := os.ReadFile(mf)
	if err := os.WriteFile(mf, bytes.Replace(b, []byte(`"size": 1`), []byte(`"size": 2`), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp, "-channel", "rc", "-want", "rc"}, nil, &out); err == nil {
		t.Fatal("tampered manifest verified")
	}
}

func TestManifestNeedsRealAssets(t *testing.T) {
	dir := t.TempDir()
	if _, err := buildManifest(dir, "0.1.0", 1, 30, "", commitA, time.Now()); err == nil {
		t.Fatal("manifest built from an empty folder")
	}
	writeAssets(t, dir, "0.1.0", true)
	m, err := buildManifest(dir, "0.1.0", 1, 30, "", commitA, time.Now())
	if err != nil || m.Channel != "stable" || len(m.Assets) != 4 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := buildManifest(dir, "v0.1.0", 1, 30, "", commitA, time.Now()); err == nil {
		t.Fatal("v prefix accepted")
	}
}

func TestCheckAssets(t *testing.T) {
	dir := t.TempDir()
	writeAssets(t, dir, "0.1.0", false)
	m, err := buildManifest(dir, "0.1.0", 1, 30, "", commitA, time.Now())
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
	m, err := buildManifest(dir, "0.1.0", 1, 30, "", commitA, time.Now())
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
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.1", "-seq", "1", "-commit", commitA, "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-key", kp, "-in", mf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []string{"", "Stable", "beta"} {
		if err := run([]string{"verify", "-in", mf, "-sig", sg, "-pub", pp, "-channel", ch, "-want", "rc"}, nil, &out); err == nil {
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
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.48", "-seq", "48", "-commit", commitA, "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v0.1.0-rc.48", commitA, "", &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v99.0.0-rc.1", commitA, "", &out); err == nil {
		t.Fatal("wrong tag passed")
	}
	// the feed already holds a newer one
	writeAssets(t, dir, "0.1.0-rc.60", false)
	feed := filepath.Join(dir, "feed.json")
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.60", "-seq", "60", "-commit", commitA, "-out", feed}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v0.1.0-rc.48", commitA, feed, &out); err == nil {
		t.Fatal("older manifest allowed past a newer feed")
	}
	if err := preflight(feed, "v0.1.0-rc.60", commitA, mf, &out); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightCommitAndExpiry(t *testing.T) {
	dir := t.TempDir()
	writeAssets(t, dir, "0.1.0-rc.1", false)
	mf := filepath.Join(dir, "m.json")
	var out bytes.Buffer
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.1", "-seq", "1", "-commit", commitA, "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v0.1.0-rc.1", strings.Repeat("b", 40), "", &out); err == nil {
		t.Fatal("manifest from another commit passed")
	}
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.1", "-seq", "1", "-commit", commitA, "-days", "20", "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := preflight(mf, "v0.1.0-rc.1", commitA, "", &out); err == nil {
		t.Fatal("nearly expired manifest passed")
	}
}

func TestPassphraseMustBePlainAscii(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := sealKey("rc", priv, "pässword-with-accent", 1000); err == nil {
		t.Fatal("non-ASCII passphrase accepted")
	}
}

func TestControlFileEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UNRAVIS_KEY_PASSPHRASE", "correct horse battery")
	var out bytes.Buffer
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	sp, pp2 := filepath.Join(dir, "s.json"), filepath.Join(dir, "s.pub")
	for _, a := range [][]string{{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, {"keygen", "-role", "stable", "-out", sp, "-pub", pp2}} {
		if err := run(a, nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	cf, sg := filepath.Join(dir, "rc-control.json"), filepath.Join(dir, "rc-control.json.sig")
	if err := run([]string{"control", "-channel", "rc", "-pause", "-block", "0.1.0-rc.40, 0.1.0-rc.41", "-note", "looking at a problem", "-out", cf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-control", "-key", sp, "-in", cf, "-out", sg}, nil, &out); err == nil {
		t.Fatal("stable key signed an rc control file")
	}
	if err := run([]string{"sign", "-control", "-key", kp, "-in", cf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-control", "-in", cf, "-sig", sg, "-pub", pp + "," + pp2, "-channel", "rc", "-want", "rc"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	// A control file is not accepted as a manifest, and a manifest-signing path refuses it.
	if err := run([]string{"verify", "-in", cf, "-sig", sg, "-pub", pp, "-channel", "rc", "-want", "rc"}, nil, &out); err == nil {
		t.Fatal("control file verified as a manifest")
	}
	if err := run([]string{"sign", "-key", kp, "-in", cf, "-out", sg}, nil, &out); err == nil {
		t.Fatal("control file signed as a manifest")
	}
	// Bad input is refused before anything is written.
	bad := filepath.Join(dir, "bad.json")
	for _, a := range [][]string{
		{"control", "-channel", "rc", "-percent", "20", "-out", bad},
		{"control", "-channel", "rc", "-rollout", "0.1.0-rc.50", "-percent", "0", "-out", bad},
		{"control", "-channel", "beta", "-out", bad},
		{"control", "-channel", "rc", "-days", "200", "-out", bad},
	} {
		if err := run(a, nil, &out); err == nil {
			t.Fatalf("accepted %v", a)
		}
	}
	if _, err := os.Stat(bad); err == nil {
		t.Fatal("wrote a file for bad input")
	}
}

func TestRefreshKeepsTheReleaseAndMovesTheDates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UNRAVIS_KEY_PASSPHRASE", "correct horse battery")
	var out bytes.Buffer
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	if err := run([]string{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, nil, &out); err != nil {
		t.Fatal(err)
	}
	writeAssets(t, dir, "0.1.0-rc.48", false)
	mf, sg := filepath.Join(dir, "m.json"), filepath.Join(dir, "m.json.sig")
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.48", "-seq", "4800", "-commit", commitA, "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-key", kp, "-in", mf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(dir, "keys.txt")
	k, err := readPub(pp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keys, []byte("rc "+hexOf(k.Pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nf, ns := filepath.Join(dir, "new.json"), filepath.Join(dir, "new.json.sig")
	if err := run([]string{"refresh", "-in", mf, "-sig", sg, "-keys", keys, "-want", "rc", "-version", "0.1.0-rc.48", "-commit", commitA, "-days", "90", "-out", nf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-key", kp, "-in", nf, "-out", ns}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-in", nf, "-sig", ns, "-keys", keys, "-channel", "rc", "-want", "rc"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	ob, _ := os.ReadFile(mf)
	nb, _ := os.ReadFile(nf)
	if !strings.Contains(string(nb), `"seq": 4801`) || !strings.Contains(string(nb), `"commit": "`+commitA+`"`) || !strings.Contains(string(nb), "rc.48") || string(ob) == string(nb) {
		t.Fatalf("refresh changed the wrong things:\n%s", nb)
	}
	// Wrong channel, version or commit: refused before anything is written.
	for _, bad := range [][]string{{"-want", "stable", "-version", "0.1.0-rc.48", "-commit", commitA}, {"-want", "rc", "-version", "0.1.0-rc.49", "-commit", commitA}, {"-want", "rc", "-version", "0.1.0-rc.48", "-commit", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, {"-want", "rc"}} {
		a := append([]string{"refresh", "-in", mf, "-sig", sg, "-keys", keys, "-out", nf + "4"}, bad...)
		if err := run(a, nil, &out); err == nil {
			t.Fatalf("refreshed with %v", bad)
		}
	}
	// A tampered or unsigned input is refused; no keys given is refused.
	os.WriteFile(mf, bytes.Replace(ob, []byte(`"size": 1`), []byte(`"size": 2`), 1), 0o644)
	if err := run([]string{"refresh", "-in", mf, "-sig", sg, "-keys", keys, "-want", "rc", "-version", "0.1.0-rc.48", "-commit", commitA, "-out", nf + "2"}, nil, &out); err == nil {
		t.Fatal("refreshed a tampered manifest")
	}
	if err := run([]string{"refresh", "-in", mf, "-sig", sg, "-want", "rc", "-version", "0.1.0-rc.48", "-commit", commitA, "-out", nf + "3"}, nil, &out); err == nil {
		t.Fatal("refreshed without keys")
	}
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }

func TestKeycheck(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	if err := run([]string{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, strings.NewReader("correct horse battery\n"), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"keycheck", "-key", kp, "-role", "rc"}, strings.NewReader("correct horse battery\n"), &out); err != nil {
		t.Fatal(err)
	}
	k, err := readPub(pp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), hexOf(k.Pub)) || strings.Contains(out.String(), "horse") {
		t.Fatalf("keycheck output: %q", out.String())
	}
	if err := run([]string{"keycheck", "-key", kp}, strings.NewReader("wrong passphrase!!\n"), &out); err == nil {
		t.Fatal("a wrong passphrase was accepted")
	}
	if err := run([]string{"keycheck", "-key", kp, "-role", "stable"}, strings.NewReader("correct horse battery\n"), &out); err == nil {
		t.Fatal("an rc key passed as stable")
	}
}

func TestFeedsig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UNRAVIS_KEY_PASSPHRASE", "correct horse battery")
	var out bytes.Buffer
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	if err := run([]string{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, nil, &out); err != nil {
		t.Fatal(err)
	}
	writeAssets(t, dir, "0.1.0-rc.48", false)
	mf, sg := filepath.Join(dir, "m.json"), filepath.Join(dir, "m.json.sig")
	if err := run([]string{"manifest", "-dir", dir, "-version", "0.1.0-rc.48", "-seq", "4800", "-commit", commitA, "-out", mf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-key", kp, "-in", mf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	k, _ := readPub(pp)
	keys := filepath.Join(dir, "keys.txt")
	os.WriteFile(keys, []byte("rc "+hexOf(k.Pub)+"\n"), 0o644)
	out.Reset()
	if err := run([]string{"feedsig", "-in", mf, "-sig", sg, "-keys", keys, "-want", "rc"}, nil, &out); err != nil || strings.TrimSpace(out.String()) != "0.1.0-rc.48" {
		t.Fatalf("feedsig: %v %q", err, out.String())
	}
	if err := run([]string{"feedsig", "-in", mf, "-sig", sg, "-keys", keys, "-want", "stable"}, nil, &out); err == nil {
		t.Fatal("wrong channel accepted")
	}
	b, _ := os.ReadFile(mf)
	os.WriteFile(mf, bytes.Replace(b, []byte("rc.48"), []byte("rc.99"), 1), 0o644)
	if err := run([]string{"feedsig", "-in", mf, "-sig", sg, "-keys", keys, "-want", "rc"}, nil, &out); err == nil {
		t.Fatal("an edited feed file verified")
	}
}

func TestControlsig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UNRAVIS_KEY_PASSPHRASE", "correct horse battery")
	var out bytes.Buffer
	kp, pp := filepath.Join(dir, "k.json"), filepath.Join(dir, "k.pub")
	op, opp := filepath.Join(dir, "o.json"), filepath.Join(dir, "o.pub")
	for _, a := range [][]string{{"keygen", "-role", "rc", "-out", kp, "-pub", pp}, {"keygen", "-role", "rc", "-out", op, "-pub", opp}} {
		if err := run(a, nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	cf, sg := filepath.Join(dir, "c.json"), filepath.Join(dir, "c.json.sig")
	if err := run([]string{"control", "-channel", "rc", "-block", "0.1.0-rc.40", "-days", "10", "-out", cf}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", "-control", "-key", kp, "-in", cf, "-out", sg}, nil, &out); err != nil {
		t.Fatal(err)
	}
	k, _ := readPub(pp)
	o, _ := readPub(opp)
	keys := filepath.Join(dir, "keys.txt")
	os.WriteFile(keys, []byte("rc "+hexOf(o.Pub)+"\nrc "+hexOf(k.Pub)+"\n"), 0o644)
	other := filepath.Join(dir, "other.txt")
	os.WriteFile(other, []byte("rc "+hexOf(o.Pub)+"\n"), 0o644)
	out.Reset()
	if err := run([]string{"controlsig", "-in", cf, "-sig", sg, "-keys", keys, "-want", "rc"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	exp, err := time.Parse(time.RFC3339, strings.TrimSpace(out.String()))
	if err != nil || exp.Before(time.Now().Add(9*24*time.Hour)) {
		t.Fatalf("expiry %q %v", out.String(), err)
	}
	// Another real key of the same role does not verify it.
	if err := run([]string{"controlsig", "-in", cf, "-sig", sg, "-keys", other, "-want", "rc"}, nil, &out); err == nil {
		t.Fatal("verified with a key that did not sign it")
	}
	// A valid but different expiry (one year later) parses fine and must fail on the signature alone.
	b, _ := os.ReadFile(cf)
	c, err := update.ParseControl(b)
	if err != nil {
		t.Fatal(err)
	}
	oldExp, _ := json.Marshal(c.Expires)
	newExp, _ := json.Marshal(c.Expires.AddDate(1, 0, 0))
	edited := bytes.Replace(b, oldExp, newExp, 1)
	if bytes.Equal(edited, b) {
		t.Fatal("test could not edit the expiry")
	}
	if _, err := update.ParseControl(edited); err != nil {
		t.Fatalf("edited file should still parse: %v", err)
	}
	os.WriteFile(cf, edited, 0o644)
	if err := run([]string{"controlsig", "-in", cf, "-sig", sg, "-keys", keys, "-want", "rc"}, nil, &out); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("an edited expiry verified: %v", err)
	}
}
