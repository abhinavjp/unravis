package blackbox

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// B7: counters the owner adds by path, with no release. Settings carry only English PDH paths ("counters.extra"); the
// reading is the same one Performance Monitor shows. A path that does not exist on this server is skipped and named
// in the sample, so a typo is visible instead of silent. No wildcards: every path is one counter, so the cost of a
// settings change is bounded by the 50-path cap.

func init() { RegisterGroup("extraCounters", true, newExtraCountersGroup) }

const (
	maxExtraCounters    = 50
	maxExtraCounterPath = 200
)

// extraCounterRe accepts \Object\Counter and \Object(Instance)\Counter. Machine prefixes (\\server\...) and
// wildcards are not allowed. The instance and counter parts cannot hold a backslash.
var extraCounterRe = regexp.MustCompile(`^\\[A-Za-z0-9 .#&_%/:-]+(\([A-Za-z0-9 .#&_%/:$-]+\))?\\[A-Za-z0-9 .#&_%/:()-]+$`)

// CounterSettings is the "counters" block of blackbox.json.
type CounterSettings struct {
	// Extra are PDH paths recorded in every sample on top of the built-in counters.
	Extra []string `json:"extra,omitempty"`
}

func (s CounterSettings) validate() error {
	if len(s.Extra) > maxExtraCounters {
		return invalidf(`"counters.extra" holds at most %d paths`, maxExtraCounters)
	}
	seen := map[string]bool{}
	for _, p := range s.Extra {
		if len(p) > maxExtraCounterPath || !extraCounterRe.MatchString(p) {
			return invalidf(`"counters.extra" entry %q must look like \Object\Counter or \Object(Instance)\Counter, with no wildcard and no server name`, p)
		}
		key := strings.ToLower(p)
		if seen[key] {
			return invalidf(`"counters.extra" lists %q twice`, p)
		}
		seen[key] = true
	}
	return nil
}

// ExtraCounterPaths are the owner's extra counter paths ("" entries never appear).
func (c Config) ExtraCounterPaths() []string {
	if c.Counters == nil {
		return nil
	}
	return append([]string(nil), c.Counters.Extra...)
}

// ExtraCountersGroup is the sample block of the extra counters: value by path, and the paths that gave nothing.
type ExtraCountersGroup struct {
	Values  map[string]float64 `json:"values,omitempty"`
	Skipped []string           `json:"skipped,omitempty"`
}

type extraCountersGroup struct {
	src    CounterSource
	engine *Engine
	paths  []string
	owned  bool
}

func newExtraCountersGroup(d GroupDeps) (Group, error) {
	paths := d.Config.ExtraCounterPaths()
	if len(paths) == 0 {
		return nil, errNoTargets // nothing asked for: no source, nothing recorded
	}
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSourceTolerant(paths)
		if err != nil {
			return nil, err
		}
		src, owned = p, true
	}
	specs := make([]CounterSpec, 0, len(paths))
	for _, p := range paths {
		kind := KindGauge
		if strings.HasSuffix(strings.ToLower(p), "/sec") {
			kind = KindPDHRate // already per second from PDH; the engine only waits for two baselines
		}
		specs = append(specs, CounterSpec{Path: p, Field: p, Kind: kind, Volatile: strings.Contains(p, "(")})
	}
	return &extraCountersGroup{src: src, engine: NewEngine(specs, d.Cores), paths: paths, owned: owned}, nil
}

func (g *extraCountersGroup) Name() string   { return "extraCounters" }
func (g *extraCountersGroup) Optional() bool { return true }

func (g *extraCountersGroup) Collect(now time.Time, _ float64) (any, error) {
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	out := g.engine.Process(now, readings)
	got := map[string]bool{}
	for _, r := range readings {
		if r.OK && CounterStatusOK(r.Status) {
			got[r.Path] = true
		}
	}
	grp := ExtraCountersGroup{}
	for path, v := range out {
		// NaN and Inf cannot be written as JSON: one bad counter must not lose the whole sample.
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if grp.Values == nil {
			grp.Values = map[string]float64{}
		}
		grp.Values[path] = v
	}
	for _, p := range g.paths {
		if !got[p] {
			grp.Skipped = append(grp.Skipped, p)
		}
	}
	sort.Strings(grp.Skipped)
	if grp.Values == nil && grp.Skipped == nil {
		return nil, nil // only warming-up rates this tick: nothing to say yet
	}
	return &grp, nil
}

func (g *extraCountersGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}
