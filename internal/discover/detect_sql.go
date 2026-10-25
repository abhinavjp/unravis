package discover

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// SQLDetector finds the SQL Server error log set and notes the files in the same
// folder that Unravis cannot read directly. It reads three things and keeps
// nothing else (spec 5, ruling D3):
//
//   - the service names, to find MSSQLSERVER and MSSQL$<instance>;
//   - from the registry allowlist, the instance ID and the startup argument that
//     starts with -e (the error log path); the -d and -l arguments are read by
//     the registry call but dropped on the spot;
//   - one folder listing, through FS.ReadDir, for ERRORLOG*, system_health*.xel
//     and SQLAGENT.OUT.
//
// The .xel and SQLAGENT.OUT files are reported as export-needed with the step
// to export them; they are never opened or parsed (ruling D9). It makes no
// connection to SQL Server (ruling D8).
type SQLDetector struct{}

// Name is the detector name used in findings and gaps.
func (SQLDetector) Name() string { return "sql" }

const (
	sqlInstanceNamesKey = `HKLM\SOFTWARE\Microsoft\Microsoft SQL Server\Instance Names\SQL`
	sqlMaxStartupArgs   = 64
	sqlErrorLogLabel    = "SQL Server error log"
)

var (
	sqlNamedService = regexp.MustCompile(`(?i)^MSSQL\$([A-Za-z0-9_#$-]{1,64})$`)
	sqlInstanceID   = regexp.MustCompile(`^[A-Za-z0-9_$#.-]{1,128}$`)
	sqlXEFile       = regexp.MustCompile(`(?i)^system_health[A-Za-z0-9_.-]*\.xel$`)
	sqlAgentFile    = regexp.MustCompile(`(?i)^SQLAGENT\.OUT$`)
	sqlDashE        = regexp.MustCompile(`\s-[A-Za-z]`)
)

// SQLErrorLogArg reads one SQL Server startup argument as stored in a
// SQLArg<n> registry value and returns the error log path when the argument is
// -e, and nothing else. It accepts the value bare (-eC:\a b\ERRORLOG), with a
// space (-e C:\a b\ERRORLOG) and quoted (-e"C:\a b\ERRORLOG"). An unquoted value
// stops at the next " -x" option in case two arguments share one value. Any
// other argument, including -d and -l, returns ok false and is not looked at.
func SQLErrorLogArg(value string) (path string, ok bool) {
	v := strings.TrimSpace(value)
	if len(v) < 3 || v[0] != '-' || v[1] != 'e' {
		return "", false
	}
	rest := strings.TrimLeft(v[2:], " \t")
	if rest == "" {
		return "", false
	}
	if rest[0] == '"' {
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return "", false
		}
		after := strings.TrimSpace(rest[end+2:])
		if after != "" && !strings.HasPrefix(after, "-") {
			return "", false
		}
		path = rest[1 : 1+end]
	} else {
		if loc := sqlDashE.FindStringIndex(rest); loc != nil {
			rest = rest[:loc[0]]
		}
		path = strings.TrimSpace(rest)
	}
	return path, path != ""
}

// Detect looks for SQL Server services and their error log folders.
func (d SQLDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	const name = "sql"
	if env.Services == nil || env.FS == nil || env.Registry == nil {
		return nil, []Gap{{Detector: name, Kind: KindLogSet, Display: sqlErrorLogLabel, Reason: ReasonUnreadable}}
	}
	svcs, err := env.Services.List()
	if err != nil {
		return nil, []Gap{GapFromError(name, KindLogSet, sqlErrorLogLabel, err)}
	}
	var findings []Finding
	var gaps []Gap
	for _, svc := range svcs {
		if ctx.Err() != nil {
			break
		}
		inst, named, ok := sqlInstanceOf(svc.Name)
		if !ok {
			continue
		}
		if svc.Image != "" && !strings.EqualFold(svc.Image, "sqlservr.exe") {
			continue
		}
		f, g := sqlOne(env, inst, named)
		findings = append(findings, f...)
		gaps = append(gaps, g...)
	}
	return findings, gaps
}

// sqlInstanceOf maps a service name to its instance name.
func sqlInstanceOf(service string) (instance string, named, ok bool) {
	if strings.EqualFold(service, "MSSQLSERVER") {
		return "MSSQLSERVER", false, true
	}
	if m := sqlNamedService.FindStringSubmatch(service); m != nil {
		return m[1], true, true
	}
	return "", false, false
}

func sqlLabel(prefix, instance string, named bool) string {
	if named {
		return prefix + ", instance " + instance
	}
	return prefix
}

