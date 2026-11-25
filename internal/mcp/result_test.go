package mcp

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/server"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// BL-343: structured results, the text copy, the error codes and the frame budget, on the real data path: the ten tools run through the SDK
// loop, Bridge.call, the real dashboard HTTP handler (real session proof, real audit engine on the demo repository).

// compileSchema compiles an output schema the way a validating client does.
func compileSchema(t *testing.T, schema any) *jsonschema.Schema {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource("mem:///output.json", doc); err != nil {
		t.Fatal(err)
	}
	sc, err := c.Compile("mem:///output.json")
	if err != nil {
		t.Fatalf("output schema does not compile: %v", err)
	}
	return sc
}

// validate checks a decoded value against a compiled schema. Numbers must be json.Number, as jsonschema wants.
func validate(t *testing.T, sc *jsonschema.Schema, raw []byte) error {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return sc.Validate(doc)
}

// tenResults runs the ten tools through the SDK loop and returns each whole result object as the host received it.
func tenResults(t *testing.T, stateless bool) map[string]map[string]any {
	t.Helper()
	w := dial(t, sdkImpl, bridgeFor(t, demoDashboard(t)))
	if stateless {
		w.stateless = true
	} else {
		w.handshake()
	}
	got := map[string]map[string]any{}
	tenCalls(t, func(name string, args any) (string, bool) {
		raw := w.callRaw(name, args)
		got[name] = raw
		isError, _ := raw["isError"].(bool)
		return raw["content"].([]any)[0].(map[string]any)["text"].(string), isError
	})
	return got
}

// wireCost is what the answer takes on the wire, counted here and not by the code under test: the structured copy plus the text copy
// as a JSON string.
func wireCost(body []byte) int {
	text, _ := json.Marshal(string(body))
	return len(body) + len(text)
}

func textOf(m map[string]any) string {
	return m["content"].([]any)[0].(map[string]any)["text"].(string)
}

// Each tool's result carries the same data twice: structuredContent and the text are one JSON, field by field.
func TestStructuredEqualsText(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		results := tenResults(t, stateless)
		if len(results) != 10 {
			t.Fatalf("answered %d tools", len(results))
		}
		for tool, res := range results {
			if res["structuredContent"] == nil {
				t.Errorf("%s: no structuredContent", tool)
				continue
			}
			var fromText any
			if err := json.Unmarshal([]byte(textOf(res)), &fromText); err != nil {
				t.Fatalf("%s: text is not JSON: %v", tool, err)
			}
			if !reflect.DeepEqual(fromText, res["structuredContent"]) {
				t.Errorf("%s (stateless=%v): text and structuredContent differ", tool, stateless)
			}
			if n := len(res["content"].([]any)); n != 1 {
				t.Errorf("%s: %d content items", tool, n)
			}
		}
	}
}

func TestEnvelopeFieldsPresent(t *testing.T) {
	for tool, res := range tenResults(t, false) {
		env := res["structuredContent"].(map[string]any)
		if env["schemaVersion"] != float64(SchemaVersion) || env["tool"] != tool {
			t.Errorf("%s: schemaVersion %v tool %v", tool, env["schemaVersion"], env["tool"])
		}
		if _, ok := env["partial"].(bool); !ok {
			t.Errorf("%s: partial %v", tool, env["partial"])
		}
		if v, present := env["truncation"]; !present || v != nil {
			t.Errorf("%s: truncation %v (present %v), want null", tool, v, present)
		}
		u, _ := env["untrusted"].(map[string]any)
		var paths []string
		for _, p := range u["paths"].([]any) {
			paths = append(paths, p.(string))
		}
		if len(paths) != len(toolShapes[tool].untrusted) || (len(paths) > 0 && !reflect.DeepEqual(paths, toolShapes[tool].untrusted)) {
			t.Errorf("%s: untrusted paths %v", tool, paths)
		}
		if u["note"] != untrustedNote {
			t.Errorf("%s: untrusted note %v", tool, u["note"])
		}
	}
}

