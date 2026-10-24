package redis

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

// capLimits makes the importer defaults ceilings: a caller limit can lower them and never
// raise them. The reject fraction is a policy knob, so the caller's value is honoured.
func capLimits(in, d importer.Limits) importer.Limits {
	out := d
	if in.MaxBytes > 0 && in.MaxBytes < d.MaxBytes {
		out.MaxBytes = in.MaxBytes
	}
	if in.MaxRows > 0 && in.MaxRows < d.MaxRows {
		out.MaxRows = in.MaxRows
	}
	if in.MaxLineBytes > 0 && in.MaxLineBytes < d.MaxLineBytes {
		out.MaxLineBytes = in.MaxLineBytes
	}
	if in.MaxFields > 0 && in.MaxFields < d.MaxFields {
		out.MaxFields = in.MaxFields
	}
	if in.MaxEvents > 0 && (d.MaxEvents <= 0 || in.MaxEvents < d.MaxEvents) {
		out.MaxEvents = in.MaxEvents
	}
	if in.MaxRejectFraction > 0 {
		out.MaxRejectFraction = in.MaxRejectFraction
	}
	return out
}

func copyAttrs(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// hostLabel returns the label of the host the user picked for the file. Every file here
// comes from one Redis server, so the file itself never names a host.
func hostLabel(opts importer.Options) (string, error) {
	if opts.Host == nil || opts.Host.Label == "" {
		return "", importer.ErrUnknownHost
	}
	return opts.Host.Label, nil
}

// readLines feeds every line of r to feed. It enforces the byte and line caps, strict
// UTF-8 (a bad line is one rejected row) and the context. feed counts rows itself.
func readLines(ctx context.Context, r io.Reader, lim importer.Limits, rep *importer.Report, feed func(line []byte, no int64) error) error {
	br := bufio.NewReaderSize(importer.LimitReader(r, lim.MaxBytes), 64*1024)
	head, perr := br.Peek(16)
	if perr != nil && !errors.Is(perr, io.EOF) && !errors.Is(perr, bufio.ErrBufferFull) {
		return perr
	}
	if err := importer.Sniff(head); err != nil {
		return err
	}
	lr, _, err := textio.NewLineReader(br, lim.MaxLineBytes)
	if err != nil {
		return err
	}
	for n := 0; ; n++ {
		if n&255 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		line, no, err := lr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		var re importer.RowError
		if errors.As(err, &re) {
			rep.Rows++
			if err := lim.CheckRows(rep.Rows); err != nil {
				return err
			}
			rep.Reject(no, re.Reason)
			continue
		}
		if err != nil {
			return err
		}
		if err := feed(line, no); err != nil {
			return err
		}
	}
}

// finishRead ends a parse: at least one row, and few enough rejects.
func finishRead(rep *importer.Report, lim importer.Limits) error {
	if rep.Rows == 0 {
		return &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	return lim.CheckRejects(rep.Rows, rep.Rejected)
}

// sink validates and emits events.
type sink struct {
	opts    importer.Options
	rep     *importer.Report
	emit    func(evidence.Event) error
	allowed map[string]bool
}

// send emits ev unless it falls outside the window. bad is true when the event does not
// validate (the caller rejects the row); err is an emit failure and ends the parse.
func (k *sink) send(ev evidence.Event) (bad bool, err error) {
	if ev.Validate(k.allowed) != nil {
		return true, nil
	}
	if !k.opts.Window.From.IsZero() && ev.End.Before(k.opts.Window.From) {
		return false, nil
	}
	if !k.opts.Window.To.IsZero() && ev.Start.After(k.opts.Window.To) {
		return false, nil
	}
	if err := k.emit(ev); err != nil {
		return false, err
	}
	k.rep.Emitted++
	return false, nil
}

// markers parses the comment lines the owner's collection script writes:
//
//	# unravis-sample 2026-10-25T10:00:00Z     the time the reply was read
//	# latency-event: fork                     the event a LATENCY HISTORY block is for
func sampleTime(comment []byte) (time.Time, bool) {
	c := strings.TrimSpace(string(comment))
	if !strings.HasPrefix(c, commentSample) {
		return time.Time{}, false
	}
	c = strings.TrimSpace(c[len(commentSample):])
	if len(c) == 0 || len(c) > 64 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, c)
	if err != nil || t.Year() < 2000 || t.Year() > 2100 {
		return time.Time{}, false
	}
	return t, true
}

// tzRuleOf names the clock rule for a sample time: UTC written as Z, or an explicit offset.
func tzRuleOf(c []byte) string {
	if bytes.HasSuffix(bytes.TrimSpace(c), []byte("Z")) {
		return clock.RuleUTCField
	}
	return clock.RuleOffsetInField
}
