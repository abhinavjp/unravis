package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultFeedBase is where the signed feed files live (docs/auto-updater/SPEC.md section 5). It is a plain release download,
// not the GitHub API, so the API's 60-requests-an-hour limit does not apply.
const DefaultFeedBase = "https://github.com/abhinavjp/unravis/releases/download/update-feed"

// DefaultMirrorBases are tried, in order, only when the main address cannot be reached (network error, rate limit, server
// error). They hold byte-for-byte copies of the same signed files (the mirror-feed workflow copies them). Trust is the
// signature, never the host, so a mirror can be stale or hostile and the worst it does is fail to help.
var DefaultMirrorBases = []string{"https://raw.githubusercontent.com/abhinavjp/unravis/feed-mirror"}

const (
	fetchTimeout   = 10 * time.Second
	maxRedirects   = 3
	maxBackoff     = 6 * time.Hour
	minInterval    = 5 * time.Minute
	DefaultCadence = 30 * time.Minute
	maxStateSize   = 64 * 1024
)

// stateMu serialises load-modify-save of every state file in this process, so two checks (for example an rc build reading
// both channels) cannot lose each other's update.
var stateMu sync.Mutex

// State is what a machine remembers between checks: per channel, the highest verified seq and the hash of the manifest seen
// at that seq. An older seq is a rollback; the same seq with a different body is refused.
type State struct {
	SeenSeq  map[string]uint64 `json:"seenSeq"`
	SeenHash map[string]string `json:"seenHash"`
	// Control is, per channel, the newest verified control file (see control.go). It is kept so a pause or a withdrawn
	// version still holds when the control file cannot be fetched for a while, until its own expiry.
	Control map[string]*Control `json:"control,omitempty"`
	// DrawFor and Draw are, per channel, the version a rollout draw was made for and the draw (0 to 99).
	DrawFor map[string]string `json:"drawFor,omitempty"`
	Draw    map[string]int    `json:"draw,omitempty"`
	// ControlSeq is the highest control seq accepted per channel. It is never lowered or dropped when a file expires, so a
	// genuinely signed old file cannot be replayed to bring back a pause the owner lifted.
	ControlSeq map[string]uint64 `json:"controlSeq,omitempty"`
	// ControlHash is, per channel, the SHA-256 (hex) of the exact bytes of the control file accepted at ControlSeq. It is kept
	// after that file expires, like ControlSeq, so the same seq with other bytes is refused.
	ControlHash map[string]string `json:"controlHash,omitempty"`
	// KeptBlocked is, per channel, the withdrawn list of the newest accepted control file. It does not end when that file
	// expires: only a newer accepted control file replaces it. Pause and rollout still end at expiry.
	KeptBlocked map[string][]string `json:"keptBlocked,omitempty"`
}

// Status is the result of one check.
type Status string

const (
	StatusLatest      Status = "latest"      // nothing newer
	StatusAvailable   Status = "available"   // a newer, verified release exists
	StatusUnreachable Status = "unreachable" // network, status code or size problem; try again later
	StatusRejected    Status = "rejected"    // reached the feed, but what it returned failed a trust check
	StatusDev         Status = "dev"         // development build: never calls out
)

// Result is what the caller shows or acts on.
type Result struct {
	Status   Status
	Manifest *Manifest
	Detail   string        // plain words for the log/UI; never contains a response body
	Backoff  bool          // true when the caller should slow down
	RetryIn  time.Duration // a server-requested wait, 0 if none
	Held     bool          // a newer release exists but the signed control file holds it back (Detail says why)
}

// Fetcher checks the feed. Zero values are filled with safe defaults.
type Fetcher struct {
	Base string // feed base URL, https only, no user info, query or fragment
	// Mirrors are other addresses of the same signed files, tried in order only when Base cannot be reached. When Base is left
	// empty (the real feed) and Mirrors is nil, DefaultMirrorBases is used.
	Mirrors []string
	// Folder, when set, reads the feed files from this folder (a local path or a share such as \\server\feed) instead of
	// from the internet, for servers that cannot reach it. The files are the same signed ones and go through every check;
	// nothing in the folder is trusted because of where it is. Base and Mirrors are then unused.
	Folder    string
	HTTP      *http.Client // nil builds one shared client with the redirect rules below
	Keys      []Key
	Build     string // channel of the running build: stable or rc
	Current   string // running version
	Want      string // channel to read; defaults to Build
	StatePath string // state file; empty keeps the memory in this process only
	// AllowHost decides which hosts a request or redirect may go to. nil uses the GitHub download hosts.
	AllowHost func(host string) bool
	Now       func() time.Time
	// RetryWait is the pause before the one retry when a fresh pair fails its check (the feed may have been mid-upload).
	// Zero uses three seconds.
	RetryWait time.Duration

	mu    sync.Mutex
	cache map[string]cached // channel -> last verified manifest, sig and ETag (memory only)
	local *seenMem          // used when StatePath is empty
	once  sync.Once
	cl    *http.Client
}

