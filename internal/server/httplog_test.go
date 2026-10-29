package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
)

// bufLogger is a logger over a buffer at debug, so tests see every line the
// middleware and the events write.
func bufLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// The journey line names the route PATTERN and the request id the client was
// given — never the query string and never the body's source ids.
func TestRequestLogUsesRoutePatternAndRequestID(t *testing.T) {
	var buf bytes.Buffer
	h := New(&fakeRepository{}, nil, testToken, false, WithLogger(bufLogger(&buf)))
	body, _ := json.Marshal(testQuery())
	req := newTestRequest(http.MethodPost, "/api/analyze?probe=querysecret123", string(body), true)
	res := serve(h, req)
	if res.Code != http.StatusOK {
		t.Fatalf("analyze status=%d body=%s", res.Code, res.Body.String())
	}
	id := res.Header().Get("X-Request-Id")
	if len(id) != 8 {
		t.Fatalf("X-Request-Id = %q, want 8 hex characters", id)
	}
	out := buf.String()
	for _, want := range []string{"route=/api/analyze", "req_id=" + id, "status=200", "method=POST"} {
		if !strings.Contains(out, want) {
			t.Errorf("log line missing %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"querysecret123", "demo-west"} {
		if strings.Contains(out, secret) {
			t.Errorf("log line leaked %q:\n%s", secret, out)
		}
	}
}

// Every error reply is logged with its class: a refused token is auth, an
// unknown API route is validation.
func TestRequestLogErrorClasses(t *testing.T) {
	var buf bytes.Buffer
	h := New(&fakeRepository{}, nil, testToken, false, WithLogger(bufLogger(&buf)))
	if res := serve(h, newTestRequest(http.MethodGet, "/api/info", "", false)); res.Code != http.StatusUnauthorized {
		t.Fatalf("no-token status=%d", res.Code)
	}
	if res := serve(h, newTestRequest(http.MethodGet, "/api/nope", "", true)); res.Code != http.StatusNotFound {
		t.Fatalf("unknown route status=%d", res.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "status=401") || !strings.Contains(out, "error_class=auth") {
		t.Errorf("401 line missing its class:\n%s", out)
	}
	if !strings.Contains(out, "status=404") || !strings.Contains(out, "error_class=validation") {
		t.Errorf("404 line missing its class:\n%s", out)
	}
}

type panicRepository struct {
	*fakeRepository
}

func (panicRepository) Sources(context.Context) ([]audit.Source, error) {
	panic("boom mongodb://svc:PanicPass-123!@db.internal password=<redacted>")
}

// A panic in a handler answers a plain 500 and is logged with its stack,
// sanitized: the panic text carries credentials and they must not survive.
func TestPanicRecoveryReturns500AndLogsSanitized(t *testing.T) {
	var buf bytes.Buffer
	h := New(panicRepository{&fakeRepository{}}, nil, testToken, false, WithLogger(bufLogger(&buf)))
	res := serve(h, newTestRequest(http.MethodGet, "/api/sources", "", true))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("panic status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Internal server error.") {
		t.Fatalf("panic body is not the plain 500: %s", res.Body.String())
	}
	out := buf.String()
	if !strings.Contains(out, "panic recovered") || !strings.Contains(out, "error_class=internal") {
		t.Errorf("panic was not logged as an internal error:\n%s", out)
	}
	if strings.Contains(out, "PanicPass-123!") {
		t.Errorf("panic log leaked a password:\n%s", out)
	}
}

// POST /api/client-log: auth, kind allow-list, unknown fields, size cap,
// batch cap and the per-minute rate limit are all enforced.
func TestClientLogValidation(t *testing.T) {
	var buf bytes.Buffer
	newHandler := func() http.Handler {
		return New(&fakeRepository{}, nil, testToken, false, WithLogger(bufLogger(&buf)))
	}
	post := func(h http.Handler, body string, token bool) int {
		return serve(h, newTestRequest(http.MethodPost, "/api/client-log", body, token)).Code
	}

	if code := post(newHandler(), `{"kind":"nav","screen":"cases"}`, false); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d, want 401", code)
	}
	if code := post(newHandler(), `{"kind":"bogus","screen":"cases"}`, true); code != http.StatusBadRequest {
		t.Errorf("bad kind = %d, want 400", code)
	}
	if code := post(newHandler(), `{"kind":"nav","screen":"cases","evil":1}`, true); code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", code)
	}
	big := `{"kind":"error","message":"` + strings.Repeat("x", 9000) + `"}`
	if code := post(newHandler(), big, true); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize = %d, want 413", code)
	}
	var batch strings.Builder
	batch.WriteString(`{"entries":[`)
	for i := 0; i < clientLogMaxBatch+1; i++ {
		if i > 0 {
			batch.WriteString(",")
		}
		batch.WriteString(`{"kind":"nav","screen":"cases"}`)
	}
	batch.WriteString(`]}`)
	if code := post(newHandler(), batch.String(), true); code != http.StatusBadRequest {
		t.Errorf("oversize batch = %d, want 400", code)
	}

	// Rate limit: three pass, the fourth in the same minute is refused.
	h := newHandler()
	h.(*handler).clientLogs.limit = 3
	for i := 0; i < 3; i++ {
		if code := post(h, `{"kind":"nav","screen":"cases"}`, true); code != http.StatusNoContent {
			t.Fatalf("entry %d = %d, want 204", i, code)
		}
	}
	if code := post(h, `{"kind":"nav","screen":"cases"}`, true); code != http.StatusTooManyRequests {
		t.Errorf("over limit = %d, want 429", code)
	}
}

