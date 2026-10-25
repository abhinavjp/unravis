package discover

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"api-audit-explorer/internal/discover/configparse"
	"api-audit-explorer/internal/importer"
)

// SiteDirs lists the site folders the applog detector looks in. The IIS detector
// supplies the physical folder of each site. A nil SiteDirs lists none.
type SiteDirs func(ctx context.Context) []string

// AppLog finds the log files an application's own logger writes, by reading the
// logger's config next to each site: log4net, NLog, Serilog and Microsoft
// Extensions Logging file sinks. It keeps log file paths and nothing else from a
// config (spec 2.2, "Secrets"), and it reports Application Insights and Hangfire
// by presence alone.
type AppLog struct {
	sites SiteDirs
	extra []string
	// Environment is the ASP.NET Core environment whose appsettings.<Environment>.json
	// overlays appsettings.json. The host default is Production.
	Environment string
}

// NewAppLog builds the detector. extra holds site folders added by hand, which
// tests use and which a caller may use for a folder IIS does not know.
func NewAppLog(sites SiteDirs, extra ...string) *AppLog {
	return &AppLog{sites: sites, extra: append([]string(nil), extra...), Environment: "Production"}
}

// Name is "applog".
func (*AppLog) Name() string { return "applog" }

const (
	maxSites = 200
	maxSinks = 200
)

// Detect reads the config files in each site folder and reports the log sets they
// name. A folder, config or log folder the account may not read becomes a gap.
func (a *AppLog) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	r := &appRun{
		name: a.Name(), env: env, environment: a.Environment,
		lists: map[string]listing{}, dirs: map[string]error{}, seenGap: map[Gap]bool{}, seenSink: map[string]bool{},
	}
	if r.environment == "" {
		r.environment = "Production"
	}
	if env.FS == nil {
		r.addGap(Gap{Detector: r.name, Display: "applog", Reason: ReasonUnreadable})
		return nil, r.gaps
	}
	// Findings carry the local label, never the machine name (spec section 7).
	r.host = hostLabelLocal
	var dirs []string
	if a.sites != nil {
		dirs = append(dirs, a.sites(ctx)...)
	}
	dirs = append(dirs, a.extra...)
	seen := map[string]bool{}
	n := 0
	for _, d := range dirs {
		if ctx.Err() != nil {
			return r.findings, r.gaps
		}
		clean, err := ValidateAbsPath(d)
		if err != nil {
			r.addGap(Gap{Detector: r.name, Display: "site folder", Reason: ReasonFromError(err)})
			continue
		}
		if seen[CanonKey(clean)] {
			continue
		}
		seen[CanonKey(clean)] = true
		r.roots = append(r.roots, clean)
		if n++; n > maxSites {
			r.addGap(Gap{Detector: r.name, Display: "site folders", Reason: ReasonTooLarge})
			break
		}
		r.scanSite(ctx, clean)
	}
	for _, s := range r.sinks {
		if ctx.Err() != nil {
			return r.findings, r.gaps
		}
		r.emitSink(ctx, s)
	}
	r.emitPresence()
	return r.findings, r.gaps
}

// sink is one place an application logger writes: a folder and the file names in
// it that belong to the log.
type sink struct {
	src   string // log4net, nlog, serilog, mel
	dir   string
	res   []*regexp.Regexp
	label string   // base name with * for a varying part, safe to show
	cands []string // importer names, best guess first
}

type listing struct {
	entries []FileInfo
	err     error
}

type appRun struct {
	name        string
	env         Env
	host        string
	environment string

	roots    []string // site and extra folders the scan was given
	lists    map[string]listing
	dirs     map[string]error
	sinks    []*sink
	seenSink map[string]bool

	appInsights bool
	hangfire    bool
	quartz      bool

	findings []Finding
	gaps     []Gap
	seenGap  map[Gap]bool
}

func (r *appRun) addGap(g Gap) {
	if r.seenGap[g] {
		return
	}
	r.seenGap[g] = true
	r.gaps = append(r.gaps, g)
}

func (r *appRun) gapErr(kind Kind, display string, err error) {
	r.addGap(GapFromError(r.name, kind, display, err))
}

