package nlog

import (
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
)

// SuppliedKeys declares the settings the importer reads: the app-request
// threshold and the owner's statement that the log times are UTC.
func (*textImp) SuppliedKeys() map[string]importer.SuppliedSpec {
	keys := applog.SuppliedKeys()
	keys[SuppliedLogTime] = importer.SuppliedSpec{Kind: importer.SuppliedEnum, Values: []string{"utc"}}
	return keys
}
