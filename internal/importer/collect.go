package importer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"api-audit-explorer/internal/evidence"
)

// mergeLimits fills every zero field of l from the importer's own defaults,
// then applies the global defaults.
func mergeLimits(l, def Limits) Limits {
	if l.MaxBytes == 0 {
		l.MaxBytes = def.MaxBytes
	}
	if l.MaxRows == 0 {
		l.MaxRows = def.MaxRows
	}
	if l.MaxLineBytes == 0 {
		l.MaxLineBytes = def.MaxLineBytes
	}
	if l.MaxFields == 0 {
		l.MaxFields = def.MaxFields
	}
	if l.MaxRejectFraction == 0 {
		l.MaxRejectFraction = def.MaxRejectFraction
	}
	return l.WithDefaults()
}

// Prepare is the shared front half of an import: it fills opts.Limits from the
// importer's defaults, caps the byte count, peeks the head and refuses
// archives. The returned reader must be used for Parse; it still yields the
// head bytes. The service uses it before tee-ing the upload through SHA-256.
func Prepare(imp Importer, r io.Reader, opts Options) (io.Reader, Options, error) {
	opts.Limits = mergeLimits(opts.Limits, imp.DefaultLimits())
	br := bufio.NewReaderSize(LimitReader(r, opts.Limits.MaxBytes), HeadSize)
	head, err := br.Peek(HeadSize)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, opts, err
	}
	if err := Sniff(head); err != nil {
		return nil, opts, err
	}
	return br, opts, nil
}

// Finish applies the end-of-parse rules every importer shares: the row limit
// and the reject fraction. A non-nil result refuses the whole file.
func Finish(rep Report, lim Limits) error {
	if err := lim.CheckRows(rep.Rows); err != nil {
		return err
	}
	return lim.CheckRejects(rep.Rows, rep.Rejected)
}

// Collect runs an importer over r and returns every event it emitted. It is
// the test and preview path: Prepare (limits, head sniff), Parse with each
// event validated against the importer's allowlist and copied, then Finish.
// On any error it returns no events, which is the in-memory form of rolling
// back. An error returned by emit (a refused event) fails the run even when
// Parse swallows it and returns nil. The Report is returned even on error, for counts and reasons.
func Collect(ctx context.Context, imp Importer, r io.Reader, opts Options) ([]evidence.Event, Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, Report{Importer: imp.Name()}, err
	}
	body, opts, err := Prepare(imp, r, opts)
	if err != nil {
		return nil, Report{Importer: imp.Name()}, err
	}
	allowed := imp.AllowedAttrs()
	var events []evidence.Event
	// first is the first error emit returned. An importer that drops an emit
	// error and finishes "ok" must not produce a partial import that looks
	// complete, so a recorded emit error fails the whole run, and emit keeps
	// refusing after it.
	var first error
	emit := func(ev evidence.Event) error {
		if first != nil {
			return first
		}
		if err := ctx.Err(); err != nil {
			first = err
			return err
		}
		if err := ev.ValidateFor(opts.CaseID, imp.Name(), allowed); err != nil {
			first = fmt.Errorf("importer %s emitted an invalid event: %w", imp.Name(), err)
			return first
		}
		events = append(events, ev.Clone())
		return nil
	}
	rep, err := imp.Parse(ctx, body, opts, emit)
	if rep.Importer == "" {
		rep.Importer = imp.Name()
	}
	if err == nil && first != nil {
		err = first
	}
	if err == nil {
		err = Finish(rep, opts.Limits)
	}
	if err != nil {
		return nil, rep, err
	}
	return events, rep, nil
}
