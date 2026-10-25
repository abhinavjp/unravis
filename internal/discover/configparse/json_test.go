package configparse

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func pathFilter() JSONFilter {
	return JSONFilter{
		Decide: func(key string, container bool) Decision {
			switch {
			case key == "", key == "serilog", key == "serilog:writeto", strings.HasPrefix(key, "serilog:writeto:"):
				if container {
					return Descend
				}
				if strings.HasSuffix(key, ":path") {
					return Keep
				}
				return Skip
			case key == "applicationinsights":
				return Mark
			}
			return Skip
		},
		Validate: func(key, raw string) (string, string, bool) {
			if strings.Contains(strings.ToLower(raw), "secret") {
				return "", "config-unparsed", false
			}
			return raw, "", true
		},
	}
}

func TestParseJSONFlatKeysAndOverride(t *testing.T) {
	doc := `{
	  // comment
	  "ConnectionStrings": {"Db": "SENTINEL-7f3a"},
	  "ApplicationInsights": {"InstrumentationKey": "SENTINEL-7f3a"},
	  "Serilog": {"WriteTo": [ {"Name":"Console"}, {"Name":"File","Args":{"path":"a.log"}}, ],
	              "Other": "SENTINEL-7f3a"},
	  /* block */ "Serilog": {"WriteTo": [ {}, {"Args":{"path":"b.log"}} ]}
	}`
	res, err := ParseJSON([]byte(doc), pathFilter())
	if err != nil {
		t.Fatal(err)
	}
	if res.Values["serilog:writeto:1:args:path"] != "b.log" {
		t.Fatalf("later duplicate should win: %v", res.Values)
	}
	if !res.Present["applicationinsights"] {
		t.Fatalf("presence lost")
	}
	if strings.Contains(fmt.Sprintf("%v %v", res.Values, res.Rejects), "SENTINEL") {
		t.Fatalf("a value outside the allowlist was retained")
	}
}

func TestParseJSONRejectsAndRefusals(t *testing.T) {
	res, err := ParseJSON([]byte(`{"Serilog":{"WriteTo":[{"Args":{"path":"my-secret.log"}}]}}`), pathFilter())
	if err != nil || len(res.Values) != 0 || len(res.Rejects) != 1 || res.Rejects[0].Code != "config-unparsed" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Contains(fmt.Sprintf("%v", res.Rejects), "secret") {
		t.Fatalf("reject carries the value")
	}

	deep := strings.Repeat(`{"a":`, MaxDepth+2) + "1" + strings.Repeat("}", MaxDepth+2)
	deepSkipped := `{"x":` + strings.Repeat(`[`, MaxDepth+2) + strings.Repeat(`]`, MaxDepth+2) + `}`
	cases := map[string]struct {
		data []byte
		want error
	}{
		"deep walked":  {[]byte(deep), nil}, // the filter skips "a" so the skipper must still cap depth
		"deep skipped": {[]byte(deepSkipped), ErrDepth},
		"large":        {bytes.Repeat([]byte(" "), MaxBytes+1), ErrTooLarge},
		"unclosed":     {[]byte(`{"a": [1, 2`), ErrSyntax},
		"trailing":     {[]byte(`{} []`), ErrSyntax},
		"bad comment":  {[]byte(`{} /* never closed`), ErrSyntax},
		"empty":        {nil, ErrSyntax},
	}
	for name, c := range cases {
		_, err := ParseJSON(c.data, pathFilter())
		if name == "deep walked" {
			if err == nil {
				// a skipped over-deep container must be refused too
				t.Errorf("%s: over-deep input accepted", name)
			}
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", name, err, c.want)
		}
	}
}

func TestParseJSONBOMAndUTF16(t *testing.T) {
	bom := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"Serilog":{"WriteTo":[{"Args":{"path":"a.log"}}]}}`)...)
	res, err := ParseJSON(bom, pathFilter())
	if err != nil || res.Values["serilog:writeto:0:args:path"] != "a.log" {
		t.Fatalf("bom: %v %v", res, err)
	}
	u16 := []byte{0xFF, 0xFE}
	for _, r := range `{"Serilog":{"WriteTo":[{"Args":{"path":"a.log"}}]}}` {
		u16 = append(u16, byte(r), 0)
	}
	res, err = ParseJSON(u16, pathFilter())
	if err != nil || res.Values["serilog:writeto:0:args:path"] != "a.log" {
		t.Fatalf("utf16: %v %v", res, err)
	}
}

func TestStripJSONCKeepsStrings(t *testing.T) {
	in := `{"a":"http://x//y /* not a comment */","b":[1,2,],}`
	out, err := stripJSONC([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"http://x//y /* not a comment */"`) || strings.Contains(string(out), ",]") || strings.Contains(string(out), ",}") {
		t.Fatalf("out %q", out)
	}
}

func FuzzParseJSON(f *testing.F) {
	f.Add([]byte(`{"Serilog":{"WriteTo":[{"Args":{"path":"a.log"}}]}}`))
	f.Add([]byte(`{"a":[[[[`))
	f.Add([]byte("// x\n{/* y */}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		ParseJSON(data, pathFilter()) // must not panic or hang
	})
}
