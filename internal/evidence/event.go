package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/clock"
)

// EventSchemaVersion versions the per-row Event shape that importers emit.
// Record (finding level, §8.9) is a separate type and is unchanged.
const EventSchemaVersion = 1

// Bounds enforced by Event.Validate.
const (
	MaxEventAttrs      = 16
	MaxEventAttrValue  = 256
	maxEventAttrKey    = 64
	maxEventShort      = 64
	maxEventEvidenceID = 128
)

// ValidImporterLabel reports whether label may be written by an importer or a
// join: only Symptom and Correlation. Strong correlation stays a ledger
// decision, and "Proven cause" is never written (rulings §0.6).
func ValidImporterLabel(label string) bool {
	return label == audit.EvidenceSymptom || label == audit.EvidenceCorrelation
}

// JoinClass says how well a join pinned one event to one interval.
type JoinClass string

const (
	JoinUnique    JoinClass = "unique"
	JoinAmbiguous JoinClass = "ambiguous" // listed, never counted
	JoinNone      JoinClass = "none"      // also single-source records
)

// Event is the per-row unit an importer emits.
type Event struct {
	EvidenceID string    // EventID below
	Source     string    // importer Name(), e.g. "iis-w3c"
	Kind       string    // per-importer enum: "request", "apppool-recycle", "slow-op", ...
	Host       string    // hosts.json label after mapping; raw names never reach the store
	Start, End time.Time // UTC; End == Start for point events
	DurationMs *float64  // nil when the source has none ("-")
	Value      *float64
	Unit       string            // "ms", "percent-host", "percent-one-core", "count", "bytes", "percent", "per-sec", "ratio"
	Severity   string            // "info", "warning", "error", "critical", ""
	Line       int64             // 1-based source line (provenance anchor)
	TZRule     string            // clock.Rule* that produced Start
	Attrs      map[string]string // per-importer allowlist; <= 16 keys, <= 256 bytes per value
}

// ErrEventInvalid is wrapped by every Validate failure. Messages name fields
// and limits only, never values.
var ErrEventInvalid = errors.New("invalid event")

func invalidEvent(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrEventInvalid, fmt.Sprintf(format, a...))
}

func isUTC(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	_, off := t.Zone()
	return off == 0 && t.Location() == time.UTC
}

func finite(p *float64) bool {
	return p == nil || (!math.IsNaN(*p) && !math.IsInf(*p, 0))
}

// Validate checks the event against the shape rules and the importer's
// attribute allowlist: UTC times, End >= Start, bounded strings, finite
// numbers, a known severity and time-zone rule, and attributes that are
// allowlisted, at most 16, each value at most 256 bytes of valid UTF-8.
// A nil allowlist permits no attributes. Line is 1-based (provenance anchor)
// and TZRule is required: every event says how its time became UTC.
func (e Event) Validate(allowedAttrs map[string]bool) error {
	if e.EvidenceID == "" || len(e.EvidenceID) > maxEventEvidenceID {
		return invalidEvent("evidence id missing or too long")
	}
	for _, f := range []struct {
		name, v string
		max     int
		req     bool
	}{
		{"source", e.Source, maxEventShort, true},
		{"kind", e.Kind, maxEventShort, true},
		{"host", e.Host, maxEventShort, false},
		{"unit", e.Unit, maxEventShort, false},
	} {
		if (f.req && f.v == "") || len(f.v) > f.max || !utf8.ValidString(f.v) {
			return invalidEvent("%s missing, too long or not UTF-8", f.name)
		}
	}
	if !isUTC(e.Start) || !isUTC(e.End) {
		return invalidEvent("start and end must be set and in UTC")
	}
	if e.End.Before(e.Start) {
		return invalidEvent("end before start")
	}
	if !finite(e.DurationMs) || !finite(e.Value) {
		return invalidEvent("duration or value is not a finite number")
	}
	if e.DurationMs != nil && *e.DurationMs < 0 {
		return invalidEvent("negative duration")
	}
	switch e.Severity {
	case "", "info", "warning", "error", "critical":
	default:
		return invalidEvent("severity not in the fixed set")
	}
	if e.Line < 1 {
		return invalidEvent("line must be 1 or more")
	}
	switch e.TZRule {
	case clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleHeaderBias, clock.RuleHostLocal:
	default:
		return invalidEvent("time-zone rule not in the fixed set")
	}
	if len(e.Attrs) > MaxEventAttrs {
		return invalidEvent("more than %d attributes", MaxEventAttrs)
	}
	for k, v := range e.Attrs {
		if k == "" || len(k) > maxEventAttrKey || !allowedAttrs[k] {
			return invalidEvent("attribute key not on the importer allowlist")
		}
		if len(v) > MaxEventAttrValue || !utf8.ValidString(v) {
			return invalidEvent("attribute value too long or not UTF-8 (key %q)", k)
		}
	}
	return nil
}

