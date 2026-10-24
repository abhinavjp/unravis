package audit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

func (d *demoRepository) Activity(ctx context.Context, input ActivityInput) (Page, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if _, err := ActivityQuery(input); err != nil {
		return Page{}, err
	}
	selected := map[string]bool{}
	sources := []SourceResult{}
	for _, id := range input.SourceIDs {
		if !d.hasSource(id) {
			return Page{}, fmt.Errorf("%w: source unavailable", ErrNotFound)
		}
		selected[id] = true
		sources = append(sources, SourceResult{SourceID: id, OK: true, Version: "demo"})
	}
	keyInput := input
	keyInput.Cursor = ""
	data, _ := json.Marshal(keyInput)
	hash := sha256.Sum256(data)
	fingerprint := base64.RawURLEncoding.EncodeToString(hash[:])
	offset := 0
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		var c demoCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Fingerprint != fingerprint || c.Offset < 0 {
			return Page{}, fmt.Errorf("%w: cursor mismatch", ErrInvalidInput)
		}
		offset = c.Offset
	}
	rows := []RequestRecord{}
	for _, r := range d.records {
		if selected[r.SourceID] && ActivityOverlaps(r.InitiatedAt, r.CompletedAt, input.Start, input.End) {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].InitiatedAt.Equal(*rows[j].InitiatedAt) {
			return rows[i].InitiatedAt.After(*rows[j].InitiatedAt)
		}
		if rows[i].SourceID != rows[j].SourceID {
			return rows[i].SourceID < rows[j].SourceID
		}
		return rows[i].ID > rows[j].ID
	})
	if offset > len(rows) {
		return Page{}, fmt.Errorf("%w: cursor outside result", ErrInvalidInput)
	}
	end := min(offset+input.Limit, len(rows))
	page := Page{Rows: rows[offset:end], Sources: sources}
	if end < len(rows) {
		raw, _ := json.Marshal(demoCursor{Offset: end, Fingerprint: fingerprint})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	matched, returned := map[string]int64{}, map[string]int64{}
	for _, r := range rows {
		matched[r.SourceID]++
	}
	for _, r := range page.Rows {
		returned[r.SourceID]++
	}
	scope := demoScope{mode: QueryModeExact}
	elapsed := time.Since(started)
	for _, s := range sources {
		page.Provenance = append(page.Provenance, d.demoProvenance(s.SourceID, "page", input.Start, input.End, scope, matched[s.SourceID], returned[s.SourceID], elapsed))
	}
	return page, nil
}
