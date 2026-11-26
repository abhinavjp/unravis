package blackbox

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// B8 (bundle part): one zip for one question, "what happened on this server in the last N hours". It holds the hourly
// recordings of the window, the settings (written again from the checked settings, so unknown keys are dropped; they hold no password, those live in sealed files elsewhere),
// the newest server facts, the recorder's own manifest and a list of every file with its SHA-256. A second copy of that
// list, with the SHA-256 of the zip itself, is written NEXT to the zip, so a zip that was cut or changed in transit is
// told apart from a good one.
//
// Not in this slice, stated plainly: scrubbed tails of the SQL ERRORLOG and the Redis log, the SQL service's own files,
// and the automatic bundle on a trigger (crash dump, restart, low disk). Those are separate steps (BL-697).

// bundleMaxBytes is the unpacked size of one bundle (a variable so a test can lower it).
var bundleMaxBytes int64 = 500 << 20

const (
	bundleMinSince = time.Minute
	bundleMaxSince = 7 * 24 * time.Hour
	bundleDefault  = 2 * time.Hour
)

var bundleOutRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// BundleOptions are the choices of one bundle. Zero values are the defaults.
type BundleOptions struct {
	Since time.Duration // how far back; 0 means two hours
	Out   string        // file name without folder and without .zip; "" means <name>-bundle-<UTC time>
	Disk  DiskSpace
}

// BundleEntry is one file of the bundle.
type BundleEntry struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// BundleManifest lists the bundle. The copy inside the zip cannot hold the zip's own hash; the one beside it does.
type BundleManifest struct {
	Version   int           `json:"version"`
	Name      string        `json:"name"` // the Blackbox name
	Created   time.Time     `json:"created"`
	From      time.Time     `json:"from"`
	To        time.Time     `json:"to"`
	Files     []BundleEntry `json:"files"`
	Notes     []string      `json:"notes,omitempty"`
	ZipSHA256 string        `json:"zipSha256,omitempty"`
	ZipBytes  int64         `json:"zipBytes,omitempty"`
}

// BundleResult is a finished bundle.
type BundleResult struct {
	Path         string
	ManifestPath string
	Files        int
	Bytes        int64 // unpacked
	ZipBytes     int64
	SHA256       string // of the zip
	From, To     time.Time
	Notes        []string
}

// ParseBundleSince reads "2h", "90m", "1d" style windows. Whole days are accepted for convenience.
func ParseBundleSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return bundleDefault, nil
	}
	var d time.Duration
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n < 1 || n > 7 { // checked before the multiplication, which would overflow for huge counts
			return 0, errors.New("--since must look like 90m, 2h or 1d")
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, errors.New("--since must look like 90m, 2h or 1d")
		}
	}
	if d < bundleMinSince || d > bundleMaxSince {
		return 0, errors.New("--since must be between 1 minute and 7 days")
	}
	return d, nil
}

func bundleErr(reason SnapshotReason, msg string, cause error) error {
	return &SnapshotError{Reason: reason, Message: msg, Cause: cause}
}

// lastFacts finds the newest "facts" block in the recording files (newest file first, last line first).
func lastFacts(dir string, names []string) (json.RawMessage, bool) {
	for i := len(names) - 1; i >= 0; i-- {
		f, err := os.Open(filepath.Join(dir, names[i])) //nolint:gosec // a recording file chosen by name rules
		if err != nil {
			continue
		}
		var found json.RawMessage
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if !bytes.Contains(line, []byte(`"facts":`)) {
				continue
			}
			var s struct {
				Facts json.RawMessage `json:"facts"`
			}
			if json.Unmarshal(line, &s) == nil && len(s.Facts) > 2 {
				found = append(json.RawMessage(nil), s.Facts...)
			}
		}
		scanErr := sc.Err()
		_ = f.Close()
		if found != nil {
			return found, true
		}
		if scanErr != nil {
			return nil, false // a line too long to read: say nothing rather than hand back an older block as the newest
		}
	}
	return nil, false
}

