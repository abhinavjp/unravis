package secretscrub

import (
	"runtime"
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

// Review of PR 203 (security): every shape that leaked or that cost too much.
func TestMaskRecordsReviewFindings(t *testing.T) {
	leak := func(name, in string, secrets ...string) {
		t.Helper()
		got := Mask(in, rm)
		for _, sec := range secrets {
			if strings.Contains(got, sec) {
				t.Errorf("%s: %q is still in the text:\n%s", name, sec, got)
			}
		}
	}
	// 1. One stray quote on a line does not turn the reader off for the lines after it.
	stray := "msg=\"truncated\n" + strings.Repeat(`{"env":[{"name":"DB_PASSWORD","value":"LEAKZ"}]}`+"\n", 50)
	leak("stray quote", stray, "LEAKZ")
	// 2. JSON inside a JSON string (kubectl last-applied-configuration).
	leak("escaped json", `{"annotations":{"last":"{\"env\":[{\"name\":\"DB_PASSWORD\",\"value\":\"LEAKZ\"}]}"}}`, "LEAKZ")
	leak("escaped json, value first", `x: "{\"value\":\"LEAKZ\",\"name\":\"DB_PASSWORD\"}"`, "LEAKZ")
	// 5. Fail closed with the record still open: the value that came before its name is masked.
	old := workFactor
	workFactor = 0
	leak("budget, open record", `{"value":"LEAKZ","name":"DB_PASSWORD"`+strings.Repeat(` `, 80<<10), "LEAKZ")
	workFactor = old
	// 6. Python repr over several lines.
	leak("python multiline", "[{'name': 'DB_PASSWORD',\n  'value': 'LEAKZ'}]", "LEAKZ")
	// 7. An array value.
	leak("array value", `{"name":"DB_PASSWORD","value":["LEAKA","LEAKB"]}`, "LEAKA", "LEAKB")
	// 8. A long value (a PEM key) and what follows it.
	pem := strings.Repeat("A", 20000)
	got := Mask(`{"name":"TLS_PRIVATE_KEY","value":"`+pem+`"},{"name":"DB_PASSWORD","value":"LEAKZ"}`, rm)
	if strings.Contains(got, "AAAAAAAAAA") || strings.Contains(got, "LEAKZ") {
		t.Errorf("long value or the record after it kept")
	}
	// 10. A quoted YAML scalar over two lines.
	leak("yaml quoted two lines", "- name: DB_PASSWORD\n  value: \"first part\n    LEAKZ\"\n- name: X\n  value: keep\n", "LEAKZ", "first part")
	if got := Mask("- name: DB_PASSWORD\n  value: \"first part\n    LEAKZ\"\n- name: X\n  value: keep\n", rm); !strings.Contains(got, "value: keep") {
		t.Errorf("the next item was masked: %s", got)
	}
	// Nits: extra spaces after the dash and before the colon.
	leak("dash spaces", "-   name: DB_PASSWORD\n    value: LEAKZ\n", "LEAKZ")
	leak("space before colon", "- name: DB_PASSWORD\n  value : LEAKZ\n", "LEAKZ")
	leak("value first, dash spaces", "-   value: LEAKZ\n    name: DB_PASSWORD\n", "LEAKZ")

	// 9. Names that are titles or prose are not secret names.
	for _, in := range []string{
		`{"name":"Reset password","value":"improve UX"}`,
		"- name: Password Reset Tool\n  value: improve UX\n",
	} {
		if got := Mask(in, rm); got != in {
			t.Errorf("a title was taken for a secret name:\n in: %s\ngot: %s", in, got)
		}
	}
}

// 3 and 4: work and memory stay bounded for the shapes that cost more than they read.
func TestMaskRecordsReviewCostShapes(t *testing.T) {
	for name, in := range map[string]string{
		"unclosed backslash quotes": `{"` + strings.Repeat(`"\`, 1<<20),
		"quotes without close":      strings.Repeat(`{"a`, 1<<19),
		"many blank lines":          "a:" + strings.Repeat("\n", 8<<20),
		"names, many blank lines":   "- name: DB_PASSWORD" + strings.Repeat("\n", 4<<20),
		"escaped quotes":            strings.Repeat(`\"`, 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_ = Mask(in, rm)
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("%s: %s for %d bytes", name, d, len(in))
			}
		})
	}
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)
	_ = Mask("a:"+strings.Repeat("\n", 8<<20), rm)
	runtime.ReadMemStats(&m2)
	if grown := m2.TotalAlloc - m1.TotalAlloc; grown > 200<<20 {
		t.Fatalf("a text of 8M line breaks allocated %d MiB", grown>>20)
	}
}

