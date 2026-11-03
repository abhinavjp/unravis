package investigate

import (
	"testing"
	"time"
)

func TestCountBurst(t *testing.T) {
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	inc := Window{Start: base, End: base.Add(60 * time.Minute)}
	ref := Window{Start: base.Add(-120 * time.Minute), End: base}

	cases := []struct {
		name string
		ts   []time.Time
		want BurstCount
	}{
		{"empty", nil, BurstCount{IncMin: 60, RefMin: 120}},
		{"three close one late", []time.Time{at(0), at(2), at(4), at(30)},
			BurstCount{Inc: 4, Peak5: 3, IncMin: 60, RefMin: 120}},
		{"exactly five minutes apart", []time.Time{at(0), at(5)},
			BurstCount{Inc: 2, Peak5: 1, IncMin: 60, RefMin: 120}},
		{"reference row not in peak", []time.Time{at(-10), at(-9), at(1)},
			BurstCount{Inc: 1, Ref: 2, Peak5: 1, IncMin: 60, RefMin: 120}},
		{"unsorted input", []time.Time{at(4), at(0), at(2)},
			BurstCount{Inc: 3, Peak5: 3, IncMin: 60, RefMin: 120}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CountBurst(c.ts, inc, ref); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}

	t.Run("rates", func(t *testing.T) {
		ts := []time.Time{at(1), at(2), at(3), at(4), at(5), at(6)}
		for i := 0; i < 12; i++ {
			ts = append(ts, at(-1-i))
		}
		b := CountBurst(ts, inc, ref)
		if b.IncRate() != 6.0/60 || b.RefRate() != 12.0/120 {
			t.Errorf("rates %v %v", b.IncRate(), b.RefRate())
		}
		if (BurstCount{}).IncRate() != 0 || (BurstCount{}).RefRate() != 0 {
			t.Error("empty window rate must be zero")
		}
	})
}

func TestHTTPErrTimesFold(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	in := Input{HTTPErr: []HTTPErrRow{
		{T: now, Reason: "Timer_HeaderWait"},
		{T: now.Add(time.Minute), Reason: "Connection_Dropped"},
	}}
	got := httpErrTimes(in, "timer_headerwait")
	if len(got) != 1 || !got[0].Equal(now) {
		t.Errorf("got %v", got)
	}
}
