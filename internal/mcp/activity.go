package mcp

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
)

func (b *Bridge) activity(ctx context.Context, raw json.RawMessage) (any, error) {
	var input audit.ActivityInput
	if err := strictArgs(raw, &input); err != nil {
		return nil, err
	}
	input.Limit = defaultLimit(raw, input.Limit, 20)
	if _, err := audit.ActivityQuery(input); err != nil {
		return nil, err
	}
	var page audit.Page
	if err := b.request(ctx, "/api/activity", input, &page); err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(page.Rows))
	for _, r := range page.Rows {
		rows = append(rows, map[string]any{"sourceId": r.SourceID, "controller": r.Controller, "action": r.Action, "kind": r.Kind, "outcome": r.Outcome, "initiatedAt": r.InitiatedAt, "completedAt": r.CompletedAt, "durationMs": r.DurationMs, "apiTimeMs": r.ApiTimeMs, "auditCreateTimeMs": r.AuditCreateTimeMs, "payloadRecordTimeMs": r.PayloadRecordTimeMs})
	}
	return map[string]any{"start": input.Start, "end": input.End, "sourceIds": input.SourceIDs, "rows": rows, "sources": page.Sources, "nextCursor": page.NextCursor, "truncated": page.Truncated, "note": "Recorded activity overlapping this absolute window, newest-first and paginated. Includes earlier starts with known recorded completion after the window begins. Earlier records with no completion are excluded because they cannot establish continued activity. All request types/outcomes in the selected sources are included; no baseline query is performed. Incomplete pages/source failures are not full incident coverage. Overlap does not prove causation, parent-child relations, database waits, CPU or queue state; cross-server clock differences may affect apparent order."}, nil
}
