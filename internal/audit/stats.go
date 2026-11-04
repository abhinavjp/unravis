package audit

import (
	"context"
	"time"
)

// StatsInput names the source a SourceStats call reads.
type StatsInput struct {
	SourceID string `json:"sourceId"`
}

// SourceStats is the cheap size line of one audit source: how many documents its
// collection holds (an estimate from the collection statistics, never a scan) and
// the time of the newest one. Docs is -1 when the server could not tell. NewestAt
// is nil when the collection is empty or its newest time could not be read.
type SourceStats struct {
	SourceID string     `json:"sourceId"`
	Docs     int64      `json:"docs"`
	NewestAt *time.Time `json:"newestAt,omitempty"`
}

// SourceStatter is the optional capability behind the Sources list size line.
type SourceStatter interface {
	Stats(ctx context.Context, in StatsInput) (SourceStats, error)
}
