package sqlserver

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

// ErrorlogName is the SQL Server ERRORLOG importer.
const ErrorlogName = "sql-errorlog"

// Event kinds emitted by the ERRORLOG importer (KindError and KindNonYielding
// are shared with xe.go).
const (
	KindIOStall     = "io-stall"
	KindStart       = "start"
	KindStop        = "stop"
	KindLoginFailed = "login-failed"
)

// ERRORLOG lines are "<yyyy-mm-dd hh:mm:ss.ff> <source> <message>" in the
// server's local time. Lines without the time prefix continue the previous
// message and are counted as unclassified.
var (
	reErrlogLine  = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d{1,7})?) +(\S+) *(.*)$`)
	reErrlogDated = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} `)

	// Allowlist. Only the kind and the captured numbers are kept; the message
	// text (file paths, database, user and client names) is never stored.
	reIOStall = regexp.MustCompile(`^SQL Server has encountered (\d{1,9}) occurrence\(s\) of I/O requests taking longer than \d{1,4} seconds to complete`) // message 833
	// Message 17883, anchored at the start of the message so that text inside another
	// message (a login name, for example) cannot forge a scheduler event.
	reNonYielding = regexp.MustCompile(`^Process \d+:\d+:\d+ \(0x[0-9A-Fa-f]+\) Worker 0x[0-9A-Fa-f]+ appears to be non-yielding on Scheduler \d+`)
	reErrorSev    = regexp.MustCompile(`^Error: (\d{1,9}), Severity: (\d{1,2}), State: \d{1,3}\.?\s*$`)
	reStart       = regexp.MustCompile(`^SQL Server is starting at `)
	reStop        = regexp.MustCompile(`^(SQL Server is terminating|SQL Server shutdown )`)
	reLoginFailed = regexp.MustCompile(`^Login failed for user `) // message 18456; counted only
	reUTCAdjust   = regexp.MustCompile(`^UTC adjustment: (-)?(\d{1,2}):(\d{2})\s*$`)
	reBanner      = regexp.MustCompile(`^Microsoft SQL Server (\d{4})\b`)
	reErrlogFile  = regexp.MustCompile(`(?i)^errorlog(\.\d{1,3})?$`)
)

// errLoginFailed is SQL Server error 18456, "Login failed for user".
const errLoginFailed = 18456

var errlogAttrs = map[string]bool{"error_number": true, "sql_severity": true}

const (
	gapErrlogLocal        = "ERRORLOG times are server-local and were converted with the picked host's time zone"
	gapErrlog833          = "message 833 reports I/O stalls since its previous report; when each stall happened inside that span is not known"
	gapErrlogUnclassified = "unclassified ERRORLOG lines were counted, not imported; message text is never stored"
	gapErrlogLogin        = "failed logins are counted once each, as a count only; user and client are not kept"
	gapErrlogNoAdjust     = "no UTC adjustment line was found, so the host time zone was not cross-checked"
)

type errorlogImporter struct{}

// NewErrorlog returns the SQL Server ERRORLOG importer ("sql-errorlog").
func NewErrorlog() importer.Importer { return errorlogImporter{} }

func (errorlogImporter) Name() string { return ErrorlogName }

func (errorlogImporter) AllowedAttrs() map[string]bool {
	out := make(map[string]bool, len(errlogAttrs))
	for k := range errlogAttrs {
		out[k] = true
	}
	return out
}

func (errorlogImporter) DefaultLimits() importer.Limits {
	return importer.Limits{MaxBytes: 256 << 20, MaxRows: 2_000_000, MaxLineBytes: 64 << 10, MaxFields: 4096, MaxRejectFraction: 0.05}
}

// Detect scores the ERRORLOG file name and a head of time-prefixed lines.
func (errorlogImporter) Detect(head []byte, fileName string) int {
	score := 0
	if reErrlogFile.MatchString(fileName) {
		score += 50
	}
	// The first line is time-prefixed; the next ones are too, or are indented
	// continuations of a message (the startup banner spans several lines).
	lines := firstLines(headText(head), 3)
	matched := 0
	for i, l := range lines {
		if reErrlogLine.MatchString(l) || (i > 0 && (l[0] == '\t' || l[0] == ' ')) {
			matched++
		}
	}
	if len(lines) > 0 && reErrlogLine.MatchString(lines[0]) && matched == len(lines) {
		score += 40
		if strings.Contains(strings.Join(lines, "\n"), "Microsoft SQL Server") {
			score += 10
		}
	}
	if score > 100 {
		score = 100
	}
	return score
}

