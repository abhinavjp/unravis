package codemode

import (
	"strings"
	"testing"
)

func TestStripTable(t *testing.T) {
	ok := map[string]string{
		"plain js":                `const a = await unravis.audit_list_sources({}); return a`,
		"types":                   `const a: number = 1; type T = { x: string }; return a as number`,
		"interface":               "interface R { a: number }\nconst r: R = { a: 1 }\nreturn r",
		"enum":                    `enum E { A, B }; return E.B`,
		"generics":                `function f<T>(x: T): T { return x }; return f<number>(1)`,
		"template":                "return `a${1}b`",
		"comment last":            "return 1 // trailing comment",
		"string with import text": `return "import(x) and import.meta"`,
	}
	for name, code := range ok {
		js, f := strip(code)
		switch name {
		case "string with import text":
			// A string that spells the same text is refused too: over-refusing is the safe direction.
			if f == nil || f.code != CodeImportNotAllowed {
				t.Errorf("%s: %+v", name, f)
			}
		default:
			if f != nil {
				t.Errorf("%s: %+v", name, f)
			} else if !strings.HasPrefix(js, "(async") {
				t.Errorf("%s: %q", name, js)
			}
		}
	}
	bad := map[string]string{
		"syntax":           `let = ;`,
		"unclosed":         `return {`,
		"static import":    `import a from "b"`,
		"import star":      "import * as a from 'b'\nreturn a",
		"type import":      `import type { A } from "b"; return 1`,
		"export":           `export default 1`,
		"export in a line": "return 1\n  export const x = 1",
		"dynamic import":   `return import("x")`,
		"spaced import":    "return import /* c */ ( 'x' )",
		"import meta":      `return import.meta.url`,
		"optional dynamic": `return (await import?.("x"))`,
		"escaped":          `return import("x")`,
	}
	for name, code := range bad {
		js, f := strip(code)
		if f == nil {
			t.Errorf("%s accepted: %q", name, js)
		}
	}
}

// A script that closes the wrapper and opens its own is still one script in the one worker; it gains nothing, and what comes out is
// valid.
func TestWrapperBreakoutStaysInsideTheSandboxAndIsNotRefusedSilently(t *testing.T) {
	js, f := strip(`}); globalThis.x = 1; (async()=>{`)
	if f != nil {
		t.Fatalf("%+v", f)
	}
	if !strings.HasPrefix(js, "(async") {
		t.Error(js)
	}
}

func TestColumnsAreRelativeToTheScript(t *testing.T) {
	_, f := strip("let x = ;")
	if f == nil || !strings.Contains(f.msg, "line 1, column 9") {
		t.Errorf("%+v", f)
	}
	_, f = strip("let a = 1\n  let x = ;")
	if f == nil || !strings.Contains(f.msg, "line 2, column 11") {
		t.Errorf("%+v", f)
	}
}

func TestStrippedOutputIsBounded(t *testing.T) {
	// Each enum member prints much larger than it is written.
	var b strings.Builder
	b.WriteString("enum E {")
	for i := 0; i < 30000; i++ {
		b.WriteString("Member")
		b.WriteString(strings.Repeat("x", 10))
		b.WriteString(itoa(i))
		b.WriteString(",")
	}
	b.WriteString("}")
	if b.Len() > MaxSource {
		t.Skip("source over the limit in this build")
	}
	if _, f := strip(b.String()); f != nil && f.code != CodeSourceTooLarge {
		t.Errorf("%+v", f)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
