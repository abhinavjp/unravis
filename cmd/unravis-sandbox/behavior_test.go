package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"api-audit-explorer/internal/codemode/wire"
)

// bigTicks keeps the tick limit out of the way of tests that loop on purpose.
func bigTicks() wire.Limits {
	l := testLimits()
	l.Ticks = 2000000
	return l
}

// Every limit ends the run with its own code, and the worker itself survives (exit 0, nothing on stderr).
func TestLimitsDistinctCodes(t *testing.T) {
	small := testLimits()
	small.Ticks = 2000
	tightMem := testLimits()
	tightMem.MemPages = 192
	tightMem.HeapBytes = 256 << 20 // far more than the 12 MiB memory limit leaves: the worker clamps it
	tightMem.Ticks = 2000000
	huge := bigTicks()
	huge.CallBytes = 1 << 20
	// tiny keeps the memory bombs quick: the interpreter is slow, so a bomb that fills 8 MiB of heap takes seconds.
	tiny := bigTicks()
	tiny.MemPages = 192
	tiny.HeapBytes = 2 << 20
	// regexp backtracking polls the tick counter every 10000 steps, so a tick is far more work there than anywhere else.
	few := testLimits()
	few.Ticks = 40

	cases := []struct {
		name   string
		js     string
		limits wire.Limits
		code   string
	}{
		{"memory: arrays", `const a = []; while (true) a.push(new Array(1000).fill(1));`, tiny, "oom"},
		{"memory: string doubling, flattened", `let s = "x"; for (let i = 0; i < 26; i++) s += s; return s.charCodeAt(5);`, tiny, "oom"},
		{"memory: string doubling stays a rope until the length limit", `let s = "x"; while (true) s += s;`, tiny, "script_error"},
		{"memory: one huge ArrayBuffer", `new ArrayBuffer(1 << 30); return 1;`, bigTicks(), "oom"},
		{"memory: many ArrayBuffers", `const a = []; while (true) a.push(new ArrayBuffer(1 << 20));`, tiny, "oom"},
		{"memory: typed array", `return new Float64Array(1 << 26).length;`, bigTicks(), "oom"},
		{"memory: Array(2^32-1).fill", `return new Array(4294967295).fill(0).length;`, bigTicks(), "oom"},
		{"memory: repeat", `return "x".repeat(1 << 29).length;`, bigTicks(), "oom"},
		{"memory: objects in a Map", `const m = new Map(); for (let i = 0; ; i++) m.set(i, {i});`, tiny, "oom"},
		{"memory: heap above what the memory limit leaves", `const a = []; while (true) a.push(new Array(1000).fill(1));`, tightMem, "oom"},
		{"stack: plain recursion", `function f(n) { return f(n + 1) + 1 } return f(0);`, bigTicks(), "stack"},
		{"stack: method recursion", `const o = { f(n) { return this.f(n + 1) } }; return o.f(0);`, bigTicks(), "stack"},
		{"stack: native recursion in JSON.stringify", `let a = []; for (let i = 0; i < 100000; i++) a = [a]; return JSON.stringify(a).length;`, bigTicks(), "stack"},
		{"stack: native recursion in JSON.parse", `return JSON.parse("[".repeat(100000)).length;`, bigTicks(), "stack"},
		{"stack: native recursion in flat", `let a = []; for (let i = 0; i < 100000; i++) a = [a]; return a.flat(Infinity).length;`, bigTicks(), "stack"},
		{"stack: recursion in a promise job", `await 0; function f(n) { return f(n + 1) + 1 } return f(0);`, bigTicks(), "stack"},
		{"ticks: empty loop", `while (true) {}`, small, "ticks"},
		{"ticks: loop after an await", `await 0; for (;;) {}`, small, "ticks"},
		{"ticks: a catch cannot stop it", `while (true) { try { for (;;) {} } catch (e) {} }`, small, "ticks"},
		{"ticks: recursion that is caught again and again", `function f(n) { return f(n + 1) + 1 } while (true) { try { f(0) } catch (e) {} }`, small, "ticks"},
		{"ticks: regexp backtracking", `return /(a+)+$/.test("a".repeat(40) + "!");`, few, "ticks"},
		{"ticks: a promise loop", `async function f() { for (;;) await 0 } return f();`, small, "ticks"},
		{"output: 2 MiB string", `return "x".repeat(2 << 20);`, huge, "output_too_large"},
		{"output: large array", `return new Array(100000).fill("abcdefghijk");`, huge, "output_too_large"},
		{"script: throw", `throw new TypeError("nope");`, bigTicks(), "script_error"},
		{"script: throw a string", `throw "plain";`, bigTicks(), "script_error"},
		{"script: unhandled rejection", `await Promise.reject(new Error("later"));`, bigTicks(), "script_error"},
		{"script: syntax error", `}{`, bigTicks(), "script_error"},
		{"script: a BigInt result", `return 10n;`, bigTicks(), "script_error"},
		{"script: a cyclic result", `const a = {}; a.a = a; return a;`, bigTicks(), "script_error"},
		{"script: caught stack overflow that is thrown again", `function f(n) { return f(n + 1) + 1 } try { f(0) } catch (e) { throw new Error("mine") }`, bigTicks(), "script_error"},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := runScenario(t, scenario{js: tc.js, limits: tc.limits})
			o.wantFail(t, tc.code)
			if o.done.Error.Message == "" {
				t.Fatalf("no message in %+v", o.done.Error)
			}
			if strings.Contains(string(o.doneRaw), "wasm") || strings.Contains(string(o.doneRaw), "goroutine") {
				t.Fatalf("host detail in the done frame: %s", o.doneRaw)
			}
		})
		seen[tc.code] = tc.name
	}
	if len(seen) != 5 {
		t.Fatalf("the cases cover %d codes, want 5: %v", len(seen), seen)
	}

	t.Run("log cap: the run goes on and the logs say they were cut", func(t *testing.T) {
		l := bigTicks()
		l.LogBytes = 1000
		o := runScenario(t, scenario{limits: l, js: `for (let i = 0; i < 5000; i++) console.log("line " + i + " " + "x".repeat(50)); return "done";`})
		o.wantClean(t)
		if !o.done.OK || !o.done.LogsTruncated {
			t.Fatalf("done %+v", o.done)
		}
		total := 0
		for _, l := range o.done.Logs {
			total += len(l) + 1
		}
		if total > 1000 || len(o.done.Logs) == 0 || o.done.Logs[0] != "line 0 "+strings.Repeat("x", 50) {
			t.Fatalf("%d bytes in %d lines", total, len(o.done.Logs))
		}
	})
	t.Run("log cap: line count and line length", func(t *testing.T) {
		l := bigTicks()
		l.LogBytes = 1 << 20
		o := runScenario(t, scenario{limits: l, js: `for (let i = 0; i < 5000; i++) console.log("a"); console.log("L".repeat(100000)); return 1;`})
		o.wantClean(t)
		if !o.done.OK || !o.done.LogsTruncated || len(o.done.Logs) > maxLogLines {
			t.Fatalf("lines %d truncated %v", len(o.done.Logs), o.done.LogsTruncated)
		}
	})
	t.Run("log: a long line is cut and marked", func(t *testing.T) {
		l := bigTicks()
		l.LogBytes = 1 << 20
		o := runScenario(t, scenario{limits: l, js: `console.log("L".repeat(100000)); return 1;`})
		o.wantClean(t)
		if !o.done.OK || !o.done.LogsTruncated || len(o.done.Logs) != 1 || len(o.done.Logs[0]) > maxLineBytes {
			t.Fatalf("done %d logs", len(o.done.Logs))
		}
	})
	t.Run("log: formats and never throws", func(t *testing.T) {
		o := runScenario(t, scenario{js: `
			console.log("a", 1, {b: 2}, [3], null, undefined, 4n, Symbol("s"));
			const c = {}; c.c = c; console.log(c);
			console.log({ toString() { throw new Error("x") } });
			console.info("i"); console.warn("w"); console.error("e"); console.debug("d");
			return 1;`})
		o.wantClean(t)
		if !o.done.OK || len(o.done.Logs) != 7 || !strings.HasPrefix(o.done.Logs[0], "a 1 ") {
			t.Fatalf("done %+v", o.done)
		}
	})
}

