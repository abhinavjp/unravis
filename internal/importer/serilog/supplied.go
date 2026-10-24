package serilog

import (
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
)

// SuppliedKeys declares the app-request threshold, the only setting read.
func (*clefImp) SuppliedKeys() map[string]importer.SuppliedSpec { return applog.SuppliedKeys() }

// SuppliedKeys declares the app-request threshold, the only setting read.
func (*textImp) SuppliedKeys() map[string]importer.SuppliedSpec { return applog.SuppliedKeys() }
