package discover

import (
	"context"
	"strings"

	"api-audit-explorer/internal/importer"
)

// Web servers (spec section 5): nginx and Apache access logs.
//
// The server's service image sits in its program folder, or one folder below
// it (httpd.exe is usually in bin). The config file, conf\nginx.conf or
// conf\httpd.conf, is read through FS.Head with the 4 MiB cap and lexed for the
// access-log directives only: nginx access_log, Apache CustomLog and ServerRoot.
// Every other line is dropped in the lexer, so the SSL keys, proxy targets and
// password files it names are never kept. Include directives are not followed.
// A server whose process runs but has no service is a gap, because the process
// list does not say where its config is.

const webDetectorName = "web"

const (
	importerNginx  = "nginx-access"
	importerApache = "apache-combined"
)

// webServer describes one server the detector looks for.
type webServer struct {
	name     string // for gaps; never a path
	image    string // service image base name
	conf     string // config file, relative to the server folder
	importer string
	shows    string
	nginx    bool
}

var webServers = []webServer{
	{name: "nginx", image: "nginx.exe", conf: `conf\nginx.conf`, importer: importerNginx, shows: "discover.nginx.shows", nginx: true},
	{name: "Apache", image: "httpd.exe", conf: `conf\httpd.conf`, importer: importerApache, shows: "discover.apache.shows"},
}

// webDetector finds nginx and Apache access logs.
type webDetector struct{}

// NewWeb returns the web server detector.
func NewWeb() Detector { return webDetector{} }

func (webDetector) Name() string { return webDetectorName }

func (webDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	var findings []Finding
	var gaps []Gap
	for _, ws := range webServers {
		if ctx.Err() != nil {
			break
		}
		got, gs := detectWebServer(ctx, env, ws)
		findings = append(findings, got...)
		gaps = append(gaps, gs...)
	}
	return findings, gaps
}

// webInstall is one server folder that holds a config file.
type webInstall struct {
	root string   // the server folder: the prefix for nginx, above bin for Apache
	conf string   // full path of the config file
	info FileInfo // from checkedStat
}

// webLogRef is one access-log path a config names, or the nginx default.
type webLogRef struct {
	path     string
	declared bool // false for the nginx default when no access_log is set
}

func detectWebServer(ctx context.Context, env Env, ws webServer) ([]Finding, []Gap) {
	var gaps []Gap
	var installs []webInstall
	listed, matched := false, false
	if env.Services != nil {
		svcs, err := env.Services.List()
		if err != nil {
			gaps = append(gaps, GapFromError(webDetectorName, "", ws.name, err))
		} else {
			listed = true
			for _, s := range svcs {
				if !strings.EqualFold(s.Image, ws.image) {
					continue
				}
				matched = true
				dir, err := ValidateAbsPath(s.BinaryDir)
				if err != nil {
					gaps = append(gaps, GapFromError(webDetectorName, "", ws.name, err))
					continue
				}
				in, found, gs := findWebConf(env.FS, ws, dir)
				gaps = append(gaps, gs...)
				switch {
				case found:
					installs = append(installs, in)
				case len(gs) == 0:
					gaps = append(gaps, Gap{Detector: webDetectorName, Display: ws.name + " configuration", Reason: ReasonNotFound})
				}
			}
		}
	}
	if env.Procs != nil {
		procs, err := env.Procs.List()
		switch {
		case err != nil:
			gaps = append(gaps, GapFromError(webDetectorName, "", ws.name, err))
		case listed && !matched:
			for _, p := range procs {
				if strings.EqualFold(p.Image, ws.image) {
					gaps = append(gaps, Gap{Detector: webDetectorName, Display: ws.name, Reason: ReasonNotFound})
					break
				}
			}
		}
	}

	var findings []Finding
	seenConf := map[string]bool{}
	seenLog := map[string]bool{}
	for _, in := range installs {
		if ctx.Err() != nil {
			break
		}
		if seenConf[CanonKey(in.conf)] {
			continue
		}
		seenConf[CanonKey(in.conf)] = true
		got, gs := webLogsFor(env, ws, in)
		gaps = append(gaps, gs...)
		for _, f := range got {
			k := CanonKey(f.Paths()[0])
			if seenLog[k] {
				continue
			}
			seenLog[k] = true
			findings = append(findings, f)
		}
	}
	return findings, gaps
}

