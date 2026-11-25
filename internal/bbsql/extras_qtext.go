// extras_qtext.go: bb:qtext, scrubbed statement text per query hash (deep
// profile AND the owner's explicit sql.queryText flag).
//
// This is the one sampler that reads statement text, so it is built apart from
// every other extra:
//
//   - It is registered as TierText, which no profile lists. It is built only
//     when the host sets Runner.QueryTextMaxChars > 0 (from Config.SQLQueryText(),
//     true only for the deep profile with sql.queryText on).
//   - Its statement is the only one the lint lets name sys.dm_exec_sql_text, and
//     only through two fixed fragments (textFromSource, textExpr). Any other
//     statement that names the text function or a handle is still refused.
//   - Even built, the statement is sent only when the context carries the text
//     unlock (isAllowedCtx at the three send gates in conn.go), and tickExtras
//     adds that unlock only while QueryTextMaxChars > 0.
//   - Every kept text goes through querytext.Scrub(text, maxChars): comments
//     dropped, literals turned into ?, secrets scrubbed, statements holding a
//     credential word withheld whole (the Withheld marker is kept so the hash is
//     still explained), size capped.
//
// Source: sys.dm_exec_query_stats CROSS APPLY sys.dm_exec_sql_text(sql_handle).
// Columns checked on Microsoft Learn: statement_start_offset and
// statement_end_offset are byte offsets (int, 0-based, -1 = to the end of the
// batch); dm_exec_sql_text returns text nvarchar(max), NULL when encrypted.
// Permission is VIEW SERVER STATE (VIEW SERVER PERFORMANCE STATE on 2022+),
// inside the C8 ceiling.
//
// Known limits (live proof owed, BACKLOG):
//   - Heavier than the numeric samplers: the APPLY runs for every cached plan
//     before the TOP. 5-minute cadence, deep profile only.
//   - It keeps the TOP 40 plans by cumulative CPU, so a hash that only just got
//     busy needs time to appear. A hash seen once is re-sent only after 6 hours.
//   - The statement text is cut to 8000 characters in SQL; a cut literal cannot
//     leak: Scrub fails closed on an unterminated literal.
package bbsql

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"api-audit-explorer/internal/querytext"
)

const (
	// CapQText is how many plans bb:qtext fetches (TOP 41, keeps 40).
	CapQText  = 40
	qtextKind = "qtext"

	// qtextSeenMax bounds the per-hash re-send memory; qtextResend is how long a
	// hash stays quiet after its text was sent.
	qtextSeenMax = 4096
	qtextResend  = 6 * time.Hour
)

// The two fragments the lint lets through for the text statement only. They are
// the sole place the text function, the handle and the statement offsets appear.
const (
	textFromSource = "sys.dm_exec_query_stats AS qs CROSS APPLY sys.dm_exec_sql_text(qs.sql_handle) AS st"
	textExpr       = "LEFT(SUBSTRING(st.text, (qs.statement_start_offset / 2) + 1, ((CASE WHEN qs.statement_end_offset = -1 THEN DATALENGTH(st.text) ELSE qs.statement_end_offset END - qs.statement_start_offset) / 2) + 1), 8000)"
)

const stmtQText = setPrefix + `SELECT TOP 41 CONVERT(varchar(18), qs.query_hash, 1) AS qhash,
  ` + textExpr + ` AS q_text
FROM ` + textFromSource + `
WHERE qs.query_hash IS NOT NULL
ORDER BY qs.total_worker_time DESC /* unravis:bb:qtext */`

// textStatementTexts holds the statement texts that need the text unlock. It is
// written only at init.
var textStatementTexts = map[string]bool{}

// isTextStatement reports whether text is a statement that needs the unlock.
func isTextStatement(text string) bool { return textStatementTexts[text] }

type textUnlockKey struct{}

// withTextUnlock returns a context that lets a text statement through the send
// gates. Only tickExtras calls it, and only while QueryTextMaxChars > 0.
func withTextUnlock(ctx context.Context) context.Context {
	return context.WithValue(ctx, textUnlockKey{}, true)
}

func textUnlocked(ctx context.Context) bool {
	v, _ := ctx.Value(textUnlockKey{}).(bool)
	return v
}

// isAllowedCtx is IsAllowed plus the text gate: a text statement passes only
// with the unlock in the context. Every send path in conn.go uses it.
func isAllowedCtx(ctx context.Context, text string) bool {
	if !IsAllowed(text) {
		return false
	}
	if textStatementTexts[text] && !textUnlocked(ctx) {
		return false
	}
	return true
}

var errQText = errors.New("bbsql: invalid query text row")

// qtextRaw is the input row: the hash and the (possibly NULL) cut text.
type qtextRaw struct {
	Hash string `json:"qHash"`
	Text string `json:"qText"`
}

// QText is the bb:qtext payload: one hash and its scrubbed text.
type QText struct {
	QHash string `json:"qHash"`
	Text  string `json:"qText"`
}

type qtextSampler struct {
	maxChars int
	seen     map[string]time.Time
	obs      Observation
}

func (qtextSampler) ID() string              { return "bb:qtext" }
func (qtextSampler) Kind() string            { return qtextKind }
func (qtextSampler) Interval() time.Duration { return ExtraEvery5m }

func (s *qtextSampler) Observe(o Observation) { s.obs = o }

// Decode scrubs every kept text. A row is dropped when its text is NULL
// (encrypted), when it is this tool's own statement (it carries the tag), or
// when Scrub leaves nothing. A hash already sent in the last 6 hours is skipped,
// and a hash repeated in one reply (several plans) is sent once.
func (s *qtextSampler) Decode(rows []Row) (any, bool, error) {
	var cur []qtextRaw
	truncated, err := decodeRows(rows, &cur, CapQText)
	if err != nil {
		return nil, false, err
	}
	for _, r := range cur {
		if !qHashRe.MatchString(r.Hash) {
			return nil, false, errQHash
		}
	}
	now := s.obs.Time
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	for h, t := range s.seen {
		if now.Sub(t) >= qtextResend {
			delete(s.seen, h)
		}
	}
	if len(s.seen) >= qtextSeenMax {
		s.seen = map[string]time.Time{}
	}
	out := make([]QText, 0, len(cur))
	sent := make(map[string]bool, len(cur))
	for _, r := range cur {
		if r.Text == "" || strings.Contains(r.Text, "unravis:bb:") || sent[r.Hash] {
			continue
		}
		if _, ok := s.seen[r.Hash]; ok {
			continue
		}
		t := querytext.Scrub(r.Text, s.maxChars)
		if t == "" {
			continue
		}
		sent[r.Hash] = true
		s.seen[r.Hash] = now
		out = append(out, QText{QHash: r.Hash, Text: t})
	}
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out, truncated, nil
}

func init() {
	registerExtraStatementFrags("bb:qtext", stmtQText, CapQText, textFromSource,
		[]string{textFromSource, textExpr})
	textStatementTexts[stmtQText] = true
	registerExtraColumns(map[string]string{"q_text": "qText"})
	registerExtraRowSpec(reflect.TypeOf(qtextRaw{}), rowSpec{cols: map[string]colRule{
		"qHash": req(), "qText": reqNull(),
	}})
	registerTextFactory(func(max int) Sampler { return &qtextSampler{maxChars: max} })
}
