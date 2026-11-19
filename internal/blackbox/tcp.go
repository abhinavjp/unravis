package blackbox

import (
	"encoding/binary"
	"errors"
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
		`\TCPv4\Connection Failures`,
		`\TCPv6\Connection Failures`,
	}
}

// tcpPaths adds the per-interface outbound queue gauge (wildcard expands to
// the interfaces PDH enumerates). First expansion of this object is slow
// (~1.5 s), so it is only used after the host is already listening.
func tcpPaths() []string {
	return append(tcpRatePaths(),
		`\Network Interface(*)\Output Queue Length`,
		`\Network Interface(*)\Packets Received Discarded`,
		`\Network Interface(*)\Packets Outbound Errors`)
}

// tcpMaxInterfaces caps how many adapters each per-adapter counter follows. A
// host with hundreds of virtual adapters (containers, VPNs) must not make the
// sampler track hundreds of series; the cap keeps the cost flat and the sum is
// still labeled by how many adapters it covers.
const tcpMaxInterfaces = 64

const (
	tcpQueuePrefix  = `\Network Interface(`
	tcpQueueSuffix  = `\Output Queue Length`
	tcpDiscSuffix   = `\Packets Received Discarded`
	tcpOutErrSuffix = `\Packets Outbound Errors`
	// tcpRowStride is the MIB_TCPROW2 size (7 DWORDs). Only dwState at
	// offset 0 is ever read; addresses, ports and PIDs are never copied.
	tcpRowStride = 28
	// maxTCPStateRows caps the table scan for footprint (BL-122).
	maxTCPStateRows = 65536
	// maxTCPTableBytes is the checked byte ceiling for the native table. It is
	// enforced BEFORE any allocation (W6-03): a table larger than the rows we
	// would ever scan is refused, so the sampler never allocates the whole
	// native buffer (addresses/ports/PIDs included) just to count a prefix.
	maxTCPTableBytes = 4 + maxTCPStateRows*tcpRowStride
	// tcpTableMaxAttempts bounds the retry loop when the table grows between
	// the size query and the read (W6-03).
	tcpTableMaxAttempts = 3
)

// Fixed TCP-table failure codes (C10): never raw provider text.
var (
	errTCPTableUnreadable = errors.New("tcp table unreadable")
	errTCPTableGrew       = errors.New("tcp table grew during read")
)

// tcpStates is one TCP-table read outcome. It distinguishes the three cases the
// review asked for (W6-03): empty (counts nil, truncated false, no error),
// unreadable (error), and incomplete (truncated true, counts partial or nil).
type tcpStates struct {
	counts    []TCPStateCount
	truncated bool
}

type tcpGroup struct {
	src        CounterSource
	eng        *Engine
	cores      int
	refresh    refreshTimer
	owned      bool // factory created the source: Close owns it
	queueKeys  []string
	discKeys   []string
	outErrKeys []string
	// How many adapters PDH listed for each per-adapter counter, before the cap.
	// A sum over fewer than this is partial (BL-138).
	queueSeen, discSeen, outErrSeen int
	counts                          func() (tcpStates, error)             // injected in tests
	openWild                        func([]string) (CounterSource, error) // the slow expansion; a seam for tests
	upgraded                        bool                                  // one-time lazy wildcard expansion started
	warm                            chan CounterSource                    // the background expansion; nil once taken
}

func newTCPGroup(d GroupDeps) (Group, error) {
	src, owned := d.Source, false
	if src == nil {
		p, err := NewPDHSourceTolerant(tcpRatePaths())
		if err != nil {
			return nil, err // no PDH (non-Windows): group skipped
		}
		src, owned = p, true
	}
	g := &tcpGroup{src: src, cores: d.Cores, owned: owned, counts: tcpTableCounts, openWild: openPDH}
	g.rebuild(tcpPathsOf(src))
	return g, nil
}

