package server

import (
	"testing"
	"time"
)

func axisAt(day, hh, mm, ss int) time.Time {
	return time.Date(2026, 3, day, hh, mm, ss, 0, time.UTC)
}

func TestTracingAxisTable(t *testing.T) {
	d10 := func(hh, mm, ss int) time.Time { return axisAt(10, hh, mm, ss) }
	cases := []struct {
		name       string
		start, end time.Time
		seconds    int64
		buckets    int
	}{
		{"1 h aligned", d10(10, 0, 0), d10(11, 0, 0), 60, 60},
		{"1 h off hour", d10(10, 7, 0), d10(11, 7, 0), 60, 120},
		{"10 s window", d10(10, 0, 0), d10(10, 0, 10), 60, 60},
		{"2 h aligned", d10(10, 0, 0), d10(12, 0, 0), 60, 120},
		{"2 h off hour", d10(10, 7, 0), d10(12, 7, 0), 300, 36},
		{"2 h 1 s", d10(10, 0, 0), d10(12, 0, 1), 300, 36},
		{"10 h", d10(10, 0, 0), d10(20, 0, 0), 300, 120},
		{"20 h", d10(0, 0, 0), d10(20, 0, 0), 600, 120},
		{"24 h aligned", d10(0, 0, 0), axisAt(11, 0, 0, 0), 900, 96},
		{"24 h off hour", d10(10, 7, 0), axisAt(11, 10, 7, 0), 900, 100},
		{"5 d aligned", d10(0, 0, 0), axisAt(15, 0, 0, 0), 3600, 120},
		{"5 d 1 s", d10(0, 0, 0), axisAt(15, 0, 0, 1), 7200, 61},
		{"10 d 1 s", d10(0, 0, 0), axisAt(20, 0, 0, 1), 14400, 61},
		{"20 d 1 s", d10(0, 0, 0), axisAt(30, 0, 0, 1), 21600, 81},
		{"29 d 23 h 59 m", d10(0, 0, 0), axisAt(40, 0, -1, 0), 21600, 120},
		{"30 d aligned", d10(0, 0, 0), axisAt(40, 0, 0, 0), 21600, 120},
		{"30 d at 00:01", d10(0, 1, 0), axisAt(40, 0, 1, 0), 28800, 91},
	}
	for _, c := range cases {
		a := newTracingAxis(c.start, c.end)
		if a.WidthMs != c.seconds*1000 || a.Buckets != c.buckets {
			t.Errorf("%s: width %d s, %d buckets; want %d s, %d", c.name, a.WidthMs/1000, a.Buckets, c.seconds, c.buckets)
		}
		if a.AxisStart.Minute() != 0 || a.AxisStart.Second() != 0 || a.AxisEnd.Minute() != 0 || a.AxisEnd.Second() != 0 {
			t.Errorf("%s: axis %v .. %v is not on the hour", c.name, a.AxisStart, a.AxisEnd)
		}
		if a.AxisStart.After(c.start) || a.AxisEnd.Before(c.end) {
			t.Errorf("%s: axis %v .. %v does not cover the window", c.name, a.AxisStart, a.AxisEnd)
		}
	}
}

func TestTracingAxisInvariants(t *testing.T) {
	spans := []time.Duration{10 * time.Second, 59 * time.Second, time.Minute, 7 * time.Minute, time.Hour, 2 * time.Hour,
		2*time.Hour + time.Second, 10 * time.Hour, 24 * time.Hour, 25 * time.Hour, 5 * 24 * time.Hour, 10 * 24 * time.Hour,
		20 * 24 * time.Hour, 30 * 24 * time.Hour, 31 * 24 * time.Hour, 60 * 24 * time.Hour, 61 * 24 * time.Hour, 93 * 24 * time.Hour, 120 * 24 * time.Hour, 121 * 24 * time.Hour, 186 * 24 * time.Hour}
	offsets := []time.Duration{0, time.Minute, 5*time.Hour + 59*time.Minute, 23*time.Hour + 59*time.Minute}
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	for _, span := range spans {
		for _, off := range offsets {
			start := day.Add(off)
			a := newTracingAxis(start, start.Add(span))
			if a.Buckets > 120 || a.Buckets < 1 {
				t.Errorf("span %v at +%v: %d buckets", span, off, a.Buckets)
			}
			frame := a.AxisEnd.UnixMilli() - a.AxisStart.UnixMilli()
			if int64(a.Buckets)*a.WidthMs < frame {
				t.Errorf("span %v at +%v: %d x %d ms does not cover %d ms", span, off, a.Buckets, a.WidthMs, frame)
			}
			if int64(a.Buckets-1)*a.WidthMs >= frame {
				t.Errorf("span %v at +%v: %d buckets leave an empty last cell", span, off, a.Buckets)
			}
			found := false
			for _, r := range allRungs {
				found = found || r == a.WidthMs
			}
			if !found {
				t.Errorf("span %v at +%v: width %d ms is not a rung", span, off, a.WidthMs)
			}
		}
	}
}