// Date.now and performance.now move in steps of 10 ms from the base; frozen never moves.
func TestClockQuantized10ms(t *testing.T) {
	const js = `
		const d = [], p = [], g = [];
		for (let i = 0; i < 6000; i++) { d.push(Date.now()); p.push(performance.now()); if (i % 300 == 0) g.push(new Date().getTime(), new Date().getMilliseconds()); }
		return { d: [...new Set(d)], p: [...new Set(p)], g, off: new Date().getTimezoneOffset() };`
	type res struct {
		D, P []float64
		G    []float64
	}
	t.Run("live", func(t *testing.T) {
		o := runScenario(t, scenario{js: js, limits: bigTicks(), clock: wire.Clock{Mode: "live", BaseMs: 1700000000007}})
		var r res
		o.resultJSON(t, &r)
		o.wantClean(t)
		if len(r.D) < 2 || len(r.P) < 2 {
			t.Fatalf("the clock never moved: %d and %d values", len(r.D), len(r.P))
		}
		for i, v := range r.D {
			if int64(v)%10 != 0 || v < 1700000000000 || (i > 0 && v <= r.D[i-1]) {
				t.Fatalf("Date.now values %v", r.D)
			}
		}
		for i, v := range r.P {
			if int64(v)%10 != 0 || v < 0 || (i > 0 && v <= r.P[i-1]) {
				t.Fatalf("performance.now values %v", r.P)
			}
		}
		if r.D[0] < 1700000000000 || r.D[0] > 1700000000000+30000 { // the base 1700000000007 floors to 1700000000000; the run starts a moment later
			t.Fatalf("first Date.now %v", r.D[0])
		}
		for _, v := range r.G {
			if int64(v)%10 != 0 && int64(v) > 1000 {
				t.Fatalf("new Date() is not quantized: %v", r.G)
			}
		}
	})
	t.Run("frozen", func(t *testing.T) {
		o := runScenario(t, scenario{js: js, limits: bigTicks(), clock: wire.Clock{Mode: "frozen", BaseMs: 1700000000000}})
		var r res
		o.resultJSON(t, &r)
		if len(r.D) != 1 || r.D[0] != 1700000000000 || len(r.P) != 1 || r.P[0] != 0 {
			t.Fatalf("frozen clock gave %v and %v", r.D, r.P)
		}
	})
}

