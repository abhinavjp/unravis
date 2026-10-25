package discover

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/importer"
)

// This file holds what the Redis, MongoDB and SQL Server file detectors share.
// Every helper is prefixed db so it cannot clash with a detector written beside
// it. Nothing here opens a socket or runs a query: the database detectors read
// files and nothing else (spec ruling D8).

// localHostLabel is the label a finding carries until the server maps it to an
// entry in hosts.json (spec 7.1).
const localHostLabel = "local"

// ServiceCommandLines is an optional capability of Env.Services. Services.List
// drops every argument, so a detector that needs the one argument that names a
// config file asks here. An implementation returns the service's command line as
// stored. A detector passes it straight to one of the extractors below (for
// example RedisConfigArg), keeps only the value they return, and never stores,
// logs or returns the line itself, because it can hold keys and passwords.
//
// A Services without this method is not an error: the detectors fall back to the
// config file's usual place beside the service image.
type ServiceCommandLines interface {
	CommandLine(service string) (string, error)
}

// errNoCommandLine means the Services in use cannot give a command line.
var errNoCommandLine = fmt.Errorf("service command lines are not available: %w", fs.ErrNotExist)

func dbCommandLine(env Env, service string) (string, error) {
	cl, ok := env.Services.(ServiceCommandLines)
	if !ok {
		return "", errNoCommandLine
	}
	return cl.CommandLine(service)
}

// dbArgTokens returns the arguments of a service command line, the image cut
// off, split by the Windows quoting rules. Callers read one token and drop the
// rest.
func dbArgTokens(cmdline string) []string {
	s := strings.TrimSpace(cmdline)
	if s == "" {
		return nil
	}
	var rest string
	if s[0] == '"' {
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return nil
		}
		rest = s[end+2:]
	} else {
		// Same cut as SplitImagePath: the first ".exe" followed by a space or the
		// end, else the first space.
		lower := strings.ToLower(s)
		cut := -1
		for from := 0; ; {
			i := strings.Index(lower[from:], ".exe")
			if i < 0 {
				break
			}
			e := from + i + 4
			if e == len(s) || s[e] == ' ' {
				cut = e
				break
			}
			from = e
		}
		if cut < 0 {
			cut = strings.IndexByte(s, ' ')
			if cut < 0 {
				cut = len(s)
			}
		}
		rest = s[cut:]
	}
	return dbSplitArgs(rest)
}

// dbSplitArgs splits like CommandLineToArgvW: double quotes group, backslashes
// are literal unless they sit in front of a quote.
func dbSplitArgs(s string) []string {
	const maxTokens = 256
	var out []string
	var cur []rune
	in, started := false, false
	rs := []rune(s)
	flush := func() {
		if started {
			out = append(out, string(cur))
		}
		cur, started = cur[:0], false
	}
	for i := 0; i < len(rs) && len(out) < maxTokens; i++ {
		c := rs[i]
		switch {
		case c == '\\':
			n := 0
			for i < len(rs) && rs[i] == '\\' {
				n++
				i++
			}
			started = true
			if i < len(rs) && rs[i] == '"' {
				for k := 0; k < n/2; k++ {
					cur = append(cur, '\\')
				}
				if n%2 == 1 {
					cur = append(cur, '"')
				} else {
					in = !in
				}
			} else {
				for k := 0; k < n; k++ {
					cur = append(cur, '\\')
				}
				i--
			}
		case c == '"':
			in, started = !in, true
		case (c == ' ' || c == '\t') && !in:
			flush()
		default:
			started = true
			cur = append(cur, c)
		}
	}
	flush()
	return out
}

// RedisConfigArg returns the config file argument of a redis-server or memurai
// service command line, and nothing else from it. Redis takes the config path as
// the first argument; the Windows service wrapper puts a few value-less service
// flags in front of it. When the first real argument is an option (starts with
// "-") there is no config file, and ok is false: later arguments such as
// --requirepass are never read.
func RedisConfigArg(cmdline string) (path string, ok bool) {
	toks := dbArgTokens(cmdline)
	i := 0
skip:
	for i < len(toks) {
		switch strings.ToLower(toks[i]) {
		case "--service-run", "--service-install", "--service-uninstall", "--service-start", "--service-stop":
			i++
		case "--service-name":
			i += 2
		default:
			break skip
		}
	}
	if i < len(toks) && toks[i] != "" && !strings.HasPrefix(toks[i], "-") {
		return toks[i], true
	}
	return "", false
}

// MongoConfigArg returns the value of --config (or -f) from a mongod service
// command line, and nothing else from it.
func MongoConfigArg(cmdline string) (path string, ok bool) {
	toks := dbArgTokens(cmdline)
	for i, t := range toks {
		switch {
		case t == "--config" || t == "-f":
			if i+1 < len(toks) && toks[i+1] != "" {
				return toks[i+1], true
			}
			return "", false
		case strings.HasPrefix(t, "--config="):
			v := t[len("--config="):]
			return v, v != ""
		}
	}
	return "", false
}

