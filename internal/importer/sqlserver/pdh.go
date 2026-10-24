package sqlserver

import (
	"strings"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/wincounters"
	"api-audit-explorer/internal/importer/wincsv"
)

// PDHName is the importer name for SQL Server performance counters.
const PDHName = "sql-pdh"

// NewPDH returns the SQL Server PDH counter importer: the P6 wincounters
// parser (relog CSV, header-bias time, database instances dropped) under the
// name "sql-pdh", detected by DetectPDH.
func NewPDH() importer.Importer { return wincounters.NewImporter(PDHName, DetectPDH) }

// DetectPDH scores a PDH-CSV file whose header names a SQL Server counter
// object (SQLServer: for the default instance, MSSQL$<name>: for a named
// one): 100, else 0. A PDH file without SQL counters is left to win-counters;
// a SQL one scores within importer.TieWindow of win-counters' 95, so the
// registry lists both and the user picks (the parser is the same).
func DetectPDH(head []byte) int {
	if wincounters.DetectPDH(head) == 0 {
		return 0
	}
	hdr := wincsv.HeadText(head)
	if i := strings.IndexAny(hdr, "\r\n"); i >= 0 {
		hdr = hdr[:i]
	}
	hdr = strings.ToLower(hdr)
	if strings.Contains(hdr, `\sqlserver:`) || strings.Contains(hdr, `\mssql$`) {
		return 100
	}
	return 0
}
