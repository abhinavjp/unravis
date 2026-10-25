package discover

import (
	"context"
	"strings"
)

// MongoDetector finds the log file of a MongoDB service on this host. It reads
// mongod.cfg for systemLog.path and systemLog.destination with a small
// line-based reader and keeps nothing else from the file: security.keyFile,
// net.tls.*, setParameter and every other key are skipped as text (spec 5,
// ruling D3). It makes no connection to MongoDB (ruling D8). The log it offers
// is read by the mongod-json importer, which takes the JSON format of MongoDB
// 4.4 and later.
type MongoDetector struct{}

// Name is the detector name used in findings and gaps.
func (MongoDetector) Name() string { return "mongo" }

// Detect looks for mongod services and their log files.
func (d MongoDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	const name = "mongo"
	if env.Services == nil || env.FS == nil {
		return nil, []Gap{{Detector: name, Kind: KindLogFile, Display: "MongoDB log", Reason: ReasonUnreadable}}
	}
	svcs, err := env.Services.List()
	if err != nil {
		return nil, []Gap{GapFromError(name, KindLogFile, "MongoDB log", err)}
	}
	var findings []Finding
	var gaps []Gap
	for _, svc := range svcs {
		if ctx.Err() != nil {
			break
		}
		if !strings.EqualFold(svc.Image, "mongod.exe") {
			continue
		}
		f, g := mongoOne(env, svc)
		findings = append(findings, f...)
		gaps = append(gaps, g...)
	}
	return findings, gaps
}

func mongoOne(env Env, svc Service) ([]Finding, []Gap) {
	const name, display = "mongo", "MongoDB log"
	gap := func(err error) ([]Finding, []Gap) {
		return nil, []Gap{GapFromError(name, KindLogFile, display, err)}
	}
	reason := func(r Reason) ([]Finding, []Gap) {
		return nil, []Gap{{Detector: name, Kind: KindLogFile, Display: display, Reason: r}}
	}

	var candidates []string
	declared := false
	cl, err := dbCommandLine(env, svc.Name)
	switch {
	case err == nil:
		arg, ok := MongoConfigArg(cl)
		if !ok {
			// mongod started with command-line options only: nothing to read.
			return reason(ReasonNotFound)
		}
		p, perr := ValidLogPath(arg, svc.BinaryDir)
		if perr != nil {
			return gap(perr)
		}
		candidates, declared = []string{p}, true
	case IsAccessDenied(err):
		return gap(err)
	}
	if candidates == nil {
		if svc.BinaryDir == "" {
			return reason(ReasonConfigUnparsed)
		}
		candidates = []string{WinJoin(svc.BinaryDir, "mongod.cfg")}
	}
	cfg, err := dbFindConfig(env.FS, candidates)
	if err != nil {
		return gap(err)
	}
	if err := dbFinalCheck(env.FS, cfg); err != nil {
		return gap(err)
	}

	var st mongoLogCfg
	if err := dbReadConfig(env.FS, cfg, st.line); err != nil {
		return gap(err)
	}
	if st.unparsed {
		return reason(ReasonConfigUnparsed)
	}
	if st.havePath && st.pathBad {
		return reason(ReasonConfigUnparsed)
	}
	if st.haveDest && !strings.EqualFold(st.dest, "file") {
		// syslog or an unknown destination: no log file to read.
		return reason(ReasonNotFound)
	}
	if !st.havePath || st.path == "" {
		return reason(ReasonNotFound)
	}
	logPath, perr := ValidLogPath(st.path, WinDir(cfg))
	if perr != nil {
		return gap(perr)
	}
	return dbSingleFileFinding(env, name, display, "MongoDB", "mongod-json", "discover.mongo.shows", logPath, declared)
}

