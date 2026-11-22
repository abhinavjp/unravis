package releasedash

import (
	"regexp"
	"strings"

	"api-audit-explorer/internal/secretscrub"
)

// Redactor hides what must never reach the screen, the clipboard or a saved log: tokens, private keys, passphrase style
// "name: value" pairs, other people's key file names and the user's folder name. The release script redacts each line when it
// writes the log (tools/release/ReleaseLib.ps1, Protect-LogLine); this does the same again when the page reads it, so an older
// or hand-edited log is covered too. It keeps state across lines, because a private key spans many of them.
type Redactor struct {
	inKey    bool
	keyLines int // lines hidden in the current key block
	names    []string
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
	ansiRE   = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)
	tokenRE  = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})`)
	bearerRE = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`)
	pairRE   = regexp.MustCompile(`(?i)([\w-]*(?:pass(?:word|phrase)|nonce|seed[-_ ]?phrase|key[-_ ]?seed|_seed|mnemonic|pwd|secret|token|api[-_ ]?key|authorization|credential|private[-_ ]?key))(["']?\s*[:=]\s*)("[^"]*"|'[^']*'|.+)`)
	// More secret shapes. Spaces and tabs are written out ("[ \t]") and not \s, and letters as [A-Za-z], so the PowerShell twin agrees
	// (.NET counts more characters as \s and \w than Go does). A flag with its value after a space or "=" ("--passphrase <v>"); an
	// environment style name ending in _PASS or _PIN ("SIGN_PASS=<v>"); user:password in a URL or after curl's -u or --user.
	flagRE     = regexp.MustCompile(`(?i)((?:^|[ \t])--?(?:pass(?:word|phrase|wd)?|pwd|token|secret|nonce|pin)(?:[ \t]+|=))(?:"[^"]*"|'[^']*'|[^ \t\r\n\f\v]+)`)
	envPassRE  = regexp.MustCompile(`(?i)([A-Za-z0-9_]*_(?:pass|passwd|pin)[ \t]*=[ \t]*)[^ \t\r\n\f\v]+`)
	urlCredRE  = regexp.MustCompile(`(://)[^ \t\r\n\f\v/:@]+:[^ \t\r\n\f\v/]+@`)
	userFlagRE = regexp.MustCompile(`((?:^|[ \t])--?(?:u|user)[ \t]+)[^ \t\r\n\f\v:]+:[^ \t\r\n\f\v]+`)
	keyBeginR  = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*(?:PRIVATE KEY|SECRET KEY|CERTIFICATE)[A-Z ]*-----`)
	keyEndR    = regexp.MustCompile(`-----END [A-Z0-9 ]*(?:PRIVATE KEY|SECRET KEY|CERTIFICATE)[A-Z ]*-----`)
	// A line that can still belong to a key block: nothing, base64, or a header such as "Proc-Type: 4,ENCRYPTED". After the CI prefix
	// ("job<TAB>step<TAB>") and the time stamp are taken off. Anything else (a sentence, a test result) ends a block that never closed.
	// "[ \t\r\f\v]" and not \s: the PowerShell twin must agree, and .NET counts more characters as \s than Go does. A "*" is a value GitHub masked.
	keyBodyR = regexp.MustCompile(`^[ \t\r\f\v]*$|^[ \t\r\f\v]*[A-Za-z0-9+/=_*-]+[ \t\r\f\v]*$|^[ \t\r\f\v]*[A-Za-z][A-Za-z0-9-]{0,30}:[ \t\r\f\v].*$`)
	// What a tool puts in front of each line it echoes: BuildKit "#7 0.512 ", nl or cat -n "  12<TAB>", "> ", "+ ". A key printed that way stays hidden.
	keyPfxR   = regexp.MustCompile(`^(?:#\d+[ \t]+[\d.]+[ \t]+|[ \t]*\d+\t|[>+][ \t]?)`)
	keyRunR   = regexp.MustCompile(`[A-Za-z0-9+/=]{40,}`) // a long base64 run is key text wherever it sits on the line
	stampPfx  = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z ?`)
	keyFileRE = regexp.MustCompile(`(?i)[\w.-]+\.(?:key|pem|pfx|p12|ppk)\b`)
	userDirRE = regexp.MustCompile(`(?i)([A-Za-z]:[\\/]+Users[\\/]+|/home/|/Users/)[^\\/\s"']+`)
	maxLine   = 2000
)

const keyHidden = "<private key hidden>"

const maxKeyLines = 400 // a 4096-bit key is about 30 lines; this only stops a runaway block

// keyBody is the text of a log line without its CI prefix ("job<TAB>step<TAB>") and time stamp.
func keyBody(s string) string {
	if p := strings.SplitN(s, "\t", 3); len(p) == 3 {
		s = p[2]
	}
	return stampPfx.ReplaceAllString(strings.TrimSuffix(s, "\r"), "")
}

// keyText says whether a line (CI prefix and time stamp already off) can still belong to a key block.
func keyText(body string) bool {
	b := strings.Trim(keyPfxR.ReplaceAllString(body, ""), "\"',")
	return keyBodyR.MatchString(b) || keyRunR.MatchString(body)
}

// Line returns the line cleaned of colour codes and control characters, with every secret hidden. The empty string is a valid
// answer (an empty line stays empty).
func (r *Redactor) Line(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	s = ansiRE.ReplaceAllString(s, "")
	body := keyBody(s)
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
			return "<private key hidden>"
		}
		// A block whose END line never came (a header printed on its own, a cut log) must not hide the rest of the log.
		if r.keyLines < maxKeyLines && keyText(body) {
			r.keyLines++
			return "<private key hidden>"
		}
		r.inKey = false
	}
	if keyBeginR.MatchString(s) {
		if !keyEndR.MatchString(s) {
			r.inKey, r.keyLines = true, 0
		}
		return "<private key hidden>"
	}
	// The shared pass first: the pair pattern below stops a quoted value at a backslash-escaped quote and does not read XML.
	s = secretscrub.MaskURLUserinfo(s, "<hidden>")
	s = secretscrub.Mask(s, "<hidden>")
	s = tokenRE.ReplaceAllString(s, "<token hidden>")
	s = bearerRE.ReplaceAllString(s, "$1 <hidden>")
	s = pairRE.ReplaceAllString(s, "$1$2<hidden>")
	s = flagRE.ReplaceAllString(s, "$1<hidden>")
	s = envPassRE.ReplaceAllString(s, "$1<hidden>")
	s = urlCredRE.ReplaceAllString(s, "${1}<hidden>@")
	s = userFlagRE.ReplaceAllString(s, "$1<hidden>")
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
