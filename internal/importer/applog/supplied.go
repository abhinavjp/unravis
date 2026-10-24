package applog

import "api-audit-explorer/internal/importer"

// SuppliedKeys is the setting every importer built on this package accepts: the
// app-request threshold in milliseconds. The sink reads any number from 0 up and
// falls back to the default with a gap otherwise; the spec stops short of a day.
func SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		SuppliedRequestMinMs: {Kind: importer.SuppliedNumber, Min: 0, Max: 86_400_000},
	}
}