// Round 2 of the security review (SWP-1122-13).
func TestMaskRecordsRound2Findings(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"cut objects do not switch the reader off",
			strings.Repeat(`{"a":{"b":{"c":{"d":{"e":"x`+"\n", 12) + `{"name":"DB_PASSWORD","value":"leak1"}`,
			strings.Repeat(`{"a":{"b":{"c":{"d":{"e":"x`+"\n", 12) + `{"name":"DB_PASSWORD","value":"[hidden]"}`},
		{"unbalanced braces in text",
			strings.Repeat("tmpl ${ x\n", 40) + `{"name":"DB_PASSWORD","value":"leak2"}`,
			strings.Repeat("tmpl ${ x\n", 40) + `{"name":"DB_PASSWORD","value":"[hidden]"}`},
		{"escaped quote inside the inner value",
			`{"a":"{\"name\":\"DB_PASSWORD\",\"value\":\"a\\\"b leak3\"}"}`,
			`{"a":"{\"name\":\"DB_PASSWORD\",\"value\":\"[hidden]\"}"}`},
		{"stray escaped quote outside a string",
			`x \" y {"name":"DB_PASSWORD","value":"leak4"}`,
			`x \" y {"name":"DB_PASSWORD","value":"[hidden]"}`},
		{"string over 64 KiB on one line",
			`{"name":"DB_PASSWORD","value":"` + strings.Repeat("a", 70000) + `"} {"name":"API_KEY","value":"leak5"}`,
			`{"name":"DB_PASSWORD","value":"[hidden]"} {"name":"API_KEY","value":"[hidden]"}`},
		{"bare and quoted span on one value",
			`{"name":"DB_PASSWORD","value": abc LEAK6}`,
			`{"name":"DB_PASSWORD","value": [hidden] LEAK6}`},
		{"comment lines do not use the window",
			"- name: DB_PASSWORD\n" + strings.Repeat("  # c\n", 30) + "  value: leak7\n",
			"- name: DB_PASSWORD\n" + strings.Repeat("  # c\n", 30) + "  value: [hidden]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %.300s\ngot: %.300s\nwant: %.300s", tc.in, got, tc.want)
			}
		})
	}
}

func TestSortEditsJoinsOverlaps(t *testing.T) {
	got := sortEdits([]edit{{10, 12}, {5, 8}, {5, 20}, {6, 7}, {20, 22}})
	want := []edit{{5, 20}, {20, 22}}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestMaskRecordsRound2CostShapes(t *testing.T) {
	// A bare "[" array that never closes, 100000 times: every search for the "]" is charged, so the scan ends over budget and fails closed.
	for name, in := range map[string]string{
		"open arrays":             strings.Repeat(`{"value":[}`, 100000),
		"open arrays, escaped":    strings.Repeat(`{\"value\":[}`, 100000),
		"names near one value":    strings.Repeat("- name: DB_PASSWORD\n", 100000) + "  value: x\n",
		"same names, many values": strings.Repeat("- name: DB_PASSWORD\n  value: x\n", 100000),
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			out := Mask(in, rm)
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("%s: %s for %d bytes", name, d, len(in))
			}
			if len(out) > 2*len(in) {
				t.Fatalf("%s: output %d bytes for %d", name, len(out), len(in))
			}
		})
	}
	// An over-budget scan masks from where the readers stopped, not from the last span they found.
	in := strings.Repeat(`{"value":[}`, 100000) + `{"name":"DB_PASSWORD","value":"leak8"}`
	if strings.Contains(Mask(in, rm), "leak8") {
		t.Fatal("a secret after an over-budget scan survived")
	}
}

