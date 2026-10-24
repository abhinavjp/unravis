package dotnetcounters

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
)

// dateOrder is what the file has shown so far about the order of day and month
// in its culture-formatted dates.
type dateOrder int

const (
	orderUnknown dateOrder = iota
	orderMonthFirst
	orderDayFirst
)

// cultureState carries the date order across the rows of one CSV file.
type cultureState struct {
	order     dateOrder // confirmed by a row with a day above 12; unknown until then
	ambiguous bool      // an earlier row had both leading fields at 12 or less
	mixed     bool      // a day-first row came after rows read month-first
}

// cultureTime is the CSV timestamp as DateTime.ToString() writes it in the
// exporting machine's culture: three numeric date fields (year first or last),
// a time with seconds and an optional AM/PM marker. A fraction of a second is
// accepted and dropped.
var cultureTime = regexp.MustCompile(`^(\d{1,4})([/.\-])(\d{1,2})([/.\-])(\d{1,4}),?\s+(\d{1,2})[:.](\d{2})[:.](\d{2})(?:\.\d+)?\s*([AaPp][Mm])?$`)

// timeEnd matches the start of a line up to the end of its time of day; the
// comma after it separates the timestamp from the provider.
var timeEnd = regexp.MustCompile(`^\s*\d{1,4}[/.\-]\d{1,2}[/.\-]\d{1,4},?\s+\d{1,2}[:.]\d{2}[:.]\d{2}(?:\.\d+)?(?:\s*[AaPp][Mm])?`)

const isoLayout = "2006-01-02T15:04:05"

// parseCSVTime turns a culture timestamp into UTC through the host zone. It
// returns a row reason when the row is to be rejected; a nil loc is the caller's
// whole-file refusal and never reaches here.
func parseCSVTime(s string, loc *time.Location, st *cultureState) (time.Time, importer.Reason, bool) {
	m := cultureTime.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, importer.ReasonBadTime, false
	}
	a, sep1, b, sep2, c := m[1], m[2], m[3], m[4], m[5]
	if sep1 != sep2 {
		return time.Time{}, importer.ReasonBadTime, false
	}
	var year, month, day int
	switch {
	case len(a) == 4 && len(c) <= 2: // year first: unambiguous
		year, month, day = atoi(a), atoi(b), atoi(c)
	case len(c) == 4 && len(a) <= 2:
		x, y := atoi(a), atoi(b)
		year = atoi(c)
		switch {
		case x > 12 && y > 12:
			return time.Time{}, importer.ReasonBadTime, false
		case x > 12: // the first field can only be the day
			if st.order == orderMonthFirst {
				return time.Time{}, importer.ReasonBadTime, false
			}
			if st.order == orderUnknown && st.ambiguous {
				st.mixed = true
			}
			st.order = orderDayFirst
			day, month = x, y
		case y > 12: // the second field can only be the day
			if st.order == orderDayFirst {
				return time.Time{}, importer.ReasonBadTime, false
			}
			st.order = orderMonthFirst
			month, day = x, y
		default:
			dayFirst := st.order == orderDayFirst
			if st.order == orderUnknown {
				st.ambiguous = true
				dayFirst = sep1 != "/" // slash dates are read month first, dot and hyphen dates day first
			}
			if dayFirst {
				day, month = x, y
			} else {
				month, day = x, y
			}
		}
	default:
		return time.Time{}, importer.ReasonBadTime, false
	}
	hour, minute, second := atoi(m[6]), atoi(m[7]), atoi(m[8])
	if ap := strings.ToLower(m[9]); ap != "" {
		if hour < 1 || hour > 12 {
			return time.Time{}, importer.ReasonBadTime, false
		}
		hour %= 12
		if ap == "pm" {
			hour += 12
		}
	}
	if month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, importer.ReasonBadTime, false
	}
	wall := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if wall.Day() != day || int(wall.Month()) != month { // 30 February
		return time.Time{}, importer.ReasonBadTime, false
	}
	res, err := clock.ParseLocal(isoLayout, wall.Format(isoLayout), loc)
	if err != nil {
		return time.Time{}, importer.ReasonBadTime, false
	}
	switch {
	case res.Nonexistent:
		return time.Time{}, importer.ReasonDSTNonexistent, false
	case res.Ambiguous:
		return time.Time{}, importer.ReasonDSTAmbiguous, false
	}
	return res.UTC, "", true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s) // the regexp guarantees digits, at most four
	return n
}

var jsonOffsetLayouts = []string{"2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05Z07:00"}
var jsonLocalLayouts = []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"}

// parseJSONTime reads the JSON "timestamp": ISO 8601 with a numeric offset
// (the exporter's round-trip format), or with "Z" for UTC. Without either it is
// host-local time and needs loc. needLoc is true when the value is host-local and
// loc is nil: the caller refuses the file.
func parseJSONTime(s string, loc *time.Location) (utc time.Time, rule string, reason importer.Reason, needLoc, ok bool) {
	s = strings.TrimSpace(s)
	for _, l := range jsonOffsetLayouts {
		if res, err := clock.ParseLocal(l, s, time.UTC); err == nil {
			rule = clock.RuleOffsetInField
			if strings.HasSuffix(s, "Z") {
				rule = clock.RuleUTCField
			}
			return res.UTC, rule, "", false, true
		}
	}
	for _, l := range jsonLocalLayouts {
		if loc == nil {
			if _, err := time.Parse(l, s); err == nil {
				return time.Time{}, "", "", true, false
			}
			continue
		}
		if res, err := clock.ParseLocal(l, s, loc); err == nil {
			switch {
			case res.Nonexistent:
				return time.Time{}, "", importer.ReasonDSTNonexistent, false, false
			case res.Ambiguous:
				return time.Time{}, "", importer.ReasonDSTAmbiguous, false, false
			}
			return res.UTC, clock.RuleHostLocal, "", false, true
		}
	}
	return time.Time{}, "", importer.ReasonBadTime, false, false
}
