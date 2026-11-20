package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
)

// State is what a machine remembers between checks. It lives in a small JSON file the program owns.
type State struct {
	SeenSeq map[string]uint64 `json:"seenSeq"` // per channel: highest verified seq so far; an older manifest is a rollback
}

// Status is the result of one check.
type Status string

const (
	StatusLatest      Status = "latest"      // nothing newer
	StatusAvailable   Status = "available"   // a newer, verified release exists
	StatusUnreachable Status = "unreachable" // network, status code or size problem; try again later
	StatusRejected    Status = "rejected"    // reached the feed, but it failed a trust check (signature, rollback, channel...)
	StatusDev         Status = "dev"         // development build: never calls out
)

// Result is what the caller shows or acts on. Manifest is set only for StatusAvailable and StatusLatest after a verified read.
type Result struct {
	Status   Status
	Manifest *Manifest
	Detail   string        // plain words for the log/UI; never contains the response body
	Backoff  bool          // true when the caller should slow down (rate limit, repeated errors)
	RetryIn  time.Duration // a server-requested wait, 0 if none
}

// Fetcher checks the feed. Zero values are filled with safe defaults by Check.
type Fetcher struct {
	Base      string       // feed base URL, https only
	HTTP      *http.Client // nil builds a client with the redirect rules below
	Keys      []Key
	Build     string // channel of the running build: stable or rc
	Current   string // running version
	Want      string // channel to read; defaults to Build
	StatePath string // state file; empty keeps nothing between runs
	// AllowHost decides which hosts a request or redirect may go to. nil uses the GitHub download hosts.
	AllowHost func(host string) bool
	Now       func() time.Time

	mu    sync.Mutex
	cache map[string]cached // channel -> last verified manifest, sig and the ETag of that manifest (memory only)
}

type cached struct {
	manifest, sig []byte
	etag          string
}

// DefaultAllowHost lets the feed and the GitHub download hosts it redirects to, and nothing else.
func DefaultAllowHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || host == "objects.githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
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
	return &http.Client{Timeout: fetchTimeout, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: fetchTimeout}}
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
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	st := f.loadState()

	base := f.Base
	if base == "" {
		base = DefaultFeedBase
	}
	f.mu.Lock()
	c, haveCache := f.cache[want]
	f.mu.Unlock()
	sentETag := ""
	if haveCache {
		sentETag = c.etag
	}
	manifest, newETag, notModified, res := f.get(ctx, base+"/"+want+".json", sentETag)
	if res != nil {
		return *res
	}
	var sig []byte
	if notModified {
		manifest, sig, newETag = c.manifest, c.sig, c.etag // the cached copy was verified when it was stored; it is verified again below
	} else {
		var r *Result
		sig, _, _, r = f.get(ctx, base+"/"+want+".json.sig", "")
		if r != nil {
			return *r
		}
	}
	m, err := Verify(manifest, sig, Trust{Keys: f.Keys, Channel: f.Build, Want: want}, now())
	if err != nil {
		return Result{Status: StatusRejected, Detail: err.Error()}
	}
	if m.Seq < st.SeenSeq[want] {
		return Result{Status: StatusRejected, Detail: "the feed offers an older release than one already seen (rollback refused)"}
	}
	d, why, err := Judge(m, f.Current, st.SeenSeq[want])
	if err != nil {
		return Result{Status: StatusRejected, Detail: err.Error()}
	}
	// Remember what was verified: freshness for next time, and the cached copy for a 304.
	st.SeenSeq[want] = m.Seq
	f.mu.Lock()
	if f.cache == nil {
		f.cache = map[string]cached{}
	}
	f.cache[want] = cached{manifest, sig, newETag} // stored only now: the ETag is never trusted before the pair has verified
	f.mu.Unlock()
	f.saveState(st)
	if d == DecisionInstall {
		return Result{Status: StatusAvailable, Manifest: m, Detail: "version " + m.Version + " is available"}
	}
	return Result{Status: StatusLatest, Manifest: m, Detail: why}
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
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "could not reach the update feed", Backoff: true}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && etag != "":
		return nil, "", true, nil
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the feed asked us to slow down", Backoff: true, RetryIn: retryAfter(resp)}
	default:
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: fmt.Sprintf("the feed answered %d", resp.StatusCode), Backoff: true}
	}
	if resp.ContentLength > MaxManifestSize {
		return nil, "", false, &Result{Status: StatusRejected, Detail: "the feed file is too large"}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxManifestSize+1))
	if err != nil {
		return nil, "", false, &Result{Status: StatusUnreachable, Detail: "the feed read was cut off", Backoff: true}
	}
	if len(b) > MaxManifestSize {
		return nil, "", false, &Result{Status: StatusRejected, Detail: "the feed file is too large"}
	}
	return b, resp.Header.Get("ETag"), false, nil
}

func retryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		var secs int
		if _, err := fmt.Sscanf(v, "%d", &secs); err == nil && secs > 0 && secs <= int(maxBackoff.Seconds()) {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

func (f *Fetcher) loadState() State {
	st := State{SeenSeq: map[string]uint64{}}
	if f.StatePath == "" {
		return st
	}
	b, err := os.ReadFile(f.StatePath)
	if err != nil || len(b) > 64*1024 {
		return st
	}
	var s State
	if json.Unmarshal(b, &s) == nil {
		if s.SeenSeq != nil {
			st.SeenSeq = s.SeenSeq
		}
	}
	return st
}

func (f *Fetcher) saveState(st State) {
	if f.StatePath == "" {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f.StatePath), 0o700); err != nil {
		return
	}
	tmp := f.StatePath + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, f.StatePath)
	}
}

// NextDelay is how long to wait before the next check: the interval with up to 10% jitter, doubled for each failure in a row
// up to six hours. A wait the server asked for wins when it is longer. The interval is never below five minutes.
func NextDelay(interval time.Duration, failures int, retryIn time.Duration, rnd *rand.Rand) time.Duration {
	if interval < minInterval {
		interval = minInterval
	}
	d := interval
	for i := 0; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	if retryIn > d {
		d = retryIn
	}
	if rnd != nil {
		d += time.Duration(rnd.Int63n(int64(d)/10 + 1))
	}
	return d
}
