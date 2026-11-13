package blackbox

import (
	"encoding/binary"
	"strings"
	"time"
)

func init() { RegisterGroup("tcp", true, newTCPGroup) }

// tcpRatePaths are the cheap direct counters, added at factory time so host
// startup stays fast. The Network Interface wildcard is deferred (upgrade).
func tcpRatePaths() []string {
	return []string{
		`\TCPv4\Segments Retransmitted/sec`,
		`\TCPv6\Segments Retransmitted/sec`,
		`\TCPv4\Connections Reset`,
		`\TCPv6\Connections Reset`,
	}
}

// tcpPaths adds the per-interface outbound queue gauge (wildcard expands to
// the interfaces PDH enumerates). First expansion of this object is slow
// (~1.5 s), so it is only used after the host is already listening.
func tcpPaths() []string {
	return append(tcpRatePaths(), `\Network Interface(*)\Output Queue Length`)
}

const (
	tcpQueuePrefix = `\Network Interface(`
	tcpQueueSuffix = `\Output Queue Length`
	// tcpRowStride is the MIB_TCPROW2 size (7 DWORDs). Only dwState at
	// offset 0 is ever read; addresses, ports and PIDs are never copied.
	tcpRowStride = 28
	// maxTCPStateRows caps the table scan for footprint (BL-122).
	maxTCPStateRows = 65536
)

type tcpGroup struct {
	src       CounterSource
	eng       *Engine
	cores     int
	changed   func() bool
	owned     bool // factory created the source: Close owns it
	queueKeys []string
	counts    func() ([]TCPStateCount, error) // injected in tests
	upgraded  bool // one-time lazy wildcard expansion attempted
}

func newTCPGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSource(tcpRatePaths())
		if err != nil {
			return nil, err // no PDH (non-Windows): group skipped
		}
		src, owned = p, true
	}
	g := &tcpGroup{src: src, cores: d.Cores, changed: d.ProcessSetChanged, owned: owned, counts: tcpTableCounts}
	g.rebuild(tcpPathsOf(src))
	return g, nil
}

func (g *tcpGroup) Name() string   { return "tcp" }
func (g *tcpGroup) Optional() bool { return true }

// tcpPathsOf reads the expanded paths when the source exposes them.
func tcpPathsOf(src CounterSource) []string {
	if p, ok := src.(interface{ Paths() []string }); ok {
		return p.Paths()
	}
	return nil
}

// rebuild derives specs from the expanded paths and a fresh engine: new
// instances start their two-baseline gate again.
func (g *tcpGroup) rebuild(paths []string) {
	specs := make([]CounterSpec, 0, len(paths))
	g.queueKeys = nil
	for _, p := range paths {
		switch p {
		case `\TCPv4\Segments Retransmitted/sec`:
			specs = append(specs, CounterSpec{Path: p, Field: "retrans4", Kind: KindPDHRate})
		case `\TCPv6\Segments Retransmitted/sec`:
			specs = append(specs, CounterSpec{Path: p, Field: "retrans6", Kind: KindPDHRate})
		case `\TCPv4\Connections Reset`:
			specs = append(specs, CounterSpec{Path: p, Field: "reset4", Kind: KindPDHRate})
		case `\TCPv6\Connections Reset`:
			specs = append(specs, CounterSpec{Path: p, Field: "reset6", Kind: KindPDHRate})
		default:
			if strings.HasPrefix(p, tcpQueuePrefix) && strings.HasSuffix(p, tcpQueueSuffix) {
				specs = append(specs, CounterSpec{Path: p, Field: p, Kind: KindGauge})
				g.queueKeys = append(g.queueKeys, p)
			}
		}
	}
	g.eng = NewEngine(specs, g.cores)
}

// upgrade swaps in a source that also expands the Network Interface wildcard.
// That first expansion is slow, so it is deferred past host startup to the
// first Collect (the status server is already listening by then). One attempt
// only: on failure the rate-only source is kept and OutboundQueue stays nil.
// Injected sources are never swapped. The fresh engine re-gates the rates.
func (g *tcpGroup) upgrade() {
	if g.upgraded || !g.owned {
		return
	}
	g.upgraded = true
	p, err := NewPDHSource(tcpPaths())
	if err != nil {
		return
	}
	old := g.src
	g.src = p
	g.rebuild(tcpPathsOf(p))
	_ = old.Close()
}

func (g *tcpGroup) Collect(now time.Time, _ float64) (any, error) {
	g.upgrade()
	// Re-expand wildcards only when the process set changed, never per tick.
	if g.changed != nil && g.changed() {
		if r, ok := g.src.(interface{ RefreshInstances() error }); ok {
			if err := r.RefreshInstances(); err != nil {
				return nil, err
			}
		}
		g.rebuild(tcpPathsOf(g.src))
	}
	var grp TCPGroup
	have := false
	if readings, err := g.src.Collect(); err == nil {
		out := g.eng.Process(now, readings)
		// Machine-wide sums need both families; a missing side stays nil.
		if a, ok := out["retrans4"]; ok {
			if b, ok := out["retrans6"]; ok {
				v := a + b
				grp.RetransmitsPerSec, have = &v, true
			}
		}
		if a, ok := out["reset4"]; ok {
			if b, ok := out["reset6"]; ok {
				v := a + b
				grp.ResetsPerSec, have = &v, true
			}
		}
		var sum float64
		n := 0
		for _, k := range g.queueKeys {
			if v, ok := out[k]; ok {
				sum += v
				n++
			}
		}
		if n > 0 {
			grp.OutboundQueue, have = &sum, true
		}
	}
	// A failed state query leaves States nil; the rates above are kept.
	if states, err := g.counts(); err == nil && len(states) > 0 {
		grp.States, have = states, true
	}
	if !have {
		return nil, nil // both sides failed: group omitted, Missing via collector
	}
	return &grp, nil
}

// Close releases the query handle only when the factory created the source.
func (g *tcpGroup) Close() error {
	if g.owned {
		return g.src.Close()
	}
	return nil
}

// tcpStateCountsFromTable counts MIB_TCPTABLE2 rows by dwState only. The
// address/port/PID bytes are never read; the scan is capped at maxTCPStateRows
// and a truncated buffer counts complete rows only. States come out sorted.
func tcpStateCountsFromTable(buf []byte) []TCPStateCount {
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	if n > maxTCPStateRows {
		n = maxTCPStateRows
	}
	if fit := (len(buf) - 4) / tcpRowStride; n > fit {
		n = fit
	}
	var counts [13]uint32 // MIB_TCP_STATE 1..12
	for i := 0; i < n; i++ {
		if s := binary.LittleEndian.Uint32(buf[4+i*tcpRowStride:]); s >= 1 && s <= 12 {
			counts[s]++
		}
	}
	out := make([]TCPStateCount, 0, 12)
	for s := 1; s <= 12; s++ {
		if counts[s] > 0 {
			out = append(out, TCPStateCount{State: uint8(s), Count: counts[s]})
		}
	}
	return out
}