// The advertised output schema accepts every real result, and the error answer of every code (also checked below).
func TestResultValidatesAgainstSchema(t *testing.T) {
	w := dial(t, sdkImpl, bridgeFor(t, demoDashboard(t)))
	w.handshake()
	listed := w.request("tools/list", "")
	advertised := map[string]any{}
	for _, tl := range resultOf(t, listed)["tools"].([]any) {
		tool := tl.(map[string]any)
		advertised[tool["name"].(string)] = tool["outputSchema"]
	}
	for tool, res := range tenResults(t, false) {
		if advertised[tool] == nil {
			t.Fatalf("%s: tools/list has no outputSchema", tool)
		}
		sc := compileSchema(t, advertised[tool])
		body := []byte(textOf(res))
		if err := validate(t, sc, body); err != nil {
			t.Errorf("%s: real result does not validate: %v\n%.600s", tool, err, body)
		}
		// The schema is not vacuous: without a field the tool always returns, and without the envelope, it fails.
		for _, drop := range append([]string{"schemaVersion", "untrusted", "partial", "truncation"}, toolShapes[tool].stable[0]) {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(body, &m)
			delete(m, drop)
			broken, _ := json.Marshal(m)
			if validate(t, sc, broken) == nil {
				t.Errorf("%s: a result without %q still validates", tool, drop)
			}
		}
	}
}

// ---- golden files ----

func canonicalIndent(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func checkGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // a fixed golden path under testdata
	if err != nil {
		t.Fatalf("%v (create it with: go test ./internal/mcp -run %s -update)", err, t.Name())
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != strings.ReplaceAll(string(got), "\r\n", "\n") {
		t.Fatalf("%s changed; review the change (additive only) and update the golden with: go test ./internal/mcp -run %s -update\n got  %.400s\n want %.400s", path, t.Name(), got, want)
	}
}

// The output schemas are a public contract: a change shows here and must be reviewed.
func TestOutputSchemasGolden(t *testing.T) {
	for _, d := range tools() {
		name := d["name"].(string)
		checkGolden(t, filepath.Join("testdata", "outputschema", name+".json"), canonicalIndent(t, canonical(t, outputSchema(name))))
	}
	if len(toolShapes) != len(tools()) {
		t.Fatalf("%d shapes for %d tools", len(toolShapes), len(tools()))
	}
}

// pathsOf lists the JSON paths of a value with the kinds seen there ("groups[].controller": "string"), arrays as "[]". Null is its own kind.
func pathsOf(v any, prefix string, out map[string]map[string]bool) {
	kind := func(k string) {
		if out[prefix] == nil {
			out[prefix] = map[string]bool{}
		}
		out[prefix][k] = true
	}
	switch x := v.(type) {
	case map[string]any:
		kind("object")
		for k, child := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			pathsOf(child, p, out)
		}
	case []any:
		kind("array")
		for _, child := range x {
			pathsOf(child, prefix+"[]", out)
		}
	case string:
		kind("string")
	case float64:
		kind("number")
	case bool:
		kind("boolean")
	case nil:
		kind("null")
	}
}

func shapeText(results map[string]map[string]any, tool string) []byte {
	paths := map[string]map[string]bool{}
	pathsOf(results[tool]["structuredContent"], "", paths)
	var lines []string
	for p, kinds := range paths {
		if p == "" {
			continue
		}
		var ks []string
		for k := range kinds {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		lines = append(lines, p+": "+strings.Join(ks, "|"))
	}
	sort.Strings(lines)
	return []byte(strings.Join(lines, "\n") + "\n")
}

// One compatibility fixture per public shape, from a real result: a field that was there may not go away or change its type; a new field
// is allowed (refresh the fixture with -update). Types seen only in some rows are kept in the fixture, so a row type may not be dropped.
func TestResultShapesAdditive(t *testing.T) {
	results := tenResults(t, false)
	for _, tool := range []string{"audit_get_guide", "audit_list_sources", "audit_compare", "audit_get_groups", "audit_get_timeline", "audit_compare_timelines", "audit_get_requests", "audit_get_slowest", "audit_get_breakdown", "audit_get_activity"} {
		got := shapeText(results, tool)
		path := filepath.Join("testdata", "shapes", tool+".txt")
		if *updateGolden {
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path) //nolint:gosec // a fixed golden path under testdata
		if err != nil {
			t.Fatalf("%v (create it with: go test ./internal/mcp -run TestResultShapesAdditive -update)", err)
		}
		have := map[string]bool{}
		for _, l := range strings.Split(strings.TrimSpace(string(got)), "\n") {
			have[l] = true
		}
		for _, l := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(want), "\r\n", "\n")), "\n") {
			if !have[l] {
				t.Errorf("%s: the shape line %q is no longer in the result (a field was removed or changed type)", tool, l)
			}
		}
	}
}

// ---- untrusted paths ----

