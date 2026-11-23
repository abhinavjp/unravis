package server

// Wire contracts (BL-445). contracts/v1/schema holds one JSON Schema per answer shape and contracts/v1/fixtures one golden body per
// case, captured from the real handlers below. The Go side checks both ways: the live answer must satisfy the schema and equal the
// golden, and every golden must satisfy its schema. The Rust shell reads the same golden files (desktop/ipc-core/tests/contracts.rs;
// it runs in the desktop jobs at promotion, or by hand with cargo test -p ipc-core), so a renamed or dropped field fails there too.
//
// Regenerate the goldens after a deliberate change:  UPDATE_CONTRACTS=1 go test ./internal/server -run TestWireContracts
// and review the diff. Never put real data in a fixture: the demo dataset and fixed test values only.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/version"
)

const contractsDir = "../../contracts/v1"

func compileContract(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	sc, err := c.Compile(filepath.Join(contractsDir, "schema", name+".schema.json"))
	if err != nil {
		t.Fatalf("schema %s: %v", name, err)
	}
	return sc
}

func schemaValue(t *testing.T, raw []byte) any {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	return v
}

// pretty re-encodes a body the way the fixtures are stored: keys sorted, two-space indent, one trailing newline.
func pretty(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // a number is written back as it came, not rounded through float64
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

type wireCase struct {
	name   string // fixture file name and schema (error answers share one schema)
	schema string
	status int
	body   func(t *testing.T) *httptest.ResponseRecorder
}

func TestWireContracts(t *testing.T) {
	anchor := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	oldV, oldC, oldD := version.Version, version.Commit, version.Date
	version.Version, version.Commit, version.Date = "1.2.3", "abc1234", "2026-10-27"
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldV, oldC, oldD })

	demo := New(audit.NewDemo(anchor), nil, testToken, true)
	cases := []wireCase{
		{name: "info.demo", schema: "info", status: http.StatusOK, body: func(t *testing.T) *httptest.ResponseRecorder {
			return request(demo, http.MethodGet, "/api/info", "", true)
		}},
		{name: "version", schema: "version", status: http.StatusOK, body: func(t *testing.T) *httptest.ResponseRecorder {
			return request(demo, http.MethodGet, "/api/version", "", true)
		}},
		{name: "error.unauthorized", schema: "error", status: http.StatusUnauthorized, body: func(t *testing.T) *httptest.ResponseRecorder {
			return request(demo, http.MethodGet, "/api/info", "", false)
		}},
		{name: "error.validation", schema: "error", status: http.StatusBadRequest, body: func(t *testing.T) *httptest.ResponseRecorder {
			return request(demo, http.MethodPost, "/api/analyze", `{"unexpected":true}`, true)
		}},
		{name: "error.locked", schema: "error", status: http.StatusLocked, body: func(t *testing.T) *httptest.ResponseRecorder {
			e := newMKEnv(t)
			if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
				t.Fatalf("setup = %d %s", res.Code, res.Body)
			}
			if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
				t.Fatalf("lock = %d", res.Code)
			}
			return e.do(http.MethodGet, "/api/cases", "")
		}},
	}
	update := os.Getenv("UPDATE_CONTRACTS") == "1"
	if update && os.Getenv("CI") != "" {
		t.Fatal("UPDATE_CONTRACTS=1 rewrites the goldens, so the comparison below would always pass: not allowed in CI")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := compileContract(t, c.schema)
			res := c.body(t)
			if res.Code != c.status {
				t.Fatalf("status = %d want %d: %s", res.Code, c.status, res.Body)
			}
			if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("content type = %q", ct)
			}
			live := res.Body.Bytes()
			if err := sc.Validate(schemaValue(t, live)); err != nil {
				t.Fatalf("live answer breaks the schema: %v\n%s", err, live)
			}
			path := filepath.Join(contractsDir, "fixtures", c.name+".json")
			want := pretty(t, live)
			if update {
				if err := os.WriteFile(path, want, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			golden, err := os.ReadFile(path) //nolint:gosec // a fixed fixtures folder and names from this file
			if err != nil {
				t.Fatalf("golden missing (run with UPDATE_CONTRACTS=1): %v", err)
			}
			if err := sc.Validate(schemaValue(t, golden)); err != nil {
				t.Fatalf("golden breaks the schema: %v", err)
			}
			// A Windows checkout may turn the golden's line breaks into CRLF; the answer is compared as LF text.
			golden = bytes.ReplaceAll(golden, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(golden, want) {
				t.Fatalf("live answer differs from the golden %s (UPDATE_CONTRACTS=1 regenerates):\n--- golden\n%s--- live\n%s", c.name, golden, want)
			}
		})
	}
}

// TestWireContractsCatchDrift proves the schemas have teeth: a renamed, dropped, extra or wrongly typed field is refused.
func TestWireContractsCatchDrift(t *testing.T) {
	info := compileContract(t, "info")
	golden, err := os.ReadFile(filepath.Join(contractsDir, "fixtures", "info.demo.json")) //nolint:gosec // a fixed fixtures folder
	if err != nil {
		t.Fatal(err)
	}
	// A required field that is simply missing (not renamed, so the extra-field rule cannot be what refuses it).
	var m map[string]any
	if err := json.Unmarshal(golden, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"demo", "limits", "now"} {
		cut := map[string]any{}
		for k, v := range m {
			if k != key {
				cut[k] = v
			}
		}
		raw, _ := json.Marshal(cut)
		if err := info.Validate(schemaValue(t, raw)); err == nil {
			t.Errorf("schema accepted an answer without %q", key)
		}
	}
	for _, tc := range []struct{ name, from, to string }{
		{"renamed field", `"maxSources"`, `"maxSource"`},
		{"extra field", `"demo": true`, `"demo": true, "surprise": 1`},
		{"wrong type", `"maxPageSize": 100`, `"maxPageSize": "100"`},
	} {
		mutated := strings.Replace(string(golden), tc.from, tc.to, 1)
		if mutated == string(golden) {
			t.Fatalf("%s: the golden has no %s; update the test", tc.name, tc.from)
		}
		if err := info.Validate(schemaValue(t, []byte(mutated))); err == nil {
			t.Errorf("%s: schema accepted a drifted answer", tc.name)
		}
	}
}

// TestWireContractsErrorSchemaCoversTheHandlerShapes: every failure shape the handlers write is accepted, so the schema does not
// refuse a real body (master-key and case-key throttle, source-add failures) that has no golden yet.
func TestWireContractsErrorSchemaCoversTheHandlerShapes(t *testing.T) {
	sc := compileContract(t, "error")
	for _, body := range []any{
		masterKeyFailure{Error: "locked", Reason: "locked", State: "locked"},
		masterKeyFailure{Error: "wait", Reason: "throttled", State: "locked", RetryAfterSeconds: 4},
		caseKeyFailure{Error: "wait", Reason: "throttled", RetryAfterSeconds: 2},
		caseFailure{Error: "x", Reason: "y", Column: "c", ImportID: 9007199254740993},
		struct {
			Error       string   `json:"error"`
			Code        string   `json:"code"`
			Stage       string   `json:"stage"`
			Collections []string `json:"collections,omitempty"`
		}{"no", "collection_missing", "check", []string{"a"}},
	} {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := sc.Validate(schemaValue(t, raw)); err != nil {
			t.Errorf("schema refuses a real body %s: %v", raw, err)
		}
	}
}
