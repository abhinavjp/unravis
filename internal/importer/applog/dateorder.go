package applog

import (
	"fmt"
	"regexp"

	"api-audit-explorer/internal/importer"
)

// SuppliedDateOrder is the key an owner uses to state whether a log writes slash
// dates day first ("dmy") or month first ("mdy").
const SuppliedDateOrder = "date-order"

// Date orders SuppliedDateOrder accepts.
const (
	DateOrderMDY = "mdy"
	DateOrderDMY = "dmy"
)

// Known-gap sentences for slash dates. Each passes evidence.LintCausal.
const (
	GapDateOrderAssumed = "The date order of slash dates was not stated and no date in the file shows it; " +
		"dates with day and month both 12 or less were read month first."
	GapDateOrderMixed = "A slash date with a day above 12 showed day first after earlier dates had been read month first; " +
		"those earlier dates may have day and month swapped."
)

// DateOrderSpec is the spec of SuppliedDateOrder.
func DateOrderSpec() importer.SuppliedSpec {
	return importer.SuppliedSpec{Kind: importer.SuppliedEnum, Values: []string{DateOrderMDY, DateOrderDMY}}
}

// SuppliedKeysWithDateOrder is SuppliedKeys plus the date-order setting, for the
// importers that read slash dates.
func SuppliedKeysWithDateOrder() map[string]importer.SuppliedSpec {
	keys := SuppliedKeys()
	keys[SuppliedDateOrder] = DateOrderSpec()
	return keys
}

type dateOrder int

const (
	orderUnknown dateOrder = iota
	orderMonthFirst
	orderDayFirst
)

// DateOrder tracks the order of day and month in the slash dates of one file.
// The owner's setting fixes it. Without one, the first date with a field above
// 12 shows it. Until then a date with both leading fields at 12 or less is read
// month first. A nil *DateOrder reads each date on its own, month first when both
// fields fit, and keeps no state; sniffing a head uses that.
type DateOrder struct {
	order     dateOrder
	fixed     bool // the owner stated the order
	ambiguous bool // a date was read under the month-first default
	mixed     bool // day-first showed after dates read month first
}

// NewDateOrder starts the tracker from the supplied value; anything but "mdy"
// or "dmy" leaves the order to be inferred.
func NewDateOrder(supplied string) *DateOrder {
	d := &DateOrder{}
	switch supplied {
	case DateOrderMDY:
		d.order, d.fixed = orderMonthFirst, true
	case DateOrderDMY:
		d.order, d.fixed = orderDayFirst, true
	}
	return d
}

// Resolve reads the two leading numbers of a slash date and returns the month
// and day. ok is false when no order fits them: both above 12, or a field above
// 12 against an order already fixed or shown.
func (d *DateOrder) Resolve(x, y int) (month, day int, ok bool) {
	switch {
	case x > 12 && y > 12:
		return 0, 0, false
	case x > 12: // the first field can only be the day
		if d != nil {
			if d.order == orderMonthFirst {
				return 0, 0, false
			}
			if d.order == orderUnknown && d.ambiguous {
				d.mixed = true
			}
			d.order = orderDayFirst
		}
		return y, x, true
	case y > 12: // the second field can only be the day
		if d != nil {
			if d.order == orderDayFirst {
				return 0, 0, false
			}
			d.order = orderMonthFirst
		}
		return x, y, true
	}
	if d == nil {
		return x, y, true
	}
	if d.order == orderDayFirst {
		return y, x, true
	}
	if d.order == orderUnknown && x != y {
		d.ambiguous = true
	}
	return x, y, true
}

// Gaps adds the sentences the file's dates call for.
func (d *DateOrder) Gaps(add func(string)) {
	if d == nil || d.fixed {
		return
	}
	if d.mixed {
		add(GapDateOrderMixed)
	}
	if d.ambiguous && d.order == orderUnknown {
		add(GapDateOrderAssumed)
	}
}

var slashDate = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})(\s.*)?$`)

// NormalizeSlashDate rewrites a value that starts with a numeric slash date
// with the year last ("5/3/2001 10:00:00 AM") to month first with two-digit
// fields ("03/05/2001 10:00:00 AM") under d, so the month-first layouts read it.
// A value in any other shape is returned unchanged. ok is false when the date
// cannot be read under any order.
func NormalizeSlashDate(v string, d *DateOrder) (string, bool) {
	m := slashDate.FindStringSubmatch(v)
	if m == nil {
		return v, true
	}
	x, y := atoi(m[1]), atoi(m[2])
	month, day, ok := d.Resolve(x, y)
	if !ok {
		return v, false
	}
	return fmt.Sprintf("%02d/%02d/%s%s", month, day, m[3], m[4]), true
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
