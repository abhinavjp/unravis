package importer

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
)

type plainImporter struct{}

func (plainImporter) Name() string                  { return "plain" }
func (plainImporter) Detect([]byte, string) int     { return 0 }
func (plainImporter) AllowedAttrs() map[string]bool { return nil }
func (plainImporter) DefaultLimits() Limits         { return Limits{} }
func (plainImporter) Parse(context.Context, io.Reader, Options, func(evidence.Event) error) (Report, error) {
	return Report{}, nil
}

type keyedImporter struct{ plainImporter }

func (keyedImporter) SuppliedKeys() map[string]SuppliedSpec {
	return map[string]SuppliedSpec{
		"log-time": {Kind: SuppliedEnum, Values: []string{"utc", "local"}},
		"slowms":   {Kind: SuppliedNumber, Min: 0, Max: 600000, Integer: true},
		"ratio":    {Kind: SuppliedNumber, Min: -1, Max: 1},
		"pattern":  {Kind: SuppliedText, MaxLen: 40, Check: func(v string) bool { return strings.Contains(v, "%m") }},
		"nocheck":  {Kind: SuppliedText},
		"unset":    {},
	}
}

func TestValidateSuppliedAccepts(t *testing.T) {
	got, err := ValidateSupplied(keyedImporter{}, map[string]string{
		"log-time": "utc", "slowms": "0100", "ratio": "-0.50", "pattern": "%d %m",
	})
	// "0100" is a plain number with a leading zero; it is stored in its shortest form.
	if err != nil {
		t.Fatalf("ValidateSupplied: %v", err)
	}
	want := map[string]string{"log-time": "utc", "slowms": "100", "ratio": "-0.5", "pattern": "%d %m"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got, err := ValidateSupplied(keyedImporter{}, map[string]string{"slowms": "-0"}); err != nil || got["slowms"] != "0" {
		t.Errorf("zero: %v, %v", got, err)
	}
}

func TestValidateSuppliedEmptyIsNil(t *testing.T) {
	for _, imp := range []Importer{nil, plainImporter{}, keyedImporter{}} {
		if got, err := ValidateSupplied(imp, nil); got != nil || err != nil {
			t.Errorf("%T nil: %v, %v", imp, got, err)
		}
		if got, err := ValidateSupplied(imp, map[string]string{}); got != nil || err != nil {
			t.Errorf("%T empty: %v, %v", imp, got, err)
		}
	}
}

func TestValidateSuppliedRefuses(t *testing.T) {
	many := map[string]string{}
	for i := 0; i <= MaxSuppliedKeys; i++ {
		many[string(rune('a'+i))] = "1"
	}
	cases := map[string]map[string]string{
		"unknown key":          {"surprise": "1"},
		"unset kind":           {"unset": "1"},
		"upper-case key":       {"Slowms": "100"},
		"enum value":           {"log-time": "UTC"},
		"enum with space":      {"log-time": "utc "},
		"fraction in integer":  {"slowms": "1.5"},
		"above max":            {"slowms": "600001"},
		"below min":            {"slowms": "-1"},
		"exponent":             {"slowms": "1e3"},
		"hex":                  {"slowms": "0x10"},
		"plus sign":            {"slowms": "+5"},
		"empty number":         {"slowms": ""},
		"words":                {"slowms": "ten"},
		"too many digits":      {"slowms": "1234567890123456"},
		"nan":                  {"ratio": "NaN"},
		"inf":                  {"ratio": "Inf"},
		"text without check":   {"nocheck": "abc"},
		"text fails check":     {"pattern": "%d"},
		"text over its bound":  {"pattern": strings.Repeat("%m", 21)},
		"text with newline":    {"pattern": "%m\n"},
		"text with zero width": {"pattern": "%m​"},
		"text bad utf-8":       {"pattern": "%m\xff"},
		"one good one bad":     {"slowms": "100", "surprise": "1"},
		"more than 16":         many,
	}
	for name, raw := range cases {
		got, err := ValidateSupplied(keyedImporter{}, raw)
		if !errors.Is(err, ErrBadSupplied) || got != nil {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
	// An importer that declares nothing, or no importer, accepts nothing.
	for _, imp := range []Importer{nil, plainImporter{}} {
		if _, err := ValidateSupplied(imp, map[string]string{"slowms": "1"}); !errors.Is(err, ErrBadSupplied) {
			t.Errorf("%T: %v", imp, err)
		}
	}
}

func TestSuppliedErrorsNameNeitherKeyNorValue(t *testing.T) {
	const secret = "SENTINEL-7f3a"
	for _, raw := range []map[string]string{{secret: "1"}, {"slowms": secret}, {"pattern": secret}} {
		_, err := ValidateSupplied(keyedImporter{}, raw)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("%v: %v", raw, err)
		}
	}
}

func TestValidSuppliedKeyAndText(t *testing.T) {
	for _, k := range []string{"a", "slowms", "log-time", "app-request-min-ms", "0", strings.Repeat("a", 40)} {
		if !ValidSuppliedKey(k) {
			t.Errorf("key %q refused", k)
		}
	}
	for _, k := range []string{"", "-a", "A", "a_b", "a.b", "a b", "s.a", strings.Repeat("a", 41), "é"} {
		if ValidSuppliedKey(k) {
			t.Errorf("key %q accepted", k)
		}
	}
	if !ValidSuppliedText("a b %m") || !ValidSuppliedText(strings.Repeat("x", MaxSuppliedValue)) {
		t.Error("plain text refused")
	}
	for _, v := range []string{"", strings.Repeat("x", MaxSuppliedValue+1), "a\tb", "a b", "\xff"} {
		if ValidSuppliedText(v) {
			t.Errorf("text %q accepted", v)
		}
	}
}