// findWebConf looks for the config file in the service folder and then in the
// folder above it, and returns the first one found.
func findWebConf(fsys FS, ws webServer, binDir string) (webInstall, bool, []Gap) {
	for _, root := range []string{binDir, WinDir(binDir)} {
		if root == "" {
			continue
		}
		conf := WinJoin(root, ws.conf)
		info, err := statChecked(fsys, conf)
		switch {
		case err == nil:
			return webInstall{root: root, conf: conf, info: info}, true, nil
		case IsNotFound(err):
			continue
		default:
			return webInstall{}, false, []Gap{GapFromError(webDetectorName, "", ws.name+" configuration", err)}
		}
	}
	return webInstall{}, false, nil
}

// webLogsFor reads one config file and returns a finding for each access log it
// names that exists.
func webLogsFor(env Env, ws webServer, in webInstall) ([]Finding, []Gap) {
	confName := ws.name + " configuration"
	if in.info.Size > MaxConfigBytes {
		return nil, []Gap{{Detector: webDetectorName, Display: confName, Reason: ReasonTooLarge}}
	}
	data, err := env.FS.Head(in.conf, MaxConfigBytes)
	if err != nil {
		return nil, []Gap{GapFromError(webDetectorName, "", confName, err)}
	}
	values, serverRoot, declared := accessLogDirectives(ws, webStatements(ws, string(data)))

	// Apache resolves relative log paths against ServerRoot. A ServerRoot that is
	// relative or unsafe is not used, and the server folder stands in for it.
	logRoot := in.root
	if !ws.nginx && serverRoot != "" {
		if sr, err := ValidLogPath(serverRoot, ""); err == nil {
			logRoot = sr
		}
	}

	logName := ws.name + " access log"
	var refs []webLogRef
	var gaps []Gap
	if ws.nginx && !declared {
		p, err := ValidLogPath(WinJoin(in.root, "logs", "access.log"), in.root)
		if err != nil {
			gaps = append(gaps, GapFromError(webDetectorName, KindLogFile, logName, err))
		} else {
			refs = append(refs, webLogRef{path: p})
		}
	}
	for _, v := range values {
		if ws.nginx && v == "off" {
			continue
		}
		if notAFile(ws, v) {
			gaps = append(gaps, Gap{Detector: webDetectorName, Kind: KindLogFile, Display: logName, Reason: ReasonConfigUnparsed})
			continue
		}
		p, err := ValidLogPath(v, logRoot)
		if err != nil {
			gaps = append(gaps, GapFromError(webDetectorName, KindLogFile, logName, err))
			continue
		}
		refs = append(refs, webLogRef{path: p, declared: true})
	}

	var out []Finding
	for _, r := range refs {
		f, ok, gs := webLogFinding(env, ws, r)
		gaps = append(gaps, gs...)
		if ok {
			out = append(out, f)
		}
	}
	return out, gaps
}

// webLogFinding checks one access log and builds its finding. Confidence is 90
// when the config names the file and the importer confirms its head, 70 for the
// nginx default location, and 40 when the head does not confirm the importer.
func webLogFinding(env Env, ws webServer, r webLogRef) (Finding, bool, []Gap) {
	name := WinBase(r.path)
	info, err := statChecked(env.FS, r.path)
	if err != nil {
		if !r.declared && IsNotFound(err) {
			// The nginx default is not there: nothing to report.
			return Finding{}, false, nil
		}
		return Finding{}, false, []Gap{GapFromError(webDetectorName, KindLogFile, name, err)}
	}
	if info.IsDir {
		return Finding{}, false, []Gap{{Detector: webDetectorName, Kind: KindLogFile, Display: name, Reason: ReasonUnreadable}}
	}
	conf := 70
	if r.declared {
		conf = 90
	}
	if env.Importers != nil {
		imp, ok := env.Importers.ByName(ws.importer)
		if !ok {
			return Finding{}, false, []Gap{{Detector: webDetectorName, Kind: KindLogFile, Display: name, Reason: ReasonUnreadable}}
		}
		head, err := env.FS.Head(r.path, importer.HeadSize)
		if err != nil {
			return Finding{}, false, []Gap{GapFromError(webDetectorName, KindLogFile, name, err)}
		}
		if imp.Detect(head, name) <= 0 {
			conf = 40
		}
	}
	f := Finding{
		Detector: webDetectorName, Kind: KindLogFile, Importer: ws.importer,
		Display: name, Confidence: conf, Shows: ws.shows, Files: 1,
		SizeBytes: info.Size, Newest: info.ModTime, Cost: CostForSize(info.Size),
	}
	f.SetPaths(r.path)
	return f, true, nil
}

