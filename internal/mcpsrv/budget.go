package mcpsrv

import (
	"sync"
	"time"
)

// BL-350, the budget part: how much one host may ask of the repositories (spec 03 section 3.4, "Budgets default"). A host that is
// approved can still not loop: it gets a refusal that says so, and the person sees the refusals in the ledger. Counters live in memory,
// per profile id, and survive a revoke and a reissue of the same id (a new grant does not reset the rate); a restart of the owner resets
// them, which a host cannot cause. The numbers are defaults until the BL-355 spike sets them.

// Budgets are the limits of one host.
type Budgets struct {
	MaxLiveHeavy    int           // audit_compare running at once, per host
	MaxLiveHeavyAll int           // audit_compare running at once, all hosts
	CompareRate     int           // audit_compare calls per RateWindow
	LiveRate        int           // calls of the other live tools (requests, slowest, activity, breakdown) per RateWindow
	RateWindow      time.Duration //
	CumulativeBytes int           // result bytes per CumulativeWindow, all tools
	CumulativeWin   time.Duration //
}

// DefaultBudgets are the numbers of spec 03: 1 and 2 heavy at once, 12 comparisons and 120 other live calls an hour, 2 MiB in 10 minutes.
var DefaultBudgets = Budgets{
	MaxLiveHeavy: 1, MaxLiveHeavyAll: 2, CompareRate: 12, LiveRate: 120, RateWindow: time.Hour,
	CumulativeBytes: 2 << 20, CumulativeWin: 10 * time.Minute,
}

const bucket = 10 * time.Second // the byte counter keeps one number per 10 s, so its size is bounded whatever the call rate

type byteBucket struct {
	at time.Time
	n  int
}

type profileBudget struct {
	compares, lives []time.Time
	heavy           int
	bytes           []byteBucket
}

type limiter struct {
	b       Budgets
	mu      sync.Mutex
	per     map[string]*profileBudget
	heavyAl int
}

func newLimiter(b Budgets) *limiter {
	if b == (Budgets{}) {
		b = DefaultBudgets
	}
	return &limiter{b: b, per: map[string]*profileBudget{}}
}

func trim(ts []time.Time, now time.Time, win time.Duration) []time.Time {
	i := 0
	for i < len(ts) && !ts[i].After(now.Add(-win)) {
		i++
	}
	return ts[i:]
}

// budgetRefusal says which limit was reached. The text never names a number the host could not already know.
type budgetRefusal struct{ what string }

// admit counts one call of the class and says whether it may run. done must be called when it ends, with the size of what it returned.
func (l *limiter) admit(profile, class string, now time.Time) (done func(bytes int), refused *budgetRefusal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pb := l.per[profile]
	if pb == nil {
		pb = &profileBudget{}
		l.per[profile] = pb
	}
	pb.compares, pb.lives = trim(pb.compares, now, l.b.RateWindow), trim(pb.lives, now, l.b.RateWindow)
	var used int
	k := 0
	for _, bk := range pb.bytes {
		if bk.at.After(now.Add(-l.b.CumulativeWin)) {
			pb.bytes[k] = bk
			k++
			used += bk.n
		}
	}
	pb.bytes = pb.bytes[:k]
	if used >= l.b.CumulativeBytes {
		return nil, &budgetRefusal{"the amount of data returned in the last minutes"}
	}
	heavy := class == ClassLiveCompare
	switch {
	case heavy:
		if pb.heavy >= l.b.MaxLiveHeavy || l.heavyAl >= l.b.MaxLiveHeavyAll {
			return nil, &budgetRefusal{"comparisons running at once"}
		}
		if len(pb.compares) >= l.b.CompareRate {
			return nil, &budgetRefusal{"comparisons in the last hour"}
		}
		pb.compares = append(pb.compares, now)
		pb.heavy++
		l.heavyAl++
	case isLive(class):
		if len(pb.lives) >= l.b.LiveRate {
			return nil, &budgetRefusal{"live reads in the last hour"}
		}
		pb.lives = append(pb.lives, now)
	}
	return func(n int) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if heavy {
			pb.heavy--
			l.heavyAl--
		}
		if n <= 0 {
			return
		}
		at := now.Truncate(bucket)
		if m := len(pb.bytes); m > 0 && pb.bytes[m-1].at.Equal(at) {
			pb.bytes[m-1].n += n
		} else {
			pb.bytes = append(pb.bytes, byteBucket{at: at, n: n})
		}
	}, nil
}