// dbReadConfig reads one config file through FS.Head and hands its lines to
// each. A file at the 4 MiB cap is refused as config-unparsed rather than read
// from a cut-off tail. Lines longer than 64 KiB are skipped. The error carries
// no content.
func dbReadConfig(fsys FS, path string, each func(line string)) error {
	data, err := fsys.Head(path, MaxConfigBytes)
	if err != nil {
		return err
	}
	if len(data) >= MaxConfigBytes {
		return unparsedErr("config-too-large")
	}
	data = []byte(strings.TrimPrefix(string(data), "\xef\xbb\xbf"))
	r := bufio.NewReaderSize(strings.NewReader(string(data)), 64<<10)
	for {
		line, isPrefix, err := r.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return unparsedErr("config-read")
		}
		if isPrefix {
			// Skip the rest of an over-long line.
			for isPrefix && err == nil {
				_, isPrefix, err = r.ReadLine()
			}
			continue
		}
		each(string(line))
	}
}

// dbFinalCheck refuses a path whose file or any parent folder is a reparse
// point, or whose canonical name differs from the expected one (8.3 alias,
// junction). A missing path returns the not-found error.
func dbFinalCheck(fsys FS, path string) error {
	canon, reparse, err := fsys.Final(path)
	if err != nil {
		return err
	}
	if reparse {
		return unsafeErr("reparse")
	}
	if CanonKey(canon) != CanonKey(path) {
		return unsafeErr("final-mismatch")
	}
	return nil
}

// dbSniffAgrees reports whether the importer registry, shown the head of the
// file, picks the finding's importer. The scan reads at most importer.HeadSize
// bytes here (spec 1.4). A nil registry, an empty file or a tie never agree.
func dbSniffAgrees(env Env, path, importerName string) (agree bool, err error) {
	if env.Importers == nil {
		return false, nil
	}
	head, err := env.FS.Head(path, importer.HeadSize)
	if err != nil {
		return false, err
	}
	if len(head) == 0 {
		return false, nil
	}
	best, _, tied := env.Importers.Detect(head, WinBase(path))
	return best != nil && best.Name() == importerName && tied == nil, nil
}

// dbConfidence follows spec 1.3: 90 for a declared path the sniff confirms, 70
// for a default location the sniff confirms, 40 otherwise.
func dbConfidence(declared, agree bool) int {
	switch {
	case agree && declared:
		return 90
	case agree:
		return 70
	default:
		return 40
	}
}

// dbSetDetail names a set of files for Display: the first name, then a count.
func dbSetDetail(names []string) string {
	if len(names) == 0 {
		return ""
	}
	sorted := append([]string(nil), names...)
	sort.Slice(sorted, func(i, j int) bool { return strings.ToLower(sorted[i]) < strings.ToLower(sorted[j]) })
	if len(sorted) == 1 {
		return sorted[0]
	}
	return fmt.Sprintf("%s and %d more", sorted[0], len(sorted)-1)
}

// dbTally sums metadata over the files of one finding.
type dbTally struct {
	size           int64
	files          int
	oldest, newest time.Time
}

func (t *dbTally) add(fi FileInfo) {
	t.size += fi.Size
	t.files++
	if !fi.ModTime.IsZero() {
		if t.oldest.IsZero() || fi.ModTime.Before(t.oldest) {
			t.oldest = fi.ModTime
		}
		if fi.ModTime.After(t.newest) {
			t.newest = fi.ModTime
		}
	}
}

// dbSingleFileFinding checks one validated log path (final name, not a reparse
// point, a file) and builds its finding. importerName is the finding's importer.
// A failed probe becomes a gap with no path or value in it.
func dbSingleFileFinding(env Env, detector, gapDisplay, product, importerName, shows, path string, declared bool) ([]Finding, []Gap) {
	gapOf := func(err error) ([]Finding, []Gap) {
		return nil, []Gap{GapFromError(detector, KindLogFile, gapDisplay, err)}
	}
	if err := dbFinalCheck(env.FS, path); err != nil {
		return gapOf(err)
	}
	info, err := env.FS.Stat(path)
	if err != nil {
		return gapOf(err)
	}
	if info.IsDir || info.Reparse {
		return gapOf(unsafeErr("not-file"))
	}
	agree, err := dbSniffAgrees(env, path, importerName)
	if err != nil {
		return gapOf(err)
	}
	var t dbTally
	t.add(info)
	f := Finding{
		Detector: detector, Kind: KindLogFile, Importer: importerName, HostLabel: localHostLabel,
		Display:    product + " (" + WinBase(path) + ")",
		Confidence: dbConfidence(declared, agree), Shows: shows,
		SizeBytes: t.size, Files: 1, Oldest: t.oldest, Newest: t.newest, Cost: CostForSize(t.size),
	}
	f.SetPaths(path)
	return []Finding{f}, nil
}

// dbFindConfig looks for the first config file that exists among candidates.
// It returns the denied error when none exists and a probe was denied.
func dbFindConfig(fsys FS, candidates []string) (path string, err error) {
	var denied error
	for _, c := range candidates {
		info, e := fsys.Stat(c)
		switch {
		case e == nil && !info.IsDir:
			return c, nil
		case IsAccessDenied(e):
			if denied == nil {
				denied = e
			}
		case e != nil && !IsNotFound(e):
			return "", e
		}
	}
	if denied != nil {
		return "", denied
	}
	return "", fs.ErrNotExist
}

// dbIsRelative reports whether a config value is relative to a folder, by the
// same test ValidLogPath uses.
func dbIsRelative(v string) bool {
	n := strings.ReplaceAll(strings.TrimSpace(v), "/", `\`)
	return !strings.HasPrefix(n, `\`) && !(len(n) >= 2 && n[1] == ':')
}
