package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// BL-341: the bounds of the hand-rolled loop, held by the SDK loop: oversize frames, cancel, malformed input, duplicate keys, the
// in-flight cap, scrubbed errors, EOF, and nothing but JSON-RPC on stdout.

func TestSDKOversizeFrameRejected(t *testing.T) {
	const message = "MCP input could not be read or exceeded 1 MiB"
	pad := func(n int) string { return strings.Repeat("A", n) }
	for _, im := range bothImpls {
		t.Run(im.name, func(t *testing.T) {
			// One byte over the cap: the loop stops with the plain error, writes nothing back, and does not echo the frame.
			over := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + pad(maxMessage) + `"}}` + "\n"
			b, _ := New(18765, WithToken(testSessionToken))
			var out bytes.Buffer
			done := make(chan error, 1)
			go func() { done <- im.serve(b, context.Background(), strings.NewReader(over), &out) }()
			select {
			case err := <-done:
				if err == nil || err.Error() != message {
					t.Fatalf("err=%v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("loop hung on an oversize frame")
			}
			if strings.Contains(out.String(), "AAAA") {
				t.Fatal("the frame was echoed")
			}
			// A frame just under the cap still works.
			w := dial(t, im, mustBridge(t))
			w.handshake()
			w.send(`{"jsonrpc":"2.0","id":"big","method":"ping","params":{"pad":"` + pad(maxMessage-1024) + `"}}`)
			if res := w.reply("big"); res["error"] != nil {
				t.Fatalf("frame under the cap refused: %.200v", res)
			}
			// The largest frame the line reader takes, then another right behind it in the same write: both are answered.
			fill := maxMessage - 2 - len(`{"jsonrpc":"2.0","id":"max","method":"ping","params":{"pad":""}}`)
			w.send(`{"jsonrpc":"2.0","id":"max","method":"ping","params":{"pad":"` + pad(fill) + `"}}` + "\n" + `{"jsonrpc":"2.0","id":"after","method":"ping"}`)
			for _, id := range []string{"max", "after"} {
				if res := w.reply(id); res["error"] != nil {
					t.Fatalf("%s: %.200v", id, res)
				}
			}
		})
	}
}

func mustBridge(t *testing.T) *Bridge {
	t.Helper()
	b, err := New(18765, WithToken(testSessionToken))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// An answer over 1 MiB is replaced by the same tool error in both loops: the host never receives a line over the cap.
func TestSDKOversizeAnswerReplaced(t *testing.T) {
	var sources []map[string]any
	for i := 0; i < 4000; i++ {
		sources = append(sources, map[string]any{"id": fmt.Sprintf("s%d", i), "label": strings.Repeat("L", 400), "database": "d", "collection": "c"})
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(sources) }))
	defer upstream.Close()
	var answers []string
	for _, im := range bothImpls {
		w := dial(t, im, newBridge(fixed(upstream.URL), upstream.Client()))
		w.handshake()
		text, isError := w.call("audit_list_sources", map[string]any{})
		if !isError || text != "Tool output exceeds 1 MiB. Reduce the requested page limit." {
			t.Fatalf("%s: isError=%v text=%.120s", im.name, isError, text)
		}
		answers = append(answers, text)
	}
	if answers[0] != answers[1] {
		t.Fatal("loops disagree")
	}
}

func TestSDKCancelDuringBlockedHandler(t *testing.T) {
	for _, c := range []struct {
		im        impl
		stateless bool
	}{{legacyImpl, false}, {sdkImpl, false}, {sdkImpl, true}} {
		name := c.im.name
		if c.stateless {
			name += " stateless"
		}
		t.Run(name, func(t *testing.T) {
			b, hits, cancelled := blockingUpstream(t)
			w := dial(t, c.im, b)
			w.stateless = c.stateless
			if !c.stateless {
				w.handshake()
			}
			w.send(w.line("slow", "tools/call", `"name":"audit_list_sources","arguments":{}`))
			eventually(t, "the call reaches the dashboard", func() bool { return hits.Load() == 1 })
			w.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"slow"}}`)
			res := w.reply("slow")
			content := resultOf(t, res)["content"].([]any)[0].(map[string]any)["text"]
			if resultOf(t, res)["isError"] != true || content != "audit operation cancelled or its five-minute deadline expired" {
				t.Fatalf("cancelled call answered %v", res)
			}
			eventually(t, "the dashboard request is abandoned", func() bool { return cancelled.Load() == 1 })
			// The session is still usable afterwards.
			if res := w.request("tools/list", ""); res["error"] != nil {
				t.Fatalf("session broken after cancel: %v", res)
			}
			if err := w.finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// answerClass reduces an answer to what a host reacts to: a protocol error code, or a tool result with its error flag and text.
func answerClass(m map[string]any) string {
	if e, ok := m["error"].(map[string]any); ok {
		return fmt.Sprintf("error %v", e["code"])
	}
	r, _ := m["result"].(map[string]any)
	if r == nil {
		return "empty"
	}
	if content, ok := r["content"].([]any); ok && len(content) > 0 {
		isError, _ := r["isError"].(bool)
		return fmt.Sprintf("tool isError=%v %v", isError, content[0].(map[string]any)["text"])
	}
	return "result"
}

func TestSDKMalformedArgs(t *testing.T) {
	const guard = "invalid or unknown tool arguments; follow the tool input schema"
	deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
	cases := []struct{ name, line, want string }{
		{"not json", `not-json`, "error -32700"},
		{"truncated json", `{"jsonrpc":"2.0","id":1,"method":"pi`, "error -32700"},
		{"NUL bytes", "\x00\x00", "error -32700"},
		{"empty object", `{}`, "error -32600"},
		{"empty array", `[]`, "error -32600"},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, "error -32600"},
		{"deep array", deep, "error -32600"},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, "error -32600"},
		{"no method", `{"jsonrpc":"2.0","id":1}`, "error -32600"},
		{"fractional id", `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, "error -32600"},
		{"null id", `{"jsonrpc":"2.0","id":null,"method":"ping"}`, "error -32600"},
		{"object id", `{"jsonrpc":"2.0","id":{},"method":"ping"}`, "error -32600"},
		{"bool id", `{"jsonrpc":"2.0","id":true,"method":"ping"}`, "error -32600"},
		{"unknown method", `{"jsonrpc":"2.0","id":"m","method":"shell/exec"}`, "error -32601"},
		{"resources/list", `{"jsonrpc":"2.0","id":"m","method":"resources/list"}`, "error -32601"},
		{"prompts/list", `{"jsonrpc":"2.0","id":"m","method":"prompts/list"}`, "error -32601"},
		{"logging/setLevel", `{"jsonrpc":"2.0","id":"m","method":"logging/setLevel","params":{"level":"debug"}}`, "error -32601"},
		{"completion/complete", `{"jsonrpc":"2.0","id":"m","method":"completion/complete","params":{}}`, "error -32601"},
		{"call without params", `{"jsonrpc":"2.0","id":"m","method":"tools/call"}`, "error -32602"},
		{"call without name", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"arguments":{}}}`, "error -32602"},
		{"call with numeric name", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":7}}`, "error -32602"},
		{"call with extra params key", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":"audit_get_guide","x":1}}`, "error -32602"},
		{"arguments array", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":"audit_get_guide","arguments":[1]}}`, "tool isError=true tool arguments must be an object"},
		{"unknown argument", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":"audit_get_guide","arguments":{"x":1}}}`, "tool isError=true " + guard},
		{"wrong argument type", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":"audit_get_groups","arguments":{"resultId":"r","limit":"ten"}}}`, "tool isError=true " + guard},
		{"limit zero", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":"audit_get_groups","arguments":{"resultId":"r","limit":0}}}`, "tool isError=true result expired or was replaced; run audit_compare again. Results last ten minutes and only the latest successful comparison is retained"},
		{"unknown tool", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":"audit_delete_source","arguments":{}}}`, "tool isError=true unknown tool; only the advertised read-only audit tools are available"},
		{"mutation tool name", `{"jsonrpc":"2.0","id":"m","method":"tools/call","params":{"name":"shell","arguments":{"cmd":"id"}}}`, "tool isError=true unknown tool; only the advertised read-only audit tools are available"},
	}
	for _, im := range bothImpls {
		t.Run(im.name, func(t *testing.T) {
			w := dial(t, im, mustBridge(t))
			w.handshake()
			for _, c := range cases {
				w.send(c.line)
				got, ok := w.next(5 * time.Second)
				if !ok {
					t.Fatalf("%s: no answer, loop ended or hung", c.name)
				}
				if class := answerClass(got); class != c.want {
					t.Errorf("%s: %s, want %s", c.name, class, c.want)
				}
				// Nothing a bad line does may stop the loop: the next good request is answered.
				if res := w.request("tools/list", ""); res["error"] != nil {
					t.Fatalf("%s: loop broken afterwards: %v", c.name, res)
				}
			}
			if err := w.finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSDKDuplicateJSONKeys(t *testing.T) {
	// The last key wins everywhere, as encoding/json does, and the SDK sees the line the checks saw: no case where the loop validates
	// one reading of a frame and the SDK dispatches another.
	lines := []string{
		`{"jsonrpc":"2.0","id":"a","method":"tools/list","method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":1,"id":"b","method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"c","method":"tools/call","params":{"name":"audit_get_guide","name":"audit_delete_source","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"d","method":"tools/call","params":{"name":"audit_delete_source","name":"audit_get_guide","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"e","method":"tools/call","params":{"name":"audit_get_guide","arguments":{"x":1},"arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"f","method":"tools/call","params":{"name":"audit_get_groups","arguments":{"resultId":"r","limit":500,"limit":0}}}`,
		`{"jsonrpc":"2.0","id":"g","method":"tools/call","params":{"name":"audit_get_guide","params":{},"arguments":{}}}`,
	}
	classes := map[string][]string{}
	for _, im := range bothImpls {
		w := dial(t, im, mustBridge(t))
		w.handshake()
		for _, line := range lines {
			w.send(line)
			m, ok := w.next(5 * time.Second)
			if !ok {
				t.Fatalf("%s: no answer to %s", im.name, line)
			}
			class := answerClass(m)
			if len(class) > 60 {
				class = class[:60]
			}
			classes[im.name] = append(classes[im.name], class)
		}
	}
	for i := range lines {
		if classes["legacy"][i] != classes["sdk"][i] {
			t.Errorf("line %d: legacy %q, sdk %q", i, classes["legacy"][i], classes["sdk"][i])
		}
	}
}

func TestSDKCapabilitiesOnlyTools(t *testing.T) {
	srv := demoDashboard(t)
	only := `{"tools":{}}`
	for _, im := range bothImpls {
		w := dial(t, im, bridgeFor(t, srv))
		caps := resultOf(t, w.handshake())["capabilities"]
		if got := string(mustJSON(t, caps)); got != only {
			t.Errorf("%s initialize capabilities %s", im.name, got)
		}
		if !w.silent(300 * time.Millisecond) {
			t.Errorf("%s sent something nobody asked for after the handshake (a list-changed or log notification)", im.name)
		}
	}
	w := dial(t, sdkImpl, bridgeFor(t, srv))
	w.stateless = true
	if got := string(mustJSON(t, resultOf(t, w.request("server/discover", ""))["capabilities"])); got != only {
		t.Errorf("sdk discover capabilities %s", got)
	}
	if !w.silent(300 * time.Millisecond) {
		t.Error("sdk sent an unsolicited message in the stateless lifecycle")
	}
	// Methods of capabilities not offered are refused in both lifecycles.
	for _, method := range []string{"resources/list", "resources/read", "resources/subscribe", "prompts/list", "prompts/get", "logging/setLevel", "completion/complete", "subscriptions/listen", "tasks/get", "sampling/createMessage", "roots/list"} {
		if code := errorCode(w.request(method, "")); code != -32601 {
			t.Errorf("stateless %s: code %d", method, code)
		}
		h := dial(t, sdkImpl, bridgeFor(t, srv))
		h.handshake()
		if code := errorCode(h.request(method, "")); code != -32601 {
			t.Errorf("handshake %s: code %d", method, code)
		}
	}
}

func TestSDKCacheScopePrivate(t *testing.T) {
	srv := demoDashboard(t)
	w := dial(t, sdkImpl, bridgeFor(t, srv))
	w.stateless = true
	for _, method := range []string{"server/discover", "tools/list"} {
		res := resultOf(t, w.request(method, ""))
		if res["cacheScope"] != "private" || res["ttlMs"] != float64(0) {
			t.Errorf("%s: cacheScope=%v ttlMs=%v", method, res["cacheScope"], res["ttlMs"])
		}
	}
	h := dial(t, sdkImpl, bridgeFor(t, srv))
	h.handshake()
	if res := resultOf(t, h.request("tools/list", "")); res["cacheScope"] != "private" {
		t.Errorf("tools/list after handshake: cacheScope=%v", res["cacheScope"])
	}
	// Nothing on the wire says "public", whatever the call.
	for _, line := range []string{"server/discover", "tools/list"} {
		w.send(w.line("p", line, ""))
		w.reply("p")
	}
	for len(w.raw) > 0 {
		if line := <-w.raw; strings.Contains(line, `"public"`) {
			t.Errorf("a public cache scope on the wire: %.200s", line)
		}
	}
}

func TestSDKInFlightLimit4(t *testing.T) {
	for _, c := range []struct {
		im        impl
		stateless bool
	}{{legacyImpl, false}, {sdkImpl, false}, {sdkImpl, true}} {
		name := c.im.name
		if c.stateless {
			name += " stateless"
		}
		t.Run(name, func(t *testing.T) {
			b, hits, cancelled := blockingUpstream(t)
			w := dial(t, c.im, b)
			w.stateless = c.stateless
			if !c.stateless {
				w.handshake()
			}
			call := func(id string) { w.send(w.line(id, "tools/call", `"name":"audit_list_sources","arguments":{}`)) }
			for _, id := range []string{"1", "2", "3", "4"} {
				call(id)
			}
			eventually(t, "four calls reach the dashboard", func() bool { return hits.Load() == 4 })
			call("5")
			if res := w.reply("5"); errorCode(res) != -32000 || !strings.Contains(fmt.Sprint(res["error"]), "Too many in-flight requests") {
				t.Fatalf("fifth call: %v", res)
			}
			call("2") // the same id again while it is running
			if res := w.reply("2"); errorCode(res) != -32600 || !strings.Contains(fmt.Sprint(res["error"]), "already in flight") {
				t.Fatalf("duplicate id: %v", res)
			}
			if hits.Load() != 4 {
				t.Fatalf("a refused call reached the dashboard: %d", hits.Load())
			}
			// Cancelling frees a slot for the next call.
			w.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"1"}}`)
			if res := w.reply("1"); resultOf(t, res)["isError"] != true {
				t.Fatalf("cancelled call: %v", res)
			}
			// The hand-rolled loop frees the slot just after it writes the answer, so a host racing it can meet the old slot: retry there.
			// The SDK loop frees it before it writes, so its first try must be accepted.
			for try := 0; ; try++ {
				id := fmt.Sprintf("6-%d", try)
				call(id)
				res, answered := w.next(300 * time.Millisecond)
				if !answered {
					break // running: no answer yet
				}
				if errorCode(res) != -32000 || c.im.name == "sdk" || try > 20 {
					t.Fatalf("freed slot not given: %v", res)
				}
			}
			eventually(t, fmt.Sprintf("the freed slot is used (hits=%d cancelled=%d)", hits.Load(), cancelled.Load()), func() bool { return hits.Load() == 5 })
			if err := w.finish(); err != nil {
				t.Fatal(err)
			}
			eventually(t, "every abandoned call is cancelled when the host goes", func() bool { return cancelled.Load() == 5 })
		})
	}
}

func TestSDKErrorsScrubbed(t *testing.T) {
	const secret = "private-password-7f3a9"
	lines := []string{
		secret + "-not-json",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + secret + `","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"` + secret + `"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{"` + secret + `":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"audit_get_groups","arguments":{"resultId":"` + secret + `"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":"` + secret + `"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"audit_get_guide","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":"` + secret + `","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":"` + secret + `"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/list","params":{"cursor":"` + secret + `"}}`,
	}
	for _, im := range bothImpls {
		t.Run(im.name, func(t *testing.T) {
			b := newBridge(fixed("http://127.0.0.1:1"), &http.Client{})
			var out bytes.Buffer
			input := strings.Join(lines, "\n") + "\n"
			if err := im.serve(b, context.Background(), strings.NewReader(input), &out); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), secret) {
				t.Fatalf("input echoed:\n%s", out.String())
			}
		})
	}
	t.Run("version refusal echoes at most a date", func(t *testing.T) {
		// SEP-2575 errors carry the version the client asked for. The SDK hands the string back as it came; the loop refuses anything
		// longer than a date first, so an arbitrary client string never comes back.
		w := dial(t, sdkImpl, mustBridge(t))
		w.send(`{"jsonrpc":"2.0","id":"v","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01-` + secret + secret + `","io.modelcontextprotocol/clientCapabilities":{}}}}`)
		res := w.reply("v")
		if code := errorCode(res); code != -32602 || strings.Contains(string(mustJSON(t, res)), secret) {
			t.Fatalf("long version: %v", res)
		}
		w.send(`{"jsonrpc":"2.0","id":"w","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`)
		if code := errorCode(w.reply("w")); code != -32022 {
			t.Fatalf("unsupported date code %d", code)
		}
	})
	t.Run("dashboard down", func(t *testing.T) {
		for _, im := range bothImpls {
			w := dial(t, im, newBridge(fixed("http://127.0.0.1:1"), &http.Client{}))
			w.handshake()
			text, isError := w.call("audit_list_sources", map[string]any{})
			if !isError || text != "Unravis did not answer. Start the dashboard, then retry" {
				t.Errorf("%s: %v %q", im.name, isError, text)
			}
		}
	})
}

func TestSDKEOFExitsClean(t *testing.T) {
	for _, im := range bothImpls {
		t.Run(im.name+" idle", func(t *testing.T) {
			w := dial(t, im, mustBridge(t))
			w.handshake()
			if err := w.finish(); err != nil {
				t.Fatalf("EOF after a clean session: %v", err)
			}
			if _, ok := w.next(200 * time.Millisecond); ok {
				t.Fatal("output after EOF")
			}
		})
		t.Run(im.name+" with a call running", func(t *testing.T) {
			b, hits, cancelled := blockingUpstream(t)
			w := dial(t, im, b)
			w.handshake()
			w.send(w.line("slow", "tools/call", `"name":"audit_list_sources","arguments":{}`))
			eventually(t, "the call reaches the dashboard", func() bool { return hits.Load() == 1 })
			start := time.Now()
			if err := w.finish(); err != nil {
				t.Fatalf("EOF with a call running: %v", err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatalf("took %v to stop", time.Since(start))
			}
			eventually(t, "the running dashboard request is cancelled when the host closes the pipe", func() bool { return cancelled.Load() == 1 })
		})
		t.Run(im.name+" parent context", func(t *testing.T) {
			input, writer := io.Pipe()
			defer writer.Close()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- im.serve(mustBridge(t), ctx, input, io.Discard) }()
			time.Sleep(50 * time.Millisecond)
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("parent cancellation left the idle loop blocked")
			}
		})
		t.Run(im.name+" stdout closed", func(t *testing.T) {
			input, writer := io.Pipe()
			defer writer.Close()
			done := make(chan error, 1)
			go func() { done <- im.serve(mustBridge(t), context.Background(), input, failingWriter{}) }()
			io.WriteString(writer, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")
			select {
			case err := <-done:
				if err == nil || err.Error() != "MCP output could not be written" {
					t.Fatalf("err=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the loop kept running with nowhere to write")
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// Stdout carries one JSON-RPC object per line and nothing else, whatever arrives on stdin.
func TestSDKStdoutOnlyJSONRPC(t *testing.T) {
	rng := rand.New(rand.NewSource(341))
	seeds := []string{
		initLine + "\n" + readyLine + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n",
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + statelessMeta + `}}` + "\n",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{},` + statelessMeta + `}}` + "\n",
		"not-json\n\n\x00\xff\n",
		`[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]` + "\n",
	}
	for i := 0; i < 300; i++ {
		seed := []byte(seeds[rng.Intn(len(seeds))])
		for n := rng.Intn(6); n > 0 && len(seed) > 0; n-- {
			seed[rng.Intn(len(seed))] = byte(rng.Intn(256))
		}
		checkStdout(t, sdkImpl, seed)
	}
}

// checkStdout runs one input through the loop and checks what it wrote.
func checkStdout(t *testing.T, im impl, input []byte) {
	t.Helper()
	b, err := New(1, WithToken(testSessionToken))
	if err != nil {
		t.Skip()
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = im.serve(b, ctx, bytes.NewReader(input), &out) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("%s did not stop after its input ended: %.100q", im.name, input)
	}
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		if len(line) > maxMessage {
			t.Fatalf("a response line of %d bytes is over the %d cap", len(line), maxMessage)
		}
		var object map[string]any
		if json.Unmarshal([]byte(line), &object) != nil || object["jsonrpc"] != "2.0" {
			t.Fatalf("stdout carries something that is not a JSON-RPC object: %.200q (input %.100q)", line, input)
		}
	}
}

// FuzzServeSDK is FuzzBridgeServe for the SDK loop, with stateless seeds added.
func FuzzServeSDK(f *testing.F) {
	const start = initLine + "\n" + readyLine + "\n"
	f.Add([]byte(start + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"))
	f.Add([]byte(start + `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}` + "\n"))
	f.Add([]byte(start + `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"audit_list_sources","arguments":{"x":1,"x":2}}}` + "\n"))
	f.Add([]byte(start + `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"audit_compare","arguments":{"start":"x","limit":-1}}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5}}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + statelessMeta + `}}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{},` + statelessMeta + `}}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}` + "\n"))
	f.Add([]byte("not-json\n\n\x00\xff\n"))
	f.Add([]byte(`[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]` + "\n"))
	f.Fuzz(func(t *testing.T, input []byte) { checkStdout(t, sdkImpl, input) })
}
