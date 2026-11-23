package cmdtree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/pipesec"
)

// Tests that came out of the review of the command line side (PR 224): each one fails when the line it guards is removed.

func (o *owner) helloBody(mut func(map[string]any)) string {
	m := map[string]any{"v": 1, "pid": o.rec.PID, "gen": o.rec.StartTime.UTC().Format(time.RFC3339Nano), "appVersion": "1.2.3", "owner": "app",
		"state": "ready", "proto": map[string]int{"major": 1, "minor": 0}}
	if mut != nil {
		mut(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// serve replaces the owner's routes: hello answers with status and body, and op runs opFn (an op that must not be reached passes nil).
func (o *owner) serve(status int, hello string, opFn http.HandlerFunc) {
	mux := http.NewServeMux()
	mux.HandleFunc("/cli/v1/hello", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, hello)
	})
	mux.HandleFunc("/cli/v1/op", func(w http.ResponseWriter, r *http.Request) {
		if opFn == nil {
			o.t.Error("an operation was sent")
			return
		}
		opFn(w, r)
	})
	o.srv.Handler = mux
}

func TestCtrlCStopsTheCallAndTellsTheOwner(t *testing.T) {
	started := make(chan struct{})
	o := newOwner(t, fakeBackend(func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		close(started)
		<-ctx.Done()
		return nil, clicore.NewErrorAfterWork(clicore.CodeCanceled, "stopped", "")
	}))
	var cancels atomic.Int32
	inner := o.srv.Handler
	o.srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cli/v1/cancel" {
			cancels.Add(1)
		}
		inner.ServeHTTP(w, r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan result, 1)
	go func() { done <- runCtx(t, ctx, o.attacher(), "", nil, "audit", "guide", "--json", "--timeout", "30s") }()
	<-started
	cancel()
	select {
	case res := <-done:
		if res.code != 6 || errCode(t, res) != "CANCELED" {
			t.Errorf("%+v", res)
		}
	case <-time.After(5 * time.Second): // the connection is closed at once; waiting for the owner or the 30 s deadline is the failure
		t.Fatal("Ctrl+C did not stop the command")
	}
	time.Sleep(200 * time.Millisecond)
	if cancels.Load() != 1 {
		t.Errorf("cancel requests: %d", cancels.Load())
	}
}

func TestEndOfContextWinsOverBusyPipe(t *testing.T) {
	o := newOwner(t, nil)
	a := o.attacher()
	a.Dial = func(ctx context.Context, _ locator.Record) (net.Conn, error) {
		<-ctx.Done()
		return nil, fmt.Errorf("%w: %w", pipesec.ErrPipeBusy, ctx.Err())
	}
	r := runOn(t, a, "", nil, "sources", "list", "--timeout", "100ms", "--json")
	if r.code != 6 || errCode(t, r) != "TIMEOUT" {
		t.Errorf("deadline while busy: %+v", r)
	}
	a.Dial = func(context.Context, locator.Record) (net.Conn, error) { return nil, pipesec.ErrPipeBusy }
	r = runOn(t, a, "", nil, "sources", "list", "--json")
	if r.code != 3 || errCode(t, r) != "OWNER_BUSY" || decode(t, r.err)["error"].(map[string]any)["retryable"] != true {
		t.Errorf("busy: %+v", r)
	}
}

func TestOwnerBusyRetryRuleFollowsTheOperation(t *testing.T) {
	busy := fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return nil, clicore.NewErrorAfterWork(clicore.CodeOwnerBusy, "busy", "")
	})
	o := newOwner(t, busy)
	r := o.run("sources", "list", "--json")
	if r.code != 3 || decode(t, r.err)["error"].(map[string]any)["retryable"] != true {
		t.Errorf("a cheap read may be asked again: %+v", r)
	}
	r = o.run(append([]string{"audit", "compare", "--json"}, o.window()...)...)
	if r.code != 3 || decode(t, r.err)["error"].(map[string]any)["retryable"] != false {
		t.Errorf("a live query is never repeated after it was sent: %+v", r)
	}
}

