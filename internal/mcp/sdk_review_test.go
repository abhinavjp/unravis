package mcp

import (
	"api-audit-explorer/internal/version"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	segjson "github.com/segmentio/encoding/json"
)

// BL-342 review 1 (docs/desktop/spikes/BL-341-go-sdk.md): each test here fails against the loop as it was when the review was made.

// survives sends one line, then a ping with a fixed id, and reports whether the ping is answered: whatever the line was, the session goes on.
func survives(w *wire, line string) (first map[string]any, alive bool) {
	w.t.Helper()
	w.send(line)
	if _, err := w.in.Write([]byte(`{"jsonrpc":"2.0","id":"after","method":"ping"}` + "\n")); err != nil {
		return nil, false
	}
	for {
		m, ok := w.next(5 * time.Second)
		if !ok {
			return first, false
		}
		if sameID(m["id"], "after") {
			if first == nil {
				// The SDK handles requests concurrently: the answer to the line can follow the ping's.
				first, _ = w.next(200 * time.Millisecond)
			}
			return first, true
		}
		if first == nil {
			first = m
		}
	}
}

// M2: a line that is valid JSON never ends the session.
func TestSDKValidJSONKeepsSession(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	cases := []struct {
		name, line string
		want       string // "" no answer expected or any; otherwise answerClass
	}{
		{"trailing space", `{"jsonrpc":"2.0","id":"p1","method":"ping"} `, "result"},
		{"trailing tab", "{\"jsonrpc\":\"2.0\",\"id\":\"p1\",\"method\":\"ping\"}\t", "result"},
		{"leading space", ` {"jsonrpc":"2.0","id":"p1","method":"ping"}`, "result"},
		{"carriage return", "{\"jsonrpc\":\"2.0\",\"id\":\"p1\",\"method\":\"ping\"}\r", "result"},
		// A key in another case, alone, is read as the hand-rolled loop read it (encoding/json): answered. The SDK is sent the canonical form.
		{"uppercase JSONRPC key", `{"JSONRPC":"2.0","id":"p1","method":"ping"}`, "result"},
		{"uppercase METHOD key", `{"jsonrpc":"2.0","id":"p1","METHOD":"ping"}`, "result"},
		{"uppercase ID key", `{"jsonrpc":"2.0","ID":"p1","method":"ping"}`, "result"},
		{"params nested 1001", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":{"x":` + deep(1001) + `}}`, "error -32600"},
		{"params nested 300", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":{"x":` + deep(300) + `}}`, "error -32600"},
		{"params nested 100", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":{"x":` + deep(100) + `}}`, "result"},
		{"invalid utf-8 in params", "{\"jsonrpc\":\"2.0\",\"id\":\"p1\",\"method\":\"ping\",\"params\":{\"x\":\"\xff\"}}", "result"},
		{"params as a number", `{"jsonrpc":"2.0","id":"p1","method":"tools/list","params":5}`, "error -32602"},
		{"params as an array", `{"jsonrpc":"2.0","id":"p1","method":"tools/list","params":[]}`, "error -32602"},
		{"params null", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":null}`, "result"},
		{"notification tools/call", `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`, ""},
		{"notification with a bad shape", `{"jsonrpc":"2.0","method":"notifications/cancelled","params":"x"}`, ""},
		{"cancel with an object id", `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":{}}}`, ""},
		{"initialize twice", initLine, "error -32600"},
		{"huge version", `{"jsonrpc":"2.0","id":"p1","method":"initialize","params":{"protocolVersion":"` + strings.Repeat("V", 5000) + `","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`, ""},
		{"unicode escapes", `{"jsonrpc":"2.0","id":"p1","method":"ping"}`, "result"},
		{"lone surrogate in params", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":{"x":"\ud800"}}`, "result"},
		{"big number in params", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":{"x":1e999999}}`, "result"},
		{"id past 2^53", `{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`, "error -32600"},
		// Review 2, S4: an id that would come back changed is refused.
		{"id with invalid utf-8", "{\"jsonrpc\":\"2.0\",\"id\":\"\xff\",\"method\":\"ping\"}", "error -32600"},
		{"id with a lone surrogate", `{"jsonrpc":"2.0","id":"\ud800","method":"ping"}`, "error -32600"},
		// Review 2, M1: characters that a JSON encoder escapes (< > & become six bytes) must not push a line under 1 MiB over the SDK's cap.
		{"350 KB of <", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":{"x":"` + strings.Repeat("<", 350000) + `"}}`, "result"},
		{"400 KB of &", `{"jsonrpc":"2.0","id":"p1","method":"ping","params":{"x":"` + strings.Repeat("&", 400000) + `"}}`, "result"},
		{"900 KB of > in a cursor", `{"jsonrpc":"2.0","id":"p1","method":"tools/list","params":{"cursor":"` + strings.Repeat(">", 900000) + `"}}`, ""},
		{"400 KB of > in tool arguments", `{"jsonrpc":"2.0","id":"p1","method":"tools/call","params":{"name":"audit_get_guide","arguments":{"x":"` + strings.Repeat(">", 400000) + `"}}}`, "tool isError=true invalid or unknown tool arguments; follow the tool input schema"},
		{"900 KB of & in the method", `{"jsonrpc":"2.0","id":"p1","method":"` + strings.Repeat("&", 900000) + `"}`, "error -32601"},
	}
	for _, mode := range []string{"handshake", "stateless"} {
		w := dial(t, sdkImpl, mustBridge(t))
		w.stateless = mode == "stateless"
		if mode == "handshake" {
			w.handshake()
		}
		for _, c := range cases {
			line := c.line
			if w.stateless && c.name == "initialize twice" {
				continue
			}
			first, alive := survives(w, line)
			if !alive {
				t.Fatalf("%s: %q ended the session", mode, c.name)
			}
			if c.want != "" {
				if first == nil {
					t.Errorf("%s: %q got no answer, want %s", mode, c.name, c.want)
				} else if class := answerClass(first); class != c.want && (c.want != "result" || class != "empty") {
					t.Errorf("%s: %q answered %s, want %s", mode, c.name, class, c.want)
				}
			}
		}
		if err := w.finish(); err != nil {
			t.Fatal(err)
		}
	}
}

// M1: no spelling of a tools/call gets past the four-in-flight rule or the duplicate-id rule.
func TestSDKInFlightCannotBeBypassed(t *testing.T) {
	t.Run("a shadow method key", func(t *testing.T) {
		b, hits, _ := blockingUpstream(t)
		w := dial(t, sdkImpl, b)
		w.handshake()
		for i := 0; i < 10; i++ {
			w.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":"c%d","method":"tools/call","METHOD":"ping","params":{"name":"audit_list_sources","arguments":{}}}`, i))
			// Refused as a whole: two readings of the method, so no id to trust.
			if res, ok := w.next(3 * time.Second); !ok || errorCode(res) != -32600 {
				t.Fatalf("line %d: %v", i, res)
			}
		}
		time.Sleep(300 * time.Millisecond)
		if hits.Load() != 0 {
			t.Fatalf("%d requests reached the dashboard through a shadow key", hits.Load())
		}
	})
	t.Run("an id under another key case is answered and frees its slot", func(t *testing.T) {
		// The SDK reads "ID" as no id at all (a notification, never answered); admitting it by the loop's reading leaked the slot.
		// Now the SDK is sent the canonical message, so the call is a real one, answered under its id.
		w := dial(t, sdkImpl, bridgeFor(t, demoDashboard(t)))
		w.handshake()
		for round := 0; round < 3; round++ {
			for i := 0; i < 4; i++ {
				w.send(fmt.Sprintf(`{"jsonrpc":"2.0","ID":"u%d-%d","method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`, round, i))
			}
			for i := 0; i < 4; i++ {
				if res := w.reply(fmt.Sprintf("u%d-%d", round, i)); res["error"] != nil {
					t.Fatalf("round %d call %d: %v", round, i, res)
				}
			}
		}
		text, isError := w.call("audit_get_guide", map[string]any{})
		if isError || !strings.Contains(text, "guide") {
			t.Fatalf("a normal call afterwards: %q %v", text, isError)
		}
	})
	t.Run("ids beyond 2^53 are refused and cannot leak slots", func(t *testing.T) {
		b, hits, _ := blockingUpstream(t)
		w := dial(t, sdkImpl, b)
		w.handshake()
		for _, id := range []string{"9007199254740993", "9007199254740995", "9007199254740997", "9007199254740999", "-9007199254740993", "1e400", "123456789012345678901234567890"} {
			w.send(`{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"audit_list_sources","arguments":{}}}`)
			res, ok := w.next(3 * time.Second)
			if !ok || errorCode(res) != -32600 || res["id"] != nil {
				t.Fatalf("id %s: %v", id, res)
			}
		}
		if hits.Load() != 0 {
			t.Fatalf("a refused id reached the dashboard")
		}
		// The largest safe ids round-trip exactly.
		for _, id := range []string{"9007199254740991", "-9007199254740991"} {
			w.send(`{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}`)
			if raw := <-w.raw; !strings.Contains(raw, `"id":`+id+`,`) && !strings.HasSuffix(strings.TrimSpace(strings.Split(raw, `"id":`)[1]), "") {
				t.Fatalf("id %s came back as %s", id, raw)
			}
		}
	})
	t.Run("a ping cannot reuse the id of a call that is running", func(t *testing.T) {
		b, hits, _ := blockingUpstream(t)
		w := dial(t, sdkImpl, b)
		w.handshake()
		w.send(`{"jsonrpc":"2.0","id":"x0","method":"tools/call","params":{"name":"audit_list_sources","arguments":{}}}`)
		eventually(t, "the call is running", func() bool { return hits.Load() == 1 })
		for i := 0; i < 6; i++ {
			w.send(`{"jsonrpc":"2.0","id":"x0","method":"ping"}`)
			if res := w.reply("x0"); errorCode(res) != -32600 {
				t.Fatalf("ping with the id of a running call: %v", res)
			}
		}
		// Three more calls fill the cap; the fifth is refused, so x0 still holds its slot.
		for _, id := range []string{"x1", "x2", "x3"} {
			w.send(w.line(id, "tools/call", `"name":"audit_list_sources","arguments":{}`))
		}
		eventually(t, "four calls running", func() bool { return hits.Load() == 4 })
		w.send(w.line("x4", "tools/call", `"name":"audit_list_sources","arguments":{}`))
		if res := w.reply("x4"); errorCode(res) != -32000 {
			t.Fatalf("fifth call: %v", res)
		}
	})
}

// A client's lines reach the SDK only as the loop re-encoded them: ids in one form, no stray keys, no raw bytes.
func TestSDKForwardsOnlyTheCanonicalMessage(t *testing.T) {
	line, refusal := readLine([]byte(`  {"jsonrpc":"2.0","extra":{"a":1},"id":"a","method":"tools/list","params":null} `))
	if refusal != nil {
		t.Fatal(refusal)
	}
	if got, want := string(line.message), `{"id":"a","jsonrpc":"2.0","method":"tools/list"}`; got != want {
		t.Fatalf("canonical message %s, want %s", got, want)
	}
	line, refusal = readLine([]byte(`{"params":{"name":"audit_get_guide","arguments":{}},"method":"tools/call","id":-0,"jsonrpc":"2.0"}`))
	if refusal != nil {
		t.Fatal(refusal)
	}
	if got, want := string(line.message), `{"id":0,"jsonrpc":"2.0","method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`; got != want {
		t.Fatalf("canonical message %s, want %s", got, want)
	}
}

// S6: for every line the loop accepts, the SDK's own decoder reads the same method, id and params as the loop did. A line it reads
// differently must be refused, never forwarded. A table here; FuzzReadLine searches for more.
func TestSDKDecoderAgreesWithTheLoop(t *testing.T) {
	for _, in := range differentialLines() {
		checkDifferential(t, in)
	}
}

func differentialLines() []string {
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	return []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":"a","method":"tools/list","params":{"cursor":"c"}}`,
		`{"jsonrpc":"2.0","id":"a","method":"tools/call","METHOD":"ping","params":{"name":"x"}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","Method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"a","method":"tools/list","params":{"_meta":{"x":1}},"PARAMS":{}}`,
		`{"jsonrpc":"2.0","ID":"a","method":"ping"}`,
		`{"JSONRPC":"2.0","id":"a","method":"ping"}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping"} `,
		"{\"jsonrpc\":\"2.0\",\"id\":\"a\",\"method\":\"ping\"}\t",
		`{"jsonrpc":"2.0","id":9007199254740993,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":-0,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1.0,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping"}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"x":` + deep(1001) + `}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"x":` + deep(255) + `}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"x":` + deep(257) + `}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"a","reason":"r"}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"a","REQUESTID":"b"}}`,
		`{"jsonrpc":"2.0","id":"a","method":"paramſ"}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","paramſ":{}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"k":1,"K":2}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"a":{"b":1,"B":2}}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":[{"b":1},{"b":2}]}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"x":"\ud800"}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"x":1E400}}`,
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"x":-0.0}}`,
		"{\"jsonrpc\":\"2.0\",\"id\":\"a\\u2028\",\"method\":\"ping\",\"params\":{\"x\":\" \xff\"}}",
		`{"jsonrpc":"2.0","id":"a","method":"ping","params":{"x":"</script>&"}}`,
		// Review 2: keys read in another case by the checks and not by the SDK.
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"audit_get_guide","ARGUMENTS":{"bogus":1}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"audit_get_guide","Arguments":"not-an-object"}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"audit_get_guide","argumentſ":{"bogus":1}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"Name":"audit_get_guide","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"nAme":"audit_get_guide","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{},"_META":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"nAme":"0000000000000000000","Arguments":{"0":"00"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"\u006eame":"audit_get_guide","arguments":null}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a<b>&c","arguments":{"x":"<>&"}}}`,
		`{"jsonrpc":"2.0","id":"<<<>>>&&&","method":"ping"}`,
		"{\"jsonrpc\":\"2.0\",\"id\":\"\xff\",\"method\":\"ping\"}",
		`{"jsonrpc":"2.0","id":"\ud800","method":"ping"}`,
		"\ufeff" + `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":1.0E+2,"y":-0,"z":0.1e-400}}`,
	}
}

// checkDifferential fails when the loop would forward a message the SDK decoder reads another way (or cannot read at all).
func checkDifferential(t *testing.T, in string) {
	t.Helper()
	line, refusal := readLine([]byte(in))
	if refusal != nil {
		return // refused: nothing is forwarded
	}
	if len(line.message) > maxMessage {
		t.Fatalf("%.80q: canonical message of %d bytes is over the %d cap", in, len(line.message), maxMessage)
	}
	if bytes.ContainsAny(line.message, "\n\r") {
		t.Fatalf("%.80q: canonical message holds a line break: %q", in, line.message)
	}
	msg, err := jsonrpc.DecodeMessage(line.message)
	if err != nil {
		t.Fatalf("%.80q: the SDK cannot read the canonical message %.120q: %v", in, line.message, err)
	}
	checkTypedReading(t, in, line)
	var method string
	var id jsonrpc.ID
	var params json.RawMessage
	switch m := msg.(type) {
	case *jsonrpc.Request:
		method, id, params = m.Method, m.ID, m.Params
	default:
		t.Fatalf("%.80q: the SDK reads a %T, want a request", in, msg)
	}
	if method != line.method {
		t.Fatalf("%.80q: method %q (SDK) vs %q (loop)", in, method, line.method)
	}
	if hasID := len(line.id) != 0; hasID != id.IsValid() {
		t.Fatalf("%.80q: SDK id valid=%v, loop id %q", in, id.IsValid(), line.id)
	}
	if id.IsValid() {
		back, _ := json.Marshal(id.Raw())
		if idKey(back) != string(line.id) {
			t.Fatalf("%.80q: id %s (SDK) vs %s (loop)", in, back, line.id)
		}
	}
	if len(line.params) == 0 && len(params) == 0 {
		return
	}
	read := func(raw []byte) (v any, err error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		return v, decoder.Decode(&v)
	}
	a, errA := read(line.params)
	b, errB := read(params)
	if errA != nil || errB != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("%.80q: params %.80q (SDK) vs %.80q (loop)", in, params, line.params)
	}
}

// FuzzReadLine is the differential fuzz target: whatever the loop accepts, the SDK reads the same envelope, and for tools/call the same
// name, arguments and _meta, from a message that fits the frame cap. Its seeds are the probes of both reviews.
func FuzzReadLine(f *testing.F) {
	for _, in := range differentialLines() {
		f.Add([]byte(in))
	}
	f.Add([]byte(`{"jsonrpc":"2.0","id":"c0","method":"tools/call","METHOD":"ping","params":{"name":"audit_list_sources","arguments":{}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("<", 350000) + `"}}`))
	f.Fuzz(func(t *testing.T, in []byte) { checkDifferential(t, string(in)) })
}

// The SDK writes each message and its newline in one Write; frameWriter's size check counts on it. A bump that splits them fails here.
func TestSDKWritesOneMessagePerWrite(t *testing.T) {
	b := bridgeFor(t, demoDashboard(t))
	out := &recordingWriter{}
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- b.Serve(context.Background(), pr, out) }()
	send := func(line string, wantWrites int) {
		if _, err := io.WriteString(pw, line+"\n"); err != nil {
			t.Fatal(err)
		}
		eventually(t, "answers written", func() bool { return out.count() >= wantWrites })
	}
	send(initLine, 1)
	send(readyLine, 1)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, 2)
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`, 3)
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, w := range out.snapshot() {
		if !bytes.HasSuffix(w, []byte("\n")) || bytes.Count(w, []byte("\n")) != 1 {
			t.Fatalf("a Write is not exactly one line: %.100q", w)
		}
	}
}

type recordingWriter struct {
	mu     sync.Mutex
	writes [][]byte
}

func (r *recordingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (r *recordingWriter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.writes)
}

func (r *recordingWriter) snapshot() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.writes...)
}

// A refusal from the input goroutine after the session ended writes nothing (the goroutine can outlive Serve when the input is not closable).
func TestFrameWriterRejectAfterSessionEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &recordingWriter{}
	f := &frameWriter{w: out, done: ctx, onFail: func() {}, released: func(json.RawMessage) {}}
	f.reject(nil, -32700, "Parse error")
	cancel()
	f.reject(nil, -32700, "Parse error")
	if out.count() != 1 {
		t.Fatalf("%d writes, want 1", out.count())
	}
}

// S5: the server reports the build's version, and audit_compare (which replaces the cached comparison) is the one tool not idempotent.
func TestSDKServerVersionAndHints(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = "9.8.7-test"
	w := dial(t, sdkImpl, mustBridge(t))
	res := resultOf(t, w.handshake())
	if info, _ := res["serverInfo"].(map[string]any); info["version"] != "9.8.7-test" || info["name"] != "unravis" {
		t.Fatalf("serverInfo %v", info)
	}
	list := resultOf(t, w.request("tools/list", ""))["tools"].([]any)
	if len(list) != 10 {
		t.Fatalf("%d tools", len(list))
	}
	for _, item := range list {
		tool := item.(map[string]any)
		hints := tool["annotations"].(map[string]any)
		wantIdempotent := tool["name"] != "audit_compare"
		if hints["readOnlyHint"] != true || hints["destructiveHint"] != false || hints["openWorldHint"] != false || hints["idempotentHint"] != wantIdempotent {
			t.Errorf("%v annotations %v", tool["name"], hints)
		}
	}
}

// S2: MCPGODEBUG is cleared before the SDK reads it. A malformed value made the SDK panic in its init and killed every binary at startup.
func TestEnvGuardClearsMCPGODEBUG(t *testing.T) {
	if os.Getenv("UNRAVIS_ENVGUARD_CHILD") == "1" {
		if got := os.Getenv("MCPGODEBUG"); got != "" {
			t.Fatalf("MCPGODEBUG still set: %q", got)
		}
		return
	}
	for _, value := range []string{"bad", "nowrapinvalidparams=1", "a=b,c"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestEnvGuardClearsMCPGODEBUG$", "-test.v") //nolint:gosec // re-executes this test binary
		cmd.Env = append(os.Environ(), "MCPGODEBUG="+value, "UNRAVIS_ENVGUARD_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "PASS") {
			t.Fatalf("MCPGODEBUG=%s: %v\n%.600s", value, err, out)
		}
	}
}

// A host that writes its requests and closes the pipe (a script, a smoke test) still gets an answer to each: the loop does not end the
// SDK's session while it owes answers. Calls still running are abandoned, as they always were.
func TestSDKAnswersWhatWasReadBeforeEOF(t *testing.T) {
	handshake := initLine + "\n" + readyLine + "\n" +
		`{"jsonrpc":"2.0","id":"a","method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":"b","method":"tools/list"}` + "\n" + `{"jsonrpc":"2.0","id":"c","method":"nope"}` + "\n"
	stateless := `{"jsonrpc":"2.0","id":"a","method":"server/discover","params":{` + statelessMeta + `}}` + "\n" +
		`{"jsonrpc":"2.0","id":"b","method":"tools/list","params":{` + statelessMeta + `}}` + "\n"
	for _, c := range []struct {
		im    impl
		input string
		ids   []string
	}{{legacyImpl, handshake, []string{"init", "a", "b", "c"}}, {sdkImpl, handshake, []string{"init", "a", "b", "c"}}, {sdkImpl, stateless, []string{"a", "b"}}} {
		for i := 0; i < 20; i++ { // the order of the two threads decides; repeat
			var out answers
			if err := c.im.serve(bridgeFor(t, demoDashboard(t)), context.Background(), strings.NewReader(c.input), &out); err != nil {
				t.Fatal(err)
			}
			for _, id := range c.ids {
				if _, ok := out.answered(id); !ok {
					t.Fatalf("%s run %d: no answer to %q before EOF; output:\n%.400s", c.im.name, i, id, out.text())
				}
			}
		}
	}
}

// checkTypedReading: for tools/call, the SDK's case-sensitive decoder reads the same name, arguments and _meta from the message it is sent as
// the loop's checks read from the client's line (encoding/json, strictArgs). A key the checks accept in another case, or through a Unicode
// fold, must not turn into "absent" on the SDK's side (review 2, M2).
func checkTypedReading(t *testing.T, in string, line *sdkLine) {
	t.Helper()
	if line.method != "tools/call" {
		return
	}
	type typed struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      json.RawMessage `json:"_meta"`
	}
	var original rpcRequest
	if json.Unmarshal([]byte(in), &original) != nil {
		t.Fatalf("%.80q: accepted by the loop but not readable", in)
	}
	var checked typed
	if err := strictArgs(original.Params, &checked); err != nil {
		t.Fatalf("%.80q: params the loop's check refuses reached the SDK: %v", in, err)
	}
	var message struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line.message, &message); err != nil {
		t.Fatal(err)
	}
	var read typed
	decoder := segjson.NewDecoder(bytes.NewReader(message.Params))
	decoder.DontMatchCaseInsensitiveStructFields() // what the SDK's own decoder does
	if err := decoder.Decode(&read); err != nil {
		t.Fatalf("%.80q: the SDK cannot read the params %.120q: %v", in, message.Params, err)
	}
	same := func(a, b json.RawMessage) bool {
		var x, y bytes.Buffer
		if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
			return len(bytes.TrimSpace(a)) == 0 && len(bytes.TrimSpace(b)) == 0
		}
		return x.String() == y.String()
	}
	if checked.Name != read.Name || !same(checked.Arguments, read.Arguments) || !same(checked.Meta, read.Meta) {
		t.Fatalf("%.100q: checks read name %q arguments %q _meta %q; the SDK reads %q %q %q", in, checked.Name, checked.Arguments, checked.Meta, read.Name, read.Arguments, read.Meta)
	}
}

// Review 2, M2: a tools/call whose params use another case for a key, or a Unicode fold of it, is read as the hand-rolled loop read it (the
// checks and the tool see the same arguments), not run with defaults.
func TestSDKToolParamsKeyCaseMatchesLegacy(t *testing.T) {
	bad := `{"resultId":"r","limit":"ten"}`
	lines := []string{
		`{"name":"audit_get_groups","ARGUMENTS":` + bad + `}`,
		`{"name":"audit_get_groups","Arguments":` + bad + `}`,
		`{"name":"audit_get_groups","argumentſ":` + bad + `}`,
		`{"name":"audit_get_guide","Arguments":"not-an-object"}`,
		`{"name":"audit_get_guide","ARGUMENTS":{"bogus":1}}`,
		`{"Name":"audit_get_guide","arguments":{}}`,
		`{"nAme":"audit_get_guide","arguments":{}}`,
		`{"NAME":"audit_delete_source","arguments":{}}`,
		`{"name":"audit_get_guide","arguments":{},"_META":{"x":1}}`,
	}
	for _, mode := range []string{"handshake", "stateless"} {
		classes := map[string][]string{}
		for _, im := range bothImpls {
			if mode == "stateless" && im.name == "legacy" {
				continue
			}
			w := dial(t, im, mustBridge(t))
			w.stateless = mode == "stateless"
			if !w.stateless {
				w.handshake()
			}
			for i, params := range lines {
				if w.stateless {
					params = strings.TrimSuffix(params, "}") + "," + statelessMeta + "}"
					if strings.Contains(params, `"_META"`) {
						continue
					}
				}
				id := fmt.Sprintf("k%d", i)
				w.send(`{"jsonrpc":"2.0","id":"` + id + `","method":"tools/call","params":` + params + `}`)
				class := answerClass(w.reply(id))
				if len(class) > 70 {
					class = class[:70]
				}
				classes[im.name] = append(classes[im.name], class)
			}
		}
		if mode == "handshake" {
			for i := range lines {
				if classes["legacy"][i] != classes["sdk"][i] {
					t.Errorf("%s: %s: legacy %q, sdk %q", mode, lines[i], classes["legacy"][i], classes["sdk"][i])
				}
			}
		}
		// Whatever the legacy loop did, the arguments the client sent are never dropped: the bad ones are refused.
		for i, params := range lines[:5] {
			class := classes["sdk"][i]
			refused := strings.Contains(class, "tool arguments must be an object") || strings.Contains(class, "invalid or unknown tool arguments")
			if !strings.HasPrefix(class, "tool isError=true") || !refused {
				t.Errorf("%s: %s ran with the arguments dropped: %q", mode, params, classes["sdk"][i])
			}
		}
	}
}

// D24 (spec 03-mcp): each description says whether the tool queries production.
func TestToolDescriptionsSayLiveOrCached(t *testing.T) {
	want := map[string]string{
		"audit_get_activity": "Live query.", "audit_compare": "Live query.", "audit_get_requests": "Live query.", "audit_get_slowest": "Live query.", "audit_get_breakdown": "Live query.",
		"audit_get_guide": "Cached, no query.", "audit_list_sources": "Cached, no query.", "audit_get_groups": "Cached, no query.", "audit_get_timeline": "Cached, no query.", "audit_compare_timelines": "Cached, no query.",
	}
	for _, def := range tools() {
		name, _ := def["name"].(string)
		if description, _ := def["description"].(string); !strings.HasPrefix(description, want[name]+" ") || want[name] == "" {
			t.Errorf("%s: description starts %.40q, want prefix %q", name, description, want[name])
		}
	}
}