// checkDir confirms a folder is a plain directory the scan may walk: no reparse
// point on the way, and the system's name for it matches the expected one.
func (r *appRun) checkDir(dir string) error {
	k := CanonKey(dir)
	if err, ok := r.dirs[k]; ok {
		return err
	}
	canon, reparse, err := r.env.FS.Final(dir)
	if err == nil && (reparse || !strings.EqualFold(canon, dir)) {
		err = unsafeErr("final-path")
	}
	r.dirs[k] = err
	return err
}

func (r *appRun) list(dir string) ([]FileInfo, error) {
	k := CanonKey(dir)
	if l, ok := r.lists[k]; ok {
		return l.entries, l.err
	}
	entries, err := r.env.FS.ReadDir(dir)
	r.lists[k] = listing{entries, err}
	return entries, err
}

// scanSite reads the config files in one site folder.
func (r *appRun) scanSite(ctx context.Context, dir string) {
	if err := r.checkDir(dir); err != nil {
		r.gapErr("", "site folder", err)
		return
	}
	entries, err := r.list(dir)
	if err != nil {
		r.gapErr("", "site folder", err)
		if !errors.Is(err, ErrTooManyEntries) {
			return
		}
	}

	var xmlCfgs []FileInfo
	var base, overlay *FileInfo
	overlayName := "appsettings." + strings.ToLower(r.environment) + ".json"
	for i := range entries {
		e := entries[i]
		if e.IsDir {
			continue
		}
		lower := strings.ToLower(e.Name)
		switch {
		case lower == "web.config", lower == "app.config", lower == "log4net.config", lower == "nlog.config",
			strings.HasSuffix(lower, ".exe.config"):
			xmlCfgs = append(xmlCfgs, e)
		case lower == "appsettings.json":
			base = &entries[i]
		case lower == overlayName:
			overlay = &entries[i]
		case lower == "applicationinsights.config":
			r.appInsights = true
		case isHangfireDLL(lower):
			r.hangfire = true
		case isQuartzDLL(lower):
			r.quartz = true
		}
	}
	// Hangfire ships as a package dll in bin. Only names are read.
	if bin := WinJoin(dir, "bin"); !r.hangfire || !r.quartz {
		if be, berr := r.list(bin); berr == nil || errors.Is(berr, ErrTooManyEntries) {
			for _, e := range be {
				if e.IsDir {
					continue
				}
				lower := strings.ToLower(e.Name)
				if isHangfireDLL(lower) {
					r.hangfire = true
				} else if isQuartzDLL(lower) {
					r.quartz = true
				}
			}
		} else if IsAccessDenied(berr) {
			r.gapErr("", "bin folder", berr)
		}
	}

	for _, e := range xmlCfgs {
		if ctx.Err() != nil {
			return
		}
		r.readXML(dir, e)
	}
	if base != nil || overlay != nil {
		r.readJSONSet(ctx, dir, base, overlay)
	}
}

func isHangfireDLL(lower string) bool {
	return strings.HasPrefix(lower, "hangfire.") && strings.HasSuffix(lower, ".dll")
}

// isQuartzDLL matches Quartz.dll and its companion packages, such as
// Quartz.Extensions.Hosting.dll. Only the name is read.
func isQuartzDLL(lower string) bool {
	return strings.HasPrefix(lower, "quartz.") && strings.HasSuffix(lower, ".dll")
}

// readConfig loads one config file, up to MaxConfigBytes.
func (r *appRun) readConfig(dir string, e FileInfo) ([]byte, bool) {
	if e.Reparse {
		r.addGap(Gap{Detector: r.name, Display: e.Name, Reason: ReasonUnsafePath})
		return nil, false
	}
	if e.Size > MaxConfigBytes {
		r.addGap(Gap{Detector: r.name, Display: e.Name, Reason: ReasonTooLarge})
		return nil, false
	}
	p := WinJoin(dir, e.Name)
	canon, reparse, err := r.env.FS.Final(p)
	if err != nil {
		r.gapErr("", e.Name, err)
		return nil, false
	}
	if reparse || !strings.EqualFold(canon, p) {
		r.addGap(Gap{Detector: r.name, Display: e.Name, Reason: ReasonUnsafePath})
		return nil, false
	}
	data, err := r.env.FS.Head(p, MaxConfigBytes)
	if err != nil {
		r.gapErr("", e.Name, err)
		return nil, false
	}
	return data, true
}

