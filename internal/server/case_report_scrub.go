package server

import "regexp"

// reportTypedNotes is printed in every report: notes are the author's words, with
// anything that looks like a secret taken out.
const reportTypedNotes = "Typed notes are printed as written, with secrets removed."

const reportRemoved = "[removed]"

// noteValue is the value after "=" or ":": a quoted string, or a run of
// characters up to a space, ";", ",", "&" or a quote.
const noteValue = `(?:"[^"]*"|'[^']*'|[^\s;,&"']+)`

// noteConnPair is one key=value pair of a connection string.
const noteConnPair = `(?:server|data source|address|addr|network address|initial catalog|database|user id|uid|user|pwd|password|integrated security|trusted_connection|host)\s*=\s*` + noteValue

// noteScrubbers run in order. Each match is replaced by "[removed]".
var noteScrubbers = []*regexp.Regexp{
	// scheme://anything, which includes mongodb://user:pw@host and https://host?token=x.
	regexp.MustCompile(`\S*://\S*`),
	// A connection string without a scheme: two or more pairs joined by ";".
	regexp.MustCompile(`(?i)` + noteConnPair + `(?:\s*;\s*` + noteConnPair + `)+(?:\s*;)?`),
	// One secret pair. No word boundary on purpose, so "my_password=x" is caught too.
	regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret|token|api[_ -]?key|account[_ -]?key|access[_ -]?key|shared[_ -]?access[_ -]?key)\s*=\s*` + noteValue),
	// "password: value" (a colon is only taken for the password words, so "token: expired" is left alone).
	regexp.MustCompile(`(?i)(?:password|passwd|pwd)\s*:\s*` + noteValue),
	// A bearer token.
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`),
}

// scrubTypedNote takes secrets out of a note a person typed. It is applied before
// the text is clipped, so a secret cannot be cut in half and slip past a pattern.
func scrubTypedNote(s string) string {
	for _, re := range noteScrubbers {
		s = re.ReplaceAllString(s, reportRemoved)
	}
	return s
}
