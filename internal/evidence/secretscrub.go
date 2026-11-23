package evidence

import (
	"regexp"

	"api-audit-explorer/internal/secretscrub"
)

// secretRemoved is what ScrubSecrets puts in place of a secret.
const secretRemoved = "[removed]"

// quotedOrBraced is a quoted or braced value. A quote inside a quoted value is
// doubled (as in an apostrophe written twice, the ADO way) or escaped with a
// backslash (JSON and Python repr), and "}}" is an escaped brace. The closing
// mark is optional: a value that never closes runs to the end of the text, so
// nothing after an unclosed quote or brace is kept.
const quotedOrBraced = `"(?:[^"\\]|\\(?s:.)|"")*"?|'(?:[^'\\]|\\(?s:.)|'')*'?|\{(?:[^}]|\}\})*\}?`

// quotedString is a quoted value that must close, with the same escapes.
const quotedString = `"(?:[^"\\]|\\(?s:.)|"")*"|'(?:[^'\\]|\\(?s:.)|'')*'`

// valueToSemicolon is the value of a login-password key. A password may hold
// spaces, so an unquoted value runs to the next ";" or the end of the line (ADO
// syntax), not to the next space. When nothing follows the key on its line, the
// value is the text after one line break (CRLF, LF or a lone CR) and any spaces
// ("Password:" then the password on its own line). Blank lines end the search.
const valueToSemicolon = `(?:` + quotedOrBraced + `|[^;\r\n]+|(?:(?:\r\n?|\n)[ \t]*(?:` + quotedOrBraced + `|[^;\r\n]+))?)`

// loginKeyNames names the keys whose value is a login password.
const loginKeyNames = `(?:password|passwd|pwd)`

// secretValue is the value after "=" or ":" of the other keys: a quoted string,
// or a run of characters up to a space, ";", ",", "&" or a quote.
// The escaped forms come first; the plain forms follow so a value that ends in a
// backslash (secret="abc\") or has an odd quote still goes whole.
const secretValue = `(?:` + quotedString + `|"[^"]*"|'[^']*'|[^\s;,&"']+)`

// secretConnPair is one key=value pair of a connection string.
const secretConnPair = `(?:(?:server|data source|address|addr|network address|initial catalog|database|user id|uid|user|integrated security|trusted_connection|host)\s*=\s*` + secretValue +
	`|` + loginKeyNames + `\s*=[ \t]*` + valueToSemicolon + `)`

// accessKeyNames names keys that hold a key or token and are never ordinary words.
const accessKeyNames = `(?:api[_ -]?key|account[_ -]?key|access[_ -]?key|shared[_ -]?access[_ -]?key)`

// The XML patterns keep the tags and attribute names and replace only the value.
const (
	xmlKeyName               = `[\w.:-]*(?:password|passwd|pwd)`
	xmlElementPattern        = `(?is)(<` + xmlKeyName + `(?:\s[^<>]*)?>)(?:\s*<!\[CDATA\[.*?\]\]>\s*|[^<]*)(</` + xmlKeyName + `\s*>)`
	xmlKeyAttr               = `\b(?:key|name)\s*=\s*(?:"` + xmlKeyName + `"|'` + xmlKeyName + `')`
	xmlKeyedElementPattern   = `(?is)(<[\w:.-]+\s[^<>]*?` + xmlKeyAttr + `(?:[^<>]*[^<>/])?>\s*(?:<value>\s*)?)(?:\s*<!\[CDATA\[.*?\]\]>\s*|[^<]*)`
	xmlAttrKeyFirstPattern   = `(?i)(<[\w:.-]+\s[^<>]*?` + xmlKeyAttr + `[^<>]*?\bvalue\s*=\s*)(?:"[^"]*"|'[^']*')`
	xmlAttrValueFirstPattern = `(?i)(<[\w:.-]+\s[^<>]*?\bvalue\s*=\s*)(?:"[^"]*"|'[^']*')([^<>]*?` + xmlKeyAttr + `)`
)

