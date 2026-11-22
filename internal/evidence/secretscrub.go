package evidence

import "regexp"

// secretRemoved is what ScrubSecrets puts in place of a secret.
const secretRemoved = "[removed]"

// quotedOrBraced is a quoted or braced value. A quote inside a quoted value is
// doubled (as in an apostrophe written twice), and "}}" is an escaped brace, as ADO writes
// them. The closing mark is optional: a value that never closes runs to the end
// of the text, so nothing after an unclosed quote or brace is kept.
const quotedOrBraced = `"(?:[^"]|"")*"?|'(?:[^']|'')*'?|\{(?:[^}]|\}\})*\}?`

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
const secretValue = `(?:"[^"]*"|'[^']*'|[^\s;,&"']+)`

// secretConnPair is one key=value pair of a connection string.
const secretConnPair = `(?:(?:server|data source|address|addr|network address|initial catalog|database|user id|uid|user|integrated security|trusted_connection|host)\s*=\s*` + secretValue +
	`|` + loginKeyNames + `\s*=[ \t]*` + valueToSemicolon + `)`

// accessKeyNames names keys that hold a key or token and are never ordinary words.
const accessKeyNames = `(?:api[_ -]?key|account[_ -]?key|access[_ -]?key|shared[_ -]?access[_ -]?key)`

// secretScrubbers run in order. Each match is replaced by "[removed]".
var secretScrubbers = []*regexp.Regexp{
	// A connection string without a scheme: two or more pairs joined by ";". It runs
	// before the URL rule so a JDBC string (jdbc:...://host;password=a b;) goes whole.
	regexp.MustCompile(`(?i)` + secretConnPair + `(?:\s*;\s*` + secretConnPair + `)+(?:\s*;)?`),
	// A password key: the value is not cut at a space (see valueToSemicolon). It also
	// runs before the URL rule, for the same reason. No word boundary on purpose, so
	// "my_password=x" is caught too. The JSON form ("password":"x") is caught as well.
	regexp.MustCompile(`(?i)` + loginKeyNames + `["']?\s*[=:][ \t]*` + valueToSemicolon),
	// scheme://anything, which includes mongodb://user:pw@host and https://host?token=x.
	regexp.MustCompile(`\S*://\S*`),
	// One key pair, plain or JSON form (`"api_key":"x"`).
	regexp.MustCompile(`(?i)` + accessKeyNames + `["']?\s*[=:]\s*` + secretValue),
	// "token" and "secret" are also ordinary words: "=" always counts, ":" only in
	// JSON form (a closing quote after the key) or before a quoted value, so
	// "token: expired" is left alone.
	regexp.MustCompile(`(?i)(?:token|secret)(?:["']\s*[=:]|\s*=)\s*` + secretValue),
	regexp.MustCompile(`(?i)(?:token|secret)\s*:\s*(?:"[^"]*"|'[^']*')`),
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
	for _, re := range secretScrubbers {
		s = re.ReplaceAllString(s, secretRemoved)
	}
	return s
}
