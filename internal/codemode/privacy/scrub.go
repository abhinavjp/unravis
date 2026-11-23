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
	// credentials in a header or a token shape: the whole value of an Authorization or cookie header (also written as JSON
	// "Authorization":"Basic x" or as map[Authorization:[x]]), "Bearer x", a JWT, and the prefixes of the common key formats (AWS
	// access key id, GitHub, Slack, OpenAI-style, Google API key).
	reAuthHdr  = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|x-api-key|api-key|apikey|cookie|set-cookie)\b(\\?"?\s*[:=]\s*)(\\"[^\\\r\n]*\\"|"(?:[^"\\\r\n]|\\.)*"|\[[^\]\r\n]*\]|[^\r\n]*)`)
	reBearer   = regexp.MustCompile(`(?i)\b(bearer)\s+[^\s"',;]{4,}`)
	reJWT      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}(?:\.[A-Za-z0-9_-]*)?`)
	reKeyShape = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}|\bgh[pousr]_[A-Za-z0-9]{20,}|\bgithub_pat_[A-Za-z0-9_]{20,}|\bxox[abprs]-[A-Za-z0-9-]{10,}|\bsk-[A-Za-z0-9_-]{20,}|\bAIza[0-9A-Za-z_-]{35}`)
	// the start of a key=value part of a connection string that names a place or a login (the password forms are secretscrub's). The
	// longer names come first. The value is read by maskConnKV.
	reConnKey = regexp.MustCompile(`(?i)\b(data source|network address|initial catalog|workstation id|application name|instance name|attachdbfilename|accountendpoint|user name|user id|hostname|username|sslrootcert|sslcert|sslkey|passfile|unix_socket|dbname|database|address|endpoint|server|host|addr|dsn|uid|user|port|service)\s*=\s*`)
	// the next key of a space separated DSN ("user=alice dbname=payroll host=db"): an unquoted value stops before it.
	reNextKey = regexp.MustCompile(`\s[A-Za-z_][A-Za-z0-9_]*\s*=`)
	// a database, broker or storage URL is removed whole; any other scheme://host/... keeps scheme and host and loses login, path and
	// query (the path of an smb, s3, sftp or ldap URL holds names).
	reDBURL   = regexp.MustCompile(`(?i)\b(?:mongodb(?:\+srv)?|mssql|sqlserver|postgres(?:ql)?|mysql|redis|rediss|amqps?|jdbc:[a-z0-9:]+|odbc|oracle|cassandra|couchdb|cosmos)://[^\s"'<>]*`)
	reWebURL  = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]{1,15}://)([^\s"'<>/?#]*)([^\s"'<>]*)`)
	reFileURI = regexp.MustCompile(`(?i)\bfile:/{1,3}[^\s"'<>]*`)
	// where the driver errors print a value without single quotes: the double-quoted value after "invalid input ...:", "value", "duplicate
	// entry" and the double-quoted name after "for user", "role", "login" (Postgres, MySQL with ANSI quotes, SQL Server).
	reDQValue = regexp.MustCompile(`(?i)(\b(?:invalid input (?:value|syntax)[^:\r\n"]*:|value|duplicate entry|for user|role|login|database|schema)\s*)"[^"\r\n]*"`)
	// the text before the "(" of a detail that lists values; see maskParen.
	reRowDetail = regexp.MustCompile(`(?i)\bfailing row contains\s*\(`)
	reKeyDetail = regexp.MustCompile(`(?i)\bkey\s*\(`)
	reDupDetail = regexp.MustCompile(`(?i)\bduplicate key value is\s*\(`)
	reMongoDup  = regexp.MustCompile(`(?i)(dup key:?\s*)\{[^}]*\}`)
	// Windows drive paths and UNC paths: the path runs to a ":", a quote, a ";" or the end of the line, so a name with a space
	// ("C:\Users\John Smith: denied") is taken whole. Words after an unquoted path on the same line go with it (fail closed).
	reWinPath = regexp.MustCompile(`(^|[^A-Za-z0-9_])[A-Za-z]:[\\/][^:;\r\n"'<>|*?]*`)
	reUNC     = regexp.MustCompile(`\\\\[^\s\\/:*?"<>|]+(?:\\[^:;\r\n"'<>|*?]*)?`)
	reEnvPath = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_]*%(?:[\\/][^\s"'<>|*?]*)?`)
	// a Unix path: "~/x", "~alice/x", "./x", "../x", or "/a/b" (two or more parts) after a byte that cannot be part of a word or a
	// longer path.
	reUnixPath = regexp.MustCompile(`(^|[^A-Za-z0-9_.~/\\-])((?:~[A-Za-z0-9_.-]*|\.{1,2})/[^\s"'<>|*?,;]+|/[^\s/"'<>|*?,;]+/[^\s"'<>|*?,;]*)`)
	// text that reads as a SQL statement, so the shape rules of querytext apply to it.
	reLooksSQL = regexp.MustCompile(`(?is)\bselect\b.+\bfrom\b|\binsert\s+into\b|\bupdate\s+\S+\s+set\b|\bdelete\s+from\b|\bexec(?:ute)?\s+\S|\bmerge\s+into\b|\bwhere\s+\S+\s*(?:=|<|>|like\b|in\b|between\b)|\bvalues\s*\(`)
)

// ScrubText removes secrets, tokens and keys, connection string parts, database and URL detail, file paths, SQL literals and the
// values of driver error details from text that came from a host call, a driver or a script. The text is cut at maxScrubInput
// bytes (at a place that cannot split an address) before any rule runs. It never returns more than a constant factor of its input.
func ScrubText(s string) string {
	if s == "" {
		return s
	}
	s = capInput(strings.ToValidUTF8(s, "?"))
	// SQL first: a statement is reshaped (literals become ?), withheld when it may carry a credential.
	if reLooksSQL.MatchString(s) {
		s = querytext.Scrub(s, querytext.MaxMaxChars)
	}
	s = secretscrub.Mask(s, Mask)
	s = secretscrub.MaskURLUserinfo(s, Mask)
	s = reAuthHdr.ReplaceAllStringFunc(s, scrubAuthHeader)
	s = reBearer.ReplaceAllString(s, "${1} "+Mask)
	s = reJWT.ReplaceAllString(s, Mask)
	s = reKeyShape.ReplaceAllString(s, Mask)
	s = reDBURL.ReplaceAllString(s, Mask)
	s = reFileURI.ReplaceAllString(s, Mask)
	s = reWebURL.ReplaceAllStringFunc(s, scrubWebURL)
	s = maskConnKV(s)
	s = maskParen(s, reRowDetail, false, true)
	s = maskParen(s, reDupDetail, false, false)
	s = maskParen(s, reKeyDetail, true, false)
	s = reMongoDup.ReplaceAllString(s, "${1}{"+Mask+"}")
	s = reDQValue.ReplaceAllString(s, "${1}\""+Mask+"\"")
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

// scrubAuthHeader masks the value of one Authorization or cookie header, in the form it was written: a quoted JSON value keeps its
// quotes, a bracketed Go map value keeps its brackets.
func scrubAuthHeader(m string) string {
	sub := reAuthHdr.FindStringSubmatch(m)
	if sub == nil {
		return Mask
	}
	switch {
	case sub[3] == Mask || sub[3] == "["+Mask+"]" || sub[3] == `"`+Mask+`"` || sub[3] == `\"`+Mask+`\"`:
		return m // masked already (by secretscrub or an earlier pass)
	case strings.HasPrefix(sub[3], `\"`):
		return sub[1] + sub[2] + `\"` + Mask + `\"`
	case strings.HasPrefix(sub[3], `"`):
		return sub[1] + sub[2] + `"` + Mask + `"`
	case strings.HasPrefix(sub[3], "["):
		return sub[1] + sub[2] + "[" + Mask + "]"
	}
	return sub[1] + sub[2] + Mask
}

// maskConnKV masks the value after every connection string key of reConnKey. A quoted or braced value ends at its closing mark; any
// other ends at ";" or the end of the line, and, when the text is a space separated DSN, before the next "key=".
func maskConnKV(s string) string {
	locs := reConnKey.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		v := loc[1]
		if v < last {
			continue // inside a value already masked
		}
		end := lineEnd(s, v)
		switch {
		case v < len(s) && (s[v] == '"' || s[v] == '\''):
			if c := strings.IndexByte(s[v+1:end], s[v]); c >= 0 {
				end = v + 1 + c + 1
			}
		case v < len(s) && s[v] == '{':
			if c := strings.IndexByte(s[v+1:end], '}'); c >= 0 {
				end = v + 1 + c + 1
			}
		default:
			if c := strings.IndexByte(s[v:end], ';'); c >= 0 {
				end = v + c
			}
			if nk := reNextKey.FindStringIndex(s[v:end]); nk != nil {
				end = v + nk[0]
			}
		}
		b.WriteString(s[last:v])
		b.WriteString(Mask)
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func lineEnd(s string, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return i
		}
	}
	return len(s)
}

// maskParen masks the text inside the parentheses that follow every match of re (the match ends at the "("). The group ends at its
// matching ")" (nested pairs count) or at the end of the line; with toEOL it always ends at the end of the line (a row can hold
// ")"). With chain, a group that is followed by "=(" has the second group masked too ("Key (lower(email::text))=(alice@x)").
func maskParen(s string, re *regexp.Regexp, chain, toEOL bool) string {
	locs := re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		from := loc[1]
		if from < last {
			continue
		}
		b.WriteString(s[last:from])
		pos := from
		for round := 0; ; round++ {
			end := groupEnd(s, pos, toEOL)
			b.WriteString(Mask)
			pos = end
			if !chain || round > 0 || end >= len(s) || s[end] != ')' {
				break
			}
			// ")" then optional spaces, "=", optional spaces, "(": a second group
			k := end + 1
			for k < len(s) && s[k] == ' ' {
				k++
			}
			if k >= len(s) || s[k] != '=' {
				break
			}
			k++
			for k < len(s) && s[k] == ' ' {
				k++
			}
			if k >= len(s) || s[k] != '(' {
				break
			}
			b.WriteString(s[end : k+1])
			pos = k + 1
		}
		last = pos
	}
	b.WriteString(s[last:])
	return b.String()
}

// groupEnd returns the index of the ")" that closes the group that starts at from, or the end of the line.
func groupEnd(s string, from int, toEOL bool) int {
	eol := lineEnd(s, from)
	if toEOL {
		return eol
	}
	depth := 1
	for i := from; i < eol; i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return eol
}

// capInput cuts s to maxScrubInput bytes at a place that cannot split an address or a word: it backs up over the bytes that could
// belong to one, so "10.20.30.40" is never left as "10.20". A cut text ends in "...".
func capInput(s string) string {
	if len(s) <= maxScrubInput {
		return s
	}
	n := maxScrubInput
	for k := 0; k < 4096 && n > 0 && (isV6Char(s[n-1]) || isWord(s[n-1]) || s[n-1] == '%' || s[n-1] == '-'); k++ {
		n--
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
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
