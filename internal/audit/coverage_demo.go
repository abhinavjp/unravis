package audit

import (
	"context"
	"fmt"
	"sort"
	"time"
)

var _ CoverageProber = (*demoRepository)(nil)

// Coverage computes the census from the demo records. The demo is synthetic, so
// it is always allowed. It has no AccountId, so accountId reports 0 %.
func (d *demoRepository) Coverage(ctx context.Context, in CoverageInput) (CoverageReport, error) {
	if err := ctx.Err(); err != nil {
		return CoverageReport{}, err
	}
	if err := ValidateCoverageInput(in); err != nil {
		return CoverageReport{}, err
	}
	if !d.hasSource(in.SourceID) {
		return CoverageReport{}, fmt.Errorf("%w: source unavailable", ErrNotFound)
	}
	began := time.Now()
	type key struct{ kind, day string }
	type counts struct{ docs, ria, created, api int64 }
	groups := map[key]*counts{}
	var sourceRecords int64
	for _, r := range d.records {
		if r.SourceID != in.SourceID {
			continue
		}
		sourceRecords++
		// Same window rule as the live census: RequestInitiatedAt, else CreatedOn.
		at := r.InitiatedAt
		if at == nil {
			at = r.CreatedOn
		}
		if at == nil || at.Before(in.Start) || !at.Before(in.End) {
			continue
		}
		k := key{r.Kind, at.UTC().Format("2006-01-02")}
		c := groups[k]
		if c == nil {
			c = &counts{}
			groups[k] = c
		}
		c.docs++
		if r.InitiatedAt != nil {
			c.ria++
		}
		if r.CreatedOn != nil {
			c.created++
		}
		if r.ApiTimeMs != nil {
			c.api++
		}
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].day != keys[j].day {
			return keys[i].day < keys[j].day
		}
		return keys[i].kind < keys[j].kind
	})
	field := func(nonNull, docs int64) FieldCoverage {
		return FieldCoverage{NonNull: nonNull, Pct: CoveragePct(nonNull, docs)}
	}
	row := func(kind, day string, c counts) CoverageRow {
		return CoverageRow{
			Kind: kind, Day: day, Docs: c.docs,
			RequestInitiatedAt: field(c.ria, c.docs), CreatedOn: field(c.created, c.docs),
			ApiTime: field(c.api, c.docs), AccountID: field(0, c.docs),
		}
	}
	rows := make([]CoverageRow, 0, len(keys))
	var total counts
	for _, k := range keys {
		c := groups[k]
		rows = append(rows, row(k.kind, k.day, *c))
		total.docs += c.docs
		total.ria += c.ria
		total.created += c.created
		total.api += c.api
	}
	examined := sourceRecords
	p := Provenance{
		SourceID: in.SourceID, Window: "coverage", Start: in.Start, End: in.End,
		QueryMode: QueryModeExact, ScanClass: ScanClassOf(QueryModeExact, in.End.Sub(in.Start), false),
		DocsReturned: int64(len(rows)), DocsExamined: &examined, SampleSize: total.docs,
		ElapsedMs: time.Since(began).Milliseconds(), MaxTimeMs: CensusQueryBudget.Milliseconds(),
	}
	p.Completeness = CompletenessOf(p)
	return CoverageReport{
		SourceID: in.SourceID, Rows: rows, Total: row("all", "", total),
		TenantAttribution: TenantAttribution(0, total.docs), Provenance: []Provenance{p},
	}, nil
}