// mongoLogCfg is a reader for exactly two keys of a mongod YAML config:
// systemLog.destination and systemLog.path. It works one line at a time, tracks
// only whether it is inside the top-level systemLog block and at what
// indentation, and stores a value only for those two keys. Every other line is
// looked at for its indentation and key name and dropped.
type mongoLogCfg struct {
	inSys     bool
	sysIndent int // indentation of the keys directly under systemLog, -1 until seen

	dest, path         string
	haveDest, havePath bool
	pathBad            bool
	unparsed           bool // systemLog written in a form this reader does not follow
}

const mongoMaxValue = 1024

func (m *mongoLogCfg) line(raw string) {
	raw = strings.TrimRight(raw, " \t\r")
	t := strings.TrimLeft(raw, " \t")
	if t == "" || t[0] == '#' || t == "---" || t == "..." {
		return
	}
	indent := len(raw) - len(t)
	key, rest, ok := yamlKey(t)
	if !ok {
		return
	}
	if indent == 0 {
		m.inSys, m.sysIndent = false, -1
		if key == "systemLog" {
			if yamlHasValue(rest) {
				// Flow style ("systemLog: {path: ...}") or a scalar: not followed.
				m.unparsed = true
				return
			}
			m.inSys = true
		}
		return
	}
	if !m.inSys {
		return
	}
	if m.sysIndent < 0 {
		m.sysIndent = indent
	}
	if indent != m.sysIndent {
		return // nested deeper (component, verbosity) or misaligned
	}
	switch key {
	case "path":
		v, ok := yamlScalar(rest)
		m.havePath = true
		if !ok || len(v) > mongoMaxValue {
			m.pathBad, m.path = true, ""
			return
		}
		m.pathBad, m.path = false, v
	case "destination":
		v, ok := yamlScalar(rest)
		m.haveDest = true
		if !ok || len(v) > 64 {
			v = "?"
		}
		m.dest = v
	}
}

// yamlKey splits "key: rest". The key must be a bare word; the colon must be
// followed by a space or end the line.
func yamlKey(t string) (key, rest string, ok bool) {
	for i := 0; i < len(t); i++ {
		if t[i] == ':' && (i+1 == len(t) || t[i+1] == ' ' || t[i+1] == '\t') {
			key = strings.TrimSpace(t[:i])
			if key == "" || strings.ContainsAny(key, `"'{}[]#&*!|>%@`+"`") {
				return "", "", false
			}
			return key, t[i+1:], true
		}
	}
	return "", "", false
}

// yamlHasValue reports whether the text after a key's colon holds anything but a
// comment.
func yamlHasValue(rest string) bool {
	r := strings.TrimSpace(rest)
	return r != "" && r[0] != '#'
}

// yamlScalar reads one plain, single-quoted or double-quoted scalar. In double
// quotes only \\, \" and \/ are understood; any other escape makes the value
// unreadable rather than guessed.
func yamlScalar(rest string) (string, bool) {
	s := strings.TrimSpace(rest)
	if s == "" || s[0] == '#' {
		return "", true
	}
	switch s[0] {
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			switch c := s[i]; {
			case c == '\\':
				if i+1 >= len(s) {
					return "", false
				}
				i++
				switch s[i] {
				case '\\', '"', '/':
					b.WriteByte(s[i])
				default:
					return "", false
				}
			case c == '"':
				if yamlTrailerOK(s[i+1:]) {
					return b.String(), true
				}
				return "", false
			default:
				b.WriteByte(c)
			}
		}
		return "", false
	case '\'':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				if yamlTrailerOK(s[i+1:]) {
					return b.String(), true
				}
				return "", false
			}
			b.WriteByte(s[i])
		}
		return "", false
	case '[', '{', '|', '>', '&', '*', '!', '%', '@', '`':
		return "", false
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s), true
}

// yamlTrailerOK allows only white space or a comment after a quoted scalar.
func yamlTrailerOK(rest string) bool {
	r := strings.TrimSpace(rest)
	return r == "" || r[0] == '#'
}
