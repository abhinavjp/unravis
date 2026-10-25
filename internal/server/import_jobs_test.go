package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
)

func withJobHooks(f func(*importJobs)) Option {
	return func(h *handler) { f(&h.jobs) }
}

func withImportLimit(n int) Option {
	return func(h *handler) { h.imports.max = n }
}

func (e *caseEnv) handler() *handler { return e.h.(*handler) }

func asyncPath(id string) string { return commitPath(id) + "&async=1" }

// postAsync sends an async import and returns the 202 answer.
func (e *caseEnv) postAsync(path string, file *upload) importJobDTO {
	e.t.Helper()
	res := e.upload(path, ackFields, file)
	if res.Code != http.StatusAccepted {
		e.t.Fatalf("async import = %d %s", res.Code, res.Body)
	}
	var job importJobDTO
	decodeBody(e.t, res, &job)
	if job.ID == "" || (job.State != jobQueued && job.State != jobRunning) {
		e.t.Fatalf("async answer = %s", res.Body)
	}
	return job
}

func jobPath(caseID, job string) string {
	return "/api/cases/" + caseID + "/imports/jobs/" + job
}

func (e *caseEnv) job(caseID, job string) importJobDTO {
	e.t.Helper()
	res := e.get(jobPath(caseID, job))
	if res.Code != http.StatusOK {
		e.t.Fatalf("get job = %d %s", res.Code, res.Body)
	}
	var out importJobDTO
	decodeBody(e.t, res, &out)
	return out
}

