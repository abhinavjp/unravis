package importertest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
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
		"accepts-empty-file": broken{parse: func(ctx context.Context, r io.Reader, o importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
			b, _ := io.ReadAll(r)
			if len(b) <= 1 {
				return importer.Report{Importer: "toy-csv"}, nil
			}
			return base.Parse(ctx, bytes.NewReader(b), o, emit)
		}},
	}
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
		})
	}
}
