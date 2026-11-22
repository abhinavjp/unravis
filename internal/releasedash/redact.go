package releasedash

import (
	"regexp"
	"strings"
)

// Redactor hides what must never reach the screen, the clipboard or a saved log: tokens, private keys, passphrase style
// "name: value" pairs, other people's key file names and the user's folder name. The release script redacts each line when it
// writes the log (tools/release/ReleaseLib.ps1, Protect-LogLine); this does the same again when the page reads it, so an older
// or hand-edited log is covered too. It keeps state across lines, because a private key spans many of them.
type Redactor struct {
	inKey bool
	names []string
}

// NewRedactor hides the given machine and user names as well (names shorter than three characters are ignored).
func NewRedactor(names ...string) *Redactor {
	r := &Redactor{}
	for _, n := range names {
		if n = strings.TrimSpace(n); len(n) >= 3 {
			r.names = append(r.names, n)
		}
	}
	return r
}

var (
	ansiRE    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)
	tokenRE   = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})`)
	bearerRE  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`)
	pairRE    = regexp.MustCompile(`(?i)([\w-]*(?:pass(?:word|phrase)?|pwd|secret|token|api[-_ ]?key|authorization|credential|private[-_ ]?key))(["']?\s*[:=]\s*)("[^"]*"|'[^']*'|.+)`)
	keyBeginR = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*(?:PRIVATE KEY|SECRET KEY|CERTIFICATE)[A-Z ]*-----`)
	keyEndR   = regexp.MustCompile(`-----END [A-Z0-9 ]*(?:PRIVATE KEY|SECRET KEY|CERTIFICATE)[A-Z ]*-----`)
	keyFileRE = regexp.MustCompile(`(?i)[\w.-]+\.(?:key|pem|pfx|p12|ppk)\b`)
	userDirRE = regexp.MustCompile(`(?i)([A-Za-z]:[\\/]+Users[\\/]+|/home/|/Users/)[^\\/\s"']+`)
	maxLine   = 2000
)

// Line returns the line cleaned of colour codes and control characters, with every secret hidden. The empty string is a valid
// answer (an empty line stays empty).
func (r *Redactor) Line(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.Map(func(c rune) rune {
		if c == '\t' {
			return ' '
		}
		if c < 32 || c == 127 {
			return -1
		}
		return c
	}, s)
	if r.inKey {
		if keyEndR.MatchString(s) {
			r.inKey = false
		}
		return "<private key hidden>"
	}
	if keyBeginR.MatchString(s) {
		if !keyEndR.MatchString(s) {
			r.inKey = true
		}
		return "<private key hidden>"
	}
	s = tokenRE.ReplaceAllString(s, "<token hidden>")
	s = bearerRE.ReplaceAllString(s, "$1 <hidden>")
	s = pairRE.ReplaceAllString(s, "$1$2<hidden>")
	s = keyFileRE.ReplaceAllString(s, "<key file>")
	s = userDirRE.ReplaceAllString(s, "${1}<user>")
	for _, n := range r.names {
		s = replaceFold(s, n, "<name>")
	}
	if rs := []rune(s); len(rs) > maxLine {
		s = string(rs[:maxLine]) + " ...(line cut)"
	}
	return strings.TrimRight(s, " ")
}

func replaceFold(s, sub, with string) string {
	ls, lsub := strings.ToLower(s), strings.ToLower(sub)
	if len(ls) != len(s) || !strings.Contains(ls, lsub) { // non-ASCII case folding changed the length: leave it
		return s
	}
	var b strings.Builder
	alnum := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	from := 0
	for {
		i := strings.Index(ls[from:], lsub)
		if i < 0 {
			break
		}
		i += from
		end := i + len(sub)
		// Only a whole word: "abhinav" is hidden, the "abhinav" inside the repository name "abhinavjp" is not.
		if (i > 0 && alnum(ls[i-1])) || (end < len(ls) && alnum(ls[end])) {
			from = i + 1
			continue
		}
		b.WriteString(s[:i])
		b.WriteString(with)
		s, ls, from = s[end:], ls[end:], 0
	}
	b.WriteString(s)
	return b.String()
}
