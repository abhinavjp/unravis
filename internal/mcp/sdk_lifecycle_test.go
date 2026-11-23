package mcp

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/server"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// BL-341: parity of the official Go SDK loop (serveSDK, in sdk_proto_test.go) with the hand-rolled loop (Bridge.Serve), driven as
// a host would drive them: JSON-RPC lines in, JSON-RPC lines out. Each test runs the same script against both and compares.

var updateGolden = flag.Bool("update", false, "rewrite the golden files of the catalog tests")

type impl struct {
	name  string
	serve func(*Bridge, context.Context, io.Reader, io.Writer) error
}

var (
	legacyImpl = impl{"legacy", (*Bridge).Serve}
	sdkImpl    = impl{"sdk", (*Bridge).serveSDK}
	bothImpls  = []impl{legacyImpl, sdkImpl}
)

// statelessMeta is the per-request envelope of protocol 2026-07-28 (SEP-2575): version, client info and capabilities travel with every call.
const statelessMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"spike-client","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}`

const (
	initLine  = `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"spike-client","version":"1"}}}`
	readyLine = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

// wire is a host: it writes lines to a loop's input and reads its output.
type wire struct {
	t         *testing.T
	in        io.WriteCloser
	lines     chan map[string]any
	raw       chan string
	done      chan error
	backlog   []map[string]any
	cancel    context.CancelFunc
	stateless bool
	seq       int
}

func dial(t *testing.T, im impl, b *Bridge) *wire {
	t.Helper()
	input, in := io.Pipe()
	out, output := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	w := newWire(t, in, out)
	w.cancel = cancel
	go func() { w.done <- im.serve(b, ctx, input, output); _ = output.Close() }()
	t.Cleanup(func() { cancel(); _ = in.Close() })
	return w
}

// newWire reads the loop's output (one JSON-RPC object per line) and writes to its input.
func newWire(t *testing.T, in io.WriteCloser, out io.Reader) *wire {
	w := &wire{t: t, in: in, lines: make(chan map[string]any, 256), raw: make(chan string, 256), done: make(chan error, 1)}
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		for scanner.Scan() {
			w.raw <- scanner.Text()
			var message map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				t.Errorf("stdout line is not JSON: %.200q", scanner.Text())
				continue
			}
			w.lines <- message
		}
		close(w.lines)
	}()
	return w
}

func (w *wire) send(line string) {
	w.t.Helper()
	if _, err := io.WriteString(w.in, line+"\n"); err != nil {
		w.t.Fatalf("write: %v", err)
	}
}

// next returns the next message, or false when none arrives within d.
func (w *wire) next(d time.Duration) (map[string]any, bool) {
	if len(w.backlog) > 0 {
		m := w.backlog[0]
		w.backlog = w.backlog[1:]
		return m, true
	}
	select {
	case m, ok := <-w.lines:
		return m, ok
	case <-time.After(d):
		return nil, false
	}
}

func sameID(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// reply waits for the message answering id; other messages stay queued for next.
func (w *wire) reply(id any) map[string]any {
	w.t.Helper()
	deadline := time.After(10 * time.Second)
	for i, m := range w.backlog {
		if sameID(m["id"], id) {
			w.backlog = append(w.backlog[:i], w.backlog[i+1:]...)
			return m
		}
	}
	for {
		select {
		case m, ok := <-w.lines:
			if !ok {
				w.t.Fatalf("output ended while waiting for the answer to %v", id)
			}
			if sameID(m["id"], id) {
				return m
			}
			w.backlog = append(w.backlog, m)
		case <-deadline:
			w.t.Fatalf("no answer to %v", id)
		}
	}
}

// silent reports that nothing is written for d.
func (w *wire) silent(d time.Duration) bool {
	_, ok := w.next(d)
	return !ok
}

func (w *wire) finish() error {
	w.t.Helper()
	_ = w.in.Close()
	select {
	case err := <-w.done:
		return err
	case <-time.After(10 * time.Second):
		w.t.Fatal("the loop did not stop after its input ended")
		return nil
	}
}

// handshake opens the older lifecycle (initialize, then notifications/initialized) and returns the initialize result.
func (w *wire) handshake() map[string]any {
	w.t.Helper()
	w.send(initLine)
	res := w.reply("init")
	w.send(readyLine)
	return res
}

// request sends one request in the wire's lifecycle (with the per-request envelope when stateless) and returns the whole answer.
func (w *wire) request(method string, params string) map[string]any {
	w.t.Helper()
	w.seq++
	id := "r" + strconv.Itoa(w.seq)
	w.send(w.line(id, method, params))
	return w.reply(id)
}

// line builds a request; params is the JSON object body without braces, so the envelope can be added.
func (w *wire) line(id, method, params string) string {
	if w.stateless {
		if params != "" {
			params += ","
		}
		params += statelessMeta
	}
	return `{"jsonrpc":"2.0","id":"` + id + `","method":"` + method + `","params":{` + params + `}}`
}

// call runs one tool and returns its text and isError flag.
func (w *wire) call(name string, args any) (string, bool) {
	w.t.Helper()
	body, _ := json.Marshal(args)
	res := w.request("tools/call", `"name":"`+name+`","arguments":`+string(body))
	if res["error"] != nil {
		w.t.Fatalf("%s: protocol error %v", name, res["error"])
	}
	result := res["result"].(map[string]any)
	content := result["content"].([]any)
	isError, _ := result["isError"].(bool)
	return content[0].(map[string]any)["text"].(string), isError
}

func resultOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", m)
	}
	return r
}

