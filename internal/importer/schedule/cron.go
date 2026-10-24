package schedule

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// cronSpec is an expanded 5 or 6 field cron expression. Only the operators
// * , - / are understood. L W # ? and month or day names are reported as
// unknown by parseCron: they are never guessed.
type cronSpec struct {
	secs, mins, hours []int
	dom               [32]bool
	mon               [13]bool
	dow               [7]bool // 0 = Sunday; 7 is folded onto 0
	domStar, dowStar  bool
}

// both reports that day-of-month and day-of-week are both restricted.
func (c *cronSpec) both() bool { return !c.domStar && !c.dowStar }

var errCron = errors.New("cron syntax")

type fieldKind int

const (
	fSec fieldKind = iota
	fMin
	fHour
	fDom
	fMon
	fDow
)

var fieldRange = [...][2]int{fSec: {0, 59}, fMin: {0, 59}, fHour: {0, 23}, fDom: {1, 31}, fMon: {1, 12}, fDow: {0, 7}}

const (
	monthNames = "JAN|FEB|MAR|APR|MAY|JUN|JUL|AUG|SEP|OCT|NOV|DEC"
	dayNames   = "SUN|MON|TUE|WED|THU|FRI|SAT"
)

var (
	monNameRE = regexp.MustCompile(`(?i)^(` + monthNames + `)(-(` + monthNames + `))?(/\d{1,2})?$`)
	dowNameRE = regexp.MustCompile(`(?i)^(` + dayNames + `)(-(` + dayNames + `))?(/\d{1,2})?$`)
	// Specials: day-of-month L, LW, L-3, 15W; day-of-week 5L, 5#2; "?" for either.
	domSpecialRE = regexp.MustCompile(`^(\?|L|LW|L-\d{1,2}|\d{1,2}W)$`)
	dowSpecialRE = regexp.MustCompile(`^(\?|\d{1,2}L|\d{1,2}#\d{1,2}|L)$`)
)

// parseCron parses a cron expression. It returns unknown=true for a valid
// expression that uses an unsupported feature, and an error (errCron or a
// field-count error) for a malformed one.
func parseCron(expr string) (spec *cronSpec, unknown bool, err error) {
	toks := strings.Fields(expr)
	if len(toks) != 5 && len(toks) != 6 {
		return nil, false, errCron
	}
	kinds := []fieldKind{fMin, fHour, fDom, fMon, fDow}
	if len(toks) == 6 {
		kinds = append([]fieldKind{fSec}, kinds...)
	}
	spec = &cronSpec{secs: []int{0}}
	for i, tok := range toks {
		set, star, unk, ferr := parseField(tok, kinds[i])
		if ferr != nil {
			return nil, false, ferr
		}
		if unk {
			unknown = true
			continue
		}
		switch kinds[i] {
		case fSec:
			spec.secs = listOf(set)
		case fMin:
			spec.mins = listOf(set)
		case fHour:
			spec.hours = listOf(set)
		case fDom:
			for v := 1; v <= 31; v++ {
				spec.dom[v] = set[v]
			}
			spec.domStar = star
		case fMon:
			for v := 1; v <= 12; v++ {
				spec.mon[v] = set[v]
			}
		case fDow:
			for v := 0; v < 7; v++ {
				spec.dow[v] = set[v]
			}
			spec.dow[0] = spec.dow[0] || set[7]
			spec.dowStar = star
		}
	}
	if unknown {
		return nil, true, nil
	}
	return spec, false, nil
}

func listOf(set []bool) []int {
	var out []int
	for v, ok := range set {
		if ok {
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// parseField parses one field into a membership set indexed by value.
func parseField(tok string, kind fieldKind) (set []bool, star, unknown bool, err error) {
	lo, hi := fieldRange[kind][0], fieldRange[kind][1]
	set = make([]bool, hi+1)
	star = strings.HasPrefix(tok, "*")
	if tok == "" {
		return nil, false, false, errCron
	}
	for _, part := range strings.Split(tok, ",") {
		if part == "" {
			return nil, false, false, errCron
		}
		if isSpecial(part, kind) {
			unknown = true
			continue
		}
		base, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, ok := smallInt(stepStr)
			if !ok || n < 1 || n > hi {
				return nil, false, false, errCron
			}
			step = n
		}
		var a, b int
		switch {
		case base == "*":
			a, b = lo, hi
			if kind == fDow {
				b = 6
			}
		case strings.Contains(base, "-"):
			x, y, _ := strings.Cut(base, "-")
			av, ok1 := smallInt(x)
			bv, ok2 := smallInt(y)
			if !ok1 || !ok2 || av > bv {
				return nil, false, false, errCron
			}
			a, b = av, bv
		default:
			v, ok := smallInt(base)
			if !ok {
				return nil, false, false, errCron
			}
			a, b = v, v
			if hasStep {
				b = hi // "a/n" runs from a to the field maximum
				if kind == fDow {
					b = 6
				}
			}
		}
		if a < lo || b > hi {
			return nil, false, false, errCron
		}
		for v := a; v <= b; v += step {
			set[v] = true
		}
	}
	return set, star, unknown, nil
}

// isSpecial reports whether a part is an unsupported-but-valid construct.
func isSpecial(part string, kind fieldKind) bool {
	switch kind {
	case fDom:
		return domSpecialRE.MatchString(part)
	case fDow:
		return dowSpecialRE.MatchString(part) || dowNameRE.MatchString(part)
	case fMon:
		return monNameRE.MatchString(part)
	}
	return false
}

// smallInt parses 1 to 4 ASCII digits.
func smallInt(s string) (int, bool) {
	if len(s) == 0 || len(s) > 4 || !allDigits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// dayOK applies the standard cron day rule: when either day field starts with
// "*" both must match; otherwise a match on either is enough.
func (c *cronSpec) dayOK(m time.Month, d int, wd time.Weekday) bool {
	if !c.mon[m] {
		return false
	}
	domOK, dowOK := c.dom[d], c.dow[wd]
	if c.domStar || c.dowStar {
		return domOK && dowOK
	}
	return domOK || dowOK
}
