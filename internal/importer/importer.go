// Package importer is the contract every offline file importer implements.
// Importers read one local file, drop what the privacy rules forbid at parse
// time, and emit evidence.Event rows. They make no network calls, never keep
// raw bytes and never put a cell, line or message into an error, report or
// log (plan §4).
package importer

import (
	"context"
	"io"
	"strconv"
	"time"

	"api-audit-explorer/internal/evidence"
)

// HeadSize is how many leading bytes Detect and Sniff see.
const HeadSize = 64 << 10

// Importer parses one file format.
type Importer interface {
	Name() string                            // stable: "iis-w3c", "mongod-json", ...
	Detect(head []byte, fileName string) int // 0..100 over the first 64 KiB; 0 = not mine
	// Parse reads r to the end or until an error. The caller wraps r in
	// LimitReader(MaxBytes) and has run Sniff on the head (Collect does both);
	// Parse enforces rows, line length, fields and the per-row rules itself
	// and checks ctx at least every few thousand rows.
	Parse(ctx context.Context, r io.Reader, opts Options, emit func(evidence.Event) error) (Report, error)
	AllowedAttrs() map[string]bool
	DefaultLimits() Limits
}

// HostHint describes a host: the one the user picked for a file or one
// looked up in hosts.json.
type HostHint struct {
	Label, Role  string         // "web-a", "iis" | "sql" | "mongo" | "app"
	TZ           *time.Location // nil: host-local fields are refused
	LogicalCores int            // 0: per-process CPU left undivided and flagged
	Runtime      string         // "iis-10", "sql-2019", "mongod-7.0"
}

// Window restricts events to a UTC range. The zero value means no restriction.
type Window struct{ From, To time.Time } // UTC

// Contains reports whether t lies in the window (inclusive); an unset bound is open.
func (w Window) Contains(t time.Time) bool {
	return (w.From.IsZero() || !t.Before(w.From)) && (w.To.IsZero() || !t.After(w.To))
}

// Options carry everything an importer may know besides the file bytes.
type Options struct {
	CaseID, ImportNonce, FileName string                                 // FileName: sanitised base name, display only
	Host                          *HostHint                              // host the user picked for this file, if any
	Hosts                         func(observed string) (HostHint, bool) // hosts.json lookup for names inside the file
	Limits                        Limits
	Window                        Window
	Pseudonymize                  func(kind, value string) string // keyed per case; kind "user" | "client"
	Supplied                      map[string]string               // owner-supplied quality, e.g. "slowms": "100"
}

// MaxReportErrors is how many row errors a Report keeps.
const MaxReportErrors = 100

// MaxObservedHosts bounds Report.ObservedHosts; one more is an ErrLimit.
const MaxObservedHosts = 1000

// RowError is one rejected row. It holds a line number and a fixed reason,
// never row content, and is also usable as an error value.
type RowError struct {
	Line   int64
	Reason Reason
}

func (e RowError) Error() string {
	return "line " + strconv.FormatInt(e.Line, 10) + ": " + string(e.Reason)
}

// Report is what an importer learned about a file besides the events.
type Report struct {
	Importer                string
	Rows, Emitted, Rejected int64
	Errors                  []RowError // first 100 only
	ObservedHosts           []string   // raw names; the service maps or refuses them, never stores them
	First, Last             time.Time  // UTC coverage
	Quality                 evidence.SourceQuality
	FieldRules              map[string]string // field -> clock.Rule*
	KnownGaps               []string          // fixed sentences only
}

// Reject counts a rejected row and keeps the first MaxReportErrors of them.
func (r *Report) Reject(line int64, reason Reason) {
	r.Rejected++
	if len(r.Errors) < MaxReportErrors {
		r.Errors = append(r.Errors, RowError{Line: line, Reason: reason})
	}
}

// Observe records a raw host name once. It returns *LimitError past
// MaxObservedHosts distinct names.
func (r *Report) Observe(host string) error {
	for _, h := range r.ObservedHosts {
		if h == host {
			return nil
		}
	}
	if len(r.ObservedHosts) >= MaxObservedHosts {
		return &LimitError{Limit: "hosts", Max: MaxObservedHosts, At: MaxObservedHosts + 1}
	}
	r.ObservedHosts = append(r.ObservedHosts, host)
	return nil
}

// Cover widens First and Last to include t (converted to UTC).
func (r *Report) Cover(t time.Time) {
	t = t.UTC()
	if r.First.IsZero() || t.Before(r.First) {
		r.First = t
	}
	if r.Last.IsZero() || t.After(r.Last) {
		r.Last = t
	}
}

// Rule records that field was converted to UTC by rule (clock.Rule*).
func (r *Report) Rule(field, rule string) {
	if r.FieldRules == nil {
		r.FieldRules = map[string]string{}
	}
	r.FieldRules[field] = rule
}

// Gap records one fixed known-gap sentence once.
func (r *Report) Gap(sentence string) {
	for _, g := range r.KnownGaps {
		if g == sentence {
			return
		}
	}
	r.KnownGaps = append(r.KnownGaps, sentence)
}
