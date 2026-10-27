package casestore

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"
)

// The "needs another try" list (decisions A14, finding C19). When the create
// wizard cannot finish a step, the step is written into the case, so a reload, a
// restart or a case file carries it. The kinds are kept apart on purpose: "not
// added to analysis" and "import failed" are different things for the person.
const (
	RetryEnvironments = "environments" // the environment names were not saved
	RetryTag          = "tag"          // a source tag was not saved
	RetryAudit        = "audit-source" // a database was not added to the analysis
	RetryImport       = "import"       // a file or finding was not imported
)

// Bounds of a retry list.
const (
	MaxRetries       = 200
	MaxRetryKeyChars = 200
	MaxRetryReason   = 40
	MaxRetryPayload  = 4 << 10
)

// RetryItem is one step that did not go through. It holds short words and ids
// only: never a path, a connection string, a password or any file content. Payload
// is a small JSON object the retry needs (for example the tag to write); it is
// checked to be JSON and bounded, and never logged.
type RetryItem struct {
	ID      int64 // assigned by the store; ignored on input
	Kind    string
	Key     string // the wizard's key for the source: stable across a run, so a repeat of the run replaces the item
	Label   string
	Reason  string // a fixed reason id such as "case-closed" or "unreachable"
	Payload []byte
	Created time.Time // assigned by the store (UTC); ignored on input
}

// RetryStore is implemented by a Case that keeps the retry list. A separate
// interface, like Tagger, so it breaks no Case; both stores implement it.
//
// The list is state about the case, not evidence in it: writing it is allowed on
// a Closed case (so a case found Closed mid-create can still say what is left),
// where the retry itself is refused until the case is reopened.
type RetryStore interface {
	// Retries lists the items, oldest first. A case without any gives an empty list.
	Retries(ctx context.Context) ([]RetryItem, error)
	// ReplaceRetries makes the list exactly items (cleaned, ids and times assigned)
	// and returns it. Replacing, not appending, is what makes a repeated wizard run
	// safe: the list after run two says what is still undone after run two.
	// ErrBadRetry for a bad item, ErrCapExceeded past MaxRetries.
	ReplaceRetries(ctx context.Context, items []RetryItem) ([]RetryItem, error)
	// RemoveRetry deletes one item. An ID that is not there is not an error.
	RemoveRetry(ctx context.Context, id int64) error
	// NoteRetry writes one item without touching the others: an item with the same
	// kind and key (any case) is replaced, otherwise it is added. The server uses it
	// to say "this file needs another try" before an import starts, so a crash
	// in the middle leaves the line behind (C17). Same refusals as ReplaceRetries.
	NoteRetry(ctx context.Context, item RetryItem) (RetryItem, error)
}

// ErrBadRetry is the refusal of a retry item that breaks the bounds above.
var ErrBadRetry = errorString("invalid retry item")

type errorString string

func (e errorString) Error() string { return string(e) }

func reasonOK(s string) bool {
	if s == "" || len(s) > MaxRetryReason {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// CleanRetries applies the ReplaceRetries rules and returns the list to store.
func CleanRetries(items []RetryItem) ([]RetryItem, error) {
	if len(items) > MaxRetries {
		return nil, ErrCapExceeded
	}
	out := make([]RetryItem, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		switch it.Kind {
		case RetryEnvironments, RetryTag, RetryAudit, RetryImport:
		default:
			return nil, ErrBadRetry
		}
		var ok bool
		if it.Label, ok = cleanTagText(it.Label, MaxTagTextChars); !ok {
			return nil, ErrBadRetry
		}
		if it.Key, ok = cleanTagText(it.Key, MaxRetryKeyChars); !ok {
			return nil, ErrBadRetry
		}
		if it.Label == "" || it.Key == "" || !reasonOK(it.Reason) {
			return nil, ErrBadRetry
		}
		if len(it.Payload) > MaxRetryPayload {
			return nil, ErrBadRetry
		}
		if len(it.Payload) > 0 {
			trimmed := bytes.TrimSpace(it.Payload)
			var obj map[string]json.RawMessage
			if json.Unmarshal(trimmed, &obj) != nil || obj == nil {
				return nil, ErrBadRetry
			}
			it.Payload = append([]byte(nil), trimmed...)
		} else {
			it.Payload = nil
		}
		// One item per step and source: a repeat of the same pair keeps the last.
		k := it.Kind + "\x00" + strings.ToLower(it.Key)
		if seen[k] {
			continue
		}
		seen[k] = true
		it.ID, it.Created = 0, time.Time{}
		out = append(out, it)
	}
	return out, nil
}
