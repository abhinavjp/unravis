package importertest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
	"api-audit-explorer/internal/importer/textio"
)

// toy is a minimal, correct importer over "time,host,note,ms" CSV. It shows
// the shape every real importer takes and proves the harness accepts one.
//
// The two flags make strictly typed variants: numericNote rejects a row whose
// note is not an integer, and rejectTrigger rejects a note that starts with a
// formula trigger (the strictness a TSV or typed-column importer shows).
type toy struct {
	numericNote   bool
	rejectTrigger bool
}

func (toy) Name() string { return "toy-csv" }

func (toy) Detect(head []byte, _ string) int {
	if bytes.HasPrefix(bytes.TrimPrefix(head, []byte{0xEF, 0xBB, 0xBF}), []byte("time,host,note,ms")) {
		return 90
	}
	return 0
}

func (toy) AllowedAttrs() map[string]bool { return map[string]bool{"note": true} }
func (toy) DefaultLimits() importer.Limits {
	return importer.NewLimits(1<<20, 10000)
}

func (t toy) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	rep := importer.Report{Importer: t.Name()}
	rep.Rule("time", clock.RuleUTCField)
	cr, _, err := textio.NewCSV(r, textio.CSVOptions{
		RequireHeader: []string{"time", "host", "note", "ms"},
		MaxLine:       opts.Limits.MaxLineBytes,
		MaxFields:     opts.Limits.MaxFields,
	})
	if err != nil {
		return rep, err
	}
	for {
		if rep.Rows%64 == 0 {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
		}
		rec, line, err := cr.Next()
		var re importer.RowError
		switch {
		case errors.Is(err, io.EOF):
			return rep, nil
		case errors.As(err, &re):
			rep.Rows++
			rep.Reject(re.Line, re.Reason)
			continue
		case err != nil:
			return rep, err
		}
		rep.Rows++
		if err := opts.Limits.CheckRows(rep.Rows); err != nil {
			return rep, err
		}
		ts, terr := time.Parse(time.RFC3339, rec[cr.Index("time")])
		ms, merr := strconv.ParseFloat(rec[cr.Index("ms")], 64)
		switch {
		case terr != nil:
			rep.Reject(line, importer.ReasonBadTime)
			continue
		case merr != nil:
			rep.Reject(line, importer.ReasonBadNumber)
			continue
		}
		note := rec[cr.Index("note")]
		if t.numericNote {
			if _, err := strconv.Atoi(note); err != nil {
				rep.Reject(line, importer.ReasonBadNumber)
				continue
			}
		}
		if t.rejectTrigger && note != "" && strings.ContainsRune("=+-@\t\r", rune(note[0])) {
			rep.Reject(line, importer.ReasonBadNumber)
			continue
		}
		ts = ts.UTC()
		// The raw host name is reported, never stored: Event.Host is the
		// label the owner picked or mapped.
		raw := rec[cr.Index("host")]
		if err := rep.Observe(raw); err != nil {
			return rep, err
		}
		var label string
		switch {
		case opts.Host != nil:
			label = opts.Host.Label
		case opts.Hosts != nil:
			h, ok := opts.Hosts(raw)
			if !ok {
				return rep, &importer.QuarantineError{Reason: importer.ReasonUnknownHost}
			}
			label = h.Label
		}
		ev := evidence.Event{
			EvidenceID: evidence.EventID(opts.CaseID, t.Name(), opts.ImportNonce, line, 0),
			Source:     t.Name(),
			Kind:       "toy",
			Host:       label,
			Start:      ts,
			End:        ts.Add(time.Duration(ms * float64(time.Millisecond))),
			DurationMs: &ms,
			Unit:       "ms",
			Line:       line,
			TZRule:     clock.RuleUTCField,
			Attrs:      map[string]string{"note": note},
		}
		if err := emit(ev); err != nil {
			return rep, err
		}
		rep.Emitted++
		rep.Cover(ev.Start)
		rep.Cover(ev.End)
	}
}

