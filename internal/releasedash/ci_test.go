package releasedash

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"api-audit-explorer/internal/localci"
)

// ciFixture is a small CI store and a release data folder that joins it by commit.
type ciFixture struct {
	dir, data string
	d         *Dash
	h         http.Handler
	now       time.Time
	passID    string
	failID    string
	liveID    string
	liveReq   string
}

const (
	fxTokenSecret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	fxPassSHA     = "aaaaaaa1111111111111111111111111111111aa"
	fxFailSHA     = "bbbbbbb2222222222222222222222222222222bb"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o750))
	must(t, os.WriteFile(path, []byte(text), 0o600))
}

func newCIFixture(t *testing.T) *ciFixture {
	t.Helper()
	f := &ciFixture{dir: t.TempDir(), data: t.TempDir(), now: time.Now().UTC().Truncate(time.Second)}
	st, err := localci.OpenStore(f.dir)
	must(t, err)

	// 1. a pull request that passed, with a log that holds a secret and a private key block above the part a page asks for.
	start := f.now.Add(-3 * time.Hour)
	id, dir, err := st.NewRun(start, "pr12-fast-gate")
	must(t, err)
	f.passID = id
	writeFile(t, filepath.Join(dir, "checkout.log"), "fetching\nmerged\n")
	writeFile(t, filepath.Join(dir, "summary.md"), "## Result\ntoken "+fxTokenSecret+"\n")
	var logText strings.Builder
	logText.WriteString("start\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234567890abcdef\n-----END RSA PRIVATE KEY-----\n")
	for i := 0; i < 20; i++ {
		logText.WriteString("line " + strings.Repeat("x", i) + "\n")
	}
	logText.WriteString("token " + fxTokenSecret + "\r\n")
	writeFile(t, filepath.Join(dir, "logs", "build", "01-checkout.log"), "ok\n")
	writeFile(t, filepath.Join(dir, "logs", "build", "02-go-test.log"), logText.String())
	rec := &localci.RunResult{Workflow: "fast-gate.yml", Conclusion: "success", Start: start, End: start.Add(90 * time.Second), Jobs: []*localci.JobResult{
		{Key: "build", ID: "build", Label: "build", Status: "success", Start: start, End: start.Add(80 * time.Second), Steps: []localci.StepResult{
			{Index: 1, Name: "checkout", Conclusion: "success", Start: start, End: start.Add(time.Second)},
			{Index: 2, Name: "go test", Conclusion: "success", Start: start.Add(time.Second), End: start.Add(80 * time.Second)},
			{Index: 3, Name: "no log step", Conclusion: "skipped"},
		}},
	}}
	must(t, localci.WriteRunRecord(dir, rec))
	must(t, st.AppendIndex(localci.IndexEntry{RunID: id, Workflow: "fast-gate.yml", Event: "pull_request", Repo: "o/r", PR: 12, Author: "ann", BaseRef: "develop", HeadRef: "feat",
		HeadSHA: fxPassSHA, CommitSHA: fxPassSHA, Conclusion: "success", Start: start, End: rec.End}, nil))

	// 2. a push that failed.
	start2 := f.now.Add(-2 * time.Hour)
	id2, dir2, err := st.NewRun(start2, "push-develop-fast-gate")
	must(t, err)
	f.failID = id2
	rec2 := &localci.RunResult{Workflow: "fast-gate.yml", Conclusion: "failure", Start: start2, End: start2.Add(30 * time.Second), Jobs: []*localci.JobResult{
		{Key: "_checkout", ID: "_checkout", Label: "checkout", Status: "failure", Reason: "merge conflict for " + fxTokenSecret, Pseudo: true, Start: start2, End: start2.Add(time.Second)},
	}}
	must(t, localci.WriteRunRecord(dir2, rec2))
	writeFile(t, filepath.Join(dir2, "checkout.log"), "merge conflict\n")
	must(t, st.AppendIndex(localci.IndexEntry{RunID: id2, Workflow: "fast-gate.yml", Event: "push", Repo: "o/r", HeadRef: "develop", CommitSHA: fxFailSHA, Conclusion: "failure",
		Reason: "conflict", Start: start2, End: rec2.End}, nil))

	// 3. a running request with a run folder that has no record yet, a queued one, and a stopped one.
	q, err := localci.OpenQueue(localci.QueueDir(f.dir))
	must(t, err)
	live, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Ref: "feat2", Base: "develop", Number: 13, SHA: "ccccccc3333333333333333333333333333333cc", Workflow: "fast-gate.yml"})
	must(t, err)
	_, err = q.Claim("a1", nil)
	must(t, err)
	f.liveReq = live.ID
	lid, ldir, err := st.NewRun(time.Now().UTC().Add(time.Second), "pr13-fast-gate")
	must(t, err)
	f.liveID = lid
	writeFile(t, filepath.Join(ldir, "logs", "build", "01-checkout.log"), "one\ntwo\nthree partial")
	_, _, err = q.Enqueue(localci.Request{Kind: localci.KindPush, Repo: "o/r", Ref: "master", SHA: "ddddddd4444444444444444444444444444444dd", Workflow: "fast-gate.yml"})
	must(t, err)

	// agents and the status ledger
	hb := localci.Heartbeat{Name: "a1", Slots: 2, State: "ready", Started: f.now, LastSeen: time.Now(), Jobs: []localci.AgentJob{{ID: live.ID, Label: "pr 13 into develop", Since: f.now}},
		Status: &localci.StatusHealth{Posted: 3, Mode: "shadow", LastError: "boom " + fxTokenSecret}}
	b, _ := json.Marshal(hb)
	writeFile(t, filepath.Join(f.dir, "agents", "a1.json"), string(b))
	old := localci.Heartbeat{Name: "old", Slots: 1, State: "ready", Started: f.now.Add(-48 * time.Hour), LastSeen: f.now.Add(-24 * time.Hour)}
	b, _ = json.Marshal(old)
	writeFile(t, filepath.Join(f.dir, "agents", "old.json"), string(b))
	writeFile(t, filepath.Join(f.dir, "status", "ledger.json"),
		`{"o/r@`+fxPassSHA+` fast-gate":{"state":"success","description":"all green","at":"`+f.now.Format(time.RFC3339)+`"}}`)

	// a release that was made from the passing commit
	writeFile(t, filepath.Join(f.data, "audit.jsonl"), `{"ts":"`+f.now.Format(time.RFC3339Nano)+`","event":"run-start","version":"1.2.3-rc.1","commit":"`+fxPassSHA[:7]+`","machine":"BUILDBOX","user":"zed"}`+"\n")

	f.d = New(f.data, "", nil)
	f.d.Projects = []Project{{ID: "unravis", Name: "Unravis", Repo: "o/r", CIStore: f.dir, Branches: []string{"develop", "master"}}}
	must(t, f.d.Projects[0].Normalize())
	f.h = Handler(f.d, "tok", fstest.MapFS{"index.html": {Data: []byte("hi")}})
	return f
}

