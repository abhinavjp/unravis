package audit

import (
	"context"
	"fmt"
)

var _ SourceStatter = (*demoRepository)(nil)

// Stats counts the demo records of one source and finds the newest time.
func (d *demoRepository) Stats(ctx context.Context, in StatsInput) (SourceStats, error) {
	if err := ctx.Err(); err != nil {
		return SourceStats{}, err
	}
	if !d.hasSource(in.SourceID) {
		return SourceStats{}, fmt.Errorf("%w: source unavailable", ErrNotFound)
	}
	out := SourceStats{SourceID: in.SourceID}
	for _, r := range d.records {
		if r.SourceID != in.SourceID {
			continue
		}
		out.Docs++
		at := r.InitiatedAt
		if at == nil {
			at = r.CreatedOn
		}
		if at != nil && (out.NewestAt == nil || at.After(*out.NewestAt)) {
			t := at.UTC()
			out.NewestAt = &t
		}
	}
	return out, nil
}
