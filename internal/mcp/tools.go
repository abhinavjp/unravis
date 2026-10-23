package mcp

import (
	"api-audit-explorer/internal/audit"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type groupArgs struct {
	ResultID string `json:"resultId"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	Search   string `json:"search"`
	State    string `json:"state"`
	Sort     string `json:"sort"`
}

func groupPage(rows []audit.Comparison, args groupArgs) (map[string]any, error) {
	if args.Limit == 0 {
		args.Limit = 20
	}
	if args.Offset < 0 || args.Limit < 1 || args.Limit > 100 || len(args.Search) > 200 {
		return nil, errors.New("offset must be non-negative, limit 1–100, search at most 200 bytes")
	}
	switch args.State {
	case "", "compared", "missing_current", "missing_reference", "insufficient_data", "undefined_baseline":
	default:
		return nil, errors.New("unknown comparison state")
	}
	if args.Sort == "" {
		args.Sort = "p95_delta_ms"
	}
	if args.Sort != "p95_delta_ms" && args.Sort != "p95_ratio" && args.Sort != "current_count" {
		return nil, errors.New("sort must be p95_delta_ms, p95_ratio or current_count")
	}
	filtered := make([]audit.Comparison, 0, len(rows))
	for _, row := range rows {
		if args.State != "" && row.State != args.State {
			continue
		}
		if !strings.Contains(strings.ToLower(row.Controller+"."+row.Action+" "+row.SourceID+" "+row.Kind+" "+row.Outcome), strings.ToLower(args.Search)) {
			continue
		}
		filtered = append(filtered, row)
	}
	value := func(row audit.Comparison) *float64 {
		if args.Sort == "p95_ratio" {
			return row.P95Ratio
		}
		if args.Sort == "current_count" {
			v := float64(row.Current.Count)
			return &v
		}
		return row.P95DeltaMs
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := value(filtered[i]), value(filtered[j])
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return *a > *b
	})
	start := min(args.Offset, len(filtered))
	end := min(start+args.Limit, len(filtered))
	var next any
	if end < len(filtered) {
		next = end
	}
	return map[string]any{"groups": filtered[start:end], "matchedGroups": len(filtered), "totalGroups": len(rows), "offset": start, "nextOffset": next, "sort": args.Sort}, nil
}
func (b *Bridge) cached(id string) (*audit.Analysis, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" || b.result == nil || id != b.resultID || b.now().Sub(b.created) > resultTTL {
		return nil, errors.New("result expired or was replaced; run audit_compare again. Results last ten minutes and only the latest successful comparison is retained")
	}
	return b.result, nil
}
func findGroup(result *audit.Analysis, key string) (audit.Comparison, error) {
	for _, row := range result.APIs {
		if row.Key == key {
			return row, nil
		}
	}
	return audit.Comparison{}, errors.New("API group is not in this result; use audit_get_groups to select its apiKey")
}
func (b *Bridge) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "audit_get_activity":
		return b.activity(ctx, raw)
	case "audit_get_guide":
		if err := strictArgs(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return map[string]any{"guide": investigationGuide}, nil
	case "audit_list_sources":
		if err := strictArgs(raw, &struct{}{}); err != nil {
			return nil, err
		}
		var sources []audit.Source
		if err := b.request(ctx, "/api/sources", nil, &sources); err != nil {
			return nil, err
		}
		if sources == nil {
			sources = []audit.Source{}
		}
		return map[string]any{"sources": sources, "note": "Only currently connected sources are available. Use the dashboard to connect sources; this tool cannot change connections."}, nil
	case "audit_compare":
		var args struct {
			Query audit.Query `json:"query"`
			Limit int         `json:"limit"`
			Sort  string      `json:"sort"`
		}
		if err := strictArgs(raw, &args); err != nil {
			return nil, err
		}
		if err := audit.ValidateQuery(args.Query); err != nil {
			return nil, err
		}
		pageArgs := groupArgs{Limit: args.Limit, Sort: args.Sort}
		if _, err := groupPage(nil, pageArgs); err != nil {
			return nil, err
		}
		b.mu.Lock()
		if b.comparing {
			b.mu.Unlock()
			return nil, errors.New("a comparison is already running in this MCP session; wait or cancel it before starting another")
		}
		b.comparing = true
		b.mu.Unlock()
		defer func() { b.mu.Lock(); b.comparing = false; b.mu.Unlock() }()
		var result audit.Analysis
		if err := b.request(ctx, "/api/analyze", args.Query, &result); err != nil {
			return nil, err
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, errors.New("could not create result handle")
		}
		id := hex.EncodeToString(random[:])
		b.mu.Lock()
		b.result = &result
		b.resultID = id
		b.created = b.now()
		b.mu.Unlock()
		page, _ := groupPage(result.APIs, pageArgs)
		comparable, limited := 0, 0
		var currentEvents, referenceEvents int64
		for _, row := range result.APIs {
			if row.State == "compared" {
				comparable++
			} else {
				limited++
			}
			currentEvents += row.CurrentEvents
			referenceEvents += row.ReferenceEvents
		}
		page["resultId"] = id
		page["query"] = result.Query
		page["sources"] = result.Sources
		page["quality"] = result.Quality
		page["comparableGroups"] = comparable
		page["groupsWithoutReliableRatio"] = limited
		page["recordedEvents"] = map[string]any{"current": currentEvents, "reference": referenceEvents, "currentPerSecond": float64(currentEvents) / args.Query.End.Sub(args.Query.Start).Seconds(), "referencePerSecond": float64(referenceEvents) / args.Query.BaselineEnd.Sub(args.Query.BaselineStart).Seconds()}
		page["approximate"] = result.Approximate
		page["expiresInSeconds"] = int(resultTTL.Seconds())
		page["interpretation"] = "Statistics are approximate; groups with fewer than 20 usable timings in either period have no reliable slowdown ratio. Source failures mean incomplete coverage. Counts/rates reflect these filters, not all server traffic. Read audit_get_guide before drawing conclusions."
		return page, nil
	case "audit_get_groups":
		var args groupArgs
		if err := strictArgs(raw, &args); err != nil {
			return nil, err
		}
		result, err := b.cached(args.ResultID)
		if err != nil {
			return nil, err
		}
		page, err := groupPage(result.APIs, args)
		if err != nil {
			return nil, err
		}
		page["resultId"] = args.ResultID
		return page, nil
	case "audit_get_timeline":
		var args struct {
			ResultID string `json:"resultId"`
			APIKey   string `json:"apiKey"`
		}
		if err := strictArgs(raw, &args); err != nil {
			return nil, err
		}
		result, err := b.cached(args.ResultID)
		if err != nil {
			return nil, err
		}
		row, err := findGroup(result, args.APIKey)
		if err != nil {
			return nil, err
		}
		points := []audit.Point{}
		for _, series := range result.Timeline {
			if series.Key == args.APIKey {
				points = series.Points
				break
			}
		}
		return map[string]any{"resultId": args.ResultID, "query": result.Query, "group": row, "points": points, "note": "Each point summarizes a time slot, not an individual request. Absent slots are missing observations, not zero duration. Reference statistics cover the whole fixed earlier period. Slot P95 values with few samples are unstable; inspect counts and narrow the interval before claiming precise incident boundaries."}, nil
	case "audit_compare_timelines":
		var args struct {
			ResultID string   `json:"resultId"`
			APIKeys  []string `json:"apiKeys"`
		}
		if err := strictArgs(raw, &args); err != nil {
			return nil, err
		}
		result, err := b.cached(args.ResultID)
		if err != nil {
			return nil, err
		}
		return compareTimelines(result, args.ResultID, args.APIKeys)
	case "audit_get_requests":
		var args struct {
			ResultID string `json:"resultId"`
			APIKey   string `json:"apiKey"`
			Cursor   string `json:"cursor"`
			Limit    int    `json:"limit"`
		}
		if err := strictArgs(raw, &args); err != nil {
			return nil, err
		}
		if args.Limit == 0 {
			args.Limit = 20
		}
		if args.Limit < 1 || args.Limit > 100 || len(args.Cursor) > 4096 {
			return nil, errors.New("limit must be 1–100 and cursor at most 4096 bytes")
		}
		result, err := b.cached(args.ResultID)
		if err != nil {
			return nil, err
		}
		row, err := findGroup(result, args.APIKey)
		if err != nil {
			return nil, err
		}
		var page audit.Page
		if err = b.request(ctx, "/api/requests", audit.PageInput{Query: result.Query, APIKey: args.APIKey, Cursor: args.Cursor, Limit: args.Limit}, &page); err != nil {
			return nil, err
		}
		// Explicit projection: never return raw URLs, request IDs, headers or payload text.
		rows := make([]map[string]any, 0, len(page.Rows))
		for _, record := range page.Rows {
			rows = append(rows, map[string]any{"initiatedAt": record.InitiatedAt, "completedAt": record.CompletedAt, "durationMs": record.DurationMs, "apiTimeMs": record.ApiTimeMs, "auditCreateTimeMs": record.AuditCreateTimeMs, "payloadRecordTimeMs": record.PayloadRecordTimeMs, "requestKB": record.RequestKB, "responseKB": record.ResponseKB, "outcome": record.Outcome, "status": record.Status})
		}
		return map[string]any{"resultId": args.ResultID, "sourceId": row.SourceID, "controller": row.Controller, "action": row.Action, "kind": row.Kind, "outcome": row.Outcome, "query": result.Query, "rows": rows, "nextCursor": page.NextCursor, "sources": page.Sources, "truncated": page.Truncated, "note": "Newest-first metadata page; not a random sample or a complete ranking of slow requests. AuditCreateTime plus ApiTime make up total latency; PayloadRecordTime happens after the response and is not latency. Do not infer database, network or queue wait from them without checking their instrumentation in code."}, nil
	default:
		return nil, errors.New("unknown tool; only the advertised read-only audit tools are available")
	}
}
