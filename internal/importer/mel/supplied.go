package mel

import (
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
)

// SuppliedKeys declares the app-request threshold and the date order of slash dates.
func (*consoleImp) SuppliedKeys() map[string]importer.SuppliedSpec {
	return applog.SuppliedKeysWithDateOrder()
}

// SuppliedKeys declares the app-request threshold and the date order of slash dates.
func (*jsonImp) SuppliedKeys() map[string]importer.SuppliedSpec {
	return applog.SuppliedKeysWithDateOrder()
}

// SuppliedKeys declares the app-request threshold and the date order of slash dates.
func (*stdoutImp) SuppliedKeys() map[string]importer.SuppliedSpec {
	return applog.SuppliedKeysWithDateOrder()
}
