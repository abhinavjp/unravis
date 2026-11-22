package releasedash

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLogs(t *testing.T, dir string) {
	t.Helper()
	ld := filepath.Join(dir, "logs")
	if err := os.MkdirAll(ld, 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(name, body string, mod time.Time) {
		p := filepath.Join(ld, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, mod, mod)
	}
	put("run-20261121T095900Z.log", strings.Join([]string{
		"2026-11-21T09:59:00Z Pinned commit 0123456",
		"2026-11-21T10:00:01Z [1/7] Preflight",
		"2026-11-21T10:00:30Z Preflight OK",
		"2026-11-21T10:01:00Z [2/7] Tag",
		"2026-11-21T10:02:00Z [3/7] Waiting for the build",
		"2026-11-21T10:10:00Z done on SECRET-PC",
	}, "\n")+"\n", time.Date(2026, 11, 21, 10, 21, 0, 0, time.UTC))
	put("run-20261121T105900Z.log", strings.Join([]string{
		"2026-11-21T10:59:00Z [1/7] Preflight",
		"2026-11-21T11:00:00Z passphrase: hunter2hunter2 on SECRET-PC",
		"2026-11-21T11:00:10Z [3/7] Waiting for the build",
		"2026-11-21T11:00:20Z build job failed",
		"2026-11-21T11:00:30Z STOPPED at Wait : CI failed",
	}, "\n")+"\n", time.Date(2026, 11, 21, 11, 0, 31, 0, time.UTC))
	sample, err := os.ReadFile("testdata/ci-sample.log")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(filepath.Join(ld, "ci-v0.1.0-rc.55-ci-777.log.gz")) //nolint:gosec // test file in t.TempDir
	zw := gzip.NewWriter(f)
	_, _ = zw.Write(sample)
	_ = zw.Close()
	_ = f.Close()
}

func newDash(t *testing.T) *Dash {
	dir := writeLocal(t)
	writeLogs(t, dir)
	return New(dir, "", nil)
}

func TestReleaseStepTimesAndLogRanges(t *testing.T) {
	d := newDash(t)
	det, err := d.Release("0.1.0-rc.55", time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(det.Steps) != 7 || !det.Recorded || det.Running {
		t.Fatalf("%+v", det)
	}
	tag := det.Steps[1]
	if tag.Name != "Tag" || tag.Seconds != 60 || tag.Log != "run-20261121T095900Z.log" || tag.From != 3 || tag.To != 4 {
		t.Fatalf("tag step %+v", tag)
	}
	if b := det.Steps[2]; b.Seconds != 480 { // 10:02 to 10:10
		t.Fatalf("build %+v", b)
	}
	if det.Failure == nil || det.Failure.Stage != "Verify" || det.Failure.Message != "asserts failed" {
		t.Fatalf("failure %+v", det.Failure)
	}
	kinds := ""
	for _, l := range det.Logs {
		kinds += l.Kind + ","
	}
	if kinds != "run,ci," {
		t.Fatalf("logs %s (the other release's run log must not be mapped)", kinds)
	}
}

func TestFailureComesFromTheStoppedLine(t *testing.T) {
	d := newDash(t)
	det, _ := d.Release("0.1.0-rc.56", time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	f := det.Failure
	if f == nil || f.Stage != "Build" || f.Message != "CI failed" || f.Log != "run-20261121T105900Z.log" || f.Line != 5 {
		t.Fatalf("%+v", f)
	}
	j := strings.Join(f.Context, "|")
	if !strings.Contains(j, "build job failed") || strings.Contains(j, "hunter2") || strings.Contains(j, "SECRET") {
		t.Fatalf("context %q", j)
	}
}

func TestLogRouteRedactsAndRefusesOtherFiles(t *testing.T) {
	d := newDash(t)
	mux := http.NewServeMux()
	d.registerDetail(mux)
	get := func(u string) (int, string) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", u, nil))
		return rec.Code, rec.Body.String()
	}
	code, body := get("/api/log?file=run-20261121T105900Z.log")
	if code != 200 || strings.Contains(body, "hunter2") || strings.Contains(body, "SECRET") || !strings.Contains(body, "STOPPED at Wait") {
		t.Fatalf("%d %s", code, body)
	}
	var page struct {
		Lines []LogLine
		Total int
	}
	_ = json.Unmarshal([]byte(body), &page)
	if page.Total != 5 {
		t.Fatalf("total %d", page.Total)
	}
	_, body = get("/api/log?file=run-20261121T105900Z.log&from=3&n=1")
	if !strings.Contains(body, "build job failed") || strings.Contains(body, "STOPPED") {
		t.Fatalf("page %s", body)
	}
	for _, f := range []string{"../audit.jsonl", "audit.jsonl", "run-x.log", "..%5Caudit.jsonl", ""} {
		if code, _ := get("/api/log?file=" + f); code != 404 {
			t.Errorf("file %q gave %d", f, code)
		}
	}
	if code, _ := get("/api/release?v=nonsense"); code != 400 {
		t.Errorf("bad version %d", code)
	}
}

func TestTestsReadSavedCILog(t *testing.T) {
	d := newDash(t)
	rep, err := d.Tests("0.1.0-rc.55")
	if err != nil || len(rep.Runs) != 1 || rep.Runs[0].Kind != "ci" || rep.Runs[0].Run != "777" {
		t.Fatalf("%v %+v", err, rep)
	}
	if res := rep.Runs[0].Results; res.Passed < 3000 || len(res.Suites) < 3 {
		t.Fatalf("results %+v", res.Passed)
	}
	none, _ := d.Tests("0.1.0-rc.56")
	if len(none.Runs) != 0 || none.Note == "" {
		t.Fatalf("%+v", none)
	}
}

func fakeGitHub(t *testing.T, hits *int) *GH {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		switch {
		case strings.HasPrefix(r.URL.Path, "/commits/v0.1.0-rc.55/check-runs"):
			w.Header().Set("ETag", `"a"`)
			_, _ = w.Write([]byte(`{"check_runs":[{"name":"go","status":"completed","conclusion":"success","started_at":"2026-11-21T10:00:00Z","completed_at":"2026-11-21T10:03:30Z","html_url":"https://github.com/abhinavjp/unravis/actions/runs/1/job/2"},{"name":"evil","html_url":"https://evil.example/x"}]}`))
		case strings.HasPrefix(r.URL.Path, "/compare/v0.1.0-rc.54...v0.1.0-rc.55"):
			_, _ = w.Write([]byte(`{"total_commits":2,"commits":[{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","html_url":"https://github.com/abhinavjp/unravis/commit/aaa","commit":{"message":"First thing (#41)\n\nbody"}},{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","commit":{"message":"Second thing"}}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return &GH{Base: srv.URL}
}

func TestChecksChangesAndCaching(t *testing.T) {
	hits := 0
	d := newDash(t)
	d.GH = fakeGitHub(t, &hits)
	ctx := context.Background()
	cs, _, err := d.GH.Checks(ctx, "v0.1.0-rc.55", time.Hour)
	if err != nil || len(cs) != 2 || cs[0].Seconds != 210 || cs[0].URL == "" || cs[1].URL != "" {
		t.Fatalf("%v %+v", err, cs)
	}
	_, _, _ = d.GH.Checks(ctx, "v0.1.0-rc.55", time.Hour)
	if hits != 1 {
		t.Fatalf("answer was not kept: %d calls", hits)
	}
	ch, _, err := d.GH.Changes(ctx, "v0.1.0-rc.54", "v0.1.0-rc.55", time.Hour)
	if err != nil || ch.Total != 2 || len(ch.Commits) != 2 || ch.Commits[0].Title != "Second thing" || ch.Commits[1].PR != 41 || ch.Commits[1].SHA != "aaaaaaa" {
		t.Fatalf("%v %+v", err, ch)
	}
	if _, _, err := d.GH.Changes(ctx, "", "v0.1.0-rc.55", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GH.GetJSON(ctx, "/x/../../etc", time.Hour, &struct{}{}); err == nil {
		t.Fatal("path walk accepted")
	}
	if _, err := d.GH.GetJSON(ctx, "/nothing", time.Hour, &struct{}{}); err == nil {
		t.Fatal("404 must be an error")
	}
}

func TestPreviousTagSameChannel(t *testing.T) {
	rels := []Rel{{Tag: "v0.1.0-rc.55"}, {Tag: "v0.1.0-rc.54"}, {Tag: "v0.1.0-rc.56", Draft: true}, {Tag: "v0.0.9"}, {Tag: "bad"}}
	if got := previousTag(rels, "0.1.0-rc.56"); got != "v0.1.0-rc.55" {
		t.Fatal(got)
	}
	if got := previousTag(rels, "0.1.0-rc.54"); got != "" {
		t.Fatal(got)
	}
}

func TestArtifactsShowDigestKeyAndManifest(t *testing.T) {
	d := newDash(t)
	d.GH.URL = (func() string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`[{"tag_name":"v0.1.0-rc.55","published_at":"2026-11-21T10:12:00Z","assets":[{"name":"Unravis.exe","size":12345,"digest":"sha256:` + strings.Repeat("ab", 32) + `","browser_download_url":"https://github.com/abhinavjp/unravis/releases/download/v0.1.0-rc.55/Unravis.exe"},{"name":"x","digest":"junk","browser_download_url":"https://evil.example/x"}]}]`))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	})()
	info, err := d.Artifacts(context.Background(), "0.1.0-rc.55")
	if err != nil || len(info.Assets) != 2 || info.Assets[0].Digest != strings.Repeat("ab", 32) || info.Assets[1].Digest != "" || info.Assets[1].URL != "" {
		t.Fatalf("%v %+v", err, info)
	}
}

func TestReleaseWithoutLogsHasEmptyList(t *testing.T) {
	d := New(writeLocal(t), "", nil) // no logs folder at all
	det, err := d.Release("0.1.0-rc.55", time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if det.Logs == nil || len(det.Logs) != 0 {
		t.Fatalf("logs must be an empty list, got %#v", det.Logs)
	}
}

func TestGitHubRoutesAnswer200WithErrorWhenUnreachable(t *testing.T) {
	d := newDash(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	srv.Close() // nothing listens any more
	// The release list is seeded, so this test never reaches the real GitHub: the previous release is known, and the compare call hits the closed server.
	d.GH = &GH{Base: srv.URL, rels: []Rel{{Tag: "v0.1.0-rc.54"}, {Tag: "v0.1.0-rc.55"}}, at: time.Now()}
	mux := http.NewServeMux()
	d.registerDetail(mux)
	for _, p := range []string{"/api/release/checks?v=0.1.0-rc.55", "/api/release/changes?v=0.1.0-rc.55"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		var b struct {
			Data  any    `json:"data"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		if rec.Code != 200 || b.Error == "" || b.Data != nil {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body.String())
		}
	}
}
