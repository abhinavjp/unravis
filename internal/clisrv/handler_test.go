package clisrv

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/mcp"
	dashboard "api-audit-explorer/internal/server"
)

const rid = "req_0123456789abcdef"

type fakeBackend struct {
	fn func(ctx context.Context, tool string, args json.RawMessage) (any, *clicore.Error)
}

func (f fakeBackend) Call(ctx context.Context, tool string, args json.RawMessage) (any, *clicore.Error) {
	return f.fn(ctx, tool, args)
}

func newHandler(b Backend) http.Handler {
	return Handler(Deps{Backend: b, Info: func() Info { return Info{PID: 42, Gen: "133", AppVersion: "1.2.3", Owner: "app"} }, Logf: func(string, ...any) {}})
}

type reply struct {
	Status int
	OK     bool                                   `json:"ok"`
	Data   map[string]any                         `json:"data"`
	Meta   map[string]any                         `json:"meta"`
	Error  struct{ Code, Message, Action string } `json:"error"`
	Raw    string
}

func post(t *testing.T, h http.Handler, path string, body string, hdr ...string) reply {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out reply
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil && w.Code == 200 {
		t.Fatalf("not JSON: %q", w.Body.String())
	}
	out.Status, out.Raw = w.Code, w.Body.String()
	return out
}

func op(t *testing.T, h http.Handler, name, args string) reply {
	t.Helper()
	if args == "" {
		args = "{}"
	}
	return post(t, h, "/cli/v1/op", `{"op":"`+name+`","args":`+args+`,"requestId":"`+rid+`"}`)
}

func echo() Backend {
	return fakeBackend{func(_ context.Context, tool string, args json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"tool": tool, "args": json.RawMessage(args)}, nil
	}}
}

func TestHello(t *testing.T) {
	h := newHandler(echo())
	r := httptest.NewRequest("GET", "/cli/v1/hello", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got helloBody
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	want := helloBody{V: 1, PID: 42, Gen: "133", Proto: protoBody{1, 0}, AppVersion: "1.2.3", Owner: "app", State: "ready"}
	if got != want {
		t.Errorf("%+v", got)
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "token") || strings.Contains(strings.ToLower(w.Body.String()), "proof") {
		t.Errorf("hello carries a token or a proof: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/cli/v1/hello", nil))
	if w.Code != 405 {
		t.Errorf("POST hello: %d", w.Code)
	}
}

// Deny by default: every row of the matrix that the human command line may not run is refused with PERMISSION_DENIED before any
// handler runs, and an operation with no row does not exist.
func TestGateIsTheMatrix(t *testing.T) {
	called := false
	h := newHandler(fakeBackend{func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		called = true
		return map[string]any{}, nil
	}})
	for _, row := range clicap.Rows() {
		called = false
		r := op(t, h, row.Op, "")
		switch {
		case row.Human == clicap.Deny:
			if r.OK || r.Error.Code != "PERMISSION_DENIED" || called {
				t.Errorf("%s: %+v called=%v", row.Op, r, called)
			}
		case row.Human == clicap.NA:
			// an offline operation: refused too
			if r.OK || called {
				t.Errorf("%s: %+v", row.Op, r)
			}
		case row.Tool != "" || row.Op == "app.status":
			if !r.OK {
				t.Errorf("%s: %+v", row.Op, r)
			}
		case row.Op == "cases.list" || row.Op == "cases.show":
			// built: show wants an id first, and list finds no case store behind this handler
			want := "ENDPOINT_UNAVAILABLE"
			if row.Op == "cases.show" {
				want = "INVALID_ARGUMENT"
			}
			if r.OK || r.Error.Code != want {
				t.Errorf("%s: %+v", row.Op, r)
			}
		default:
			if r.OK || r.Error.Code != "UNKNOWN_OPERATION" {
				t.Errorf("%s (not built): %+v", row.Op, r)
			}
		}
	}
	for _, name := range []string{"nosuch", "audit.delete", "Audit.Compare", "../x", ""} {
		if r := op(t, h, name, ""); r.OK || r.Error.Code != "UNKNOWN_OPERATION" {
			t.Errorf("%q: %+v", name, r)
		}
	}
}

