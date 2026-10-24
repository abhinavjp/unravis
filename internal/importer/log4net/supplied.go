package log4net

import (
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
)

// SuppliedKeys declares the settings the text importer reads: the app-request
// threshold, the conversionPattern value (checked by the importer's own
// pattern compiler, so a pattern it cannot read is refused up front) and the
// owner's statement that zone-less log times are UTC.
func (*textImp) SuppliedKeys() map[string]importer.SuppliedSpec {
	keys := applog.SuppliedKeys()
	keys[SuppliedPattern] = importer.SuppliedSpec{
		Kind: importer.SuppliedText, MaxLen: 512,
		Check: func(p string) bool { _, why := compile(p); return why == "" },
	}
	keys[SuppliedLogTime] = importer.SuppliedSpec{Kind: importer.SuppliedEnum, Values: []string{"utc"}}
	return keys
}

// SuppliedKeys declares the app-request threshold, the only setting the XML
// importer reads (its times are Unix epochs).
func (*xmlImp) SuppliedKeys() map[string]importer.SuppliedSpec { return applog.SuppliedKeys() }