func (r *appRun) parseGap(name string, err error) {
	reason := ReasonConfigUnparsed
	if errors.Is(err, configparse.ErrTooLarge) {
		reason = ReasonTooLarge
	}
	r.addGap(Gap{Detector: r.name, Display: name, Reason: reason})
}

// --- XML configs: log4net, NLog, ApplicationInsights element ---

func xmlFilter(configDir string) configparse.XMLFilter {
	return configparse.XMLFilter{
		Attrs: map[string][]string{
			"appender":            {"type"},
			"file":                {"value"},
			"datepattern":         {"value"},
			"layout":              {"type"},
			"target":              {"type", "filename", "archivefilename"},
			"applicationinsights": {},
		},
		Keep: func(elem string, anc []string, raw map[string]string) (map[string]string, []configparse.Reject) {
			parent := ""
			if len(anc) > 0 {
				parent = anc[len(anc)-1]
			}
			switch elem {
			case "appender":
				fa := "0"
				if strings.Contains(strings.ToLower(raw["type"]), "fileappender") {
					fa = "1"
				}
				return map[string]string{"fa": fa}, nil
			case "file":
				v, ok := raw["value"]
				if parent != "appender" || !ok {
					return nil, nil
				}
				return map[string]string{"p": resolveRaw(v, configDir, kindPlain)}, nil
			case "datepattern":
				v, ok := raw["value"]
				if parent != "appender" || !ok {
					return nil, nil
				}
				if t, good := dateTemplate(v); good {
					return map[string]string{"t": t}, nil
				}
				return map[string]string{"t": ""}, nil
			case "layout":
				if parent != "appender" {
					return nil, nil
				}
				x := "0"
				if strings.Contains(strings.ToLower(raw["type"]), "xmllayout") {
					x = "1"
				}
				return map[string]string{"xml": x}, nil
			case "target":
				if strings.ToLower(strings.TrimSpace(raw["type"])) != "file" {
					return nil, nil
				}
				kept := map[string]string{"file": "1"}
				if v, ok := raw["filename"]; ok {
					kept["fn"] = resolveRaw(v, configDir, kindNLog)
				}
				if v, ok := raw["archivefilename"]; ok {
					kept["afn"] = resolveRaw(v, configDir, kindNLogArchive)
				}
				return kept, nil
			case "applicationinsights":
				return map[string]string{"present": "1"}, nil
			}
			return nil, nil
		},
	}
}

func (r *appRun) readXML(dir string, e FileInfo) {
	data, ok := r.readConfig(dir, e)
	if !ok {
		return
	}
	res, err := configparse.ParseXML(data, xmlFilter(dir))
	if err != nil {
		r.parseGap(e.Name, err)
		return
	}
	res.Root.Walk(func(n *configparse.Node) bool {
		switch n.Name {
		case "appender":
			if n.Attrs["fa"] == "1" {
				r.log4netSink(n)
			}
		case "target":
			r.nlogSink(n.Attrs["fn"], n.Attrs["afn"])
		case "applicationinsights":
			r.appInsights = true
		}
		return true
	})
}

func (r *appRun) log4netSink(n *configparse.Node) {
	var fileEnc, dateTmpl string
	var haveDate, xml bool
	for _, c := range n.Children {
		switch c.Name {
		case "file":
			fileEnc = c.Attrs["p"]
		case "datepattern":
			haveDate, dateTmpl = true, c.Attrs["t"]
		case "layout":
			xml = c.Attrs["xml"] == "1"
		}
	}
	if fileEnc == "" {
		return
	}
	dir, base, reason, ok := decodeSink(fileEnc)
	if !ok {
		r.addGap(Gap{Detector: r.name, Kind: KindLogFile, Display: "log4net log path", Reason: reason})
		return
	}
	dateRe, label := "", base
	if haveDate {
		dateRe, label = `[0-9][0-9A-Za-z._-]*`, base+"*"
		if dateTmpl != "" {
			dateRe = tmplRegex(dateTmpl)
		}
	}
	cands := []string{"log4net-text", "log4net-xml"}
	if xml {
		cands = []string{"log4net-xml", "log4net-text"}
	}
	r.addSink(&sink{src: "log4net", dir: dir, res: []*regexp.Regexp{rollingRe(base, dateRe, true)}, label: label, cands: cands})
}

