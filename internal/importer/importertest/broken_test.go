package importertest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// The harness has to fail the faults it exists to catch. Each importer below
// breaks one rule. TestHarnessCatchesBrokenImporters re-runs this test binary
// for each one (TestBrokenChild) and expects the run to fail.

type parseFunc func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error)

type broken struct {
	toy
	parse parseFunc
}

func (b broken) Parse(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	return b.parse(ctx, r, o, emit)
}

// eofOnError reports every read error, including a byte-limit breach, as EOF.
type eofOnError struct{ r io.Reader }

func (e eofOnError) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && err != io.EOF {
		err = io.EOF
	}
	return n, err
}

func brokenImporters() map[string]importer.Importer {
	var base toy
	return map[string]importer.Importer{
		"leaks-cell": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			rep, err := base.Parse(ctx, r, o, emit)
			if rep.Rejected > 0 {
				return rep, errors.New("bad cell: " + importertest.Sentinel)
			}
			return rep, err
		}},
		"ignores-row-limit": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			o.Limits.MaxRows = 0
			return base.Parse(ctx, r, o, emit)
		}},
		"ignores-context": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(context.Background(), r, o, func(ev evidence.Event) error {
				if err := emit(ev); err != nil && ctx.Err() != nil {
					return nil // swallow the cancellation
				}
				return nil
			})
		}},
		"panics-on-tiny-file": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			b, _ := io.ReadAll(r)
			if len(b) < 5 {
				var s []byte
				_ = s[len(b)]
			}
			return base.Parse(ctx, bytes.NewReader(b), o, emit)
		}},
		"escapes-formula-cells": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, r, o, func(ev evidence.Event) error {
				if v := ev.Attrs["note"]; v != "" && strings.ContainsAny(v[:1], "=+-@\t\r") {
					ev.Attrs["note"] = "'" + v
				}
				return emit(ev)
			})
		}},
		"emits-non-utc": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, r, o, func(ev evidence.Event) error {
				ev.Start = ev.Start.In(time.FixedZone("x", 3600))
				return emit(ev)
			})
		}},
		// Read errors and limit breaches become a clean end of file, so a
		// cut-off or oversized file imports "ok" with fewer events.
		"swallows-read-errors": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, eofOnError{r}, o, emit)
		}},
		// The error from emit is dropped and parsing carries on.
		"ignores-emit-error": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, r, o, func(ev evidence.Event) error {
				_ = emit(ev)
				return nil
			})
		}},
		// The cell's own host name is stored in Event.Host.
		"raw-host": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			o.Host = nil
			o.Hosts = func(raw string) (importer.HostHint, bool) { return importer.HostHint{Label: raw}, true }
			return base.Parse(ctx, r, o, emit)
		}},
		"bad-event-id": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, r, o, func(ev evidence.Event) error {
				ev.EvidenceID = "row-" + ev.EvidenceID[len(ev.EvidenceID)-16:]
				return emit(ev)
			})
		}},
		"line-zero": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, r, o, func(ev evidence.Event) error {
				ev.Line = 0
				return emit(ev)
			})
		}},
		"no-tz-rule": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, r, o, func(ev evidence.Event) error {
				ev.TZRule = ""
				return emit(ev)
			})
		}},
		"trims-formula-cells": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(ctx, r, o, func(ev evidence.Event) error {
				ev.Attrs["note"] = strings.TrimLeft(ev.Attrs["note"], "=+-@ \t\r")
				return emit(ev)
			})
		}},
		// Parse never looks at its context. Under Collect the cancellation still
		// stops it, because Collect's emit fails; called directly it runs on.
		"ctx-only-via-emit": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			return base.Parse(context.Background(), r, o, emit)
		}},
		"calls-network": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			if resp, err := http.Get("http://127.0.0.1:1/"); err == nil {
				resp.Body.Close()
			}
			return base.Parse(ctx, r, o, emit)
		}},
		"accepts-empty-file": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			b, _ := io.ReadAll(r)
			if len(b) <= 1 {
				return importer.Report{Importer: "toy-csv"}, nil
			}
			return base.Parse(ctx, bytes.NewReader(b), o, emit)
		}},
	}
}

// wantFail names the conformance check that must catch each fault, so a broken
// importer cannot pass for the wrong reason (failing early in good-fixture, say,
// and leaving the check meant for it untested).
var wantFail = map[string]string{
	"leaks-cell":            "sentinel-never-leaks",
	"ignores-row-limit":     "limit-rows",
	"ignores-context":       "context-cancel",
	"panics-on-tiny-file":   "truncated-and-tiny-files",
	"escapes-formula-cells": "formula-cells-verbatim",
	"emits-non-utc":         "good-fixture",
	"accepts-empty-file":    "truncated-and-tiny-files",
	"swallows-read-errors":  "read-errors-surface",
	"ignores-emit-error":    "emit-errors-surface",
	"raw-host":              "hosts-are-labels",
	"bad-event-id":          "event-ids",
	"line-zero":             "event-ids",
	"no-tz-rule":            "event-ids",
	"trims-formula-cells":   "formula-cells-verbatim",
	"ctx-only-via-emit":     "context-cancel-in-parse",
	"calls-network":         "no-network",
}

const childEnv = "IMPORTERTEST_BROKEN_CASE"

func TestBrokenChild(t *testing.T) {
	name := os.Getenv(childEnv)
	if name == "" {
		t.Skip("only run as a child of TestHarnessCatchesBrokenImporters")
	}
	imp, ok := brokenImporters()[name]
	if !ok {
		t.Fatalf("no broken importer %q", name)
	}
	importertest.Run(t, imp, fixtures())
}

func TestHarnessCatchesBrokenImporters(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns the test binary")
	}
	for name := range brokenImporters() {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(os.Args[0], "-test.run=^TestBrokenChild$", "-test.v")
			cmd.Env = append(os.Environ(), childEnv+"="+name)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("harness accepted an importer with fault %q\n%s", name, out)
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) || !bytes.Contains(out, []byte("--- FAIL")) {
				t.Fatalf("child did not fail in a test: %v\n%s", err, out)
			}
			sub, ok := wantFail[name]
			if !ok {
				t.Fatalf("broken importer %q has no entry in wantFail", name)
			}
			if !bytes.Contains(out, []byte("--- FAIL: TestBrokenChild/"+sub)) {
				t.Fatalf("fault %q was not caught by %q\n%s", name, sub, out)
			}
		})
	}
}
