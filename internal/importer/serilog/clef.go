package serilog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// NewCLEF returns the importer for Serilog compact JSON lines (CLEF).
func NewCLEF() importer.Importer { return &clefImp{} }

type clefImp struct{}

func (*clefImp) Name() string { return NameCLEF }

// AllowedAttrs is the attribute set of the application-log export policy.
func (*clefImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*clefImp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 2 << 30, MaxRows: 10_000_000, MaxEvents: 2_000_000,
		MaxLineBytes: 256 << 10, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// clefHead matches the start of a CLEF line: an object whose "@t" is an ISO
// date. The line may be cut anywhere after it (Detect sees only the head).
var clefHead = regexp.MustCompile(`^\{[^\n]*?"@t"\s*:\s*"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`)

// Detect scores a head whose first line is a JSON object with an "@t" time at
// 90, and 95 when the same line also has a message template or rendered
// message.
func (*clefImp) Detect(head []byte, _ string) int {
	line := firstLine(wincsv.HeadText(head))
	if !clefHead.MatchString(line) {
		return 0
	}
	if strings.Contains(line, `"@mt"`) || strings.Contains(line, `"@m"`) {
		return 95
	}
	return 90
}

// firstLine returns the first non-blank line of s, trimmed.
func firstLine(s string) string {
	for s != "" {
		var l string
		l, s, _ = strings.Cut(s, "\n")
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// clefLine holds the members a line is read for. Every other member
// (properties, "@i", "@r") is skipped by the decoder and never copied.
type clefLine struct {
	T             json.RawMessage `json:"@t"`
	L             json.RawMessage `json:"@l"`
	X             json.RawMessage `json:"@x"`
	M             json.RawMessage `json:"@m"`
	MT            json.RawMessage `json:"@mt"`
	SourceContext json.RawMessage `json:"SourceContext"`

	// Request-logging properties; read only for the request-finished family.
	Elapsed       json.RawMessage `json:"Elapsed"`
	ElapsedMs     json.RawMessage `json:"ElapsedMilliseconds"`
	StatusCode    json.RawMessage `json:"StatusCode"`
	RequestMethod json.RawMessage `json:"RequestMethod"`
}

// rawString returns the value of a JSON string member.
func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// rawNumber returns the value of a JSON number member (a string is not read).
func rawNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || raw[0] == '"' {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// noOffsetTime is an ISO time with no zone: recognised only to reject it with
// the right reason.
const noOffsetTime = "2006-01-02T15:04:05.999999999"

// clefTime reads "@t". A value with an offset or "Z" is exact; one without is
// tz-unlabelled, never guessed.
func clefTime(v string) (time.Time, string, importer.Reason) {
	if len(v) > 48 {
		return time.Time{}, "", importer.ReasonBadTime
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		if _, e2 := time.Parse(noOffsetTime, v); e2 == nil {
			return time.Time{}, "", importer.ReasonTZUnlabelled
		}
		return time.Time{}, "", importer.ReasonBadTime
	}
	if !saneTime(t) {
		return time.Time{}, "", importer.ReasonBadTime
	}
	rule := clock.RuleOffsetInField
	if strings.HasSuffix(v, "Z") {
		rule = clock.RuleUTCField
	}
	return t.UTC(), rule, ""
}

// frame turns one line into a record. reason is set when the line is rejected.
func (*clefImp) frame(line []byte) (rec applog.Record, templateOnly bool, reason importer.Reason) {
	var cl clefLine
	if err := json.Unmarshal(line, &cl); err != nil {
		return rec, false, importer.ReasonFieldCount
	}
	ts, ok := rawString(cl.T)
	if !ok {
		return rec, false, importer.ReasonBadTime
	}
	t, rule, reason := clefTime(ts)
	if reason != "" {
		return rec, false, reason
	}
	rec = applog.Record{Time: t, Rule: rule, Level: "Information"}
	if len(cl.L) > 0 && !bytes.Equal(cl.L, []byte("null")) {
		rec.Level, _ = rawString(cl.L) // a non-string level is stored as none
	}
	rec.Category, _ = rawString(cl.SourceContext)
	rec.Exception, _ = rawString(cl.X)
	if m, ok := rawString(cl.M); ok {
		rec.Message = m
	} else if mt, ok := rawString(cl.MT); ok {
		rec.Message, templateOnly = mt, true
	}
	if cl.Elapsed != nil || cl.ElapsedMs != nil || cl.StatusCode != nil || cl.RequestMethod != nil {
		if fam, _ := applog.Classify(rec.Category, 0, rec.Message, rec.Exception); fam == applog.FamilyRequestFinished {
			rec.Num = map[string]float64{}
			if v, ok := rawNumber(cl.Elapsed); ok {
				rec.Num[applog.NumElapsed] = v
			}
			if v, ok := rawNumber(cl.ElapsedMs); ok {
				rec.Num[applog.NumElapsedMs] = v
			}
			if v, ok := rawNumber(cl.StatusCode); ok {
				rec.Num[applog.NumStatus] = v
			}
			if m, ok := rawString(cl.RequestMethod); ok {
				if v, ok := applog.MethodNum(m); ok {
					rec.Num[applog.NumMethod] = v
				}
			}
		}
	}
	return rec, templateOnly, ""
}

func (c *clefImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, c.DefaultLimits())
	rep := importer.Report{Importer: NameCLEF, FieldRules: map[string]string{}}
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return rep, err
	}
	// The lines carry no host name: they belong to the host the user picked.
	if opts.Host == nil {
		return rep, importer.ErrUnknownHost
	}
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return rep, err
	}
	so := opts
	so.Limits = lim
	sink := applog.NewSink(NameCLEF, so, emit)
	var rules ruleTracker
	for {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		line, no, err := lr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return rep, err
			}
			rep.Rows++
			if err := lim.CheckRows(rep.Rows); err != nil {
				return rep, err
			}
			rep.Reject(no, reason)
			continue
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		rep.Rows++
		if err := lim.CheckRows(rep.Rows); err != nil {
			return rep, err
		}
		rec, templateOnly, reason := c.frame(line)
		if reason != "" {
			rep.Reject(no, reason)
			continue
		}
		if templateOnly {
			rep.Gap(GapTemplateOnly)
		}
		rules.see(rec.Rule)
		if err := sink.Add(rec, no); err != nil {
			return rep, err
		}
	}
	if rep.Rows == 0 {
		return rep, textio.ErrEmpty
	}
	if err := sink.Finish(&rep); err != nil {
		return rep, err
	}
	rules.finish(&rep, "@t")
	return rep, finishReport(&rep, lim, opts.Host)
}