func TestTracingAxisBucketClipping(t *testing.T) {
	sumOK := func(a tracingAxis) (sum int64, count int) {
		for k := 0; k < a.Buckets; k++ {
			from, to, _, ok := a.Bucket(k)
			if ok {
				sum += to.UnixMilli() - from.UnixMilli()
				count++
			}
		}
		return sum, count
	}

	// The real axis for 10:07-11:07 is 60 s wide on 10:00-12:00.
	start, end := axisAt(10, 10, 7, 0), axisAt(10, 11, 7, 0)
	a := newTracingAxis(start, end)
	if _, _, _, ok := a.Bucket(6); ok {
		t.Error("cell 6 (10:06-10:07) lies before the window but is ok")
	}
	if from, to, clipped, ok := a.Bucket(7); !ok || clipped || !from.Equal(start) || !to.Equal(start.Add(time.Minute)) {
		t.Errorf("cell 7 = %v %v clipped %v ok %v", from, to, clipped, ok)
	}
	if from, to, clipped, ok := a.Bucket(66); !ok || clipped || !from.Equal(end.Add(-time.Minute)) || !to.Equal(end) {
		t.Errorf("cell 66 = %v %v clipped %v ok %v", from, to, clipped, ok)
	}
	if _, _, _, ok := a.Bucket(67); ok {
		t.Error("cell 67 starts at the window end (half open) but is ok")
	}
	if _, _, _, ok := a.Bucket(-1); ok {
		t.Error("cell -1 is ok")
	}
	if _, _, _, ok := a.Bucket(a.Buckets); ok {
		t.Error("a cell past the grid is ok")
	}
	if sum, count := sumOK(a); sum != time.Hour.Milliseconds() || count != 60 {
		t.Errorf("sum of ok cells = %d ms in %d cells, want one hour in 60", sum, count)
	}

	// A window that does not meet the 60 s grid: the first and last cells are cut.
	start, end = axisAt(10, 10, 0, 30), axisAt(10, 11, 0, 30)
	a = newTracingAxis(start, end)
	if from, to, clipped, ok := a.Bucket(0); !ok || !clipped || !from.Equal(start) || !to.Equal(axisAt(10, 10, 1, 0)) {
		t.Errorf("first cell = %v %v clipped %v ok %v", from, to, clipped, ok)
	}
	if from, to, clipped, ok := a.Bucket(60); !ok || !clipped || !from.Equal(axisAt(10, 11, 0, 0)) || !to.Equal(end) {
		t.Errorf("last cell = %v %v clipped %v ok %v", from, to, clipped, ok)
	}
	if _, _, _, ok := a.Bucket(61); ok {
		t.Error("cell 61 is ok")
	}
	if sum, _ := sumOK(a); sum != time.Hour.Milliseconds() {
		t.Errorf("sum of ok cells = %d ms, want one hour", sum)
	}

	// A wide cell: 1 h cells on 10:00-12:00 around 10:07-11:07.
	start, end = axisAt(10, 10, 7, 0), axisAt(10, 11, 7, 0)
	wide := tracingAxis{Start: start, End: end, AxisStart: axisAt(10, 10, 0, 0), AxisEnd: axisAt(10, 12, 0, 0), WidthMs: hourMs, Buckets: 2}
	if from, to, clipped, ok := wide.Bucket(0); !ok || !clipped || !from.Equal(start) || !to.Equal(axisAt(10, 11, 0, 0)) {
		t.Errorf("wide cell 0 = %v %v clipped %v ok %v", from, to, clipped, ok)
	}
	if from, to, clipped, ok := wide.Bucket(1); !ok || !clipped || !from.Equal(axisAt(10, 11, 0, 0)) || !to.Equal(end) {
		t.Errorf("wide cell 1 = %v %v clipped %v ok %v", from, to, clipped, ok)
	}
	if sum, _ := sumOK(wide); sum != time.Hour.Milliseconds() {
		t.Errorf("wide sum = %d ms, want one hour", sum)
	}

	// A fully inside cell is not clipped.
	whole := tracingAxis{Start: axisAt(10, 10, 0, 0), End: axisAt(10, 12, 0, 0), AxisStart: axisAt(10, 10, 0, 0), AxisEnd: axisAt(10, 12, 0, 0), WidthMs: hourMs, Buckets: 2}
	if _, _, clipped, ok := whole.Bucket(1); !ok || clipped {
		t.Errorf("an inside cell: clipped %v ok %v", clipped, ok)
	}
}