func TestRequestIsStrict(t *testing.T) {
	h := newHandler(echo())
	for name, body := range map[string]string{
		"unknown field (a principal)": `{"op":"audit.guide","args":{},"requestId":"` + rid + `","principal":"cli-ci"}`,
		"bad request id":              `{"op":"audit.guide","args":{},"requestId":"x"}`,
		"no request id":               `{"op":"audit.guide","args":{}}`,
		"bad call id":                 `{"op":"audit.guide","args":{},"requestId":"` + rid + `","callId":"../x"}`,
		"not an object":               `[]`,
		"trailing data":               `{"op":"audit.guide","requestId":"` + rid + `"} {}`,
		"empty":                       ``,
		"repeated name in args":       `{"op":"audit.guide","args":{"a":1,"a":2},"requestId":"` + rid + `"}`,
		"repeated name deep":          `{"op":"audit.guide","args":{"a":{"b":[{"c":1,"c":2}]}},"requestId":"` + rid + `"}`,
		"args not an object":          `{"op":"audit.guide","args":[1],"requestId":"` + rid + `"}`,
	} {
		r := post(t, h, "/cli/v1/op", body)
		if r.OK || r.Error.Code != "INVALID_ARGUMENT" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// the same name in two different objects is fine
	if r := op(t, h, "audit.guide", `{"a":{"x":1},"b":{"x":2}}`); !r.OK {
		t.Errorf("distinct objects: %+v", r)
	}
	big := `{"op":"audit.guide","args":{"p":"` + strings.Repeat("a", MaxRequestBytes) + `"},"requestId":"` + rid + `"}`
	if r := post(t, h, "/cli/v1/op", big); r.OK || r.Error.Code != "REQUEST_TOO_LARGE" {
		t.Errorf("large: %+v", r)
	}
	if r := post(t, h, "/cli/v1/op", `{"op":"audit.guide","requestId":"`+rid+`"}`, "Origin", "http://evil.example"); r.Status != 403 {
		t.Errorf("origin: %d", r.Status)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/cli/v1/op", nil))
	if w.Code != 405 {
		t.Errorf("GET: %d", w.Code)
	}
}

func TestAnswerShapeAndMeta(t *testing.T) {
	h := newHandler(fakeBackend{func(_ context.Context, tool string, _ json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"sources": []map[string]any{{"sourceId": "a", "ok": true}, {"sourceId": "b", "ok": false}}}, nil
	}})
	r := op(t, h, "audit.compare", "")
	if !r.OK || r.Meta["coverage"] != "partial" || r.Meta["identifiers"] != "shown" || len(r.Meta["warnings"].([]any)) != 1 {
		t.Errorf("partial: %+v", r)
	}
	h = newHandler(echo())
	r = op(t, h, "audit.guide", "")
	if !r.OK || r.Meta["coverage"] != "complete" || r.Data["tool"] != "audit_get_guide" {
		t.Errorf("complete: %+v", r)
	}
	// a failure keeps the code and says nothing is retryable
	h = newHandler(fakeBackend{func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return nil, clicore.NewErrorAfterWork(clicore.CodeResultExpired, "gone", "again")
	}})
	r = op(t, h, "audit.groups", `{"resultId":"x"}`)
	if r.OK || r.Error.Code != "RESULT_EXPIRED" || r.Error.Action != "again" || strings.Contains(r.Raw, `"retryable":true`) {
		t.Errorf("error: %+v", r)
	}
}

func TestTooLargeAnswerIsRefused(t *testing.T) {
	h := newHandler(fakeBackend{func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"x": strings.Repeat("a", MaxResponseBytes)}, nil
	}})
	if r := op(t, h, "audit.guide", ""); r.OK || r.Error.Code != "RESULT_TOO_LARGE" {
		t.Errorf("%.200s", r.Raw)
	}
}

func TestAppStatus(t *testing.T) {
	h := newHandler(echo())
	r := op(t, h, "app.status", "")
	if !r.OK || r.Data["running"] != true || r.Data["pid"] != float64(42) || r.Data["owner"] != "app" || r.Data["state"] != "ready" {
		t.Errorf("%+v", r)
	}
	if r := op(t, h, "app.status", `{"x":1}`); r.OK {
		t.Errorf("args are refused: %+v", r)
	}
}

