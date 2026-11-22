package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// BL-430: the case series route takes an optional width in pixels.
func TestSeriesWidthCutsBucketsAndAddsFirstLast(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)

	// Without width the answer is the one it always was: no first, last or width on the wire.
	plain := e.get(seriesURL(id, seriesFrom, seriesTo, "10000", "kind=toy;field=duration"))
	if plain.Code != http.StatusOK {
		t.Fatalf("plain = %d %s", plain.Code, plain.Body)
	}
	for _, key := range []string{`"first"`, `"last"`, `"width"`} {
		if strings.Contains(plain.Body.String(), key) {
			t.Errorf("answer without width carries %s: %s", key, plain.Body)
		}
	}

	// A width alone (no bucket): 60 s in 3 pixels is a 20 001 ms bucket.
	res := e.get(seriesURL(id, seriesFrom, seriesTo, "", "kind=toy;field=duration") + "&width=3")
	if res.Code != http.StatusOK {
		t.Fatalf("width = %d %s", res.Code, res.Body)
	}
	var out struct {
		BucketMs int64 `json:"bucketMs"`
		Buckets  int   `json:"buckets"`
		Width    int   `json:"width"`
		Series   []struct {
			Count  int64 `json:"count"`
			Points []struct {
				T     int64    `json:"t"`
				Min   float64  `json:"min"`
				Max   float64  `json:"max"`
				Count int64    `json:"count"`
				First *float64 `json:"first"`
				Last  *float64 `json:"last"`
			} `json:"points"`
		} `json:"series"`
	}
	decodeBody(t, res, &out)
	if out.Width != 3 || out.BucketMs != 20_001 || out.Buckets != 3 || len(out.Series) != 1 {
		t.Fatalf("header: %+v", out)
	}
	// kind=toy: 10, 20, 30 (s 1..3), 50 (s 12) fall in the first 20 s; 90 (s 59) in the third.
	pts := out.Series[0].Points
	if len(pts) != 2 || out.Series[0].Count != 5 {
		t.Fatalf("points: %+v", pts)
	}
	if p := pts[0]; p.Count != 4 || p.Min != 10 || p.Max != 50 || p.First == nil || *p.First != 10 || *p.Last != 50 {
		t.Errorf("bucket 0: %+v", p)
	}
	if p := pts[1]; p.Count != 1 || *p.First != 90 || *p.Last != 90 {
		t.Errorf("bucket 2: %+v", p)
	}

	// A bucket that would be refused as too many is coarsened when a width is given.
	if res := e.get(seriesURL(id, seriesFrom, seriesTo, "10", "kind=toy") + "&width=100"); res.Code != http.StatusOK {
		t.Errorf("bucket 10 with width = %d %s", res.Code, res.Body)
	}
	for name, w := range map[string]string{"zero": "0", "negative": "-4", "word": "wide", "fraction": "1.5"} {
		res := e.get(seriesURL(id, seriesFrom, seriesTo, "1000", "kind=toy") + "&width=" + w)
		if f := failureOf(t, res); res.Code != http.StatusBadRequest || f.Reason != "bad-width" {
			t.Errorf("width %s: %d %+v", name, res.Code, f)
		}
	}
	// An explicit bucket below 1 is still refused when a width is given.
	for _, bad := range []string{"0", "-5"} {
		res := e.get(seriesURL(id, seriesFrom, seriesTo, bad, "kind=toy") + "&width=100")
		if f := failureOf(t, res); res.Code != http.StatusBadRequest || f.Reason != "bad-bucket" {
			t.Errorf("bucket %s with width: %d %+v", bad, res.Code, f)
		}
	}
	// No bucket and no width is still bad-bucket.
	if f := failureOf(t, e.get(seriesURL(id, seriesFrom, seriesTo, "", "kind=toy"))); f.Reason != "bad-bucket" {
		t.Errorf("no bucket: %+v", f)
	}
}

type bbM4Answer struct {
	BucketSeconds int `json:"bucketSeconds"`
	Lines         []struct {
		Key    string      `json:"key"`
		Points [][]float64 `json:"points"`
	} `json:"lines"`
	Processes []struct {
		Name string      `json:"name"`
		CPU  [][]float64 `json:"cpu"`
		WS   [][]float64 `json:"workingSet"`
	} `json:"processes"`
}