// MakeBundle writes <folder>\snapshots\<out>.zip and <out>.manifest.json. Nothing is left behind on failure.
func MakeBundle(cfg Config, now time.Time, o BundleOptions) (res BundleResult, err error) {
	now = now.UTC()
	if o.Since == 0 {
		o.Since = bundleDefault
	}
	if o.Since < bundleMinSince || o.Since > bundleMaxSince {
		return res, bundleErr(SnapOther, "--since must be between 1 minute and 7 days.", nil)
	}
	if o.Disk == nil {
		o.Disk = OSDisk{}
	}
	out := o.Out
	if out == "" {
		stamp := "-bundle-" + now.Format("2006-01-02-1504")
		out = CutText(cfg.Name, 63-len(stamp)) + stamp
	}
	if !bundleOutRe.MatchString(out) || strings.HasSuffix(strings.ToLower(out), ".zip") || strings.Contains(out, "..") || reservedDeviceBase(out) {
		return res, bundleErr(SnapOther, "--out must be a plain name (letters, digits, dot, dash, underscore), without a folder and without .zip.", nil)
	}
	from := now.Add(-o.Since)
	ents, rerr := os.ReadDir(cfg.Folder)
	if rerr != nil {
		return res, bundleErr(SnapOther, "The recordings folder cannot be read.", rerr)
	}
	var names []string
	var total int64
	sizes := map[string]int64{}
	for _, e := range ents {
		h, ok := HourOfName(e.Name())
		if !ok || e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		if !h.Add(time.Hour).After(from) || h.After(now) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		names = append(names, e.Name())
		sizes[e.Name()] = info.Size()
		total += info.Size()
	}
	sort.Strings(names)
	if len(names) == 0 {
		return res, bundleErr(SnapOther, "There are no recordings in that window.", nil)
	}
	if total > bundleMaxBytes {
		return res, bundleErr(SnapOther, fmt.Sprintf("That window holds %s of recordings, more than one bundle may hold (%s). Use a shorter --since.", mb(uint64(total)), mb(uint64(bundleMaxBytes))), nil)
	}
	if info, derr := o.Disk.Free(cfg.Folder); derr == nil && uint64(total) > info.Free {
		return res, bundleErr(SnapDriveFull, fmt.Sprintf("The drive is full: %s needed, %s free. Free space, then bundle again.", mb(uint64(total)), mb(info.Free)), nil)
	}

	base := filepath.Join(cfg.Folder, SnapshotsDir)
	if fi, lerr := os.Lstat(base); lerr == nil && fi.Mode().Type() != os.ModeDir {
		return res, bundleErr(SnapOther, "The snapshots folder is a link, not a folder. Nothing was written.", nil)
	}
	if merr := os.MkdirAll(base, 0o755); merr != nil {
		return res, classifySnapErr(merr, total)
	}
	if fi, lerr := os.Lstat(base); lerr != nil || fi.Mode().Type() != os.ModeDir {
		return res, bundleErr(SnapOther, "The snapshots folder is a link, not a folder. Nothing was written.", nil)
	}
	// Claim both names at once, exclusively: a second bundle, a leftover file or a planted link is never overwritten or
	// followed. Only the files this run made are removed on failure.
	var zf, mf *os.File
	var zipPath, manPath string
	for i := 1; ; i++ {
		final := out
		if i > 1 {
			final = fmt.Sprintf("%s-%d", out, i)
		}
		zipPath, manPath = filepath.Join(base, final+".zip"), filepath.Join(base, final+".manifest.json")
		z, zerr := os.OpenFile(zipPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // inside the recordings folder
		if zerr != nil {
			if errors.Is(zerr, os.ErrExist) && i < 1000 {
				continue
			}
			return res, classifySnapErr(zerr, total)
		}
		m, merr := os.OpenFile(manPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // inside the recordings folder
		if merr != nil {
			_ = z.Close()
			_ = os.Remove(zipPath)
			if errors.Is(merr, os.ErrExist) && i < 1000 {
				continue
			}
			return res, classifySnapErr(merr, total)
		}
		zf, mf = z, m
		break
	}
	done := false
	defer func() {
		if !done {
			_ = zf.Close()
			_ = mf.Close()
			_ = os.Remove(zipPath)
			_ = os.Remove(manPath)
		}
	}()
	zw := zip.NewWriter(zf)
	// The files are whole hours, so the real coverage starts at the first included hour, up to 59 minutes before the
	// requested start.
	firstHour, _ := HourOfName(names[0])
	man := BundleManifest{Version: 1, Name: cfg.Name, Created: now.Truncate(time.Second), From: firstHour, To: now.Truncate(time.Second)}
	add := func(name string, src io.Reader) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(src, bundleMaxBytes-res.Bytes+1))
		if err != nil {
			return err
		}
		if res.Bytes+n > bundleMaxBytes {
			return errBundleTooBig // a file grew while it was copied
		}
		man.Files = append(man.Files, BundleEntry{Name: name, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		res.Bytes += n
		return nil
	}
	fail := func(err error) (BundleResult, error) {
		_ = zw.Close()
		if errors.Is(err, errBundleTooBig) {
			return res, bundleErr(SnapOther, "The recordings grew past the size one bundle may hold while it was written. Use a shorter --since.", nil)
		}
		return res, classifySnapErr(err, total)
	}
	for _, n := range names {
		f, err := os.Open(filepath.Join(cfg.Folder, n)) //nolint:gosec // a recording file chosen by name rules
		if err != nil {
			return fail(&openErr{err})
		}
		err = add("recordings/"+n, f)
		_ = f.Close()
		if err != nil {
			return fail(err)
		}
	}
	if b, merr := cfg.Marshal(); merr == nil {
		if err := add("blackbox.json", strings.NewReader(string(b))); err != nil {
			return fail(err)
		}
	}
	if raw, rerr := os.ReadFile(filepath.Join(cfg.Folder, ManifestName)); rerr == nil && len(raw) < 4<<20 { //nolint:gosec // fixed name
		if err := add("recorder-manifest.json", strings.NewReader(string(raw))); err != nil {
			return fail(err)
		}
	} else {
		man.Notes = append(man.Notes, "the recorder's manifest.json was not readable")
	}
	if facts, ok := lastFacts(cfg.Folder, names); ok {
		if err := add("facts.json", strings.NewReader(string(facts))); err != nil {
			return fail(err)
		}
	} else {
		man.Notes = append(man.Notes, "no server facts in this window (they are written once an hour)")
	}
	man.Notes = append(man.Notes, "recordings are whole hours: they start at 'from', which can be earlier than the requested start; bundle-manifest.json lists every other file but not itself")
	man.Notes = append(man.Notes, "not included: ERRORLOG and Redis log tails, SQL service files")
	mb2, _ := json.MarshalIndent(man, "", "  ")
	if err := add("bundle-manifest.json", strings.NewReader(string(mb2))); err != nil {
		return fail(err)
	}
	if err := zw.Close(); err != nil {
		return res, classifySnapErr(err, total)
	}
	if err := zf.Sync(); err != nil {
		return res, classifySnapErr(err, total)
	}
	if err := zf.Close(); err != nil {
		return res, classifySnapErr(err, total)
	}
	sum, size, herr := fileSHA256Size(zipPath)
	if herr != nil {
		return res, classifySnapErr(herr, total)
	}
	man.ZipSHA256, man.ZipBytes = sum, size
	outer, _ := json.MarshalIndent(man, "", "  ")
	if _, err := mf.Write(append(outer, '\n')); err != nil {
		return res, classifySnapErr(err, total)
	}
	if err := mf.Sync(); err != nil {
		return res, classifySnapErr(err, total)
	}
	if err := mf.Close(); err != nil {
		return res, classifySnapErr(err, total)
	}
	done = true
	res.Path, res.ManifestPath, res.Files = zipPath, manPath, len(man.Files)
	res.ZipBytes, res.SHA256, res.From, res.To, res.Notes = size, sum, man.From, man.To, man.Notes
	return res, nil
}

var errBundleTooBig = errors.New("bundle too big")

func fileSHA256Size(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // the zip just written
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// reservedDeviceBase says whether name (the part before the first dot) is a Windows device name. A file called NUL.zip
// is the device, not a file.
func reservedDeviceBase(name string) bool {
	base := strings.ToUpper(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}