func TestCancelAndDeadline(t *testing.T) {
	before := runtime.NumGoroutine()
	started := make(chan struct{})
	h := newHandler(fakeBackend{func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		close(started)
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			return nil, clicore.NewErrorAfterWork(clicore.CodeTimeout, "slow", "")
		}
		return nil, clicore.NewErrorAfterWork(clicore.CodeCanceled, "stopped", "")
	}})
	done := make(chan reply)
	go func() {
		done <- post(t, h, "/cli/v1/op", `{"op":"audit.compare","args":{},"requestId":"`+rid+`","callId":"0123456789abcdef"}`)
	}()
	<-started
	if c := post(t, h, "/cli/v1/cancel", `{"callId":"0123456789abcdef"}`); c.Status != 200 || !strings.Contains(c.Raw, "cancelling") {
		t.Errorf("cancel: %+v", c)
	}
	if r := <-done; r.OK || r.Error.Code != "CANCELED" {
		t.Errorf("canceled call: %+v", r)
	}
	if c := post(t, h, "/cli/v1/cancel", `{"callId":"0123456789abcdef"}`); !strings.Contains(c.Raw, `"none"`) {
		t.Errorf("cancel of a finished call: %+v", c)
	}
	if c := post(t, h, "/cli/v1/cancel", `{"callId":"x"}`); c.Status != 400 {
		t.Errorf("bad id: %+v", c)
	}
	// a deadline
	h = newHandler(fakeBackend{func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		<-ctx.Done()
		return nil, clicore.NewErrorAfterWork(clicore.CodeTimeout, "slow", "")
	}})
	if r := post(t, h, "/cli/v1/op", `{"op":"audit.compare","args":{},"requestId":"`+rid+`","deadlineMs":20}`); r.OK || r.Error.Code != "TIMEOUT" {
		t.Errorf("deadline: %+v", r)
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutines: %d before, %d after", before, after)
	}
}

func TestOnlyFourCallsAtOnce(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 16)
	h := newHandler(fakeBackend{func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return map[string]any{}, nil
	}})
	var wg sync.WaitGroup
	for i := 0; i < MaxInFlight; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); op(t, h, "audit.compare", "") }()
	}
	for i := 0; i < MaxInFlight; i++ {
		<-started
	}
	if r := op(t, h, "audit.compare", ""); r.OK || r.Error.Code != "OWNER_BUSY" {
		t.Errorf("fifth call: %+v", r)
	}
	close(release)
	wg.Wait()
	if r := op(t, h, "audit.guide", ""); !r.OK {
		t.Errorf("after: %+v", r)
	}
}

func TestDuplicateKeys(t *testing.T) {
	for in, want := range map[string]bool{
		`{}`: false, `{"a":1,"b":2}`: false, `{"a":1,"a":2}`: true, `{"a":{"a":1}}`: false, `{"a":[{"b":1},{"b":2}]}`: false,
		`{"a":[{"b":1,"b":2}]}`: true, `{"a":"a","b":"a"}`: false, `{"a":null,"a":null}`: true, `{"a":1,"b":{"c":1},"a":3}`: true,
		`[{"a":1},{"a":1}]`: false, `{"a":[1,2,3],"b":[1,2,3]}`: false,
	} {
		if dup, bad := duplicateKeys([]byte(in)); bad || dup != want {
			t.Errorf("%s: dup=%v bad=%v, want %v", in, dup, bad, want)
		}
	}
	for _, in := range []string{`{`, `{"a"`, `{"a":1`, `nul`, `{"a":}`} {
		if _, bad := duplicateKeys([]byte(in)); !bad {
			t.Errorf("%s is not valid", in)
		}
	}
}

// The real path: the command line routes, the tool code of the MCP bridge, the dashboard handler and its repository, all in one process
// with no network. The repository is the demo data set (the real sources come only on a PC).
func TestRealPathOverTheDashboardHandler(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)
	const token = "tok-0123456789abcdef0123456789abcdef"
	dash := dashboard.New(audit.NewDemo(now), nil, token, false)
	h := newHandler(MCPBackend(mcp.NewLocal(dash, token, mcp.WithIdentifiers(true))))

	src := op(t, h, "sources.list", "")
	if !src.OK || len(src.Data["sources"].([]any)) != 2 {
		t.Fatalf("sources: %.300s", src.Raw)
	}
	args, _ := json.Marshal(map[string]any{"query": audit.Query{
		SourceIDs: []string{"demo-west", "demo-east"}, Start: now.Add(-1 * time.Hour), End: now, BaselineStart: now.Add(-25 * time.Hour), BaselineEnd: now.Add(-24 * time.Hour),
		Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 60}})
	cmp := op(t, h, "audit.compare", string(args))
	if !cmp.OK {
		t.Fatalf("compare: %.400s", cmp.Raw)
	}
	id, _ := cmp.Data["resultId"].(string)
	if len(id) != 32 || cmp.Meta["coverage"] != "complete" || cmp.Meta["identifiers"] != "shown" {
		t.Fatalf("compare data: %.400s", cmp.Raw)
	}
	// a follow-up from a "new process" (a new request) pages the same result with no second query
	page := op(t, h, "audit.groups", `{"resultId":"`+id+`","limit":2}`)
	if !page.OK || len(page.Data["groups"].([]any)) == 0 || len(page.Data["groups"].([]any)) > 2 {
		t.Errorf("groups: %.400s", page.Raw)
	}
	// an unknown or expired handle, a bad argument and a name the tool does not take
	if r := op(t, h, "audit.groups", `{"resultId":"00000000000000000000000000000000"}`); r.OK || r.Error.Code != "RESULT_EXPIRED" || r.Error.Action == "" {
		t.Errorf("expired: %+v", r)
	}
	if r := op(t, h, "audit.groups", `{"resultId":"`+id+`","limit":9999}`); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
		t.Errorf("limit: %+v", r)
	}
	if r := op(t, h, "audit.guide", `{"principal":"cli-ci"}`); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
		t.Errorf("principal in args: %+v", r)
	}
	if r := op(t, h, "audit.compare", `{"query":{}}`); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
		t.Errorf("empty query: %+v", r)
	}
	// a source that is not connected
	bad := bytes.Replace(args, []byte("demo-west"), []byte("nosuch"), 1)
	if r := op(t, h, "audit.compare", string(bad)); r.OK {
		t.Errorf("unknown source accepted: %.300s", r.Raw)
	}
	// nothing in any answer carries the session token
	if strings.Contains(src.Raw+cmp.Raw+page.Raw, token) {
		t.Errorf("the session token is in an answer")
	}
}