func (f *ciFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(t, "GET", "127.0.0.1:80", path, "tok")
}

func (f *ciFixture) do(t *testing.T, method, host, path, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://"+host+path, nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return v
}

func TestCIRoutesAreReadOnlyLoopbackAndCSP(t *testing.T) {
	f := newCIFixture(t)
	for _, p := range []string{"/api/projects", "/api/runs", "/api/run?id=" + f.passID, "/api/run/log?id=" + f.passID + "&job=build&step=2", "/api/overview"} {
		if c := f.do(t, "POST", "127.0.0.1:80", p, "tok").Code; c != 405 {
			t.Errorf("POST %s: %d", p, c)
		}
		if c := f.do(t, "GET", "evil.example", p, "tok").Code; c != 404 {
			t.Errorf("rebind %s: %d", p, c)
		}
		if c := f.do(t, "GET", "127.0.0.1:80", p, "").Code; c != 401 {
			t.Errorf("no cookie %s: %d", p, c)
		}
		w := f.get(t, p)
		if w.Code != 200 {
			t.Errorf("GET %s: %d %s", p, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("headers of %s: %v", p, w.Header())
		}
		if h := f.do(t, "HEAD", "127.0.0.1:80", p, "tok"); h.Code != 200 {
			t.Errorf("HEAD %s: %d", p, h.Code)
		}
	}
}

func TestCIProjectsRoute(t *testing.T) {
	f := newCIFixture(t)
	out := decode[struct{ Projects []CIProject }](t, f.get(t, "/api/projects"))
	if len(out.Projects) != 1 || out.Projects[0].ID != "unravis" || !out.Projects[0].CIFound || !out.Projects[0].Releases || len(out.Projects[0].Steps) != 7 || out.Projects[0].Channels[0] != "rc" {
		t.Fatalf("%+v", out)
	}
	if strings.Contains(f.get(t, "/api/projects").Body.String(), f.dir) {
		t.Fatal("the store path must not be sent")
	}
	// A Dash made without a record shows one default project that has no runs.
	d := New(t.TempDir(), "", nil)
	h := Handler(d, "tok", fstest.MapFS{"index.html": {Data: []byte("x")}})
	r := httptest.NewRequest("GET", "http://127.0.0.1:80/api/projects", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: "tok"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var def struct{ Projects []CIProject }
	must(t, json.Unmarshal(w.Body.Bytes(), &def))
	if len(def.Projects) != 1 || def.Projects[0].HasCI || len(def.Projects[0].Steps) != 7 || def.Projects[0].Channels[1] != "stable" {
		t.Fatalf("%+v", def)
	}
}

func TestCIRunsListFiltersAndPages(t *testing.T) {
	f := newCIFixture(t)
	all := decode[CIPage](t, f.get(t, "/api/runs"))
	if !all.CIFound || all.Total != 4 || len(all.Runs) != 4 {
		t.Fatalf("want 4 rows (2 ended, 1 running, 1 waiting): %+v", all)
	}
	byStatus := map[string]int{}
	for _, r := range all.Runs {
		byStatus[r.Status]++
		if r.Word == "" {
			t.Errorf("row without a status word: %+v", r)
		}
	}
	if byStatus[stPassed] != 1 || byStatus[stFailed] != 1 || byStatus[stRunning] != 1 || byStatus[stWaiting] != 1 {
		t.Fatalf("statuses %v", byStatus)
	}
	// newest first: waiting or running rows come before the runs that ended hours ago.
	if all.Runs[len(all.Runs)-1].ID != f.passID {
		t.Fatalf("oldest must be last: %+v", all.Runs)
	}
	// pass row carries the release that was made from its commit
	for _, r := range all.Runs {
		if r.ID == f.passID && r.Release != "1.2.3-rc.1" {
			t.Fatalf("release chip: %+v", r)
		}
		if r.ID == f.failID && r.Release != "" {
			t.Fatalf("no release for the failed push: %+v", r)
		}
	}
	for q, want := range map[string]int{"status=passed": 1, "status=failed": 1, "status=waiting": 1, "pr=12": 1, "branch=develop": 3, "sha=aaaaaaa1": 1, "release=1.2.3-rc.1": 1, "release=9.9.9": 0} {
		got := decode[CIPage](t, f.get(t, "/api/runs?"+q))
		if got.Total != want || len(got.Runs) != want {
			t.Errorf("%s: total %d rows %d, want %d", q, got.Total, len(got.Runs), want)
		}
	}
	// paging by cursor walks every row once, in order
	var ids []string
	cur := ""
	for i := 0; i < 10; i++ {
		p := decode[CIPage](t, f.get(t, "/api/runs?limit=2&cursor="+cur))
		for _, r := range p.Runs {
			ids = append(ids, r.ID)
		}
		if p.Next == "" {
			break
		}
		cur = p.Next
	}
	if len(ids) != 4 {
		t.Fatalf("paged %v", ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("a row came twice: %v", ids)
		}
		seen[id] = true
	}
	for _, bad := range []string{"status=bogus", "pr=abc", "pr=0", "sha=zz", "sha=ab", "limit=0", "limit=x", "branch=" + strings.Repeat("a", 200), "release=" + strings.Repeat("a", 80)} {
		if c := f.get(t, "/api/runs?"+bad).Code; c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
	if c := f.get(t, "/api/runs?project=nope").Code; c != 404 {
		t.Errorf("unknown project: %d", c)
	}
	if got := decode[CIPage](t, f.get(t, "/api/runs?limit=1000&cursor=garbage")); len(got.Runs) != 4 {
		t.Errorf("a cursor that does not parse starts at the top: %+v", got)
	}
}

func TestCIRunDetailRecordedLiveAndRequest(t *testing.T) {
	f := newCIFixture(t)
	d := decode[CIDetail](t, f.get(t, "/api/run?id="+f.passID))
	if d.Run.Status != stPassed || d.Run.Word != "Passed" || d.Live || len(d.Jobs) != 1 || len(d.Jobs[0].Steps) != 3 || !d.Checkout {
		t.Fatalf("%+v", d)
	}
	if !d.Jobs[0].Steps[1].HasLog || d.Jobs[0].Steps[2].HasLog || d.Jobs[0].Steps[2].Word != "Skipped" || d.Jobs[0].Steps[1].Ms != 79000 {
		t.Fatalf("steps %+v", d.Jobs[0].Steps)
	}
	if strings.Contains(d.Summary, fxTokenSecret) || !strings.Contains(d.Summary, "token hidden") {
		t.Fatalf("summary not redacted: %q", d.Summary)
	}
	if d.Run.Release != "1.2.3-rc.1" || len(d.Posted) != 1 || d.Posted[0].Status != stPassed || d.Posted[0].Context != "fast-gate" {
		t.Fatalf("release or posted: %+v %+v", d.Run, d.Posted)
	}
	// a run that stopped before its jobs: the pseudo job is marked, and its reason is redacted
	fd := decode[CIDetail](t, f.get(t, "/api/run?id="+f.failID))
	if fd.Run.Status != stFailed || len(fd.Jobs) != 1 || !fd.Jobs[0].Pseudo || strings.Contains(fd.Jobs[0].Reason, fxTokenSecret) {
		t.Fatalf("%+v", fd)
	}
	// a run that is going has no record: it is found by its folder and request
	ld := decode[CIDetail](t, f.get(t, "/api/run?id="+f.liveID))
	if !ld.Live || ld.Run.Status != stRunning || ld.Run.Word != "Running" || len(ld.Jobs) != 1 || len(ld.Jobs[0].Steps) != 1 || ld.Jobs[0].Steps[0].Status != stRunning {
		t.Fatalf("%+v", ld)
	}
	// the request id of the same run opens the same page
	rd := decode[CIDetail](t, f.get(t, "/api/run?id="+f.liveReq))
	if rd.Run.ID != f.liveID {
		t.Fatalf("request id should open the run: %+v", rd.Run)
	}
	// a request that waits has a page too, with no jobs
	var waitReq string
	for _, r := range decode[CIPage](t, f.get(t, "/api/runs?status=waiting")).Runs {
		waitReq = r.ID
	}
	wd := decode[CIDetail](t, f.get(t, "/api/run?id="+waitReq))
	if wd.Run.Status != stWaiting || wd.Run.Word != "Waiting" || len(wd.Jobs) != 0 {
		t.Fatalf("%+v", wd)
	}
}

func TestCIRunIDsCannotEscapeTheStore(t *testing.T) {
	f := newCIFixture(t)
	writeFile(t, filepath.Join(f.dir, "secret.txt"), "top secret\n")
	for _, id := range []string{"..", "../..", "..%2F..", "..\\..", "a/b", "20260101-000000-x/../..", "/etc/passwd", "C:\\Windows", "%00", strings.Repeat("a", 300)} {
		w := f.get(t, "/api/run?id="+id)
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("id %q: %d", id, w.Code)
		}
		if strings.Contains(w.Body.String(), "top secret") {
			t.Errorf("id %q leaked", id)
		}
		w = f.get(t, "/api/run/log?id="+id+"&job=build&step=1")
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("log id %q: %d", id, w.Code)
		}
	}
	for _, job := range []string{"..", "../..", "a/b", "build/../..", "..\\x", "", "nojob"} {
		w := f.get(t, "/api/run/log?id="+f.passID+"&job="+job+"&step=1")
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("job %q: %d", job, w.Code)
		}
	}
	for _, step := range []string{"0", "-1", "10000", "x", "99"} {
		w := f.get(t, "/api/run/log?id="+f.passID+"&job=build&step="+step)
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("step %q: %d", step, w.Code)
		}
	}
	if c := f.get(t, "/api/run?id=20990101-000000-nothing").Code; c != 404 {
		t.Errorf("unknown run: %d", c)
	}
	if c := f.get(t, "/api/run").Code; c != 400 {
		t.Errorf("no id: %d", c)
	}
}

