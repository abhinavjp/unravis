package mel

import (
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
)

// SuppliedKeys declares the app-request threshold, the only setting read.
func (*consoleImp) SuppliedKeys() map[string]importer.SuppliedSpec { return applog.SuppliedKeys() }

// SuppliedKeys declares the app-request threshold, the only setting read.
func (*jsonImp) SuppliedKeys() map[string]importer.SuppliedSpec { return applog.SuppliedKeys() }

// SuppliedKeys declares the app-request threshold, the only setting read.
func (*stdoutImp) SuppliedKeys() map[string]importer.SuppliedSpec { return applog.SuppliedKeys() }