// Math.random is the run's own generator, seeded from the seed of the run frame.
func TestMathRandomSeeded(t *testing.T) {
	const js = `return [Math.random(), Math.random(), Math.random(), Math.random()];`
	get := func(seed string) []float64 {
		o := runScenario(t, scenario{js: js, seed: seed})
		var v []float64
		o.resultJSON(t, &v)
		o.wantClean(t)
		return v
	}
	a1, a2 := get("00112233445566778899aabbccddeeff"), get("00112233445566778899aabbccddeeff")
	b := get("ffeeddccbbaa99887766554433221100")
	if fmt.Sprint(a1) != fmt.Sprint(a2) {
		t.Fatalf("the same seed gave %v and %v", a1, a2)
	}
	if fmt.Sprint(a1) == fmt.Sprint(b) {
		t.Fatalf("different seeds gave the same numbers %v", a1)
	}
	seen := map[float64]bool{}
	for _, v := range append(a1, b...) {
		if v < 0 || v >= 1 {
			t.Fatalf("out of range: %v", v)
		}
		seen[v] = true
	}
	if len(seen) != 8 {
		t.Fatalf("repeated values: %v %v", a1, b)
	}
}

// The globals of a script are the JavaScript language, console, performance and unravis; nothing that reaches the machine or builds code.
func TestGlobalsRemoved(t *testing.T) {
	o := runScenario(t, scenario{js: `return Object.getOwnPropertyNames(globalThis).sort();`})
	var names []string
	o.resultJSON(t, &names)
	want := []string{"AggregateError", "Array", "ArrayBuffer", "AsyncDisposableStack", "BigInt", "BigInt64Array", "BigUint64Array", "Boolean", "DataView", "Date",
		"DisposableStack", "Error", "EvalError", "Float16Array", "Float32Array", "Float64Array", "Infinity", "Int16Array", "Int32Array", "Int8Array",
		"InternalError", "Iterator", "JSON", "Map", "Math", "NaN", "Number", "Object", "Promise", "Proxy", "RangeError", "ReferenceError", "Reflect", "RegExp",
		"Set", "String", "SuppressedError", "Symbol", "SyntaxError", "TypeError", "URIError", "Uint16Array", "Uint32Array", "Uint8Array", "Uint8ClampedArray",
		"WeakMap", "WeakSet", "console", "decodeURI", "decodeURIComponent", "encodeURI", "encodeURIComponent", "escape", "globalThis", "isFinite", "isNaN",
		"parseFloat", "parseInt", "performance", "queueMicrotask", "undefined", "unescape", "unravis"}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("globals changed:\n got %v\nwant %v", names, want)
	}

	o = runScenario(t, scenario{js: `
		const gone = ["eval", "Function", "AsyncFunction", "GeneratorFunction", "SharedArrayBuffer", "Atomics", "WebAssembly", "fetch", "XMLHttpRequest", "WebSocket",
			"std", "os", "scriptArgs", "process", "require", "module", "exports", "Buffer", "Deno", "Bun", "navigator", "window", "self", "global", "setTimeout",
			"setInterval", "setImmediate", "clearTimeout", "structuredClone", "WeakRef", "FinalizationRegistry", "TextEncoder", "TextDecoder", "URL", "Intl", "crypto",
			"importScripts", "postMessage", "Worker", "SharedWorker", "localStorage", "indexedDB", "print", "loadScript", "gc", "evalScript", "__filename", "__dirname"];
		return gone.filter(n => typeof globalThis[n] !== "undefined");`})
	var present []string
	o.resultJSON(t, &present)
	if len(present) != 0 {
		t.Fatalf("still reachable: %v", present)
	}

	o = runScenario(t, scenario{js: `
		const out = {};
		for (const n of ["eval", "Function", "SharedArrayBuffer", "Atomics", "WebAssembly", "process", "require", "fetch", "std", "os"]) {
			try { new Function; } catch (e) {}
			try { out[n] = typeof eval(n); } catch (e) { out[n] = e.name; }
		}
		return out;`})
	var m map[string]string
	o.resultJSON(t, &m)
	for n, v := range m {
		if v != "ReferenceError" {
			t.Fatalf("%s: %q", n, v)
		}
	}
}

