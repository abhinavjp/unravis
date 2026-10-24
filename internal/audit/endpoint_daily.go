package audit

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// EndpointDailyInput asks for the daily p95 and request count of one operation.
// The window may be up to MaxSeriesDays whole days in the zone; TimeZone is an
// IANA name and "" means UTC. Operation is "Controller.Action".
type EndpointDailyInput struct {
	SourceIDs []string  `json:"sourceIds"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	TimeZone  string    `json:"timeZone,omitempty"`
	Operation string    `json:"operation"`
}

// EndpointDailyDay is one whole local day. P50Ms and P95Ms are null when the day
// has fewer than MinSeriesDayCount timed requests (Low is then true) or was read
// only in part (Partial). Count is the timed requests behind the day.
type EndpointDailyDay struct {
	Date    string    `json:"date"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Count   int64     `json:"count"`
	P50Ms   *float64  `json:"p50Ms"`
	P95Ms   *float64  `json:"p95Ms"`
	Low     bool      `json:"low,omitempty"`
	Partial bool      `json:"partial,omitempty"`
}

// EndpointDaily is the daily series of one operation. Quantiles are bin midpoints
// of the merged histogram, accurate to about 5%.
type EndpointDaily struct {
	Operation  string             `json:"operation"`
	Zone       string             `json:"zone"`
	Days       []EndpointDailyDay `json:"days"`
	Provenance []Provenance       `json:"provenance,omitempty"`
	Warnings   []string           `json:"warnings"`
}

// ValidateEndpointDaily checks sources, the operation, the zone and the window.
func ValidateEndpointDaily(in EndpointDailyInput) error {
	if len(in.SourceIDs) == 0 || len(in.SourceIDs) > MaxSources {
		return invalidInputf("sourceIds must contain 1 to %d sources", MaxSources)
	}
	seen := make(map[string]struct{}, len(in.SourceIDs))
	for _, id := range in.SourceIDs {
		if strings.TrimSpace(id) == "" {
			return invalidInputf("sourceIds must not contain blank values")
		}
		if utf8.RuneCountInString(id) > MaxFilterLength {
			return invalidInputf("sourceIds must be at most %d characters", MaxFilterLength)
		}
		if _, ok := seen[id]; ok {
			return invalidInputf("sourceIds must not contain duplicates")
		}
		seen[id] = struct{}{}
	}
	if len(in.Operation) > MaxFilterLength || !plainText(in.Operation) {
		return invalidInputf("operation must be Controller.Action")
	}
	if _, _, ok := SplitOperation(in.Operation); !ok {
		return invalidInputf("operation must be Controller.Action")
	}
	loc, err := LoadZone(in.TimeZone)
	if err != nil {
		return err
	}
	return validateStepChange(&StepChangeInput{Start: in.Start, End: in.End}, loc)
}

// RunEndpointDaily reads the daily series of one operation, one day per Analyze
// call. A live source is limited to liveSeriesMaxDays days and refused beyond
// that with ErrBroadScanRefused. A day with a partial read is listed with no
// quantiles and a warning.
func RunEndpointDaily(ctx context.Context, repo Repository, in EndpointDailyInput) (EndpointDaily, error) {
	if err := ValidateEndpointDaily(in); err != nil {
		return EndpointDaily{}, err
	}
	if repo == nil {
		return EndpointDaily{}, invalidInputf("no repository")
	}
	loc, _ := LoadZone(in.TimeZone)
	days := localDays(in.Start, in.End, loc, MaxSeriesDays)
	reads, prov, err := readDays(ctx, repo, in.SourceIDs, days, in.Operation, liveSeriesMaxDays)
	if err != nil {
		return EndpointDaily{}, err
	}
	out := EndpointDaily{Operation: in.Operation, Zone: loc.String(), Days: make([]EndpointDailyDay, 0, len(reads)), Provenance: prov, Warnings: []string{}}
	partial, low := 0, 0
	for _, d := range reads {
		row := EndpointDailyDay{Date: d.day.Date, Start: d.day.Start, End: d.day.End}
		h := d.ops[in.Operation]
		if h != nil {
			row.Count = h.count
		}
		switch {
		case d.partial:
			row.Partial = true
			partial++
		case h != nil && h.count >= MinSeriesDayCount:
			if v, ok := h.quantile(0.50); ok {
				v = math.Round(v*10) / 10
				row.P50Ms = &v
			}
			if v, ok := h.quantile(0.95); ok {
				v = math.Round(v*10) / 10
				row.P95Ms = &v
			}
		case h != nil:
			row.Low = true
			low++
		}
		out.Days = append(out.Days, row)
	}
	if partial > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d %s read only in part and shown with no p95.", partial, plural(partial, "day was", "days were")))
	}
	if low > 0 {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%d %s fewer than %d timed requests and shown with no p95.", low, plural(low, "day has", "days have"), MinSeriesDayCount))
	}
	return out, nil
}