// nlogSink adds the sinks for one NLog File target. Each argument is an encoded
// sink or empty.
func (r *appRun) nlogSink(fnEnc, afnEnc string) {
	if fnEnc == "" {
		return
	}
	dir, name, reason, ok := decodeSink(fnEnc)
	if !ok {
		r.addGap(Gap{Detector: r.name, Kind: KindLogFile, Display: "NLog log path", Reason: reason})
		return
	}
	main := &sink{src: "nlog", dir: dir, label: tmplLabel(name), cands: []string{"nlog-text"}}
	if afnEnc == "" {
		main.res = []*regexp.Regexp{templateRe(name, true)}
		r.addSink(main)
		return
	}
	main.res = []*regexp.Regexp{templateRe(name, false)}
	adir, aname, areason, aok := decodeSink(afnEnc)
	switch {
	case !aok:
		r.addGap(Gap{Detector: r.name, Kind: KindLogFile, Display: "NLog archive path", Reason: areason})
		main.res = []*regexp.Regexp{templateRe(name, true)}
	case CanonKey(adir) == CanonKey(dir):
		main.res = append(main.res, templateRe(aname, false))
	default:
		r.addSink(&sink{src: "nlog", dir: adir, res: []*regexp.Regexp{templateRe(aname, false)}, label: tmplLabel(aname), cands: []string{"nlog-text"}})
	}
	r.addSink(main)
}

func (r *appRun) addSink(s *sink) {
	var parts []string
	for _, re := range s.res {
		parts = append(parts, re.String())
	}
	k := CanonKey(s.dir) + "\x00" + s.src + "\x00" + strings.Join(parts, "\x00")
	if r.seenSink[k] {
		return
	}
	r.seenSink[k] = true
	if len(r.sinks) >= maxSinks {
		r.addGap(Gap{Detector: r.name, Kind: KindLogFile, Display: "log paths", Reason: ReasonTooLarge})
		return
	}
	r.sinks = append(r.sinks, s)
}

// --- JSON configs: Serilog, NLog, MEL file sinks, ApplicationInsights section ---

var nlogLeaf = map[string]bool{"type": true, "filename": true, "archivefilename": true}

func serilogLeaf(key string) bool {
	switch {
	case strings.HasSuffix(key, ":args:name"):
		return false
	case strings.HasSuffix(key, ":name"), strings.HasSuffix(key, ":args:path"), strings.HasSuffix(key, ":args:pathformat"),
		strings.HasSuffix(key, ":args:rollinginterval"), strings.HasSuffix(key, ":args:formatter"):
		return true
	}
	return false
}

func lastSeg(key string) string {
	if i := strings.LastIndexByte(key, ':'); i >= 0 {
		return key[i+1:]
	}
	return key
}

func jsonFilter(configDir string) configparse.JSONFilter {
	return configparse.JSONFilter{
		Decide: func(key string, container bool) configparse.Decision {
			switch {
			case key == "":
				return configparse.Descend
			case key == "applicationinsights":
				return configparse.Mark
			case key == "serilog", key == "nlog", key == "logging", key == "logging:file":
				if container {
					return configparse.Descend
				}
			case strings.HasPrefix(key, "serilog:writeto"):
				if container {
					return configparse.Descend
				}
				if serilogLeaf(key) {
					return configparse.Keep
				}
			case key == "nlog:targets" || strings.HasPrefix(key, "nlog:targets:"):
				if container {
					return configparse.Descend
				}
				if nlogLeaf[lastSeg(key)] && key != "nlog:targets" {
					return configparse.Keep
				}
			case key == "logging:file:path", key == "logging:file:pathformat":
				return configparse.Keep
			}
			return configparse.Skip
		},
		Validate: func(key, raw string) (string, string, bool) {
			seg := lastSeg(key)
			switch {
			case strings.HasPrefix(key, "serilog:"):
				switch seg {
				case "name":
					switch strings.ToLower(strings.TrimSpace(raw)) {
					case "file":
						return "file", "", true
					case "rollingfile":
						return "rollingfile", "", true
					}
					return "other", "", true
				case "path":
					return resolveRaw(raw, configDir, kindPlain), "", true
				case "pathformat":
					return resolveRaw(raw, configDir, kindTokens), "", true
				case "rollinginterval":
					switch strings.ToLower(strings.TrimSpace(raw)) {
					case "year", "month", "day", "hour", "minute":
						return "1", "", true
					}
					return "0", "", true
				case "formatter":
					if strings.Contains(strings.ToLower(raw), "compact") {
						return "clef", "", true
					}
					return "text", "", true
				}
			case strings.HasPrefix(key, "nlog:"):
				switch seg {
				case "type":
					if strings.ToLower(strings.TrimSpace(raw)) == "file" {
						return "file", "", true
					}
					return "other", "", true
				case "filename":
					return resolveRaw(raw, configDir, kindNLog), "", true
				case "archivefilename":
					return resolveRaw(raw, configDir, kindNLogArchive), "", true
				}
			case key == "logging:file:path":
				return resolveRaw(raw, configDir, kindPlain), "", true
			case key == "logging:file:pathformat":
				return resolveRaw(raw, configDir, kindTokens), "", true
			}
			return "", "", false
		},
	}
}

