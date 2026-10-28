package evidence

import "regexp"

// secretRemoved is what ScrubSecrets puts in place of a secret.
const secretRemoved = "[removed]"

// secretValue is the value after "=" or ":": a quoted string, or a run of
// characters up to a space, ";", ",", "&" or a quote.
const secretValue = `(?:"[^"]*"|'[^']*'|[^\s;,&"']+)`

// secretConnPair is one key=value pair of a connection string.
const secretConnPair = `(?:server|data source|address|addr|network address|initial catalog|database|user id|uid|user|pwd|password|integrated security|trusted_connection|host)\s*=\s*` + secretValue

// strongSecretKey names secrets that are never ordinary words.
const strongSecretKey = `(?:password|passwd|pwd|api[_ -]?key|account[_ -]?key|access[_ -]?key|shared[_ -]?access[_ -]?key)`

// secretScrubbers run in order. Each match is replaced by "[removed]".
var secretScrubbers = []*regexp.Regexp{
	// scheme://anything, which includes mongodb://user:pw@host and https://host?token=x.
	regexp.MustCompile(`\S*://\S*`),
	// A connection string without a scheme: two or more pairs joined by ";".
	regexp.MustCompile(`(?i)` + secretConnPair + `(?:\s*;\s*` + secretConnPair + `)+(?:\s*;)?`),
	// One secret pair, plain or JSON form (`"password":"x"`). No word boundary on
	// purpose, so "my_password=x" is caught too.
	regexp.MustCompile(`(?i)` + strongSecretKey + `["']?\s*[=:]\s*` + secretValue),
	// "token" and "secret" are also ordinary words: "=" always counts, ":" only in
	// JSON form (a closing quote after the key) or before a quoted value, so
	// "token: expired" is left alone.
	regexp.MustCompile(`(?i)(?:token|secret)(?:["']\s*[=:]|\s*=)\s*` + secretValue),
	regexp.MustCompile(`(?i)(?:token|secret)\s*:\s*(?:"[^"]*"|'[^']*')`),
	// A bearer token.
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`),
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
