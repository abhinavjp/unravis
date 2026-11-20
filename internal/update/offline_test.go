package update

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// offlineFolder writes a signed release (setup file only present) into a folder.
func offlineFolder(t *testing.T, mut func(m *Manifest, payload *[]byte)) (string, Key, Manifest) {
	t.Helper()
	k, priv := key(t, ChannelRC)
	m := goodManifest("0.1.0-rc.49", ChannelRC)
	payload := []byte(strings.Repeat("setup-bytes", 100))
	if mut != nil {
		mut(&m, &payload)
	}
	sum := sha256.Sum256(payload)
	a := m.Assets["setup"]
	a.SHA256, a.Size = hex.EncodeToString(sum[:]), int64(len(payload))
	m.Assets["setup"] = a
	mb, sb := sign(t, m, k, priv)
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, OfflineManifestName), mb, 0o644))
	must(os.WriteFile(filepath.Join(dir, OfflineSignatureName), sb, 0o644))
	must(os.WriteFile(filepath.Join(dir, a.Name), payload, 0o644))
	return dir, k, m
}

func TestVerifyFolderGood(t *testing.T) {
	dir, k, m := offlineFolder(t, nil)
	r, err := VerifyFolder(dir, Trust{Keys: []Key{k}, Channel: ChannelRC, Want: ChannelRC}, "0.1.0-rc.48", now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Decision != DecisionInstall || r.Manifest.Version != m.Version {
		t.Fatalf("decision %v", r.Decision)
	}
	found, missing := 0, 0
	for _, f := range r.Files {
		if f.Found {
			found++
		} else {
			missing++
		}
	}
	if found != 1 || missing != 2 {
		t.Fatalf("found %d missing %d", found, missing)
	}
}

func TestVerifyFolderRefusals(t *testing.T) {
	trust := func(k Key) Trust { return Trust{Keys: []Key{k}, Channel: ChannelRC, Want: ChannelRC} }
	t.Run("changed file", func(t *testing.T) {
		dir, k, m := offlineFolder(t, nil)
		p := filepath.Join(dir, m.Assets["setup"].Name)
		b, _ := os.ReadFile(p)
		b[0] ^= 1
		os.WriteFile(p, b, 0o644)
		if _, err := VerifyFolder(dir, trust(k), "0.1.0-rc.48", now); err == nil || !strings.Contains(err.Error(), "do not use") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("changed manifest", func(t *testing.T) {
		dir, k, _ := offlineFolder(t, nil)
		p := filepath.Join(dir, OfflineManifestName)
		b, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(strings.Replace(string(b), "rc.49", "rc.50", 1)), 0o644)
		if _, err := VerifyFolder(dir, trust(k), "0.1.0-rc.48", now); err == nil {
			t.Fatal("a changed manifest must fail")
		}
	})
	t.Run("other key", func(t *testing.T) {
		dir, _, _ := offlineFolder(t, nil)
		other, _ := key(t, ChannelRC)
		if _, err := VerifyFolder(dir, trust(other), "0.1.0-rc.48", now); err == nil {
			t.Fatal("an untrusted key must fail")
		}
	})
	t.Run("no signature", func(t *testing.T) {
		dir, k, _ := offlineFolder(t, nil)
		os.Remove(filepath.Join(dir, OfflineSignatureName))
		if _, err := VerifyFolder(dir, trust(k), "0.1.0-rc.48", now); err == nil {
			t.Fatal("a missing signature must fail")
		}
	})
	t.Run("not newer", func(t *testing.T) {
		dir, k, _ := offlineFolder(t, nil)
		r, err := VerifyFolder(dir, trust(k), "0.1.0-rc.49", now)
		if err != nil || r.Decision != DecisionIgnore {
			t.Fatalf("same version: %v %v", r.Decision, err)
		}
	})
	t.Run("link as file", func(t *testing.T) {
		dir, k, m := offlineFolder(t, nil)
		p := filepath.Join(dir, m.Assets["setup"].Name)
		os.Rename(p, filepath.Join(dir, "real.bin"))
		if err := os.Symlink(filepath.Join(dir, "real.bin"), p); err != nil {
			t.Skip("no symlinks here")
		}
		if _, err := VerifyFolder(dir, trust(k), "0.1.0-rc.48", now); err == nil {
			t.Fatal("a link in place of the file must fail")
		}
	})
}