// seenMem is the in-process copy of what has been seen, so a lost or unreadable state file cannot reopen a rollback while the
// program is running. With a StatePath it is shared by every Fetcher in the process that uses the same file.
type seenMem struct {
	seq  map[string]uint64
	hash map[string]string
	// control seq floor and rollout draw, kept here too so an unsaved or lost state file cannot reopen a replay or redraw
	ctlSeq  map[string]uint64
	drawFor map[string]string
	draw    map[string]int
	// control hash and withdrawn list, kept here too for the same reason
	ctlHash map[string]string
	kept    map[string][]string
}

var (
	memByPath   = map[string]*seenMem{} // guarded by stateMu
	errReadMemo = errors.New("could not read the update memory")
)

func (f *Fetcher) memory() *seenMem { // call with stateMu held
	if f.StatePath == "" {
		if f.local == nil {
			f.local = &seenMem{seq: map[string]uint64{}, hash: map[string]string{}}
		}
		return f.local
	}
	m := memByPath[f.StatePath]
	if m == nil {
		m = &seenMem{seq: map[string]uint64{}, hash: map[string]string{}}
		memByPath[f.StatePath] = m
	}
	return m
}

type cached struct {
	manifest, sig []byte
	etag          string
}

// DefaultAllowHost lets the GitHub release-download hosts and nothing else.
func DefaultAllowHost(host string) bool {
	switch host {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	}
	return false
}

// mirrorPath is the only place on raw.githubusercontent.com a program reads from. The host serves every repository, so it is
// allowed by exact path and never as a host.
const mirrorHost, mirrorPath = "raw.githubusercontent.com", "/abhinavjp/unravis/feed-mirror"

// allowURL is allow for a whole address: the default hosts, plus the one backup path. A custom AllowHost (tests) decides alone.
func (f *Fetcher) allowURL(u *url.URL) bool {
	if f.AllowHost != nil {
		return f.AllowHost(u.Hostname())
	}
	if u.Hostname() == mirrorHost {
		// No dot segments and no escaped slashes or dots: the path must mean what it says.
		if u.EscapedPath() != u.Path {
			return false
		}
		for _, seg := range strings.Split(u.Path, "/") {
			if seg == "." || seg == ".." {
				return false
			}
		}
		return u.Path == mirrorPath || strings.HasPrefix(u.Path, mirrorPath+"/")
	}
	return DefaultAllowHost(u.Hostname())
}

func (f *Fetcher) allow(h string) bool {
	if f.AllowHost != nil {
		return f.AllowHost(h)
	}
	return DefaultAllowHost(h)
}

func (f *Fetcher) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	f.once.Do(func() {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = http.ProxyFromEnvironment
		tr.IdleConnTimeout = 30 * time.Second
		tr.TLSHandshakeTimeout = fetchTimeout
		f.cl = &http.Client{Timeout: fetchTimeout, Transport: tr}
	})
	return f.cl
}

func (f *Fetcher) validBase(base string) error {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !f.allowURL(u) {
		return errors.New("the feed address is not an allowed https address")
	}
	if p := u.Port(); p != "" && p != "443" && f.AllowHost == nil {
		return errors.New("the feed address uses an unexpected port")
	}
	return nil
}

