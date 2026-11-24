package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"api-audit-explorer/internal/audit"
)

// BL-349: evidence aliases, signed cursors and the bounds of the result handle, for a bridge that serves a host's grant. All of it is
// off on the stdio server and the command line (the options are not set), which keep their raw record ids, raw dashboard cursors and
// the single ten-minute comparison they always had.

// EvidenceKey names one audit record of one source.
type EvidenceKey struct{ SourceID, RecordID string }

// EvidenceMinter turns the records of one page into aliases, one per key in the same order. It is the backend's: it binds each alias to
// the profile, its revision and the source, and fails closed. An error fails the whole call (a page with some rows cited and some not
// would read as complete).
type EvidenceMinter func(ctx context.Context, keys []EvidenceKey) ([]string, error)

// WithEvidenceRefs makes the rows of audit_get_requests and audit_get_slowest carry "evidenceRef", an opaque alias of the record that the
// person can resolve in the app. The raw record id is not added by this option (WithHiddenRecordIDs and the identifiers grant decide it).
func WithEvidenceRefs(m EvidenceMinter) Option {
	return func(b *Bridge) { b.minter = m }
}

// CursorCodec seals the cursors of the three paged tools. Seal wraps the dashboard's own cursor (pos) for the tool and the fingerprint of
// the query it belongs to; Open returns pos only for a cursor that Seal made for the same tool and fingerprint, for the same bridge,
// that is not expired and whose signature holds. Any other string is refused: the answer never says which check failed.
type CursorCodec interface {
	Seal(tool, fingerprint, pos string) (string, error)
	Open(tool, fingerprint, cursor string) (pos string, ok bool)
}

// WithCursors turns on signed cursors: a client's cursor reaches the dashboard only after it was opened by the codec. Without it the
// cursor is the dashboard's own text (the stdio server and the command line).
func WithCursors(c CursorCodec) Option {
	return func(b *Bridge) { b.cursors = c }
}

// WithResultCap sets the largest comparison result this bridge keeps (the size of the result as JSON, bytes). A larger one is answered
// but not kept, so no result handle is issued for it. Zero means no cap.
func WithResultCap(n int) Option {
	return func(b *Bridge) { b.resultCap = n }
}

// WithClock sets the clock of the result handle (tests; the owner passes none).
func WithClock(now func() time.Time) Option {
	return func(b *Bridge) {
		if now != nil {
			b.now = now
		}
	}
}

const (
	maxSealedPos = 1024 // the largest dashboard cursor a sealed cursor wraps
	// cursorGone is the fixed answer to a cursor that does not open. It never says why (expired, changed, another host, another query).
	cursorGone = "that cursor is not valid any more; repeat the call without a cursor to start again"
)

// fingerprint is a short digest of the parts that define what a cursor pages through (never the cursor, the limit or the handle).
func fingerprint(parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		raw, _ := json.Marshal(p)
		h.Write(raw)
		h.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:16])
}

// openCursor returns the dashboard cursor to send for the client's cursor. With no codec it is the text as it is (legacy path); with
// one, an empty cursor is the first page and anything else must open.
func (b *Bridge) openCursor(tool, fp, cursor string) (string, error) {
	if b.cursors == nil || cursor == "" {
		return cursor, nil
	}
	pos, ok := b.cursors.Open(tool, fp, cursor)
	if !ok {
		return "", coded(CodeResultExpired, cursorGone)
	}
	return pos, nil
}

// sealCursor returns the cursor to show the client for the dashboard's next cursor.
func (b *Bridge) sealCursor(tool, fp, next string) (string, error) {
	if b.cursors == nil || next == "" {
		return next, nil
	}
	if len(next) > maxSealedPos {
		return "", coded(CodeInternal, "the page cursor is too large to sign")
	}
	c, err := b.cursors.Seal(tool, fp, next)
	if err != nil || c == "" {
		return "", coded(CodeInternal, "the page cursor could not be signed")
	}
	return c, nil
}

// evidenceRefs adds "evidenceRef" to the projected rows (rows[i] is records[i]). source is the source id to use for a record that carries
// none. A record with no id gets no ref; a record with no source at all fails the call.
func (b *Bridge) evidenceRefs(ctx context.Context, records []audit.RequestRecord, rows []map[string]any, source string) error {
	if b.minter == nil || len(records) == 0 {
		return nil
	}
	keys := make([]EvidenceKey, 0, len(records))
	at := make([]int, 0, len(records))
	for i, r := range records {
		if r.ID == "" {
			continue
		}
		src := r.SourceID
		if src == "" {
			src = source
		}
		if src == "" {
			return coded(CodeInternal, "an evidence reference could not be made")
		}
		keys, at = append(keys, EvidenceKey{SourceID: src, RecordID: r.ID}), append(at, i)
	}
	if len(keys) == 0 {
		return nil
	}
	refs, err := b.minter(ctx, keys)
	if err != nil || len(refs) != len(keys) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return coded(CodeInternal, "an evidence reference could not be made")
	}
	for n, i := range at {
		rows[i]["evidenceRef"] = refs[n]
	}
	return nil
}

// ---- the result handle: bounds ----

// ResultBytes says how many bytes of comparison result the bridge holds (0 when none, or when it has expired).
func (b *Bridge) ResultBytes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.result == nil || b.now().Sub(b.created) > resultTTL {
		return 0
	}
	return b.resultSize
}

// ResultUsedAt is when the held result was made or last used (zero when none).
func (b *Bridge) ResultUsedAt() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.result == nil {
		return time.Time{}
	}
	return b.used
}

// DropResult forgets the held result and its handle: the next use of the handle answers "expired or replaced". It frees the memory of an
// expired result and makes room when the backend holds too much.
func (b *Bridge) DropResult() {
	b.mu.Lock()
	b.result, b.resultID, b.resultSize = nil, "", 0
	b.mu.Unlock()
}

// DropResultIf forgets the held result only if it was last used at used (the time ResultUsedAt gave), so a newer result stored in
// between is kept. It reports whether it dropped.
func (b *Bridge) DropResultIf(used time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.result == nil || !b.used.Equal(used) {
		return false
	}
	b.result, b.resultID, b.resultSize = nil, "", 0
	return true
}

// ExpireResult drops the held result if its ten minutes are over, and reports whether it did.
func (b *Bridge) ExpireResult() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.result == nil || b.now().Sub(b.created) <= resultTTL {
		return false
	}
	b.result, b.resultID, b.resultSize = nil, "", 0
	return true
}
