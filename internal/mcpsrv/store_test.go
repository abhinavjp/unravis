package mcpsrv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/mcpaudit"
)

// allBytes is the main file and the -wal and -shm files, what a person copying the folder would carry away.
func allBytes(t *testing.T, dir string) []byte {
	t.Helper()
	var all []byte
	for _, name := range []string{mcpaudit.FileName, mcpaudit.FileName + "-wal", mcpaudit.FileName + "-shm"} {
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // a file of this test's own temp folder
		if err == nil {
			all = append(all, b...)
		}
	}
	if len(all) == 0 {
		t.Fatal("no store bytes read")
	}
	return all
}

// The store holds digests only: not the secret, not the claim nonce, not the server key in the clear.
func TestSecretNeverInDB(t *testing.T) {
	e := newEnv(t)
	n := nonce(t)
	_, _ = e.g.Enroll(ctx, req("h1", n))
	if _, err := e.g.Approve(ctx, "h1", appr()); err != nil {
		t.Fatal(err)
	}
	secret, _, err := e.g.Claim(ctx, "h1", n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.g.Authenticate(ctx, credScheme+"h1."+secret); err != nil {
		t.Fatal(err)
	}
	for when, b := range map[string][]byte{"open (db, wal, shm)": allBytes(t, e.dir)} {
		for what, needle := range map[string]string{"secret": secret, "nonce": n} {
			if bytes.Contains(b, []byte(needle)) {
				t.Errorf("%s found in the store while %s", what, when)
			}
			// base64 of the raw bytes and hex are the other usual spellings
			if raw, err := decodeB64(needle); err == nil && bytes.Contains(b, raw) {
				t.Errorf("raw %s bytes found in the store while %s", what, when)
			}
		}
	}
	e.restart() // closes: the WAL is folded into the main file
	b := allBytes(t, e.dir)
	for what, needle := range map[string]string{"secret": secret, "nonce": n} {
		if bytes.Contains(b, []byte(needle)) {
			t.Errorf("%s found in the store after close", what)
		}
	}
	if !bytes.Contains(b, []byte("SEALED1:")) {
		t.Error("the server key is not stored sealed")
	}
}

func decodeB64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// The server key is sealed: the store of another account (a protector that cannot open it) is set aside, not read, and starts empty.
func TestStoreOfAnotherAccountIsSetAside(t *testing.T) {
	e := newEnv(t)
	h, _ := e.enrolled("h1")
	_ = h
	if err := e.db.Close(); err != nil {
		t.Fatal(err)
	}
	now := e.clk.Now
	db, rec, err := mcpaudit.Open(ctx, e.dir, failProt{xorProt{0x5a}}, now)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if rec == "" || !strings.Contains(rec, ".bad-") {
		t.Fatalf("not set aside: %q", rec)
	}
	if _, err := os.Stat(rec); err != nil {
		t.Fatalf("set-aside file missing: %v", err)
	}
	if n, _ := db.Count(ctx); n != 0 {
		t.Fatalf("the new store has %d profiles: grants crossed to another account", n)
	}
	// The old credential cannot work on the new store.
	g, _ := NewGrants(ctx, db, now)
	if _, err := g.Authenticate(ctx, h); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old credential on the new store: %v", err)
	}
	e.db = db // t.Cleanup closes it again; Close is idempotent for database/sql
}

func TestCorruptStoreIsSetAside(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, mcpaudit.FileName), bytes.Repeat([]byte("not a database "), 400), 0o600); err != nil {
		t.Fatal(err)
	}
	db, rec, err := mcpaudit.Open(ctx, dir, xorProt{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if rec == "" {
		t.Fatal("corrupt file not set aside")
	}
	if n, err := db.Count(ctx); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestOpenWithoutProtectorFailsClosed(t *testing.T) {
	if _, _, err := mcpaudit.Open(ctx, t.TempDir(), nil, nil); !errors.Is(err, mcpaudit.ErrNoProtector) {
		t.Fatalf("%v", err)
	}
}

// ---- constant-time compare ---------------------------------------------------------------------------------------------------------

// No digest, secret, nonce or MAC is compared with == or bytes.Equal in the grant code: subtle.ConstantTimeCompare only.
func TestNoBytesEqualOnSecrets(t *testing.T) {
	fset := token.NewFileSet()
	var files []string
	for _, dir := range []string{".", "../mcpaudit"} {
		m, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m {
			if !strings.HasSuffix(f, "_test.go") {
				files = append(files, f)
			}
		}
	}
	if len(files) < 6 {
		t.Fatalf("only %d source files found", len(files))
	}
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "bytes" && (sel.Sel.Name == "Equal" || sel.Sel.Name == "Compare") {
						t.Errorf("%s: bytes.%s", fset.Position(x.Pos()), sel.Sel.Name)
					}
				}
			case *ast.BinaryExpr:
				if (x.Op == token.EQL || x.Op == token.NEQ) && (secretish(x.X) || secretish(x.Y)) {
					t.Errorf("%s: %s compares a secret value with %s", fset.Position(x.Pos()), name, x.Op)
				}
			}
			return true
		})
	}
}

