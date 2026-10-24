package mongoexports

import (
	"time"

	"api-audit-explorer/internal/importer"
)

// SuppliedKeys declares the settings an export reads: slowms for the profiler
// (whole milliseconds, 0 to an hour) and date for mongostat, whose rows have
// only a time of day. The other two exports read no setting.
func (m impl) SuppliedKeys() map[string]importer.SuppliedSpec {
	switch m.sp.name {
	case NameProfile:
		return map[string]importer.SuppliedSpec{
			"slowms": {Kind: importer.SuppliedNumber, Min: 0, Max: maxSlowms, Integer: true},
		}
	case NameMongostat:
		return map[string]importer.SuppliedSpec{
			"date": {Kind: importer.SuppliedText, MaxLen: 10, Check: validDay},
		}
	}
	return nil
}

// validDay accepts YYYY-MM-DD for a real calendar day in 2000 to 2100.
func validDay(s string) bool {
	if !reDay.MatchString(s) {
		return false
	}
	d, err := time.Parse("2006-01-02", s)
	return err == nil && d.Year() >= 2000 && d.Year() <= 2100
}
