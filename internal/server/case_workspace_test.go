package server

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

func workspacePath(id string) string { return "/api/cases/" + id + "/workspace" }

func workspaceEnvs(t *testing.T, fn func(t *testing.T, e *caseEnv)) {
	t.Helper()
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			fn(t, tagEnv(t, store, &fakeRepository{}))
		})
	}
}

const wsBody = `{"version":1,"connection":{"ref":"saved-1","databases":["audit","logs"]},"filters":{"range":"7d","controller":"Home"}}`

func TestCaseWorkspaceRoundTrip(t *testing.T) {
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		id := e.newCase("ws")
		res := e.get(workspacePath(id))
		if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "{}" {
			t.Fatalf("fresh GET = %d %s, want 200 {}", res.Code, res.Body)
		}
		res = e.json(http.MethodPut, workspacePath(id), " "+wsBody+"\n")
		if res.Code != http.StatusOK {
			t.Fatalf("PUT = %d %s", res.Code, res.Body)
		}
		if got := strings.TrimSpace(res.Body.String()); got != wsBody {
			t.Errorf("PUT answered %s", got)
		}
		res = e.get(workspacePath(id))
		if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != wsBody {
			t.Fatalf("GET = %d %s", res.Code, res.Body)
		}
		if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q", ct)
		}
		// The note is kept per case.
		other := e.newCase("other")
		if res = e.get(workspacePath(other)); strings.TrimSpace(res.Body.String()) != "{}" {
			t.Errorf("workspace leaked into another case: %s", res.Body)
		}
		// An empty object clears the note.
		if res = e.json(http.MethodPut, workspacePath(id), `{}`); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "{}" {
			t.Fatalf("clear = %d %s", res.Code, res.Body)
		}
		if res = e.get(workspacePath(id)); strings.TrimSpace(res.Body.String()) != "{}" {
			t.Errorf("after clear GET = %s", res.Body)
		}
	})
}

func TestCaseWorkspaceUnknownAndBadCase(t *testing.T) {
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			body := ""
			if method == http.MethodPut {
				body = `{}`
			}
			for _, id := range []string{absentCaseID, "not-an-id"} {
				res := e.json(method, workspacePath(id), body)
				if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
					t.Errorf("%s %s = %d %s, want 404 case-not-found", method, id, res.Code, res.Body)
				}
			}
		}
	})
}

