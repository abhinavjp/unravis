package evidence

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
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
	text = numberWord.ReplaceAllString(text, "<"+strconv.Itoa(ThinCell)+" $2")
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

// numberWord matches a spelled-out small count in front of a counted noun
// ("three rows", "One request"), which maskNumbers masks like the digits.
var numberWord = regexp.MustCompile(`(?i)\b(zero|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen)\s+(rows?|requests?|events?|users?|records?|errors?|calls?|queries|lines?|entries|items?|files?|hits?|failures?|sessions?|rejected|imports?|lookups?)\b`)

// isBareInteger reports whether text[i:j], a run of digits, stands alone. A run
// glued to a short plain word ("3rows", "5xx"), part of a list ("3,4") or of a
// ratio ("3/40") is bare too: those read as counts. A run inside a decimal, a
// grouped number ("1,200"), a time, a date, an id or a percentage is not.
func isBareInteger(text string, i, j int) bool {
	if i > 0 {
		p := text[i-1]
		if isWordByte(p) {
			return false
		}
		if i > 1 && isDigit(text[i-2]) {
			switch p {
			case ',':
				if groupedComma(text, i-1) {
					return false
				}
			case '/':
				if !isRatio(text, i-1) {
					return false
				}
			case '-':
				if !isRange(text, i-1) {
					return false
				}
			case '.', ':':
				return false
			}
		}
	}
	if j < len(text) {
		n := text[j]
		switch {
		case n == '%':
			return false
		case isWordByte(n):
			return gluedWord(text, j)
		case j+1 < len(text) && isDigit(text[j+1]):
			switch n {
			case ',':
				if groupedComma(text, j) {
					return false
				}
			case '/':
				if !isRatio(text, j) {
					return false
				}
			case '-':
				if !isRange(text, j) {
					return false
				}
			case '.', ':':
				return false
			}
		}
	}
	return true
}

// isRange reports a hyphen at text[k] between two short integers that stand
// alone ("5-7 rows"): a range of counts, which reads like the numbers it joins.
// A date ("2026-10-24"), a longer run, or a hyphen chain of three or more
// numbers is not a range.
func isRange(text string, k int) bool {
	a, e := k, k+1
	for a > 0 && isDigit(text[a-1]) {
		a--
	}
	for e < len(text) && isDigit(text[e]) {
		e++
	}
	if k-a < 1 || k-a > 3 || e-(k+1) < 1 || e-(k+1) > 3 {
		return false
	}
	if a > 0 && isWordByte(text[a-1]) {
		return false
	}
	if a > 1 && strings.IndexByte("-.:/", text[a-1]) >= 0 && isDigit(text[a-2]) {
		return false
	}
	if e < len(text) {
		n := text[e]
		switch {
		case n == '%', isWordByte(n) && !gluedWord(text, e):
			return false
		case strings.IndexByte("-.:/", n) >= 0 && e+1 < len(text) && isDigit(text[e+1]):
			return false
		}
	}
	return true
}

// groupedComma reports a thousands separator at text[k]: a digit before it and
// exactly three digits after it.
func groupedComma(text string, k int) bool {
	if k == 0 || k+1 >= len(text) || !isDigit(text[k-1]) {
		return false
	}
	n := 0
	for m := k + 1; m < len(text) && isDigit(text[m]); m++ {
		n++
	}
	return n == 3
}

// isRatio reports a slash at text[k] between two numbers that cannot be a month
// and a day ("3/40"); "10/24" and "10/24/2026" are dates and are left alone.
func isRatio(text string, k int) bool {
	a, e := k, k+1
	for a > 0 && isDigit(text[a-1]) {
		a--
	}
	for e < len(text) && isDigit(text[e]) {
		e++
	}
	left, errL := strconv.Atoi(text[a:k])
	right, errR := strconv.Atoi(text[k+1 : e])
	if errL != nil || errR != nil || (e < len(text) && text[e] == '/') || (a > 0 && text[a-1] == '/') {
		return false
	}
	return right > 31 || left > 12
}

