package schedule

import (
	"testing"
	"time"
)

func TestParseCronValid(t *testing.T) {
	cases := []struct {
		expr              string
		secs, mins, hours []int
		domStar, dowStar  bool
	}{
		{"* * * * *", []int{0}, seq(0, 59, 1), seq(0, 23, 1), true, true},
		{"*/15 * * * *", []int{0}, []int{0, 15, 30, 45}, seq(0, 23, 1), true, true},
		{"5,10-12 1 * * *", []int{0}, []int{5, 10, 11, 12}, []int{1}, true, true},
		{"0 8-18/5 * * *", []int{0}, []int{0}, []int{8, 13, 18}, true, true},
		{"10 0 12 * * *", []int{10}, []int{0}, []int{12}, true, true},
	}
	for _, c := range cases {
		spec, unk, err := parseCron(c.expr)
		if err != nil || unk {
			t.Fatalf("%q: unk=%v err=%v", c.expr, unk, err)
		}
		if !equalInts(spec.secs, c.secs) || !equalInts(spec.mins, c.mins) || !equalInts(spec.hours, c.hours) {
			t.Errorf("%q: secs=%v mins=%v hours=%v", c.expr, spec.secs, spec.mins, spec.hours)
		}
		if spec.domStar != c.domStar || spec.dowStar != c.dowStar {
			t.Errorf("%q: star flags %v %v", c.expr, spec.domStar, spec.dowStar)
		}
	}
}

func TestParseCronStartStep(t *testing.T) {
	spec, _, err := parseCron("0 5/10 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if !equalInts(spec.hours, []int{5, 15}) {
		t.Errorf("hours = %v", spec.hours)
	}
}

func TestParseCronUnknown(t *testing.T) {
	// L W # ? and names are valid but unsupported: unknown, never guessed.
	for _, expr := range []string{
		"0 0 L * *", "0 0 LW * *", "0 0 15W * *", "0 0 L-3 * *", "0 0 ? * MON",
		"0 0 * * 5L", "0 0 * * 5#2", "0 0 * JAN *", "0 0 * * MON-FRI", "0 0 * * mon", "0 0 1 JAN-MAR *",
	} {
		spec, unk, err := parseCron(expr)
		if err != nil || !unk || spec != nil {
			t.Errorf("%q: spec=%v unk=%v err=%v, want unknown", expr, spec, unk, err)
		}
	}
}

func TestParseCronMalformed(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * * *", "61 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *",
		"* * * 13 *", "* * * * 8", "5-3 * * * *", "*/0 * * * *", "*/x * * * *", "1,,2 * * * *",
		"? * * * *", "L * * * *", "* * * * #", "a b c d e", sentinel + " * * * *", "0 0 * * =1+1",
		"-5 * * * *", "1-2-3 * * * *", "*/100 * * * *",
	} {
		if spec, unk, err := parseCron(expr); err == nil {
			t.Errorf("%q: accepted (spec=%v unk=%v)", expr, spec != nil, unk)
		}
	}
}

func TestCronDayRule(t *testing.T) {
	both, _, _ := parseCron("0 0 13 * 5") // restricted both: either matches
	if !both.both() {
		t.Fatal("both() should hold")
	}
	// 2001-04-13 is a Friday and the 13th; 2001-04-06 a Friday; 2001-04-13.
	if !both.dayOK(time.April, 13, time.Friday) || !both.dayOK(time.April, 6, time.Friday) || !both.dayOK(time.April, 13, time.Monday) {
		t.Error("either-match rule failed")
	}
	if both.dayOK(time.April, 7, time.Saturday) {
		t.Error("neither matches but dayOK is true")
	}
	star, _, _ := parseCron("0 0 * * 1-5")
	if star.both() || !star.dayOK(time.April, 9, time.Monday) || star.dayOK(time.April, 7, time.Saturday) {
		t.Error("weekday rule failed")
	}
	// 7 and 0 are both Sunday.
	sun, _, _ := parseCron("0 0 * * 7")
	if !sun.dowSunday() {
		t.Error("7 should fold onto Sunday")
	}
	monthOnly, _, _ := parseCron("0 0 * 2 *")
	if monthOnly.dayOK(time.March, 1, time.Thursday) || !monthOnly.dayOK(time.February, 1, time.Thursday) {
		t.Error("month field failed")
	}
}

func (c *cronSpec) dowSunday() bool { return c.dow[0] }

func seq(lo, hi, step int) []int {
	var out []int
	for v := lo; v <= hi; v += step {
		out = append(out, v)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
