package mcp

import (
	"api-audit-explorer/internal/audit"
	"errors"
	"time"
)

func compareTimelines(result *audit.Analysis, id string, keys []string) (any, error) {
	if len(keys) < 1 || len(keys) > 8 {
		return nil, errors.New("select 1–8 distinct API keys from the cached comparison")
	}
	seen := map[string]bool{}
	groups := make([]audit.Comparison, 0, len(keys))
	observations := make([]map[int64]audit.Point, 0, len(keys))
	for _, key := range keys {
		if seen[key] {
			return nil, errors.New("apiKeys must be unique")
		}
		seen[key] = true
		row, err := findGroup(result, key)
		if err != nil {
			return nil, err
		}
		groups = append(groups, row)
		points := map[int64]audit.Point{}
		for _, series := range result.Timeline {
			if series.Key == key {
				for _, point := range series.Points {
					points[point.At.UnixNano()] = point
				}
				break
			}
		}
		observations = append(observations, points)
	}
	q := result.Query
	width := time.Duration(q.BucketSeconds) * time.Second
	if width <= 0 {
		return nil, errors.New("cached query has no usable time interval")
	}
	seconds := int64(q.BucketSeconds)
	epoch := q.Start.Unix() / seconds
	if q.Start.Unix()%seconds < 0 {
		epoch--
	}
	first := time.Unix(epoch*seconds, 0).UTC()
	slots := make([]map[string]any, 0, 120)
	for at := first; at.Before(q.End); at = at.Add(width) {
		if len(slots) >= 120 {
			return nil, errors.New("cached query exceeds the time-slot budget")
		}
		start, end := at, at.Add(width)
		if start.Before(q.Start) {
			start = q.Start
		}
		if end.After(q.End) {
			end = q.End
		}
		values := make([]map[string]any, 0, len(groups))
		for i, group := range groups {
			point, observed := observations[i][at.UnixNano()]
			var current, baseline, delta, percent any
			if group.Reference.P95 != nil && group.Reference.P95.ValueMs != nil {
				baseline = *group.Reference.P95.ValueMs
			}
			if observed && point.P95 != nil && point.P95.ValueMs != nil {
				current = *point.P95.ValueMs
				if value, ok := baseline.(float64); ok {
					delta = *point.P95.ValueMs - value
					if value > 0 && group.Reference.P95.LowerMs > 0 {
						percent = (*point.P95.ValueMs/value - 1) * 100
					}
				}
			}
			status := "missing"
			if observed && point.Count > 0 {
				status = "few_samples"
				if point.Count >= 20 && group.State == "compared" {
					status = "enough_samples"
				}
			}
			values = append(values, map[string]any{"apiKey": group.Key, "count": point.Count, "sampleStatus": status, "currentP95Ms": current, "baselineP95Ms": baseline, "increaseMs": delta, "increasePercent": percent})
		}
		slots = append(slots, map[string]any{"at": at.UTC(), "start": start.UTC(), "end": end.UTC(), "groups": values})
	}
	return map[string]any{"resultId": id, "query": q, "groups": groups, "slots": slots, "note": "Each API uses its own fixed reference P95. Positive increase means slower; zero means unchanged. Values are estimates; few_samples is exploratory and cannot establish a precise incident boundary. Missing slots are not zero. Compare absolute milliseconds, percentages and request counts together. Reported slot start/end are clipped to the selected query."}, nil
}