// gluedWord reports that the letters starting at text[j] form a short plain
// word ending at a non-word byte: a unit or noun glued to a number ("3rows").
// A letter-and-digit tail ("3fa9c1d2", a pseudonym) is not one.
func gluedWord(text string, j int) bool {
	k := j
	for k < len(text) && ((text[k] >= 'a' && text[k] <= 'z') || (text[k] >= 'A' && text[k] <= 'Z')) {
		k++
	}
	if k == j || k-j > 12 {
		return false
	}
	return k == len(text) || !isWordByte(text[k])
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isWordByte(c byte) bool {
	return isDigit(c) || c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// removedText is what scrubText puts in place of an identifier-like token.
const removedText = "[removed]"

// Free text that still looks risky after scrubbing is not exported at all.
const (
	ledgerWithheld = "[ledger text withheld]"
	noteWithheld   = "[note withheld]"
)

// confusables folds the Cyrillic and Greek letters that look like Latin ones,
// so "PRОVEN" with a Cyrillic О is seen as "PROVEN".
var confusables = strings.NewReplacer(
	// Cyrillic lower case
	"а", "a", "в", "b", "е", "e", "ѕ", "s", "і", "i", "ј", "j", "к", "k", "о", "o", "р", "p",
	"с", "c", "у", "y", "х", "x", "һ", "h", "ԁ", "d", "ԛ", "q", "ԝ", "w", "ӏ", "l", "ѵ", "v",
	"ё", "e", "ї", "i", "ү", "y", "п", "n", "т", "t", "м", "m", "г", "r",
	// Cyrillic upper case
	"А", "A", "В", "B", "Е", "E", "Ѕ", "S", "І", "I", "Ј", "J", "К", "K", "М", "M", "Н", "H",
	"О", "O", "Р", "P", "С", "C", "Т", "T", "Х", "X", "У", "Y", "Ү", "Y", "Ԛ", "Q", "Ԝ", "W",
	"Ӏ", "I", "Ѵ", "V", "Ё", "E", "Ї", "I",
	// Greek
	"Α", "A", "Β", "B", "Ε", "E", "Ζ", "Z", "Η", "H", "Ι", "I", "Κ", "K", "Μ", "M", "Ν", "N",
	"Ο", "O", "Ρ", "P", "Τ", "T", "Υ", "Y", "Χ", "X", "α", "a", "ε", "e", "ι", "i", "κ", "k",
	"ν", "v", "ο", "o", "ρ", "p", "τ", "t", "υ", "u", "χ", "x", "ϲ", "c", "ϳ", "j",
	// Latin look-alikes NFKC leaves alone
	"ı", "i", "ȷ", "j", "ɑ", "a", "ɡ", "g", "ꞁ", "l",
	// Latin small capitals, which NFKC leaves alone
	"ᴀ", "a", "ʙ", "b", "ᴄ", "c", "ᴅ", "d", "ᴇ", "e", "ꜰ", "f", "ɢ", "g",
	"ʜ", "h", "ɪ", "i", "ᴊ", "j", "ᴋ", "k", "ʟ", "l", "ᴍ", "m", "ɴ", "n",
	"ᴏ", "o", "ᴘ", "p", "ʀ", "r", "ꜱ", "s", "ᴛ", "t", "ᴜ", "u", "ᴠ", "v",
	"ᴡ", "w", "ʏ", "y", "ᴢ", "z",
)

// isIgnorable reports runes that render as nothing: format characters (zero
// width space and joiners, soft hyphen, bidi controls), variation selectors,
// the combining grapheme joiner and the Hangul and Mongolian fillers.
func isIgnorable(r rune) bool {
	switch {
	case unicode.Is(unicode.Cf, r):
		return true
	case r == 0x034F, r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0:
		return true
	case r >= 0x180B && r <= 0x180F, r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF:
		return true
	}
	return false
}

// normalizeText is what every scrub and every "proven" check sees: NFKC (so
// full-width and compatibility letters become plain ones), with invisible
// characters removed and Cyrillic and Greek look-alikes folded to Latin. The
// normalised text is also what is exported, so what a reader sees is what was
// checked.
func normalizeText(text string) string {
	text = strings.Map(func(r rune) rune {
		if isIgnorable(r) {
			return -1
		}
		return r
	}, norm.NFKC.String(text))
	text = confusables.Replace(text)
	// Folding can create a new composable sequence; one more NFKC pass settles it.
	return norm.NFKC.String(text)
}

// ipv4 matches a dotted quad anywhere, even glued to a word ("ip_10.1.2.3").
// Over-removal of a four-part version number is accepted.
var ipv4 = regexp.MustCompile(`\d{1,3}(?:\.\d{1,3}){3}`)

// ipv6Candidate matches runs of hex digits, dots and colons with at least two
// colons; ipv6Token confirms a candidate is an address, so clock times
// ("10:30:00") are kept.
var ipv6Candidate = regexp.MustCompile(`[0-9A-Fa-f.:]*:[0-9A-Fa-f.:]*:[0-9A-Fa-f.:]*(?:%[0-9A-Za-z]+)?`)

func ipv6Token(m string) bool {
	if i := strings.IndexByte(m, '%'); i >= 0 {
		m = m[:i]
	}
	// The candidate can start inside a word ("add:fe80::1"), so every start
	// after a colon is tried too, with and without trailing dots and colons.
	starts := []int{0}
	for i := 0; i < len(m); i++ {
		if m[i] == ':' {
			starts = append(starts, i, i+1)
		}
	}
	for _, s := range starts {
		for _, c := range []string{m[s:], strings.TrimRight(m[s:], ".:")} {
			if strings.Count(c, ":") >= 2 && net.ParseIP(c) != nil {
				return true
			}
		}
	}
	return false
}

var scrubPatterns = []*regexp.Regexp{
	// URLs and connection strings first: they can contain an email, an IP or a host.
	regexp.MustCompile(`\S*://\S*`),
	regexp.MustCompile(`(?i)\bwww\.\S+`),
	// Any token with an "@": emails with or without a TLD, non-ASCII domains, handles.
	regexp.MustCompile(`\S*@\S*`),
	// Windows drive and UNC paths.
	regexp.MustCompile(`(?i)\b[a-z]:[\\/]\S*`),
	regexp.MustCompile(`\\\\\S+`),
	// Scheme-less URLs: host.tld/path, with an optional port.
	regexp.MustCompile(`(?i)\b[a-z0-9-]+(?:\.[a-z0-9-]+)+(?::\d+)?/\S*`),
	// Bare host names on a common top-level domain.
	regexp.MustCompile(`(?i)\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:com|net|org|io|dev|app|ai|co|cloud|local|internal|corp|lan|intranet|uk|us|de|in|eu)\b`),
	// ObjectId-like: 24 or more hex characters, even when glued to a word.
	regexp.MustCompile(`[0-9a-fA-F]{24,}`),
}

// absPath matches a bare absolute path ("/a/b", "/api/orders/98765") that
// starts the text or follows a space, quote, bracket or separator.
var absPath = regexp.MustCompile(`(^|[\s"'(\[{=,;])/[\w.~%+\-]\S*`)

// provenWord matches "proven" in any form ("Proven cause", "unproven",
// "proven_cause"); isProvenance tells the harmless "provenance" apart.
var provenWord = regexp.MustCompile(`(?i)(?:un)?proven[a-z]*(?:[\s_-]+cause)?`)

func isProvenance(m string) bool { return strings.HasPrefix(strings.ToLower(m), "provenanc") }

// claimNorm is the text every claim check (cause words, "proven", "strong
// correlation") looks at: normalizeText, then accents and other combining
// marks removed, lower case, and every run of anything but a letter or a digit
// (spaces, "-", "_", ".", ":", punctuation) turned into one space. So
// "Blocked_By", "due-to" and "próven" read as plain words.
func claimNorm(text string) string {
	t := norm.NFD.String(normalizeText(text))
	t = strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Mn, r):
			return -1
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return unicode.ToLower(r)
		}
		return ' '
	}, t)
	return strings.Join(strings.Fields(norm.NFKC.String(t)), " ")
}

