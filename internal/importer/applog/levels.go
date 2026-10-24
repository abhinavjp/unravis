package applog

import "strings"

// levelSeverity maps every level spelling of the app-log frameworks (plan
// §2.3, lower-cased) to the event severity. The spellings are the export's
// appLogLevels list, so a stored level always survives the export.
//
//   - log4net DEBUG/INFO/WARN/ERROR/FATAL (and TRACE)
//   - Serilog VRB/DBG/INF/WRN/ERR/FTL and the CLEF names
//   - MEL trce/dbug/info/warn/fail/crit and the JSON names (None included)
//   - pino 10..60 and the winston names
var levelSeverity = map[string]string{
	"trace": "", "trce": "", "verbose": "", "vrb": "", "debug": "", "dbug": "", "dbg": "",
	"silly": "", "none": "", "10": "", "20": "",
	// winston "http" sits below "info".
	"http": "",
	"info": "info", "inf": "info", "information": "info", "30": "info",
	"warn": "warning", "wrn": "warning", "warning": "warning", "40": "warning",
	"error": "error", "err": "error", "fail": "error", "50": "error",
	"fatal": "critical", "ftl": "critical", "crit": "critical", "critical": "critical", "60": "critical",
}

// Level checks a level spelling. It returns the spelling to store (the input
// as written, trimmed) and its severity; ok is false for a spelling outside
// the fixed set, which is then never stored.
func Level(v string) (stored, severity string, ok bool) {
	v = strings.TrimSpace(v)
	sev, ok := levelSeverity[strings.ToLower(v)]
	if !ok {
		return "", "", false
	}
	return v, sev, true
}

// atLeastWarning reports a severity of warning or above.
func atLeastWarning(sev string) bool {
	return sev == "warning" || sev == "error" || sev == "critical"
}