// Round 3 of the security review (SWP-1122-13).
func TestMaskRecordsRound3Findings(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"apostrophe string holds JSON",
			`payload: '{"name":"DB_PASSWORD","value":"leak1"}'`,
			`payload: '{"name":"DB_PASSWORD","value":"[hidden]"}'`},
		{"apostrophe array holds JSON",
			`args: ['{"name":"DB_PASSWORD","value":"leak2"}']`,
			`args: ['{"name":"DB_PASSWORD","value":"[hidden]"}']`},
		{"apostrophe that never closes",
			`note: 'x {"name":"DB_PASSWORD","value":"leak3"}`,
			`note: 'x {"name":"DB_PASSWORD","value":"[hidden]"}`},
		{"cut value, later value on the next line",
			"{\"name\":\"DB_PASSWORD\",\"value\":\"leak4cut\nlater line \"value\":\"other\" done",
			"{\"name\":\"DB_PASSWORD\",\"value\":\"[hidden]\nlater line \"value\":\"[hidden]\" done"},
		{"duplicate value key",
			`{"name":"DB_PASSWORD","value":"leak5","value":""}`,
			`{"name":"DB_PASSWORD","value":"[hidden]","value":""}`},
		{"odd quote earlier on the line",
			`5" screen {"name":"DB_PASSWORD","value":"leak6"}`,
			`5" screen {"name":"DB_PASSWORD","value":"[hidden]"}`},
		{"JSON inside quotes with bare quotes",
			`msg: "{"name":"DB_PASSWORD","value":"leak7"}"`,
			`msg: "{"name":"DB_PASSWORD","value":"[hidden]"}"`},
		{"bracket inside an array string",
			`{"name":"DB_PASSWORD","value":["a]","leak8"]}`,
			`{"name":"DB_PASSWORD","value":[[hidden]]}`},
		{"cut value at line end",
			"{\"name\":\"DB_PASSWORD\",\"value\":\nnext line word",
			"{\"name\":\"DB_PASSWORD\",\"value\":\nnext line word"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Mask(tc.in, rm); got != tc.want {
				t.Fatalf("\n in: %.300s\ngot: %.300s\nwant: %.300s", tc.in, got, tc.want)
			}
		})
	}
	long := strings.Repeat("z", 70000)
	for name, in := range map[string]string{
		"bare value over 64 KiB":  `{"name":"DB_PASSWORD","value":` + long + `}`,
		"array value over 64 KiB": `{"name":"DB_PASSWORD","value":["` + long + `","` + long + `"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if out := Mask(in, rm); strings.Contains(out, "zzzz") {
				t.Fatalf("%s: a tail of the value survived", name)
			}
		})
	}
	// A cut array at the end of the text masks the rest of the text.
	if out := Mask(`{"name":"DB_PASSWORD","value":["a","leak9`, rm); strings.Contains(out, "leak9") {
		t.Fatalf("cut array leaked: %s", out)
	}
	// Ordinary prose with apostrophes and quotes stays.
	for _, in := range []string{`it's fine, they said 'great' and "ok" {"name":"HOST","value":"db1"}`, `5" screen, a 6' pole`} {
		if out := Mask(in, rm); out != in {
			t.Fatalf("ordinary text changed: %s", out)
		}
	}
}

func TestMaskRecordsRound3CostShapes(t *testing.T) {
	for name, in := range map[string]string{
		"bare values":           strings.Repeat(`"value":x`, 200000),
		"open arrays with text": strings.Repeat(`{"name":"DB_PASSWORD","value":["a`+"\n", 50000),
		"apostrophes":           strings.Repeat(`a: '`, 300000),
		"apostrophe json":       strings.Repeat(`k: '{"a":1} `, 100000),
		"odd quotes and braces": strings.Repeat(`5" {"`, 200000),
	} {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_ = Mask(in, rm)
			if d := time.Since(start); d > 3*time.Second {
				t.Fatalf("%s: %s for %d bytes", name, d, len(in))
			}
		})
	}
}
