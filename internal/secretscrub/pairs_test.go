package secretscrub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPairRecordsMaskTheValue(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"flow yaml", `env: [{name: DB_PASSWORD, value: SECRET1}]`},
		{"flow yaml reversed", `{value: SECRET2, name: DB_PASSWORD}`},
		{"flow yaml quoted", `{name: "DB_PASSWORD", value: "SECRET3 x"}`},
		{"go map", `got map[name:DB_PASSWORD value:SECRET4]`},
		{"go struct", `{Name:DB_PASSWORD Value:SECRET5}`},
		{"go %#v", `{Name:"DB_PASSWORD", Value:"SECRET6"}`},
		{"python kwargs", `EnvVar(name='DB_PASSWORD', value='SECRET7')`},
		{"python kwargs reversed", `EnvVar(value='SECRET8', name='DB_PASSWORD')`},
		{"python list", `[EnvVar(name='API_TOKEN', value='SECRET9', value_from=None)]`},
		{"hcl", "env {\n  name  = \"DB_PASSWORD\"\n  value = \"SECRET10\"\n}"},
		{"hcl reversed", "env {\n  value = \"SECRET11\"\n  name  = \"DB_PASSWORD\"\n}"},
		{"hcl bare", "env {\n  name = DB_PASSWORD\n  value = SECRET12\n}"},
		{"key and value", `{key: client_secret, value: SECRET13}`},
		{"never closes", `x = {name: DB_PASSWORD, value: SECRET14`},
		{"quote never closes", `{name: DB_PASSWORD, value: "SECRET15` + "\n"},
		{"inside a json string", `{"cmd":"run(name=DB_PASSWORD, value=SECRET16)"}`},
		{"cut and nested", `a{b(c[name:DB_PASSWORD value:SECRET17]`},
		// Review: escaped quotes inside a JSON string, the common Go %#v print in a log message.
		{"escaped go print", `{"msg": "{Name:\"DB_PASSWORD\", Value:\"SECRET18\"}"}`},
		{"escaped python", `{"msg": "EnvVar(name=\"DB_PASSWORD\", value=\"SECRET19\")"}`},
		{"escaped flow yaml", `{"msg": "{name: \"DB_PASSWORD\", value: \"SECRET20\"}"}`},
		// Review: a bare value runs on over punctuation and white space.
		{"comma in value", `map[name:DB_PASSWORD value:ab,SECRET21]`},
		{"semicolon in value", `map[name:DB_PASSWORD value:ab;SECRET22]`},
		{"other bracket in value", `[name:DB_PASSWORD value:ab)SECRET23]`},
		{"nested bracket in value", `(name=DB_PASSWORD value=a(x)(y)SECRET24)`},
		{"space in value", `{Name:DB_PASSWORD Value:hello SECRET25}`},
		{"quote in value", `map[name:DB_PASSWORD value:ab'SECRET26]`},
		{"value then name, comma in value", `{value: ab,SECRET27, name: DB_PASSWORD}`},
		// Review: a bracket inside the quoted text of another member does not close the record.
		{"bracket in other member", `EnvVar(note='a)', name='DB_PASSWORD', value='SECRET28')`},
		{"heredoc", "env {\n  name = \"DB_PASSWORD\"\n  value = <<EOT\nSECRET29\nline two SECRET30\nEOT\n}"},
		{"value on next line", "{name: DB_PASSWORD, value:\n  SECRET31}"},
		{"yaml tag", `{name: DB_PASSWORD, value: !!str SECRET32}`},
		// Review round 2: an unmatched bracket in another value does not eat the next record.
		{"unmatched bracket before", `[{name: A, value: a(b}, {name: DB_PASSWORD, value: SECRET34}]`},
		{"unmatched bracket regex", `[{name: PATTERN, value: ^(a|b}, {name: DB_PASSWORD, value: SECRET35}]`},
		{"heredoc never ends", "{name: A, value = <<EOT\nfoo\n}\n{name: DB_PASSWORD, value: SECRET36}"},
		{"name before a bracket", `(name=DB_PASSWORD(x) value=SECRET37)`},
		{"escaped quote in unclosed", `{"m": "(name=DB_PASSWORD value='ab\"SECRET38)"}`},
		// Review round 3: a quoted value followed by text is still a value.
		{"quote then slash", `(name="DB_PASSWORD" value="SECRET39"/>`},
		{"quote then comment", `{name: DB_PASSWORD, value: "SECRET40"#c}`},
		{"quote then call", `Env(name='DB_PASSWORD', value='SECRET41'.strip())`},
		{"quote then pipe", `{name: DB_PASSWORD, value: "SECRET42"|base64}`},
		{"unclosed quote in json string", `{"msg": "(name=DB_PASSWORD value='SECRET33)"}`},
	} {
		if out := Mask(tc.in, m); strings.Contains(out, "SECRET") {
			t.Errorf("%s: leaked: %q -> %q", tc.name, tc.in, out)
		}
	}
}

