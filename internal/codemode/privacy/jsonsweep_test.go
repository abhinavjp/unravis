package privacy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestJSONSweepsValuesAndKeys(t *testing.T) {
	s := NewSweeper(false)
	_ = s.Learn(KindUser, "alice@example.com")
	in := `{"ip":"10.1.2.3","note":"from 10.1.2.3 and ::1","alice@example.com":1,"list":["10.1.2.4",{"a":"x"}],"n":12,"f":1.50e3,"t":true,"z":null}`
	out, err := s.JSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid: %s", out)
	}
	want := `{"ip":"[IP_1]","note":"from [IP_1] and [IP_2]","[USER_1]":1,"list":["[IP_3]",{"a":"x"}],"n":12,"f":1.50e3,"t":true,"z":null}`
	if string(out) != want {
		t.Fatalf("got  %s\nwant %s", out, want)
	}
}

func TestJSONUnchangedReturnsSameBytes(t *testing.T) {
	s := NewSweeper(false)
	in := []byte("{ \"a\" : [1, 2,\n 3],  \"b\":\"x\" }")
	out, err := s.JSON(in)
	if err != nil || string(out) != string(in) {
		t.Fatalf("%q %v", out, err)
	}
}

func TestJSONKeepsOrderAndDuplicates(t *testing.T) {
	s := NewSweeper(false)
	out, err := s.JSON([]byte(`{"z":"10.0.0.1","a":1,"z":2,"m":{"y":[],"x":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"z":"[IP_1]","a":1,"z":2,"m":{"y":[],"x":{}}}` {
		t.Fatalf("got %s", out)
	}
}

func TestJSONScalarsAndEscapes(t *testing.T) {
	s := NewSweeper(false)
	for in, want := range map[string]string{
		`"10.0.0.1"`:         `"[IP_1]"`,
		`"a<b>&\u0041"`:      `"a<b>&\u0041"`, // unchanged: original bytes kept
		`["<10.0.0.1>&"]`:    `["<[IP_1]>&"]`, // no HTML escaping added
		`"line\n10.0.0.1\t"`: `"line\n[IP_1]\t"`,
		`7`:                  `7`,
		`null`:               `null`,
	} {
		out, err := s.JSON([]byte(in))
		if err != nil || string(out) != want {
			t.Errorf("%s: got %s err %v want %s", in, out, err, want)
		}
	}
}

func TestJSONLearnedNumber(t *testing.T) {
	s := NewSweeper(false)
	_ = s.Learn(KindUser, "12345678")
	_ = s.Learn(KindUser, "42") // a number is always a whole value, however short: a count of 42 is hidden too (fail closed)
	out, err := s.JSON([]byte(`{"id":12345678,"count":42,"other":43}`))
	if err != nil || string(out) != `{"id":"[USER_1]","count":"[USER_2]","other":43}` {
		t.Fatalf("%s %v", out, err)
	}
}

func TestJSONRefusals(t *testing.T) {
	s := NewSweeper(false)
	bad := []string{``, `{`, `{"a":}`, `[1,]`, `{"a":1} x`, `1 2`, `{"a":1}{"b":2}`, `}`, `]`, `{"a" 1}`, `nul`, `"abc`, `{1:2}`}
	for _, in := range bad {
		if _, err := s.JSON([]byte(in)); !errors.Is(err, ErrBadJSON) {
			t.Errorf("%q: err %v", in, err)
		}
		if _, err := NewSweeper(true).JSON([]byte(in)); !errors.Is(err, ErrBadJSON) {
			t.Errorf("allow %q: err %v", in, err)
		}
	}
	if _, err := s.JSON([]byte(strings.Repeat("[", maxDepth+1) + strings.Repeat("]", maxDepth+1))); !errors.Is(err, ErrTooDeep) {
		t.Errorf("deep: %v", err)
	}
	if _, err := s.JSON([]byte(strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth))); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	if _, err := s.JSON(make([]byte, MaxJSONBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("large: %v", err)
	}
	var e *json.SyntaxError
	if _, err := s.JSON([]byte(`{"ip":"10.9.9.9" x`)); errors.As(err, &e) || (err != nil && strings.Contains(err.Error(), "10.9.9.9")) {
		t.Errorf("error carries the text: %v", err)
	}
}

func TestJSONAllowModeValidatesOnly(t *testing.T) {
	in := []byte(`{"ip":"10.1.2.3"}`)
	out, err := NewSweeper(true).JSON(in)
	if err != nil || string(out) != string(in) {
		t.Fatal(string(out), err)
	}
}

// Property: for a JSON text built from random parts, the swept text is valid JSON, holds no address, and decodes to the same
// shape (same number of members) as the input.
func FuzzJSONSweep(f *testing.F) {
	for _, s := range []string{`{"a":"10.0.0.1"}`, `["::1",{"::1":[null,true,1.5]}]`, `"x"`, `{"a":{"b":{"c":"1.2.3.4"}}}`, `[]`, `{}`, `{"k":"v"`, "\xff"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		s := NewSweeper(false)
		out, err := s.JSON(in)
		if err != nil {
			return
		}
		if !json.Valid(out) {
			t.Fatalf("invalid output %q from %q", out, in)
		}
		var before, after any
		if json.Unmarshal(in, &before) == nil {
			if err := json.Unmarshal(out, &after); err != nil {
				t.Fatalf("output does not decode: %v", err)
			}
			if count(before) != count(after) {
				t.Fatalf("shape changed: %q -> %q", in, out)
			}
			// every string and key in the output is free of addresses
			walk(after, func(str string) {
				if ContainsIP(str) {
					t.Fatalf("address in %q (from %q)", str, in)
				}
			})
		}
	})
}

func count(v any) int {
	n := 1
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			n += count(e)
		}
	case []any:
		for _, e := range x {
			n += count(e)
		}
	}
	return n
}

func walk(v any, f func(string)) {
	switch x := v.(type) {
	case string:
		f(x)
	case map[string]any:
		for k, e := range x {
			f(k)
			walk(e, f)
		}
	case []any:
		for _, e := range x {
			walk(e, f)
		}
	}
}
