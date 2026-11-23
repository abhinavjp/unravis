package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/pipesrv"
	"api-audit-explorer/internal/server"
)

// The command line route of the owner, as main wires it: the pipe handler in front, the dashboard behind it. Only the command line image
// gets through, and the call reaches the real dashboard handler.
func TestCLIRouteIsWiredBehindThePipeHandler(t *testing.T) {
	const token = "tok-0123456789abcdef0123456789abcdef"
	dash := server.New(audit.NewDemo(time.Now()), nil, token, false)
	h := pipesrv.Handler(dash, pipesrv.WithCLI(newCLIHandler(dash, token)))
	call := func(role pipesrv.Role, image, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(pipesrv.WithPeerImage(pipesrv.WithRole(context.Background(), role), image))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const cli = `C:\Program Files\Unravis\bin\unravis.exe`
	if w := call(pipesrv.RoleAG, cli, "GET", "/cli/v1/hello", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"ready"`) {
		t.Errorf("hello: %d %s", w.Code, w.Body.String())
	}
	w := call(pipesrv.RoleAG, cli, "POST", "/cli/v1/op", `{"op":"sources.list","args":{},"requestId":"req_0123456789abcdef"}`)
	var got struct {
		OK   bool
		Data struct{ Sources []any }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got.OK || len(got.Data.Sources) != 2 {
		t.Errorf("sources.list: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), token) {
		t.Errorf("the session token is in the answer")
	}
	for _, image := range []string{`C:\Program Files\Unravis\unravis-desktop.exe`, `C:\Windows\System32\cmd.exe`, ""} {
		if w := call(pipesrv.RoleAG, image, "POST", "/cli/v1/op", `{}`); w.Code != http.StatusForbidden {
			t.Errorf("%q: %d", image, w.Code)
		}
	}
	// the dashboard's own routes stay unreachable on the ag pipe, with or without a token
	if w := call(pipesrv.RoleAG, cli, "GET", "/api/cases", ""); w.Code != 404 {
		t.Errorf("/api/cases on ag: %d", w.Code)
	}
}