// matchPath reports whether a concrete path of the walker ("groups[].controller") is one of the listed ones.
func listed(paths []string, p string) bool {
	for _, x := range paths {
		if x == p {
			return true
		}
	}
	return false
}

func walkStrings(v any, prefix string, visit func(path, s string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			walkStrings(child, p, visit)
		}
	case []any:
		for _, child := range x {
			walkStrings(child, prefix+"[]", visit)
		}
	case string:
		visit(prefix, x)
	}
}

// Every listed path exists in a real result, and every string in a result that is a controller, action or label from the audit data is at a
// listed path: a field that carries audit data cannot be added without being named.
func TestUntrustedPathsExist(t *testing.T) {
	results := tenResults(t, false)
	for tool, res := range results {
		env := res["structuredContent"].(map[string]any)
		paths := toolShapes[tool].untrusted
		// presence: any kind, not only strings (a path may hold an empty list item); look the paths up in the shape
		all := map[string]map[string]bool{}
		pathsOf(env, "", all)
		for _, p := range paths {
			// connectionLabel is omitted when a source has none (the demo sources have none); the path is still the right one.
			if all[p] == nil && !strings.HasSuffix(p, ".connectionLabel") && !strings.Contains(p, ".filter.") && !strings.HasPrefix(p, "other.") && !strings.HasSuffix(p, "indexWarning") {
				t.Errorf("%s: untrusted path %q is not in a real result", tool, p)
			}
		}
	}
	// Names that come from the audit data: every controller, action and label the demo repository holds.
	data := map[string]bool{}
	for _, res := range []map[string]any{results["audit_compare"], results["audit_list_sources"]} {
		walkStrings(res["structuredContent"], "", func(path, s string) {
			switch {
			case strings.HasSuffix(path, ".controller"), strings.HasSuffix(path, ".action"), strings.HasSuffix(path, ".label"), strings.HasSuffix(path, ".key"):
				if s != "" {
					data[s] = true
				}
			}
		})
	}
	if len(data) < 3 {
		t.Fatalf("the demo data gave %d names; the check would prove little", len(data))
	}
	for tool, res := range results {
		if tool == "audit_get_guide" {
			continue // fixed text: it mentions no tenant name (a golden test in BL-354 pins it)
		}
		walkStrings(res["structuredContent"], "", func(path, s string) {
			if !data[s] || strings.HasPrefix(path, "untrusted") || strings.HasPrefix(path, "query.") || strings.HasPrefix(path, "sourceIds") {
				return
			}
			if !listed(toolShapes[tool].untrusted, path) {
				t.Errorf("%s: %q at %q comes from audit data but its path is not listed as untrusted", tool, s, path)
			}
		})
	}
}

// ---- errors ----

// errorOf runs a tool through the SDK loop and returns the error envelope. It fails when the call did not fail.
func errorOf(t *testing.T, w *wire, tool string, args any) map[string]any {
	t.Helper()
	res := w.callRaw(tool, args)
	if res["isError"] != true {
		t.Fatalf("%s did not fail: %.300s", tool, textOf(res))
	}
	env, _ := res["structuredContent"].(map[string]any)
	var fromText map[string]any
	if json.Unmarshal([]byte(textOf(res)), &fromText) != nil || !reflect.DeepEqual(fromText, env) {
		t.Fatalf("%s: the error text and structuredContent differ: %v vs %v", tool, fromText, env)
	}
	e, _ := env["error"].(map[string]any)
	if e == nil {
		t.Fatalf("%s: no error object in %v", tool, env)
	}
	return e
}

func ts(d time.Duration) string { return demoNow.Add(d).Format(time.RFC3339) }

func windowQuery(extra map[string]any) map[string]any {
	q := map[string]any{
		"sourceIds": []string{"demo-west", "demo-east"}, "start": ts(-15 * time.Minute), "end": ts(0),
		"baselineStart": ts(-30 * time.Minute), "baselineEnd": ts(-15 * time.Minute),
		"kind": "all", "outcome": "all", "metric": "total", "bucketSeconds": 60,
	}
	for k, v := range extra {
		q[k] = v
	}
	return q
}

// gatedDashboard is the real dashboard handler with one gate in front of /api/analyze: a compare waits there until release is called, so a
// second compare can be made while the first is still running.
func gatedDashboard(t *testing.T) (srv *httptest.Server, reached chan struct{}, release func()) {
	t.Helper()
	real := server.New(audit.NewDemo(demoNow), nil, testSessionToken, true)
	reached, gate := make(chan struct{}, 4), make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/analyze" {
			reached <- struct{}{}
			select {
			case <-gate:
			case <-r.Context().Done():
			}
		}
		real.ServeHTTP(w, r)
	}))
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(func() { release(); srv.Close() })
	return srv, reached, release
}