// BL-430 on the Blackbox series route, read through the same reader as a real recording.
func TestBlackboxSeriesWidthAndM4Shape(t *testing.T) {
	b := newBBEnv(t, nil)
	spikeAt := invT0.Add(100*time.Minute + 15*time.Second)
	b.world.put(share, bbRec("web-b", func(r *simRec) {
		r.CPU = func(at time.Time) float64 {
			if at.Equal(spikeAt) {
				return 99 // one sample, one reading: must survive any downsampling
			}
			return 20 + float64(at.Minute()%5)
		}
	}))
	it := b.mustAdd(share, "")
	win := `"start":"` + invT0.Add(-30*time.Minute).Format(time.RFC3339) + `","end":"` + invT0.Add(3*time.Hour).Format(time.RFC3339) + `"`
	path := "/api/blackbox/" + it.ID + "/series"
	cpu := func(a bbM4Answer) [][]float64 {
		for _, l := range a.Lines {
			if l.Key == "cpu" {
				return l.Points
			}
		}
		t.Fatalf("no cpu line")
		return nil
	}
	get := func(extra string) (bbM4Answer, string) {
		t.Helper()
		res := b.post(path, `{`+win+extra+`}`)
		if res.Code != http.StatusOK {
			t.Fatalf("series %s = %d %s", extra, res.Code, res.Body)
		}
		var a bbM4Answer
		decodeBody(t, res, &a)
		return a, res.Body.String()
	}

	// Default: unchanged shape, [t, avg, lo, hi], at most 240 points.
	def, _ := get(``)
	if p := cpu(def); len(p) == 0 || len(p) > 240 || len(p[0]) != 4 {
		t.Fatalf("default shape: %d points, %v", len(p), p[:1])
	}
	// width alone cuts the buckets to the width and keeps the old shape.
	for _, w := range []int{60, 200} {
		a, _ := get(`,"width":` + strconv.Itoa(w))
		p := cpu(a)
		if len(p) == 0 || len(p) > w || len(p[0]) != 4 {
			t.Fatalf("width %d: %d points, shape %v", w, len(p), p[0])
		}
	}
	// M4 shape at widths 60, 600 and 3000 (above 600 clamps to 600): the spike is in a bucket's high and in first or last when it stands alone.
	for _, w := range []int{60, 600, 3000} {
		a, _ := get(`,"width":` + strconv.Itoa(w) + `,"shape":"m4"`)
		p := cpu(a)
		if len(p) == 0 || len(p) > w || len(p) > 600 || len(p[0]) != 6 {
			t.Fatalf("m4 width %d: %d points, %v", w, len(p), p[0])
		}
		hi, found := 0.0, false
		for _, pt := range p {
			if pt[3] > hi {
				hi = pt[3]
			}
			if pt[2] > pt[3] || pt[4] < pt[2] || pt[4] > pt[3] || pt[5] < pt[2] || pt[5] > pt[3] {
				t.Fatalf("m4 width %d: first/last outside lo..hi: %v", w, pt)
			}
			found = found || pt[3] == 99
		}
		if hi != 99 || !found {
			t.Fatalf("m4 width %d: the spike was lost, highest = %v", w, hi)
		}
		for _, pr := range a.Processes {
			for _, pt := range append(pr.CPU, pr.WS...) {
				if len(pt) != 6 {
					t.Fatalf("process point shape: %v", pt)
				}
			}
		}
	}
	// m4 without width: the points are the default count, with first and last.
	a, _ := get(`,"shape":"m4"`)
	if p := cpu(a); len(p) > 240 || len(p[0]) != 6 {
		t.Fatalf("m4 only: %v", p[0])
	}
	// A bad shape or width is refused.
	for _, extra := range []string{`,"shape":"m5"`, `,"width":-1`} {
		if res := b.post(path, `{`+win+extra+`}`); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", extra, res.Code)
		}
	}
}

func TestBBSeriesPointCount(t *testing.T) {
	for _, tc := range []struct{ points, width, want int }{
		{0, 0, 240}, {-3, 0, 240}, {100, 0, 100}, {9999, 0, 600},
		{0, 90, 90}, {0, 3000, 600}, {400, 90, 90}, {50, 90, 50}, {500, 3000, 500}, {700, 3000, 600},
	} {
		if got := bbSeriesPointCount(tc.points, tc.width); got != tc.want {
			t.Errorf("points %d width %d = %d, want %d", tc.points, tc.width, got, tc.want)
		}
	}
}

// First and last follow the sample times, not the order the samples are folded in.
func TestBBSeriesM4FirstLastByTimeAndTies(t *testing.T) {
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	mk := func(sec int, cpu float64) blackbox.Sample {
		s := fullSample(start.Add(time.Duration(sec)*time.Second), sec)
		s.CPU = f64(cpu)
		return s
	}
	// One bucket (30 s cadence floor over a 60 s window with 2 points): three samples out of order, two on the same second.
	samples := []blackbox.Sample{mk(20, 3), mk(2, 1), mk(2, 7), mk(10, 9)}
	win := bbWindow{Start: start, End: start.Add(25 * time.Second)}
	ans := buildBBSeriesShape(blackbox.Manifest{IntervalSeconds: 30}, samples, blackbox.ReadReport{}, blackbox.StateInfo{}, win, 1, true, true)
	for _, l := range ans.Lines {
		if l.Key != "cpu" {
			continue
		}
		if len(l.M4) != 1 || l.M4[0][4] != 1 || l.M4[0][5] != 3 || l.M4[0][2] != 1 || l.M4[0][3] != 9 {
			t.Fatalf("cpu m4 = %v", l.M4)
		}
		return
	}
	t.Fatal("no cpu line")
}

// A window the recording fills end to end: with width or shape m4 the answer holds at most `points` buckets, never one more.
func TestBBSeriesM4NeverMoreBucketsThanPoints(t *testing.T) {
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	var samples []blackbox.Sample
	for i := 0; i <= 600; i++ { // one sample a second, 600 s, both ends included
		s := fullSample(start.Add(time.Duration(i)*time.Second), i)
		s.Seq = uint64(i + 1)
		samples = append(samples, s)
	}
	win := bbWindow{Start: start, End: start.Add(600 * time.Second)}
	man := blackbox.Manifest{IntervalSeconds: 1}
	for _, points := range []int{60, 7, 600} {
		ans := buildBBSeriesShape(man, samples, blackbox.ReadReport{}, blackbox.StateInfo{}, win, points, true, true)
		found := false
		for _, l := range ans.Lines {
			if l.Key != "cpu" {
				continue
			}
			found = true
			if len(l.M4) == 0 || len(l.M4) > points {
				t.Fatalf("points %d: %d cpu points", points, len(l.M4))
			}
			for _, p := range l.M4 {
				if p[3] < p[2] {
					t.Fatalf("bad point %v", p)
				}
			}
		}
		if !found {
			t.Fatal("no cpu line")
		}
	}
}