// strongCorrelation matches "strong" and "correlat..." close together in
// either order, on claimNorm text with its spaces removed ("strongly
// correlated", "correlation, a strong one", "Strong_Correlation").
var strongCorrelation = regexp.MustCompile(`strong.{0,60}correlat|correlat.{0,60}strong`)

// containsStrong reports "strong correlation" in any spelling, after claimNorm.
func containsStrong(text string) bool {
	return strongCorrelation.MatchString(strings.ReplaceAll(claimNorm(text), " ", ""))
}

// containsProven reports whether text carries the word "proven" in any form,
// after claimNorm, so look-alike letters, accents and invisible characters do
// not hide it.
func containsProven(text string) bool {
	for _, m := range provenWord.FindAllString(claimNorm(text), -1) {
		if !isProvenance(m) {
			return true
		}
	}
	return false
}

// scrubText normalises text, then replaces URLs (with or without a scheme),
// connection strings, email-like tokens, IPv4 and IPv6 addresses, host names,
// absolute paths, ObjectId-like hex tokens and the word "proven" with
// "[removed]".
func scrubText(text string) string {
	text = normalizeText(text)
	for _, re := range scrubPatterns {
		text = re.ReplaceAllString(text, removedText)
	}
	text = ipv6Candidate.ReplaceAllStringFunc(text, func(m string) string {
		if ipv6Token(m) {
			return removedText
		}
		return m
	})
	text = ipv4.ReplaceAllString(text, removedText)
	text = absPath.ReplaceAllString(text, "${1}"+removedText)
	// The export never carries the word "proven", in any form.
	return provenWord.ReplaceAllStringFunc(text, func(m string) string {
		if isProvenance(m) {
			return m
		}
		return removedText
	})
}