// Check runs one check. It never installs anything. It reads two small files, verifies them, and says what it found.
func (f *Fetcher) Check(ctx context.Context) Result {
	if f.Current == "" || isDev(f.Current) {
		return Result{Status: StatusDev, Detail: "development build: no update check"}
	}
	if f.Build != ChannelStable && f.Build != ChannelRC {
		return Result{Status: StatusRejected, Detail: "unknown build channel"}
	}
	want := f.Want
	if want == "" {
		want = f.Build
	}
	if want != ChannelStable && want != ChannelRC {
		return Result{Status: StatusRejected, Detail: "unknown channel"}
	}
	if f.Build == ChannelStable && want != ChannelStable {
		return Result{Status: StatusRejected, Detail: "a stable build only follows the stable channel"}
	}
	base := f.Base
	mirrors := f.Mirrors
	if f.Folder != "" {
		now := time.Now
		if f.Now != nil {
			now = f.Now
		}
		return f.checkAt(ctx, "folder:"+f.Folder, want, now)
	}
	if base == "" {
		base = DefaultFeedBase
		if mirrors == nil {
			mirrors = DefaultMirrorBases
		}
	}
	if err := f.validBase(base); err != nil {
		return Result{Status: StatusRejected, Detail: err.Error()}
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	res := f.checkAt(ctx, base, want, now)
	if res.Status != StatusUnreachable || ctx.Err() != nil {
		return res
	}
	// Only "could not reach" moves on to a backup address. A rejected answer (bad signature, rollback) never does.
	for _, m := range mirrors {
		if f.validBase(m) != nil {
			continue
		}
		r2 := f.checkAt(ctx, m, want, now)
		if r2.Status == StatusAvailable || r2.Status == StatusLatest { // a stale or odd backup never replaces the honest "could not reach"
			if r2.Status == StatusAvailable {
				// The backup may be behind: it cannot prove that this release was not paused or withdrawn since. So it can say a
				// newer version exists, but it never starts an install. The main address (or a later check) decides.
				r2.Status, r2.Held = StatusLatest, true
				r2.Detail = "version " + r2.Manifest.Version + " exists, but the main update address could not be reached, so it is waiting until that can confirm it is safe to install"
			}
			r2.Backoff, r2.RetryIn = res.Backoff, res.RetryIn // the main address still asked us to slow down
			return r2
		}
		if ctx.Err() != nil {
			break
		}
	}
	return res
}

// checkAt runs one check against one address, with the one retry for a pair that failed its check mid-upload.
func (f *Fetcher) checkAt(ctx context.Context, base, want string, now func() time.Time) Result {
	res := f.once1(ctx, base, want, now, true)
	if res.Status == StatusRejected && res.retryable {
		wait := f.RetryWait
		if wait == 0 {
			wait = 3 * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Result{Status: StatusUnreachable, Detail: "check stopped"}
		case <-timer.C:
		}
		res = f.once1(ctx, base, want, now, false)
	}
	return res.Result
}

type attempt struct {
	Result
	retryable bool // a signature or parse failure on a fresh pair: the feed may have been mid-upload
}

func (f *Fetcher) once1(ctx context.Context, base, want string, now func() time.Time, useCache bool) attempt {
	f.mu.Lock()
	ck := base + "|" + want // the validators of one address mean nothing at another
	c, haveCache := f.cache[ck]
	f.mu.Unlock()
	sentETag := ""
	if haveCache && useCache {
		sentETag = c.etag
	}
	manifest, newETag, notModified, stop := f.get(ctx, base+"/"+want+".json", sentETag)
	if stop != nil {
		return attempt{Result: *stop}
	}
	var sig []byte
	fresh := true
	if notModified {
		manifest, sig, newETag, fresh = c.manifest, c.sig, c.etag, false // verified when stored; verified again below
	} else {
		var s *Result
		sig, _, _, s = f.get(ctx, base+"/"+want+".json.sig", "")
		if s != nil {
			return attempt{Result: *s}
		}
	}
	m, err := Verify(manifest, sig, Trust{Keys: f.Keys, Channel: f.Build, Want: want}, now())
	if err != nil {
		return attempt{Result: Result{Status: StatusRejected, Detail: err.Error()}, retryable: fresh}
	}
	sum := sha256.Sum256(manifest)
	hash := hex.EncodeToString(sum[:])
	var ctl *Control // only worth a request when this release would be an upgrade
	var ctlHash string
	if cur, e1 := ParseVersion(f.Current); e1 == nil {
		if next, e2 := ParseVersion(m.Version); e2 == nil && Compare(next, cur) > 0 {
			ctl, ctlHash = f.fetchControl(ctx, base, want, now())
		}
	}

	stateMu.Lock()
	defer stateMu.Unlock()
	st, loadNote, lerr := f.loadState()
	if lerr != nil { // a read problem is not "no memory": fail closed for this check
		return attempt{Result: Result{Status: StatusUnreachable, Detail: lerr.Error(), Backoff: true}}
	}
	mem := f.memory()
	dirty := loadNote != ""
	if ms := mem.seq[want]; ms > st.SeenSeq[want] || (ms == st.SeenSeq[want] && ms > 0 && st.SeenHash[want] == "") {
		st.SeenSeq[want], st.SeenHash[want] = ms, mem.hash[want]
		dirty = true // the file is behind this process's memory: put it right now, before any refusal below
	}
	note := loadNote
	if dirty {
		if err := f.saveState(st); err != nil {
			note += " (could not save the update memory: " + err.Error() + ")"
		}
	}
	loadNote = note
	seen := st.SeenSeq[want]
	if m.Seq < seen {
		return attempt{Result: Result{Status: StatusRejected, Detail: "the feed offers an older release than one already seen (rollback refused)" + loadNote}}
	}
	if m.Seq == seen && st.SeenHash[want] != "" && st.SeenHash[want] != hash {
		return attempt{Result: Result{Status: StatusRejected, Detail: "the feed offers different content under a seq already seen" + loadNote}}
	}
	d, why, err := Judge(m, f.Current, seen)
	if err != nil {
		return attempt{Result: Result{Status: StatusRejected, Detail: err.Error() + loadNote}}
	}
	st.SeenSeq[want], st.SeenHash[want] = m.Seq, hash
	mem.seq[want], mem.hash[want] = m.Seq, hash
	f.mu.Lock()
	if f.cache == nil {
		f.cache = map[string]cached{}
	}
	f.cache[ck] = cached{manifest, sig, newETag} // stored only now: the ETag is never trusted before the pair verified
	f.mu.Unlock()
	if err := f.saveState(st); err != nil && !strings.Contains(note, "could not save") {
		note += " (could not save the update memory: " + err.Error() + ")"
	}
	if d == DecisionInstall {
		hold, why := f.applyControl(&st, mem, ctl, ctlHash, want, m.Version, now(), f.Folder != "")
		_ = f.saveState(st) // the control file and the rollout draw are remembered either way
		if hold {
			return attempt{Result: Result{Status: StatusLatest, Manifest: m, Detail: why, Held: true}}
		}
		return attempt{Result: Result{Status: StatusAvailable, Manifest: m, Detail: "version " + m.Version + " is available" + note}}
	}
	return attempt{Result: Result{Status: StatusLatest, Manifest: m, Detail: why + note}}
}

// fetchControl reads and verifies the channel's control file. Any problem (missing, unreachable, bad signature) returns nil:
// the caller then uses the last one it accepted, if it has not expired. A control file can only hold an update back, so
// failing to read it never blocks a release by itself.
func (f *Fetcher) fetchControl(ctx context.Context, base, want string, now time.Time) (*Control, string) {
	body, _, _, stop := f.get(ctx, base+"/"+want+"-control.json", "")
	if stop != nil || body == nil {
		return nil, ""
	}
	sig, _, _, stop := f.get(ctx, base+"/"+want+"-control.json.sig", "")
	if stop != nil || sig == nil {
		return nil, ""
	}
	c, err := VerifyControl(body, sig, Trust{Keys: f.Keys, Channel: f.Build, Want: want}, now)
	if err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(body)
	return c, hex.EncodeToString(sum[:])
}

// applyControl merges (required: hold the release when there is no live control file at all) a freshly read control file into the state and says whether this machine must hold the release back.
// A file with a lower seq than the highest ever accepted is ignored (a replay), even when the file that set that floor has
// since expired. Call with stateMu held; the caller saves the state.
func (f *Fetcher) applyControl(st *State, mem *seenMem, fresh *Control, freshHash, want, version string, now time.Time, required bool) (bool, string) {
	if st.Control == nil {
		st.Control = map[string]*Control{}
	}
	if st.ControlSeq == nil {
		st.ControlSeq = map[string]uint64{}
	}
	if st.ControlHash == nil {
		st.ControlHash = map[string]string{}
	}
	if st.KeptBlocked == nil {
		st.KeptBlocked = map[string][]string{}
	}
	if mem.ctlSeq == nil {
		mem.ctlSeq, mem.drawFor, mem.draw = map[string]uint64{}, map[string]string{}, map[string]int{}
	}
	if mem.ctlHash == nil {
		mem.ctlHash, mem.kept = map[string]string{}, map[string][]string{}
	}
	cur := st.Control[want]
	if cur != nil && cur.Channel != want {
		cur = nil
	}
	floor := max(st.ControlSeq[want], mem.ctlSeq[want])
	if cur != nil {
		floor = max(floor, cur.Seq)
	}
	known := st.ControlHash[want]
	if known == "" {
		known = mem.ctlHash[want]
	}
	accept := false
	switch {
	case fresh == nil || fresh.Seq < floor:
		// nothing new, or a replay
	case fresh.Seq > floor:
		accept = true
	case known != "": // the same number: only the very same file counts
		accept = known == freshHash
	case cur != nil && cur.Seq == fresh.Seq: // a state written before hashes were kept: compare what the file says
		accept = sameControl(cur, fresh)
	default:
		accept = true // first sight of this number
	}
	if accept {
		cur, floor = fresh, fresh.Seq
		st.Control[want] = fresh
		st.ControlHash[want], mem.ctlHash[want] = freshHash, freshHash
	}
	st.ControlSeq[want], mem.ctlSeq[want] = floor, floor
	if ctlLive(cur, now) {
		// The withdrawn list of the newest live file is kept past its expiry; only a newer accepted file replaces it.
		if len(cur.Blocked) > 0 {
			st.KeptBlocked[want] = append([]string(nil), cur.Blocked...)
		} else {
			delete(st.KeptBlocked, want)
		}
		mem.kept[want] = st.KeptBlocked[want]
	} else {
		delete(st.Control, want)
		if required {
			// A folder or share is writable by more people than the feed is, so a missing control file there could be a deleted one.
			// Without a valid, unexpired control file the release is held until one is copied in (Set-Control.ps1, no switches).
			return true, "the update folder has no valid control file, so version " + version + " is on hold; copy a current " + want + "-control.json and .sig into the folder"
		}
		kept := st.KeptBlocked[want]
		if kept == nil {
			kept = mem.kept[want]
		} else {
			mem.kept[want] = kept
		}
		for _, b := range kept {
			if b == version {
				return true, "version " + version + " was withdrawn"
			}
		}
		return false, ""
	}
	return cur.Hold(version, func() int {
		if st.Draw == nil {
			st.Draw, st.DrawFor = map[string]int{}, map[string]string{}
		}
		if st.DrawFor[want] == version {
			d := st.Draw[want]
			mem.draw[want], mem.drawFor[want] = d, version
			return d
		}
		if mem.drawFor[want] != version {
			mem.draw[want], mem.drawFor[want] = newDraw(), version
		}
		st.Draw[want], st.DrawFor[want] = mem.draw[want], version
		return mem.draw[want]
	})
}

// sameControl reports whether two control files say the same thing (the bytes were proven equal to this form when they were read).
func sameControl(a, b *Control) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

func isDev(v string) bool { _, err := ParseVersion(v); return err != nil }

// get reads one feed file. It returns a Result only when the check must stop.
func (f *Fetcher) get(ctx context.Context, rawURL, etag string) (body []byte, newETag string, notModified bool, stop *Result) {
	if f.Folder != "" {
		return f.readFolderFile(rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !f.allowURL(u) {
		return nil, "", false, &Result{Status: StatusRejected, Detail: "the feed address is not an allowed https address"}
	}
	cl := *f.client()
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" || !f.allow(req.URL.Hostname()) { // a redirect may only go to the release hosts, never to the backup host
			return errors.New("redirect to a host that is not allowed")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "bad request"}
	}
	req.Header.Set("User-Agent", "Unravis/"+f.Current) // version only: no machine id, no user data
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := cl.Do(req)
	if err != nil {
		if ctx.Err() == context.Canceled { // the program is shutting down: not a failure to back off from
			return nil, "", false, &Result{Status: StatusUnreachable, Detail: "check stopped"}
		}
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "could not reach the update feed", Backoff: true}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && etag != "":
		return nil, "", true, nil
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable:
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the feed asked us to slow down", Backoff: true, RetryIn: retryAfter(resp)}
	default:
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: fmt.Sprintf("the feed answered %d", resp.StatusCode), Backoff: true}
	}
	if resp.ContentLength > MaxManifestSize {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the feed file is too large", Backoff: true}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxManifestSize+1))
	if err != nil {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the feed read was cut off", Backoff: true}
	}
	if len(b) > MaxManifestSize {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the feed file is too large", Backoff: true}
	}
	return b, resp.Header.Get("ETag"), false, nil
}