// Accepted UI entries land in the log with src=ui; the message is sanitized
// and truncated to 300 characters server-side.
func TestClientLogWritesSanitizedEntries(t *testing.T) {
	var buf bytes.Buffer
	h := New(&fakeRepository{}, nil, testToken, false, WithLogger(bufLogger(&buf)))
	body := `{"entries":[` +
		`{"kind":"nav","screen":"cases"},` +
		`{"kind":"error","screen":"guided","code":"query_failed","stage":"run","message":"failed password=<redacted> ` + strings.Repeat("y", 400) + `"}` +
		`]}`
	res := serve(h, newTestRequest(http.MethodPost, "/api/client-log", body, true))
	if res.Code != http.StatusNoContent {
		t.Fatalf("client-log status=%d body=%s", res.Code, res.Body.String())
	}
	out := buf.String()
	if !strings.Contains(out, "src=ui") || !strings.Contains(out, "kind=nav") || !strings.Contains(out, "kind=error") {
		t.Errorf("entries missing from the log:\n%s", out)
	}
	if strings.Contains(out, "UiSecret-123") {
		t.Errorf("client log leaked a secret from the message:\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("y", 301)) {
		t.Errorf("client log message was not truncated to 300 chars:\n%s", out)
	}
}

// GET /api/logs/info names the file, its size and its newest entry time.
func TestLogsInfo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, applog.FileName), []byte("a line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := New(&fakeRepository{}, nil, testToken, false, WithLogDir(dir))
	res := serve(h, newTestRequest(http.MethodGet, "/api/logs/info", "", true))
	if res.Code != http.StatusOK {
		t.Fatalf("logs/info status=%d body=%s", res.Code, res.Body.String())
	}
	var info struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		Newest string `json:"newest"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(info.Path, applog.FileName) || info.Size != 7 || info.Newest == "" {
		t.Errorf("logs/info = %+v", info)
	}
}

// The end-to-end promise: drive the login (session token), case-key unlock
// (right and wrong key) and connection-test flows with known secret strings,
// then grep the whole log folder — none of them may appear anywhere.
func TestNoSecretEverReachesTheLog(t *testing.T) {
	logDir := t.TempDir()
	lf, err := applog.Open(logDir, applog.ParseLevel("debug"), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Case-key flow: create a keyed case, lock it, try the wrong key, unlock.
	env := newCKEnv(t)
	env.h.(*handler).logger = lf.Logger
	c := env.newKeyedCase("Secret keeping")
	if res := env.keyCall(c.ID, "lock", `{}`); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	wrongKey := "not the case key at all"
	if res := env.keyCall(c.ID, "unlock", unlockBody(wrongKey)); res.Code == http.StatusOK {
		t.Fatalf("wrong key unlocked the case")
	}
	if res := env.keyCall(c.ID, "unlock", unlockBody(ckPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}

	// Connection-test flow: the typed failure message itself carries the
	// credentials (that is what a driver hands back); the log must clean it.
	connSecret := "ConnSecret-123!"
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}, err: &audit.ConnectionError{
		Stage: "connect", Code: "connection_failed",
		Message: "dial mongodb://svc:" + connSecret + "@db.internal:27017 failed password=" + connSecret,
	}}
	h := New(repo, nil, testToken, false, WithLogger(lf.Logger))
	body, _ := json.Marshal(audit.ConnectionInput{URI: "mongodb://svc:" + connSecret + "@db.internal:27017", Password: connSecret})
	if res := serve(h, newTestRequest(http.MethodPost, "/api/connections/test", string(body), true)); res.Code != http.StatusBadRequest {
		t.Fatalf("connection test status=%d", res.Code)
	}
	// A refused login attempt with a wrong token value.
	serve(h, newTestRequest(http.MethodGet, "/api/info", "", false))

	if err := lf.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(logDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(data)
	}
	text := all.String()
	if text == "" {
		t.Fatal("log file is empty; the flows wrote nothing")
	}
	if !strings.Contains(text, "case_key_unlock") {
		t.Errorf("the unlock events are missing from the log:\n%s", text)
	}
	for _, secret := range []string{testToken, ckPass, wrongKey, connSecret, "svc:" + connSecret} {
		if strings.Contains(text, secret) {
			t.Errorf("log file contains the secret %q", secret)
		}
	}
}

// A failed audit read during a guided investigation logs, per source, the
// source id, the error code and the sanitized reason — the lines that answer
// "why could the audit source not be read" — plus the audit_failure cause.
func TestInvestigationLogsPerSourceDiagnostics(t *testing.T) {
	var buf bytes.Buffer
	page := audit.Page{Sources: []audit.SourceResult{{
		SourceID: "demo-west", OK: false, ErrorCode: "connection_failed",
		Error: "dial mongodb://svc:DiagPass-123!@db.internal refused",
	}}}
	repo := &fakeRepository{requestPages: []audit.Page{page}}
	e := tagEnv(t, casestore.NewMemory(), repo, WithLogger(bufLogger(&buf)))
	id := e.newCase("diag")
	res := e.json(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(""))
	if res.Code != http.StatusOK {
		t.Fatalf("investigation status=%d body=%s", res.Code, res.Body)
	}
	out := buf.String()
	for _, want := range []string{"audit_source_start", "audit_source_read", "source_id=demo-west", "error_code=connection_failed", "investigation_audit_failed", "cause=failed", "investigation_started", "investigation_finished"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DiagPass-123!") {
		t.Errorf("source diagnostics leaked a password:\n%s", out)
	}
}

// newTestRequest and serve mirror request() in server_test.go but return the
// recorder, so a test can read response headers such as X-Request-Id.
func newTestRequest(method, path, body string, withToken bool) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8765"
	if withToken {
		req.Header.Set("X-Audit-Token", testToken)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}
