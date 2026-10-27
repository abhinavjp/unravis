package blackbox

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func jsonLine(s Sample) (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
func itoa(i int) string           { return strconv.Itoa(i) }

var t0 = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

func TestGapBetweenRule(t *testing.T) {
	// 15 s interval: a gap is more than 45 s.
	if _, ok := GapBetween(t0, t0.Add(45*time.Second), 15, CauseLate); ok {
		t.Fatal("45 s is not a gap")
	}
	g, ok := GapBetween(t0, t0.Add(46*time.Second), 15, CauseLate)
	if !ok || g.Seconds != 46 || g.Cause != CauseLate {
		t.Fatalf("%+v %v", g, ok)
	}
}

func TestDetectGapsSkipsOutOfOrder(t *testing.T) {
	times := []time.Time{t0, t0.Add(15 * time.Second), t0.Add(10 * time.Second), t0.Add(30 * time.Second), t0.Add(200 * time.Second), t0.Add(215 * time.Second)}
	gaps := DetectGaps(times, 15, CauseRestart)
	if len(gaps) != 1 || gaps[0].From != t0.Add(30*time.Second) || gaps[0].Seconds != 170 {
		t.Fatalf("%+v", gaps)
	}
}

func TestIsLate(t *testing.T) {
	if IsLate(time.Time{}, t0, 15) {
		t.Fatal("no sample yet is not late")
	}
	if IsLate(t0, t0.Add(45*time.Second), 15) || !IsLate(t0, t0.Add(46*time.Second), 15) {
		t.Fatal("boundary")
	}
}

func TestSampleOmitsMissingFields(t *testing.T) {
	z := 0.0
	line, err := jsonLine(Sample{V: 1, Run: "r", Seq: 1, T: t0, CPU: &z})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(line, `"cpu":0`) {
		t.Fatal("a real zero is kept: " + line)
	}
	if contains(line, "memAvail") || contains(line, "netIn") {
		t.Fatal("absent values must not appear: " + line)
	}
}
