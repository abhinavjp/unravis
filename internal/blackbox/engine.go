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
}

// CounterReading is one raw counter sample. It is named CounterReading because
// the package already has a Reading (the manifest read in reader.go).
type CounterReading struct {
	Path   string
	Value  float64
	Status uint32 // PDH status; valid only when 0 (ERROR_SUCCESS)
	OK     bool   // source sets false for any non-zero status; engine double-checks
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
		if !r.OK || r.Status != 0 {
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