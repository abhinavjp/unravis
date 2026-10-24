package mel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

type jsonImp struct{}

// NewJSON returns the importer for the JSON console format (one object per
// line).
func NewJSON() importer.Importer { return &jsonImp{} }

func (*jsonImp) Name() string { return NameJSON }

func (*jsonImp) AllowedAttrs() map[string]bool { return applog.AllowedAttrs() }

func (*jsonImp) DefaultLimits() importer.Limits { return defaultLimits(wincsv.DefaultMaxLineBytes) }

// jsonLine is the part of a JSON console entry the importer reads. State, scopes
// and every other member are skipped by the decoder and never kept.
type jsonLine struct {
	Timestamp *string         `json:"Timestamp"`
	EventID   json.RawMessage `json:"EventId"`
	LogLevel  *string         `json:"LogLevel"`
	Category  *string         `json:"Category"`
	Message   *string         `json:"Message"`
	Exception *string         `json:"Exception"`
}

// decodeLine decodes one line. ok is false when the line is not an object with
// a level and a category.
func decodeLine(line []byte) (jl jsonLine, ok bool) {
	t := bytes.TrimSpace(line)
	if len(t) == 0 || t[0] != '{' {
		return jl, false
	}
	if err := json.Unmarshal(t, &jl); err != nil {
		return jsonLine{}, false
	}
	return jl, jl.LogLevel != nil && jl.Category != nil
}

// eventID reads EventId: a number, or an object with an Id member.
func eventID(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var o struct{ ID int }
	if json.Unmarshal(raw, &o) == nil {
		return o.ID
	}
	return 0
}

// Detect scores a head whose first lines are JSON console entries with a
// parseable Timestamp at 90, such entries with no Timestamp at 30 (so that Parse
// gives the refusal), else 0.
func (*jsonImp) Detect(head []byte, _ string) int {
	text := wincsv.HeadText(head)
	if len(text) > 16384 {
		text = text[:16384]
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 1 {
		lines = lines[:len(lines)-1]
	}
	shaped := false
	for i, l := range lines {
		if i > 32 {
			break
		}
		jl, ok := decodeLine([]byte(l))
		if !ok {
			continue
		}
		shaped = true
		if jl.Timestamp != nil {
			if _, _, why := parseStamp(strings.TrimSpace(*jl.Timestamp), nil, nil); why == "" || why == importer.ReasonTZUnlabelled {
				return 90
			}
		}
	}
	if shaped {
		return 30
	}
	return 0
}

func (m *jsonImp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	acc := wincsv.NewAcc(NameJSON, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}
	opts.Limits = lim
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return acc.Fail(err)
	}
	sink := applog.NewSink(NameJSON, opts, emit)
	s := newScan(ctx, opts, acc, sink, false)
	if err := s.runJSON(lr); err != nil {
		return acc.Fail(err)
	}
	if err := s.finish(); err != nil {
		return acc.Fail(err)
	}
	if s.dated == 0 && s.beats == 0 {
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if err := sink.Finish(&acc.Rep); err != nil {
		return acc.Fail(err)
	}
	setRule(acc, s.rule, opts)
	return acc.Finish()
}

func (s *scan) runJSON(lr *textio.LineReader) error {
	for {
		line, no, err := lr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return err
			}
			if e := s.acc.Row(no); e != nil {
				return e
			}
			s.sink.Reject(no, reason)
			continue
		}
		s.n++
		if s.n%checkEvery == 0 {
			if e := s.ctx.Err(); e != nil {
				return e
			}
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := s.acc.Row(no); err != nil {
			return err
		}
		if err := s.jsonEntry(line, no); err != nil {
			return err
		}
	}
}

// jsonEntry places one JSON console entry.
func (s *scan) jsonEntry(line []byte, no int64) error {
	jl, ok := decodeLine(line)
	if !ok {
		s.sink.Reject(no, importer.ReasonFieldCount)
		return nil
	}
	s.headers++
	s.cur = entry{open: true, line: no, rec: applog.Record{
		Level: *jl.LogLevel, Category: *jl.Category, EventID: eventID(jl.EventID)}}
	if jl.Message != nil {
		s.cur.rec.Message = *jl.Message
	}
	if jl.Exception != nil {
		s.cur.rec.Exception = *jl.Exception
	}
	if jl.Timestamp != nil {
		s.dated++
		t, rule, why := parseStamp(strings.TrimSpace(*jl.Timestamp), s.loc, s.dates)
		switch why {
		case "":
			s.cur.timed, s.cur.rec.Time, s.cur.rec.Rule = true, t, rule
			s.noteRule(rule)
		case importer.ReasonTZUnlabelled:
			return wincsv.TZUnlabelled()
		default:
			s.sink.Reject(no, why)
			s.cur = entry{}
			return nil
		}
	}
	return s.flushRec()
}

// flushRec places the entry in s.cur whose text is already in the record.
func (s *scan) flushRec() error {
	e := &s.cur
	if !e.open {
		return nil
	}
	e.open = false
	if e.timed {
		return s.sink.Add(e.rec, e.line)
	}
	if strings.HasPrefix(e.rec.Category, kestrelPrefix) {
		msg := strings.TrimSpace(e.rec.Message)
		if t, ok := applog.HeartbeatAsOfOrder(msg, s.dates); ok {
			e.rec.Time, e.rec.Rule = t, clock.RuleOffsetInField
			s.beats++
			s.acc.Gap(GapHeartbeat)
			s.noteRule(e.rec.Rule)
			return s.sink.Add(e.rec, e.line)
		}
		if strings.HasPrefix(msg, `As of "`) {
			// The line states a time that cannot be read.
			s.sink.Reject(e.line, importer.ReasonBadTime)
			s.acc.Gap(GapHeartbeat)
			return nil
		}
	}
	s.untimed++
	return nil
}
