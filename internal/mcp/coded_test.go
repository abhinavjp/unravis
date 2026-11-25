package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// statusHandler answers every dashboard route with one status.
func statusHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func TestEveryDashboardStatusHasItsCode(t *testing.T) {
	for status, want := range map[int]string{
		400: CodeInvalidArguments, 401: CodeBackendUnavailable, 404: CodeNotFound, 413: CodeBudgetExceeded, 423: CodeLocked,
		429: CodeBusy, 500: CodeInternal, 503: CodeInternal, 504: CodeDeadlineExceeded,
	} {
		b := NewLocal(statusHandler(status, "{}"), "tok-0123456789abcdef0123456789abcdef")
		_, err := b.Call(context.Background(), "audit_list_sources", json.RawMessage(`{}`))
		if err == nil || Code(err) != want {
			t.Errorf("HTTP %d: code %q (%v), want %q", status, Code(err), err, want)
		}
	}
	// a 200 answer that is not data
	b := NewLocal(statusHandler(200, "not json"), "tok-0123456789abcdef0123456789abcdef")
	if _, err := b.Call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); Code(err) != CodeInternal {
		t.Errorf("bad body: %v", err)
	}
}

func TestCodeOfPlainAndContextErrors(t *testing.T) {
	if Code(nil) != "" || Code(errors.New("x")) != CodeInvalidArguments || Code(context.Canceled) != CodeCancelled || Code(context.DeadlineExceeded) != CodeDeadlineExceeded {
		t.Error("Code")
	}
	b := NewLocal(statusHandler(200, "[]"), "tok-0123456789abcdef0123456789abcdef")
	// an argument problem and an unknown tool
	if _, err := b.Call(context.Background(), "audit_list_sources", json.RawMessage(`{"x":1}`)); Code(err) != CodeInvalidArguments {
		t.Errorf("args: %v", err)
	}
	if _, err := b.Call(context.Background(), "audit_delete_source", json.RawMessage(`{}`)); Code(err) != CodeInternal {
		t.Errorf("unknown tool: %v", err)
	}
	if _, err := b.Call(context.Background(), "audit_get_groups", json.RawMessage(`{"resultId":"nope"}`)); Code(err) != CodeResultExpired {
		t.Errorf("expired: %v", err)
	}
}

func TestLocalCallHonoursContext(t *testing.T) {
	ran := false
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true; _, _ = w.Write([]byte("[]")) })
	b := NewLocal(h, "tok-0123456789abcdef0123456789abcdef")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Call(ctx, "audit_list_sources", json.RawMessage(`{}`)); Code(err) != CodeCancelled || ran {
		t.Errorf("cancelled before: %v ran=%v", err, ran)
	}
	dctx, dcancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer dcancel()
	time.Sleep(5 * time.Millisecond)
	if _, err := b.Call(dctx, "audit_list_sources", json.RawMessage(`{}`)); Code(err) != CodeDeadlineExceeded {
		t.Errorf("deadline: %v", err)
	}
	// cancelled while the handler works: the answer is dropped even if the handler ignores its context
	ctx2, cancel2 := context.WithCancel(context.Background())
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cancel2(); _, _ = w.Write([]byte("[]")) })
	if _, err := NewLocal(slow, "tok-0123456789abcdef0123456789abcdef").Call(ctx2, "audit_list_sources", json.RawMessage(`{}`)); Code(err) != CodeCancelled {
		t.Errorf("cancel while working: %v", err)
	}
}

func TestLocalCallContainsAHandlerPanic(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom secret") })
	_, err := NewLocal(h, "tok-0123456789abcdef0123456789abcdef").Call(context.Background(), "audit_list_sources", json.RawMessage(`{}`))
	if Code(err) != CodeBackendUnavailable || strings.Contains(err.Error(), "secret") {
		t.Errorf("%v", err)
	}
}

func TestLocalAnswerIsCapped(t *testing.T) {
	var written int
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat("a", 1<<20))
		for i := 0; i < 40; i++ { // 40 MiB, with no end in sight for a handler that does not stop
			n, _ := w.Write(chunk)
			written += n
		}
	})
	_, err := NewLocal(h, "tok-0123456789abcdef0123456789abcdef").Call(context.Background(), "audit_list_sources", json.RawMessage(`{}`))
	if Code(err) != CodeResultTooLarge {
		t.Errorf("%v", err)
	}
	if rec := (&recorder{header: http.Header{}}); true {
		_, _ = rec.Write(make([]byte, maxBody+100))
		if rec.body.Len() != maxBody+1 {
			t.Errorf("the recorder kept %d bytes", rec.body.Len())
		}
	}
}

func TestLocalRequestSeesTheTokenAndLocalHost(t *testing.T) {
	var host, tok, remote string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, tok, remote = r.Host, r.Header.Get("X-Audit-Token"), r.RemoteAddr
		_, _ = w.Write([]byte("[]"))
	})
	if _, err := NewLocal(h, "tok-0123456789abcdef0123456789abcdef").Call(context.Background(), "audit_list_sources", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" || tok != "tok-0123456789abcdef0123456789abcdef" || remote != "127.0.0.1:0" {
		t.Errorf("%q %q %q", host, tok, remote)
	}
}

func TestIdentifierFiltersFollowTheOption(t *testing.T) {
	q := `{"query":{"sourceIds":["a"],"start":"2026-11-23T09:00:00Z","end":"2026-11-23T10:00:00Z","baselineStart":"2026-11-22T09:00:00Z","baselineEnd":"2026-11-22T10:00:00Z","kind":"all","outcome":"all","metric":"total","bucketSeconds":60,"userIds":[7]}}`
	h := statusHandler(200, `{"apis":[]}`)
	if _, err := NewLocal(h, "tok-0123456789abcdef0123456789abcdef").Call(context.Background(), "audit_compare", json.RawMessage(q)); err == nil {
		t.Error("a user id filter was accepted with identifiers off")
	}
	if _, err := NewLocal(h, "tok-0123456789abcdef0123456789abcdef", WithIdentifiers(true)).Call(context.Background(), "audit_compare", json.RawMessage(q)); err != nil {
		t.Errorf("a user id filter was refused with identifiers on: %v", err)
	}
}