func TestFloorMsBefore1970(t *testing.T) {
	before := time.Date(1969, 12, 31, 23, 30, 0, 0, time.UTC).UnixMilli()
	want := time.Date(1969, 12, 31, 23, 0, 0, 0, time.UTC).UnixMilli()
	if got := floorMs(before, hourMs); got != want {
		t.Errorf("floorMs = %d, want %d", got, want)
	}
	if got := ceilMs(before, hourMs); got != 0 {
		t.Errorf("ceilMs = %d, want 0", got)
	}
	if got := floorMs(-hourMs, hourMs); got != -hourMs {
		t.Errorf("floorMs on a multiple = %d, want %d", got, -hourMs)
	}
	if got := ceilMs(-hourMs, hourMs); got != -hourMs {
		t.Errorf("ceilMs on a multiple = %d", got)
	}
	if got := floorMs(1, hourMs); got != 0 {
		t.Errorf("floorMs(1) = %d", got)
	}
	if got := ceilMs(1, hourMs); got != hourMs {
		t.Errorf("ceilMs(1) = %d", got)
	}
	// An axis before 1970 floors to the hour as well.
	a := newTracingAxis(time.Date(1969, 12, 31, 23, 30, 0, 0, time.UTC), time.Date(1970, 1, 1, 0, 30, 0, 0, time.UTC))
	if !a.AxisStart.Equal(time.Date(1969, 12, 31, 23, 0, 0, 0, time.UTC)) || !a.AxisEnd.Equal(time.Date(1970, 1, 1, 1, 0, 0, 0, time.UTC)) {
		t.Errorf("axis %v .. %v", a.AxisStart, a.AxisEnd)
	}
}

func TestTracingLadderEdges(t *testing.T) {
	cases := []struct {
		span time.Duration
		want int64
	}{
		{2 * time.Hour, 60}, {2*time.Hour + time.Second, 300}, {10 * time.Hour, 300}, {10*time.Hour + time.Second, 600},
		{20 * time.Hour, 600}, {20*time.Hour + time.Second, 900}, {24 * time.Hour, 900}, {24*time.Hour + time.Second, 3600},
		{5 * 24 * time.Hour, 3600}, {5*24*time.Hour + time.Second, 7200}, {10 * 24 * time.Hour, 7200},
		{10*24*time.Hour + time.Second, 14400}, {20 * 24 * time.Hour, 14400}, {20*24*time.Hour + time.Second, 21600},
		{30 * 24 * time.Hour, 21600}, {30*24*time.Hour + time.Second, 28800}, {60 * 24 * time.Hour, 43200},
		{60*24*time.Hour + time.Second, 86400}, {93 * 24 * time.Hour, 86400}, {120 * 24 * time.Hour, 86400},
		{120*24*time.Hour + time.Second, 172800}, {186 * 24 * time.Hour, 172800},
	}
	for _, c := range cases {
		if got := ladderWidth(c.span.Milliseconds()); got != c.want*1000 {
			t.Errorf("ladderWidth(%v) = %d s, want %d s", c.span, got/1000, c.want)
		}
	}
}

func TestTracingAxisSubMillisecond(t *testing.T) {
	// An end just past an hour mark must still be covered by the axis.
	start := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 10, 11, 0, 0, 500, time.UTC)
	a := newTracingAxis(start, end)
	if a.AxisEnd.Before(end) || !a.AxisEnd.Equal(time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("axis end %v does not cover %v", a.AxisEnd, end)
	}
}