// readJSONSet parses appsettings.json and then appsettings.<Environment>.json and
// overlays the second on the first key by key, as .NET configuration does.
func (r *appRun) readJSONSet(ctx context.Context, dir string, base, overlay *FileInfo) {
	merged := map[string]string{}
	present := map[string]bool{}
	for _, e := range []*FileInfo{base, overlay} {
		if e == nil || ctx.Err() != nil {
			continue
		}
		data, ok := r.readConfig(dir, *e)
		if !ok {
			continue
		}
		res, err := configparse.ParseJSON(data, jsonFilter(dir))
		if err != nil {
			r.parseGap(e.Name, err)
			continue
		}
		for k, v := range res.Values {
			merged[k] = v
		}
		for k := range res.Present {
			present[k] = true
		}
	}
	if present["applicationinsights"] {
		r.appInsights = true
	}
	r.serilogSinks(merged)
	r.nlogJSONSinks(merged)
	r.melSink(merged)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (r *appRun) serilogSinks(vals map[string]string) {
	type group struct{ name, path, pathFmt, dated, formatter string }
	groups := map[string]*group{}
	get := func(k string) *group {
		if groups[k] == nil {
			groups[k] = &group{}
		}
		return groups[k]
	}
	for _, k := range sortedKeys(vals) {
		if !strings.HasPrefix(k, "serilog:") {
			continue
		}
		v := vals[k]
		switch {
		case strings.HasSuffix(k, ":args:pathformat"):
			get(strings.TrimSuffix(k, ":args:pathformat")).pathFmt = v
		case strings.HasSuffix(k, ":args:path"):
			get(strings.TrimSuffix(k, ":args:path")).path = v
		case strings.HasSuffix(k, ":args:rollinginterval"):
			get(strings.TrimSuffix(k, ":args:rollinginterval")).dated = v
		case strings.HasSuffix(k, ":args:formatter"):
			get(strings.TrimSuffix(k, ":args:formatter")).formatter = v
		case strings.HasSuffix(k, ":name") && !strings.HasSuffix(k, ":args:name"):
			get(strings.TrimSuffix(k, ":name")).name = v
		}
	}
	gkeys := make([]string, 0, len(groups))
	for k := range groups {
		gkeys = append(gkeys, k)
	}
	sort.Strings(gkeys)
	for _, gk := range gkeys {
		g := groups[gk]
		if g.name != "file" && g.name != "rollingfile" {
			continue
		}
		enc := firstNonEmpty(g.path, g.pathFmt)
		if g.name == "rollingfile" {
			enc = firstNonEmpty(g.pathFmt, g.path)
		}
		if enc == "" {
			continue
		}
		dir, name, reason, ok := decodeSink(enc)
		if !ok {
			r.addGap(Gap{Detector: r.name, Kind: KindLogFile, Display: "Serilog log path", Reason: reason})
			continue
		}
		var re *regexp.Regexp
		if hasPlaceholder(name) {
			re = templateRe(name, true)
		} else {
			re = serilogRe(name, g.dated == "1")
		}
		cands := []string{"serilog-text", "serilog-clef"}
		if g.formatter == "clef" {
			cands = []string{"serilog-clef", "serilog-text"}
		}
		r.addSink(&sink{src: "serilog", dir: dir, res: []*regexp.Regexp{re}, label: tmplLabel(name), cands: cands})
	}
}

func (r *appRun) nlogJSONSinks(vals map[string]string) {
	type group struct{ typ, fn, afn string }
	groups := map[string]*group{}
	for _, k := range sortedKeys(vals) {
		if !strings.HasPrefix(k, "nlog:targets:") {
			continue
		}
		seg := lastSeg(k)
		if !nlogLeaf[seg] {
			continue
		}
		gk := strings.TrimSuffix(k, ":"+seg)
		if groups[gk] == nil {
			groups[gk] = &group{}
		}
		switch seg {
		case "type":
			groups[gk].typ = vals[k]
		case "filename":
			groups[gk].fn = vals[k]
		case "archivefilename":
			groups[gk].afn = vals[k]
		}
	}
	gkeys := make([]string, 0, len(groups))
	for k := range groups {
		gkeys = append(gkeys, k)
	}
	sort.Strings(gkeys)
	for _, gk := range gkeys {
		if g := groups[gk]; g.typ == "file" {
			r.nlogSink(g.fn, g.afn)
		}
	}
}

func (r *appRun) melSink(vals map[string]string) {
	enc := firstNonEmpty(vals["logging:file:path"], vals["logging:file:pathformat"])
	if p, pf := vals["logging:file:path"], vals["logging:file:pathformat"]; pf != "" && p != "" {
		// PathFormat is the rolling form; it wins when both are set.
		enc = pf
	}
	if enc == "" {
		return
	}
	dir, name, reason, ok := decodeSink(enc)
	if !ok {
		r.addGap(Gap{Detector: r.name, Kind: KindLogFile, Display: "Logging file path", Reason: reason})
		return
	}
	r.addSink(&sink{src: "mel", dir: dir, res: []*regexp.Regexp{templateRe(name, true)}, label: tmplLabel(name), cands: []string{"mel-json", "mel-console"}})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- Findings ---

var srcShows = map[string]string{
	"log4net": wordLog4netShows, "nlog": wordNLogShows, "serilog": wordSerilogShows, "mel": wordMELShows,
}

type matchedFile struct {
	info FileInfo
	path string
}

// emitSink lists a sink's folder, picks the files that belong to the log,
// confirms the format with a 64 KiB head and adds the finding.
func (r *appRun) emitSink(ctx context.Context, s *sink) {
	// A label a config chose may name a key or credential file; never show it.
	disp := s.label
	if sensitiveLogName(disp) {
		disp = "log file"
	}
	if err := r.checkDir(s.dir); err != nil {
		r.gapErr(KindLogSet, disp, err)
		return
	}
	entries, err := r.list(s.dir)
	if err != nil {
		r.gapErr(KindLogSet, disp, err)
		if !errors.Is(err, ErrTooManyEntries) {
			return
		}
	}
	var files []matchedFile
	unsafe := false
	for _, e := range entries {
		if e.IsDir || e.Reparse || !s.matches(e.Name) {
			continue
		}
		p := WinJoin(s.dir, e.Name)
		if _, perr := ValidLogPath(p, ""); perr != nil {
			unsafe = true
			continue
		}
		if !logExtOK(e.Name) || sensitiveLogName(e.Name) {
			unsafe = true
			continue
		}
		files = append(files, matchedFile{e, p})
	}
	if unsafe {
		r.addGap(Gap{Detector: r.name, Kind: KindLogSet, Display: disp, Reason: ReasonUnsafePath})
	}
	if len(files) == 0 {
		if !unsafe {
			r.addGap(Gap{Detector: r.name, Kind: KindLogSet, Display: disp, Reason: ReasonNotFound})
		}
		return
	}
	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].info.ModTime.Equal(files[j].info.ModTime) {
			return files[i].info.ModTime.Before(files[j].info.ModTime)
		}
		return strings.ToLower(files[i].info.Name) < strings.ToLower(files[j].info.Name)
	})
	newest := files[len(files)-1]

	imp, conf, err := r.pickImporter(files, s.cands)
	if err != nil {
		r.gapErr(KindLogSet, disp, err)
		return
	}
	if imp == "" {
		r.addGap(Gap{Detector: r.name, Kind: KindLogSet, Display: disp, Reason: ReasonUnreadable})
		return
	}
	// Outside the site folders a config may point anywhere, so the head must
	// have been read and recognised (pickImporter scores 40 when it was not).
	if conf < 70 && !underRoots(s.dir, r.roots) {
		r.addGap(Gap{Detector: r.name, Kind: KindLogSet, Display: disp, Reason: ReasonUnsafePath})
		return
	}

	var total int64
	oldest, latest := files[0].info.ModTime, files[0].info.ModTime
	paths := make([]string, len(files))
	for i, f := range files {
		total += f.info.Size
		paths[i] = f.path
		if f.info.ModTime.Before(oldest) {
			oldest = f.info.ModTime
		}
		if f.info.ModTime.After(latest) {
			latest = f.info.ModTime
		}
	}
	kind, detail := KindLogFile, newest.info.Name
	if len(files) > 1 {
		kind = KindLogSet
		detail += " and " + strconv.Itoa(len(files)-1) + " more"
	}
	f := Finding{
		Detector: r.name, Kind: kind, Importer: imp, HostLabel: r.host, Confidence: conf,
		Shows: srcShows[s.src], SizeBytes: total, Files: len(files),
		Oldest: oldest, Newest: latest, Cost: CostForSize(total),
	}
	f.SetPaths(paths...)
	f.SetSites(nil, detail)
	r.findings = append(r.findings, f)
}

