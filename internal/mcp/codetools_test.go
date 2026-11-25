package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
)

// BL-574 slice 2b: the two code-mode tools are advertised only by a server started with them, never by the loopback bridge, and never
// as part of the ten-tool catalog a script is told about.

// fakeCaller records the tools it was asked for and answers with a fixed body.
type fakeCaller struct {
	mu    sync.Mutex
	calls []string
	body  string
}

func (f *fakeCaller) CallEnvelope(_ context.Context, name string, _ json.RawMessage) ([]byte, bool) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	return []byte(f.body), false
}

func dialCaller(t *testing.T, c Caller, withCode bool) *wire {
	t.Helper()
	input, in := io.Pipe()
	out, output := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	w := newWire(t, in, out)
	w.cancel = cancel
	go func() { w.done <- ServeCaller(ctx, c, withCode, input, output); _ = output.Close() }()
	t.Cleanup(func() { cancel(); _ = in.Close() })
	return w
}

func listedNames(t *testing.T, w *wire) []string {
	t.Helper()
	var names []string
	for _, tl := range resultOf(t, w.request("tools/list", ""))["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	return names
}

// Without the flag the list is the ten tools and the instructions are the bridge's; with it the two code tools come after the ten.
func TestToolsListWithAndWithoutCode(t *testing.T) {
	var ten []string
	for _, d := range tools() {
		ten = append(ten, d["name"].(string))
	}
	if len(ten) != 10 {
		t.Fatalf("the catalog has %d tools", len(ten))
	}
	off := dialCaller(t, &fakeCaller{}, false)
	initOff := off.handshake()
	if got := listedNames(t, off); strings.Join(got, ",") != strings.Join(ten, ",") {
		t.Errorf("without code: %v", got)
	}
	on := dialCaller(t, &fakeCaller{}, true)
	initOn := on.handshake()
	got := listedNames(t, on)
	if len(got) != 12 || strings.Join(got[:10], ",") != strings.Join(ten, ",") || got[10] != ToolTypes || got[11] != ToolExecute {
		t.Errorf("with code: %v", got)
	}
	if a, b := resultOf(t, initOff)["instructions"], resultOf(t, initOn)["instructions"]; a == b || a != serverInstructions || b != serverInstructionsCode {
		t.Errorf("instructions: %v / %v", a, b)
	}
	// The code tools carry no output schema (their answer is the run record, not the audit envelope) and are not read-only.
	for _, tl := range resultOf(t, on.request("tools/list", ""))["tools"].([]any) {
		tool := tl.(map[string]any)
		if !IsCodeTool(tool["name"].(string)) {
			continue
		}
		if tool["outputSchema"] != nil {
			t.Errorf("%v has an output schema", tool["name"])
		}
	}
}

// A tool that is not advertised cannot be called through the SDK server either, and a server with code routes the call to its caller.
func TestCodeToolsReachTheCallerOnlyWhenServed(t *testing.T) {
	body := `{"runId":"r1","status":"ok"}`
	fc := &fakeCaller{body: body}
	off := dialCaller(t, fc, false)
	off.handshake()
	res := off.request("tools/call", `"name":"`+ToolExecute+`","arguments":{"code":"1"}`)
	if isErr, _ := res["result"].(map[string]any)["isError"].(bool); res["error"] == nil && !isErr {
		t.Errorf("a server without code ran %s: %v", ToolExecute, res)
	}
	if len(fc.calls) != 0 {
		t.Errorf("the caller was asked for %v", fc.calls)
	}
	fc = &fakeCaller{body: body}
	on := dialCaller(t, fc, true)
	on.handshake()
	text, isErr := on.call(ToolExecute, map[string]any{"code": "1"})
	if isErr || text != body || len(fc.calls) != 1 || fc.calls[0] != ToolExecute {
		t.Errorf("served code tool: %q isError %v calls %v", text, isErr, fc.calls)
	}
}

// The two tools are not in the catalog a script is told about (BindingTools feeds the .d.ts and the run's tool list), so a script cannot
// call the tool that runs scripts.
func TestCodeToolsAreNotBindingTools(t *testing.T) {
	for _, bt := range BindingTools() {
		if IsCodeTool(bt.Name) {
			t.Errorf("%s is a binding tool", bt.Name)
		}
	}
	for _, name := range []string{ToolTypes, ToolExecute} {
		if knownTool(name) || KnownTool(name) {
			t.Errorf("%s is one of the ten", name)
		}
	}
	if got := CodeCatalog(); len(got) != 2 || got[0]["name"] != ToolTypes || got[1]["name"] != ToolExecute {
		t.Errorf("catalog %v", got)
	}
}

// The loopback bridge speaks with the session token, which is not a grant: it never runs a script.
func TestBridgeNeverRunsCodeTools(t *testing.T) {
	b := bridgeFor(t, demoDashboard(t))
	for _, name := range []string{ToolTypes, ToolExecute} {
		body, isErr := b.CallEnvelope(context.Background(), name, json.RawMessage(`{"code":"1"}`))
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		if json.Unmarshal(body, &env) != nil || !isErr || env.Error.Code != WirePermissionRequired {
			t.Errorf("%s: isError %v body %.200s", name, isErr, body)
		}
	}
}
