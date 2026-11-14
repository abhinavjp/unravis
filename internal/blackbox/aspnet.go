package blackbox

import "time"

// BBX-4-03 Classic ASP.NET request queues and restarts, machine-wide: fixed
// English PDH paths for .NET Framework hosts. No wildcards, nothing to
// re-expand; one query handle reused for the source lifetime. No text.

func init() { RegisterGroup("aspnet", true, newASPNETGroup) }

// aspnetPaths are the fixed English PDH paths this group samples (UNVERIFIED
// against a live Framework host pending the typeperf check).
var aspnetPaths = []string{
	`\ASP.NET\Requests Queued`,
	`\ASP.NET\Requests Rejected`,
	`\ASP.NET\Request Wait Time`,
	`\ASP.NET\Application Restarts`,
	`\ASP.NET Applications(__Total__)\Requests Current`,
}

// aspnetSpecs binds each fixed path to its ASPNETGroup json field and kind.
// Requests Rejected is the only rate (two baselines before emission); the
// rest are gauges. Field names are the ASPNETGroup json fields.
var aspnetSpecs = []CounterSpec{
	{Path: aspnetPaths[0], Field: "requestsQueued", Kind: KindGauge},
	{Path: aspnetPaths[1], Field: "requestsRejected", Kind: KindPDHRate},
	{Path: aspnetPaths[2], Field: "requestWaitTimeMs", Kind: KindGauge},
	{Path: aspnetPaths[3], Field: "applicationRestarts", Kind: KindGauge},
	{Path: aspnetPaths[4], Field: "requestsCurrent", Kind: KindGauge},
}

type aspnetGroup struct {
	src   CounterSource
	eng   *Engine
	owned bool // factory created the source: Close owns it
}

func newASPNETGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSource(aspnetPaths)
		if err != nil {
			return nil, err // no PDH (non-Windows) or no ASP.NET objects: skipped
		}
		src, owned = p, true
	}
	return &aspnetGroup{src: src, eng: NewEngine(aspnetSpecs, d.Cores), owned: owned}, nil
}

func (g *aspnetGroup) Name() string   { return "aspnet" }
func (g *aspnetGroup) Optional() bool { return true }

func (g *aspnetGroup) Collect(now time.Time, _ float64) (any, error) {
	readings, err := g.src.Collect()
	if err != nil {
		return nil, err
	}
	out := g.eng.Process(now, readings)
	if len(out) == 0 {
		return nil, nil // nothing valid: group omitted, Missing via collector
	}
	var grp ASPNETGroup
	if v, ok := out["requestsQueued"]; ok {
		grp.RequestsQueued = &v
	}
	if v, ok := out["requestsCurrent"]; ok {
		grp.RequestsCurrent = &v
	}
	if v, ok := out["requestsRejected"]; ok {
		grp.RequestsRejected = &v
	}
	if v, ok := out["requestWaitTimeMs"]; ok {
		grp.RequestWaitTimeMs = &v
	}
	if v, ok := out["applicationRestarts"]; ok {
		grp.ApplicationRestarts = &v
	}
	return &grp, nil
}

// Close releases the query handle only when the factory created the source.
func (g *aspnetGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}