func TestCaseWorkspaceRefusesBadBodies(t *testing.T) {
	long := strings.Repeat("a", workspaceMaxString+1)
	deep := strings.Repeat(`{"a":`, workspaceMaxDepth) + `1` + strings.Repeat(`}`, workspaceMaxDepth)
	okDeep := strings.Repeat(`{"a":`, workspaceMaxDepth-1) + `1` + strings.Repeat(`}`, workspaceMaxDepth-1)
	cases := []struct {
		name, body string
		code       int
	}{
		{"invalid json", `{"a":`, 400},
		{"array", `[1,2]`, 400},
		{"string", `"x"`, 400},
		{"null", `null`, 400},
		{"two values", `{} {}`, 400},
		{"trailing junk", `{}x`, 400},
		{"forbidden at depth 3", `{"a":{"b":{"Password":"hunter2-marker"}}}`, 400},
		{"forbidden in array", `{"a":[{"x":{"API_Key":"k-marker"}}]}`, 400},
		{"forbidden top level", `{"connectionString":"mongodb://marker"}`, 400},
		{"forbidden uri", `{"filters":{"URI":"x"}}`, 400},
		{"forbidden host", `{"db":{"host":"h"}}`, 400},
		{"forbidden user", `{"user":"u"}`, 400},
		{"forbidden cacertificate", `{"caCertificate":"x"}`, 400},
		{"forbidden url", `{"url":"x"}`, 400},
		{"forbidden token", `{"x":{"Token":"x"}}`, 400},
		{"forbidden secret", `{"secret":1}`, 400},
		{"forbidden passwd", `{"passwd":1}`, 400},
		{"forbidden username", `{"username":1}`, 400},
		{"duplicate key hides a forbidden one", `{"a":{"password":"x"},"a":1}`, 400},
		{"duplicate key at depth", `{"a":{"b":1,"b":2}}`, 400},
		{"duplicate key top level", `{"a":1,"a":1}`, 400},
		{"zero-width separator", "{\"pass​word\":\"x\"}", 400},
		{"soft hyphen separator", "{\"api­key\":\"x\"}", 400},
		{"dotted key", `{"api.key":"x"}`, 400},
		{"pwd", `{"db":{"pwd":"x"}}`, 400},
		{"pass", `{"pass":"x"}`, 400},
		{"hostname", `{"a":[{"HostName":"x"}]}`, 400},
		{"address", `{"address":"x"}`, 400},
		{"connstr", `{"conn_str":"x"}`, 400},
		{"credential", `{"credential":"x"}`, 400},
		{"credentials", `{"Credentials":{}}`, 400},
		{"auth", `{"auth":1}`, 400},
		{"authSource", `{"authSource":"admin"}`, 400},
		{"cert", `{"cert":"x"}`, 400},
		{"certificate", `{"certificate":"x"}`, 400},
		{"privateKey", `{"private-key":"x"}`, 400},
		{"connection string value", `{"connection":{"ref":"mongodb://u:p@h"}}`, 400},
		{"url value in array", `{"a":["https://example.test/x"]}`, 400},
		{"too deep", deep, 400},
		{"string too long", `{"a":"` + long + `"}`, 400},
		{"key too long", `{"` + long + `":1}`, 400},
		{"string in array too long", `{"a":["` + long + `"]}`, 400},
		{"over the cap", `{"a":"` + strings.Repeat("a", casestore.MaxWorkspaceBlob) + `"}`, 413},
		{"over one MiB", `{"a":"` + strings.Repeat("a", 2<<20) + `"}`, 413},
	}
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		id := e.newCase("bad bodies")
		if res := e.json(http.MethodPut, workspacePath(id), wsBody); res.Code != http.StatusOK {
			t.Fatalf("seed = %d %s", res.Code, res.Body)
		}
		for _, c := range cases {
			res := e.json(http.MethodPut, workspacePath(id), c.body)
			if res.Code != c.code {
				t.Errorf("%s: %d %.200s, want %d", c.name, res.Code, res.Body, c.code)
				continue
			}
			out := res.Body.String()
			for _, leak := range []string{"hunter2-marker", "k-marker", "mongodb://marker", "Password", "API_Key", "connectionString", "u:p@h", "mongodb", "HostName"} {
				if strings.Contains(out, leak) {
					t.Errorf("%s: the answer repeats the input %q: %s", c.name, leak, out)
				}
			}
			if err := evidence.LintCausal(failureOf(t, res).Error); err != nil {
				t.Errorf("%s: message is causal: %v", c.name, err)
			}
		}
		// Every refused write left the stored note alone.
		if res := e.get(workspacePath(id)); strings.TrimSpace(res.Body.String()) != wsBody {
			t.Errorf("a refused write changed the note: %s", res.Body)
		}
		// The edge that is allowed: depth 8, a string of exactly 512, keys that only contain a forbidden word.
		for _, ok := range []string{okDeep, `{"a":"` + strings.Repeat("a", workspaceMaxString) + `"}`, `{"userId":1,"hostCount":2,"tokens":3}`,
			`{"connection":{"ref":"saved-1","databases":["audit_prod","logs-2024","Brain Payroll"]},"filters":{"from":"2024-01-02T03:04:05Z","path":"/api/v1/a:b","note":"a:b//c"}}`,
			`{"passes":1,"authors":["x"],"addressee":"y","a":{"a":1},"b":{"a":2}}`} {
			if res := e.json(http.MethodPut, workspacePath(id), ok); res.Code != http.StatusOK {
				t.Errorf("allowed body refused: %d %.200s", res.Code, res.Body)
			}
		}
	})
}

func TestCaseWorkspaceNeedsJSONContentType(t *testing.T) {
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		id := e.newCase("ct")
		req, _ := http.NewRequest(http.MethodPut, workspacePath(id), strings.NewReader(`{}`))
		req.Header.Set("X-Audit-Token", testToken)
		req.Header.Set("Content-Type", "text/plain")
		if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
			t.Errorf("text/plain PUT = %d %s", res.Code, res.Body)
		}
	})
}

func TestCaseWorkspaceClosedCaseReadsButRefusesWrites(t *testing.T) {
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		id := e.newCase("closing")
		if res := e.json(http.MethodPut, workspacePath(id), wsBody); res.Code != http.StatusOK {
			t.Fatalf("PUT = %d %s", res.Code, res.Body)
		}
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
			t.Fatalf("close = %d %s", res.Code, res.Body)
		}
		res := e.json(http.MethodPut, workspacePath(id), `{"version":2}`)
		if res.Code != http.StatusConflict || failureOf(t, res).Reason != "case-closed" {
			t.Errorf("PUT on a closed case = %d %s", res.Code, res.Body)
		}
		if res = e.get(workspacePath(id)); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != wsBody {
			t.Errorf("GET on a closed case = %d %s", res.Code, res.Body)
		}
	})
}

// Reopen restores nothing by itself: the saved Mongo link and filters sit in the workspace
// note, which a Closed case still reads. After Reopen the same note is there, whole, and
// saving works again. This is the contract the Reopen docs rest on.
func TestCaseWorkspaceSurvivesCloseAndReopen(t *testing.T) {
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		id := e.newCase("round trip")
		if res := e.json(http.MethodPut, workspacePath(id), wsBody); res.Code != http.StatusOK {
			t.Fatalf("PUT = %d %s", res.Code, res.Body)
		}
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
			t.Fatalf("close = %d %s", res.Code, res.Body)
		}
		if res := e.get(workspacePath(id)); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != wsBody {
			t.Fatalf("GET while closed = %d %s", res.Code, res.Body)
		}
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/reopen", ""); res.Code != http.StatusOK {
			t.Fatalf("reopen = %d %s", res.Code, res.Body)
		}
		if res := e.get(workspacePath(id)); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != wsBody {
			t.Errorf("GET after reopen = %d %s: the note must come back whole", res.Code, res.Body)
		}
		if res := e.json(http.MethodPut, workspacePath(id), `{"version":3}`); res.Code != http.StatusOK {
			t.Errorf("PUT after reopen = %d %s: saving must work again", res.Code, res.Body)
		}
	})
}

