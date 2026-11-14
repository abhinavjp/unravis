package blackbox

import "time"

// CounterKind selects how a raw reading becomes an emitted field.
type CounterKind uint8

const (
	// KindGauge emits the value as-is (no clamp: multi-core CPU may exceed 100).
	KindGauge CounterKind = iota
	// KindRate is a cumulative raw counter; the engine computes a per-second delta.
	KindRate
	// KindPDHRate is already per-second from PDH; the engine still gates it.
	KindPDHRate
)

// CounterSpec binds an English PDH path to an output field and a kind.
type CounterSpec struct {
	Path  string // English PDH path (fixed identifier, never captured text)
	Field string // output key
	Kind  CounterKind
	// Volatile marks a path whose instance name can be rebound to a different
	// entity: PDH names Process instances "w3wp", "w3wp#1" by enumeration order,
	// so the same string can mean another process after a refresh. Such a path
	// must never carry a rate baseline across Reconcile. Machine-wide paths
	// (a disk, a processor, an app pool's queue, a SQL instance, an interface)
	// are their own identity and leave this false.
	Volatile bool
}

// PDH per-counter data statuses that mean "this reading is usable". They are
// separate from the API return code: PdhGetFormattedCounterValue can succeed
// while CStatus reports NEW_DATA, which is a normal success for a counter whose
// value changed since the previous collect.
const (
	pdhStatusValidData uint32 = 0x00000000 // PDH_CSTATUS_VALID_DATA
	pdhStatusNewData   uint32 = 0x00000001 // PDH_CSTATUS_NEW_DATA
)

// CounterStatusOK reports whether a PDH CStatus is usable data. Both source and
// engine use this one predicate so a valid reading cannot be dropped at one
// boundary and accepted at the other.
func CounterStatusOK(status uint32) bool {
	return status == pdhStatusValidData || status == pdhStatusNewData
}

// CounterReading is one raw counter sample. It is named CounterReading because
// the package already has a Reading (the manifest read in reader.go).
type CounterReading struct {
	Path   string
	Value  float64
	Status uint32 // PDH CStatus; usable when CounterStatusOK(Status)
	OK     bool   // false when the API call failed or the status is not usable
}

// CounterSource produces readings; implemented by PDH (later) and by fakes.
type CounterSource interface {
	Collect() ([]CounterReading, error)
	Close() error
}

// pathHistory holds the per-path baseline state, reused across Process calls.
type pathHistory struct {
	lastValue float64
	lastTime  time.Time
	baselines int // count of valid baseline samples recorded for this path
}

// Engine turns raw readings into emitted fields under the BL-121 rules.
type Engine struct {
	byPath  map[string]CounterSpec
	history map[string]*pathHistory
	cores   int // >=1; held for later normalization, never used to clamp
}

// NewEngine clamps cores to >=1 and indexes specs by path.
func NewEngine(specs []CounterSpec, cores int) *Engine {
	if cores < 1 {
		cores = 1
	}
	byPath := make(map[string]CounterSpec, len(specs))
	for _, s := range specs {
		byPath[s.Path] = s
	}
	return &Engine{
		byPath:  byPath,
		history: make(map[string]*pathHistory),
		cores:   cores,
	}
}

// Reconcile adopts a new spec set after a wildcard refresh WITHOUT throwing away
// the baselines of counters that did not change. Rebuilding the engine instead
// would re-prime every rate on each unrelated process arrival, so a busy host
// could go indefinitely without a single rate while its gauges still looked
// healthy.
//
// History is kept only for a path that is still present, still has the same
// kind, and is not Volatile: a changed kind changes the meaning of the stored
// value, and a volatile instance name may now belong to another process. Removed
// paths lose their history so a departed instance cannot leak state.
func (e *Engine) Reconcile(specs []CounterSpec) {
	next := make(map[string]CounterSpec, len(specs))
	for _, s := range specs {
		next[s.Path] = s
	}
	for path := range e.history {
		s, still := next[path]
		old, had := e.byPath[path]
		if !still || s.Volatile || (had && s.Kind != old.Kind) {
			delete(e.history, path)
		}
	}
	e.byPath = next
}

// reconcileEngine adopts specs into an existing engine, or builds the first one.
// Group rebuild functions run both at construction (no engine yet) and on a
// refresh, so they share this instead of choosing between NewEngine/Reconcile.
func reconcileEngine(e *Engine, specs []CounterSpec, cores int) *Engine {
	if e == nil {
		return NewEngine(specs, cores)
	}
	e.Reconcile(specs)
	return e
}

// Process emits only the fields that are valid this cycle. Absent fields mean
// "missing", never 0. One output map per call; history maps are reused.
func (e *Engine) Process(now time.Time, readings []CounterReading) map[string]float64 {
	out := make(map[string]float64)
	for _, r := range readings {
		spec, ok := e.byPath[r.Path]
		if !ok {
			continue // no spec for this path
		}
		// Valid-status check on every reading; invalid never advances baselines.
		// NEW_DATA is a success status, not an error: rejecting it would drop
		// exactly the samples where a counter changed.
		if !r.OK || !CounterStatusOK(r.Status) {
			continue
		}
		if spec.Kind == KindGauge {
			out[spec.Field] = r.Value // no clamp: 350 on 4 cores stays 350
			continue
		}
		e.processRate(spec, r, now, out)
	}
	return out
}

// processRate gates KindRate/KindPDHRate behind two valid baselines, then emits.
func (e *Engine) processRate(spec CounterSpec, r CounterReading, now time.Time, out map[string]float64) {
	h := e.history[r.Path]
	if h == nil {
		h = &pathHistory{}
		e.history[r.Path] = h
	}

	// The first two valid samples are baselines: record them, emit nothing.
	if h.baselines < 2 {
		h.baselines++
		h.lastValue = r.Value
		h.lastTime = now
		return
	}

	if spec.Kind == KindPDHRate {
		// Already per-second; no delta, no reset/dt guard. Just gate on baselines.
		out[spec.Field] = r.Value
		h.lastValue = r.Value
		h.lastTime = now
		return
	}

	// KindRate: per-second delta against the previous valid sample.
	prevValue := h.lastValue
	prevTime := h.lastTime

	// Counter reset (cumulative dropped): skip emission, restart the baselines so
	// the next two valid samples re-gate before any rate.
	if r.Value < prevValue {
		h.baselines = 1
		h.lastValue = r.Value
		h.lastTime = now
		return
	}

	dt := now.Sub(prevTime).Seconds()
	if dt <= 0 {
		// Zero/negative elapsed time: no divide-by-zero, no emission this cycle.
		h.lastValue = r.Value
		h.lastTime = now
		return
	}

	out[spec.Field] = (r.Value - prevValue) / dt
	h.lastValue = r.Value
	h.lastTime = now
}