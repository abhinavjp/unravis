package discover

import (
	"errors"
	"regexp"
	"strings"
)

// File name templates for the application-logger detector. A config file names a
// log with literal text and, for a rolling log, a few date or counter tokens. The
// detector never keeps the token text. It turns each accepted token into one
// placeholder rune, and later into a regular expression that picks matching file
// names out of a directory listing. Any token it does not know makes the whole
// value config-unparsed (spec section 5, NLog row).
const (
	phDigits = '\uE000' // a run of digits: a date part such as 2001 or 09
	phAlpha  = '\uE001' // a run of letters: a month or day name
	phSeq    = '\uE002' // an archive counter or date: digits with - . _ between
)

func isPlaceholder(r rune) bool { return r >= phDigits && r <= phSeq }

func hasPlaceholder(s string) bool {
	for _, r := range s {
		if isPlaceholder(r) {
			return true
		}
	}
	return false
}

// tmplRegex turns template text into a regular expression fragment.
func tmplRegex(tmpl string) string {
	var b strings.Builder
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			b.WriteString(regexp.QuoteMeta(lit.String()))
			lit.Reset()
		}
	}
	for _, r := range tmpl {
		switch r {
		case phDigits:
			flush()
			b.WriteString(`[0-9]+`)
		case phAlpha:
			flush()
			b.WriteString(`[A-Za-z]+`)
		case phSeq:
			flush()
			b.WriteString(`[0-9]+(?:[-._][0-9]+)*`)
		default:
			lit.WriteRune(r)
		}
	}
	flush()
	return b.String()
}

// tmplLabel shows a template for a person: each placeholder becomes "*".
func tmplLabel(tmpl string) string {
	var b strings.Builder
	star := false
	for _, r := range tmpl {
		if isPlaceholder(r) {
			if !star {
				b.WriteByte('*')
			}
			star = true
			continue
		}
		star = false
		b.WriteRune(r)
	}
	return b.String()
}

// tmplProbe replaces each placeholder with "0" so ordinary path checks can run
// on the template.
func tmplProbe(tmpl string) string {
	return strings.Map(func(r rune) rune {
		if isPlaceholder(r) {
			return '0'
		}
		return r
	}, tmpl)
}

var safeLiteral = regexp.MustCompile(`^[A-Za-z0-9._ -]*$`)

// dateTemplate converts a .NET or Java style date format ("yyyy-MM-dd",
// "'.'yyyyMMdd") to a template. Letters other than the known date fields, any
// separator that a file name cannot hold, an unclosed quote, a secret word or a
// length over 64 make it fail.
func dateTemplate(pat string) (string, bool) {
	if pat == "" || len(pat) > 64 {
		return "", false
	}
	lower := strings.ToLower(pat)
	for _, w := range secretWords {
		if strings.Contains(lower, w) {
			return "", false
		}
	}
	rs := []rune(pat)
	var b strings.Builder
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == '\'':
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j >= len(rs) {
				return "", false
			}
			lit := string(rs[i+1 : j])
			if !safeLiteral.MatchString(lit) {
				return "", false
			}
			b.WriteString(lit)
			i = j + 1
		case strings.ContainsRune("yMdHhmsfF", c) || c == 't':
			j := i
			for j < len(rs) && rs[j] == c {
				j++
			}
			n := j - i
			if (c == 'M' || c == 'd') && n >= 3 || c == 't' {
				b.WriteRune(phAlpha)
			} else {
				b.WriteRune(phDigits)
			}
			i = j
		case c == '-' || c == '_' || c == '.' || c == ' ':
			b.WriteRune(c)
			i++
		default:
			return "", false
		}
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

// errLayout is the refusal for a layout renderer or token the detector does not
// resolve. Its text names a rule, never the value.
func errLayout(code string) error { return unparsedErr(code) }