func TestCIStepLogWindowRedactionAndLive(t *testing.T) {
	f := newCIFixture(t)
	u := "/api/run/log?id=" + f.passID + "&job=build&step=2"
	full := decode[CILog](t, f.get(t, u))
	if full.Total != 25 || full.From != 0 || full.Live {
		t.Fatalf("%+v", full)
	}
	b, _ := json.Marshal(full)
	if strings.Contains(string(b), fxTokenSecret) || strings.Contains(string(b), "MIIEow") || strings.Contains(string(b), "\r") {
		t.Fatalf("log not redacted: %s", b)
	}
	if full.Lines[1].Text != "<private key hidden>" {
		t.Fatalf("%+v", full.Lines[:4])
	}
	// a window that starts inside the key block still hides the key (redaction runs from line 0)
	w := decode[CILog](t, f.get(t, u+"&from=2&n=2"))
	if len(w.Lines) != 2 || w.From != 2 || w.Total != 25 || w.Lines[0].Text != "<private key hidden>" {
		t.Fatalf("%+v", w)
	}
	// tail returns the last lines
	tl := decode[CILog](t, f.get(t, u+"&tail=1&n=3"))
	if len(tl.Lines) != 3 || tl.From != 22 || !strings.Contains(tl.Lines[2].Text, "token hidden") {
		t.Fatalf("%+v", tl)
	}
	// a from past the end is an empty window, not an error
	if e := decode[CILog](t, f.get(t, u+"&from=999")); len(e.Lines) != 0 || e.From != 25 {
		t.Fatalf("%+v", e)
	}
	// the checkout log
	co := decode[CILog](t, f.get(t, "/api/run/log?id="+f.passID+"&job=_checkout"))
	if co.Total != 2 || !strings.Contains(co.Title, "fetching") {
		t.Fatalf("%+v", co)
	}
	// a log of a run that is going drops the line still being written
	lv := decode[CILog](t, f.get(t, "/api/run/log?id="+f.liveID+"&job=build&step=1"))
	if !lv.Live || lv.Total != 2 || lv.Lines[1].Text != "two" {
		t.Fatalf("%+v", lv)
	}
	// and the page gets the next line once the file grows
	p := filepath.Join(f.dir, "runs", f.liveID, "logs", "build", "01-checkout.log")
	time.Sleep(10 * time.Millisecond)
	writeFile(t, p, "one\ntwo\nthree partial line now complete\nfour")
	lv = decode[CILog](t, f.get(t, "/api/run/log?id="+f.liveID+"&job=build&step=1"))
	if lv.Total != 3 || !strings.Contains(lv.Lines[2].Text, "complete") {
		t.Fatalf("%+v", lv)
	}
}