func sqlOne(env Env, instance string, named bool) ([]Finding, []Gap) {
	const name = "sql"
	label := sqlLabel(sqlErrorLogLabel, instance, named)
	gap := func(err error) ([]Finding, []Gap) {
		return nil, []Gap{GapFromError(name, KindLogSet, label, err)}
	}
	reason := func(r Reason) ([]Finding, []Gap) {
		return nil, []Gap{{Detector: name, Kind: KindLogSet, Display: label, Reason: r}}
	}

	// Instance name to instance ID.
	id, err := env.Registry.String(sqlInstanceNamesKey, instance)
	if err != nil {
		return gap(err)
	}
	id = strings.TrimSpace(id)
	if !sqlInstanceID.MatchString(id) {
		return reason(ReasonConfigUnparsed)
	}

	// The -e startup argument. Every other SQLArg value is dropped as read.
	params := `HKLM\SOFTWARE\Microsoft\Microsoft SQL Server\` + id + `\MSSQLServer\Parameters`
	raw, found := "", false
	for i := 0; i < sqlMaxStartupArgs && !found; i++ {
		v, err := env.Registry.String(params, fmt.Sprintf("SQLArg%d", i))
		if err != nil {
			if IsNotFound(err) {
				break
			}
			return gap(err)
		}
		raw, found = SQLErrorLogArg(v)
	}
	if !found {
		return reason(ReasonNotFound)
	}
	logPath, perr := ValidLogPath(raw, "")
	if perr != nil {
		return gap(perr)
	}
	dir, base := WinDir(logPath), WinBase(logPath)
	if dir == "" || base == "" {
		return reason(ReasonUnsafePath)
	}
	if err := dbFinalCheck(env.FS, dir); err != nil {
		return gap(err)
	}
	entries, err := env.FS.ReadDir(dir)
	var gaps []Gap
	if err != nil {
		if !sqlTooMany(err) {
			return gap(err)
		}
		gaps = append(gaps, GapFromError(name, KindLogSet, label, err))
	}

	// Sort the listing into the three sets. A reparse point is never followed.
	setRe := regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(base) + `(\.[0-9]{1,3})?$`)
	var logs, xe, agent []FileInfo
	skippedReparse := false
	for _, e := range entries {
		var into *[]FileInfo
		switch {
		case setRe.MatchString(e.Name):
			into = &logs
		case sqlXEFile.MatchString(e.Name):
			into = &xe
		case sqlAgentFile.MatchString(e.Name):
			into = &agent
		default:
			continue
		}
		if e.Reparse {
			skippedReparse = true
			continue
		}
		if e.IsDir {
			continue
		}
		*into = append(*into, e)
	}
	if skippedReparse {
		gaps = append(gaps, Gap{Detector: name, Kind: KindLogSet, Display: label, Reason: ReasonUnsafePath})
	}

	var findings []Finding
	if len(logs) == 0 {
		gaps = append(gaps, Gap{Detector: name, Kind: KindLogSet, Display: label, Reason: ReasonNotFound})
	} else {
		f, g := sqlLogSet(env, dir, label, logs)
		findings = append(findings, f...)
		gaps = append(gaps, g...)
	}
	if len(xe) > 0 {
		findings = append(findings, sqlExportFinding(sqlLabel("SQL Server system_health events", instance, named),
			"discover.sqlxe.shows", "discover.sqlxe.how", xe))
	}
	if len(agent) > 0 {
		findings = append(findings, sqlExportFinding(sqlLabel("SQL Server Agent log", instance, named),
			"discover.sqlagent.shows", "discover.sqlagent.how", agent))
	}
	return findings, gaps
}

func sqlTooMany(err error) bool { return ReasonFromError(err) == ReasonTooLarge }

// sqlLogSet builds the sql-errorlog finding for the files of one folder.
func sqlLogSet(env Env, dir, label string, logs []FileInfo) ([]Finding, []Gap) {
	const name = "sql"
	// Current file first (no number), then ERRORLOG.1, .2, ...
	sort.SliceStable(logs, func(i, j int) bool {
		return sqlSuffix(logs[i].Name) < sqlSuffix(logs[j].Name)
	})
	var t dbTally
	var paths, names []string
	newest := logs[0]
	for _, fi := range logs {
		p := WinJoin(dir, fi.Name)
		if _, err := ValidateAbsPath(p); err != nil {
			return nil, []Gap{GapFromError(name, KindLogSet, label, err)}
		}
		paths = append(paths, p)
		names = append(names, fi.Name)
		t.add(fi)
		if fi.ModTime.After(newest.ModTime) {
			newest = fi
		}
	}
	agree, err := dbSniffAgrees(env, WinJoin(dir, newest.Name), "sql-errorlog")
	if err != nil {
		return nil, []Gap{GapFromError(name, KindLogSet, label, err)}
	}
	f := Finding{
		Detector: name, Kind: KindLogSet, Importer: "sql-errorlog", HostLabel: localHostLabel,
		Display:    label + " (" + dbSetDetail(names) + ")",
		Confidence: dbConfidence(true, agree), Shows: "discover.sql.shows",
		SizeBytes: t.size, Files: t.files, Oldest: t.oldest, Newest: t.newest, Cost: CostForSize(t.size),
	}
	f.SetPaths(paths...)
	return []Finding{f}, nil
}

// sqlSuffix orders ERRORLOG before ERRORLOG.1 before ERRORLOG.2.
func sqlSuffix(fileName string) int {
	i := strings.LastIndexByte(fileName, '.')
	if i < 0 {
		return -1
	}
	n := 0
	for _, c := range fileName[i+1:] {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// sqlExportFinding is an export-needed finding for files that are listed but
// never opened. It has no importer and no path, so the server has nothing to
// read; the How key says how to export the data.
func sqlExportFinding(display, shows, how string, files []FileInfo) Finding {
	var t dbTally
	var names []string
	for _, fi := range files {
		t.add(fi)
		names = append(names, fi.Name)
	}
	return Finding{
		Detector: "sql", Kind: KindExportNeeded, HostLabel: localHostLabel,
		Display:    display + " (" + dbSetDetail(names) + ")",
		Confidence: 70, Shows: shows, How: how,
		SizeBytes: t.size, Files: t.files, Oldest: t.oldest, Newest: t.newest, Cost: CostForSize(t.size),
	}
}