// Escape attempts: every route to code from a string, a module, a host object or a prototype is closed. Each attempt must fail the way
// a missing feature fails, and nothing may come back that shows it worked.
func TestEscapeAttempts(t *testing.T) {
	t.Setenv("UNRAVIS_SANDBOX_CANARY", "canary-5c1f0e77")
	attempts := map[string]string{
		"import()":                                     `await import("x")`,
		"import() of a data url":                       `await import("data:text/javascript,export default 1")`,
		"import() of a relative file":                  `await import("./qjs.wasm")`,
		"import() of a built-in":                       `await import("std")`,
		"import() of os":                               `await import("os")`,
		"import.meta":                                  `import.meta.url`,
		"direct eval":                                  `eval("1+1")`,
		"indirect eval":                                `(0, eval)("1+1")`,
		"eval from globalThis":                         `globalThis.eval("1")`,
		"eval from a string name":                      `globalThis["ev" + "al"]("1")`,
		"new Function":                                 `new Function("return 1")()`,
		"Function(...)":                                `Function("return 1")()`,
		"function constructor":                         `(function () {}).constructor("return 1")()`,
		"arrow constructor":                            `(() => 1).constructor("return 1")()`,
		"method constructor":                           `({ f() {} }).f.constructor("return 1")()`,
		"class constructor chain":                      `(class {}).constructor("return 1")()`,
		"Function.prototype.constructor":               `Function.prototype.constructor("return 1")()`,
		"Object.constructor":                           `Object.constructor("return 1")()`,
		"Array.constructor":                            `[].constructor.constructor("return 1")()`,
		"globalThis.constructor":                       `globalThis.constructor.constructor("return 1")()`,
		"this.constructor":                             `this.constructor.constructor("return 1")()`,
		"string constructor":                           `"".constructor.constructor("return 1")()`,
		"getPrototypeOf(Object)":                       `Object.getPrototypeOf(Object).constructor("return 1")()`,
		"__proto__ walk":                               `Object.__proto__.constructor("return 1")()`,
		"AsyncFunction constructor":                    `Object.getPrototypeOf(async function () {}).constructor("return 1")()`,
		"AsyncFunction via instance":                   `(async function () {}).constructor("return 1")()`,
		"GeneratorFunction constructor":                `Object.getPrototypeOf(function* () {}).constructor("return 1")().next()`,
		"AsyncGeneratorFunction constructor":           `Object.getPrototypeOf(async function* () {}).constructor("return 1")()`,
		"generator via instance":                       `(function* () {}).constructor("yield 1")().next()`,
		"constructor by getOwnPropertyDescriptor":      `Object.getOwnPropertyDescriptor(Function.prototype, "constructor").value("return 1")()`,
		"constructor by Reflect.get":                   `Reflect.get(Function.prototype, "constructor")("return 1")()`,
		"Reflect.construct on a prototype constructor": `Reflect.construct(Object.getPrototypeOf(function () {}).constructor, ["return 1"])()`,
		"constructor from a bound function":            `(function () {}).bind().constructor("return 1")()`,
		"constructor from a Proxy":                     `new Proxy(function () {}, {}).constructor("return 1")()`,
		"setTimeout with a string":                     `setTimeout("1", 0)`,
		"require":                                      `require("fs")`,
		"process.env":                                  `process.env.UNRAVIS_SANDBOX_CANARY`,
		"process.exit":                                 `process.exit(0)`,
		"std.getenv":                                   `std.getenv("UNRAVIS_SANDBOX_CANARY")`,
		"os.exec":                                      `os.exec(["cmd"])`,
		"scriptArgs":                                   `scriptArgs[0]`,
		"fs":                                           `fs.readFileSync("x")`,
		"net":                                          `net.connect(1)`,
		"fetch":                                        `await fetch("http://127.0.0.1:1/")`,
		"XMLHttpRequest":                               `new XMLHttpRequest()`,
		"WebSocket":                                    `new WebSocket("ws://127.0.0.1:1")`,
		"WebAssembly.instantiate":                      `await WebAssembly.instantiate(new Uint8Array(8))`,
		"SharedArrayBuffer":                            `new SharedArrayBuffer(8)`,
		"Atomics":                                      `Atomics.wait(new Int32Array(1), 0, 0, 1)`,
		"Intl":                                         `new Intl.DateTimeFormat()`,
		"performance.timeOrigin":                       `performance.timeOrigin.x.y`,
		"unravis.eval":                                 `unravis.eval("1")`,
		"unravis.constructor":                          `unravis.constructor.constructor("return 1")()`,
		"tool constructor":                             `unravis.audit_compare.constructor("return 1")()`,
		"console constructor":                          `console.log.constructor("return 1")()`,
		"performance.now constructor":                  `performance.now.constructor("return 1")()`,
		"Math.random constructor":                      `Math.random.constructor("return 1")()`,
		"promise executor constructor":                 `new Promise(function () {}).constructor.constructor("return 1")()`,
		"error stack constructor":                      `new Error().constructor.constructor("return 1")()`,
		"Symbol.species trick":                         `Array.prototype.constructor[Symbol.species].constructor("return 1")()`,
	}
	names := make([]string, 0, len(attempts))
	for n := range attempts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		// 1: the attempt runs in the script and its error is caught: it must be a thrown error, never a value.
		// 2: the same attempt uncaught ends the run as script_error, the worker exits 0 and says nothing on stderr.
		src := attempts[n]
		t.Run(n, func(t *testing.T) {
			o := runScenario(t, scenario{js: `try { const v = ` + wrapExpr(src) + `; return { escaped: true, v: typeof v + ":" + String(v) } } catch (e) { return { escaped: false, name: e && e.name } }`})
			o.wantClean(t)
			var r struct {
				Escaped bool
				V       string
				Name    string
			}
			if !o.done.OK {
				// A syntax error of the attempt itself ends the run as script_error, which is also a refusal.
				if o.done.Error.Code != "script_error" {
					t.Fatalf("unexpected end %+v", o.done.Error)
				}
				return
			}
			o.resultJSON(t, &r)
			if r.Escaped {
				t.Fatalf("the attempt worked: %s", o.doneRaw)
			}
			if strings.Contains(string(o.doneRaw), "canary-5c1f0e77") {
				t.Fatalf("the environment leaked: %s", o.doneRaw)
			}
		})
	}
}