// Parse reads an ERRORLOG (UTF-16LE with or without a BOM, or UTF-8). The
// times are server-local, so a host with a time zone is required; without one
// the file is refused before any line is read.
func (x errorlogImporter) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := effectiveLimits(opts.Limits, x.DefaultLimits())
	s := newSink(ctx, ErrorlogName, opts, lim, emit)
	s.rep.Quality = evidence.SourceQuality{TimezoneRule: clock.RuleHostLocal}
	s.rep.FieldRules = map[string]string{"timestamp": clock.RuleHostLocal}
	if err := ctx.Err(); err != nil {
		return s.rep, err
	}
	br, head, err := sniffed(importer.LimitReader(r, lim.MaxBytes))
	if err != nil {
		return s.rep, err
	}
	if opts.Host == nil || opts.Host.TZ == nil || opts.Host.Label == "" {
		return s.rep, importer.ErrTimezoneUnlabelled
	}
	loc, host := opts.Host.TZ, opts.Host.Label

	// textio decodes a BOM (UTF-8, UTF-16LE/BE) and bounds every decoded line
	// (LimitError "line-bytes"); a BOM-less UTF-16LE copy is transcoded here
	// first, through the same bounded reader.
	var src io.Reader = br
	if looksUTF16LE(head) {
		src = transform.NewReader(br, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder())
	}
	lr, _, err := textio.NewLineReader(src, lim.MaxLineBytes)
	if err != nil {
		return s.rep, err
	}

	var unclassified, logins, ioStalls, adjusted, dated bool
	var have18456 bool
	var last18456 time.Time
	phys := 0
	for {
		raw, line, err := lr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A RowError (bad text) rejects the line; anything else (a
			// limit, a read error) ends the import.
			var re importer.RowError
			if !errors.As(err, &re) {
				return s.rep, err
			}
			if rerr := s.row(); rerr != nil {
				return s.rep, rerr
			}
			s.reject(line, re.Reason)
			continue
		}
		if phys++; phys&127 == 0 {
			if err := ctx.Err(); err != nil {
				return s.rep, err
			}
		}
		text := strings.TrimRight(strings.TrimPrefix(string(raw), string(rune(0xfeff))), "\r ")
		if strings.TrimSpace(text) == "" {
			continue
		}
		if err := s.row(); err != nil {
			return s.rep, err
		}
		m := reErrlogLine.FindStringSubmatch(text)
		if m == nil {
			if reErrlogDated.MatchString(text) {
				s.reject(line, importer.ReasonBadTime)
			} else {
				unclassified = true // continuation of the previous message
			}
			continue
		}
		lt, err := clock.ParseLocal("2006-01-02 15:04:05", m[1], loc)
		switch {
		case err != nil:
			s.reject(line, importer.ReasonBadTime)
			continue
		case lt.Ambiguous:
			s.reject(line, importer.ReasonDSTAmbiguous)
			continue
		case lt.Nonexistent:
			s.reject(line, importer.ReasonDSTNonexistent)
			continue
		}
		ts := lt.UTC
		s.seen(ts)
		dated = true
		msg := m[3]

		if a := reUTCAdjust.FindStringSubmatch(msg); a != nil {
			h, _ := strconv.Atoi(a[2])
			mi, _ := strconv.Atoi(a[3])
			adj := h*60 + mi
			if a[1] == "-" {
				adj = -adj
			}
			if _, off := ts.In(loc).Zone(); off/60 != adj {
				return s.rep, &importer.QuarantineError{Reason: importer.ReasonTZMismatch}
			}
			adjusted = true
			continue
		}
		if b := reBanner.FindStringSubmatch(msg); b != nil {
			s.rep.Quality.SourceVersion = "SQL Server " + b[1]
			unclassified = true
			continue
		}

		// A failed login writes "Error: 18456" and then "Login failed for user" in the
		// same second; the pair is one failed login, counted once.
		pairedLogin := have18456 && last18456.Equal(ts)
		have18456 = false

		e := evidence.Event{Host: host, Start: ts, End: ts, TZRule: clock.RuleHostLocal}
		switch {
		case reLoginFailed.MatchString(msg):
			logins = true
			if pairedLogin {
				continue
			}
			e.Kind, e.Severity, e.Value, e.Unit = KindLoginFailed, "warning", ptr(1), "count"
		case reIOStall.MatchString(msg):
			n, _ := strconv.Atoi(reIOStall.FindStringSubmatch(msg)[1])
			e.Kind, e.Severity, e.Value, e.Unit = KindIOStall, "warning", ptr(float64(n)), "count"
			ioStalls = true
		case reNonYielding.MatchString(msg):
			e.Kind, e.Severity = KindNonYielding, "error"
		case reErrorSev.MatchString(msg):
			g := reErrorSev.FindStringSubmatch(msg)
			sev, _ := strconv.Atoi(g[2])
			if sev > 25 {
				s.reject(line, importer.ReasonBadNumber)
				continue
			}
			num, _ := strconv.Atoi(g[1])
			if num == errLoginFailed {
				// Error 18456 is a failed login: a count, like the "Login failed" line.
				logins = true
				have18456, last18456 = true, ts
				e.Kind, e.Severity, e.Value, e.Unit = KindLoginFailed, "warning", ptr(1), "count"
				break
			}
			e.Kind, e.Severity = KindError, sqlSeverity(sev)
			e.Attrs = map[string]string{"error_number": strconv.Itoa(num), "sql_severity": strconv.Itoa(sev)}
		case reStart.MatchString(msg):
			e.Kind, e.Severity = KindStart, "info"
		case reStop.MatchString(msg):
			e.Kind, e.Severity = KindStop, "info"
		default:
			unclassified = true
			continue
		}
		if err := s.put(e, line, 0); err != nil {
			return s.rep, err
		}
	}
	// A file without one valid time-prefixed line (empty, cut to a few bytes,
	// another format) is not an ERRORLOG; an "ok" import of nothing would hide
	// that.
	if !dated {
		return s.rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat}
	}
	s.rep.KnownGaps = append(s.rep.KnownGaps, gapErrlogLocal)
	if !adjusted {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapErrlogNoAdjust)
	}
	if ioStalls {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapErrlog833)
	}
	if logins {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapErrlogLogin)
	}
	if unclassified {
		s.rep.KnownGaps = append(s.rep.KnownGaps, gapErrlogUnclassified)
	}
	return s.finish()
}
