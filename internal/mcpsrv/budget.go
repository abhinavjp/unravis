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
	CumulativeBytes int           // result bytes per CumulativeWindow, all tools (a code run's final result counts too)
	CumulativeWin   time.Duration //
	// BL-574 L1: a code run (unravis_execute) is counted in the same limiter as the other calls. Zero means the default, so a Budgets
	// written before these fields existed still lets code runs through.
	MaxCodeRuns    int // code runs at once, per host
	MaxCodeRunsAll int // code runs at once, all hosts
	CodeRate       int // code runs started per RateWindow
}

// DefaultBudgets are the numbers of spec 03: 1 and 2 heavy at once, 12 comparisons and 120 other live calls an hour, 2 MiB in 10 minutes.
var DefaultBudgets = Budgets{
	MaxLiveHeavy: 1, MaxLiveHeavyAll: 2, CompareRate: 12, LiveRate: 120, RateWindow: time.Hour,
	CumulativeBytes: 2 << 20, CumulativeWin: 10 * time.Minute,
	MaxCodeRuns: 1, MaxCodeRunsAll: 2, CodeRate: 30,
}

const bucket = 10 * time.Second // the byte counter keeps one number per 10 s, so its size is bounded whatever the call rate

type byteBucket struct {
	at time.Time
	n  int
}

type profileBudget struct {
	compares, lives []time.Time
	codeRuns        []time.Time
	heavy           int
	code            int // code runs running now
	bytes           []byteBucket
}

type limiter struct {
	b       Budgets
	mu      sync.Mutex
	per     map[string]*profileBudget
	heavyAl int
	codeAl  int
}

func newLimiter(b Budgets) *limiter {
	if b == (Budgets{}) {
		b = DefaultBudgets
	}
	if b.MaxCodeRuns == 0 {
		b.MaxCodeRuns = DefaultBudgets.MaxCodeRuns
	}
	if b.MaxCodeRunsAll == 0 {
		b.MaxCodeRunsAll = DefaultBudgets.MaxCodeRunsAll
	}
	if b.CodeRate == 0 {
		b.CodeRate = DefaultBudgets.CodeRate
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
type budgetRefusal struct {
	what string
	busy bool // a limit on what runs at once, not on how much was asked: it is not written to the ledger
}

// refundCodeRun takes back the place one code run admitted at that time took in the hourly count (the run was refused before a script started).
func (l *limiter) refundCodeRun(profile string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pb := l.per[profile]
	if pb == nil {
		return
	}
	for i, t := range pb.codeRuns {
		if t.Equal(at) {
			pb.codeRuns = append(pb.codeRuns[:i], pb.codeRuns[i+1:]...)
			return
		}
	}
}

// admit counts one call of the class and says whether it may run. done must be called when it ends, with the size of what it returned
// and the time it ended: the bytes of a long call are charged where they were delivered, not where the call started.
func (l *limiter) admit(profile, class string, now time.Time) (done func(bytes int, at time.Time), refused *budgetRefusal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pb := l.per[profile]
	if pb == nil {
		pb = &profileBudget{}
		l.per[profile] = pb
	}
	pb.compares, pb.lives = trim(pb.compares, now, l.b.RateWindow), trim(pb.lives, now, l.b.RateWindow)
	pb.codeRuns = trim(pb.codeRuns, now, l.b.RateWindow)
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
		return nil, &budgetRefusal{what: "the amount of data returned in the last minutes"}
	}
	heavy := class == ClassLiveCompare
	code := class == ClassCode
	switch {
	case code:
		if pb.code >= l.b.MaxCodeRuns {
			return nil, &budgetRefusal{what: "code runs at once", busy: true}
		}
		if l.codeAl >= l.b.MaxCodeRunsAll {
			// as with comparisons: it does not tell a host with nothing running that other hosts are running code
			return nil, &budgetRefusal{what: "what Unravis can run now", busy: true}
		}
		if len(pb.codeRuns) >= l.b.CodeRate {
			return nil, &budgetRefusal{what: "code runs in the last hour"}
		}
		pb.codeRuns = append(pb.codeRuns, now)
		pb.code++
		l.codeAl++
	case heavy:
		if pb.heavy >= l.b.MaxLiveHeavy {
			return nil, &budgetRefusal{what: "comparisons running at once"}
		}
		if l.heavyAl >= l.b.MaxLiveHeavyAll {
			// not "comparisons running at once": that would tell a host with nothing running that other hosts are comparing
			return nil, &budgetRefusal{what: "what Unravis can run now"}
		}
		if len(pb.compares) >= l.b.CompareRate {
			return nil, &budgetRefusal{what: "comparisons in the last hour"}
		}
		pb.compares = append(pb.compares, now)
		pb.heavy++
		l.heavyAl++
	case isLive(class):
		if len(pb.lives) >= l.b.LiveRate {
			return nil, &budgetRefusal{what: "live reads in the last hour"}
		}
		pb.lives = append(pb.lives, now)
	}
	return func(n int, end time.Time) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if heavy {
			pb.heavy--
			l.heavyAl--
		}
		if code {
			pb.code--
			l.codeAl--
		}
		if n <= 0 {
			return
		}
		at := end.Truncate(bucket)
		if m := len(pb.bytes); m > 0 && pb.bytes[m-1].at.Equal(at) {
			pb.bytes[m-1].n += n
		} else {
			pb.bytes = append(pb.bytes, byteBucket{at: at, n: n})
		}
	}, nil
}
