package audit

import (
	"context"
	"testing"
	"time"
)

func TestLoadWindowsPoolHistogramsAndIncludeMissingCounts(t *testing.T) {
	q := analysisQuery()
	var b []Bucket
	b = append(b, repeatedBuckets(19, 10, anchor)...)
	b = append(b, makeBuckets("s1", "Reports", "Run", "synchronous", "success", "valid", anchor, 1, 2000)...)
	b = append(b, makeBuckets("s1", "Reports", "Run", "synchronous", "success", "incomplete", anchor, 3, 0)...)
	a, e := BuildAnalysis(q, b, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(a.LoadWindows) != 1 {
		t.Fatal(a.LoadWindows)
	}
	w := a.LoadWindows[0]
	if w.Count != 23 || w.Timing.Count != 20 || *w.Timing.MaxMs != 2000 || *w.Timing.P95.ValueMs > 20 {
		t.Fatalf("pooled %+v", w)
	}
}
func TestDemoSlowestIsGlobalDurationOrderAndMetricAware(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := analysisQuery()
	q.SourceIDs = []string{"demo-west", "demo-east"}
	q.Start = anchor.Add(-15 * time.Minute)
	q.End = anchor
	q.BaselineStart = anchor.Add(-30 * time.Minute)
	q.BaselineEnd = q.Start
	q.BucketSeconds = 60
	p, e := d.Slowest(context.Background(), PageInput{Query: q, Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Rows) != 2 || *p.Rows[0].DurationMs < *p.Rows[1].DurationMs {
		t.Fatal(p)
	}
	for _, r := range p.Rows {
		if r.Controller != "Payroll" {
			t.Fatal("not globally slowest")
		}
	}
}
