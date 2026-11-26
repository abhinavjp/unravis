package pull

import (
	"regexp"

	"api-audit-explorer/internal/evidence"
)

// Extra rules per kind, on top of evidence.ScrubSecrets. Redis logs and slowlogs can carry the password of an AUTH command or a config line.
var kindScrubbers = map[string][]*regexp.Regexp{
	"redis": {
		regexp.MustCompile(`(?i)\b(?:requirepass|masterauth|masteruser)\s+\S+`),
		regexp.MustCompile(`(?i)"auth"(?:\s+"[^"]*"){1,2}`),
		regexp.MustCompile(`(?i)\bAUTH\s+\S+(?:\s+\S+)?`),
	},
}

// scrubLine removes secrets from one line. ok is false when the scrubber itself failed: the caller must drop the line (fail closed).
func scrubLine(kind, line string) (out string, changed bool, ok bool) {
	defer func() {
		if recover() != nil {
			out, changed, ok = "", false, false
		}
	}()
	out = evidence.ScrubSecrets(line)
	for _, re := range kindScrubbers[kind] {
		out = re.ReplaceAllString(out, "[removed]")
	}
	return out, out != line, true
}
