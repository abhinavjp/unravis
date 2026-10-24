package redis

import (
	"time"

	"api-audit-explorer/internal/importer"
)

// SuppliedKeys declares the settings the owner reads from CONFIG GET and types
// in: the SLOWLOG threshold in microseconds, its ring length, and the time the
// SLOWLOG was read (RFC 3339, years 2000 to 2100, as the parse reads it).
func (Slowlog) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		slowThresholdK: {Kind: importer.SuppliedNumber, Min: 0, Max: float64(maxSlowThreshUS), Integer: true},
		slowMaxLenK:    {Kind: importer.SuppliedNumber, Min: 0, Max: 1 << 20, Integer: true},
		suppliedExpAtK: {Kind: importer.SuppliedText, MaxLen: 64, Check: validExportedAt},
	}
}

// SuppliedKeys declares latency-monitor-threshold in milliseconds.
func (Latency) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		latThresholdK: {Kind: importer.SuppliedNumber, Min: 0, Max: float64(maxLatThresh), Integer: true},
	}
}

func validExportedAt(s string) bool {
	t, err := time.Parse(time.RFC3339, s)
	return err == nil && t.Year() >= 2000 && t.Year() <= 2100
}