// Names are matched without regard to case by the decoders here and in the tool code, and the last one wins: a name given twice in
// two cases would say two things at once, so it is refused, in the envelope and in the arguments, also with the Kelvin sign and long s.
func TestRepeatedNamesInAnyCaseAreRefused(t *testing.T) {
	h := newHandler(echo())
	for name, body := range map[string]string{
		"op twice":                `{"op":"sources.list","op":"audit.guide","requestId":"` + rid + `"}`,
		"op in two cases":         `{"op":"sources.list","OP":"audit.guide","requestId":"` + rid + `"}`,
		"request id in two cases": `{"op":"audit.guide","requestId":"` + rid + `","RequestID":"` + rid + `"}`,
		"args in two cases":       `{"op":"audit.groups","args":{"resultId":"a","RESULTID":"b"},"requestId":"` + rid + `"}`,
		"Kelvin sign":             `{"op":"audit.guide","args":{"k":1,"K":2},"requestId":"` + rid + `"}`,
		"long s":                  `{"op":"audit.guide","args":{"s":1,"ſ":2},"requestId":"` + rid + `"}`,
		"deep, in two cases":      `{"op":"audit.guide","args":{"a":{"b":[{"X":1,"x":2}]}},"requestId":"` + rid + `"}`,
	} {
		if r := post(t, h, "/cli/v1/op", body); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if c := post(t, h, "/cli/v1/cancel", `{"callId":"0123456789abcdef","CallID":"0123456789abcdef"}`); c.Status != 400 {
		t.Errorf("cancel with a repeated name: %d", c.Status)
	}
	if dup, bad := duplicateKeys([]byte(`{"a":1,"A":2}`)); !dup || bad {
		t.Errorf("fold: %v %v", dup, bad)
	}
	if dup, _ := duplicateKeys([]byte(`{"a":1,"b":2}`)); dup {
		t.Error("distinct names")
	}
}

func TestPrincipalNeverFromRequestJSON(t *testing.T) {
	var gotTool string
	h := newHandler(fakeBackend{func(_ context.Context, tool string, args json.RawMessage) (any, *clicore.Error) {
		gotTool = tool
		return map[string]any{}, nil
	}})
	for name, body := range map[string]string{
		"in the envelope": `{"op":"audit.guide","principal":"cli-ci","requestId":"` + rid + `"}`,
		"profile":         `{"op":"audit.guide","profile":"cli-ci","requestId":"` + rid + `"}`,
		"hostName":        `{"op":"audit.guide","hostName":"cli","requestId":"` + rid + `"}`,
	} {
		gotTool = ""
		if r := post(t, h, "/cli/v1/op", body); r.OK || gotTool != "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// and in the arguments the tool code refuses it (checked on the real path in TestRealPathOverTheDashboardHandler)
	if r := op(t, h, "audit.guide", `{"principal":"x"}`); !r.OK || gotTool != "audit_get_guide" {
		t.Errorf("a fake backend accepts any arguments; the tool code is what refuses: %+v", r)
	}
}

func TestSameCallIDCannotHideACall(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	h := newHandler(fakeBackend{func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, clicore.NewErrorAfterWork(clicore.CodeCanceled, "stopped", "")
	}})
	done := make(chan reply)
	body := `{"op":"audit.compare","args":{},"requestId":"` + rid + `","callId":"0123456789abcdef"}`
	go func() { done <- post(t, h, "/cli/v1/op", body) }()
	<-started
	// the same id again is refused, so the first call stays cancellable
	if r := post(t, h, "/cli/v1/op", body); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
		t.Errorf("second call with the same id: %+v", r)
	}
	if c := post(t, h, "/cli/v1/cancel", `{"callId":"0123456789abcdef"}`); !strings.Contains(c.Raw, "cancelling") {
		t.Errorf("cancel after a refused duplicate: %+v", c)
	}
	<-stopped
	if r := <-done; r.OK {
		t.Errorf("%+v", r)
	}
}