// openPDH opens the real PDH source; the nil interface is returned on failure.
func openPDH(paths []string) (CounterSource, error) {
	p, err := NewPDHSource(paths)
	if err != nil {
		return nil, err
	}
	return p, nil
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
	g.queueKeys, g.discKeys, g.outErrKeys = nil, nil, nil
	g.queueSeen, g.discSeen, g.outErrSeen = 0, 0, 0
	for _, p := range paths {
		switch p {
		case `\TCPv4\Segments Retransmitted/sec`:
			specs = append(specs, CounterSpec{Path: p, Field: "retrans4", Kind: KindPDHRate})
		case `\TCPv6\Segments Retransmitted/sec`:
			specs = append(specs, CounterSpec{Path: p, Field: "retrans6", Kind: KindPDHRate})
		// Connections Reset is a CUMULATIVE counter (live-verified), not a
		// /sec counter: KindRate makes the engine emit the per-second delta
		// with a counter-reset guard, never the raw uptime-climbing total.
		case `\TCPv4\Connections Reset`:
			specs = append(specs, CounterSpec{Path: p, Field: "reset4", Kind: KindRate})
		case `\TCPv6\Connections Reset`:
			specs = append(specs, CounterSpec{Path: p, Field: "reset6", Kind: KindRate})
		case `\TCPv4\Connection Failures`:
			specs = append(specs, CounterSpec{Path: p, Field: "fail4", Kind: KindRate})
		case `\TCPv6\Connection Failures`:
			specs = append(specs, CounterSpec{Path: p, Field: "fail6", Kind: KindRate})
		default:
			if !strings.HasPrefix(p, tcpQueuePrefix) {
				continue
			}
			switch {
			case strings.HasSuffix(p, tcpQueueSuffix):
				g.queueSeen++
			case strings.HasSuffix(p, tcpDiscSuffix):
				g.discSeen++
			case strings.HasSuffix(p, tcpOutErrSuffix):
				g.outErrSeen++
			}
			switch {
			case strings.HasSuffix(p, tcpQueueSuffix) && len(g.queueKeys) < tcpMaxInterfaces:
				specs = append(specs, CounterSpec{Path: p, Field: p, Kind: KindGauge})
				g.queueKeys = append(g.queueKeys, p)
			// Discarded and outbound-error packets are cumulative counts: the
			// engine turns them into a per-second delta with a reset guard.
			case strings.HasSuffix(p, tcpDiscSuffix) && len(g.discKeys) < tcpMaxInterfaces:
				specs = append(specs, CounterSpec{Path: p, Field: p, Kind: KindRate})
				g.discKeys = append(g.discKeys, p)
			case strings.HasSuffix(p, tcpOutErrSuffix) && len(g.outErrKeys) < tcpMaxInterfaces:
				specs = append(specs, CounterSpec{Path: p, Field: p, Kind: KindRate})
				g.outErrKeys = append(g.outErrKeys, p)
			}
		}
	}
	// Interface names and the TCPv4/v6 objects are stable identities: keep their
	// baselines when an unrelated process changes.
	g.eng = reconcileEngine(g.eng, specs, g.cores)
}

// upgrade swaps in a source that also expands the Network Interface wildcard.
// That first expansion is slow (about 1.5 s), so it runs in the background and
// never on the tick path: the first Collect starts it, and a later Collect swaps
// the new source in once it is ready. Until then the rate-only source answers, so
// the group is never slow because of the expansion (PERF-16). One attempt only:
// on failure the rate-only source is kept and OutboundQueue stays nil. Injected
// sources are never swapped. The fresh engine re-gates the rates.
func (g *tcpGroup) upgrade() {
	if !g.owned {
		return
	}
	if g.warm == nil {
		if g.upgraded {
			return
		}
		g.upgraded = true
		ch := make(chan CounterSource, 1)
		g.warm = ch
		open := g.openWild
		go func() {
			p, err := open(tcpPaths())
			if err != nil {
				p = nil
			}
			ch <- p
		}()
		return
	}
	select {
	case p := <-g.warm:
		g.warm = nil
		if p == nil {
			return
		}
		old := g.src
		g.src = p
		g.rebuild(tcpPathsOf(p))
		_ = old.Close()
	default:
	}
}

