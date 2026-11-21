package releasedash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const auditText = `{"ts":"2026-11-21T10:00:00.0000000Z","prev":"0","event":"run-start","version":"0.1.0-rc.55","machine":"SECRET-PC","user":"someone"}
{"ts":"2026-11-21T10:01:00Z","event":"preflight-ok","version":"0.1.0-rc.55","key":"rc-1.key.json"}
{"ts":"2026-11-21T10:02:00Z","event":"tagged","version":"0.1.0-rc.55"}
{"ts":"2026-11-21T10:10:00Z","event":"built","version":"0.1.0-rc.55","run":"123456"}
{"ts":"2026-11-21T10:11:00Z","event":"signed","tag":"v0.1.0-rc.55"}
{"ts":"2026-11-21T10:12:00Z","event":"published","version":"0.1.0-rc.55"}
{"ts":"2026-11-21T10:13:00Z","event":"feed-updated","version":"0.1.0-rc.55"}
{"ts":"2026-11-21T10:20:00Z","event":"verify-failed","version":"0.1.0-rc.55","run":"789","reason":"asserts failed"}
{"ts":"2026-11-21T10:21:00Z","event":"withdrawn","version":"0.1.0-rc.55"}
{"ts":"2026-11-21T11:00:00Z","event":"stopped","version":"0.1.0-rc.56","stage":"Wait","reason":"CI failed"}
not json
`

func writeLocal(t *testing.T) string {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), []byte(auditText), 0o600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"version":"0.1.0-rc.56","stage":"Tag","status":"stopped","reason":"CI failed","key":"x"}`), 0o600)
	return dir
}

func TestReadLocalDropsMachineAndKeyFields(t *testing.T) {
	l := ReadLocal(writeLocal(t))
	if !l.Found || l.Run == nil || l.Run.Status != "stopped" || len(l.Events) != 10 {
		t.Fatalf("got %+v", l)
	}
	for _, e := range l.Events {
		if strings.Contains(e.Reason+e.Name+e.Version, "SECRET") {
			t.Fatal("machine leaked")
		}
	}
	if ReadLocal(filepath.Join(t.TempDir(), "none")).Found {
		t.Fatal("missing folder must not be found")
	}
}

func TestMergeStepsVerifyAndWithdrawn(t *testing.T) {
	loc := ReadLocal(writeLocal(t))
	gh := []Rel{{Tag: "v0.1.0-rc.55", Published: "2026-11-21T10:12:00Z", URL: RepoURL + "releases/tag/v0.1.0-rc.55"}, {Tag: "v0.1.0-rc.54"}, {Tag: "weird"}}
	rels := merge(gh, []Feed{{Channel: "rc", Version: "0.1.0-rc.54", Blocked: []string{"0.1.0-rc.55"}}}, loc)
	if len(rels) != 3 || rels[0].Version != "0.1.0-rc.56" || rels[1].Version != "0.1.0-rc.55" {
		t.Fatalf("order %+v", rels)
	}
	r55, r54, r56 := rels[1], rels[2], rels[0]
	if !r55.Withdrawn || r55.Verify != "failed" || !r55.Recorded || r55.RunURL != RepoURL+"actions/runs/789" {
		t.Fatalf("r55 %+v", r55)
	}
	if r55.Steps[3].State != "done" {
		t.Fatalf("sign (tag field) not read: %+v", r55.Steps)
	}
	if r54.Recorded || !r54.OnFeed || r54.Steps[5].State != "done" || r54.Steps[0].State != "unknown" || r54.Verify != "unknown" {
		t.Fatalf("r54 %+v", r54)
	}
	if r56.Steps[2].State != "failed" || r56.Steps[2].Reason != "CI failed" {
		t.Fatalf("r56 %+v", r56.Steps)
	}
}

func TestGitHubCachesAndSurvivesErrors(t *testing.T) {
	calls, fail := 0, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			w.WriteHeader(403)
			return
		}
		if r.Header.Get("If-None-Match") == `"e1"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"e1"`)
		w.Write([]byte(`[{"tag_name":"v1.0.0","html_url":"https://evil.example/x"}]`))
	}))
	defer srv.Close()
	g := &GH{URL: srv.URL, MinGap: time.Hour}
	rels, _, err := g.Get(context.Background(), false)
	if err != "" || len(rels) != 1 || rels[0].URL != "" {
		t.Fatalf("%v %q", rels, err)
	}
	g.Get(context.Background(), false)
	if calls != 1 {
		t.Fatalf("not cached: %d", calls)
	}
	g.at = time.Now().Add(-time.Hour)
	g.Get(context.Background(), false) // 304
	fail, g.at = true, time.Now().Add(-time.Hour)
	rels, _, err = g.Get(context.Background(), false)
	if len(rels) != 1 || err == "" {
		t.Fatalf("last list must stay: %v %q", rels, err)
	}
}

func TestHandlerNeedsCodeLoopbackAndReadOnly(t *testing.T) {
	d := New(t.TempDir(), "", nil)
	d.GH = &GH{URL: "http://127.0.0.1:1/x", MinGap: time.Hour}
	d.FeedGap = time.Hour
	d.feedAt = time.Now()
	h := Handler(d, "tok", fstest.MapFS{"index.html": {Data: []byte("hi")}})
	do := func(method, host, path, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if c := do("GET", "127.0.0.1:80", "/api/snapshot", "").Code; c != 401 {
		t.Fatal("no cookie", c)
	}
	if c := do("GET", "evil.example", "/api/snapshot", "tok").Code; c != 404 {
		t.Fatal("rebind", c)
	}
	if c := do("POST", "127.0.0.1:80", "/api/snapshot", "tok").Code; c != 405 {
		t.Fatal("write", c)
	}
	if w := do("GET", "127.0.0.1:80", "/?t=tok", ""); w.Code != 303 || len(w.Result().Cookies()) != 1 {
		t.Fatal("code exchange", w.Code)
	}
	if w := do("GET", "127.0.0.1:80", "/api/snapshot", "tok"); w.Code != 200 || !strings.Contains(w.Body.String(), `"releases"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestClipHidesPathsAndKeepsUTF8(t *testing.T) {
	if got := clip(`cannot open D:\secret\rc-1.key.json now`); strings.Contains(got, "secret") || !strings.Contains(got, "<path>") {
		t.Fatal(got)
	}
	long := strings.Repeat("é", 400)
	if got := clip(long); len([]rune(got)) != maxReason || strings.ContainsRune(got, '\uFFFD') {
		t.Fatal("cut mid character")
	}
}

func TestCancelledRequestIsNotRemembered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) }))
	defer srv.Close()
	g := &GH{URL: srv.URL, MinGap: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := g.Get(ctx, false); err != "" {
		t.Fatal("a cancelled call must not leave an error behind:", err)
	}
	if _, okAt, err := g.Get(context.Background(), false); err != "" || okAt.IsZero() {
		t.Fatal("the next call must try again", err)
	}
}

func TestStateFileMustBeSmallAndRegular(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "state.json"), append([]byte(`{"version":"0.1.0-rc.1","pad":"`), append([]byte(strings.Repeat("a", 70<<10)), []byte(`"}`)...)...), 0o600)
	if ReadLocal(dir).Run != nil {
		t.Fatal("an oversized state file must be ignored")
	}
}
