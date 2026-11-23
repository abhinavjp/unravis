package tsgen

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func js(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func build(t *testing.T, tools ...Tool) Result {
	t.Helper()
	r, err := Generate(Spec{Namespace: "unravis", Header: []string{"test"}, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // test: fixed testdata path
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the generated text (run with -update after checking the change):\n%s", path, got)
	}
}

const fixtureIn = `{"type":"object","additionalProperties":false,"required":["ids","mode"],"properties":{
 "ids":{"type":"array","items":{"type":"string","minLength":1},"minItems":1,"maxItems":8,"description":"Connected source ids"},
 "mode":{"type":"string","enum":["a","b"]},
 "limit":{"type":"integer","minimum":1,"maximum":100},
 "when":{"type":"string","format":"date-time"},
 "range":{"type":"object","additionalProperties":false,"properties":{"min":{"type":"number"},"max":{"type":"number"}}},
 "weird-name":{"type":["string","null"]},
 "tuple":{"type":"array","prefixItems":[{"type":"string"}]},
 "pat":{"type":"object","patternProperties":{"^x":{"type":"string"}}},
 "mix":{"oneOf":[{"type":"string"},{"type":"integer"}]},
 "deep":{"oneOf":[{"oneOf":[{"type":"string"}]},{"type":"integer"}]}
}}`

const fixtureOut = `{"type":"object","additionalProperties":true,"required":["tool","partial"],
 "properties":{"tool":{"const":"x"},"partial":{"type":"boolean"},"rows":{"type":"array","items":{"$ref":"#/$defs/Row"}},
 "next":{"type":["string","null"]},"error":{"type":"object","required":["code"],"properties":{"code":{"type":"string"}}},
 "cut":{"oneOf":[{"type":"null"},{"type":"object","properties":{"n":{"type":"integer"}}}]}},
 "if":{"required":["error"]},"else":{"required":["rows"]},
 "$defs":{"Row":{"type":"object","required":["id"],"properties":{"id":{"type":"string"},"child":{"$ref":"#/$defs/Row"}}}}}`

func TestGoldenFixture(t *testing.T) {
	r := build(t, Tool{Name: "audit_x", Description: "Does x.\nSecond line.", Notes: []string{"Limit: 4 in flight."}, Input: js(t, fixtureIn), Output: js(t, fixtureOut)},
		Tool{Name: "audit_empty", Description: "No arguments.", Input: js(t, `{"type":"object","properties":{},"additionalProperties":false}`)})
	golden(t, "fixture.d.ts", r.DTS)
	if !strings.HasPrefix(r.DTS, "// schema_sha256: "+r.SchemaSHA256+"\n") || len(r.SchemaSHA256) != 64 {
		t.Errorf("hash line missing:\n%.200s", r.DTS)
	}
}

func TestDeterministicAndHashFollowsSchema(t *testing.T) {
	a := build(t, Tool{Name: "t", Input: js(t, fixtureIn), Output: js(t, fixtureOut)})
	b := build(t, Tool{Name: "t", Input: js(t, fixtureIn), Output: js(t, fixtureOut)})
	if a.DTS != b.DTS {
		t.Fatal("two runs differ")
	}
	changed := strings.Replace(fixtureIn, `"maximum":100`, `"maximum":101`, 1)
	c := build(t, Tool{Name: "t", Input: js(t, changed), Output: js(t, fixtureOut)})
	if c.SchemaSHA256 == a.SchemaSHA256 {
		t.Error("hash did not change with the schema")
	}
	d := build(t, Tool{Name: "t", Description: "other", Input: js(t, fixtureIn), Output: js(t, fixtureOut)})
	if d.SchemaSHA256 == a.SchemaSHA256 {
		t.Error("hash did not change with the description")
	}
}

func TestRecursiveSchemaTerminates(t *testing.T) {
	in := `{"type":"object","properties":{"n":{"$ref":"#/$defs/Node"}},"$defs":{
	  "Node":{"type":"object","properties":{"next":{"$ref":"#/$defs/Node"},"kids":{"type":"array","items":{"$ref":"#/$defs/Other"}}}},
	  "Other":{"type":"object","properties":{"back":{"$ref":"#/$defs/Node"}}},
	  "Loop":{"$ref":"#/$defs/Loop2"},"Loop2":{"$ref":"#/$defs/Loop"}},
	  "additionalProperties":false}`
	done := make(chan Result, 1)
	go func() { done <- build(t, Tool{Name: "t", Input: js(t, in)}) }()
	select {
	case r := <-done:
		if strings.Count(r.DTS, "type Node =") != 1 || !strings.Contains(r.DTS, "next?: Node;") {
			t.Errorf("recursive type not written once:\n%s", r.DTS)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("generator did not stop on a recursive schema")
	}
	loop := `{"type":"object","properties":{"l":{"$ref":"#/$defs/Loop"}},"$defs":{"Loop":{"$ref":"#/$defs/Loop2"},"Loop2":{"$ref":"#/$defs/Loop"}}}`
	r := build(t, Tool{Name: "t", Input: js(t, loop)})
	if strings.Contains(r.DTS, "type Loop = Loop") || !strings.Contains(r.DTS, "l?: unknown") {
		t.Errorf("a ref loop must become unknown:\n%s", r.DTS)
	}
}

func TestInlineDepthIsCapped(t *testing.T) {
	s := `{"type":"string"}`
	for i := 0; i < 40; i++ {
		s = `{"type":"array","items":` + s + `}`
	}
	r := build(t, Tool{Name: "t", Input: js(t, `{"type":"object","properties":{"a":`+s+`}}`)})
	if !strings.Contains(r.DTS, "unknown") || strings.Contains(r.DTS, "string[]") {
		t.Errorf("deep nesting not cut:\n%s", r.DTS)
	}
}

func TestUnsupportedConstructsAreUnknown(t *testing.T) {
	r := build(t, Tool{Name: "t", Input: js(t, fixtureIn)})
	for _, want := range []string{"tuple?: unknown[];", "mix?: string | number;", "deep?: unknown;", "pat?: Record<string, unknown>;", `"weird-name"?: string | null;`} {
		if !strings.Contains(r.DTS, want) {
			t.Errorf("missing %q in\n%s", want, r.DTS)
		}
	}
	r = build(t, Tool{Name: "t", Input: js(t, `{"type":"object","properties":{"x":{"$ref":"https://example.com/s.json"},"y":{"not":{"type":"string"}},"z":true,"w":false}}`)})
	for _, want := range []string{"x?: unknown;", "y?: unknown;", "z?: unknown;", "w?: never;"} {
		if !strings.Contains(r.DTS, want) {
			t.Errorf("missing %q in\n%s", want, r.DTS)
		}
	}
}

func TestFailureAndSuccessForms(t *testing.T) {
	r := build(t, Tool{Name: "audit_x", Input: js(t, fixtureIn), Output: js(t, fixtureOut)})
	for _, want := range []string{
		"type AuditXOutput = AuditXOutputFailure | AuditXOutputSuccess;",
		`Required<Pick<AuditXOutputBase, "error">>`,
		`Required<Pick<AuditXOutputBase, "rows">>`,
		"error?: never",
		"[key: string]: unknown;",
		"tool: \"x\";",
	} {
		if !strings.Contains(r.DTS, want) {
			t.Errorf("missing %q in\n%s", want, r.DTS)
		}
	}
}

// A schema is data from outside the file: nothing in it may end a comment, start a declaration or break a line.
func TestHostileTextCannotEscape(t *testing.T) {
	evil := "x */ declare const pwned: 1; /* \u2028 // \r\n  \u202e end"
	in := `{"type":"object","properties":{` +
		string(mustJSON(evil)) + `:{"type":"string","description":` + string(mustJSON(evil)) + `,"pattern":` + string(mustJSON(evil)) + `,"enum":[` + string(mustJSON(evil)) + `]},
		"ok":{"enum":[` + string(mustJSON("a\"; declare const pwned2: 1; \"")) + `]}}}`
	r := build(t, Tool{Name: "audit_x*/ declare const pwned3: 1; /*", Description: evil, Notes: []string{evil}, Input: js(t, in)})
	for _, line := range strings.Split(r.DTS, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "declare const") || strings.Contains(trimmed, "pwned3") && !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "function") {
			t.Errorf("injected declaration: %q", line)
		}
	}
	if strings.Contains(r.DTS, "\u2028") || strings.Contains(r.DTS, "\u202e") || strings.Contains(r.DTS, "\r") {
		t.Error("a line separator or bidi control survived")
	}
	re := regexp.MustCompile(`(?s)/\*\*.*?\*/`)
	// Take away the doc blocks and the string literals: what is left is code, and it must hold the one namespace declaration and nothing
	// the schema text could have injected.
	code := regexp.MustCompile(`"(\\.|[^"\\])*"`).ReplaceAllString(re.ReplaceAllString(r.DTS, ""), `""`)
	if strings.Count(code, "declare ") != 1 || strings.Contains(code, "pwned: 1") || strings.Contains(code, "/*") || strings.Contains(code, "*/") {
		t.Errorf("injected code outside a comment or string:\n%s", code)
	}
	if !regexp.MustCompile(`function audit_x_+declare_const_pwned3__1_+\(`).MatchString(r.DTS) {
		t.Errorf("tool name was not sanitized:\n%s", r.DTS)
	}
	if strings.Contains(r.DTS, `"a"; declare const pwned2`) {
		t.Error("enum value broke out of its string")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestNamesAreSanitizedAndCollisionsRefused(t *testing.T) {
	r := build(t, Tool{Name: "delete", Input: js(t, `{}`)}, Tool{Name: "9lives", Input: js(t, `{}`)}, Tool{Name: "a-b.c", Input: js(t, `{}`)})
	for _, want := range []string{"function _delete(", "function _9lives(", "function a_b_c("} {
		if !strings.Contains(r.DTS, want) {
			t.Errorf("missing %q in\n%s", want, r.DTS)
		}
	}
	for _, c := range [][]Tool{
		{{Name: "a-b", Input: js(t, `{}`)}, {Name: "a_b", Input: js(t, `{}`)}},
		{{Name: "a_b", Input: js(t, `{}`)}, {Name: "a_b", Input: js(t, `{}`)}},
	} {
		if _, err := Generate(Spec{Namespace: "unravis", Tools: c}); err == nil {
			t.Errorf("collision %v was accepted", c)
		}
	}
	if _, err := Generate(Spec{Namespace: "bad name", Tools: []Tool{{Name: "a"}}}); err == nil {
		t.Error("bad namespace accepted")
	}
	if _, err := Generate(Spec{Namespace: "unravis"}); err == nil {
		t.Error("no tools accepted")
	}
}

func TestSameDefinitionNameWithOtherSchemaIsRefused(t *testing.T) {
	mk := func(typ string) Tool {
		return Tool{Name: "t" + typ, Input: js(t, `{"type":"object","properties":{"a":{"$ref":"#/$defs/Row"}},"$defs":{"Row":{"type":"`+typ+`"}}}`)}
	}
	if _, err := Generate(Spec{Namespace: "unravis", Tools: []Tool{mk("string"), mk("number")}}); err == nil {
		t.Error("two schemas under one definition name were accepted")
	}
	r, err := Generate(Spec{Namespace: "unravis", Tools: []Tool{mk("string"), {Name: "tstring2", Input: mk("string").Input}}})
	if err != nil || strings.Count(r.DTS, "type Row =") != 1 {
		t.Errorf("same schema must be shared: %v\n%s", err, r.DTS)
	}
}

func TestOptionalInput(t *testing.T) {
	r := build(t, Tool{Name: "a", Input: js(t, `{"type":"object","properties":{"x":{"type":"string"}}}`)}, Tool{Name: "b", Input: js(t, `{"type":"object","required":["x"],"properties":{"x":{"type":"string"}}}`)})
	if !strings.Contains(r.DTS, "function a(input?: AInput)") || !strings.Contains(r.DTS, "function b(input: BInput)") {
		t.Errorf("optional input wrong:\n%s", r.DTS)
	}
}

// A definition named like a built-in type would change what Promise<...> or Record<...> mean for the whole file.
func TestBuiltinTypeNamesAreRefused(t *testing.T) {
	for _, name := range []string{"Promise", "Record", "Required", "Pick", "Array"} {
		in := js(t, `{"type":"object","properties":{"a":{"$ref":"#/$defs/`+name+`"}},"$defs":{"`+name+`":{"type":"string"}}}`)
		if _, err := Generate(Spec{Namespace: "unravis", Tools: []Tool{{Name: "t", Input: in}}}); err == nil {
			t.Errorf("a definition called %s was accepted", name)
		}
	}
	// A tool called promise gives the type PromiseInput, which is fine; a tool whose name makes the type Promise itself is not possible
	// (the suffix), but a variant body must not collide with a definition either.
	in := js(t, `{"type":"object","properties":{"a":{"$ref":"#/$defs/TOutputBase"}},"$defs":{"TOutputBase":{"type":"string"}}}`)
	if _, err := Generate(Spec{Namespace: "unravis", Tools: []Tool{{Name: "t", Input: js(t, `{}`), Output: map[string]any{"type": "object", "properties": in["properties"], "$defs": in["$defs"],
		"if": map[string]any{"required": []any{"a"}}, "else": map[string]any{"required": []any{"a"}}}}}}); err == nil {
		t.Error("a definition sharing the name of a variant type was accepted")
	}
}
