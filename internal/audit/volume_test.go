package audit

import (
	"testing"
	"time"
)

func TestVolumeIncludesAllTimingStatesAndMergesOnlyMatchingRequests(t *testing.T) {
	q := analysisQuery()
	q.SourceIDs = []string{"s1", "s2"}
	var buckets []Bucket
	for i, state := range []string{"valid", "incomplete", "invalid"} {
		buckets = append(buckets, makeBuckets("s1", "Payroll", "Run", "synchronous", "success", state, anchor, int64(i+1), 10)...)
	}
	buckets = append(buckets, makeBuckets("s2", "Payroll", "Run", "hangfire", "failure", "valid", anchor, 4, 10)...)
	buckets = append(buckets, makeBuckets("s1", "Other", "Go", "synchronous", "success", "valid", anchor.Add(10*time.Second), 7, 10)...)
	a, err := BuildAnalysis(q, buckets, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, b := range a.RequestVolume {
		total += b.Count
	}
	if total != 17 || len(a.RequestVolume) != 2 {
		t.Fatalf("volume=%+v", a.RequestVolume)
	}
	q.Kind = "synchronous"
	q.Outcome = "success"
	a, err = BuildAnalysis(q, buckets, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	total = 0
	for _, b := range a.RequestVolume {
		total += b.Count
	}
	if total != 13 {
		t.Fatalf("filtered count=%d", total)
	}
}

func TestVolumeClipsEpochSlotsWithoutCountingReference(t *testing.T) {
	q := analysisQuery()
	q.Start = anchor.Add(time.Second)
	q.End = anchor.Add(19 * time.Second)
	a, err := BuildAnalysis(q, repeatedBuckets(3, 10, anchor), repeatedBuckets(9, 10, q.BaselineStart), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.RequestVolume) != 1 || a.RequestVolume[0].Count != 3 || !a.RequestVolume[0].At.Equal(anchor) {
		t.Fatalf("volume=%+v", a.RequestVolume)
	}
}
