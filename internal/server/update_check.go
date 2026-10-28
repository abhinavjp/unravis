package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"api-audit-explorer/internal/version"
)

// P12 "Check for updates" (buildout/plan/installer-and-releases.md section 10).
// The check runs only when a person presses the button: POST /api/update-check
// (POST, so a page load or prefetch never triggers it). It asks GitHub for the
// latest release of abhinavjp/unravis with no auth, no cookies and no user
// data, and answers "latest", "available", "dev" or "unreachable". Every
// answer is HTTP 200; the error text and the response body never leave the
// process.

const (
	updateCheckReleasesURL = "https://api.github.com/repos/abhinavjp/unravis/releases/latest"
	updateCheckPagePrefix  = "https://github.com/abhinavjp/unravis/"
	updateCheckBodyLimit   = 256 * 1024
	updateCheckCacheTTL    = 5 * time.Second
	updateCheckTimeout     = 10 * time.Second
)

// updateCheckReply is the JSON body of POST /api/update-check.
type updateCheckReply struct {
	Status  string `json:"status"` // "latest", "available", "dev" or "unreachable"
	Current string `json:"current"`
	Latest  string `json:"latest"` // empty unless status is "available"
	URL     string `json:"url"`    // empty unless status is "available" and the page is the project's own
}

// updateCheckState serialises checks and caches the last answer for
// updateCheckCacheTTL, so a double click costs one outgoing call.
type updateCheckState struct {
	mu     sync.Mutex
	http   *http.Client // nil uses the default client; tests set a fake RoundTripper
	now    func() time.Time
	cached updateCheckReply
	at     time.Time
	valid  bool
}

// updateCheckStates holds one state per server. The handler struct lives in
// server.go, which this change does not touch, so the state is kept here keyed
// by the handler; a handler lives as long as the process.
var updateCheckStates sync.Map // *handler -> *updateCheckState

func updateCheckStateFor(h *handler) *updateCheckState {
	state, _ := updateCheckStates.LoadOrStore(h, &updateCheckState{now: time.Now})
	return state.(*updateCheckState)
}

// WithUpdateCheckHTTP sets the client the update check talks through. Tests
// use it to inject a fake RoundTripper; nil restores the default client.
func WithUpdateCheckHTTP(client *http.Client) Option {
	return func(h *handler) {
		state := updateCheckStateFor(h)
		state.mu.Lock()
		state.http = client
		state.mu.Unlock()
	}
}

// updateCheck answers POST /api/update-check. It always answers 200; a failed
// check is status "unreachable", never an HTTP error.
func (h *handler) updateCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, updateCheckStateFor(h).check(r.Context()), 4096)
}

func (s *updateCheckState) check(ctx context.Context) updateCheckReply {
	current := version.Version
	if isDevVersion(current) {
		// Dev builds say so and never call out.
		return updateCheckReply{Status: "dev", Current: current}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.valid && s.cached.Current == current && s.now().Sub(s.at) < updateCheckCacheTTL {
		return s.cached
	}
	reply := s.fetch(ctx, current)
	s.cached, s.at, s.valid = reply, s.now(), true
	return reply
}

// isDevVersion reports whether the stamp is a development build: "dev",
// empty, or anything not starting with a digit once a leading v is trimmed.
func isDevVersion(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return true
	}
	return !unicode.IsDigit(rune(v[0]))
}

func (s *updateCheckState) fetch(ctx context.Context, current string) updateCheckReply {
	unreachable := updateCheckReply{Status: "unreachable", Current: current}
	client := s.http
	if client == nil {
		client = defaultUpdateCheckClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateCheckReleasesURL, nil)
	if err != nil {
		return unreachable
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Unravis/"+current)
	res, err := client.Do(req)
	if err != nil {
		return unreachable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// 403/429 rate limits land here too. The body is not read or logged.
		return unreachable
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, updateCheckBodyLimit+1))
	if err != nil || len(body) > updateCheckBodyLimit {
		return unreachable
	}
	var release struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return unreachable
	}
	if release.TagName == "" {
		return unreachable
	}
	if release.Draft || release.Prerelease {
		// Drafts and prereleases are not offers to upgrade.
		return updateCheckReply{Status: "latest", Current: current}
	}
	latest := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if compareVersions(latest, current) <= 0 {
		return updateCheckReply{Status: "latest", Current: current}
	}
	reply := updateCheckReply{Status: "available", Current: current, Latest: latest}
	// Only link to the project's own release pages.
	if strings.HasPrefix(release.HTMLURL, updateCheckPagePrefix) {
		reply.URL = release.HTMLURL
	}
	return reply
}

// defaultUpdateCheckClient is the client used when no other is set: a ten
// second timeout, and redirects that leave the API host are not followed.
var defaultUpdateCheckClient = sync.OnceValue(func() *http.Client {
	return &http.Client{
		Timeout: updateCheckTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != via[0].URL.Host {
				return http.ErrUseLastResponse
			}
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
})

// compareVersions compares two dotted numeric versions and returns 1 when a
// is newer than b, 0 when equal and -1 when older. A leading v and any
// -suffix (a prerelease marker on the tag, or "-dirty"/a hash on the running
// build) are ignored.
func compareVersions(a, b string) int {
	an, bn := versionNumbers(a), versionNumbers(b)
	for i := 0; i < len(an) || i < len(bn); i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionNumbers(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			n = 0
		}
		numbers = append(numbers, n)
	}
	return numbers
}
