// Package ledger is the P2-lite hypothesis ledger (roadmap §8.6). It holds
// analyst-edited hypotheses, their states, provenance references and notes. It
// holds no row data and no identifiers, and it never sets a state or computes a
// verdict: guards can only block "strong correlation".
package ledger

import (
	"errors"
	"time"
)

// States. There is no "proven cause" state.
const (
	StateOpen         = "open"
	StateSupported    = "supported_by_correlation"
	StateStrong       = "supported_by_strong_correlation"
	StateContradicted = "contradicted"
	StateNotTestable  = "not_testable"
)

// Alternative statuses for a named alternative explanation.
const (
	AltOpen        = "open"
	AltRuledOut    = "ruled_out"
	AltNotTestable = "not_testable"
)

// The per-field limits bound one entry: at most MaxTextLen characters in each of
// about 2*MaxListLen+3 text fields, which JSON escaping can grow to about 1.3 MB.
// MaxTotalBytes caps all entries together, measured as GET /api/ledger serialises
// them. It is half the server's 16 MiB response cap, so the list always fits, and
// it leaves room for several maximum-size entries.
const (
	MaxHypotheses = 200
	MaxTextLen    = 2000
	MaxListLen    = 50
	MaxTotalBytes = 8 << 20
	// MaxSettlingLen caps the one-observation "what would settle it" field.
	MaxSettlingLen = 500
)

var (
	ErrNotFound = errors.New("hypothesis not found")
	ErrInvalid  = errors.New("invalid hypothesis")
)

// GuardError blocks a state change. Reasons are plain-language sentences.
type GuardError struct{ Reasons []string }

func (e *GuardError) Error() string { return "blocked: " + joinReasons(e.Reasons) }

func joinReasons(r []string) string {
	out := ""
	for i, s := range r {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}

type Alternative struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// Facts are the analyst-stated inputs the guards read. The product derives none of them.
type Facts struct {
	PresentK          int           `json:"presentK"`        // episodes with the linked event present
	TotalN            int           `json:"totalN"`          // independent episodes examined
	Counterexamples   []string      `json:"counterexamples"` // episodes with the event absent, or present without a stall
	Alternatives      []Alternative `json:"alternatives"`
	JoinAmbiguousPct  *float64      `json:"joinAmbiguousPct,omitempty"`
	TimezoneRecorded  bool          `json:"timezoneRecorded"`
	ConfounderOverlap bool          `json:"confounderOverlap"`
	CoverageGap       bool          `json:"coverageGap"`
	OverlapOnly       bool          `json:"overlapOnly"` // support is temporal overlap alone
}

type HistoryEntry struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
	Note  string    `json:"note"` // e.g. "earlier incident"; never carried over as support
}

// Input is what the analyst edits. State is the analyst's choice.
type Input struct {
	Text                string   `json:"text"`
	State               string   `json:"state"`
	EvidenceIDs         []string `json:"evidenceIds"` // provenance references only
	Notes               string   `json:"notes"`
	SettlingObservation string   `json:"settlingObservation"` // one observation that would settle the theory either way; up to MaxSettlingLen characters
	Facts               Facts    `json:"facts"`
}

type Hypothesis struct {
	ID                  string         `json:"id"`
	Text                string         `json:"text"`
	State               string         `json:"state"`
	EvidenceIDs         []string       `json:"evidenceIds"`
	Notes               string         `json:"notes"`
	SettlingObservation string         `json:"settlingObservation"` // see Input
	Facts               Facts          `json:"facts"`
	Guards              []string       `json:"guards"` // reasons "strong correlation" is blocked now; empty when it would be allowed
	CreatedAt           time.Time      `json:"createdAt"`
	UpdatedAt           time.Time      `json:"updatedAt"`
	History             []HistoryEntry `json:"history"`
}

// Store is the ledger. Create always starts at StateOpen. Update returns a
// *GuardError when Input.State is StateStrong and a guard blocks it.
// Reuse starts a new open entry from an old one and records the old final
// state as history. Persistence is a local JSON file; an empty path is in memory.
type Store interface {
	List() ([]Hypothesis, error)
	Create(text string) (Hypothesis, error)
	Update(id string, in Input) (Hypothesis, error)
	Reuse(id string) (Hypothesis, error)
	Delete(id string) error
}