// wrapExpr makes an attempt that may use await or be a statement into one expression.
func wrapExpr(src string) string {
	if strings.Contains(src, "await ") {
		return "(await (async () => (" + strings.TrimPrefix(src, "await ") + "))())"
	}
	return "(" + src + ")"
}

// A stronger end-to-end check of the same: the static forms that are syntax errors in a script, and the dynamic import in a position
// where it is not caught.
func TestStaticModuleSyntaxRefused(t *testing.T) {
	for _, js := range []string{`import x from "y"; return 1`, `export const a = 1; return 1`, `return import.meta`} {
		o := runScenario(t, scenario{js: js})
		o.wantFail(t, "script_error")
	}
	o := runScenario(t, scenario{js: `await import("anything"); return "imported"`})
	o.wantFail(t, "script_error")
}

// unravis is frozen and cannot be replaced; the tool functions cannot be changed or extended.
func TestUnravisFrozen(t *testing.T) {
	o := runScenario(t, scenario{js: `
		"use strict";
		const out = {};
		const tryit = (name, f) => { try { f(); out[name] = "allowed" } catch (e) { out[name] = e.name } };
		const before = unravis.audit_compare;
		tryit("overwrite", () => { unravis.audit_compare = () => 1 });
		tryit("add", () => { unravis.extra = 1 });
		tryit("delete", () => { delete unravis.audit_compare });
		tryit("defineProperty", () => { Object.defineProperty(unravis, "audit_compare", { value: 1 }) });
		tryit("setPrototypeOf", () => { Object.setPrototypeOf(unravis, { x: 1 }) });
		tryit("proto", () => { unravis.__proto__ = { x: 1 } });
		tryit("replace the global", () => { unravis = {} });
		tryit("delete the global", () => { delete globalThis.unravis });
		tryit("redefine the global", () => { Object.defineProperty(globalThis, "unravis", { value: {} }) });
		tryit("tool property", () => { unravis.audit_compare.x = 1 });
		tryit("tool prototype", () => { Object.setPrototypeOf(unravis.audit_compare, null) });
		tryit("freeze console", () => { console.log = () => 1 });
		tryit("freeze console 2", () => { console.extra = 1 });
		tryit("performance.now", () => { performance.now = () => 0 });
		out.same = unravis.audit_compare === before;
		out.frozen = Object.isFrozen(unravis);
		out.keys = Object.keys(unravis).length;
		out.proto = Object.getPrototypeOf(unravis) === Object.prototype || Object.getPrototypeOf(unravis) === null;
		return out;`})
	var m map[string]any
	o.resultJSON(t, &m)
	for k, v := range m {
		switch k {
		case "same", "frozen", "proto":
			if v != true {
				t.Errorf("%s: %v", k, v)
			}
		case "keys":
			if v != float64(len(allTools)) {
				t.Errorf("keys %v", v)
			}
		default:
			if v == "allowed" {
				t.Errorf("%s was allowed", k)
			}
		}
	}

	// Only the tools in the run frame exist.
	o = runScenario(t, scenario{tools: []string{"audit_compare", "audit_get_guide"}, js: `return [Object.keys(unravis), typeof unravis.audit_list_sources];`})
	var r []any
	o.resultJSON(t, &r)
	if fmt.Sprint(r) != "[[audit_compare audit_get_guide] undefined]" {
		t.Fatalf("tools %v", r)
	}
}

