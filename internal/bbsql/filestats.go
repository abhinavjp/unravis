// filestats.go holds the two 60 s samples of BL-119: per-file I/O stall deltas
// and, only on an instance with availability groups enabled, the AG replica
// state. Both are ids and numbers: no paths, no database names, no replica
// server names. Samplers are not safe for concurrent use and never are
// concurrent: the runner serialises every tick.
package bbsql

import "time"

// fileKey identifies one database file across ticks.
type fileKey struct {
	databaseID uint32
	fileID     uint32
}

// filestatsCumulative is the last cumulative sighting of a file's counters.
type filestatsCumulative struct {
	readStallMs  uint64
	writeStallMs uint64
	reads        uint64
	writes       uint64
}

type filestatsSampler struct {
	prev map[fileKey]filestatsCumulative
}

func (filestatsSampler) ID() string              { return "bb:filestats" }
func (filestatsSampler) Kind() string            { return "filestats" }
func (filestatsSampler) Interval() time.Duration { return DemotedInterval }

// Decode emits the delta since the previous sighting of the same file. The
// first cycle only records the baseline: a delta with nothing to subtract from
// would be a lie. A counter that went backwards is a restart or a role change,
// so that file's baseline is replaced and the file is skipped for this cycle.
func (s *filestatsSampler) Decode(rows []Row) (any, bool, error) {
	var cur []FileStats
	truncated, err := decodeRows(rows, &cur, CapFileStats)
	if err != nil {
		return nil, false, err
	}
	if len(cur) == 0 {
		// Nothing seen: every baseline is stale, and an empty reply is not a
		// sample.
		s.prev = map[fileKey]filestatsCumulative{}
		return nil, false, nil
	}

	nextPrev := make(map[fileKey]filestatsCumulative, len(cur))
	firstCycle := s.prev == nil
	out := make([]FileStats, 0, len(cur))
	for _, r := range cur {
		k := fileKey{databaseID: r.DatabaseID, fileID: r.FileID}
		cum := filestatsCumulative{
			readStallMs:  r.ReadStallMs,
			writeStallMs: r.WriteStallMs,
			reads:        r.Reads,
			writes:       r.Writes,
		}
		nextPrev[k] = cum
		if firstCycle {
			continue
		}
		prev, ok := s.prev[k]
		if !ok {
			// New file: no baseline yet.
			continue
		}
		if cum.readStallMs < prev.readStallMs || cum.writeStallMs < prev.writeStallMs ||
			cum.reads < prev.reads || cum.writes < prev.writes {
			continue // counters reset: baseline replaced, nothing emitted
		}
		dReadStall := cum.readStallMs - prev.readStallMs
		dWriteStall := cum.writeStallMs - prev.writeStallMs
		dReads := cum.reads - prev.reads
		dWrites := cum.writes - prev.writes
		if dReadStall == 0 && dWriteStall == 0 && dReads == 0 && dWrites == 0 {
			// An idle file carries no information in a stall-delta series and
			// costs payload: the caps make payload the scarce resource here.
			continue
		}
		out = append(out, FileStats{
			DatabaseID:   r.DatabaseID,
			FileID:       r.FileID,
			ReadStallMs:  dReadStall,
			WriteStallMs: dWriteStall,
			Reads:        dReads,
			Writes:       dWrites,
			// Size on disk is a gauge, not a counter: the current value, never a
			// difference.
			SizeOnDiskBytes: r.SizeOnDiskBytes,
		})
	}

	s.prev = nextPrev
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

// hadrFlag is the decode target for the bb:hadr gate row.
type hadrFlag struct {
	Hadr int64 `json:"hadr"`
}

type agSampler struct{}

func (agSampler) ID() string              { return "bb:ag" }
func (agSampler) Kind() string            { return "ag" }
func (agSampler) Interval() time.Duration { return DemotedInterval }
func (agSampler) GateID() string          { return "bb:hadr" }

// GateOpen fails closed for anything it cannot read: no rows, a NULL answer
// (an edition or platform without availability groups), an unreadable value or
// an unexpected column. So a Standard edition instance produces no ag payload
// and never has an availability-group DMV queried.
func (agSampler) GateOpen(rows []Row) bool {
	var out []hadrFlag
	truncated, err := decodeRows(rows, &out, 1)
	if err != nil || truncated || len(out) != 1 {
		return false
	}
	return out[0].Hadr == 1
}

func (agSampler) Decode(rows []Row) (any, bool, error) {
	var out []AG
	truncated, err := decodeRows(rows, &out, CapAG)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

func init() {
	RegisterSampler(&filestatsSampler{})
	RegisterSampler(&agSampler{})
}