func TestPairRecordsLeaveOtherThingsReadable(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"plain setting", `{name: LOG_LEVEL, value: debug}`},
		{"go map plain", `map[name:Timeout value:30]`},
		{"python plain", `EnvVar(name='PATH', value='/usr/bin')`},
		{"no bracket", "name: DB_HOST\nvalue: db1\n"},
		{"url", `see (http://host/x name=Title)`},
		{"comparison", `if (name == "DB_PASSWORD" && value == "x")`},
		{"title", `{name: "Reset password", value: later}`},
		{"value word in another member", `{description: "set value: notsecret", name: DB_PASSWORD_HINT_X}`},
	} {
		if out := Mask(tc.in, m); out != tc.in {
			t.Errorf("%s: changed: %q -> %q", tc.name, tc.in, out)
		}
	}
}

// A value inside a JSON string must not break the JSON around it.
func TestPairRecordsKeepJSONValid(t *testing.T) {
	for _, in := range []string{
		`{"cmd":"run(name=DB_PASSWORD, value=SECRET1)","n":1}`,
		`{"cmd":"[name=DB_PASSWORD, value=SECRET2"}`,
		`{"cmd":"{name: API_TOKEN, value: SECRET3}"}`,
		`{"cmd":"EnvVar(name='DB_PASSWORD', value='SECRET4')"}`,
		`{"cmd":"(name=DB_PASSWORD value=SECRET5\"x)"}`,
		`{"msg": "(name=DB_PASSWORD value='SECRET6)"}`,
		`{"msg":"set name=DB_PASSWORD value: ","b":1}`,
		`{"msg": "(name=DB_PASSWORD value: "}`,
		`["name=DB_PASSWORD value=", 1]`,
		`{"a": 1, "z": ["{name: DB_PASSWORD, value: "]}`,
		`{"m": "(name=DB_PASSWORD value='ab\"SECRET10)"}`,
		`{"msg":"name: DB_PASSWORD value: ","b":{"c":"d"}}`,
		`{"a":"x","msg":"[name:DB_PASSWORD value:'SECRET7","z":1}`,
		`{"msg": "{Name:\"DB_PASSWORD\", Value:\"SECRET8\"}","z":1}`,
		`{"msg": "map[name:DB_PASSWORD value:ab,SECRET9]","z":1}`,
	} {
		out := Mask(in, m)
		if strings.Contains(out, "SECRET") {
			t.Errorf("leaked: %q -> %q", in, out)
		}
		if !json.Valid([]byte(out)) {
			t.Errorf("not valid JSON any more: %q -> %q", in, out)
		}
	}
}

func TestPairRecordsHostile(t *testing.T) {
	for name, in := range map[string]string{
		"openers":    strings.Repeat("{name: ", 200000),
		"members":    "{" + strings.Repeat("name=a value=b ", 100000),
		"brackets":   strings.Repeat("[(", 300000),
		"quotes":     "{" + strings.Repeat("name='", 200000),
		"separator":  "{" + strings.Repeat("a=", 400000),
		"heredocs":   "{" + strings.Repeat("value = <<E\nx\n", 200000),
		"spaces":     "{value: " + strings.Repeat("a b ", 400000),
		"commas":     "{value: " + strings.Repeat("a,b;", 400000),
		"nested":     strings.Repeat("(value=a(", 200000),
		"space run":  "{value: a" + strings.Repeat(" ", 1<<20),
		"comma run":  "{value: a" + strings.Repeat(",", 1<<20),
		"mixed runs": "{value: a" + strings.Repeat(", ;\t", 1<<18),
		"strings":    strings.Repeat("{note: 'a)' value: ", 100000),
	} {
		start := time.Now()
		out := Mask(in, m)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: %s for %d bytes", name, d, len(in))
		}
		if len(out) > 4*len(in)+64 {
			t.Errorf("%s: output grew from %d to %d", name, len(in), len(out))
		}
	}
}