// A script that pollutes the built-in prototypes cannot change what the host receives for a call it did not shape, nor what a result
// from the host contains; "__proto__" in an answer is data.
func TestPrototypePollution(t *testing.T) {
	answers := map[string]answer{
		"audit_get_guide": func(json.RawMessage) (any, *wire.RetError) {
			return json.RawMessage(`{"__proto__":{"admin":true},"constructor":{"prototype":{"owned":true}},"v":1}`), nil
		},
	}
	o := runScenario(t, scenario{answers: answers, js: `
		Object.prototype.polluted = "yes";
		Object.defineProperty(Object.prototype, "hidden", { value: "yes", enumerable: false, configurable: true });
		Array.prototype.polluted = "yes";
		String.prototype.polluted = "yes";
		Object.prototype.then = undefined;
		const r = await unravis.audit_get_guide({});
		const plain = await unravis.audit_compare({ a: 1, list: [1, 2], s: "x" });
		const emptyArgs = await unravis.audit_list_sources({});
		const proto = JSON.parse('{"__proto__":{"x":1},"k":2}');
		const echoed = await unravis.audit_get_requests(proto);
		return {
			resultKeys: Object.keys(r), ownProto: Object.prototype.hasOwnProperty.call(r, "__proto__"), admin: ({}).admin, owned: ({}).owned,
			v: r.v, plain: plain, emptyArgs: emptyArgs, echoedKeys: Object.keys(echoed.echo),
		};`})
	var res struct {
		ResultKeys []string
		OwnProto   bool
		Admin      any
		Owned      any
		V          int
		Plain      map[string]json.RawMessage
		EmptyArgs  map[string]json.RawMessage
		EchoedKeys []string
	}
	o.resultJSON(t, &res)
	if !res.OwnProto || res.Admin != nil || res.Owned != nil || res.V != 1 || fmt.Sprint(res.ResultKeys) != "[__proto__ constructor v]" {
		t.Fatalf("result %+v", res)
	}
	if len(o.calls) != 4 {
		t.Fatalf("%d calls", len(o.calls))
	}
	if string(o.calls[0].Args) != `{}` || string(o.calls[1].Args) != `{"a":1,"list":[1,2],"s":"x"}` || string(o.calls[2].Args) != `{}` {
		t.Fatalf("args reached the host changed: %s | %s | %s", o.calls[0].Args, o.calls[1].Args, o.calls[2].Args)
	}
	if string(o.calls[3].Args) != `{"__proto__":{"x":1},"k":2}` {
		t.Fatalf("__proto__ as data was changed: %s", o.calls[3].Args)
	}
	if len(res.Plain) != 1 || len(res.EmptyArgs) != 1 || fmt.Sprint(res.EchoedKeys) != "[__proto__ k]" {
		t.Fatalf("results %+v", res)
	}
}

