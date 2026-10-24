package dotnetcounters

import "api-audit-explorer/internal/importer"

// SuppliedKeys declares the process id the owner collected from (a whole number
// of at most ten digits in the parse; the spec stops at the 32-bit limit).
func (*imp) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		SuppliedPID: {Kind: importer.SuppliedNumber, Min: 0, Max: 4294967295, Integer: true},
	}
}
