package applog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const (
	testCase   = "case7f3a0001"
	testSrc    = "mel-json"
	sentinel   = "SENTINEL-7f3a"
	removedVal = "[removed]"
)

var t0 = time.Date(2001, 10, 25, 1, 2, 3, 0, time.UTC)

// hashPS stands in for the case pseudonymiser: kind-<8 hex> of a hash, so
// the input never shows in the output.
func hashPS(kind, v string) string {
	h := sha256.Sum256([]byte(kind + "|" + v))
	return kind + "-" + hex.EncodeToString(h[:4])
}

func testOpts() importer.Options {
	return importer.Options{CaseID: testCase, ImportNonce: "nonce-0001", Host: &importer.HostHint{Label: "web-a"},
		Pseudonymize: hashPS}
}

type result struct {
	evs []evidence.Event
	rep importer.Report
}

// runSink adds recs at lines 1..n and finishes. Every emitted event must pass
// ValidateFor with the core's attribute set.
func runSink(t testing.TB, opts importer.Options, recs []Record) (result, error) {
	t.Helper()
	var res result
	s := NewSink(testSrc, opts, func(ev evidence.Event) error {
		if err := ev.ValidateFor(testCase, testSrc, AllowedAttrs()); err != nil {
			t.Fatalf("invalid event %+v: %v", ev, err)
		}
		res.evs = append(res.evs, ev)
		return nil
	})
	for i, r := range recs {
		if err := s.Add(r, int64(i+1)); err != nil {
			return res, err
		}
	}
	err := s.Finish(&res.rep)
	return res, err
}

func mustRun(t testing.TB, opts importer.Options, recs []Record) result {
	t.Helper()
	res, err := runSink(t, opts, recs)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func rec(level, cat, msg string) Record {
	return Record{Time: t0, Rule: clock.RuleUTCField, Level: level, Category: cat, Message: msg}
}

func byKind(evs []evidence.Event, kind string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func hasGap(rep importer.Report, g string) bool {
	for _, k := range rep.KnownGaps {
		if k == g {
			return true
		}
	}
	return false
}

// exportAttrs runs one event through the case export and returns the
// exported attributes.
func exportAttrs(t testing.TB, ev evidence.Event) map[string]string {
	t.Helper()
	exp, err := evidence.BuildCase(evidence.CaseInput{Events: []evidence.Event{ev}, Now: t0,
		Key: []byte("applog-test-key-0000000000000000")})
	if err != nil {
		t.Fatalf("BuildCase: %v", err)
	}
	if len(exp.Body.Events) != 1 {
		t.Fatalf("export has %d events", len(exp.Body.Events))
	}
	return exp.Body.Events[0].Attrs
}

// attrEvent is a log-entry carrying attrs, for export cross-checks.
func attrEvent(line int64, attrs map[string]string) evidence.Event {
	return evidence.Event{EvidenceID: evidence.EventID(testCase, testSrc, "n", line, 0), Source: testSrc,
		Kind: KindLogEntry, Start: t0, End: t0, Line: line, TZRule: clock.RuleUTCField, Attrs: attrs}
}

func dump(t testing.TB, v ...any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