// retryAfter reads a Retry-After in seconds, capped at six hours. The date form is ignored (the normal back-off applies).
func retryAfter(resp *http.Response) time.Duration {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange && !strings.HasPrefix(resp.Header.Get("Retry-After"), "-") {
		return maxBackoff
	}
	if err != nil || secs <= 0 {
		return 0
	}
	if d := time.Duration(secs) * time.Second; secs < int(maxBackoff.Seconds()) {
		return d
	}
	return maxBackoff
}

// loadState reads the state file. A missing file is normal. A file that cannot be parsed is set aside under a unique name and
// the note says so, so the problem is visible. Any other read problem is an error: the check fails closed instead of
// starting from zero. The in-process memory protects this run either way.
func (f *Fetcher) loadState() (State, string, error) {
	st := State{SeenSeq: map[string]uint64{}, SeenHash: map[string]string{}}
	if f.StatePath == "" {
		return st, "", nil
	}
	fi, err := os.Stat(f.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return st, "", nil
	}
	if err != nil || !fi.Mode().IsRegular() {
		return st, "", errReadMemo
	}
	if fi.Size() > maxStateSize {
		return st, f.setAside(), nil
	}
	b, err := os.ReadFile(f.StatePath)
	if err != nil {
		return st, "", errReadMemo
	}
	var s State
	if json.Unmarshal(b, &s) != nil {
		return st, f.setAside(), nil
	}
	if s.SeenSeq != nil {
		st.SeenSeq = s.SeenSeq
	}
	if s.SeenHash != nil {
		st.SeenHash = s.SeenHash
	}
	st.Control, st.DrawFor, st.Draw, st.ControlSeq = s.Control, s.DrawFor, s.Draw, s.ControlSeq
	st.ControlHash, st.KeptBlocked = s.ControlHash, s.KeptBlocked
	return st, "", nil
}