func TestCaseWorkspaceClumsyUse(t *testing.T) {
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		id := e.newCase("clumsy")
		// Double PUT: the second replaces the first as a whole.
		for _, b := range []string{`{"a":1,"b":2}`, `{"a":1,"b":2}`, `{"c":3}`} {
			if res := e.json(http.MethodPut, workspacePath(id), b); res.Code != http.StatusOK {
				t.Fatalf("PUT %s = %d %s", b, res.Code, res.Body)
			}
		}
		if res := e.get(workspacePath(id)); strings.TrimSpace(res.Body.String()) != `{"c":3}` {
			t.Errorf("after double PUT: %s", res.Body)
		}

		// Concurrent PUTs: all succeed and the result is exactly one of the bodies.
		const n = 8
		var wg sync.WaitGroup
		codes := make(chan int, n)
		bodies := map[string]bool{}
		for i := 0; i < n; i++ {
			b := `{"writer":` + string(rune('0'+i)) + `}`
			bodies[b] = true
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes <- e.json(http.MethodPut, workspacePath(id), b).Code
			}()
		}
		wg.Wait()
		close(codes)
		for code := range codes {
			if code != http.StatusOK {
				t.Errorf("parallel PUT = %d", code)
			}
		}
		if got := strings.TrimSpace(e.get(workspacePath(id)).Body.String()); !bodies[got] {
			t.Errorf("after parallel PUTs the note is %q, not one of the bodies", got)
		}

		// GET and PUT after the case is erased answer 404.
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
			t.Fatalf("close = %d", res.Code)
		}
		if res := e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code != http.StatusNoContent {
			t.Fatalf("erase = %d %s", res.Code, res.Body)
		}
		for _, m := range []string{http.MethodGet, http.MethodPut} {
			body := ""
			if m == http.MethodPut {
				body = `{"a":1}`
			}
			if res := e.json(m, workspacePath(id), body); res.Code != http.StatusNotFound {
				t.Errorf("%s after erase = %d %s", m, res.Code, res.Body)
			}
		}
	})
}

func TestCaseWorkspaceDamagedNoteIsReportedNotServed(t *testing.T) {
	workspaceEnvs(t, func(t *testing.T, e *caseEnv) {
		id := e.newCase("damaged")
		c, err := e.store.Open(context.Background(), casestore.CaseID(id))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := c.(casestore.WorkspaceBlobStore).SetWorkspaceBlob(context.Background(), []byte(`[1]`)); err != nil {
			t.Fatal(err)
		}
		res := e.get(workspacePath(id))
		if res.Code != http.StatusInternalServerError || failureOf(t, res).Reason != "workspace-damaged" {
			t.Errorf("damaged GET = %d %s", res.Code, res.Body)
		}
		// A PUT repairs it.
		if res = e.json(http.MethodPut, workspacePath(id), `{"a":1}`); res.Code != http.StatusOK {
			t.Errorf("repair PUT = %d %s", res.Code, res.Body)
		}
	})
}

func TestCaseWorkspaceDemoIsUnavailable(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, true)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		if res := request(h, m, workspacePath(absentCaseID), "{}", true); res.Code != http.StatusServiceUnavailable {
			t.Errorf("demo %s = %d", m, res.Code)
		}
	}
}

func TestCaseWorkspaceIsLockedBehindTheMasterKey(t *testing.T) {
	e := newMKEnv(t)
	if res := e.do(http.MethodPost, "/api/master-key/setup", setupBody(mkPass, mkPass, true)); res.Code != http.StatusOK {
		t.Fatalf("setup = %d %s", res.Code, res.Body)
	}
	id := e.newCase("locked")
	path := workspacePath(id)
	if res := e.do(http.MethodPut, path, wsBody); res.Code != http.StatusOK {
		t.Fatalf("PUT unlocked = %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d", res.Code)
	}
	for _, c := range []struct{ method, body string }{{http.MethodGet, ""}, {http.MethodPut, wsBody}} {
		if res := e.do(c.method, path, c.body); res.Code != http.StatusLocked {
			t.Errorf("%s locked = %d %s, want 423", c.method, res.Code, res.Body)
		}
	}
	if res := e.do(http.MethodPost, "/api/master-key/unlock", unlockBody(mkPass)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	if res := e.do(http.MethodGet, path, ""); res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != wsBody {
		t.Fatalf("after unlock = %d %s", res.Code, res.Body)
	}
}
