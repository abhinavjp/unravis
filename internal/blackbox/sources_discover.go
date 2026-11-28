package blackbox

import (
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Discovery of the log sources of this server (see sources_types.go and docs/blackbox/SOURCES-MANIFEST.md).
// Pure Go: every read of the machine goes through SourcesEnv, so a test gives a fake and runs on any OS.

// scanLimit is maxSourceScan as a variable so a test can lower it.
var scanLimit = maxSourceScan

const (
	maxIISConfigBytes = 4 << 20
	maxRedisConfBytes = 64 << 10
	maxRedisConfs     = 8
	defaultIISLogDir  = `%SystemDrive%\inetpub\logs\LogFiles`
	appLogPattern     = "*.log|*.txt|*.json|*.ndjson"
)

// DiscoverSources builds the sources document. It never fails: what cannot be found is left out.
func DiscoverSources(env SourcesEnv, cfg Config, host, version string, now time.Time) SourcesDoc {
	getenv := func(name string) string {
		if env.Getenv == nil {
			return ""
		}
		for _, n := range []string{name, strings.ToLower(name), strings.ToUpper(name)} {
			if v := env.Getenv(n); v != "" {
				return v
			}
		}
		return ""
	}

	windir, ok := cleanLocalDir(getenv("windir"))
	if !ok {
		windir = `C:\Windows`
	}

	var list []SourceEntry

	// Recordings of this Blackbox.
	recDir := ""
	if f := strings.TrimSpace(cfg.Folder); f != "" {
		recDir = strings.TrimRight(strings.ReplaceAll(f, "/", `\`), `\`)
		list = append(list, SourceEntry{
			ID: "recordings", Kind: SourceRecordings, Label: "Blackbox recordings",
			Dir: recDir, Pattern: "bb-*.ndjson", Origin: "settings",
		})
	}

	list = append(list, iisSources(env, getenv, windir)...)

	list = append(list, SourceEntry{
		ID: "httperr", Kind: SourceHTTPErr, Label: "HTTP.sys error log",
		Dir: windir + `\System32\LogFiles\HTTPERR`, Pattern: "httperr*.log", Origin: "default",
	})

	list = append(list, sqlSources(env)...)
	list = append(list, redisSources(env)...)

	for _, ch := range []string{"Application", "System"} {
		list = append(list, SourceEntry{
			ID: "evt." + strings.ToLower(ch), Kind: SourceWinEvents, Label: "Windows event log " + ch,
			Dir: windir + `\System32\winevt\Logs`, Pattern: ch + ".evtx", Channel: ch, Origin: "default",
		})
	}

	// App folders from the settings: only those no other entry already covers.
	covered := map[string]bool{}
	if recDir != "" {
		covered[strings.ToLower(recDir)] = true
	}
	for _, e := range list {
		switch e.Kind {
		case SourceIIS, SourceHTTPErr, SourceRedis, SourceSQL:
			covered[strings.ToLower(e.Dir)] = true
		}
	}
	n := 0
	for _, p := range cfg.LogPaths {
		d, ok := cleanLocalDir(p)
		if !ok || covered[strings.ToLower(d)] || holdsCovered(covered, d) {
			continue
		}
		covered[strings.ToLower(d)] = true
		n++
		list = append(list, SourceEntry{
			ID: "app." + strconv.Itoa(n), Kind: SourceApp, Label: "Log folder from Blackbox settings",
			Dir: d, Pattern: appLogPattern, Origin: "settings",
		})
	}

	// Look at each folder, drop the defaults that are not there, keep the first of a repeated ID.
	seen := map[string]bool{}
	out := make([]SourceEntry, 0, len(list))
	for _, e := range list {
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		scanSource(env, &e)
		if e.Origin == "default" && e.Err == "not-found" {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > maxSourceEntries {
		out = out[:maxSourceEntries]
	}

	return SourcesDoc{
		Schema:    SourcesSchema,
		Host:      host,
		Version:   version,
		Generated: now.UTC(),
		Sources:   out,
	}
}

// holdsCovered reports whether a folder already listed lies below d. The IIS root LogFiles holds only W3SVC<id> folders,
// each listed on its own, so the root itself would show 0 files and mislead.
func holdsCovered(covered map[string]bool, d string) bool {
	p := strings.ToLower(d) + `\`
	for k := range covered {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// scanSource lists one folder (no recursion) and fills in the counts and the error word.
func scanSource(env SourcesEnv, e *SourceEntry) {
	if env.ReadDir == nil {
		e.Err = "error"
		return
	}
	entries, err := env.ReadDir(e.Dir)
	switch {
	case err == nil:
		e.Exists, e.Readable = true, true
	case errors.Is(err, fs.ErrNotExist):
		e.Err = "not-found"
		return
	case errors.Is(err, fs.ErrPermission):
		e.Exists = true
		e.Err = "access-denied"
		return
	default:
		e.Err = "error"
		return
	}

	if len(entries) > scanLimit {
		entries = entries[:scanLimit]
		e.Truncated = true
	}
	pats := strings.Split(strings.ToLower(e.Pattern), "|")
	for _, de := range entries {
		if de == nil || !de.Type().IsRegular() {
			continue
		}
		if !matchAny(pats, strings.ToLower(de.Name())) {
			continue
		}
		info, err := de.Info()
		if err != nil || info == nil {
			continue
		}
		e.Files++
		e.Bytes += info.Size()
		if t := info.ModTime().Unix(); t > e.Newest {
			e.Newest = t
		}
	}
}

func matchAny(pats []string, name string) bool {
	for _, p := range pats {
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// cleanLocalDir returns s as a plain absolute local path (X:\a\b, backslashes, no trailing one), or false for
// anything else: UNC, relative, "..", control or wildcard characters.
func cleanLocalDir(s string) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "/", `\`))
	if strings.HasPrefix(s, `\\`) || len(s) < 3 || s[1] != ':' || s[2] != '\\' {
		return "", false
	}
	c := s[0]
	if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || strings.IndexByte(`<>"|?*`, s[i]) >= 0 || (s[i] == ':' && i != 1) {
			return "", false
		}
	}
	var parts []string
	for _, seg := range strings.Split(s[3:], `\`) {
		switch seg {
		case "", ".":
		case "..":
			return "", false
		default:
			parts = append(parts, seg)
		}
	}
	if len(parts) == 0 {
		return s[:2] + `\`, true
	}
	return s[:2] + `\` + strings.Join(parts, `\`), true
}

// expandVars replaces %NAME% with its value. An unknown or empty variable, or a lone %, fails.
func expandVars(s string, getenv func(string) string) (string, bool) {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String(), true
		}
		b.WriteString(s[:i])
		rest := s[i+1:]
		j := strings.IndexByte(rest, '%')
		if j <= 0 {
			return "", false
		}
		v := getenv(rest[:j])
		if v == "" {
			return "", false
		}
		b.WriteString(v)
		s = rest[j+1:]
	}
}

func resolveDir(s string, getenv func(string) string) (string, bool) {
	x, ok := expandVars(s, getenv)
	if !ok {
		return "", false
	}
	return cleanLocalDir(x)
}

// dirAndBase splits a backslash path.
func dirAndBase(p string) (string, string) {
	i := strings.LastIndexByte(p, '\\')
	if i < 0 {
		return "", p
	}
	if i == 2 && len(p) > 2 && p[1] == ':' {
		return p[:3], p[3:]
	}
	return p[:i], p[i+1:]
}

// IIS

type iisLogFile struct {
	Directory string `xml:"directory,attr"`
	Enabled   string `xml:"enabled,attr"`
	Mode      string `xml:"centralLogFileMode,attr"`
}

type iisCentral struct {
	Directory string `xml:"directory,attr"`
}

type iisSite struct {
	Name    string     `xml:"name,attr"`
	ID      string     `xml:"id,attr"`
	LogFile iisLogFile `xml:"logFile"`
}

type iisConfig struct {
	AppHost struct {
		Sites struct {
			Defaults struct {
				LogFile iisLogFile `xml:"logFile"`
			} `xml:"siteDefaults"`
			Site []iisSite `xml:"site"`
		} `xml:"sites"`
		Log struct {
			W3C iisCentral `xml:"centralW3CLogFile"`
			Bin iisCentral `xml:"centralBinaryLogFile"`
		} `xml:"log"`
	} `xml:"system.applicationHost"`
}

func iisSources(env SourcesEnv, getenv func(string) string, windir string) []SourceEntry {
	if env.ReadFile == nil {
		return nil
	}
	data, err := env.ReadFile(windir+`\System32\inetsrv\config\applicationHost.config`, maxIISConfigBytes)
	if err != nil || len(data) == 0 {
		return nil
	}
	var cfg iisConfig
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	// Take any declared encoding as it is; a wrong one shows as a parse error and gives no entries.
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := dec.Decode(&cfg); err != nil {
		return nil
	}

	// %SystemDrive% falls back to the drive of windir.
	gv := func(name string) string {
		if v := getenv(name); v != "" {
			return v
		}
		if strings.EqualFold(name, "SystemDrive") && len(windir) >= 2 {
			return windir[:2]
		}
		return ""
	}

	sites := cfg.AppHost.Sites
	defDir := sites.Defaults.LogFile.Directory
	if strings.TrimSpace(defDir) == "" {
		defDir = defaultIISLogDir
	}

	mode := strings.ToLower(strings.TrimSpace(sites.Defaults.LogFile.Mode))
	switch mode {
	case "centralw3c", "centralbinary":
		central := cfg.AppHost.Log.W3C.Directory
		pattern := "*.log"
		if mode == "centralbinary" {
			central = cfg.AppHost.Log.Bin.Directory
			pattern = "*.log|*.ibl"
		}
		if strings.TrimSpace(central) == "" {
			central = defaultIISLogDir
		}
		d, ok := resolveDir(central, gv)
		if !ok {
			return nil
		}
		return []SourceEntry{{
			ID: "iis.central", Kind: SourceIIS, Label: "IIS central log",
			Dir: d, Pattern: pattern, Origin: "applicationHost.config",
		}}
	}

	var out []SourceEntry
	seenDir := map[string]bool{}
	for _, s := range sites.Site {
		id := strings.TrimSpace(s.ID)
		if !allDigits(id) || len(id) > 10 {
			continue
		}
		base := defDir
		if strings.TrimSpace(s.LogFile.Directory) != "" {
			base = s.LogFile.Directory
		}
		d, ok := resolveDir(base, gv)
		if !ok {
			continue
		}
		d += `\W3SVC` + id
		if seenDir[strings.ToLower(d)] {
			continue
		}
		seenDir[strings.ToLower(d)] = true
		name := strings.TrimSpace(s.Name)
		if name == "" {
			name = id
		}
		label := "IIS site " + name
		if strings.EqualFold(strings.TrimSpace(s.LogFile.Enabled), "false") {
			label += " (logging off)"
		}
		out = append(out, SourceEntry{
			ID: "iis.site." + id, Kind: SourceIIS, Label: label,
			Dir: d, Pattern: "*.log", Origin: "applicationHost.config",
		})
	}
	return out
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// SQL Server

func sqlSources(env SourcesEnv) []SourceEntry {
	if env.SQLLogs == nil {
		return nil
	}
	var out []SourceEntry
	for _, inst := range env.SQLLogs() {
		lp, ok := cleanLocalDir(inst.LogPath)
		if !ok || strings.TrimSpace(inst.Label) == "" {
			continue
		}
		dir, _ := dirAndBase(lp)
		if dir == "" {
			continue
		}
		out = append(out, SourceEntry{
			ID: "sql." + strings.ToLower(inst.Label), Kind: SourceSQL,
			Label: "SQL Server " + inst.Label + " error log",
			Dir:   dir, Pattern: "ERRORLOG*", Origin: "registry",
		})
	}
	return out
}

// Redis and Memurai

func redisSources(env SourcesEnv) []SourceEntry {
	if env.RedisConfs == nil || env.ReadFile == nil {
		return nil
	}
	confs := env.RedisConfs()
	if len(confs) > maxRedisConfs {
		confs = confs[:maxRedisConfs]
	}
	var out []SourceEntry
	for i, c := range confs {
		conf, ok := cleanLocalDir(c)
		if !ok {
			continue
		}
		data, err := env.ReadFile(conf, maxRedisConfBytes)
		if err != nil {
			continue
		}
		val := redisLogfile(data)
		if val == "" || strings.EqualFold(val, "stdout") {
			continue
		}
		confDir, confBase := dirAndBase(conf)
		val = strings.ReplaceAll(val, "/", `\`)
		if strings.HasPrefix(val, `\`) {
			continue // UNC, or rooted on an unknown drive
		}
		if len(val) < 2 || val[1] != ':' {
			val = confDir + `\` + val // relative to the settings file
		}
		lp, ok := cleanLocalDir(val)
		if !ok {
			continue
		}
		dir, base := dirAndBase(lp)
		if dir == "" || base == "" {
			continue
		}
		out = append(out, SourceEntry{
			ID: "redis." + strconv.Itoa(i+1), Kind: SourceRedis, Label: "Redis log (" + confBase + ")",
			Dir: dir, Pattern: base + "*", Origin: "redis settings",
		})
	}
	return out
}

// redisLogfile returns the value of the last "logfile" line. It looks at no other line, so nothing else of the
// settings (passwords) leaves this function.
func redisLogfile(data []byte) string {
	val := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		k := strings.IndexAny(line, " \t")
		if k < 0 || !strings.EqualFold(line[:k], "logfile") {
			continue
		}
		val = confValue(strings.TrimSpace(line[k+1:]))
	}
	return val
}

// confValue reads one value: bare, or in "..." or '...' (a backslash before the quote or another backslash
// escapes it inside double quotes, as the Redis settings reader does).
func confValue(s string) string {
	if s == "" {
		return ""
	}
	q := s[0]
	if q != '"' && q != '\'' {
		if i := strings.IndexAny(s, " \t"); i >= 0 {
			return s[:i]
		}
		return s
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			n := s[i+1]
			if (q == '"' && (n == '\\' || n == '"')) || (q == '\'' && n == '\'') {
				b.WriteByte(n)
				i++
				continue
			}
		}
		if c == q {
			break
		}
		b.WriteByte(c)
	}
	return b.String()
}
