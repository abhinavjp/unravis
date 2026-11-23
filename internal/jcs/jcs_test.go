package jcs

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// The number vectors of RFC 8785 appendix B.
func TestRFC8785AppendixBNumbers(t *testing.T) {
	cases := []struct {
		bits uint64
		want string
	}{
		{0x0000000000000000, "0"},
		{0x8000000000000000, "0"},
		{0x0000000000000001, "5e-324"},
		{0x8000000000000001, "-5e-324"},
		{0x7fefffffffffffff, "1.7976931348623157e+308"},
		{0xffefffffffffffff, "-1.7976931348623157e+308"},
		{0x4340000000000000, "9007199254740992"},
		{0xc340000000000000, "-9007199254740992"},
		{0x4430000000000000, "295147905179352830000"},
		{0x44b52d02c7e14af5, "9.999999999999997e+22"},
		{0x44b52d02c7e14af6, "1e+23"},
		{0x44b52d02c7e14af7, "1.0000000000000001e+23"},
		{0x444b1ae4d6e2ef4e, "999999999999999700000"},
		{0x444b1ae4d6e2ef4f, "999999999999999900000"},
		{0x444b1ae4d6e2ef50, "1e+21"},
		{0x3eb0c6f7a0b5ed8c, "9.999999999999997e-7"},
		{0x3eb0c6f7a0b5ed8d, "0.000001"},
		{0x41b3de4355555553, "333333333.3333332"},
		{0x41b3de4355555554, "333333333.33333325"},
		{0x41b3de4355555555, "333333333.3333333"},
		{0x41b3de4355555556, "333333333.3333334"},
		{0x41b3de4355555557, "333333333.33333343"},
		{0xbecbf647612f3696, "-0.0000033333333333333333"},
		{0x43143ff3c1cb0959, "1424953923781206.2"},
	}
	for _, c := range cases {
		got, err := FormatFloat(math.Float64frombits(c.bits))
		if err != nil || got != c.want {
			t.Errorf("%016x = %q, %v; want %q", c.bits, got, err, c.want)
		}
	}
	for _, bits := range []uint64{0x7fffffffffffffff, 0x7ff0000000000000, 0xfff0000000000000} {
		if _, err := FormatFloat(math.Float64frombits(bits)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%016x: want ErrInvalid, got %v", bits, err)
		}
	}
}

func TestCanonicalizeSortsAndStrips(t *testing.T) {
	in := ` { "b" : [1, 2.50, 1e2, -0, true, null],
	  "a": {"z": "x", "y": 1E-7},  "\u20ac": 1, "\r": 2, "aa": "\u00e9\u007f<>&" } `
	want := "{\"\\r\":2,\"a\":{\"y\":1e-7,\"z\":\"x\"},\"aa\":\"\u00e9\u007f<>&\",\"b\":[1,2.5,100,0,true,null],\"\u20ac\":1}"
	got, err := Canonicalize([]byte(in))
	if err != nil || string(got) != want {
		t.Fatalf("got %q, %v\nwant %q", got, err, want)
	}
	// Canonical output is a fixed point.
	again, err := Canonicalize(got)
	if err != nil || string(again) != want {
		t.Fatalf("not a fixed point: %q %v", again, err)
	}
}

// RFC 8785 section 3.2.3: names sort by UTF-16 code units, not by code point or by byte.
func TestSortOrderIsUTF16(t *testing.T) {
	in := "{\"\U0001F600\":1,\"\uFB33\":2,\"\u00e9\":3,\"1\":4}"
	got, err := Canonicalize([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	// U+1F600 is the pair D83D DE00 in UTF-16, which sorts before U+FB33.
	want := "{\"1\":4,\"\u00e9\":3,\"\U0001F600\":1,\"\uFB33\":2}"
	if string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStringEscapes(t *testing.T) {
	got, err := Marshal(map[string]string{"k": "a\"b\\c\n\t\b\f\r\x01\u2028"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"k\":\"a\\\"b\\\\c\\n\\t\\b\\f\\r\\u0001\u2028\"}"; string(got) != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRefusals(t *testing.T) {
	deep := strings.Repeat("[", MaxDepth+1) + strings.Repeat("]", MaxDepth+1)
	for name, in := range map[string]string{
		"duplicate name":   `{"a":1,"a":2}`,
		"trailing data":    `{} {}`,
		"trailing comma":   `[1,]`,
		"empty":            ``,
		"bad utf-8":        "\"\xff\"",
		"huge number":      `1e999`,
		"too deep":         deep,
		"unterminated":     `{"a":`,
		"bare word":        `nope`,
		"non string name":  `{1:2}`,
		"leading zero num": `[01]`,
	} {
		if _, err := Canonicalize([]byte(in)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := Canonicalize([]byte(strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth))); err != nil {
		t.Errorf("nesting at the limit: %v", err)
	}
	if _, err := Marshal(math.NaN()); !errors.Is(err, ErrInvalid) {
		t.Errorf("Marshal(NaN) = %v", err)
	}
}

func TestMarshalStructsAndRecords(t *testing.T) {
	type rec struct {
		N     int64  `json:"n"`
		Phase string `json:"phase"`
		A     string `json:"a,omitempty"`
	}
	got, err := Marshal(rec{N: 7, Phase: "begin"})
	if err != nil || string(got) != `{"n":7,"phase":"begin"}` {
		t.Fatalf("got %q %v", got, err)
	}
}