// keyedNumber matches any number written after an identifier key ("user 4242",
// "UserId: 4242", `"user_id": 98765`, "uid=98765", "account 98,765"). Every such
// number goes, not only ids the report holds.
// A number glued to a word ("user-3fa9c1d2", a pseudonym) is not a keyed number.
var keyedNumber = regexp.MustCompile(`(?i)((?:user|uid|account|acct|customer|cust|client|member|tenant|owner|person|login)(?:[\s_-]*(?:id|no|num|number))?["']?[\s:#=\-]*["']?)\d(?:,?\d)*\b`)

// digitRun matches maximal runs of digits.
var digitRun = regexp.MustCompile(`\d+`)

// removeUserIDs removes numbers written as identifier references, and any digit
// run equal to a user id the report holds. Over-removal of an unrelated number
// that happens to equal an id is accepted.
func removeUserIDs(text string, ids map[int]bool) string {
	text = keyedNumber.ReplaceAllString(text, "${1}"+removedText)
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

// longDigits is a run of five or more digits: an unkeyed id, a phone number or
// an account number may hide in it.
var longDigits = regexp.MustCompile(`\d{5,}`)

// residualRisk reports scrubbed text that still looks like it carries an
// identifier.
func residualRisk(text string) bool {
	return longDigits.MatchString(text) || strings.Contains(text, "@") || strings.Contains(text, "://")
}

// cleanFreeText is the full treatment for text written by a person or carried
// verbatim from the engine: normalise and scrub identifiers and user ids first
// (so the digits of an IP or an id are not half-masked), then mask small
// integers.
//
// DECISION: residual risk wins over readability. If the scrubbed text still
// holds a long digit run, an "@" or "://", the whole text is replaced with
// withheld rather than exported with a possible identifier in it.
func cleanFreeText(text string, ids map[int]bool, withheld string) string {
	out := maskNumbers(removeUserIDs(scrubText(text), ids))
	if residualRisk(out) || containsProven(out) {
		return withheld
	}
	return out
}