// CheckEventID reports whether id has the shape EventID produces for this case
// and importer: ev:<case8>:<importer>:<16 lowercase hex>. Messages never carry
// the id.
func CheckEventID(id, caseID, importer string) error {
	prefix := CasePrefixes(caseID)[0] + importer + ":"
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || len(rest) != 16 {
		return invalidEvent("evidence id is not ev:<case>:<importer>:<16 hex> for this case and importer")
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return invalidEvent("evidence id is not ev:<case>:<importer>:<16 hex> for this case and importer")
		}
	}
	return nil
}

// ValidateFor is Validate plus the provenance rules a store and Collect
// enforce: Source is the importer's own name and EvidenceID has the shape
// EventID builds for this case and importer, so an event cannot claim another
// case's prefix (erase strips ledger references by prefix) or another
// importer's name.
func (e Event) ValidateFor(caseID, importer string, allowedAttrs map[string]bool) error {
	if err := e.Validate(allowedAttrs); err != nil {
		return err
	}
	if e.Source != importer {
		return invalidEvent("source is not the importing importer")
	}
	return CheckEventID(e.EvidenceID, caseID, importer)
}

// Clone returns a deep copy so a store never aliases an importer's buffers.
func (e Event) Clone() Event {
	if e.DurationMs != nil {
		d := *e.DurationMs
		e.DurationMs = &d
	}
	if e.Value != nil {
		v := *e.Value
		e.Value = &v
	}
	if e.Attrs != nil {
		m := make(map[string]string, len(e.Attrs))
		for k, v := range e.Attrs {
			m[k] = v
		}
		e.Attrs = m
	}
	return e
}

// case8 is the short case tag used in IDs.
func case8(caseID string) string {
	if len(caseID) > 8 {
		return caseID[:8]
	}
	return caseID
}

func hex16(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{'|'})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// EventID is ev:<case8>:<importer>:<16hex>, hex = SHA-256(caseID|importer|importNonce|line|ordinal)[:8].
// The nonce is random per import, so the file hash is not needed before parsing.
func EventID(caseID, importer, importNonce string, line int64, ordinal int) string {
	return "ev:" + case8(caseID) + ":" + importer + ":" +
		hex16(caseID, importer, importNonce, strconv.FormatInt(line, 10), strconv.Itoa(ordinal))
}

// JoinID is jn:<case8>:<rule>:<16hex> over the sorted member IDs (order of
// memberIDs does not matter; the input slice is not modified).
func JoinID(caseID, rule string, memberIDs []string) string {
	ids := append([]string(nil), memberIDs...)
	sort.Strings(ids)
	parts := append([]string{caseID, rule}, ids...)
	return "jn:" + case8(caseID) + ":" + rule + ":" + hex16(parts...)
}

// CasePrefixes lists the evidence-ID prefixes that belong to one case; erase
// strips ledger references by these prefixes.
func CasePrefixes(caseID string) []string {
	c := case8(caseID)
	return []string{"ev:" + c + ":", "jn:" + c + ":"}
}