// expandNLog turns an NLog fileName or archiveFileName into a template. It allows
// a leading ${basedir}, ${shortdate}, and ${date:format=...} (with an optional
// universaltime=true|false). Any other renderer, a nested or unclosed ${, or a
// ${basedir} that is not first is refused. In an archive name {#} stands for the
// archive counter. The second result says whether ${basedir} led the value, so
// the rest is relative to the config's folder.
func expandNLog(raw string, archive bool) (tmpl string, relative bool, err error) {
	if strings.IndexFunc(raw, isPlaceholder) >= 0 {
		return "", false, errLayout("layout-token")
	}
	var b strings.Builder
	rest := raw
	for {
		i := strings.Index(rest, "${")
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		j := strings.IndexByte(rest[i+2:], '}')
		if j < 0 {
			return "", false, errLayout("layout-token")
		}
		inner := rest[i+2 : i+2+j]
		if strings.Contains(inner, "${") {
			return "", false, errLayout("layout-token")
		}
		rest = rest[i+2+j+1:]
		name, params, _ := strings.Cut(inner, ":")
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "basedir":
			if params != "" || b.Len() != 0 || relative {
				return "", false, errLayout("layout-renderer")
			}
			if rest == "" || (rest[0] != '\\' && rest[0] != '/') {
				return "", false, errLayout("layout-renderer")
			}
			relative = true
			rest = rest[1:]
		case "shortdate":
			if params != "" {
				return "", false, errLayout("layout-renderer")
			}
			t, _ := dateTemplate("yyyy-MM-dd")
			b.WriteString(t)
		case "date":
			t, ok := nlogDate(params)
			if !ok {
				return "", false, errLayout("layout-renderer")
			}
			b.WriteString(t)
		default:
			return "", false, errLayout("layout-renderer")
		}
	}
	out := b.String()
	if archive {
		out = counterToken.ReplaceAllString(out, string(phSeq))
	}
	if strings.ContainsAny(out, "{}") {
		return "", false, errLayout("layout-token")
	}
	return out, relative, nil
}

var counterToken = regexp.MustCompile(`\{#+\}`)

// nlogDate reads the parameters of ${date:...}: format=<f> and optionally
// universaltime=true|false. Anything else is refused.
func nlogDate(params string) (string, bool) {
	var format string
	for _, p := range strings.Split(params, ":") {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return "", false
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "format":
			format = v
		case "universaltime":
			if lv := strings.ToLower(v); lv != "true" && lv != "false" {
				return "", false
			}
		default:
			return "", false
		}
	}
	return dateTemplate(format)
}

// expandBraces turns a Serilog or logging-provider path into a template. With
// tokens false any brace is refused. With tokens true the rolling tokens {Date},
// {Hour} and {HalfHour} are accepted and every other brace is refused.
func expandBraces(raw string, tokens bool) (string, error) {
	if strings.IndexFunc(raw, isPlaceholder) >= 0 {
		return "", errLayout("layout-token")
	}
	if !strings.ContainsAny(raw, "{}") {
		return raw, nil
	}
	if !tokens {
		return "", errLayout("layout-token")
	}
	var b strings.Builder
	rest := raw
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			if strings.IndexByte(rest, '}') >= 0 {
				return "", errLayout("layout-token")
			}
			b.WriteString(rest)
			return b.String(), nil
		}
		if strings.IndexByte(rest[:i], '}') >= 0 {
			return "", errLayout("layout-token")
		}
		b.WriteString(rest[:i])
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return "", errLayout("layout-token")
		}
		switch strings.ToLower(rest[i+1 : i+j]) {
		case "date", "hour", "halfhour":
			b.WriteRune(phDigits)
		default:
			return "", errLayout("layout-token")
		}
		rest = rest[i+j+1:]
	}
}

