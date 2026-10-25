package configparse

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)

func fileFilter() XMLFilter {
	return XMLFilter{
		Attrs: map[string][]string{"appender": {"type"}, "file": {"value"}},
		Keep: func(elem string, anc []string, raw map[string]string) (map[string]string, []Reject) {
			return raw, nil
		},
	}
}

func TestParseXMLKeepsOnlyAllowlisted(t *testing.T) {
	doc := `<configuration><connectionStrings><add connectionString="SENTINEL-7f3a"/></connectionStrings>
	<log4net><appender type="X" password="SENTINEL-7f3a"><file value="a.log" other="SENTINEL-7f3a"/><smtp value="SENTINEL-7f3a"/></appender></log4net></configuration>`
	res, err := ParseXML([]byte(doc), fileFilter())
	if err != nil {
		t.Fatal(err)
	}
	var dump strings.Builder
	var walk func(n *Node)
	walk = func(n *Node) {
		fmt.Fprintf(&dump, "%s %v;", n.Name, n.Attrs)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(res.Root)
	if strings.Contains(dump.String(), "SENTINEL") {
		t.Fatalf("a non-allowlisted value was retained: %s", dump.String())
	}
	if !strings.Contains(dump.String(), "a.log") {
		t.Fatalf("allowlisted value lost: %s", dump.String())
	}
}

func TestParseXMLRefusals(t *testing.T) {
	deep := strings.Repeat("<a>", MaxDepth+2) + strings.Repeat("</a>", MaxDepth+2)
	cases := map[string]struct {
		data []byte
		want error
	}{
		"dtd":         {[]byte(`<!DOCTYPE x [<!ENTITY a "b">]><x>&a;</x>`), ErrDTD},
		"deep":        {[]byte(deep), ErrDepth},
		"large":       {bytes.Repeat([]byte(" "), MaxBytes+1), ErrTooLarge},
		"unclosed":    {[]byte(`<a><b></a>`), ErrSyntax},
		"truncated":   {[]byte(`<a><b>`), ErrSyntax},
		"undef ent":   {[]byte(`<a>&nope;</a>`), ErrSyntax},
		"bad charset": {[]byte(`<?xml version="1.0" encoding="x-unknown"?><a/>`), ErrSyntax},
	}
	for name, c := range cases {
		_, err := ParseXML(c.data, fileFilter())
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", name, err, c.want)
		}
	}
}

func TestParseXMLEncodings(t *testing.T) {
	body := `<?xml version="1.0" encoding="utf-16"?><log4net><appender type="X"><file value="a.log"/></appender></log4net>`
	u := utf16.Encode([]rune(body))
	le := []byte{0xFF, 0xFE}
	for _, c := range u {
		le = append(le, byte(c), byte(c>>8))
	}
	bom8 := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`<a><file value="b.log"/></a>`)...)
	for name, d := range map[string][]byte{"utf16": le, "utf8 bom": bom8} {
		res, err := ParseXML(d, fileFilter())
		if err != nil || res.Root == nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestParseXMLRejectsFromKeep(t *testing.T) {
	f := XMLFilter{
		Attrs: map[string][]string{"file": {"value"}},
		Keep: func(elem string, anc []string, raw map[string]string) (map[string]string, []Reject) {
			return nil, []Reject{{Element: elem, Attr: "value", Code: "unsafe-path"}}
		},
	}
	res, err := ParseXML([]byte(`<a><file value="SENTINEL-7f3a"/></a>`), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rejects) != 1 || res.Rejects[0].Code != "unsafe-path" || strings.Contains(fmt.Sprintf("%v", res.Rejects), "SENTINEL") {
		t.Fatalf("rejects %v", res.Rejects)
	}
}

func FuzzParseXML(f *testing.F) {
	f.Add([]byte(`<a><appender type="x"><file value="a.log"/></appender></a>`))
	f.Add([]byte(`<!DOCTYPE a><a/>`))
	f.Add([]byte("\xff\xfe<\x00a\x00/\x00>\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		ParseXML(data, fileFilter()) // must not panic or hang
	})
}
