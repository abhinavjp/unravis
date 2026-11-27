package releasedash

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestGHRunRoute(t *testing.T) {
	status, conclusion := "completed", "success"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "/999") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			jobStatus, jobConclusion, end := "completed", conclusion, "2026-01-01T00:01:00Z"
			if status != "completed" {
				jobStatus, jobConclusion, end = "queued", "", ""
			}
			fmt.Fprintf(w, `{"jobs":[{"id":1,"name":"go-tests (2/3)","status":%q,"conclusion":%q,"started_at":"2026-01-01T00:00:00Z","completed_at":%q,"html_url":"%sactions/runs/123/job/1","steps":[{"number":1,"name":"Skipped","status":"completed","conclusion":"skipped"},{"number":2,"name":"Build","status":%q,"conclusion":%q}]},{"id":2,"name":"B","status":"completed","conclusion":"success","html_url":"https://evil.example/"},{"id":3,"name":"C","status":"completed","conclusion":"success"},{"id":4,"name":"D","status":"completed","conclusion":"success"}]}`, jobStatus, jobConclusion, end, RepoURL, jobStatus, jobConclusion)
			return
		}
		fmt.Fprintf(w, `{"name":"Release","event":"push","head_branch":"v0.1.0-rc.66","head_sha":"abc123","status":%q,"conclusion":%q,"created_at":"2026-01-01T00:00:00Z","run_started_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:02:00Z","path":".github/workflows/ci.yml","html_url":"%sactions/runs/123","actor":{"login":"person"}}`, status, conclusion, RepoURL)
	}))
	defer server.Close()
	d := New(t.TempDir(), "", nil)
	d.GH.Base = server.URL
	d.GH.Client = server.Client()
	h := Handler(d, "tok", fstest.MapFS{"index.html": {Data: []byte("x")}})
	request := func(method, host, url, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://"+host+url, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	get := func(url string) *httptest.ResponseRecorder { return request("GET", "127.0.0.1:80", url, "tok") }
	for _, id := range []string{"abc", "", "123456789012345678901", "../x"} {
		before := calls
		w := get("/api/ghrun?id=" + id)
		if w.Code != 400 || calls != before {
			t.Fatalf("bad ID %q: %d calls %d", id, w.Code, calls-before)
		}
	}
	if w := get("/api/ghrun?id=999"); w.Code != 404 || !strings.Contains(w.Body.String(), "GitHub does not have this") {
		t.Fatalf("404: %d %s", w.Code, w.Body.String())
	}
	w := get("/api/ghrun?id=123")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var out CIDetail
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Run.Status != stPassed || out.Run.Word != "Passed" || out.Run.Source != "github" || out.Run.Release != "v0.1.0-rc.66" || out.Run.Workflow != "ci.yml" || out.Run.URL != RepoURL+"actions/runs/123" || out.Live || out.Posted == nil || len(out.Jobs) != 4 {
		t.Fatalf("run: %+v", out)
	}
	if out.Jobs[0].Key != "gh-1" || out.Jobs[0].Label != "go-tests (2/3)" || out.Jobs[0].Status != stPassed || out.Jobs[0].Steps[0].Status != stSkipped || out.Jobs[0].Steps[0].HasLog || out.Jobs[0].URL == "" || out.Jobs[1].URL != "" {
		t.Fatalf("jobs: %+v", out.Jobs)
	}
	if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", w.Header())
	}
	if request("POST", "127.0.0.1:80", "/api/ghrun?id=123", "tok").Code != 405 || request("GET", "evil.example", "/api/ghrun?id=123", "tok").Code != 404 || request("GET", "127.0.0.1:80", "/api/ghrun?id=123", "").Code != 401 {
		t.Fatal("guards failed")
	}
	status, conclusion = "completed", "failure"
	w = get("/api/ghrun?id=124")
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Live || out.Run.Status != stFailed || out.Jobs[0].Status != stFailed {
		t.Fatalf("failed: %+v", out)
	}
	status, conclusion = "in_progress", ""
	w = get("/api/ghrun?id=125")
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !out.Live || out.Run.Status != stRunning || out.Jobs[0].Status != stWaiting || out.Jobs[0].Steps[1].End != "" {
		t.Fatalf("live: %+v", out)
	}
	d.GH.jmu.Lock()
	for _, e := range d.GH.jc {
		e.at = time.Now().Add(-48 * time.Hour)
	}
	d.GH.jmu.Unlock()
	server.Close()
	w = get("/api/ghrun?id=123")
	if w.Code != 200 || w.Header().Get("X-Stale") != "1" {
		t.Fatalf("stale: %d %s", w.Code, w.Body.String())
	}
}