func errorCode(m map[string]any) int {
	e, ok := m["error"].(map[string]any)
	if !ok {
		return 0
	}
	c, _ := e["code"].(float64)
	return int(c)
}

// ---- fixtures ----

var demoNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// demoDashboard is the real dashboard handler (real HTTP, real session proof, real audit engine) over the demo repository.
func demoDashboard(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(server.New(audit.NewDemo(demoNow), nil, testSessionToken, true))
	t.Cleanup(srv.Close)
	return srv
}

func bridgeFor(t *testing.T, srv *httptest.Server, opts ...Option) *Bridge {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	b, err := New(port, append([]Option{WithToken(testSessionToken)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// blockingUpstream is a dashboard whose data routes never answer until the caller gives up. hits counts the requests that arrived and
// cancelled the requests the bridge abandoned.
func blockingUpstream(t *testing.T) (b *Bridge, hits, cancelled *atomic.Int32) {
	t.Helper()
	hits, cancelled = new(atomic.Int32), new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-r.Context().Done()
		cancelled.Add(1)
	}))
	t.Cleanup(srv.Close)
	return newBridge(fixed(srv.URL), srv.Client()), hits, cancelled
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

// canonical decodes JSON and returns it for DeepEqual.
func canonical(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// ---- lifecycles ----

func TestSDKBothLifecycles(t *testing.T) {
	srv := demoDashboard(t)
	t.Run("handshake 2025-11-25", func(t *testing.T) {
		w := dial(t, sdkImpl, bridgeFor(t, srv))
		res := resultOf(t, w.handshake())
		if res["protocolVersion"] != "2025-11-25" {
			t.Fatalf("negotiated %v", res["protocolVersion"])
		}
		if res["serverInfo"].(map[string]any)["name"] != "unravis" || res["instructions"] != serverInstructions {
			t.Fatalf("identity %v", res)
		}
		list := resultOf(t, w.request("tools/list", ""))["tools"].([]any)
		if len(list) != 10 {
			t.Fatalf("%d tools", len(list))
		}
		text, isError := w.call("audit_list_sources", map[string]any{})
		if isError || !strings.Contains(text, "demo-west") {
			t.Fatalf("list_sources: %v %.200s", isError, text)
		}
		if err := w.finish(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("stateless 2026-07-28", func(t *testing.T) {
		w := dial(t, sdkImpl, bridgeFor(t, srv))
		w.stateless = true
		// No initialize anywhere: the first thing a 2026-07-28 client may send is server/discover, or a tool call outright.
		disc := resultOf(t, w.request("server/discover", ""))
		versions := fmt.Sprint(disc["supportedVersions"])
		if !strings.Contains(versions, "2026-07-28") || !strings.Contains(versions, "2025-11-25") {
			t.Fatalf("supportedVersions %v", versions)
		}
		if disc["instructions"] != serverInstructions {
			t.Fatal("instructions missing from discover")
		}
		info := disc["_meta"].(map[string]any)["io.modelcontextprotocol/serverInfo"].(map[string]any)
		if info["name"] != "unravis" {
			t.Fatalf("serverInfo %v", info)
		}
		if n := len(resultOf(t, w.request("tools/list", ""))["tools"].([]any)); n != 10 {
			t.Fatalf("%d tools", n)
		}
		text, isError := w.call("audit_list_sources", map[string]any{})
		if isError || !strings.Contains(text, "demo-west") {
			t.Fatalf("list_sources: %v %.200s", isError, text)
		}
		// A version the server does not speak is refused with the list of those it does (SEP-2575), not guessed at.
		w.seq++
		w.send(`{"jsonrpc":"2.0","id":"v","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`)
		if code := errorCode(w.reply("v")); code != -32022 {
			t.Fatalf("unsupported version code %d", code)
		}
		// Per-request envelope without capabilities is an invalid request, not a silent default.
		w.send(`{"jsonrpc":"2.0","id":"c","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
		if code := errorCode(w.reply("c")); code != -32602 {
			t.Fatalf("missing capabilities code %d", code)
		}
		if err := w.finish(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("hand-rolled loop has no stateless lifecycle", func(t *testing.T) {
		// Characterization of the loop BL-342 would replace: a 2026-07-28 client that skips initialize gets "initialize first".
		w := dial(t, legacyImpl, bridgeFor(t, srv))
		w.stateless = true
		res := w.request("tools/list", "")
		if res["error"] == nil {
			t.Fatalf("hand-rolled loop answered a stateless request: %.200v", res)
		}
	})
	t.Run("version negotiation matches", func(t *testing.T) {
		for _, version := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25", "2026-07-28", "1999-01-01"} {
			got := map[string]any{}
			for _, im := range bothImpls {
				w := dial(t, im, bridgeFor(t, srv))
				w.send(`{"jsonrpc":"2.0","id":"i","method":"initialize","params":{"protocolVersion":"` + version + `","capabilities":{},"clientInfo":{"name":"x","version":"1"}}}`)
				got[im.name] = resultOf(t, w.reply("i"))["protocolVersion"]
			}
			if got["legacy"] != got["sdk"] {
				t.Errorf("initialize %s: legacy answers %v, sdk %v", version, got["legacy"], got["sdk"])
			}
		}
	})
}

// ---- catalog ----

var wantToolOrder = []string{"audit_get_activity", "audit_get_guide", "audit_list_sources", "audit_compare", "audit_get_groups", "audit_get_timeline", "audit_compare_timelines", "audit_get_requests", "audit_get_slowest", "audit_get_breakdown"}

func wireCatalog(t *testing.T, im impl, stateless bool) []any {
	w := dial(t, im, bridgeFor(t, demoDashboard(t)))
	if stateless {
		w.stateless = true
	} else {
		w.handshake()
	}
	return resultOf(t, w.request("tools/list", ""))["tools"].([]any)
}

func TestSDKCatalogParity(t *testing.T) {
	want := canonical(t, tools()).([]any)
	for _, c := range []struct {
		name      string
		im        impl
		stateless bool
	}{{"legacy", legacyImpl, false}, {"sdk handshake", sdkImpl, false}, {"sdk stateless", sdkImpl, true}} {
		t.Run(c.name, func(t *testing.T) {
			got := wireCatalog(t, c.im, c.stateless)
			var names []string
			for _, tool := range got {
				names = append(names, tool.(map[string]any)["name"].(string))
			}
			if !reflect.DeepEqual(names, wantToolOrder) {
				t.Fatalf("catalog order %v", names)
			}
			// Names, descriptions, input schemas and annotations: equal to the catalog the hand-rolled loop advertises, tool by tool.
			for i := range want {
				if !reflect.DeepEqual(got[i], want[i]) {
					g, _ := json.Marshal(got[i])
					w, _ := json.Marshal(want[i])
					t.Errorf("tool %s differs:\n got  %.600s\n want %.600s", names[i], g, w)
				}
			}
			for _, tool := range got {
				if tool.(map[string]any)["annotations"].(map[string]any)["readOnlyHint"] != true {
					t.Errorf("%v is not read-only", tool.(map[string]any)["name"])
				}
			}
		})
	}
}

// TestCatalogDigest pins the catalog: a change to a tool name, description or schema shows here and must be reviewed.
func TestCatalogDigest(t *testing.T) {
	sum := sha256.Sum256(mustJSON(t, tools()))
	got := hex.EncodeToString(sum[:])
	path := "testdata/catalog.sha256"
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(want)) != got {
		t.Fatalf("catalog changed (sha256 %s); review the change and update the golden with: go test ./internal/mcp -run TestCatalogDigest -update", got)
	}
}

// TestCatalogOrder pins the fixed order of the catalog (not the SDK's own, by name): the order tools/list shows on both lifecycles.
func TestCatalogOrder(t *testing.T) {
	var names []string
	for _, d := range tools() {
		names = append(names, d["name"].(string))
	}
	if !reflect.DeepEqual(names, wantToolOrder) {
		t.Fatalf("catalog order %v", names)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ---- the ten tools on the real dashboard path ----

var (
	ridPattern     = regexp.MustCompile(`"resultId":"[0-9a-f]{32}"`)
	elapsedPattern = regexp.MustCompile(`"elapsedMs":[0-9.]+`) // how long the demo query took: differs run to run
)

// tenCalls runs every tool once against the dashboard behind c and returns each call's normalized text. Arguments after the first compare
// call come out of earlier answers, as a model would read them.
func tenCalls(t *testing.T, call func(name string, args any) (string, bool)) map[string]string {
	t.Helper()
	out := map[string]string{}
	rec := func(name string, args any) string {
		t.Helper()
		text, isError := call(name, args)
		if isError {
			t.Fatalf("%s failed: %.300s", name, text)
		}
		out[name] = elapsedPattern.ReplaceAllString(ridPattern.ReplaceAllString(text, `"resultId":"RID"`), `"elapsedMs":0`)
		return text
	}
	rec("audit_list_sources", map[string]any{})
	rec("audit_get_guide", map[string]any{})
	query := map[string]any{
		"sourceIds": []string{"demo-west", "demo-east"},
		"start":     demoNow.Add(-15 * time.Minute).Format(time.RFC3339), "end": demoNow.Format(time.RFC3339),
		"baselineStart": demoNow.Add(-30 * time.Minute).Format(time.RFC3339), "baselineEnd": demoNow.Add(-15 * time.Minute).Format(time.RFC3339),
		"kind": "all", "outcome": "all", "metric": "total", "bucketSeconds": 60,
	}
	var compared struct {
		ResultID string `json:"resultId"`
		Groups   []struct {
			Key string `json:"key"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(rec("audit_compare", map[string]any{"query": query, "limit": 5})), &compared); err != nil || compared.ResultID == "" || len(compared.Groups) == 0 {
		t.Fatalf("compare returned no groups: %v", err)
	}
	rid, key := compared.ResultID, compared.Groups[0].Key
	rec("audit_get_groups", map[string]any{"resultId": rid, "limit": 5})
	rec("audit_get_timeline", map[string]any{"resultId": rid, "apiKey": key})
	rec("audit_compare_timelines", map[string]any{"resultId": rid, "apiKeys": []string{key}})
	rec("audit_get_requests", map[string]any{"resultId": rid, "apiKey": key, "limit": 3})
	rec("audit_get_slowest", map[string]any{"resultId": rid, "limit": 3})
	rec("audit_get_breakdown", map[string]any{"resultId": rid, "dim": "statusCode"})
	rec("audit_get_activity", map[string]any{"sourceIds": []string{"demo-west"}, "start": query["start"], "end": query["end"], "limit": 5})
	return out
}

func TestServeParity(t *testing.T) {
	srv := demoDashboard(t)
	run := func(im impl, stateless bool) map[string]string {
		w := dial(t, im, bridgeFor(t, srv))
		if stateless {
			w.stateless = true
		} else {
			w.handshake()
		}
		return tenCalls(t, w.call)
	}
	legacy := run(legacyImpl, false)
	if len(legacy) != 10 {
		t.Fatalf("legacy loop answered %d of the ten tools", len(legacy))
	}
	for name, got := range map[string]map[string]string{"sdk handshake": run(sdkImpl, false), "sdk stateless": run(sdkImpl, true)} {
		for tool, text := range legacy {
			if got[tool] != text {
				i := 0
				for i < len(text) && i < len(got[tool]) && text[i] == got[tool][i] {
					i++
				}
				t.Errorf("%s: %s answer differs from the hand-rolled loop at byte %d:\n got  ...%.200s\n want ...%.200s", name, tool, i, got[tool][max(0, i-60):], text[max(0, i-60):])
			}
		}
		if len(got) != 10 {
			t.Errorf("%s answered %d of the ten tools", name, len(got))
		}
	}
}

// identifiers stay off by default and on with --mcp-allow-identifiers, through the SDK loop too (the switch lives in the bridge).
func TestSDKIdentifierSwitchStillDecides(t *testing.T) {
	srv := demoDashboard(t)
	filter := map[string]any{"query": map[string]any{
		"sourceIds": []string{"demo-west"}, "start": demoNow.Add(-15 * time.Minute).Format(time.RFC3339), "end": demoNow.Format(time.RFC3339),
		"baselineStart": demoNow.Add(-30 * time.Minute).Format(time.RFC3339), "baselineEnd": demoNow.Add(-15 * time.Minute).Format(time.RFC3339),
		"kind": "all", "outcome": "all", "metric": "total", "bucketSeconds": 60, "userIds": []int{1000}}}
	for _, allowed := range []bool{false, true} {
		w := dial(t, sdkImpl, bridgeFor(t, srv, WithIdentifiers(allowed)))
		w.handshake()
		text, isError := w.call("audit_compare", filter)
		if refused := isError && strings.Contains(text, "--mcp-allow-identifiers"); refused == allowed {
			t.Errorf("identifiers=%v: isError=%v text=%.160s", allowed, isError, text)
		}
	}
}

var _ = bytes.NewReader