func (s *sink) matches(name string) bool {
	for _, re := range s.res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// pickImporter scores the newest non-empty file's head against each candidate.
// 90 means the first candidate (what the config declares) reads it, 70 means a
// later one does, 40 means none did or there was nothing to read.
func (r *appRun) pickImporter(files []matchedFile, cands []string) (string, int, error) {
	if r.env.Importers == nil {
		return cands[0], 40, nil
	}
	var avail []string
	for _, c := range cands {
		if _, ok := r.env.Importers.ByName(c); ok {
			avail = append(avail, c)
		}
	}
	if len(avail) == 0 {
		return "", 0, nil
	}
	var probe *matchedFile
	for i := len(files) - 1; i >= 0; i-- {
		if files[i].info.Size > 0 {
			probe = &files[i]
			break
		}
	}
	if probe == nil {
		return avail[0], 40, nil
	}
	head, err := r.env.FS.Head(probe.path, importer.HeadSize)
	if err != nil {
		return "", 0, err
	}
	best, bestScore := "", 0
	for _, c := range avail {
		imp, _ := r.env.Importers.ByName(c)
		if sc := safeScore(imp, head, probe.info.Name); sc > bestScore {
			best, bestScore = c, sc
		}
	}
	switch best {
	case "":
		return avail[0], 40, nil
	case avail[0]:
		return best, 90, nil
	}
	return best, 70, nil
}

func safeScore(imp importer.Importer, head []byte, name string) (s int) {
	defer func() {
		if recover() != nil {
			s = 0
		}
	}()
	if len(head) > importer.HeadSize {
		head = head[:importer.HeadSize]
	}
	return imp.Detect(head, name)
}

// emitPresence adds the findings that rest on a name alone: Application Insights
// holds its data in Azure, and Hangfire keeps job history in its own storage.
// Neither names a path.
func (r *appRun) emitPresence() {
	if r.appInsights {
		f := Finding{
			Detector: r.name, Kind: KindRemoteOnly, HostLabel: r.host, Confidence: 40,
			Shows: wordAppInsightsShows, How: wordAppInsightsHow, Cost: CostSmall,
		}
		f.SetSites(nil, "Application Insights")
		r.findings = append(r.findings, f)
	}
	if r.hangfire {
		f := Finding{
			Detector: r.name, Kind: KindExportNeeded, HostLabel: r.host, Confidence: 40,
			Shows: wordHangfireShows, How: wordHangfireHow, Cost: CostSmall,
		}
		f.SetSites(nil, "Hangfire job history")
		r.findings = append(r.findings, f)
	}
	if r.quartz {
		f := Finding{
			Detector: r.name, Kind: KindExportNeeded, HostLabel: r.host, Confidence: 40,
			Shows: wordQuartzShows, How: wordQuartzHow, Cost: CostSmall,
		}
		f.SetSites(nil, "Quartz.NET scheduler log")
		r.findings = append(r.findings, f)
	}
}