// resolveTemplate checks a template that stands for a log path and splits it into
// a clean absolute folder and a file name template. A relative value is relative
// to configDir. A placeholder is allowed only in the file name; a placeholder in
// a folder name would need a directory walk, so it is refused. The folder passes
// ValidLogPath, and so does the file name with each placeholder read as "0".
func resolveTemplate(tmpl, configDir string) (dir, name string, err error) {
	t := strings.ReplaceAll(tmpl, "/", `\`)
	idx := strings.LastIndexByte(t, '\\')
	dirRaw, nameT := "", t
	if idx >= 0 {
		dirRaw, nameT = t[:idx], t[idx+1:]
	}
	if nameT == "" {
		return "", "", unparsedErr("no-file-name")
	}
	if hasPlaceholder(dirRaw) {
		return "", "", unparsedErr("folder-token")
	}
	switch {
	case idx == 0:
		dirRaw = `\`
	case len(dirRaw) == 2 && dirRaw[1] == ':':
		dirRaw += `\`
	}
	if dirRaw == "" {
		d, derr := ValidateAbsPath(configDir)
		if derr != nil {
			return "", "", unsafeErr("relative-no-dir")
		}
		dir = d
	} else {
		d, derr := ValidLogPath(dirRaw, configDir)
		if derr != nil {
			return "", "", derr
		}
		dir = d
	}
	if _, nerr := ValidLogPath(tmplProbe(nameT), `C:\x`); nerr != nil {
		return "", "", nerr
	}
	return dir, nameT, nil
}

// encodeSink and decodeSink carry a resolved sink, or the reason it was refused,
// through the parsers' string maps. The refusal holds a reason code and nothing
// from the value. Resolved text holds no NUL: ValidateAbsPath refuses control
// characters.
func encodeSink(dir, name string, err error) string {
	if err != nil {
		return "e\x00" + codeOf(err)
	}
	return "o\x00" + dir + "\x00" + name
}

func decodeSink(s string) (dir, name string, reason Reason, ok bool) {
	parts := strings.Split(s, "\x00")
	switch {
	case len(parts) == 3 && parts[0] == "o" && parts[1] != "" && parts[2] != "":
		return parts[1], parts[2], "", true
	case len(parts) == 2 && parts[0] == "e":
		return "", "", Reason(parts[1]), false
	}
	return "", "", ReasonConfigUnparsed, false
}

type pathKind int

const (
	kindPlain       pathKind = iota // log4net file, Serilog path, Logging:File:Path: no braces
	kindTokens                      // Serilog pathFormat, Logging:File:PathFormat: {Date}, {Hour}, {HalfHour}
	kindNLog                        // NLog fileName
	kindNLogArchive                 // NLog archiveFileName: also {#}
)

// resolveRaw turns one raw path value from a config into an encoded sink. It is
// the only place a raw value is read; the result holds a clean folder and a file
// name template, or a reason code.
func resolveRaw(raw, configDir string, kind pathKind) string {
	var (
		tmpl string
		err  error
	)
	switch kind {
	case kindPlain:
		tmpl, err = expandBraces(raw, false)
	case kindTokens:
		tmpl, err = expandBraces(raw, true)
	case kindNLog:
		tmpl, _, err = expandNLog(raw, false)
	case kindNLogArchive:
		tmpl, _, err = expandNLog(raw, true)
	}
	if err != nil {
		return encodeSink("", "", err)
	}
	tmpl = strings.TrimSpace(tmpl)
	dir, name, err := resolveTemplate(tmpl, configDir)
	return encodeSink(dir, name, err)
}

// serilogRe matches a Serilog file name: the stem, then the rolling date when the
// interval is not Infinite, then a size-roll number (_001), then the extension.
func serilogRe(name string, dated bool) *regexp.Regexp {
	stem, ext := splitExt(name)
	date := ""
	if dated {
		date = `(?:[0-9]+)?`
	}
	return regexp.MustCompile(`(?i)^` + regexp.QuoteMeta(stem) + date + `(?:_[0-9]+)?` + regexp.QuoteMeta(ext) + `$`)
}

// codeOf maps a refused value to the reason string stored in a Reject.
func codeOf(err error) string {
	var pe *PathError
	if errors.As(err, &pe) {
		return string(pe.Reason)
	}
	return string(ReasonConfigUnparsed)
}

// rollingRe matches the active file and its rolled forms: the base name, optional
// date text and an optional number after the base name or before the extension
// (log4net's preserveLogFileNameExtension). dateRe and num may be empty.
func rollingRe(base, dateRe string, num bool) *regexp.Regexp {
	stem, ext := splitExt(base)
	date, n := "", ""
	if dateRe != "" {
		date = "(?:" + dateRe + ")?"
	}
	if num {
		n = `(?:[._-][0-9]+)?`
	}
	qb, qs, qe := regexp.QuoteMeta(base), regexp.QuoteMeta(stem), regexp.QuoteMeta(ext)
	return regexp.MustCompile(`(?i)^(?:` + qb + date + n + `|` + qs + date + n + qe + `)$`)
}

// templateRe matches a file name template. With num set a number may follow the
// stem, as in app.1.log and app.log.1.
func templateRe(nameTmpl string, num bool) *regexp.Regexp {
	if !num {
		return regexp.MustCompile(`(?i)^` + tmplRegex(nameTmpl) + `$`)
	}
	stem, ext := splitExt(nameTmpl)
	n := `(?:[._-][0-9]+)?`
	return regexp.MustCompile(`(?i)^(?:` + tmplRegex(nameTmpl) + n + `|` + tmplRegex(stem) + n + regexp.QuoteMeta(ext) + `)$`)
}

// splitExt splits at the last dot that is not the first character.
func splitExt(name string) (stem, ext string) {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i], name[i:]
	}
	return name, ""
}