// Every code a client can see is reached on the real path: the real dashboard handler, the real bridge, the real SDK loop.
func TestErrorCodesReachable(t *testing.T) {
	srv := demoDashboard(t)
	open := func(opts ...Option) *wire {
		w := dial(t, sdkImpl, bridgeFor(t, srv, opts...))
		w.handshake()
		return w
	}
	w := open()
	compared := w.callRaw("audit_compare", map[string]any{"query": windowQuery(nil), "limit": 2})
	var rid string
	{
		var env struct {
			ResultID string `json:"resultId"`
		}
		_ = json.Unmarshal([]byte(textOf(compared)), &env)
		rid = env.ResultID
	}
	if rid == "" {
		t.Fatalf("compare gave no handle: %.200s", textOf(compared))
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closedBridge := bridgeFor(t, closed)
	closed.Close()
	offline := dial(t, sdkImpl, closedBridge)
	offline.handshake()

	cases := []struct {
		name string
		w    *wire
		tool string
		args any
		code string
	}{
		{"backend offline", offline, "audit_list_sources", map[string]any{}, WireBackendUnavailable},
		{"identifier filter without the operator switch", w, "audit_compare", map[string]any{"query": windowQuery(map[string]any{"userIds": []int{1000}})}, WirePermissionRequired},
		{"user ip dimension without the operator switch", w, "audit_get_breakdown", map[string]any{"resultId": rid, "dim": "userIp", "includeIp": true}, WirePermissionRequired},
		{"unknown handle", w, "audit_get_groups", map[string]any{"resultId": "nope"}, WireResultExpired},
		{"too many time slots", w, "audit_compare", map[string]any{"query": windowQuery(map[string]any{"start": ts(-60 * time.Minute), "baselineStart": ts(-120 * time.Minute), "baselineEnd": ts(-60 * time.Minute), "bucketSeconds": 10})}, WireInvalidArguments},
		{"source that is not connected", w, "audit_compare", map[string]any{"query": windowQuery(map[string]any{"sourceIds": []string{"no-such-source"}})}, WireSourceUnavailable},
		{"limit out of range", w, "audit_get_groups", map[string]any{"resultId": rid, "limit": 1000}, WireInvalidArguments},
		{"unknown argument", w, "audit_get_guide", map[string]any{"x": 1}, WireInvalidArguments},
		{"group not in the result", w, "audit_get_timeline", map[string]any{"resultId": rid, "apiKey": "nope"}, WireInvalidArguments},
	}
	for _, c := range cases {
		e := errorOf(t, c.w, c.tool, c.args)
		if e["code"] != c.code {
			t.Errorf("%s: code %v, want %s (%v)", c.name, e["code"], c.code, e["message"])
		}
		if e["message"] == "" || e["action"] == "" {
			t.Errorf("%s: message %q action %q", c.name, e["message"], e["action"])
		}
	}
	// Over budget on the real path: a second comparison while one is running in the same session.
	gated, reached, release := gatedDashboard(t)
	busy := dial(t, sdkImpl, bridgeFor(t, gated))
	busy.handshake()
	busy.send(busy.line("first", "tools/call", `"name":"audit_compare","arguments":`+string(mustJSON(t, map[string]any{"query": windowQuery(nil), "limit": 2}))))
	<-reached
	if e := errorOf(t, busy, "audit_compare", map[string]any{"query": windowQuery(nil), "limit": 2}); e["code"] != WireBudgetExceeded || e["retryable"] != false {
		t.Errorf("second comparison: %v", e)
	}
	release()
	if res := resultOf(t, busy.reply("first")); res["isError"] == true {
		t.Errorf("the first comparison failed: %v", res)
	}
	// The codes only a dashboard answer can bring: each status the dashboard may give maps to one wire code.
	for status, want := range map[int]string{
		400: WireInvalidArguments, 401: WireBackendUnavailable, 404: WireSourceUnavailable, 413: WireBudgetExceeded, 423: WirePermissionRequired,
		429: WireBudgetExceeded, 500: WireInternal, 504: WireDeadlineExceeded,
	} {
		w := dial(t, sdkImpl, NewLocal(statusHandler(status, "{}"), "tok-0123456789abcdef0123456789abcdef"))
		w.handshake()
		if e := errorOf(t, w, "audit_list_sources", map[string]any{}); e["code"] != want {
			t.Errorf("HTTP %d: code %v, want %s", status, e["code"], want)
		}
	}
}

// The remaining codes are reached from the bridge itself: a cancel, a result too large for one frame.
func TestWireCodeOfBridgeErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		code string
	}{
		{context.Canceled, WireCancelled},
		{context.DeadlineExceeded, WireDeadlineExceeded},
		{coded(CodeCancelled, "x"), WireCancelled},
		{coded(CodeDeadlineExceeded, "x"), WireDeadlineExceeded},
		{coded(CodeResultTooLarge, "x"), WireResultTooLarge},
		{coded(CodeBusy, "x"), WireBudgetExceeded},
		{coded(CodeLocked, "x"), WirePermissionRequired},
		{coded(CodeNotFound, "x"), WireInvalidArguments},
		{coded(CodeInternal, "x"), WireInternal},
		{fmt.Errorf("plain"), WireInvalidArguments},
	} {
		if got := wireCode(c.err); got != c.code {
			t.Errorf("%v: %s, want %s", c.err, got, c.code)
		}
	}
	// The command line still reads the internal code of the same errors.
	if Code(coded(CodeNotFound, "x")) != CodeNotFound || Code(codedWire(CodeInvalidArguments, WirePermissionRequired, "x")) != CodeInvalidArguments {
		t.Error("an internal code changed")
	}
	// A cancelled call over the wire: the SDK loop answers with the cancelled code and a fixed text.
	b, hits, _ := blockingUpstream(t)
	w := dial(t, sdkImpl, b)
	w.handshake()
	w.send(w.line("slow", "tools/call", `"name":"audit_list_sources","arguments":{}`))
	eventually(t, "the call reaches the dashboard", func() bool { return hits.Load() == 1 })
	w.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"slow"}}`)
	res := resultOf(t, w.reply("slow"))
	e := res["structuredContent"].(map[string]any)["error"].(map[string]any)
	if res["isError"] != true || e["code"] != WireCancelled {
		t.Fatalf("cancelled call answered %v", res)
	}
}

// The error text is fixed: no argument a client sent, however it is wrong, comes back in an error answer, and no tool is named by the client.
func TestToolErrorsNeverEchoInput(t *testing.T) {
	const marker = "ZQ7-SECRET-MARKER-9f3"
	w := dial(t, sdkImpl, bridgeFor(t, demoDashboard(t)))
	w.handshake()
	compared := w.callRaw("audit_compare", map[string]any{"query": windowQuery(nil), "limit": 2})
	var env struct {
		ResultID string `json:"resultId"`
	}
	_ = json.Unmarshal([]byte(textOf(compared)), &env)
	bad := windowQuery(map[string]any{"controller": marker, "action": marker, "sourceIds": []string{marker}, "userAgentContains": marker, "urlContains": marker, "auditId": marker, "operation": marker, "kind": marker, "outcome": marker, "metric": marker, "statusCodes": []string{marker}, "uiVersions": []string{marker}, "userIps": []string{marker}})
	calls := []struct {
		tool string
		args any
	}{
		{"audit_compare", map[string]any{"query": bad}},
		{"audit_compare", map[string]any{"query": windowQuery(map[string]any{"sourceIds": []string{marker}})}},
		{"audit_compare", map[string]any{"query": windowQuery(map[string]any{"userAgentContains": marker})}},
		{"audit_compare", map[string]any{"query": windowQuery(nil), "sort": marker}},
		{"audit_compare", map[string]any{"query": windowQuery(nil), marker: 1}},
		{"audit_get_groups", map[string]any{"resultId": marker}},
		{"audit_get_groups", map[string]any{"resultId": env.ResultID, "search": strings.Repeat(marker, 100)}},
		{"audit_get_groups", map[string]any{"resultId": env.ResultID, "state": marker}},
		{"audit_get_groups", map[string]any{"resultId": env.ResultID, "sort": marker}},
		{"audit_get_timeline", map[string]any{"resultId": env.ResultID, "apiKey": marker}},
		{"audit_compare_timelines", map[string]any{"resultId": env.ResultID, "apiKeys": []string{marker}}},
		{"audit_get_requests", map[string]any{"resultId": env.ResultID, "apiKey": marker}},
		{"audit_get_requests", map[string]any{"resultId": env.ResultID, "apiKey": "x", "cursor": strings.Repeat(marker, 400)}},
		{"audit_get_slowest", map[string]any{"resultId": env.ResultID, "cursor": strings.Repeat(marker, 400)}},
		{"audit_get_breakdown", map[string]any{"resultId": env.ResultID, "dim": marker}},
		{"audit_get_activity", map[string]any{"sourceIds": []string{marker}, "start": marker, "end": marker}},
		{"audit_get_activity", map[string]any{"sourceIds": []string{marker}, "start": ts(-15 * time.Minute), "end": ts(0)}},
		{marker, map[string]any{"x": marker}},
	}
	for _, c := range calls {
		res := w.callRaw(c.tool, c.args)
		body, _ := json.Marshal(res)
		if strings.Contains(string(body), marker) {
			t.Errorf("%s echoed the client's input: %.300s", c.tool, body)
		}
	}
}

// ---- the frame budget ----

func bigRows(n, width int) []any {
	rows := make([]any, n)
	for i := range rows {
		rows[i] = map[string]any{"i": i, "text": strings.Repeat("x", width)}
	}
	return rows
}

func envelopeOf(t *testing.T, body []byte) (env map[string]any) {
	t.Helper()
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

// A result at the cap is cut at a row, with a marker; it is never over the budget (the text copy included), and what is kept is a prefix.
func TestFrameCapTruncates(t *testing.T) {
	// 50 rows of 8 KB: 400 KB of rows, twice on the wire
	for _, tool := range []string{"audit_get_requests", "audit_get_slowest", "audit_get_activity", "audit_get_breakdown", "audit_get_timeline", "audit_compare_timelines", "audit_list_sources"} {
		key := toolShapes[tool].rows
		body, err := encodeResult(tool, map[string]any{key: bigRows(50, 8000), "nextCursor": "abc"})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if wireCost(body) > FrameBudget {
			t.Errorf("%s: cost %d over the budget %d", tool, wireCost(body), FrameBudget)
		}
		env := envelopeOf(t, body)
		cut, _ := env["truncation"].(map[string]any)
		kept := env[key].([]any)
		if cut == nil || cut["reason"] != "frame_budget" || int(cut["droppedRows"].(float64)) != 50-len(kept) || len(kept) == 0 || len(kept) >= 50 {
			t.Errorf("%s: truncation %v with %d rows kept", tool, cut, len(kept))
		}
		if cut["hint"] == "" {
			t.Errorf("%s: no hint", tool)
		}
		// A cut page must not read as a complete one to a caller that looks only at partial.
		if env["partial"] != true {
			t.Errorf("%s: rows were dropped but partial is %v", tool, env["partial"])
		}
		for i, r := range kept {
			if int(r.(map[string]any)["i"].(float64)) != i {
				t.Errorf("%s: row %d is not the %dth of the page: the kept rows are not a prefix", tool, i, i)
				break
			}
		}
		// the biggest prefix that fits: one more row would not
		more, _ := encodeResult(tool, map[string]any{key: bigRows(len(kept)+1, 8000), "nextCursor": "abc"})
		if len(kept)+1 < 50 && wireCost(more) <= FrameBudget && envelopeOf(t, more)["truncation"] == nil {
			t.Errorf("%s: %d rows would have fitted", tool, len(kept)+1)
		}
		if toolShapes[tool].cursorPaged && env["nextCursor"] != "" {
			t.Errorf("%s: nextCursor %v after rows were dropped; it points past rows the caller never got", tool, env["nextCursor"])
		}
	}
	// a result that fits is not touched
	body, _ := encodeResult("audit_get_requests", map[string]any{"rows": bigRows(3, 100), "nextCursor": "abc"})
	if env := envelopeOf(t, body); env["truncation"] != nil || env["partial"] != false || len(env["rows"].([]any)) != 3 || env["nextCursor"] != "abc" {
		t.Errorf("a small result changed: %s", body)
	}
}

// A page by offset loses nothing when it is cut: nextOffset points at the first row that was not sent.
func TestFrameCapKeepsOffsetPagingLossless(t *testing.T) {
	for _, tool := range []string{"audit_get_groups", "audit_compare"} {
		body, err := encodeResult(tool, map[string]any{"groups": bigRows(40, 20000), "offset": 10, "nextOffset": nil, "matchedGroups": 50, "totalGroups": 50})
		if err != nil {
			t.Fatal(err)
		}
		env := envelopeOf(t, body)
		kept := len(env["groups"].([]any))
		if kept >= 40 || env["nextOffset"] != float64(10+kept) || env["truncation"] == nil || env["partial"] != true {
			t.Errorf("%s: kept %d, nextOffset %v, truncation %v", tool, kept, env["nextOffset"], env["truncation"])
		}
	}
}

// A single row that cannot fit a frame is refused with result_too_large, not cut in the middle or sent over the cap.
func TestOneRowTooLarge(t *testing.T) {
	for _, rows := range [][]any{bigRows(1, FrameBudget), bigRows(2, FrameBudget)} {
		_, err := encodeResult("audit_get_requests", map[string]any{"rows": rows})
		if Code(err) != CodeResultTooLarge || wireCode(err) != WireResultTooLarge {
			t.Errorf("%d rows: %v", len(rows), err)
		}
	}
	// the guide has nothing to drop
	if _, err := encodeResult("audit_get_guide", map[string]any{"guide": strings.Repeat("g", FrameBudget)}); Code(err) != CodeResultTooLarge {
		t.Errorf("guide: %v", err)
	}
	// and through the loop: an error envelope with the code, the text a host can show
	big := make([]map[string]any, 2)
	for i := range big {
		big[i] = map[string]any{"id": "s", "label": strings.Repeat("L", FrameBudget), "database": "d", "collection": "c"}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(big) }))
	defer upstream.Close()
	w := dial(t, sdkImpl, newBridge(fixed(upstream.URL), upstream.Client()))
	w.handshake()
	if e := errorOf(t, w, "audit_list_sources", map[string]any{}); e["code"] != WireResultTooLarge {
		t.Errorf("got %v", e)
	}
}

// HTML-sensitive characters grow six-fold when written; the budget counts them, so a result of them cannot pass the 1 MiB cap.
func TestFrameCapCountsEscapedBytes(t *testing.T) {
	rows := make([]any, 40)
	for i := range rows {
		rows[i] = map[string]any{"i": i, "text": strings.Repeat("<", 6000)}
	}
	body, err := encodeResult("audit_get_requests", map[string]any{"rows": rows})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > FrameBudget || wireCost(body) > FrameBudget {
		t.Errorf("%d bytes, cost %d", len(body), wireCost(body))
	}
	if envelopeOf(t, body)["truncation"] == nil {
		t.Error("not cut")
	}
}

// The envelope's names are the envelope's: a tool field of the same name cannot forge them.
func TestEnvelopeWinsOverToolFields(t *testing.T) {
	body, err := encodeResult("audit_get_guide", map[string]any{"guide": "g", "schemaVersion": 99, "tool": "other", "partial": true, "untrusted": "x", "truncation": "y"})
	if err != nil {
		t.Fatal(err)
	}
	env := envelopeOf(t, body)
	if env["schemaVersion"] != float64(SchemaVersion) || env["tool"] != "audit_get_guide" || env["partial"] != false || env["truncation"] != nil {
		t.Errorf("%s", body)
	}
	if _, ok := env["untrusted"].(map[string]any); !ok {
		t.Errorf("untrusted %v", env["untrusted"])
	}
}

// partial follows what the result already says.
func TestPartialFollowsTheResult(t *testing.T) {
	for _, c := range []struct {
		name   string
		result map[string]any
		want   bool
	}{
		{"nothing wrong", map[string]any{"sources": []any{map[string]any{"sourceId": "a", "ok": true}}}, false},
		{"source failed", map[string]any{"sources": []any{map[string]any{"sourceId": "a", "ok": false}}}, true},
		{"query ran short", map[string]any{"provenance": []any{map[string]any{"partialReason": "timeout"}}}, true},
		{"provenance complete", map[string]any{"provenance": []any{map[string]any{"partialReason": ""}}}, false},
		{"dashboard cut the page", map[string]any{"truncated": true}, true},
		{"records without timings", map[string]any{"quality": map[string]any{"incomplete": 3}}, true},
		{"unusable records", map[string]any{"quality": map[string]any{"invalid": 1}}, true},
		{"unknown outcomes", map[string]any{"quality": map[string]any{"unknownOutcome": 2}}, true},
		{"pending async only", map[string]any{"quality": map[string]any{"pendingAsync": 2}}, true},
		{"clean quality counters", map[string]any{"quality": map[string]any{"incomplete": 0, "invalid": 0, "unknownOutcome": 0}}, false},
		{"approximate alone says nothing", map[string]any{"approximate": true}, false},
		{"provenance labelled partial", map[string]any{"provenance": []any{map[string]any{"completeness": "partial"}}}, true},
		{"provenance ran to end", map[string]any{"provenance": []any{map[string]any{"completeness": "ran_to_end"}}}, false},
		{"warnings sent", map[string]any{"warnings": []any{"x"}}, true},
		{"no warnings", map[string]any{"warnings": []any{}}, false},
		{"sources without an ok flag", map[string]any{"sources": []any{map[string]any{"id": "a"}}}, false},
	} {
		body, err := encodeResult("audit_get_requests", c.result)
		if err != nil {
			t.Fatal(err)
		}
		if got := envelopeOf(t, body)["partial"]; got != c.want {
			t.Errorf("%s: partial %v", c.name, got)
		}
	}
}

// The error envelope never carries more than fixed text: a context error and an over-long message are cut to a fixed sentence.
func TestErrorEnvelopeText(t *testing.T) {
	e := envelopeOf(t, encodeError("audit_compare", context.Canceled))["error"].(map[string]any)
	if e["code"] != WireCancelled || e["message"] != "the call was cancelled" || e["retryable"] != false {
		t.Errorf("%v", e)
	}
	e = envelopeOf(t, encodeError("audit_compare", fmt.Errorf("%s", strings.Repeat("é", 1000))))["error"].(map[string]any)
	if m := e["message"].(string); len(m) > maxErrorMessage || !strings.HasSuffix(m, "é") {
		t.Errorf("message of %d bytes", len(m))
	}
	if e := envelopeOf(t, encodeError("audit_compare", coded(CodeBackendUnavailable, "x")))["error"].(map[string]any); e["retryable"] != true {
		t.Errorf("backend_unavailable is retryable: %v", e)
	}
	// every code has an action
	for _, code := range wireCodes {
		if errorActions[code] == "" {
			t.Errorf("no action for %s", code)
		}
	}
}

// fixedStrings are the string paths that do not come from audit data: names of the engine's own vocabulary, handles, ids, timestamps,
// cursors and our own notes. Every other string path of a real result must be listed as untrusted, so a new field forces a decision.
var fixedStrings = map[string]bool{
	"tool": true, "guide": true, "interpretation": true, "resultId": true, "sort": true, "note": true, "dim": true, "metric": true,
	"identifiers": true, "identifiersNote": true, "nextCursor": true, "start": true, "end": true, "sourceIds[]": true, "sourceId": true,
	"groups[].sourceId": true, "groups[].state": true, "group.sourceId": true, "group.state": true, "rows[].sourceId": true, "rows[].id": true,
	"rows[].initiatedAt": true, "rows[].completedAt": true, "points[].at": true, "slots[].at": true, "slots[].start": true, "slots[].end": true,
	"slots[].groups[].sampleStatus": true, "sources[].sourceId": true, "sources[].id": true, "sources[].selectionKey": true, "sources[].connectionId": true,
}

func TestEveryStringIsUntrustedOrFixed(t *testing.T) {
	for tool, res := range tenResults(t, false) {
		walkStrings(res["structuredContent"], "", func(path, _ string) {
			switch {
			case fixedStrings[path], listed(toolShapes[tool].untrusted, path):
			case path == "untrusted.note", strings.HasPrefix(path, "untrusted.paths"), strings.HasPrefix(path, "query."), strings.HasPrefix(path, "provenance[]."):
			default:
				t.Errorf("%s: string at %q is neither listed as untrusted nor a known fixed field; decide which", tool, path)
			}
		})
	}
	// the fields the review found: raw strings from records and from the database
	for tool, want := range map[string][]string{
		"audit_get_requests":  {"rows[].status", "rows[].state", "kind", "sources[].version", "sources[].indexWarning"},
		"audit_get_slowest":   {"rows[].status", "rows[].state", "rows[].kind", "sources[].version", "sources[].indexWarning"},
		"audit_compare":       {"groups[].kind", "groups[].outcome", "sources[].version"},
		"audit_get_breakdown": {"rows[].filter.statusCodes[]", "rows[].filter.uiVersions[]", "rows[].filter.userAgentContains", "rows[].filter.userIps[]", "other.value", "sources[].indexWarning"},
	} {
		for _, p := range want {
			if !listed(toolShapes[tool].untrusted, p) {
				t.Errorf("%s: %q is not listed as untrusted", tool, p)
			}
		}
	}
}
