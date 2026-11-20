package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultDownloadBase is where release files live. A file is at <base>/v<version>/<name>.
const DefaultDownloadBase = "https://github.com/abhinavjp/unravis/releases/download"

// downloadIdle and downloadTotal are variables only so tests can shorten them.
var (
	downloadIdle  = 60 * time.Second // no byte for this long ends the download
	downloadTotal = 45 * time.Minute // hard limit for one download
)

// setupNameOK says whether name can be the setup program: only a file this updater would itself have named, so a signed manifest
// can never make us start a .cmd, .msi or anything else.
func setupNameOK(name string) bool {
	return strings.HasPrefix(name, "unravis-setup-v") && strings.HasSuffix(name, ".exe")
}

// Downloader fetches one release file named in a VERIFIED manifest and checks it byte for byte against that manifest.
// The manifest must come from Fetcher.Check (Result.Manifest); this type never looks at an unsigned manifest.
type Downloader struct {
	Dir       string       // folder for downloads; created with owner-only access
	Base      string       // defaults to DefaultDownloadBase
	HTTP      *http.Client // nil builds one with no overall timeout (the idle and total limits apply instead)
	Current   string       // running version, sent as the User-Agent
	AllowHost func(host string) bool

	once sync.Once
	cl   *http.Client
}

// Downloaded is a file that matched the manifest.
type Downloaded struct {
	Path   string
	SHA256 string
	Size   int64
}

func (d *Downloader) allow(h string) bool {
	if d.AllowHost != nil {
		return d.AllowHost(h)
	}
	return DefaultAllowHost(h)
}

func (d *Downloader) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	d.once.Do(func() {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = http.ProxyFromEnvironment
		tr.ResponseHeaderTimeout = 30 * time.Second
		tr.TLSHandshakeTimeout = 10 * time.Second
		d.cl = &http.Client{Transport: tr}
	})
	return d.cl
}

// FileURL is the address of a manifest asset. Only the signed version and the validated file name are used.
func (d *Downloader) FileURL(m *Manifest, a Asset) (string, error) {
	base := d.Base
	if base == "" {
		base = DefaultDownloadBase
	}
	if !safeFileName(a.Name) {
		return "", errors.New("the file name in the release is not safe")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return "", errors.New("the release version is not valid")
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + "/v" + m.Version + "/" + a.Name)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !d.allow(u.Hostname()) {
		return "", errors.New("the download address is not an allowed https address")
	}
	return u.String(), nil
}

