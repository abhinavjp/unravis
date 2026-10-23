package audit

import "sort"

func requestVolume(q Query, buckets []Bucket) ([]VolumeBucket, error) {
	type key struct {
		controller, action string
		at                 int64
	}
	counts := make(map[key]int64)
	for _, b := range buckets {
		if !KindMatches(q.Kind, b.Kind, b.State == "valid") {
			continue
		}
		if !BucketOutcomeMatches(q.Outcome, b.Outcome) {
			continue
		}
		k := key{b.Controller, b.Action, b.At.UnixNano()}
		count := counts[k]
		if err := checkedAdd(&count, b.Count); err != nil {
			return nil, err
		}
		counts[k] = count
	}
	rows := make([]VolumeBucket, 0, len(counts))
	for _, b := range buckets {
		k := key{b.Controller, b.Action, b.At.UnixNano()}
		if count, ok := counts[k]; ok {
			rows = append(rows, VolumeBucket{Controller: b.Controller, Action: b.Action, At: b.At.UTC(), Count: count})
			delete(counts, k)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		if a.Controller != b.Controller {
			return a.Controller < b.Controller
		}
		return a.Action < b.Action
	})
	return rows, nil
}
