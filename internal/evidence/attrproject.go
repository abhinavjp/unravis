package evidence

import "strings"

// Event attribute keys that never leave the machine on the default path.
const (
	AttrClientIP     = "client-ip"
	AttrReferer      = "referer"
	AttrCookiePrefix = "cookie:"
)

// IsSensitiveAttr reports whether an event attribute key is withheld from
// outward views unless the caller opts in: client-ip, referer, and any key
// that starts with "cookie:". The match is exact and case-sensitive.
func IsSensitiveAttr(key string) bool {
	return key == AttrClientIP || key == AttrReferer || strings.HasPrefix(key, AttrCookiePrefix)
}

// ProjectEventAttrs is the one projection every outward event view must use.
// It always returns a new, non-nil map (never the input, so a caller cannot
// change stored attributes) holding every pair when includeSensitive is true
// and every pair with a non-sensitive key when it is false. Values are never
// altered and nothing is added.
func ProjectEventAttrs(attrs map[string]string, includeSensitive bool) map[string]string {
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if !includeSensitive && IsSensitiveAttr(k) {
			continue
		}
		out[k] = v
	}
	return out
}
