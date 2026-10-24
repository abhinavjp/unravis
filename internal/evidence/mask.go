package evidence

import (
	"regexp"
	"strconv"
	"strings"
)

// MaskCount applies the thin-cell rule: any count below ThinCell prints as
// "<20" and carries no value; a count at or above it prints in full.
func MaskCount(n int) Count {
	if n < ThinCell {
		return Count{Masked: true, Text: "<" + strconv.Itoa(ThinCell)}
	}
	v := n
	return Count{Value: &v, Text: strconv.Itoa(n)}
}

// maskCount64 is MaskCount for the int64 row counts in PatternQuality.
func maskCount64(n int64) Count {
	if n >= int64(ThinCell) {
		// Clamp so a huge count cannot overflow int on a 32-bit build; any value
		// this large is unmasked anyway.
		const maxInt = int64(^uint(0) >> 1)
		if n > maxInt {
			n = maxInt
		}
	}
	return MaskCount(int(n))
}

// maskNumbers masks free text the export cannot rebuild from structured data
// (ledger text and engine notes).
//
// DECISION: free text cannot tell a count of rows or requests from a duration
// or a threshold, so every bare integer below ThinCell is replaced with "<20".
// Over-masking a duration ("5 s" -> "<20 s") is accepted; under-masking a count
// is not. Headlines and known gaps are NOT passed through this function: they
// are rebuilt from structured data, so counts are masked with MaskCount and
// durations and thresholds stay exact.
//
// A bare integer is a run of digits that is not part of a word ("B12",
// "user-3fa9"), a decimal or grouped number ("1.5", "1,200"), a time or date
// ("10:30", "2026-10-24", "10/24") or a percentage ("5%"). Those are left as is.
// Integers at or above ThinCell are left as is, so the function is idempotent.
func maskNumbers(text string) string {
	if text == "" {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	i := 0
	for i < len(text) {
		c := text[i]
		if !isDigit(c) {
			b.WriteByte(c)
			i++
			continue
		}
		j := i
		for j < len(text) && isDigit(text[j]) {
			j++
		}
		run := text[i:j]
		if isBareInteger(text, i, j) {
			if v, err := strconv.Atoi(run); err == nil && v < ThinCell {
				b.WriteString("<" + strconv.Itoa(ThinCell))
				i = j
				continue
			}
		}
		b.WriteString(run)
		i = j
	}
	return b.String()
}

// isBareInteger reports whether text[i:j], a run of digits, stands alone.
func isBareInteger(text string, i, j int) bool {
	if i > 0 {
		p := text[i-1]
		if isWordByte(p) {
			return false
		}
		if isNumericJoiner(p) && i > 1 && isDigit(text[i-2]) {
			return false
		}
	}
	if j < len(text) {
		n := text[j]
		if isWordByte(n) || n == '%' {
			return false
		}
		if isNumericJoiner(n) && j+1 < len(text) && isDigit(text[j+1]) {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWordByte(c byte) bool {
	return isDigit(c) || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isNumericJoiner(c byte) bool {
	return c == '.' || c == ',' || c == ':' || c == '-' || c == '/'
}

// removedText is what scrubText puts in place of an identifier-like token.
const removedText = "[removed]"

var scrubPatterns = []*regexp.Regexp{
	// URLs and connection strings first: they can contain an email, an IP or a host.
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://\S+`),
	regexp.MustCompile(`(?i)\bwww\.\S+`),
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	// ObjectId-like: 24 or more hex characters, even when glued to a word.
	regexp.MustCompile(`[0-9a-fA-F]{24,}`),
}

// provenWord matches "proven" in any form ("Proven cause", "unproven",
// "proven_cause"); isProvenance tells the harmless "provenance" apart.
var provenWord = regexp.MustCompile(`(?i)(?:un)?proven[a-z]*(?:[\s_-]+cause)?`)

func isProvenance(m string) bool { return strings.HasPrefix(strings.ToLower(m), "provenanc") }

// containsProven reports whether text carries the word "proven" in any form.
func containsProven(text string) bool {
	for _, m := range provenWord.FindAllString(text, -1) {
		if !isProvenance(m) {
			return true
		}
	}
	return false
}

// scrubText replaces URLs, connection strings, email addresses, IPv4
// addresses, ObjectId-like hex tokens and the word "proven" with "[removed]".
func scrubText(text string) string {
	for _, re := range scrubPatterns {
		text = re.ReplaceAllString(text, removedText)
	}
	// The export never carries the word "proven", in any form.
	return provenWord.ReplaceAllStringFunc(text, func(m string) string {
		if isProvenance(m) {
			return m
		}
		return removedText
	})
}

// userRef matches a number written as a user reference ("user 4242",
// "UserId: 4242", "user_id=4242").
var userRef = regexp.MustCompile(`(?i)\b(user(?:[\s_-]*id)?)([\s:#=]*)\d+`)

// digitRun matches maximal runs of digits.
var digitRun = regexp.MustCompile(`\d+`)

// removeUserIDs removes numbers written as user references, and any digit run
// equal to a user id the report holds. Over-removal of an unrelated number that
// happens to equal an id is accepted.
func removeUserIDs(text string, ids map[int]bool) string {
	text = userRef.ReplaceAllString(text, "${1}${2}"+removedText)
	if len(ids) == 0 {
		return text
	}
	return digitRun.ReplaceAllStringFunc(text, func(m string) string {
		if v, err := strconv.Atoi(m); err == nil && ids[v] {
			return removedText
		}
		return m
	})
}

// cleanFreeText is the full treatment for text written by a person or carried
// verbatim from the engine: scrub identifiers and user ids first (so the digits
// of an IP or an id are not half-masked), then mask small integers.
func cleanFreeText(text string, ids map[int]bool) string {
	return maskNumbers(removeUserIDs(scrubText(text), ids))
}
