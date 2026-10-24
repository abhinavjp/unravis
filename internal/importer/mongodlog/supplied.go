package mongodlog

import "api-audit-explorer/internal/importer"

// SuppliedKeys declares slowms, the owner's profiler threshold in whole
// milliseconds (0 to an hour, as the parse reads it).
func (Importer) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		"slowms": {Kind: importer.SuppliedNumber, Min: 0, Max: maxSlowms, Integer: true},
	}
}
