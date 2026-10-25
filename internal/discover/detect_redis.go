package discover

import (
	"context"
	"strings"
)

// RedisDetector finds the log file of a Redis or Memurai service on this host.
// It reads the service's redis.conf for two directives, logfile and dir, and
// keeps nothing else from the file (spec 5, ruling D3): requirepass, masterauth,
// user ACL lines, tls-*-file and every other directive are skipped as text and
// never stored. It makes no connection to Redis (ruling D8).
type RedisDetector struct{}

// Name is the detector name used in findings and gaps.
func (RedisDetector) Name() string { return "redis" }

var redisProducts = map[string]string{"redis-server.exe": "Redis", "memurai.exe": "Memurai"}

// redisDefaultConfigs are the config names the usual installers put beside the
// service image. They are tried only when the service command line is not
// available.
var redisDefaultConfigs = map[string][]string{
	"Redis":   {"redis.windows-service.conf", "redis.windows.conf", "redis.conf"},
	"Memurai": {"memurai.conf"},
}

// Detect looks for Redis and Memurai services and their log files.
func (d RedisDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	const name = "redis"
	if env.Services == nil || env.FS == nil {
		return nil, []Gap{{Detector: name, Kind: KindLogFile, Display: "Redis log", Reason: ReasonUnreadable}}
	}
	svcs, err := env.Services.List()
	if err != nil {
		return nil, []Gap{GapFromError(name, KindLogFile, "Redis log", err)}
	}
	var findings []Finding
	var gaps []Gap
	for _, svc := range svcs {
		if ctx.Err() != nil {
			break
		}
		product, ok := redisProducts[strings.ToLower(svc.Image)]
		if !ok {
			continue
		}
		f, g := redisOne(env, svc, product)
		findings = append(findings, f...)
		gaps = append(gaps, g...)
	}
	return findings, gaps
}

func redisOne(env Env, svc Service, product string) ([]Finding, []Gap) {
	const name = "redis"
	display := product + " log"
	gap := func(err error) ([]Finding, []Gap) {
		return nil, []Gap{GapFromError(name, KindLogFile, display, err)}
	}
	reason := func(r Reason) ([]Finding, []Gap) {
		return nil, []Gap{{Detector: name, Kind: KindLogFile, Display: display, Reason: r}}
	}

	// Which config file the service uses.
	var candidates []string
	declared := false
	cl, err := dbCommandLine(env, svc.Name)
	switch {
	case err == nil:
		arg, ok := RedisConfigArg(cl)
		if !ok {
			// The service starts without a config file: Redis logs to the console.
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
		for _, n := range redisDefaultConfigs[product] {
			candidates = append(candidates, WinJoin(svc.BinaryDir, n))
		}
	}
	cfg, err := dbFindConfig(env.FS, candidates)
	if err != nil {
		return gap(err)
	}
	if err := dbFinalCheck(env.FS, cfg); err != nil {
		return gap(err)
	}

	// Read the two directives and nothing else.
	var st redisConfState
	if err := dbReadConfig(env.FS, cfg, st.line); err != nil {
		return gap(err)
	}
	if st.logBad {
		return reason(ReasonConfigUnparsed)
	}
	if !st.haveLog || st.logfile == "" {
		// No logfile directive, or logfile "": Redis writes to the console.
		return reason(ReasonNotFound)
	}
	cfgDir := WinDir(cfg)
	base := cfgDir
	if st.haveDir && dbIsRelative(st.logfile) {
		// Redis changes into dir while it reads the config, and opens a relative
		// logfile from there.
		if st.dirBad {
			return reason(ReasonConfigUnparsed)
		}
		d, derr := ValidLogPath(st.dir, cfgDir)
		if derr != nil {
			return gap(derr)
		}
		base = d
	}
	logPath, perr := ValidLogPath(st.logfile, base)
	if perr != nil {
		return gap(perr)
	}
	return dbSingleFileFinding(env, name, display, product, "redis-log", "discover.redis.shows", logPath, declared)
}

// redisConfState keeps the only two directives the detector reads. Each is a raw
// path value that must still pass ValidLogPath before use.
type redisConfState struct {
	logfile, dir     string
	haveLog, haveDir bool
	logBad, dirBad   bool
}

const redisMaxValue = 1024

// line looks at one config line. A directive other than logfile and dir is
// dropped without being parsed.
func (s *redisConfState) line(raw string) {
	t := strings.TrimSpace(raw)
	if t == "" || t[0] == '#' {
		return
	}
	kw := t
	if i := strings.IndexAny(t, " \t"); i >= 0 {
		kw = t[:i]
	}
	var val *string
	var have, bad *bool
	switch strings.ToLower(kw) {
	case "logfile":
		val, have, bad = &s.logfile, &s.haveLog, &s.logBad
	case "dir":
		val, have, bad = &s.dir, &s.haveDir, &s.dirBad
	default:
		return
	}
	args, ok := redisSplitArgs(t[len(kw):])
	if !ok || len(args) != 1 || len(args[0]) > redisMaxValue {
		*have, *bad, *val = true, true, ""
		return
	}
	*have, *bad, *val = true, false, args[0]
}

// redisSplitArgs splits the arguments of one config line the way Redis does
// (sdssplitargs): double quotes with backslash escapes, single quotes with \'
// only, bare words. ok is false for an unbalanced quote.
func redisSplitArgs(s string) (args []string, ok bool) {
	isSpace := func(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
	hexv := func(c byte) (byte, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		}
		return 0, false
	}
	i := 0
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return args, true
		}
		var cur []byte
		inq, insq := false, false
		for {
			if i >= len(s) {
				if inq || insq {
					return nil, false
				}
				break
			}
			c := s[i]
			if inq {
				switch {
				case c == '\\' && i+3 < len(s) && s[i+1] == 'x':
					h1, ok1 := hexv(s[i+2])
					h2, ok2 := hexv(s[i+3])
					if ok1 && ok2 {
						cur = append(cur, h1<<4|h2)
						i += 3
					} else {
						cur = append(cur, 'x')
						i++
					}
				case c == '\\' && i+1 < len(s):
					i++
					switch s[i] {
					case 'n':
						cur = append(cur, '\n')
					case 'r':
						cur = append(cur, '\r')
					case 't':
						cur = append(cur, '\t')
					case 'b':
						cur = append(cur, '\b')
					case 'a':
						cur = append(cur, '\a')
					default:
						cur = append(cur, s[i])
					}
				case c == '"':
					if i+1 < len(s) && !isSpace(s[i+1]) {
						return nil, false
					}
					i++
					goto done
				default:
					cur = append(cur, c)
				}
			} else if insq {
				switch {
				case c == '\\' && i+1 < len(s) && s[i+1] == '\'':
					i++
					cur = append(cur, '\'')
				case c == '\'':
					if i+1 < len(s) && !isSpace(s[i+1]) {
						return nil, false
					}
					i++
					goto done
				default:
					cur = append(cur, c)
				}
			} else {
				switch c {
				case ' ', '\t', '\r', '\n':
					goto done
				case '"':
					inq = true
				case '\'':
					insq = true
				default:
					cur = append(cur, c)
				}
			}
			i++
		}
	done:
		args = append(args, string(cur))
	}
}