func TestHelloIsCheckedStrictly(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   func(o *owner) string
	}{
		"status 500":    {500, func(o *owner) string { return o.helloBody(nil) }},
		"text after":    {200, func(o *owner) string { return o.helloBody(nil) + ` {"x":1}` }},
		"unknown state": {200, func(o *owner) string { return o.helloBody(func(m map[string]any) { m["state"] = "weird" }) }},
		"generation early": {200, func(o *owner) string {
			return o.helloBody(func(m map[string]any) { m["gen"] = o.rec.StartTime.Add(-5 * time.Second).Format(time.RFC3339Nano) })
		}},
		"generation late": {200, func(o *owner) string {
			return o.helloBody(func(m map[string]any) { m["gen"] = o.rec.StartTime.Add(5 * time.Second).Format(time.RFC3339Nano) })
		}},
		"other pid": {200, func(o *owner) string { return o.helloBody(func(m map[string]any) { m["pid"] = 1 }) }},
		"version 2": {200, func(o *owner) string { return o.helloBody(func(m map[string]any) { m["v"] = 2 }) }},
	} {
		o := newOwner(t, nil)
		o.serve(tc.status, tc.body(o), nil)
		if r := o.run("sources", "list", "--json"); r.code != 3 || errCode(t, r) != "ENDPOINT_UNTRUSTED" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// a generation inside the slack of one second is fine
	o := newOwner(t, nil)
	o.serve(200, o.helloBody(func(m map[string]any) {
		m["gen"] = o.rec.StartTime.Add(500 * time.Millisecond).Format(time.RFC3339Nano)
	}),
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"ok":true,"data":{"sources":[]}}`)
		})
	if r := o.run("sources", "list", "--json"); r.code != 0 {
		t.Errorf("%+v", r)
	}
}

func TestAnswerLimitsAndOddAnswers(t *testing.T) {
	ok := func(o *owner) string { return o.helloBody(nil) }
	// an answer over 16 MiB
	o := newOwner(t, nil)
	o.serve(200, ok(o), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"p":"`)
		_, _ = w.Write(bytes.Repeat([]byte("a"), clicore.MaxResponseBytes))
		_, _ = io.WriteString(w, `"}}`)
	})
	if r := o.run("sources", "list", "--json"); r.code != 5 || errCode(t, r) != "RESULT_TOO_LARGE" {
		t.Errorf("too large: %.200v", r)
	}
	// a status that is not 200, an answer that is not JSON, a failure with no error object
	for name, fn := range map[string]http.HandlerFunc{
		"status":   func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) },
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `nope`) },
		"no error": func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"ok":false}`) },
	} {
		o := newOwner(t, nil)
		o.serve(200, ok(o), fn)
		if r := o.run("sources", "list", "--json"); r.code != 1 || errCode(t, r) != "INTERNAL" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// text from the owner is clipped: a long warning and a long error message
	o = newOwner(t, nil)
	long := strings.Repeat("w", 5000)
	o.serve(200, ok(o), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"sources":[]},"meta":{"coverage":"partial","identifiers":"shown","warnings":["`+long+`"]}}`)
	})
	r := o.run("sources", "list", "--json")
	ws := decode(t, r.out)["meta"].(map[string]any)["warnings"].([]any)
	if r.code != 0 || len(ws) != 1 || len(ws[0].(string)) > 600 {
		t.Errorf("warning: %.200v", r)
	}
	// a meta with no coverage or identifiers gets the safe values
	o = newOwner(t, nil)
	o.serve(200, ok(o), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"sources":[]},"meta":{}}`)
	})
	m := decode(t, o.run("sources", "list", "--json").out)["meta"].(map[string]any)
	if m["coverage"] != "complete" || m["identifiers"] != "hidden" || m["warnings"] == nil {
		t.Errorf("%v", m)
	}
}

func TestPartialResultIsSaidInTableMode(t *testing.T) {
	o := newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"sources": []map[string]any{{"sourceId": "a", "ok": false}}}, nil
	}))
	r := o.run("audit", "guide")
	if r.code != 0 || !strings.Contains(r.err, "partial") || !strings.Contains(r.err, "did not answer") {
		t.Errorf("%+v", r)
	}
	if r := o.run("audit", "guide", "--json"); r.err != "" {
		t.Errorf("JSON mode keeps stderr empty: %+v", r)
	}
}

func TestRequestInputEdges(t *testing.T) {
	o := newOwner(t, nil)
	dir := t.TempDir()
	write := func(b []byte) string {
		p := filepath.Join(dir, fmt.Sprint(len(b), "-", time.Now().UnixNano()))
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, b := range map[string][]byte{
		"odd length":          {0xFF, 0xFE, '{', 0, '}', 0, ' '},
		"lone high":           {0xFF, 0xFE, '{', 0, 0x3D, 0xD8, '}', 0},
		"lone low":            {0xFF, 0xFE, '{', 0, 0x00, 0xDC, '}', 0},
		"high then not low":   {0xFF, 0xFE, 0x3D, 0xD8, '{', 0, '}', 0},
		"bom only":            {0xFF, 0xFE},
		"utf8 bom only":       {0xEF, 0xBB, 0xBF},
		"two byte order mark": {0xFF, 0xFE, 0xFF, 0xFE},
	} {
		if r := o.run("audit", "guide", "--request", write(b), "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// a pair of surrogates is one character, and big-endian text is read as big-endian
	pair := []byte{0xFF, 0xFE, '{', 0, 0x3D, 0xD8, 0x00, 0xDE, '}', 0}
	if r := o.run("audit", "guide", "--request", write(pair), "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" || strings.Contains(r.err, "not valid text") {
		t.Errorf("a valid pair must get past the text check: %+v", r) // the object is not valid JSON ({ then a character), so a plain INVALID_ARGUMENT
	}
	be := []byte{0xFE, 0xFF, 0, '{', 0, '}'}
	if r := o.run("audit", "guide", "--request", write(be), "--json"); r.code != 0 {
		t.Errorf("big-endian: %+v", r)
	}
	// the empty value is refused, not read as "no request"
	if r := o.run("audit", "guide", "--request", "", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
		t.Errorf("empty: %+v", r)
	}
	if n := len(o.opsSeen()); n != 1 {
		t.Errorf("only the big-endian request reached the owner, got %d", n)
	}
}

func TestStdinThatNeverAnswersStopsAtTheDeadline(t *testing.T) {
	o := newOwner(t, nil)
	pr, pw := io.Pipe()
	defer pw.Close()
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- Run(context.Background(), []string{"audit", "guide", "--request", "-", "--timeout", "150ms", "--json"}, IO{
			In: pr, Out: &out, Err: &errb, Getenv: func(string) string { return "" }, Exe: func() (string, error) { return "unravis.exe", nil }, Attach: o.attacher().Attach,
		})
	}()
	select {
	case code := <-done:
		if code != 6 || !strings.Contains(errb.String(), "TIMEOUT") {
			t.Errorf("%d %s", code, errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the command waited on its input past --timeout")
	}
}

func TestProfileIsRefused(t *testing.T) {
	o := newOwner(t, nil)
	if r := o.run("audit", "guide", "--profile", "ci", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" || len(o.opsSeen()) != 0 {
		t.Errorf("flag: %+v", r)
	}
	if r := runOn(t, o.attacher(), "", map[string]string{"UNRAVIS_PROFILE": "ci"}, "audit", "guide", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" || len(o.opsSeen()) != 0 {
		t.Errorf("variable: %+v", r)
	}
}

func TestCompareArgumentsAreExactlyWhatWasAsked(t *testing.T) {
	o := newOwner(t, nil)
	o.run(append([]string{"audit", "compare", "--json", "--user-id", "7", "--ui-version", "v1", "--user-ip", "1.2.3.4", "--status-code", "500",
		"--controller", "C", "--action", "A", "--operation", "C.A", "--user-agent-contains", "x", "--url-contains", "/u", "--audit-id", "0123456789abcdef01234567", "--completion", "complete"}, o.window()...)...)
	s := o.opsSeen()
	args := s[len(s)-1]["args"].(map[string]any)
	if _, has := args["limit"]; has {
		t.Errorf("limit was not asked for: %v", args)
	}
	q := args["query"].(map[string]any)
	for _, k := range []string{"sourceIds", "start", "end", "baselineStart", "baselineEnd", "kind", "outcome", "metric", "bucketSeconds", "userIds", "uiVersions", "userIps",
		"statusCodes", "controller", "action", "operation", "userAgentContains", "urlContains", "auditId", "completion"} {
		if _, has := q[k]; !has {
			t.Errorf("the owner got no %s: %v", k, q)
		}
	}
	// a request file next to a field flag, and a failure to read a user id
	if r := o.run("audit", "compare", "--request", "-", "--limit", "3", "--json"); r.code != 2 {
		t.Errorf("--limit with --request: %+v", r)
	}
	// groups sends the names the tool knows
	o.run("audit", "groups", "--result-id", "00000000000000000000000000000000", "--offset", "2", "--limit", "3", "--search", "s", "--state", "compared", "--sort", "p95_ratio", "--json")
	s = o.opsSeen()
	g := s[len(s)-1]["args"].(map[string]any)
	for _, k := range []string{"resultId", "offset", "limit", "search", "state", "sort"} {
		if _, has := g[k]; !has {
			t.Errorf("groups: the owner got no %s: %v", k, g)
		}
	}
}

func TestAppStatusTakesNoArgumentsAndShowsNoDeadPid(t *testing.T) {
	o := newOwner(t, nil)
	if r := o.run("app", "status", "--request", writeTemp(t, `{"x":1}`), "--json"); r.code != 2 {
		t.Errorf("running: %+v", r)
	}
	_ = os.Remove(locator.FilePath(o.runDir, false))
	if r := o.run("app", "status", "--request", writeTemp(t, `{"x":1}`), "--json"); r.code != 2 {
		t.Errorf("not running: %+v", r)
	}
	tb := o.run("app", "status")
	if tb.code != 0 || strings.Contains(tb.out, "pid") || !strings.Contains(tb.out, "stopped") {
		t.Errorf("%+v", tb)
	}
}

func TestRunFolderFailureIsNotRunning(t *testing.T) {
	o := newOwner(t, nil)
	a := o.attacher()
	a.RunDir = func() (string, error) { return "", fmt.Errorf("no known folder") }
	if r := runOn(t, a, "", nil, "sources", "list", "--json"); r.code != 3 || errCode(t, r) != "APP_NOT_RUNNING" {
		t.Errorf("%+v", r)
	}
}

func writeTemp(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.json")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