const good = "time,host,note,ms\r\n" +
	"2001-01-01T00:00:00Z,web-a,first row,12.5\r\n" +
	"2001-01-01T00:00:01Z,web-a,\"second, quoted\",30\r\n" +
	"2001-01-01T00:00:02Z,web-b,third row,7\r\n"

const bad = "time,host,note,ms\n" +
	"2001-01-01T00:00:00Z,web-a,fine,1\n" +
	"SENTINEL-7f3a,web-a,SENTINEL-7f3a,SENTINEL-7f3a\n" +
	"2001-01-01T00:00:02Z,web-a,fine,SENTINEL-7f3a\n" +
	"2001-01-01T00:00:03Z,SENTINEL-7f3a\n"

const formula = "time,host,note,ms\n" +
	"2001-01-01T00:00:00Z,web-a,=1+1,1\n" +
	"2001-01-01T00:00:01Z,web-a,+cmd,1\n" +
	"2001-01-01T00:00:02Z,web-a,-2,1\n" +
	"2001-01-01T00:00:03Z,web-a,@SUM(A1),1\n" +
	"2001-01-01T00:00:04Z,web-a,\"\tTAB\",1\n" +
	"2001-01-01T00:00:05Z,web-a,\"\rCR\",1\n"

func fixtures() importertest.Fixtures {
	return importertest.Fixtures{
		Good:    []byte(good),
		Bad:     []byte(bad),
		Formula: []byte(formula),
		Options: importer.Options{Hosts: func(raw string) (importer.HostHint, bool) {
			switch raw {
			case "web-a":
				return importer.HostHint{Label: "host-1", Role: "iis"}, true
			case "web-b":
				return importer.HostHint{Label: "host-2", Role: "iis"}, true
			}
			return importer.HostHint{}, false
		}},
	}
}

func TestToyPassesConformance(t *testing.T) {
	importertest.Run(t, toy{}, fixtures())
}

// A typed-attribute importer (numeric codes) rejects =404 and +500 rows. That
// is correct, and NoFreeText lets it say so.
func TestNumericAttrsWithNoFreeText(t *testing.T) {
	fx := fixtures()
	fx.Good = []byte("time,host,note,ms\n" +
		"2001-01-01T00:00:00Z,web-a,404,12.5\n" +
		"2001-01-01T00:00:01Z,web-a,500,30\n" +
		"2001-01-01T00:00:02Z,web-b,200,7\n")
	fx.Formula = nil
	fx.NoFreeText = true
	importertest.Run(t, toy{numericNote: true}, fx)
}

// Without NoFreeText, an importer that rejects trigger-prefixed cells is
// still fine: the harness sees rejected rows and does not call that a dropped
// leading character.
func TestRejectingTriggersDoesNotFalseFail(t *testing.T) {
	fx := fixtures()
	fx.Formula = nil
	importertest.Run(t, toy{rejectTrigger: true}, fx)
}

// keptCellFixtures has the sentinel in a note cell the toy keeps verbatim and
// in a host cell it reports as an observed raw name. With KeepsCellValues,
// FuzzParse's seed corpus (which includes this Bad file) must pass: neither is
// a leak. Seeds run under plain "go test".
func keptCellFixtures() importertest.Fixtures {
	fx := fixtures()
	fx.Bad = []byte("time,host,note,ms\n" +
		"2001-01-01T00:00:00Z,web-a," + importertest.Sentinel + ",1\n" +
		"2001-01-01T00:00:01Z," + importertest.Sentinel + ",kept,2\n")
	fx.Options.Hosts = func(string) (importer.HostHint, bool) {
		return importer.HostHint{Label: "host-1", Role: "iis"}, true
	}
	fx.KeepsCellValues = true
	return fx
}

func FuzzToyKeepsCells(f *testing.F) {
	importertest.FuzzParse(f, toy{}, keptCellFixtures())
}

func FuzzToyParse(f *testing.F) {
	importertest.FuzzParse(f, toy{}, fixtures())
}