func TestMetaIsComplete(t *testing.T) {
	h := newHandler(echo())
	r := op(t, h, "audit.guide", "")
	if !strings.Contains(r.Raw, `"warnings":[]`) || strings.Contains(r.Raw, `"warnings":null`) {
		t.Errorf("warnings must be an empty list: %s", r.Raw)
	}
	h = newHandler(fakeBackend{func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"rows": []any{}, "truncated": true}, nil
	}})
	r = op(t, h, "audit.requests", `{}`)
	if r.Meta["coverage"] != "partial" || r.Meta["truncated"] == nil || len(r.Meta["warnings"].([]any)) != 1 {
		t.Errorf("a cut page: %+v", r.Meta)
	}
	h = newHandler(fakeBackend{func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"rows": []any{}, "truncated": false}, nil
	}})
	if r = op(t, h, "audit.requests", `{}`); r.Meta["coverage"] != "complete" || r.Meta["truncated"] != nil {
		t.Errorf("a whole page: %+v", r.Meta)
	}
}

func TestDeadlines(t *testing.T) {
	var left time.Duration
	h := newHandler(fakeBackend{func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		d, ok := ctx.Deadline()
		if !ok {
			left = -1
		} else {
			left = time.Until(d)
		}
		return map[string]any{}, nil
	}})
	for name, tc := range map[string]struct {
		ms       string
		min, max time.Duration
	}{
		"none":        {``, 25 * time.Second, 31 * time.Second},
		"zero":        {`,"deadlineMs":0`, 25 * time.Second, 31 * time.Second},
		"negative":    {`,"deadlineMs":-5`, 25 * time.Second, 31 * time.Second},
		"two minutes": {`,"deadlineMs":120000`, 110 * time.Second, 121 * time.Second},
		"huge":        {`,"deadlineMs":10000000000000`, 59 * time.Minute, 61 * time.Minute},
		"just over":   {`,"deadlineMs":3600001`, 59 * time.Minute, 61 * time.Minute},
	} {
		left = 0
		post(t, h, "/cli/v1/op", `{"op":"audit.guide","args":{},"requestId":"`+rid+`"`+tc.ms+`}`)
		if left < tc.min || left > tc.max {
			t.Errorf("%s: %v left", name, left)
		}
	}
}

func TestOriginAndIDsOnEveryRoute(t *testing.T) {
	h := newHandler(echo())
	for _, path := range []string{"/cli/v1/op", "/cli/v1/cancel"} {
		if r := post(t, h, path, `{"op":"audit.guide","requestId":"`+rid+`","callId":"0123456789abcdef"}`, "Origin", "https://evil.example"); r.Status != 403 {
			t.Errorf("%s with an Origin: %d", path, r.Status)
		}
	}
	for _, id := range []string{"req_", "req_x", "req_0123456789abcdefg", "REQ_0123456789abcdef", "req_0123456789ABCDEF"} {
		if r := post(t, h, "/cli/v1/op", `{"op":"audit.guide","requestId":"`+id+`"}`); r.OK || r.Error.Code != "INVALID_ARGUMENT" {
			t.Errorf("request id %q: %+v", id, r)
		}
	}
}

func TestHelloStateIsOneOfThree(t *testing.T) {
	for state, want := range map[string]string{"ready": "ready", "locked": "locked", "updating": "updating", "weird": "ready", "": "ready"} {
		h := Handler(Deps{Backend: echo(), Info: func() Info { return Info{} }, State: func() string { return state }, Logf: func(string, ...any) {}})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/cli/v1/hello", nil))
		if !strings.Contains(w.Body.String(), `"state":"`+want+`"`) {
			t.Errorf("%q: %s", state, w.Body.String())
		}
	}
}
