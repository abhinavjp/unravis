package keeper

import (
	"sync"
	"time"
)

// Rate classes. A class groups operations by what they cost the outside world: "reach" opens a connection to a server someone else runs
// (SQL Server, MongoDB, a Blackbox), "write" changes the sealed files, "read" only reads them.
const (
	classRead  = "read"
	classWrite = "write"
	classReach = "reach"
	classBlob  = "blob" // Blackbox file reads: many small GETs per look
	// classBad counts requests the keeper will not run (not understood, another version, an unknown operation): Unravis never sends
	// them, so a few a minute is plenty, and the limit keeps a loop of them from filling the audit log.
	classBad = "bad"
)

// DefaultLimits is calls per caller per window, by class.
var DefaultLimits = map[string]int{
	classRead:  600,
	classWrite: 60,
	classReach: 30,
	classBlob:  3000,
	classBad:   30,
}

// DefaultWindow is the sliding window of the rate limit.
const DefaultWindow = time.Minute

// RateLimiter is a sliding-window count per caller SID and class. A refusal answers "rate-limited" and is logged; it never blocks.
type RateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	limits map[string]int
	seen   map[string][]time.Time
	now    func() time.Time
}

// NewRateLimiter uses limits (nil means DefaultLimits) over window (0 means DefaultWindow). Limits that leave out the "bad" class get
// its default: that limit is never off.
func NewRateLimiter(limits map[string]int, window time.Duration) *RateLimiter {
	if limits == nil {
		limits = DefaultLimits
	}
	if n, ok := limits[classBad]; !ok || n <= 0 {
		withBad := map[string]int{classBad: DefaultLimits[classBad]}
		for k, v := range limits {
			if k != classBad {
				withBad[k] = v
			}
		}
		limits = withBad
	}
	if window <= 0 {
		window = DefaultWindow
	}
	return &RateLimiter{window: window, limits: limits, seen: map[string][]time.Time{}, now: time.Now}
}

// Allow counts one call and reports whether it is within the limit.
func (r *RateLimiter) Allow(sid, class string) bool {
	if r == nil {
		return true
	}
	limit, ok := r.limits[class]
	if !ok || limit <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	key := sid + "\x00" + class
	kept := r.seen[key][:0]
	for _, t := range r.seen[key] {
		if now.Sub(t) < r.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		r.seen[key] = kept
		return false
	}
	r.seen[key] = append(kept, now)
	// Keep the map small: forget callers whose window is empty.
	if len(r.seen) > 1024 {
		for k, v := range r.seen {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= r.window {
				delete(r.seen, k)
			}
		}
	}
	return true
}
