package sqlserver

// SeverityOf maps a SQL Server error severity (0 to 25) to the event severity the ERRORLOG importer gives it. The
// Blackbox source importer uses it, so the same error reads the same in both.
func SeverityOf(sev int) string { return sqlSeverity(sev) }
