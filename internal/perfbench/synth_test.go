package perfbench

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sealedfile"
)

// Synthetic counter cases built straight through the case store (no CSV and no
// HTTP), so the D1/D2 query tests and benchmarks can build a 1.14M-event case
// in about a minute and a mid-size one in a few seconds. The shape is the one
// the import benchmark uses: pdhCols() counters of host web-a on a 15 s cadence
// from 2001-10-24 00:00:00 UTC, about 1 percent of the cells missing. All data
// is synthetic.

var synthStart = time.Date(2001, 10, 24, 0, 0, 0, 0, time.UTC)

type synthCol struct{ object, instance, counter, unit string }

// synthCols parses pdhCols() paths of the form \\WEB-A\Object(instance)\counter.
func synthCols() []synthCol {
	var out []synthCol
	for _, p := range pdhCols() {
		parts := strings.SplitN(strings.TrimPrefix(p, `\\WEB-A\`), `\`, 2)
		obj, ctr := parts[0], parts[1]
		inst := ""
		if i := strings.IndexByte(obj, '('); i >= 0 {
			inst, obj = strings.TrimSuffix(obj[i+1:], ")"), obj[:i]
		}
		unit := "count"
		switch {
		case strings.Contains(ctr, "% Processor"):
			unit = "percent"
		case strings.Contains(ctr, "Bytes") || ctr == "Working Set":
			unit = "bytes"
		}
		out = append(out, synthCol{object: obj, instance: inst, counter: ctr, unit: unit})
	}
	return out
}

// synthRun is a RunFunc that emits the synthetic counter events.
func synthRun(caseID string, spanSec, intervalSec int) casestore.RunFunc {
	cols := synthCols()
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "win-counters", KnownGaps: []string{"synthetic benchmark data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		rng := rand.New(rand.NewPCG(29, 31))
		var line int64 = 1
		for s := 0; s < spanSec; s += intervalSec {
			t := synthStart.Add(time.Duration(s) * time.Second)
			line++
			rep.Rows++
			for i, c := range cols {
				if rng.IntN(100) == 0 {
					continue
				}
				var v float64
				switch c.unit {
				case "percent":
					v = float64(int(rng.Float64()*10000)) / 100
				default:
					v = float64(rng.IntN(1 << 30))
				}
				attrs := map[string]string{"object": c.object, "counter": c.counter}
				if c.instance != "" {
					attrs["instance"] = c.instance
				}
				e := evidence.Event{
					EvidenceID: evidence.EventID(caseID, "win-counters", "synthetic", line, i),
					Source:     "win-counters", Kind: "counter", Host: "web-a", Start: t, End: t,
					Value: &v, Unit: c.unit, Line: line, TZRule: clock.RuleHeaderBias, Attrs: attrs,
				}
				if err := emit(e); err != nil {
					return rep, err
				}
				rep.Emitted++
			}
		}
		return rep, nil
	}
}

// openSynthStore opens a store under root. The protector is DPAPI when asked for
// (so a kept benchmark case can be reopened by a later process), else random.
func newSynthStore(t testing.TB, root string, p sealedfile.Protector) *sqlite.Store {
	t.Helper()
	s, err := sqlite.New(sqlite.Options{Root: root, Protector: p, MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// buildSynthCase creates a case in s holding the synthetic counters and returns
// it open. The caller closes it.
func buildSynthCase(t testing.TB, s *sqlite.Store, spanSec, intervalSec int) (casestore.Case, int64) {
	t.Helper()
	ctx := context.Background()
	info, err := s.Create(ctx, "synthetic counters")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	meta := casestore.ImportMeta{
		Importer: "win-counters", FileName: "synthetic.csv", HostLabel: "web-a", TZRule: clock.RuleHeaderBias,
		AckGating: true, AckText: "Synthetic benchmark data, no real host.",
	}
	res, err := c.Import(ctx, meta, bytes.NewReader([]byte("synthetic body\n")), synthRun(string(info.ID), spanSec, intervalSec))
	if err != nil {
		t.Fatal(err)
	}
	return c, res.Report.Emitted
}