// settle polls until the job is done or failed.
func (e *caseEnv) settle(caseID, job string) importJobDTO {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out := e.job(caseID, job)
		if out.State == jobDone || out.State == jobFailed {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatal("job did not finish")
	return importJobDTO{}
}

// holdingImporter parks every parse until release is closed or the job's
// context ends, and tells entered when the first parse has started.
func holdingImporter() (imp toyImporter, entered chan struct{}, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	imp = toyImporter{name: "toy", score: 90, hold: func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	return imp, entered, release
}

func (e *caseEnv) slotsHeld() int {
	l := &e.handler().imports
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

// waitSlotsHeld waits for the limiter to show n slots: a job releases its slot
// just after it records its answer.
func (e *caseEnv) waitSlotsHeld(n int) {
	e.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for e.slotsHeld() != n {
		if time.Now().After(deadline) {
			e.t.Fatalf("limiter slots = %d, want %d", e.slotsHeld(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func oneRow(note string) *upload {
	return &upload{"a.csv", toyCSV(toyRow(1, "web-a", note, 5))}
}

func TestAsyncImportCommitsInTheBackground(t *testing.T) {
	spool := t.TempDir()
	e := newCaseEnv(t, envConfig{options: []Option{withJobHooks(func(j *importJobs) { j.spoolDir = spool })}})
	id := e.newCase("Async case")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5), toyRow(2, "web-a", "beta", 500))}
	started := e.postAsync(asyncPath(id), file)
	done := e.settle(id, started.ID)
	if done.State != jobDone || done.Status != http.StatusCreated || done.Failure != nil {
		t.Fatalf("job = %+v", done)
	}
	if done.BytesTotal == 0 || done.BytesRead != done.BytesTotal {
		t.Fatalf("progress = %d of %d", done.BytesRead, done.BytesTotal)
	}
	var out struct {
		Importer string           `json:"importer"`
		Report   *reportDTO       `json:"report"`
		Record   *importRecordDTO `json:"record"`
	}
	if err := json.Unmarshal(done.Result, &out); err != nil {
		t.Fatal(err)
	}
	if out.Importer != "toy" || out.Report == nil || out.Report.Emitted != 2 || out.Record == nil || out.Record.FileName != "a.csv" {
		t.Fatalf("result = %s", done.Result)
	}
	if n := len(e.importsOf(id)); n != 1 {
		t.Fatalf("imports = %d", n)
	}
	if entries, _ := os.ReadDir(spool); len(entries) != 0 {
		t.Fatalf("spool left %d file(s)", len(entries))
	}
	// The same file imported synchronously into a fresh case gives the same answer.
	other := e.newCase("Sync case")
	res := e.upload(commitPath(other), ackFields, file)
	var sync struct {
		Report *reportDTO       `json:"report"`
		Record *importRecordDTO `json:"record"`
	}
	decodeBody(t, res, &sync)
	if res.Code != done.Status || sync.Record.SHA256 != out.Record.SHA256 || sync.Report.Emitted != out.Report.Emitted || sync.Record.Rows != out.Record.Rows {
		t.Fatalf("sync %d %s differs from async %s", res.Code, res.Body, done.Result)
	}
}

func TestAsyncPreviewStoresNothing(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Async preview")
	res := e.upload(asyncPath(id)+"&preview=1", nil, oneRow("alpha"))
	if res.Code != http.StatusAccepted {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	var started importJobDTO
	decodeBody(t, res, &started)
	done := e.settle(id, started.ID)
	if done.State != jobDone || done.Status != http.StatusOK || !strings.Contains(string(done.Result), `"report"`) || strings.Contains(string(done.Result), `"record":{`) {
		t.Fatalf("job = %+v %s", done, done.Result)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("preview stored %d imports", n)
	}
	// A preview that finds the same bytes already imported fails like the sync call.
	if r := e.upload(commitPath(id), ackFields, oneRow("alpha")); r.Code != http.StatusCreated {
		t.Fatalf("commit = %d", r.Code)
	}
	res = e.upload(asyncPath(id)+"&preview=1", nil, oneRow("alpha"))
	decodeBody(t, res, &started)
	dup := e.settle(id, started.ID)
	if dup.State != jobFailed || dup.Failure == nil || dup.Failure.Reason != "duplicate" || dup.Status != http.StatusConflict || dup.Failure.ImportID == 0 {
		t.Fatalf("duplicate preview job = %+v", dup)
	}
}

// Every failure the sync call reports, the job reports with the same status and
// body fields.
func TestAsyncFailureMatchesTheSynchronousAnswer(t *testing.T) {
	rows := make([]string, 60) // the toy importer allows 50 rows
	for i := range rows {
		rows[i] = toyRow(i%60, "web-a", "n", 5)
	}
	cases := []struct {
		name string
		path string
		file *upload
	}{
		{"parser failure", "", &upload{"a.csv", toyCSV(toyRow(1, "web-a", "BOOM", 5))}},
		{"unknown host", "&host=", &upload{"a.csv", toyCSV(toyRow(1, "nowhere", "x", 5))}},
		{"row limit", "", &upload{"a.csv", toyCSV(rows...)}},
		{"wrong format", "", &upload{"a.csv", "hello\nworld\n"}},
		{"too many bad rows", "", &upload{"a.csv", toyCSV("not,a,row", "bad,row")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newCaseEnv(t, envConfig{})
			path := commitPath
			if c.path == "&host=" { // no host hint: the file's host names are looked up
				path = func(id string) string { return "/api/cases/" + id + "/imports?importer=toy" }
			}
			syncID, asyncID := e.newCase("sync"), e.newCase("async")
			want := e.upload(path(syncID), ackFields, c.file)
			if want.Code < 400 {
				t.Fatalf("sync call succeeded: %d", want.Code)
			}
			wantBody := failureOf(t, want)
			started := e.postAsync(path(asyncID)+"&async=1", c.file)
			got := e.settle(asyncID, started.ID)
			if got.State != jobFailed || got.Failure == nil || got.Status != want.Code ||
				got.Failure.Reason != wantBody.Reason || got.Failure.Error != wantBody.Error || got.Failure.Column != wantBody.Column {
				t.Fatalf("job = %+v, sync = %d %+v", got, want.Code, wantBody)
			}
			if raw := mustJSON(got); strings.Contains(raw, secretCell) {
				t.Fatalf("job body carries file content: %s", raw)
			}
			if n := len(e.importsOf(asyncID)); n != 0 {
				t.Fatalf("a failed job stored %d imports", n)
			}
		})
	}
}

func TestAsyncDuplicateKeepsTheExistingImportID(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("dup")
	first := e.settle(id, e.postAsync(asyncPath(id), oneRow("alpha")).ID)
	if first.State != jobDone {
		t.Fatalf("first = %+v", first)
	}
	second := e.settle(id, e.postAsync(asyncPath(id), oneRow("alpha")).ID)
	if second.State != jobFailed || second.Failure.Reason != "duplicate" || second.Status != http.StatusConflict || second.Failure.ImportID == 0 {
		t.Fatalf("second = %+v", second)
	}
}

// Everything checked before the parse is still refused on the request itself,
// with the status and reason of the synchronous call, and starts no job.
func TestAsyncRefusalsStaySynchronous(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("refusals")
	closed := e.newCase("closed")
	if res := e.json(http.MethodPost, "/api/cases/"+closed+"/close", ""); res.Code != http.StatusOK {
		t.Fatalf("close = %d", res.Code)
	}
	base := "/api/cases/" + id + "/imports?async=1"
	good := oneRow("alpha")
	for _, c := range []struct {
		name   string
		path   string
		fields []formField
		status int
		reason string
	}{
		{"no importer", base + "&host=web-a", ackFields, http.StatusBadRequest, "importer-required"},
		{"unknown importer", base + "&importer=nope", ackFields, http.StatusBadRequest, "unknown-importer"},
		{"unknown host", base + "&importer=toy&host=nowhere", ackFields, http.StatusUnprocessableEntity, "unknown-host"},
		{"bad supplied", base + "&importer=toy&s.x=1", ackFields, http.StatusBadRequest, "bad-supplied"},
		{"no acknowledgement", base + "&importer=toy&host=web-a", nil, http.StatusBadRequest, "ack-required"},
		{"wrong acknowledgement", base + "&importer=toy&host=web-a", []formField{{"ackGating", "true"}, {"ackText", "something else"}}, http.StatusBadRequest, "ack-mismatch"},
		{"closed case", "/api/cases/" + closed + "/imports?async=1&importer=toy&host=web-a", ackFields, http.StatusConflict, "case-closed"},
		{"missing case", "/api/cases/aaaaaaaaaaaaaaaaaaaaaaaaaa/imports?async=1&importer=toy&host=web-a", ackFields, http.StatusNotFound, "case-not-found"},
	} {
		res := e.upload(c.path, c.fields, good)
		if res.Code != c.status || failureOf(t, res).Reason != c.reason {
			t.Errorf("%s: %d %s, want %d %s", c.name, res.Code, res.Body, c.status, c.reason)
		}
	}
	// Not multipart.
	req := httptest.NewRequest(http.MethodPost, base+"&importer=toy&host=web-a", strings.NewReader("x"))
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "text/plain")
	if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("not multipart = %d", res.Code)
	}
	// Over the cap.
	small := newCaseEnv(t, envConfig{options: []Option{withMaxUpload(2048)}})
	sid := small.newCase("small")
	big := &upload{"a.csv", toyCSV(strings.Repeat(toyRow(1, "web-a", strings.Repeat("x", 200), 5)+"\n", 30))}
	if res := small.upload(asyncPath(sid), ackFields, big); res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "request-too-large" {
		t.Errorf("over cap = %d %s", res.Code, res.Body)
	}
	for _, env := range []*caseEnv{e, small} {
		if n := len(env.handler().jobs.m); n != 0 {
			t.Errorf("refusals left %d job(s)", n)
		}
		if n := env.slotsHeld(); n != 0 {
			t.Errorf("refusals left %d limiter slot(s) held", n)
		}
	}
	// The gate and slot are free: a good import still runs.
	if st := e.settle(id, e.postAsync(asyncPath(id), good).ID); st.State != jobDone {
		t.Fatalf("job after refusals = %+v", st)
	}
}

func TestAsyncJobHoldsTheGateAndTheSlotUntilItEnds(t *testing.T) {
	imp, entered, release := holdingImporter()
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{imp}, options: []Option{withImportLimit(1)}})
	id, other := e.newCase("one"), e.newCase("two")
	started := e.postAsync(asyncPath(id), oneRow("alpha"))
	<-entered
	if got := e.job(id, started.ID); got.State != jobRunning {
		t.Fatalf("state = %s", got.State)
	}

	// Another case cannot take a slot while the job runs.
	res := e.upload(asyncPath(other), ackFields, oneRow("beta"))
	if res.Code != http.StatusTooManyRequests || failureOf(t, res).Reason != "import-busy" {
		t.Fatalf("second case = %d %s", res.Code, res.Body)
	}
	// The same case waits at the gate: a sync import does not finish until the job does.
	syncDone := make(chan int, 1)
	go func() { syncDone <- e.upload(commitPath(id), ackFields, oneRow("gamma")).Code }()
	select {
	case code := <-syncDone:
		t.Fatalf("a second import finished (%d) while the job held the gate", code)
	case <-time.After(80 * time.Millisecond):
	}
	if n := len(e.handler().jobs.m); n != 1 {
		t.Fatalf("jobs = %d", n)
	}
	close(release)
	if st := e.settle(id, started.ID); st.State != jobDone {
		t.Fatalf("job = %+v", st)
	}
	// The gate passes to the waiting import; it may find the slot busy for a moment.
	if code := <-syncDone; code != http.StatusCreated && code != http.StatusTooManyRequests {
		t.Fatalf("sync import = %d", code)
	}
	if res := e.upload(asyncPath(other), ackFields, oneRow("beta")); res.Code != http.StatusAccepted {
		t.Fatalf("slot not released: %d %s", res.Code, res.Body)
	}
}

func TestAsyncSpoolIsEncryptedAndRemoved(t *testing.T) {
	spool := t.TempDir()
	imp, entered, release := holdingImporter()
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{imp}, options: []Option{withJobHooks(func(j *importJobs) { j.spoolDir = spool })}})
	id := e.newCase("spool")
	const marker = "plaintext-marker-77"
	started := e.postAsync(asyncPath(id), oneRow(marker))
	<-entered
	entries, err := os.ReadDir(spool)
	if err != nil || len(entries) != 1 {
		t.Fatalf("spool = %v %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(spool, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), marker) || strings.Contains(string(raw), toyHeader) {
		t.Fatal("spool holds plain text")
	}
	close(release)
	if st := e.settle(id, started.ID); st.State != jobDone {
		t.Fatalf("job = %+v", st)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if entries, _ = os.ReadDir(spool); len(entries) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("spool left %d file(s)", len(entries))
}

func TestEraseCancelsARunningJob(t *testing.T) {
	imp, entered, _ := holdingImporter() // never released: only a cancel ends it
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{imp}})
	id := e.newCase("erase me")
	started := e.postAsync(asyncPath(id), oneRow("alpha"))
	<-entered

	eraseDone := make(chan int, 1)
	go func() {
		eraseDone <- e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`).Code
	}()
	select {
	case code := <-eraseDone:
		if code != http.StatusNoContent {
			t.Fatalf("erase = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("erase waited for a job that nothing releases")
	}
	got := e.job(id, started.ID)
	if got.State != jobFailed || got.Failure == nil || got.Failure.Reason != "canceled" {
		t.Fatalf("job = %+v", got)
	}
	if res := e.get("/api/cases/" + id + "/imports"); res.Code != http.StatusNotFound {
		t.Fatalf("erased case = %d", res.Code)
	}
	e.waitSlotsHeld(0)
}

func TestDeleteJobCancelsThenForgets(t *testing.T) {
	imp, entered, _ := holdingImporter()
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{imp}})
	id := e.newCase("cancel me")
	started := e.postAsync(asyncPath(id), oneRow("alpha"))
	<-entered
	if res := e.json(http.MethodDelete, jobPath(id, started.ID), ""); res.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", res.Code, res.Body)
	}
	got := e.settle(id, started.ID)
	if got.State != jobFailed || got.Failure.Reason != "canceled" || got.Status != http.StatusBadRequest {
		t.Fatalf("job = %+v", got)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a cancelled job stored %d imports", n)
	}
	// Finished: a second delete forgets it.
	if res := e.json(http.MethodDelete, jobPath(id, started.ID), ""); res.Code != http.StatusNoContent {
		t.Fatalf("forget = %d", res.Code)
	}
	if res := e.get(jobPath(id, started.ID)); res.Code != http.StatusNotFound || failureOf(t, res).Reason != "job-not-found" {
		t.Fatalf("after forget = %d %s", res.Code, res.Body)
	}
	e.waitSlotsHeld(0)
}

func TestShutdownCancelsRunningJobs(t *testing.T) {
	imp, entered, _ := holdingImporter()
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{imp}})
	id := e.newCase("shutdown")
	started := e.postAsync(asyncPath(id), oneRow("alpha"))
	<-entered
	CancelImports(e.h)
	got := e.job(id, started.ID)
	if got.State != jobFailed || got.Failure.Reason != "canceled" {
		t.Fatalf("job = %+v", got)
	}
	// After shutdown no new job starts.
	if res := e.upload(asyncPath(id), ackFields, oneRow("beta")); res.Code != http.StatusTooManyRequests {
		t.Fatalf("after shutdown = %d %s", res.Code, res.Body)
	}
}

func TestFinishedJobsExpireAfterAnHour(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	e := newCaseEnv(t, envConfig{options: []Option{withJobHooks(func(j *importJobs) {
		j.clock = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})}})
	id := e.newCase("expiry")
	started := e.postAsync(asyncPath(id), oneRow("alpha"))
	e.settle(id, started.ID)
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	advance(59 * time.Minute)
	if res := e.get(jobPath(id, started.ID)); res.Code != http.StatusOK {
		t.Fatalf("at 59 min = %d", res.Code)
	}
	advance(2 * time.Minute)
	if res := e.get(jobPath(id, started.ID)); res.Code != http.StatusNotFound {
		t.Fatalf("at 61 min = %d", res.Code)
	}
	if n := len(e.handler().jobs.m); n != 0 {
		t.Fatalf("expired job kept: %d", n)
	}
}

func TestJobRegistryIsCapped(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	e := newCaseEnv(t, envConfig{options: []Option{withJobHooks(func(j *importJobs) {
		j.max = 3
		j.clock = func() time.Time { mu.Lock(); defer mu.Unlock(); now = now.Add(time.Second); return now }
	})}})
	id := e.newCase("cap")
	var ids []string
	for _, note := range []string{"a", "b", "c", "d", "e"} {
		started := e.postAsync(asyncPath(id), oneRow(note))
		e.settle(id, started.ID)
		ids = append(ids, started.ID)
	}
	if n := len(e.handler().jobs.m); n != 3 {
		t.Fatalf("kept %d jobs, want 3", n)
	}
	for i, job := range ids {
		want := http.StatusOK
		if i < 2 {
			want = http.StatusNotFound
		}
		if res := e.get(jobPath(id, job)); res.Code != want {
			t.Errorf("job %d = %d, want %d", i, res.Code, want)
		}
	}
}

func TestJobRegistryRefusesWhenEveryJobIsRunning(t *testing.T) {
	imp, entered, release := holdingImporter()
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{imp}, options: []Option{
		withJobHooks(func(j *importJobs) { j.max = 1 }), withImportLimit(5),
	}})
	a, b := e.newCase("a"), e.newCase("b")
	started := e.postAsync(asyncPath(a), oneRow("alpha"))
	<-entered
	res := e.upload(asyncPath(b), ackFields, oneRow("beta"))
	if res.Code != http.StatusTooManyRequests || failureOf(t, res).Reason != "import-busy" {
		t.Fatalf("full registry = %d %s", res.Code, res.Body)
	}
	if n := e.slotsHeld(); n != 1 {
		t.Fatalf("limiter slots = %d, want 1", n)
	}
	close(release)
	e.settle(a, started.ID)
}

func TestJobsAreScopedToTheirCase(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	a, b := e.newCase("a"), e.newCase("b")
	started := e.postAsync(asyncPath(a), oneRow("alpha"))
	e.settle(a, started.ID)
	if res := e.get(jobPath(b, started.ID)); res.Code != http.StatusNotFound {
		t.Fatalf("job through another case = %d", res.Code)
	}
	if res := e.json(http.MethodDelete, jobPath(b, started.ID), ""); res.Code != http.StatusNotFound {
		t.Fatalf("delete through another case = %d", res.Code)
	}
	if res := e.get(jobPath(a, started.ID)); res.Code != http.StatusOK {
		t.Fatalf("own case = %d", res.Code)
	}
	for _, bad := range []string{"nothex", strings.Repeat("0", 32), strings.Repeat("A", 32)} {
		if res := e.get(jobPath(a, bad)); res.Code != http.StatusNotFound {
			t.Errorf("job %q = %d", bad, res.Code)
		}
	}
	if res := e.get(jobPath("not-a-case", started.ID)); res.Code != http.StatusBadRequest {
		t.Fatalf("bad case id = %d", res.Code)
	}
}

func TestJobRoutesNeedTheToken(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, jobPath("aaaaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("0", 32)), nil)
		if res := e.serve(req); res.Code != http.StatusUnauthorized {
			t.Errorf("%s without token = %d", method, res.Code)
		}
	}
}

func TestSynchronousImportIsUnchangedWithoutAsync(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("plain")
	res := e.upload(commitPath(id), ackFields, oneRow("alpha"))
	if res.Code != http.StatusCreated {
		t.Fatalf("sync = %d %s", res.Code, res.Body)
	}
	if n := len(e.handler().jobs.m); n != 0 {
		t.Fatalf("sync import made %d job(s)", n)
	}
	// async=0 and other values do not start a job either.
	res = e.upload(commitPath(id)+"&async=0", ackFields, oneRow("beta"))
	if res.Code != http.StatusCreated {
		t.Fatalf("async=0 = %d %s", res.Code, res.Body)
	}
}

// The real encrypted store: the job opens its own case handle, commits, and a
// cancelled job leaves nothing behind.
func TestAsyncImportAgainstTheSQLiteStore(t *testing.T) {
	imp, entered, release := holdingImporter()
	store := fix2SQLite(t, nil)
	e := fix2Env(t, store, nil, []importer.Importer{imp})
	id := e.newCase("sqlite async")
	started := e.postAsync(asyncPath(id), oneRow("alpha"))
	<-entered
	close(release)
	done := e.settle(id, started.ID)
	if done.State != jobDone || done.Status != http.StatusCreated {
		t.Fatalf("job = %+v %v", done, done.Failure)
	}
	if n := len(e.importsOf(id)); n != 1 {
		t.Fatalf("imports = %d", n)
	}
	// A second file is cancelled mid-parse by DELETE on the job: nothing is stored.
	e2 := fix2Env(t, store, nil, []importer.Importer{toyImporter{name: "toy", score: 90, hold: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}})
	second := e2.postAsync(asyncPath(id), oneRow("beta"))
	waitFor(t, func() bool { return e2.job(id, second.ID).State == jobRunning })
	if res := e2.json(http.MethodDelete, jobPath(id, second.ID), ""); res.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d", res.Code)
	}
	if got := e2.settle(id, second.ID); got.State != jobFailed || got.Failure.Reason != "canceled" {
		t.Fatalf("cancelled job = %+v", got)
	}
	if n := len(e.importsOf(id)); n != 1 {
		t.Fatalf("imports after cancel = %d, want 1", n)
	}
	// Erase works once the jobs are over.
	if res := e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