func secretish(e ast.Expr) bool {
	var name string
	switch x := e.(type) {
	case *ast.Ident:
		name = x.Name
	case *ast.SelectorExpr:
		name = x.Sel.Name
	case *ast.CallExpr: // len(secret) is a length, not a value
		return false
	default:
		return false
	}
	n := strings.ToLower(name)
	if strings.HasSuffix(n, "len") { // nonceLen, secretLen: sizes, not values
		return false
	}
	for _, w := range []string{"digest", "secret", "nonce", "mac"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// The checker itself must see a violation (a test that cannot fail proves nothing).
func TestNoBytesEqualOnSecretsSeesAViolation(t *testing.T) {
	src := "package x\nimport \"bytes\"\nfunc f(a, secretDigest []byte) bool { return bytes.Equal(a, secretDigest) }\nfunc g(p struct{ SecretDigest string }, b string) bool { return p.SecretDigest == b }\n"
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Equal" {
				hits++
			}
		case *ast.BinaryExpr:
			if x.Op == token.EQL && secretish(x.X) {
				hits++
			}
		}
		return true
	})
	if hits != 2 {
		t.Fatalf("hits %d", hits)
	}
}

// ---- HTTP routes ---------------------------------------------------------------------------------------------------------------------

func serve(e *env) *httptest.Server {
	mux := http.NewServeMux()
	e.g.EnrollRoutes(mux)
	return httptest.NewServer(mux)
}

func post(t *testing.T, url string, body any) (int, map[string]any, string) {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	resp, err := http.Post(url, "application/json", rd) //nolint:gosec // the URL of this test's own httptest server
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	return resp.StatusCode, m, string(raw)
}

func TestEnrollClaimOverHTTP(t *testing.T) {
	e := newEnv(t)
	srv := serve(e)
	defer srv.Close()
	n := nonce(t)
	body := map[string]any{"profile": "cc-1", "hostName": "claude-code", "hostVersion": "2.1", "exe": `C:\u\unravis.exe`, "ppid": 7, "claimNonce": n, "requestIdentifiers": false}
	if st, m, raw := post(t, srv.URL+"/mcp/v1/enroll", body); st != 200 || m["state"] != "pending" || m["profileRev"] != float64(1) {
		t.Fatalf("enroll: %d %s", st, raw)
	}
	claim := map[string]any{"profile": "cc-1", "claimNonce": n}
	if st, m, raw := post(t, srv.URL+"/mcp/v1/enroll/claim", claim); st != 409 || strings.Contains(raw, "secret") {
		t.Fatalf("claim while pending: %d %s", st, raw)
	} else if m["error"].(map[string]any)["code"] != "permission_required" {
		t.Fatalf("code: %s", raw)
	}
	if st, _, raw := post(t, srv.URL+"/mcp/v1/enroll/claim", map[string]any{"profile": "cc-1", "claimNonce": nonce(t)}); st != 403 {
		t.Fatalf("wrong nonce: %d %s", st, raw)
	}
	if _, err := e.g.Approve(ctx, "cc-1", appr()); err != nil {
		t.Fatal(err)
	}
	st, m, raw := post(t, srv.URL+"/mcp/v1/enroll/claim", claim)
	if st != 200 || len(m["secret"].(string)) != secretLen || m["profileRev"] != float64(2) {
		t.Fatalf("claim: %d %s", st, raw)
	}
	if _, err := e.g.Authenticate(ctx, credScheme+"cc-1."+m["secret"].(string)); err != nil {
		t.Fatalf("secret from the wire does not authenticate: %v", err)
	}
	if st, _, raw := post(t, srv.URL+"/mcp/v1/enroll/claim", claim); st != 410 || strings.Contains(raw, m["secret"].(string)) {
		t.Fatalf("second claim: %d %s", st, raw)
	}
}

func TestHTTPRefusalsEchoNothing(t *testing.T) {
	e := newEnv(t)
	srv := serve(e)
	defer srv.Close()
	canary := "CANARY-" + strings.Repeat("z", 20)
	valid := `"claimNonce":"` + nonce(t) + `"`
	for name, body := range map[string]string{
		"not json":      canary,
		"unknown field": `{"profile":"a","` + canary + `":1,` + valid + `}`,
		"trailing":      `{"profile":"a",` + valid + `} ` + canary,
		"bad id":        `{"profile":"` + canary + `",` + valid + `}`,
		"bad host":      `{"profile":"a","hostName":"` + canary + "\\n" + `",` + valid + `}`,
		"too big":       `{"profile":"a","exe":"` + strings.Repeat("x", 20<<10) + `",` + valid + `}`,
		"null":          `null`,
		"array":         `[1]`,
	} {
		want := 400
		if name == "too big" {
			want = 413
		}
		st, _, raw := post(t, srv.URL+"/mcp/v1/enroll", body)
		if st != want || strings.Contains(raw, "CANARY") || strings.Contains(raw, "zzz") {
			t.Errorf("%s: %d %s", name, st, raw)
		}
		st, _, raw = post(t, srv.URL+"/mcp/v1/enroll/claim", body)
		if st != want || strings.Contains(raw, "CANARY") {
			t.Errorf("claim %s: %d %s", name, st, raw)
		}
	}
	if n, _ := e.db.Count(context.Background()); n != 0 {
		t.Fatalf("refused requests created %d profiles", n)
	}
	// Only the two routes exist, only by POST.
	for _, path := range []string{"/mcp/v1/call", "/api/info", "/mcp/v1/enroll/x", "/"} {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s: %d", path, resp.StatusCode)
		}
	}
	resp, err := http.Get(srv.URL + "/mcp/v1/enroll")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("GET enroll: %d", resp.StatusCode)
	}
}