// notAFile reports a log destination that is not a file on disk: a syslog
// target, a pipe to a program, or (nginx) a variable the server fills in at run
// time.
func notAFile(ws webServer, v string) bool {
	return strings.HasPrefix(strings.ToLower(v), "syslog:") ||
		strings.HasPrefix(v, "|") ||
		(ws.nginx && strings.Contains(v, "$"))
}

// accessLogDirectives picks the values that matter from the statements: the
// access_log or CustomLog path, and for Apache the ServerRoot. Other directives
// are ignored here, so nothing else in the file is kept.
func accessLogDirectives(ws webServer, stmts [][]string) (values []string, serverRoot string, declared bool) {
	for _, st := range stmts {
		name := st[0]
		if ws.nginx {
			if name == "access_log" && len(st) > 1 {
				values = append(values, st[1])
				declared = true
			}
			continue
		}
		switch {
		case strings.EqualFold(name, "ServerRoot") && len(st) > 1:
			serverRoot = st[1]
		case strings.EqualFold(name, "CustomLog") && len(st) > 1:
			values = append(values, st[1])
			declared = true
		}
	}
	return values, serverRoot, declared
}

func webStatements(ws webServer, text string) [][]string {
	if ws.nginx {
		return nginxStatements(text)
	}
	return apacheStatements(text)
}

// nginxStatements splits an nginx config into statements. A statement is a run
// of words ended by ';', '{' or '}'. Words are separated by blanks, a quoted
// word keeps its inside, and '#' starts a comment outside a word.
func nginxStatements(src string) [][]string {
	var out [][]string
	var cur []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			cur = append(cur, word.String())
		}
		word.Reset()
		inWord = false
	}
	endStmt := func() {
		endWord()
		if len(cur) > 0 {
			out = append(out, cur)
		}
		cur = nil
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				// An unclosed quote ends the readable part of the file.
				endStmt()
				return out
			}
			endWord()
			word.WriteString(src[i+1 : j])
			inWord = true
			endWord()
			i = j
		case c == '#' && !inWord:
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == ';' || c == '{' || c == '}':
			endStmt()
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			endWord()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endStmt()
	return out
}

// apacheStatements returns one statement per directive line. A line ending in
// '\' continues on the next. Comment lines are dropped, and section lines such
// as <VirtualHost> come back with their own name so callers can skip them.
func apacheStatements(src string) [][]string {
	var out [][]string
	pending := ""
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasSuffix(line, `\`) {
			pending += strings.TrimSuffix(line, `\`) + " "
			continue
		}
		line = pending + line
		pending = ""
		words := splitApacheWords(strings.TrimSpace(line))
		if len(words) == 0 || strings.HasPrefix(words[0], "#") {
			continue
		}
		out = append(out, words)
	}
	return out
}

// splitApacheWords splits a line on blanks. A double-quoted word keeps its
// inside and drops the quotes; a backslash escapes the next byte in quotes.
func splitApacheWords(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		if s[i] == '"' {
			var b strings.Builder
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				b.WriteByte(s[j])
				j++
			}
			out = append(out, b.String())
			i = j + 1
			continue
		}
		j := i
		for j < len(s) && s[j] != ' ' && s[j] != '\t' {
			j++
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

// statChecked applies the spec 2.2 rules to a path before a detector uses it.
// The system must report the same canonical name, and neither the entry nor a
// folder above it may be a reparse point. It returns the metadata from Stat.
func statChecked(fsys FS, p string) (FileInfo, error) {
	canonical, reparse, err := fsys.Final(p)
	if err != nil {
		return FileInfo{}, err
	}
	if reparse || !strings.EqualFold(canonical, p) {
		return FileInfo{}, unsafeErr("reparse-or-alias")
	}
	info, err := fsys.Stat(p)
	if err != nil {
		return FileInfo{}, err
	}
	if info.Reparse {
		return FileInfo{}, unsafeErr("reparse")
	}
	return info, nil
}