func TestCIOverview(t *testing.T) {
	f := newCIFixture(t)
	o := decode[CIOverview](t, f.get(t, "/api/overview"))
	if !o.CIFound || len(o.Running) != 1 || len(o.Waiting) != 1 || len(o.Pulls) != 2 {
		t.Fatalf("%+v", o)
	}
	if len(o.Branches) != 2 || o.Branches[0].Branch != "develop" || o.Branches[0].Run == nil || o.Branches[0].Run.ID != f.failID || o.Branches[1].Run != nil {
		t.Fatalf("branches %+v", o.Branches)
	}
	if len(o.Agents) != 2 {
		t.Fatalf("agents %+v", o.Agents)
	}
	for _, a := range o.Agents {
		switch a.Name {
		case "a1":
			if !a.Fresh || a.Silent != "" || len(a.Jobs) != 1 || a.Posted != 3 || strings.Contains(a.LastError, fxTokenSecret) {
				t.Errorf("a1 %+v", a)
			}
		case "old":
			if a.Fresh || a.Silent == "" {
				t.Errorf("old %+v", a)
			}
		}
	}
	if len(o.Posted) != 1 || o.Posted[0].Word != "Passed" {
		t.Fatalf("posted %+v", o.Posted)
	}
	if o.Shadow.Available || o.Shadow.Gate != 20 {
		t.Fatalf("no saved comparison yet: %+v", o.Shadow)
	}
	must(t, localci.SaveCompare(f.dir, &localci.CompareReport{Repo: "o/r", Generated: f.now, Agree: 7, Disagree: 1, Streak: 5, Gate: 20,
		Disagreeing: []string{"pr 9 abc build: local pass, github fail " + fxTokenSecret}, Warnings: []string{"w"}}))
	o = decode[CIOverview](t, f.get(t, "/api/overview"))
	if !o.Shadow.Available || o.Shadow.Streak != 5 || o.Shadow.Disagree != 1 || len(o.Shadow.Disagreeing) != 1 || strings.Contains(o.Shadow.Disagreeing[0], fxTokenSecret) {
		t.Fatalf("%+v", o.Shadow)
	}
}

