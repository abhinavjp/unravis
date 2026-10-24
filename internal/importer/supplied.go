package importer

import (
	"errors"
	"regexp"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// Owner-supplied settings (plan section 4.1). An importer lists the settings it
// reads through the optional SuppliedKeyer interface. The service accepts a
// setting only for a key the chosen importer lists, and only a value that fits
// the key's spec: one of a fixed set of words, a bounded number, or (for the
// few keys that need it, such as a log4net pattern) short text that the
// importer's own check accepts. The checked values reach Options.Supplied and the
// import manifest. They are never written to a log.

// SuppliedKind says how a SuppliedSpec checks a value.
type SuppliedKind int

const (
	SuppliedEnum   SuppliedKind = iota + 1 // one of Values, compared exactly
	SuppliedNumber                         // a plain decimal number within Min..Max
	SuppliedText                           // short text that Check accepts
)

// Bounds on what the service stores for supplied settings.
const (
	// MaxSuppliedKeys is the most settings one import may carry.
	MaxSuppliedKeys = 16
	// MaxSuppliedValue is the most bytes one value may hold, whatever the spec says.
	MaxSuppliedValue = 512
	maxSuppliedKey   = 40
)

// SuppliedSpec describes the values one supplied key accepts.
type SuppliedSpec struct {
	Kind SuppliedKind

	// Values lists the words a SuppliedEnum key accepts.
	Values []string

	// Min and Max bound a SuppliedNumber key, both inclusive. Integer refuses a
	// fractional part.
	Min, Max float64
	Integer  bool

	// MaxLen bounds a SuppliedText value in bytes (at most MaxSuppliedValue; 0
	// means MaxSuppliedValue). Check must accept the value. A text spec without
	// a Check accepts nothing.
	MaxLen int
	Check  func(string) bool
}

// SuppliedKeyer is implemented by an importer that reads owner-supplied
// settings. It is optional: an importer without it accepts none.
type SuppliedKeyer interface {
	SuppliedKeys() map[string]SuppliedSpec
}

// ErrBadSupplied means a supplied setting names an unknown key or holds a value
// the key does not accept. It never carries the key or the value.
var ErrBadSupplied = errors.New("invalid supplied setting")

var (
	suppliedKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	plainNumber        = regexp.MustCompile(`^-?[0-9]{1,15}(\.[0-9]{1,9})?$`)
)

// ValidSuppliedKey reports whether key has the shape of a supplied key.
func ValidSuppliedKey(key string) bool {
	return len(key) <= maxSuppliedKey && suppliedKeyPattern.MatchString(key)
}

// ValidSuppliedText reports whether v is bounded plain text: non-empty, at most
// MaxSuppliedValue bytes, valid UTF-8, with no control or format characters.
func ValidSuppliedText(v string) bool {
	if v == "" || len(v) > MaxSuppliedValue || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}

// Canonical checks v against the spec and returns the value to store: a number
// in its shortest plain form, text and words unchanged. ok is false for any
// value the spec does not accept.
func (s SuppliedSpec) Canonical(v string) (string, bool) {
	switch s.Kind {
	case SuppliedEnum:
		for _, w := range s.Values {
			if v == w && ValidSuppliedText(v) {
				return v, true
			}
		}
	case SuppliedNumber:
		if !plainNumber.MatchString(v) {
			return "", false
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < s.Min || f > s.Max {
			return "", false
		}
		if s.Integer && f != float64(int64(f)) {
			return "", false
		}
		if f == 0 {
			return "0", true
		}
		return strconv.FormatFloat(f, 'f', -1, 64), true
	case SuppliedText:
		limit := s.MaxLen
		if limit <= 0 || limit > MaxSuppliedValue {
			limit = MaxSuppliedValue
		}
		if len(v) <= limit && ValidSuppliedText(v) && s.Check != nil && s.Check(v) {
			return v, true
		}
	}
	return "", false
}

// SuppliedKeysOf returns the settings imp declares, or nil when it declares none.
func SuppliedKeysOf(imp Importer) map[string]SuppliedSpec {
	if k, ok := imp.(SuppliedKeyer); ok {
		return k.SuppliedKeys()
	}
	return nil
}

// ValidateSupplied checks raw (key to value as received) against the settings
// imp declares and returns the canonical map for Options.Supplied. An empty raw
// gives nil. Any unknown key, malformed key, bad value or excess count is
// ErrBadSupplied.
func ValidateSupplied(imp Importer, raw map[string]string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > MaxSuppliedKeys {
		return nil, ErrBadSupplied
	}
	declared := SuppliedKeysOf(imp)
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		spec, ok := declared[key]
		if !ok || !ValidSuppliedKey(key) {
			return nil, ErrBadSupplied
		}
		canon, ok := spec.Canonical(value)
		if !ok {
			return nil, ErrBadSupplied
		}
		out[key] = canon
	}
	return out, nil
}
