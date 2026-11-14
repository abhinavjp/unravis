package audit

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestDemoPerfSameIDGivesEqualPerf(t *testing.T) {
	repo := NewDemo(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)).(*demoRepository)
	ctx := context.Background()
	for _, r := range repo.records {
		in := RecordInput{ID: r.ID, SourceID: r.SourceID}
		first, err := repo.Record(ctx, in)
		if err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
		second, err := repo.Record(ctx, in)
		if err != nil {
			t.Fatalf("Record(%s) second call: %v", r.ID, err)
		}
		if !reflect.DeepEqual(first.Perf, second.Perf) {
			t.Fatalf("Perf for %s differs between calls", r.ID)
		}
	}
}

func TestDemoPerfHasNilAndNonNil(t *testing.T) {
	repo := NewDemo(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)).(*demoRepository)
	ctx := context.Background()
	sawNil, sawSet := false, false
	for i, r := range repo.records {
		detail, err := repo.Record(ctx, RecordInput{ID: r.ID, SourceID: r.SourceID})
		if err != nil {
			t.Fatalf("Record(%s): %v", r.ID, err)
		}
		if detail.Perf == nil {
			sawNil = true
			continue
		}
		sawSet = true
		p := detail.Perf
		if p.TextStored != nil && *p.TextStored != (i%4 == 0) {
			t.Fatalf("TextStored of %s breaks the every-fourth rule", r.ID)
		}
		if p.HitCount == nil || p.HTTPMs == nil || p.TextStored == nil {
			t.Fatalf("Perf for %s is missing scalars", r.ID)
		}
		if len(p.Filters) != 2 || len(p.SlowSQL) != 3 || len(p.SlowHTTP) != 2 || len(p.Steps) != 3 || len(p.Pressure) != 3 {
			t.Fatalf("Perf for %s has wrong list sizes", r.ID)
		}
		if detail.DurationMs != nil {
			d := *detail.DurationMs
			parts := []*float64{p.SQLMs, p.RedisMs, p.HTTPMs, p.FilterTotalMs}
			sum := 0.0
			for _, v := range parts {
				if v == nil || *v >= d {
					t.Fatalf("a scalar part of %s is nil or not below duration", r.ID)
				}
				sum += *v
			}
			if sum > d {
				t.Fatalf("sql+redis+http+filters of %s is %.2f, over duration %.2f", r.ID, sum, d)
			}
			listed := []*float64{}
			for _, f := range p.Filters {
				listed = append(listed, f.Ms)
			}
			for _, f := range p.Steps {
				listed = append(listed, f.Ms)
			}
			for _, f := range p.SlowSQL {
				listed = append(listed, f.Ms)
			}
			for _, f := range p.SlowHTTP {
				listed = append(listed, f.Ms)
			}
			for _, v := range listed {
				if v == nil || *v >= d {
					t.Fatalf("a listed part of %s is nil or not below duration", r.ID)
				}
			}
		}
	}
	if !sawNil || !sawSet {
		t.Fatalf("want both nil and non-nil Perf, got nil=%v set=%v", sawNil, sawSet)
	}
}