func TestCIStaleRunningRequest(t *testing.T) {
	f := newCIFixture(t)
	must(t, os.Remove(filepath.Join(f.dir, "agents", "a1.json")))
	for _, r := range decode[CIPage](t, f.get(t, "/api/runs?status=running")).Runs {
		if !r.Stale || r.Note == "" {
			t.Fatalf("a running request whose agent is gone must say so: %+v", r)
		}
	}
}

func TestCINoStoreIsEmptyNotAnError(t *testing.T) {
	d := New(t.TempDir(), "", nil)
	d.Projects = []Project{{ID: "p", CIStore: filepath.Join(t.TempDir(), "missing")}, {ID: "q"}}
	_, err := NormalizeProjects(d.Projects)
	must(t, err)
	h := Handler(d, "tok", fstest.MapFS{"index.html": {Data: []byte("x")}})
	for _, id := range []string{"p", "q"} {
		for _, path := range []string{"/api/runs", "/api/overview", "/api/run?id=20260101-000000-x", "/api/run/log?id=20260101-000000-x&job=a&step=1"} {
			r := httptest.NewRequest("GET", "http://127.0.0.1:80"+path+sep(path)+"project="+id, nil)
			r.AddCookie(&http.Cookie{Name: cookieName, Value: "tok"})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 200
			if strings.HasPrefix(path, "/api/run") && path != "/api/runs" {
				want = 404
			}
			if w.Code != want {
				t.Errorf("%s %s: %d %s", id, path, w.Code, w.Body.String())
			}
		}
	}
	// it must not have created the store folder
	if _, err := os.Stat(d.Projects[0].CIStore); err == nil {
		t.Fatal("reading must not create the store")
	}
}

func sep(p string) string {
	if strings.Contains(p, "?") {
		return "&"
	}
	return "?"
}

func TestCIReadingNeverWritesTheStore(t *testing.T) {
	f := newCIFixture(t)
	snap := func() map[string]string {
		m := map[string]string{}
		_ = filepath.WalkDir(f.dir, func(p string, e os.DirEntry, err error) error {
			if err == nil {
				fi, _ := e.Info()
				if e.IsDir() {
					m[p] = "dir" // a folder's own time is updated lazily by Windows even when nothing was written
					return nil
				}
				m[p] = fi.ModTime().String() + "|" + strconv.FormatInt(fi.Size(), 10)
			}
			return nil
		})
		return m
	}
	before := snap()
	for _, p := range []string{"/api/projects", "/api/runs", "/api/overview", "/api/run?id=" + f.passID, "/api/run?id=" + f.liveID, "/api/run/log?id=" + f.passID + "&job=build&step=2"} {
		f.get(t, p)
	}
	after := snap()
	if len(before) != len(after) {
		t.Fatalf("the store changed: %d entries before, %d after", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed: %q -> %q", k, v, after[k])
		}
	}
}
