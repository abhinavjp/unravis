package blackbox

import (
	"sort"
	"strings"
	"time"
)

// HTTP.sys kernel request queues, machine-wide: one wildcard per counter
// covers every app-pool queue, selected or not. English paths only.
var httpPaths = []string{
	`\HTTP Service Request Queues(*)\CurrentQueueSize`,
	`\HTTP Service Request Queues(*)\MaxQueueItemAge`,
	`\HTTP Service Request Queues(*)\RejectedRequests`,
	`\HTTP Service Request Queues(*)\ArrivalRate`,
}

// httpCounters binds each counter name to its HTTPQueue json field and kind.
var httpCounters = []struct {
	counter string
	field   string
	kind    CounterKind
}{
	{"CurrentQueueSize", "currentQueueSize", KindGauge},
	{"MaxQueueItemAge", "maxQueueItemAge", KindGauge},
	{"RejectedRequests", "rejectedRequests", KindPDHRate},
	{"ArrivalRate", "arrivalRate", KindPDHRate},
}

const httpQueuePrefix = `\HTTP Service Request Queues(`

func init() { RegisterGroup("http", true, newHTTPGroup) }

// httpCounter binds one expanded path to its pool and output field. The key
// is the engine output key; NUL cannot occur in a PDH instance name.
type httpCounter struct {
	key   string
	pool  string
	field string
}

type httpGroup struct {
	src      CounterSource
	engine   *Engine
	counters []httpCounter
	changed  func() bool
	cores    int
	owned    bool // factory created the source: Close owns it
}

func newHTTPGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSource(httpPaths)
		if err != nil {
			return nil, err // no PDH (non-Windows) or no HTTP.sys: skipped
		}
		src, owned = p, true
	}
	g := &httpGroup{src: src, changed: d.ProcessSetChanged, cores: d.Cores, owned: owned}
	g.rebuild(httpPathsOf(src))
	return g, nil
}

func (g *httpGroup) Name() string   { return "http" }
func (g *httpGroup) Optional() bool { return true }

// httpPathsOf reads the expanded paths when the source exposes them.
func httpPathsOf(src CounterSource) []string {
	if p, ok := src.(interface{ Paths() []string }); ok {
		return p.Paths()
	}
	return nil
}

// rebuild derives per-(pool,counter) specs from the expanded paths and a
// fresh engine: new instances start their two-baseline gate again.
func (g *httpGroup) rebuild(paths []string) {
	counters := make([]httpCounter, 0, len(paths))
	specs := make([]CounterSpec, 0, len(paths))
	for _, path := range paths {
		pool, field, kind, ok := parseHTTPPath(path)
		if !ok {
			continue
		}
		key := pool + "\x00" + field
		specs = append(specs, CounterSpec{Path: path, Field: key, Kind: kind})
		counters = append(counters, httpCounter{key: key, pool: pool, field: field})
	}
	g.counters = counters
	g.engine = NewEngine(specs, g.cores)
}

// parseHTTPPath extracts the pool (the instance between the parentheses)
// and matches the counter suffix; anything else is ignored.
func parseHTTPPath(path string) (pool, field string, kind CounterKind, ok bool) {
	if !strings.HasPrefix(path, httpQueuePrefix) {
		return "", "", 0, false
	}
	rest := path[len(httpQueuePrefix):]
	end := strings.IndexByte(rest, ')')
	if end < 0 || !strings.HasPrefix(rest[end+1:], `\`) {
		return "", "", 0, false
	}
	pool, counter := rest[:end], rest[end+2:]
	for _, c := range httpCounters {
		if counter == c.counter {
			return pool, c.field, c.kind, true
		}
	}
	return "", "", 0, false
}

// refresh re-expands the wildcards on the same query handle — only when the
// process set changed, never per tick.
func (g *httpGroup) refresh() error {
	if r, ok := g.src.(interface{ RefreshInstances() error }); ok {
		if err := r.RefreshInstances(); err != nil {
			return err
		}
	}
	g.rebuild(httpPathsOf(g.src))
	return nil
}

func (g *httpGroup) Collect(now time.Time, dt float64) (any, error) {
	if g.changed != nil && g.changed() {
		if err := g.refresh(); err != nil {
			return nil, err
		}
	}
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	out := g.engine.Process(now, readings)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted, Missing via collector
	}
	byPool := make(map[string]*HTTPQueue)
	for _, c := range g.counters {
		v, ok := out[c.key]
		if !ok {
			continue // missing stays nil, never 0
		}
		q := byPool[c.pool]
		if q == nil {
			q = &HTTPQueue{Pool: c.pool}
			byPool[c.pool] = q
		}
		switch c.field {
		case "currentQueueSize":
			q.CurrentQueueSize = &v
		case "maxQueueItemAge":
			q.MaxQueueItemAge = &v
		case "rejectedRequests":
			q.RejectedRequests = &v
		case "arrivalRate":
			q.ArrivalRate = &v
		}
	}
	pools := make([]string, 0, len(byPool))
	for pool := range byPool {
		pools = append(pools, pool)
	}
	sort.Strings(pools) // deterministic queue order
	queues := make([]HTTPQueue, 0, len(pools))
	for _, pool := range pools {
		queues = append(queues, *byPool[pool])
	}
	return &HTTPGroup{Queues: queues}, nil
}

// Close releases the query handle only when the factory created the source.
func (g *httpGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}