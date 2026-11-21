package update

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func writeFolderFeed(t *testing.T, dir, channel string, m Manifest, k Key, priv ed25519.PrivateKey) {
	t.Helper()
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, channel+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, channel+".json.sig"), []byte(FormatSignature(k.Pub, ed25519.Sign(priv, b))), 0o644); err != nil {
		t.Fatal(err)
	}
}

func folderFetcher(t *testing.T, dir string, k Key) *Fetcher {
	return &Fetcher{Folder: dir, Keys: []Key{k}, Build: ChannelRC, Current: "0.1.0-rc.47",
		StatePath: filepath.Join(t.TempDir(), "state.json"), Now: func() time.Time { return now }}
}

func TestFolderFeedIsVerifiedLikeTheInternetOne(t *testing.T) {
	k, priv := key(t, ChannelRC)
	dir := t.TempDir()
	f := folderFetcher(t, dir, k)
	if r := f.Check(t.Context()); r.Status != StatusUnreachable {
		t.Fatalf("empty folder: %+v", r)
	}
	writeFolderFeed(t, dir, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable || r.Manifest.Version != "0.1.0-rc.48" {
		t.Fatalf("%+v", r)
	}
	// A manifest signed by someone else is refused, wherever it sits.
	other, op := key(t, ChannelRC)
	writeFolderFeed(t, dir, "rc", goodManifest("0.1.0-rc.49", ChannelRC), other, op)
	if r := f.Check(t.Context()); r.Status != StatusRejected {
		t.Fatalf("forged folder feed: %+v", r)
	}
	// An older, validly signed manifest (rollback) is refused after a newer one was seen.
	old := goodManifest("0.1.0-rc.41", ChannelRC)
	old.Seq = 41
	writeFolderFeed(t, dir, "rc", old, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusRejected {
		t.Fatalf("rollback from a folder: %+v", r)
	}
	// A control file in the folder works too.
	writeFolderFeed(t, dir, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	c := goodControl(ChannelRC)
	c.Pause = true
	cb, _ := json.Marshal(c)
	os.WriteFile(filepath.Join(dir, "rc-control.json"), cb, 0o644)
	os.WriteFile(filepath.Join(dir, "rc-control.json.sig"), []byte(FormatSignature(k.Pub, ed25519.Sign(priv, cb))), 0o644)
	if r := f.Check(t.Context()); r.Status != StatusLatest || !r.Held {
		t.Fatalf("control in folder: %+v", r)
	}
}

func TestFolderFeedRefusesLinksAndBigFiles(t *testing.T) {
	k, priv := key(t, ChannelRC)
	dir := t.TempDir()
	writeFolderFeed(t, dir, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	big := filepath.Join(dir, "rc.json")
	os.WriteFile(big, make([]byte, MaxManifestSize+1), 0o644)
	if r := folderFetcher(t, dir, k).Check(t.Context()); r.Status != StatusUnreachable {
		t.Fatalf("oversize: %+v", r)
	}
	if runtime.GOOS != "windows" {
		writeFolderFeed(t, dir, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
		real := filepath.Join(t.TempDir(), "elsewhere.json")
		os.Rename(filepath.Join(dir, "rc.json"), real)
		if err := os.Symlink(real, filepath.Join(dir, "rc.json")); err == nil {
			if r := folderFetcher(t, dir, k).Check(t.Context()); r.Status != StatusUnreachable {
				t.Fatalf("symlink followed: %+v", r)
			}
		}
	}
}

func TestDownloadFromFolder(t *testing.T) {
	payload := []byte("the blackbox zip")
	m, a := dlManifest(payload)
	src, dst := t.TempDir(), t.TempDir()
	d := &Downloader{Dir: dst, Folder: src, Current: "0.1.0-rc.47"}
	if _, err := d.Download(t.Context(), m, AssetSetup, nil); err == nil {
		t.Fatal("downloaded a file that is not in the folder")
	}
	os.WriteFile(filepath.Join(src, a.Name), append([]byte("x"), payload[1:]...), 0o644) // right size, wrong bytes
	if _, err := d.Download(t.Context(), m, AssetSetup, nil); err == nil {
		t.Fatal("accepted a file with the wrong hash")
	}
	if _, err := os.Stat(filepath.Join(dst, a.Name)); err == nil {
		t.Fatal("left a bad file under the final name")
	}
	os.WriteFile(filepath.Join(src, a.Name), payload[:5], 0o644)
	if _, err := d.Download(t.Context(), m, AssetSetup, nil); err == nil {
		t.Fatal("accepted a short file")
	}
	os.WriteFile(filepath.Join(src, a.Name), payload, 0o644)
	got, err := d.Download(t.Context(), m, AssetSetup, nil)
	if err != nil || got.SHA256 != a.SHA256 {
		t.Fatalf("%v %+v", err, got)
	}
}
