package discover

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
)

// MaxPathLen is the longest path (in UTF-16 units) a detector may emit. Longer
// values, and any long-path prefix, are refused.
const MaxPathLen = 260

// PathError says why a path was refused. Its text names a rule, never the
// value, so it is safe in logs and responses.
type PathError struct {
	Reason Reason // unsafe-path for shape, config-unparsed for content
	Code   string // short rule name, for tests and counters
}

func (e *PathError) Error() string { return "path refused: " + e.Code }

func unsafeErr(code string) *PathError   { return &PathError{Reason: ReasonUnsafePath, Code: code} }
func unparsedErr(code string) *PathError { return &PathError{Reason: ReasonConfigUnparsed, Code: code} }

// secretWords may not appear in a config value that is kept as a path (spec 2.2),
// compared case-insensitively.
var secretWords = []string{"password", "pwd", "secret", "key=", "token", "connectionstring", "user id", "accountkey"}

var (
	alias83    = regexp.MustCompile(`(?i)^[^~.]{1,6}~[0-9]+(\.[^.]{0,3})?$`)
	driveRoot  = regexp.MustCompile(`^[A-Za-z]:\\`)
	spaceRun   = regexp.MustCompile(`\s{2,}`)
	reservedRe = regexp.MustCompile(`(?i)^(con|prn|aux|nul|conin\$|conout\$|clock\$|com[0-9\x{00B9}\x{00B2}\x{00B3}]|lpt[0-9\x{00B9}\x{00B2}\x{00B3}])$`)
)

// ValidateAbsPath checks the shape of an absolute local path and returns its
// cleaned form: backslashes, upper-case drive letter, no empty or "." segments.
// It refuses UNC and device paths, long-path prefixes, "..", an alternate data
// stream, an 8.3 short-name segment, reserved device names, trailing dots or
// spaces, control and bidi characters, characters Windows forbids, relative
// paths and anything longer than MaxPathLen. It makes no OS call, so the
// system-level checks (reparse points, GetLongPathName) are FS.Final's job.
func ValidateAbsPath(p string) (string, error) {
	clean, perr := shape(p)
	if perr != nil {
		return "", perr
	}
	return clean, nil
}

func shape(p string) (string, *PathError) {
	if p == "" {
		return "", unsafeErr("relative")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || r == 0xFFFD {
			return "", unsafeErr("chars")
		}
	}
	p = strings.ReplaceAll(p, "/", `\`)
	switch {
	case strings.HasPrefix(p, `\\?\`), strings.HasPrefix(p, `\\.\`):
		return "", unsafeErr("prefix")
	case strings.HasPrefix(p, `\\`):
		return "", unsafeErr("unc")
	}
	if !driveRoot.MatchString(p) {
		return "", unsafeErr("relative")
	}
	drive, rest := strings.ToUpper(p[:1]), p[3:]
	if strings.ContainsRune(rest, ':') {
		return "", unsafeErr("ads")
	}
	if strings.ContainsAny(rest, `<>"|?*`) {
		return "", unsafeErr("chars")
	}
	var segs []string
	for _, s := range strings.Split(rest, `\`) {
		switch {
		case s == "" || s == ".":
			continue
		case s == "..":
			return "", unsafeErr("dotdot")
		case strings.HasSuffix(s, ".") || strings.HasSuffix(s, " "):
			return "", unsafeErr("dot-space")
		case alias83.MatchString(s):
			return "", unsafeErr("alias8.3")
		}
		base := s
		if i := strings.IndexByte(s, '.'); i >= 0 {
			base = s[:i]
		}
		if reservedRe.MatchString(strings.TrimRight(base, " ")) {
			return "", unsafeErr("reserved")
		}
		segs = append(segs, s)
	}
	clean := drive + `:\` + strings.Join(segs, `\`)
	if len(utf16.Encode([]rune(clean))) > MaxPathLen {
		return "", unsafeErr("length")
	}
	return clean, nil
}

// ValidLogPath checks a value read from a config file (or a path a detector
// built) and returns the absolute cleaned path. A relative value is joined to
// configDir; with no absolute configDir it is refused. Content rules come
// first: the value must carry none of the words in secretWords, no "=", ";",
// "@" or "://", no "%" or "${" token, and no run of whitespace. The shape rules
// of ValidateAbsPath then apply to the result. Failed content rules give
// ReasonConfigUnparsed; failed shape rules give ReasonUnsafePath. The error
// never contains the value.
func ValidLogPath(raw, configDir string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", unparsedErr("empty")
	}
	lower := strings.ToLower(v)
	for _, w := range secretWords {
		if strings.Contains(lower, w) {
			return "", unparsedErr("secret-word")
		}
	}
	if strings.ContainsAny(v, "=;@") || strings.Contains(v, "://") {
		return "", unparsedErr("value-shape")
	}
	if strings.Contains(v, "%") || strings.Contains(v, "${") {
		return "", unparsedErr("layout-token")
	}
	if spaceRun.MatchString(v) {
		return "", unparsedErr("whitespace-run")
	}
	n := strings.ReplaceAll(v, "/", `\`)
	if !strings.HasPrefix(n, `\`) && !driveRoot.MatchString(n) && !(len(n) >= 2 && n[1] == ':') {
		// Relative to the config file's folder.
		dir, perr := shape(configDir)
		if perr != nil {
			return "", unsafeErr("relative-no-dir")
		}
		n = dir + `\` + n
	}
	clean, perr := shape(n)
	if perr != nil {
		return "", perr
	}
	return clean, nil
}

// CanonKey is the form used to compare paths: cleaned and lower-cased, since
// local volumes ignore case. The input should already have passed
// ValidateAbsPath.
func CanonKey(clean string) string { return strings.ToLower(clean) }

// WinJoin joins path parts with backslashes without calling the os package.
func WinJoin(parts ...string) string {
	var out []string
	for i, p := range parts {
		p = strings.ReplaceAll(p, "/", `\`)
		if i > 0 {
			p = strings.Trim(p, `\`)
		} else {
			p = strings.TrimRight(p, `\`)
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, `\`)
}

// WinBase is the last segment of a backslash path.
func WinBase(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// WinDir is the path without its last segment. A drive root keeps its slash.
func WinDir(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`)
	i := strings.LastIndexByte(p, '\\')
	switch {
	case i < 0:
		return ""
	case i == 2 && p[1] == ':':
		return p[:3]
	}
	return p[:i]
}

// SplitImagePath takes a service image path as stored (an image, maybe quoted,
// then arguments) and returns the image's folder and base name. Arguments are
// cut and never returned. A quoted image ends at its closing quote. An unquoted
// one ends at the first ".exe" followed by a space or the end, else at the first
// space. A folder that fails ValidateAbsPath (for example one that starts with
// an environment variable) comes back as "", and so does an image with no base
// name.
func SplitImagePath(cmdline string) (dir, image string) {
	s := strings.TrimSpace(cmdline)
	if s == "" {
		return "", ""
	}
	var img string
	if s[0] == '"' {
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return "", ""
		}
		img = s[1 : 1+end]
	} else {
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
		img = s[:cut]
	}
	image = WinBase(img)
	if image == "" || strings.ContainsAny(image, "%:") {
		return "", ""
	}
	d, err := ValidateAbsPath(WinDir(img))
	if err != nil {
		return "", image
	}
	return d, image
}
