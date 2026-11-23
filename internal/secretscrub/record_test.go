package secretscrub

import (
	"strings"
	"testing"
	"time"
)

const rm = "[hidden]"

func TestMaskRecordsJSON(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"name then value", `{"name":"DB_PASSWORD","value":"x1y2"}`, `{"name":"DB_PASSWORD","value":"[hidden]"}`},
		{"value then name", `{"value":"x1y2","name":"DB_PASSWORD"}`, `{"value":"[hidden]","name":"DB_PASSWORD"}`},
		{"other members around", `{"slotSetting":false,"name":"ConnPassword","value":"s3cr3t","type":"x"}`, `{"slotSetting":false,"name":"ConnPassword","value":"[hidden]","type":"x"}`},
		{"capitalised", `{"Key":"ApiKey","Value":"abc123"}`, `{"Key":"ApiKey","Value":"[hidden]"}`},
		{"in an array", `[{"name":"PATH","value":"/bin"},{"name":"MYSQL_PWD","value":"hunter2"}]`, `[{"name":"PATH","value":"/bin"},{"name":"MYSQL_PWD","value":"[hidden]"}]`},
		{"pretty printed", "{\n  \"name\": \"client_secret\",\n  \"value\": \"zzz\"\n}", "{\n  \"name\": \"client_secret\",\n  \"value\": \"[hidden]\"\n}"},
		{"escaped quote in value", `{"name":"DB_PASSWORD","value":"a\"b c"}`, `{"name":"DB_PASSWORD","value":"[hidden]"}`},
		{"number value", `{"name":"CARD_PIN","value":123456}`, `{"name":"CARD_PIN","value":[hidden]}`},
		{"python repr", `[{'name': 'DB_PASSWORD', 'value': 'x1y2'}, {'name': 'HOST', 'value': 'db1'}]`, `[{'name': 'DB_PASSWORD', 'value': '[hidden]'}, {'name': 'HOST', 'value': 'db1'}]`},
		{"apostrophe before", `it's set: {'name':'DB_PASSWORD','value':'x1y2'}`, `it's set: {'name':'DB_PASSWORD','value':'[hidden]'}`},
		{"never closed", `{"name":"DB_PASSWORD","value":"x1y2"`, `{"name":"DB_PASSWORD","value":"[hidden]"`},
		{"inside a log line", `2026-11-23 env: {"name":"DB_PASSWORD","value":"x1y2"} applied`, `2026-11-23 env: {"name":"DB_PASSWORD","value":"[hidden]"} applied`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %s\ngot: %s\nwant: %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskRecordsJSONKeepsOrdinaryRecords(t *testing.T) {
	for _, in := range []string{
		`{"name":"PATH","value":"/usr/bin"}`,
		`{"name":"tokenizer","value":"bert"}`,
		`{"name":"token_count","value":"42"}`,
		`[{"name":"DB_HOST","value":"db1"},{"name":"DB_PASSWORD","other":"x"}]`,
		`{"name":"DB_PASSWORD"} {"value":"left alone"}`, // two objects: the name and the value are not one record
		`{"name":"DB_PASSWORD","nested":{"value":"inner"}}`,
		`{"title":"a","value":"v"}`,
	} {
		if got := Mask(in, rm); got != in {
			t.Errorf("changed an ordinary record:\n in: %s\ngot: %s", in, got)
		}
	}
}

func TestMaskRecordsYAMLList(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"k8s env", "env:\n  - name: DB_PASSWORD\n    value: x1y2\n  - name: DB_HOST\n    value: db1\n", "env:\n  - name: DB_PASSWORD\n    value: [hidden]\n  - name: DB_HOST\n    value: db1\n"},
		{"quoted", "- name: \"API_KEY\"\n  value: \"abc 123\"\n", "- name: \"API_KEY\"\n  value: \"[hidden]\"\n"},
		{"single quoted", "- name: 'DB_PASSWORD'\n  value: 'it''s'\n", "- name: 'DB_PASSWORD'\n  value: '[hidden]'\n"},
		{"value first", "- value: x1y2\n  name: DB_PASSWORD\n", "- value: [hidden]\n  name: DB_PASSWORD\n"},
		{"value in between", "- name: MYSQL_PWD\n  other: 1\n  value: x1y2\n", "- name: MYSQL_PWD\n  other: 1\n  value: [hidden]\n"},
		{"comment kept", "- name: DB_PASSWORD\n  value: x1y2 # rotate\n", "- name: DB_PASSWORD\n  value: [hidden] # rotate\n"},
		{"block value", "- name: DB_PASSWORD\n  value: |\n    line one\n    line two\n- name: NEXT\n  value: keep\n", "- name: DB_PASSWORD\n  value: |\n    [hidden]\n    [hidden]\n- name: NEXT\n  value: keep\n"},
		{"value on the next line", "- name: DB_PASSWORD\n  value:\n    x1y2\n", "- name: DB_PASSWORD\n  value:\n    [hidden]\n"},
		{"crlf", "- name: DB_PASSWORD\r\n  value: x1y2\r\n", "- name: DB_PASSWORD\r\n  value: [hidden]\r\n"},
		{"azure app settings", "appSettings:\n- name: ConnPassword\n  value: s3cr3t\n  slotSetting: false\n", "appSettings:\n- name: ConnPassword\n  value: [hidden]\n  slotSetting: false\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %q\ngot: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMaskRecordsYAMLKeepsNeighbours(t *testing.T) {
	in := "env:\n  - name: DB_HOST\n    value: db1\n  - name: DB_PASSWORD\n    value: x1y2\n  - name: DB_USER\n    value: app\n"
	got := Mask(in, rm)
	for _, keep := range []string{"value: db1", "value: app", "name: DB_USER"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "x1y2") {
		t.Errorf("secret kept:\n%s", got)
	}
	// The record before is not the record after: a secret name at the top of an item does not reach back into the previous item.
	in = "- name: A\n  value: keepA\n- name: DB_PASSWORD\n  value: x1y2\n"
	if got := Mask(in, rm); !strings.Contains(got, "keepA") || strings.Contains(got, "x1y2") {
		t.Errorf("neighbour wrong:\n%s", got)
	}
}

func TestMaskRecordsPlist(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"string", "<dict>\n<key>Password</key>\n<string>x1y2</string>\n</dict>", "<dict>\n<key>Password</key>\n<string>[hidden]</string>\n</dict>"},
		{"same line", "<key>ApiKey</key><string>abc</string>", "<key>ApiKey</key><string>[hidden]</string>"},
		{"data", "<key>client_secret</key>\n  <data>QUJD</data>", "<key>client_secret</key>\n  <data>[hidden]</data>"},
		{"integer", "<key>CARD_PIN</key><integer>4821</integer>", "<key>CARD_PIN</key><integer>[hidden]</integer>"},
		{"keeps the rest", "<key>Name</key><string>web</string><key>Password</key><string>pw1</string><key>Port</key><integer>80</integer>", "<key>Name</key><string>web</string><key>Password</key><string>[hidden]</string><key>Port</key><integer>80</integer>"},
		{"no close tag", "<key>Password</key><string>x1y2", "<key>Password</key><string>[hidden]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %q\ngot: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
	for _, in := range []string{"<key>Name</key><string>web</string>", "<key>tokenizer</key><string>bert</string>", "<key>Password</key><true/>"} {
		if got := Mask(in, rm); got != in {
			t.Errorf("changed ordinary plist: %q -> %q", in, got)
		}
	}
}

// The record readers are linear and fail closed like the rest of the package.
func TestMaskRecordsHostileInputIsFast(t *testing.T) {
	big := 1 << 20
	cases := map[string]string{
		"open braces":     strings.Repeat(`{"name":"DB_PASSWORD",`, big/22),
		"deep braces":     strings.Repeat("{", big),
		"quote soup":      strings.Repeat(`"name":"`, big/8),
		"many names":      strings.Repeat("- name: DB_PASSWORD\n", big/20),
		"names no values": strings.Repeat("  - name: DB_PASSWORD\n    x: 1\n", big/34),
		"plist keys":      strings.Repeat("<key>Password</key>", big/19),
		"long string":     `{"name":"DB_PASSWORD","value":"` + strings.Repeat("a", big) + `"}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_ = Mask(in, rm)
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("%s: %s for %d bytes", name, d, len(in))
			}
		})
	}
}

// A text over the work budget masks its rest, never keeps it.
func TestMaskRecordsOverBudgetFailsClosed(t *testing.T) {
	old := workFactor
	defer func() { workFactor = old }()
	workFactor = 0
	in := strings.Repeat(`{"name":"X","value":"y"} `, 6000) + `{"name":"DB_PASSWORD","value":"tail-secret"}`
	if got := Mask(in, rm); strings.Contains(got, "tail-secret") {
		t.Fatalf("a secret after the budget ran out was kept")
	}
}

// Random text made of the record marks never panics, never grows beyond the mask per byte, and never changes a text with no secret name.
func TestMaskRecordsRandomInputNeverPanics(t *testing.T) {
	pieces := []string{"{", "}", "[", "]", `"`, "'", ":", ",", "\n", "\r\n", " ", "  ", "- ", "name", "key", "value", "Value", "DB_PASSWORD", "PATH", "x", "<key>", "</key>", "<string>", "</string>", "Password", "#", "|", ">", `\`}
	seed := uint32(12345)
	next := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>8) % n }
	for round := 0; round < 3000; round++ {
		var b strings.Builder
		for k := 0; k < 4+next(60); k++ {
			b.WriteString(pieces[next(len(pieces))])
		}
		in := b.String()
		out := Mask(in, rm)
		if len(out) > len(in)+len(rm)*(len(in)/2+1) {
			t.Fatalf("output grew too much for %q: %q", in, out)
		}
		if !strings.Contains(strings.ToLower(in), "pass") && !strings.Contains(in, "DB_PASSWORD") && out != in {
			t.Fatalf("changed a text with no secret name:\n in: %q\nout: %q", in, out)
		}
	}
}