// Calls: the budget, the in-flight limit with a FIFO queue, and what is refused before it leaves the worker.
func TestCallQueue(t *testing.T) {
	t.Run("51 calls: at most 4 in flight, in order, the 51st is the broker's to deny, the 52nd is refused here", func(t *testing.T) {
		answers := map[string]answer{
			"audit_compare": func(args json.RawMessage) (any, *wire.RetError) {
				var a struct{ I int }
				_ = json.Unmarshal(args, &a)
				if a.I >= 50 {
					return nil, &wire.RetError{Code: "call_budget", Message: "too many calls"}
				}
				return map[string]int{"i": a.I}, nil
			},
		}
		o := runScenario(t, scenario{hold: 4, answers: answers, limits: bigTicks(), js: `
			const ps = [];
			for (let i = 0; i < 52; i++) ps.push(unravis.audit_compare({ i }));
			const r = await Promise.allSettled(ps);
			return r.map(x => x.status === "fulfilled" ? x.value.i : "rejected:" + (x.reason && x.reason.message));`})
		o.wantClean(t)
		if !o.done.OK {
			t.Fatalf("%+v", o.done.Error)
		}
		if o.maxInFlight != 4 {
			t.Fatalf("max in flight %d, want 4", o.maxInFlight)
		}
		if len(o.calls) != 51 {
			t.Fatalf("%d calls reached the broker, want 51 (50 and the one it denies)", len(o.calls))
		}
		for i, c := range o.calls {
			if c.ID != int64(i+1) || c.Tool != "audit_compare" || string(c.Args) != fmt.Sprintf(`{"i":%d}`, i) {
				t.Fatalf("call %d out of order: %+v %s", i, c, c.Args)
			}
		}
		var r []any
		o.resultJSON(t, &r)
		if len(r) != 52 {
			t.Fatalf("%d outcomes", len(r))
		}
		for i := 0; i < 50; i++ {
			if r[i] != float64(i) {
				t.Fatalf("outcome %d: %v", i, r[i])
			}
		}
		for _, i := range []int{50, 51} {
			if s, _ := r[i].(string); !strings.HasPrefix(s, "rejected:") {
				t.Fatalf("outcome %d: %v", i, r[i])
			}
		}
	})
	t.Run("a tool error rejects with the code and message", func(t *testing.T) {
		answers := map[string]answer{"audit_compare": func(json.RawMessage) (any, *wire.RetError) {
			return nil, &wire.RetError{Code: "denied", Message: "not allowed here"}
		}}
		o := runScenario(t, scenario{answers: answers, js: `try { await unravis.audit_compare({}); return "no" } catch (e) { return [e instanceof Error, e.code, e.message] }`})
		var r []any
		o.resultJSON(t, &r)
		if fmt.Sprint(r) != "[true denied not allowed here]" {
			t.Fatalf("%v", r)
		}
		o.wantClean(t)
	})
	t.Run("arguments that cannot be sent are refused before a frame is written", func(t *testing.T) {
		l := bigTicks()
		l.CallBytes = 2000
		o := runScenario(t, scenario{limits: l, js: `
			const out = [];
			const tryit = async (v) => { try { await unravis.audit_compare(v); out.push("sent") } catch (e) { out.push("refused") } };
			const cyc = {}; cyc.c = cyc;
			await tryit(5); await tryit("s"); await tryit([1]); await tryit(null); await tryit(cyc); await tryit({ n: 10n });
			await tryit({ big: "x".repeat(5000) }); await tryit(() => 1); await tryit({ toJSON() { throw new Error("x") } });
			return out;`})
		o.wantClean(t)
		var r []string
		o.resultJSON(t, &r)
		for i, v := range r {
			if v != "refused" {
				t.Errorf("argument %d was %s", i, v)
			}
		}
		if len(o.calls) != 0 {
			t.Fatalf("%d calls were sent", len(o.calls))
		}
	})
	t.Run("no argument is an empty object", func(t *testing.T) {
		o := runScenario(t, scenario{js: `await unravis.audit_list_sources(); await unravis.audit_list_sources(undefined); return 1;`})
		o.wantClean(t)
		if len(o.calls) != 2 || string(o.calls[0].Args) != `{}` || string(o.calls[1].Args) != `{}` {
			t.Fatalf("calls %+v", o.calls)
		}
	})
	t.Run("a call that is never awaited does not hold the run", func(t *testing.T) {
		o := runScenario(t, scenario{js: `unravis.audit_compare({}); return "left";`})
		o.wantClean(t)
		if !o.done.OK {
			t.Fatalf("%+v", o.done.Error)
		}
	})
	t.Run("a script that waits for something that cannot happen ends", func(t *testing.T) {
		o := runScenario(t, scenario{js: `await new Promise(() => {}); return 1;`})
		o.wantFail(t, "script_error")
	})
	t.Run("results of any shape come back as JSON", func(t *testing.T) {
		answers := map[string]answer{"audit_compare": func(json.RawMessage) (any, *wire.RetError) {
			return json.RawMessage(`{"a":[1,2.5,null,true,"\u00e9\ud83d\ude00"],"big":12345678901234567890,"deep":{"x":{"y":{}}}}`), nil
		}}
		o := runScenario(t, scenario{answers: answers, js: `const r = await unravis.audit_compare({}); return [r.a, r.deep, typeof r.big];`})
		o.wantClean(t)
		if string(o.done.Result) != `[[1,2.5,null,true,"é😀"],{"x":{"y":{}}},"number"]` {
			t.Fatalf("%s", o.done.Result)
		}
	})
}