func (g *tcpGroup) Collect(now time.Time, _ float64) (any, error) {
	g.upgrade()
	// Re-expand wildcards on a slow timer (they follow network adapters, not
	// processes), never per tick (PERF-06). A refresh failure keeps the previous expansion and must NOT suppress the
	// independent rate and state reads below (W6-04): the PDH wildcard side and
	// the GetTcpTable2 side fail on their own.
	if g.refresh.due(now) {
		if r, ok := g.src.(interface{ RefreshInstances() error }); ok {
			if err := r.RefreshInstances(); err == nil {
				g.rebuild(tcpPathsOf(g.src))
			}
		} else {
			g.rebuild(tcpPathsOf(g.src))
		}
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
		// Connection failures: sum whichever families are present.
		fail, nf := 0.0, 0
		for _, k := range [...]string{"fail4", "fail6"} {
			if v, ok := out[k]; ok {
				fail += v
				nf++
			}
		}
		if nf > 0 {
			grp.ConnFailuresPerSec, have = &fail, true
		}
		// Adapters past the cap, or with no valid reading, make the sum partial,
		// never a complete machine-wide total (BL-138).
		if v, n := sumKeys(out, g.discKeys); n > 0 {
			grp.DiscardedPerSec, have = &v, true
			grp.DiscardedInterfaces, grp.DiscardedPartial = n, n < g.discSeen
		}
		if v, n := sumKeys(out, g.outErrKeys); n > 0 {
			grp.OutErrorsPerSec, have = &v, true
			grp.OutErrorsInterfaces, grp.OutErrorsPartial = n, n < g.outErrSeen
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
			grp.OutboundQueueInterfaces = n
			// A total over only some interfaces is partial, never a complete
			// machine-wide reading: one valid zero plus one unreadable
			// interface must not publish a false complete 0 (W6-04). Adapters past
			// the cap count as not covered too (BL-138).
			if n < g.queueSeen {
				grp.OutboundQueuePartial = true
			}
		}
	}
	// A failed state query leaves States nil; the rates above are kept. An
	// incomplete table is labeled truncated, never an unlabeled complete count.
	if res, err := g.counts(); err == nil {
		if len(res.counts) > 0 {
			grp.States, have = res.counts, true
		}
		if res.truncated {
			grp.StatesTruncated, have = true, true
		}
	}
	if !have {
		return nil, nil // both sides failed: group omitted, Missing via collector
	}
	return &grp, nil
}

// sumKeys adds the readings of the given fields and says how many were read.
func sumKeys(out map[string]float64, keys []string) (float64, int) {
	var sum float64
	n := 0
	for _, k := range keys {
		if v, ok := out[k]; ok {
			sum += v
			n++
		}
	}
	return sum, n
}

// Close releases the query handle only when the factory created the source.
func (g *tcpGroup) Close() error {
	if g.owned {
		if g.warm != nil {
			// The background expansion is still running: whoever finishes it
			// closes the source it built.
			ch := g.warm
			g.warm = nil
			go func() {
				if p := <-ch; p != nil {
					_ = p.Close()
				}
			}()
		}
		return g.src.Close()
	}
	return nil
}

// tcpTableSnapshot reads the table through injected seams, enforcing the byte
// ceiling BEFORE any allocation and bounding the retries when the table grows
// (W6-03). It is shared so the allocation bound is testable off Windows. sizeOf
// returns the native byte size; readInto fills buf and returns the bytes written
// (errTCPTableGrew when the buffer was too small).
func tcpTableSnapshot(sizeOf func() (uint32, error), readInto func([]byte) (uint32, error)) (tcpStates, error) {
	for attempt := 0; attempt < tcpTableMaxAttempts; attempt++ {
		size, err := sizeOf()
		if err != nil {
			return tcpStates{}, err
		}
		if size < 4 {
			return tcpStates{}, nil // empty table
		}
		if size > maxTCPTableBytes {
			// Refuse before allocating: a complete table cannot be read within
			// the ceiling, so report incomplete rather than unlabeled counts.
			return tcpStates{truncated: true}, nil
		}
		buf := make([]byte, size)
		n, err := readInto(buf)
		if err != nil {
			if errors.Is(err, errTCPTableGrew) {
				continue // table outgrew the size query: bounded retry
			}
			return tcpStates{}, err
		}
		if int(n) > len(buf) {
			n = uint32(len(buf))
		}
		counts, truncated := tcpStateCountsFromTable(buf[:n])
		return tcpStates{counts: counts, truncated: truncated}, nil
	}
	return tcpStates{truncated: true}, nil // kept growing past the retry bound
}

// tcpStateCountsFromTable counts MIB_TCPTABLE2 rows by dwState only. The
// address/port/PID bytes are never read; the scan is capped at maxTCPStateRows
// and a truncated buffer counts complete rows only. States come out sorted.
// The second return reports truncation: the claimed row count exceeded the scan
// cap or the complete rows that fit the buffer (W6-03).
func tcpStateCountsFromTable(buf []byte) ([]TCPStateCount, bool) {
	if len(buf) < 4 {
		return nil, false
	}
	n := int(binary.LittleEndian.Uint32(buf))
	truncated := false
	if n > maxTCPStateRows {
		n = maxTCPStateRows
		truncated = true
	}
	if fit := (len(buf) - 4) / tcpRowStride; n > fit {
		n = fit
		truncated = true
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
	return out, truncated
}