func (f *Fetcher) setAside() string {
	if err := os.Rename(f.StatePath, f.StatePath+".corrupt-"+strconv.FormatInt(time.Now().UnixNano(), 10)); err != nil {
		_ = os.Remove(f.StatePath)
	}
	return " (the saved update memory was unreadable and was set aside)"
}

// saveState writes the file to a unique temp file in the same folder, flushes it to disk, then renames it into place.
func (f *Fetcher) saveState(st State) error {
	if f.StatePath == "" {
		return nil
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.StatePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".update-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.StatePath)
}

// NextDelay is how long to wait before the next check: the interval (never below five minutes) with up to 10% jitter,
// doubled for each failure in a row, but never more than six hours from backing off. A wait the server asked for (capped at
// six hours) wins when it is longer.
func NextDelay(interval time.Duration, failures int, retryIn time.Duration, rnd *rand.Rand) time.Duration {
	if interval < minInterval {
		interval = minInterval
	}
	d := interval
	for i := 0; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff && d > interval {
		d = max(maxBackoff, interval)
	}
	if retryIn > maxBackoff {
		retryIn = maxBackoff
	}
	if retryIn > d {
		d = retryIn
	}
	if rnd != nil {
		d += time.Duration(rnd.Int63n(int64(d)/10 + 1))
	}
	return d
}

// readFolderFile reads one feed file from Folder. rawURL is "folder:<path>/<name>"; only the file name is used. The file must
// be a regular file (not a link) no larger than a feed file. A missing file is "could not reach", like an offline server.
func (f *Fetcher) readFolderFile(rawURL string) ([]byte, string, bool, *Result) {
	name := rawURL[strings.LastIndex(rawURL, "/")+1:]
	if name == "" || strings.ContainsAny(name, `\:`) {
		return nil, "", false, &Result{Status: StatusRejected, Detail: "the feed file name is not allowed"}
	}
	path := filepath.Join(f.Folder, name)
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the update folder has no " + name, Backoff: true}
	}
	fh, err := os.Open(path)
	if err != nil {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the update folder could not be read", Backoff: true}
	}
	defer fh.Close()
	if fi, err := fh.Stat(); err != nil || !fi.Mode().IsRegular() { // the file that is open, not the name that may have been swapped
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the update folder has no " + name, Backoff: true}
	}
	b, err := io.ReadAll(io.LimitReader(fh, MaxManifestSize+1))
	if err != nil {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the update folder could not be read", Backoff: true}
	}
	if len(b) > MaxManifestSize {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the update folder has a feed file that is too large", Backoff: true}
	}
	return b, "", false, nil
}