// xmlScrubbers run first: XML such as <password>x y</password> (the tags stay)
// and web.config pairs <add key="Password" value="x y"/>, with the value before
// or after the key (the attribute names stay, the value gets double quotes).
var xmlScrubbers = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(xmlElementPattern), "${1}" + secretRemoved + "${2}"},
	{regexp.MustCompile(xmlKeyedElementPattern), "${1}" + secretRemoved},
	{regexp.MustCompile(xmlAttrKeyFirstPattern), `${1}"` + secretRemoved + `"`},
	{regexp.MustCompile(xmlAttrValueFirstPattern), `${1}"` + secretRemoved + `"${2}`},
}

// secretScrubbers run in order. Each match is replaced by "[removed]".
var secretScrubbers = []*regexp.Regexp{
	// A connection string without a scheme: two or more pairs joined by ";". It runs
	// before the URL rule so a JDBC string (jdbc:...://host;password=a b;) goes whole.
	regexp.MustCompile(`(?i)` + secretConnPair + `(?:\s*;\s*` + secretConnPair + `)+(?:\s*;)?`),
	// A password key: the value is not cut at a space (see valueToSemicolon). It also
	// runs before the URL rule, for the same reason. No word boundary on purpose, so
	// "my_password=x" is caught too. The JSON form ("password":"x") is caught as well.
	regexp.MustCompile(`(?i)` + loginKeyNames + `["']?\s*[=:][ \t]*` + valueToSemicolon),
	// db_pass=x: "pass" counts only after a separator and only with "=", so
	// "boarding-pass: gate 4" and "bypass=1" are left alone.
	regexp.MustCompile(`(?i)[_.-]pass["']?[ \t]*=[ \t]*` + valueToSemicolon),
	// {"db_pass": "x"} and {'pass': 'x'}: the quoted JSON or repr form of "pass".
	regexp.MustCompile(`(?i)(?:[_.-]pass|["']pass)["']\s*:[ \t]*` + valueToSemicolon),
	// scheme://anything, which includes mongodb://user:pw@host and https://host?token=x.
	regexp.MustCompile(`\S*://\S*`),
	// One key pair, plain or JSON form (`"api_key":"x"`).
	regexp.MustCompile(`(?i)` + accessKeyNames + `["']?\s*[=:]\s*` + secretValue),
	// "token" and "secret" are also ordinary words: "=" always counts, ":" only in
	// JSON form (a closing quote after the key) or before a quoted value, so
	// "token: expired" is left alone.
	regexp.MustCompile(`(?i)(?:token|secret)(?:["']\s*[=:]|\s*=)\s*` + secretValue),
	regexp.MustCompile(`(?i)(?:token|secret)\s*:\s*(?:` + quotedString + `|"[^"]*"|'[^']*')`),
	// A bearer token.
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`),
	// An Authorization Basic credential.
	regexp.MustCompile(`(?i)\bbasic\s+[a-z0-9+/=]{8,}`),
	// user:pass@host without a scheme. An email has no colon before the "@".
	regexp.MustCompile(`\S+:\S+@\S+`),
}

// ScrubSecrets replaces connection strings, URLs, secret key/value pairs
// (password=, token=, api key and the like, plain or JSON form) and bearer
// tokens with "[removed]". It is the one place both the case report and the
// findings export take secrets out of text a person typed.
func ScrubSecrets(s string) string {
	// The shared pass first: it reads the forms the patterns below do not (backslash-escaped quotes in JSON, prefixed keys such as
	// DB_PASSWORD, XML elements and attributes). The patterns then run over what is left.
	s = secretscrub.MaskURLUserinfo(s, secretRemoved)
	s = secretscrub.Mask(s, secretRemoved)
	for _, r := range xmlScrubbers {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	for _, re := range secretScrubbers {
		s = re.ReplaceAllString(s, secretRemoved)
	}
	return s
}
