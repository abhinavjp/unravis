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
	"sync"
	"time"
)

// DefaultFeedBase is where the signed feed files live (docs/auto-updater/SPEC.md section 5). It is a plain release download,
// not the GitHub API, so the API's 60-requests-an-hour limit does not apply.
const DefaultFeedBase = "https://github.com/abhinavjp/unravis/releases/download/update-feed"

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
}

// Fetcher checks the feed. Zero values are filled with safe defaults.
type Fetcher struct {
	Base      string       // feed base URL, https only, no user info, query or fragment
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

	mu     sync.Mutex
	cache  map[string]cached // channel -> last verified manifest, sig and ETag (memory only)
	seen   map[string]uint64 // channel -> highest seq verified in this process
	hashes map[string]string
	once   sync.Once
	cl     *http.Client
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
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !f.allow(u.Hostname()) {
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
	if base == "" {
		base = DefaultFeedBase
	}
	if err := f.validBase(base); err != nil {
		return Result{Status: StatusRejected, Detail: err.Error()}
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}

	res := f.once1(ctx, base, want, now, true)
	if res.Status == StatusRejected && res.retryable {
		wait := f.RetryWait
		if wait == 0 {
			wait = 3 * time.Second
		}
		select {
		case <-ctx.Done():
			return Result{Status: StatusUnreachable, Detail: "check stopped"}
		case <-time.After(wait):
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
	c, haveCache := f.cache[want]
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

	stateMu.Lock()
	defer stateMu.Unlock()
	st, loadNote := f.loadState()
	f.mu.Lock()
	if f.seen != nil {
		if f.seen[want] > st.SeenSeq[want] {
			st.SeenSeq[want], st.SeenHash[want] = f.seen[want], f.hashes[want]
		}
	}
	f.mu.Unlock()
	seen := st.SeenSeq[want]
	if m.Seq < seen {
		return attempt{Result: Result{Status: StatusRejected, Detail: "the feed offers an older release than one already seen (rollback refused)" + loadNote}}
	}
	if m.Seq == seen && st.SeenHash[want] != "" && st.SeenHash[want] != hash {
		return attempt{Result: Result{Status: StatusRejected, Detail: "the feed offers different content under a seq already seen" + loadNote}}
	}
	d, why, err := Judge(m, f.Current, seen)
	if err != nil {
		return attempt{Result: Result{Status: StatusRejected, Detail: err.Error()}}
	}
	st.SeenSeq[want], st.SeenHash[want] = m.Seq, hash
	f.mu.Lock()
	if f.seen == nil {
		f.seen, f.hashes = map[string]uint64{}, map[string]string{}
	}
	f.seen[want], f.hashes[want] = m.Seq, hash
	if f.cache == nil {
		f.cache = map[string]cached{}
	}
	f.cache[want] = cached{manifest, sig, newETag} // stored only now: the ETag is never trusted before the pair verified
	f.mu.Unlock()
	note := loadNote
	if err := f.saveState(st); err != nil {
		note += " (could not save the update memory: " + err.Error() + ")"
	}
	if d == DecisionInstall {
		return attempt{Result: Result{Status: StatusAvailable, Manifest: m, Detail: "version " + m.Version + " is available" + note}}
	}
	return attempt{Result: Result{Status: StatusLatest, Manifest: m, Detail: why + note}}
}

func isDev(v string) bool { _, err := ParseVersion(v); return err != nil }

// get reads one feed file. It returns a Result only when the check must stop.
func (f *Fetcher) get(ctx context.Context, rawURL, etag string) (body []byte, newETag string, notModified bool, stop *Result) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || !f.allow(u.Hostname()) {
		return nil, "", false, &Result{Status: StatusRejected, Detail: "the feed address is not an allowed https address"}
	}
	cl := *f.client()
	cl.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" || !f.allow(req.URL.Hostname()) {
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
	if err != nil || secs <= 0 {
		return 0
	}
	if d := time.Duration(secs) * time.Second; secs < int(maxBackoff.Seconds()) {
		return d
	}
	return maxBackoff
}

// loadState reads the state file. A missing file is normal. An unreadable one is set aside as .corrupt and the note says so,
// so the problem is visible instead of silently starting from zero; the in-process memory still protects this run.
func (f *Fetcher) loadState() (State, string) {
	st := State{SeenSeq: map[string]uint64{}, SeenHash: map[string]string{}}
	if f.StatePath == "" {
		return st, ""
	}
	fi, err := os.Stat(f.StatePath)
	if err != nil {
		return st, ""
	}
	var s State
	b, rerr := []byte(nil), error(nil)
	if fi.Size() <= maxStateSize {
		b, rerr = os.ReadFile(f.StatePath)
	} else {
		rerr = errors.New("too large")
	}
	if rerr != nil || json.Unmarshal(b, &s) != nil {
		_ = os.Rename(f.StatePath, f.StatePath+".corrupt")
		return st, " (the saved update memory was unreadable and was set aside)"
	}
	if s.SeenSeq != nil {
		st.SeenSeq = s.SeenSeq
	}
	if s.SeenHash != nil {
		st.SeenHash = s.SeenHash
	}
	return st, ""
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
		d = maxBackoff
		if interval > maxBackoff {
			d = interval
		}
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