// Download fetches asset kind (for example AssetSetup) into Dir and returns it only if its size and SHA-256 equal the manifest.
// A file already in Dir with the right size and hash is reused. On any failure nothing is left behind under the final name.
// progress (optional) is called with bytes done and the total.
func (d *Downloader) Download(ctx context.Context, m *Manifest, kind string, progress func(done, total int64)) (Downloaded, error) {
	if m == nil {
		return Downloaded{}, errors.New("no release to download")
	}
	a, ok := m.Assets[kind]
	if !ok || a.Size <= 0 || a.Size > MaxAssetSize || !sha256RE.MatchString(a.SHA256) {
		return Downloaded{}, errors.New("the release has no such file")
	}
	if kind == AssetSetup && !setupNameOK(a.Name) {
		return Downloaded{}, errors.New("the setup file name in the release is not the expected form")
	}
	rawURL, err := d.FileURL(m, a)
	if err != nil {
		return Downloaded{}, err
	}
	if d.Dir == "" {
		return Downloaded{}, errors.New("no download folder")
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return Downloaded{}, errors.New("could not create the download folder")
	}
	final := filepath.Join(d.Dir, a.Name)
	if got, ok := verifyFile(final, a); ok {
		return got, nil
	}
	_ = os.Remove(final) // wrong size, wrong hash or a leftover: start clean

	ctx, cancel := context.WithTimeout(ctx, downloadTotal)
	defer cancel()
	idle := time.AfterFunc(downloadIdle, cancel)
	defer idle.Stop()

	cl := *d.client()
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" || !d.allow(req.URL.Hostname()) {
			return errors.New("redirect to a host that is not allowed")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Downloaded{}, errors.New("bad download request")
	}
	req.Header.Set("User-Agent", "Unravis/"+d.Current)
	resp, err := cl.Do(req)
	if err != nil {
		return Downloaded{}, errors.New("could not reach the download")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Downloaded{}, fmt.Errorf("the download answered %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != a.Size {
		return Downloaded{}, errors.New("the download is not the size the release says")
	}
	tmp, err := os.CreateTemp(d.Dir, ".part-*")
	if err != nil {
		return Downloaded{}, errors.New("could not write the download")
	}
	tmpName := tmp.Name()
	defer func() { tmp.Close(); os.Remove(tmpName) }() // after a successful rename the remove finds nothing
	h := sha256.New()
	var done int64
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			idle.Reset(downloadIdle)
			done += int64(n)
			if done > a.Size {
				return Downloaded{}, errors.New("the download is larger than the release says")
			}
			h.Write(buf[:n])
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				return Downloaded{}, errors.New("could not write the download")
			}
			if progress != nil {
				progress(done, a.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return Downloaded{}, errors.New("the download stopped")
			}
			return Downloaded{}, errors.New("the download was cut off")
		}
	}
	if done != a.Size {
		return Downloaded{}, errors.New("the download is shorter than the release says")
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return Downloaded{}, errors.New("the download does not match the signed release (hash differs); it was deleted")
	}
	if err := tmp.Sync(); err != nil {
		return Downloaded{}, errors.New("could not write the download")
	}
	if err := tmp.Close(); err != nil {
		return Downloaded{}, errors.New("could not write the download")
	}
	if err := os.Rename(tmpName, final); err != nil {
		return Downloaded{}, errors.New("could not finish the download")
	}
	// Read it back from disk: what is on disk is what will run.
	got, ok := verifyFile(final, a)
	if !ok {
		_ = os.Remove(final)
		return Downloaded{}, errors.New("the saved file does not match the signed release; it was deleted")
	}
	return got, nil
}

// verifyFile reports whether path is a regular file with exactly the size and SHA-256 of a.
func verifyFile(path string, a Asset) (Downloaded, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != a.Size {
		return Downloaded{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return Downloaded{}, false
	}
	defer f.Close()
	h := sha256.New()
	if n, err := io.Copy(h, f); err != nil || n != a.Size {
		return Downloaded{}, false
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return Downloaded{}, false
	}
	return Downloaded{Path: path, SHA256: a.SHA256, Size: a.Size}, true
}

// Prune deletes older downloads and leftover partial files in Dir, keeping only keep (a file name; may be empty).
// It touches only files this type creates: ".part-*" and "unravis-setup-v*.exe".
func (d *Downloader) Prune(keep string) {
	ents, err := os.ReadDir(d.Dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		n := e.Name()
		if n == keep || !e.Type().IsRegular() {
			continue
		}
		if strings.HasPrefix(n, ".part-") || (strings.HasPrefix(n, "unravis-setup-v") && strings.HasSuffix(n, ".exe")) {
			_ = os.Remove(filepath.Join(d.Dir, n))
		}
	}
}

// Recheck reads the saved file again and reports whether it still matches the manifest. Call it right before handing the file on.
func (d *Downloader) Recheck(m *Manifest, kind string) (Downloaded, bool) {
	if m == nil {
		return Downloaded{}, false
	}
	a, ok := m.Assets[kind]
	if !ok || !safeFileName(a.Name) || d.Dir == "" || (kind == AssetSetup && !setupNameOK(a.Name)) {
		return Downloaded{}, false
	}
	return verifyFile(filepath.Join(d.Dir, a.Name), a)
}
