package applog

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// Sanitize is the one helper every log line passes through. The log file is
// written so the owner can hand it to a developer, so it must never carry a
// secret even when an error message from a driver or a source does. It strips:
//
//   - connection strings and URLs with credentials (user:pass@host),
//   - password=/pwd=/secret=/token=/key= values (and the JSON "key": "value" form),
//   - bearer tokens,
//   - any value registered with RegisterSecret (the session token, case keys),
//   - anything that looks like a base64 secret longer than 24 characters,
//   - paths under the user profile, replaced with %USERPROFILE%.
//
// It is deliberately blunt: when a value might be a secret, it goes.
var (
	reBearer     = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	reJSONSecret = regexp.MustCompile(`(?i)("(?:password|passwd|pwd|secret|token|api[-_]?key|key)"\s*:\s*")[^"]*"`)
	reURLCreds   = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*)://[^\s/@:]+:[^\s/@]*@`)
	reBareCreds  = regexp.MustCompile(`\b[^\s/:@]+:[^\s/@]+@`)
	reKVSecret   = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[-_]?key|key)\s*=\s*[^\s&;,]+`)
	reLongToken  = regexp.MustCompile(`[A-Za-z0-9+/_-]{25,}={0,2}`)
)

const redacted = "[redacted]"

var (
	secretsMu sync.RWMutex
	secrets   []string
)

// RegisterSecret adds a value that must never appear in the log (the session
// token, a case key). Short values are ignored: replacing a tiny string would
// mangle ordinary words, and real secrets here are long.
func RegisterSecret(value string) {
	if len(value) < 8 {
		return
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	for _, s := range secrets {
		if s == value {
			return
		}
	}
	secrets = append(secrets, value)
}

// Sanitize returns text with every known secret shape removed.
func Sanitize(text string) string {
	if text == "" {
		return text
	}
	out := text
	secretsMu.RLock()
	for _, s := range secrets {
		out = strings.ReplaceAll(out, s, redacted)
	}
	secretsMu.RUnlock()
	for _, dir := range profileDirs() {
		out = replaceFold(out, dir, "%USERPROFILE%")
	}
	out = reBearer.ReplaceAllString(out, "Bearer "+redacted)
	out = reJSONSecret.ReplaceAllString(out, "${1}"+redacted+`"`)
	out = reURLCreds.ReplaceAllString(out, "${1}://"+redacted+"@")
	out = reBareCreds.ReplaceAllString(out, redacted+"@")
	out = reKVSecret.ReplaceAllString(out, "${1}="+redacted)
	out = reLongToken.ReplaceAllStringFunc(out, func(m string) string {
		if looksLikeSecret(m) {
			return redacted
		}
		return m
	})
	return out
}

// looksLikeSecret reports whether a long base64-shaped run is worth treating
// as a secret: a mix of letters and digits (a plain long word has no digit and
// keeps its place in a message).
func looksLikeSecret(s string) bool {
	var digit, letter bool
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			digit = true
		case s[i] >= 'a' && s[i] <= 'z', s[i] >= 'A' && s[i] <= 'Z':
			letter = true
		}
	}
	return digit && letter
}

// profileDirs lists the spellings of the user's profile folder: the OS home
// directory and the USERPROFILE/HOME variables, each with both slash forms,
// longest first so the most specific spelling wins.
func profileDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(dir string) {
		for _, v := range []string{dir, strings.ReplaceAll(dir, `\`, "/"), strings.ReplaceAll(dir, "/", `\`)} {
			if v != "" && !seen[v] {
				seen[v] = true
				dirs = append(dirs, v)
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
	}
	add(os.Getenv("USERPROFILE"))
	add(os.Getenv("HOME"))
	// Longest first, so a longer spelling is replaced before a prefix of it.
	for i := 0; i < len(dirs); i++ {
		for j := i + 1; j < len(dirs); j++ {
			if len(dirs[j]) > len(dirs[i]) {
				dirs[i], dirs[j] = dirs[j], dirs[i]
			}
		}
	}
	return dirs
}

// replaceFold replaces every case-insensitive occurrence of old in s.
func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lower, needle := strings.ToLower(s), strings.ToLower(old)
	var b strings.Builder
	for {
		i := strings.Index(lower, needle)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s = s[i+len(old):]
		lower = lower[i+len(old):]
	}
}
