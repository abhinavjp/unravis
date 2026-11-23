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
	// credentials in a header or a token shape: the whole value of an Authorization or cookie header, "Bearer x", a JWT, and the
	// prefixes of the common key formats (AWS access key id, GitHub, Slack, OpenAI-style, Google API key).
	reAuthHdr  = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|x-api-key|api-key|apikey|cookie|set-cookie)\b(\s*[:=]\s*)[^\r\n]*`)
	reBearer   = regexp.MustCompile(`(?i)\b(bearer)\s+[^\s"',;]{6,}`)
	reJWT      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}(?:\.[A-Za-z0-9_-]*)?`)
	reKeyShape = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b|\bgh[pousr]_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}|\bxox[abprs]-[A-Za-z0-9-]{10,}|\bsk-[A-Za-z0-9_-]{20,}|\bAIza[0-9A-Za-z_-]{35}`)
	// key=value parts of a connection string that name a place or a login (the password forms are secretscrub's).
	reConnKV = regexp.MustCompile(`(?i)\b(server|data source|address|addr|network address|host|hostname|initial catalog|database|user id|uid|user name|username|dsn|accountendpoint|endpoint|instance name|attachdbfilename|application name|workstation id)\s*=\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|\{[^}\r\n]*\}|[^;\r\n]*)`)
	// a database, broker or storage URL is removed whole; an http(s) or ftp URL loses its login, path and query.
	reDBURL   = regexp.MustCompile(`(?i)\b(?:mongodb(?:\+srv)?|mssql|sqlserver|postgres(?:ql)?|mysql|redis|rediss|amqps?|jdbc:[a-z0-9:]+|odbc|oracle|cassandra|couchdb|cosmos)://[^\s"'<>]*`)
	reWebURL  = regexp.MustCompile(`(?i)\b((?:https?|ftps?|wss?)://)([^\s"'<>/?#]*)([^\s"'<>]*)`)
	reFileURI = regexp.MustCompile(`(?i)\bfile:/{1,3}[^\s"'<>]*`)
	// the values that driver errors print without quotes or in other quotes (SQL Server 2627, Postgres 23505 and 22P02, Mongo E11000).
	reDupValue = regexp.MustCompile(`(?i)(duplicate key value is\s*)\([^)]*\)`)
	rePgKey    = regexp.MustCompile(`(?i)\bKey\s*\([^)]*\)\s*=\s*\([^)]*\)`)
	reMongoDup = regexp.MustCompile(`(?i)(dup key:?\s*)\{[^}]*\}`)
	rePgSyntax = regexp.MustCompile(`(?i)(invalid input syntax for[^:\r\n]*:\s*)"[^"\r\n]*"`)
	rePgValue  = regexp.MustCompile(`(?i)(\bvalue\s+)"[^"\r\n]*"`)
	// Windows drive paths and UNC paths: the path runs to a ":", a quote, a ";" or the end of the line, so a name with a space
	// ("C:\Users\John Smith: denied") is taken whole. Words after an unquoted path on the same line go with it (fail closed).
	reWinPath = regexp.MustCompile(`(^|[^A-Za-z0-9_])[A-Za-z]:[\\/][^:;\r\n"'<>|*?]*`)
	reUNC     = regexp.MustCompile(`\\\\[^\s\\/:*?"<>|]+(?:\\[^:;\r\n"'<>|*?]*)?`)
	reEnvPath = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_]*%(?:[\\/][^\s"'<>|*?]*)?`)
	// a Unix path: "~/x", "./x", "../x", or "/a/b" (two or more parts) after a byte that cannot be part of a word or a longer path.
	reUnixPath = regexp.MustCompile(`(^|[^A-Za-z0-9_.~/\\-])((?:~|\.{1,2})/[^\s"'<>|*?,;]+|/[^\s/"'<>|*?,;]+/[^\s"'<>|*?,;]*)`)
	// text that reads as a SQL statement, so the shape rules of querytext apply to it.
	reLooksSQL = regexp.MustCompile(`(?is)\bselect\b.+\bfrom\b|\binsert\s+into\b|\bupdate\s+\S+\s+set\b|\bdelete\s+from\b|\bexec(?:ute)?\s+\S|\bmerge\s+into\b|\bwhere\s+\S+\s*(?:=|<|>|like\b|in\b|between\b)|\bvalues\s*\(`)
)

// ScrubText removes secrets, tokens and keys, connection string parts, database and web URL detail, file paths and SQL literals
// from text that came from a host call, a driver or a script. It does not cut the size beyond maxScrubInput; ScrubError does. It
// never returns more than a constant factor of its input.
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
	s = reAuthHdr.ReplaceAllString(s, "${1}${2}"+Mask)
	s = reBearer.ReplaceAllString(s, "${1} "+Mask)
	s = reJWT.ReplaceAllString(s, Mask)
	s = reKeyShape.ReplaceAllString(s, Mask)
	s = reDBURL.ReplaceAllString(s, Mask)
	s = reFileURI.ReplaceAllString(s, Mask)
	s = reWebURL.ReplaceAllStringFunc(s, scrubWebURL)
	s = reConnKV.ReplaceAllString(s, "${1}="+Mask)
	s = reDupValue.ReplaceAllString(s, "${1}("+Mask+")")
	s = rePgKey.ReplaceAllString(s, "Key ("+Mask+")=("+Mask+")")
	s = reMongoDup.ReplaceAllString(s, "${1}{"+Mask+"}")
	s = rePgSyntax.ReplaceAllString(s, "${1}\""+Mask+"\"")
	s = rePgValue.ReplaceAllString(s, "${1}\""+Mask+"\"")
	s = maskQuoted(s)
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

// scrubUnixPath masks the path in a match of reUnixPath (the byte in front stays) and keeps the closing punctuation that is not part
// of a path ("[/home/bob/x.txt]", "/data/x.db:").
func scrubUnixPath(m string) string {
	sub := reUnixPath.FindStringSubmatch(m)
	if sub == nil {
		return Mask
	}
	path := sub[2]
	tail := len(path)
	for tail > 0 && strings.IndexByte(".:)]}", path[tail-1]) >= 0 {
		tail--
	}
	return sub[1] + Mask + path[tail:]
}

// maskQuoted replaces the inside of every single-quoted run with Mask, the way SQL writes a string (” is a quote inside). A quote
// opens a run only after a non-word byte (or after N, X, E, B in front of one: N'x'), so "don't" and "it's" are text. A quote
// followed by a word byte ("O'Brien") does not close it. A run that is not closed goes to the end of the text (fail closed). This is
// what hides the value in "converting the varchar value 'hunter2' to data type int", "near 'x'", "Duplicate entry 'a@b' for key".
func maskQuoted(s string) string {
	if strings.IndexByte(s, '\'') < 0 {
		return s
	}
	var b strings.Builder
	last, i := 0, 0
	for i < len(s) {
		j := strings.IndexByte(s[i:], '\'')
		if j < 0 {
			break
		}
		q := i + j
		if !opensLiteral(s, q) {
			i = q + 1
			continue
		}
		closed := -1
		for k := q + 1; k < len(s); {
			m := strings.IndexByte(s[k:], '\'')
			if m < 0 {
				break
			}
			p := k + m
			if p+1 < len(s) && (s[p+1] == '\'' || isWord(s[p+1])) {
				k = p + 2
				if s[p+1] != '\'' {
					k = p + 1
				}
				continue
			}
			closed = p
			break
		}
		if b.Cap() == 0 {
			b.Grow(len(s))
		}
		b.WriteString(s[last:q])
		b.WriteString("'" + Mask + "'")
		if closed < 0 {
			return b.String()
		}
		last = closed + 1
		i = last
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func opensLiteral(s string, q int) bool {
	if q == 0 || !isWord(s[q-1]) {
		return true
	}
	if strings.IndexByte("NnXxEeBb", s[q-1]) >= 0 && (q == 1 || !isWord(s[q-2])) {
		return true
	}
	return false
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
