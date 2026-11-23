package privacy

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"api-audit-explorer/internal/querytext"
	"api-audit-explorer/internal/secretscrub"
)

const (
	// Mask replaces every scrubbed part.
	Mask = "[scrubbed]"
	// MaxErrorBytes is the size of an error text in the audit row (spec 3.6 error_text: 1 KiB).
	MaxErrorBytes = 1024
	// maxScrubInput bounds the work for one text. What is above it is cut before any pattern runs.
	maxScrubInput = 256 << 10
)

// Each pattern is RE2 (linear time). They run after secretscrub, which has already masked the values of secret settings.
var (
	// key=value parts of a connection string that name a place or a login (the password forms are secretscrub's).
	reConnKV = regexp.MustCompile(`(?i)\b(server|data source|address|addr|network address|host|hostname|initial catalog|database|user id|uid|user name|username|dsn|accountendpoint|endpoint|instance name|attachdbfilename|application name|workstation id)\s*=\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|\{[^}\r\n]*\}|[^;\r\n]*)`)
	// a database, broker or storage URL is removed whole; an http(s) or ftp URL loses its login, path and query.
	reDBURL   = regexp.MustCompile(`(?i)\b(?:mongodb(?:\+srv)?|mssql|sqlserver|postgres(?:ql)?|mysql|redis|rediss|amqps?|jdbc:[a-z0-9:]+|odbc|oracle|cassandra|couchdb|cosmos)://[^\s"'<>]*`)
	reWebURL  = regexp.MustCompile(`(?i)\b((?:https?|ftps?|wss?)://)([^\s"'<>/?#]*)([^\s"'<>]*)`)
	reFileURI = regexp.MustCompile(`(?i)\bfile:/{1,3}[^\s"'<>]*`)
	// Windows drive paths (directories may hold spaces, the last part may not), UNC paths, %VAR% paths, and common Unix home paths.
	reWinPath  = regexp.MustCompile(`(^|[^A-Za-z0-9_])[A-Za-z]:[\\/](?:[^\\/:*?"<>|\r\n]+[\\/])*[^\\/:*?"<>|\s]*`)
	reUNC      = regexp.MustCompile(`\\\\[^\s\\/:*?"<>|]+(?:\\[^\\/:*?"<>|\r\n]+)*`)
	reEnvPath  = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_]*%(?:[\\/][^\s"'<>|*?]*)?`)
	reUnixPath = regexp.MustCompile(`(?:^|[\s"'(=:])(/(?:home|Users|root|var|etc|tmp|opt|mnt|usr|srv|proc)/[^\s"'<>|*?]*)`)
	// text that reads as a SQL statement, so the shape rules of querytext apply to it.
	reLooksSQL = regexp.MustCompile(`(?is)\bselect\b.+\bfrom\b|\binsert\s+into\b|\bupdate\s+\S+\s+set\b|\bdelete\s+from\b|\bexec(?:ute)?\s+\S|\bmerge\s+into\b|\bwhere\s+\S+\s*(?:=|<|>|like\b|in\b|between\b)|\bvalues\s*\(`)
)

// ScrubText removes secrets, connection string parts, database and web URL detail, file paths and SQL literals from text that came
// from a host call, a driver or a script. It does not cut the size beyond maxScrubInput; ScrubError does. It never returns more than
// a constant factor of its input.
func ScrubText(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToValidUTF8(s, "?")
	if len(s) > maxScrubInput {
		s = cutUTF8(s, maxScrubInput)
	}
	// SQL first: a statement is reshaped (literals become ?), withheld when it may carry a credential.
	if reLooksSQL.MatchString(s) {
		s = querytext.Scrub(s, querytext.MaxMaxChars)
	}
	s = secretscrub.Mask(s, Mask)
	s = secretscrub.MaskURLUserinfo(s, Mask)
	s = reDBURL.ReplaceAllString(s, Mask)
	s = reFileURI.ReplaceAllString(s, Mask)
	s = reWebURL.ReplaceAllStringFunc(s, scrubWebURL)
	s = reConnKV.ReplaceAllString(s, "${1}="+Mask)
	s = reUNC.ReplaceAllString(s, Mask)
	s = reWinPath.ReplaceAllString(s, "${1}"+Mask)
	s = reEnvPath.ReplaceAllString(s, Mask)
	s = reUnixPath.ReplaceAllStringFunc(s, scrubUnixPath)
	return s
}

// ScrubError is ScrubText cut to MaxErrorBytes, for the message that goes back to the script and into the audit row.
func ScrubError(s string) string {
	return cutUTF8(ScrubText(s), MaxErrorBytes)
}

// scrubWebURL keeps scheme and host (the name of the service is useful and is not a person) and drops login, path and query: a
// path or a query may hold an id.
func scrubWebURL(m string) string {
	sub := reWebURL.FindStringSubmatch(m)
	if sub == nil {
		return Mask
	}
	host := sub[2]
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	if sub[3] == "" && !strings.Contains(sub[2], "@") {
		return m // nothing but scheme and host
	}
	return sub[1] + host + "/" + Mask
}

func scrubUnixPath(m string) string {
	i := strings.IndexByte(m, '/')
	if i < 0 {
		return m
	}
	return m[:i] + Mask
}

// cutUTF8 cuts s to at most n bytes at a character boundary and marks a cut with an ellipsis inside the limit.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const dots = "..."
	keep := n
	if n > len(dots) {
		keep = n - len(dots)
	}
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	if n > len(dots) {
		return s[:keep] + dots
	}
	return s[:keep]
}
