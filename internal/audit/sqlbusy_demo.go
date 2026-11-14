package audit

import (
	"context"
	"time"
)

// SQLBusy sums the database time of the demo calls that started in each step.
// Steps start at in.Start. A call whose light totals or database time are not
// recorded is not counted; a step with no counted call is left out.
func (d *demoRepository) SQLBusy(ctx context.Context, in SQLBusyInput) (SQLBusyResult, error) {
	if err := ctx.Err(); err != nil {
		return SQLBusyResult{}, err
	}
	if err := in.Validate(); err != nil {
		return SQLBusyResult{}, err
	}
	step := time.Duration(in.BucketSeconds) * time.Second
	busy := make([]float64, in.Steps())
	calls := make([]int64, in.Steps())
	for i, r := range d.records {
		if r.InitiatedAt == nil || r.InitiatedAt.Before(in.Start) || !r.InitiatedAt.Before(in.End) {
			continue
		}
		light := demoLight(r, i)
		if light == nil || light.SQLMs == nil {
			continue
		}
		k := int(r.InitiatedAt.Sub(in.Start) / step)
		busy[k] += *light.SQLMs
		calls[k]++
	}
	result := SQLBusyResult{Bins: []SQLBusyBin{}, Recorded: true}
	for k := range busy {
		if calls[k] > 0 {
			result.Bins = append(result.Bins, SQLBusyBin{Start: in.Start.Add(time.Duration(k) * step), BusyMs: busy[k], Calls: calls[k]})
		}
	}
	return result, nil
}

var _ SQLBusyReader = (*demoRepository)(nil)
