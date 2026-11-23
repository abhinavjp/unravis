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
		"openers":   strings.Repeat("{name: ", 200000),
		"members":   "{" + strings.Repeat("name=a value=b ", 100000),
		"brackets":  strings.Repeat("[(", 300000),
		"quotes":    "{" + strings.Repeat("name='", 200000),
		"separator": "{" + strings.Repeat("a=", 400000),
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
