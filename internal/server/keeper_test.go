package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sqlconn"
)

// keeperRunner stands in for the keeper's drivers: it records the logins it was handed (inside the keeper) and never connects.
type keeperRunner struct {
	mu    sync.Mutex
	sql   []dbhealth.SQLTarget
	mongo []audit.ConnectionInput
}

func (f *keeperRunner) sawSQL(t dbhealth.SQLTarget) {
	f.mu.Lock()
	f.sql = append(f.sql, t)
	f.mu.Unlock()
}

func (f *keeperRunner) sawMongo(in audit.ConnectionInput) {
	f.mu.Lock()
	f.mongo = append(f.mongo, in)
	f.mu.Unlock()
}

func (f *keeperRunner) TestSQL(_ context.Context, t dbhealth.SQLTarget) (dbhealth.TestResult, error) {
	f.sawSQL(t)
	return dbhealth.TestResult{Version: "16.0", Edition: "Developer", Databases: []string{"Payroll"}}, nil
}

func (f *keeperRunner) HealthSQL(_ context.Context, t dbhealth.SQLTarget, label string) (dbhealth.Report, error) {
	f.sawSQL(t)
	return dbhealth.Report{Kind: "sql", Label: label, Database: t.Database, Overall: "ok", FinishedAt: time.Now()}, nil
}

func (f *keeperRunner) LaneSQL(_ context.Context, t dbhealth.SQLTarget, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	f.sawSQL(t)
	return dbhealth.Report{Kind: "sql", Label: label, Database: t.Database, Overall: "ok", FinishedAt: time.Now(), Lane: &dbhealth.LaneResult{Incident: req.Incident, Reference: req.Reference}}, nil
}

func (f *keeperRunner) TestMongo(_ context.Context, in audit.ConnectionInput) (audit.ConnectionResult, error) {
	f.sawMongo(in)
	return audit.ConnectionResult{Databases: []string{"audit"}}, nil
}

func (f *keeperRunner) ValidateMongo(audit.ConnectionInput) error { return nil }

func (f *keeperRunner) HealthMongo(_ context.Context, in audit.ConnectionInput, database, _ string) (dbhealth.Report, error) {
	f.sawMongo(in)
	return dbhealth.Report{Kind: "mongo", Label: "MongoDB", Database: database, Overall: "ok", FinishedAt: time.Now()}, nil
}

func (f *keeperRunner) LaneMongo(_ context.Context, in audit.ConnectionInput, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	f.sawMongo(in)
	return dbhealth.Report{Kind: "mongo", Label: label, Database: req.Database, Overall: "ok", FinishedAt: time.Now(), Lane: &dbhealth.LaneResult{Incident: req.Incident, Reference: req.Reference}}, nil
}

// keeperHosted stands in for the audit repository the keeper hosts (P14b): it records the passwords it was handed (inside the keeper)
// and answers from memory, never connecting.
type keeperHosted struct {
	mu        sync.Mutex
	passwords []string
	sources   []audit.Source
	next      int
}

func (f *keeperHosted) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.passwords...)
}

func (f *keeperHosted) Sources(context.Context) ([]audit.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Source(nil), f.sources...), nil
}

func (f *keeperHosted) AddSource(_ context.Context, in audit.SourceInput) (audit.Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.passwords = append(f.passwords, in.Password)
	f.next++
	id := fmt.Sprintf("kept-%d", f.next)
	src := audit.Source{ID: id, Label: in.Label, Database: in.Database, Collection: in.Collection, SelectionKey: id}
	f.sources = append(f.sources, src)
	return src, nil
}

func (f *keeperHosted) DeleteSource(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.sources {
		if s.ID == id {
			f.sources = append(f.sources[:i], f.sources[i+1:]...)
			return nil
		}
	}
	return audit.ErrNotFound
}

func (f *keeperHosted) Analyze(context.Context, audit.Query) (audit.Analysis, error) {
	return audit.Analysis{Sources: []audit.SourceResult{{SourceID: "answered-by-the-keeper", OK: true}}}, nil
}
func (f *keeperHosted) Requests(context.Context, audit.PageInput) (audit.Page, error) {
	return audit.Page{}, nil
}
func (f *keeperHosted) Payload(context.Context, audit.PayloadInput) (audit.Payload, error) {
	return audit.Payload{}, nil
}
func (f *keeperHosted) Record(context.Context, audit.RecordInput) (audit.RecordDetail, error) {
	return audit.RecordDetail{}, nil
}
func (f *keeperHosted) Close(context.Context) error { return nil }

type keeperEnv struct {
	*caseEnv
	runner *keeperRunner
	hosted *keeperHosted
	client *keeper.Client
	dir    string
	stop   func()
	legacy keeper.Legacy
	// audit is the keeper's own audit log, on disk at auditPath; protector seals the keeper's saved files.
	audit     *keeper.AuditLog
	auditPath string
	protector *storetest.Protector
}

// newKeeperEnv runs a keeper in this process (in-memory pipe, fake drivers) and an Unravis wired to it as in the shared layout.
func newKeeperEnv(t *testing.T, store casestore.Store, legacy keeper.Legacy) *keeperEnv {
	t.Helper()
	dir := t.TempDir()
	protector := storetest.RandomProtector()
	sql, err := sqlconn.New(filepath.Join(dir, "sqlconnections.dat"), protector)
	if err != nil {
		t.Fatal(err)
	}
	mongo, err := history.New(filepath.Join(dir, "connections.dat"), protector)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := bbsaved.New(filepath.Join(dir, "blackboxes.dat"), protector)
	if err != nil {
		t.Fatal(err)
	}
	runner := &keeperRunner{}
	hosted := &keeperHosted{}
	auditPath := filepath.Join(dir, "keeper-audit.log")
	auditLog := keeper.NewAuditLog(auditPath, 0)
	srv := keeper.NewServer(keeper.ServerOptions{Log: auditLog, Limiter: keeper.NewRateLimiter(map[string]int{"read": 10000, "write": 10000, "reach": 10000, "blob": 10000, "audit": 10000}, time.Minute)})
	host := keeper.NewAuditHost(func() keeper.HostedRepo { return hosted })
	(&keeper.Backend{SQL: sql, Mongo: mongo, BB: bb, Runner: runner, Audit: host}).Register(srv)
	l := keeper.NewMemoryListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, l)
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	c := keeper.NewClient(l.Dialer(keeper.Caller{SID: "S-1-5-21-1-2-3-1001", Name: `PC\analyst`, PID: 4242, Session: 1}))
	ms := keeper.NewMongoStore(c)
	var mig *keeper.Migration
	if legacy.SQL != nil || legacy.Mongo != nil || legacy.BB != nil {
		mig = &keeper.Migration{Legacy: legacy, Client: c}
	}
	// The audit repository is wired as main wires it in the shared layout: this process's own one behind the keeper router.
	e := tagEnv(t, store, keeper.NewAuditRouter(&fakeRepository{}, c), WithSQLConnections(keeper.NewSQLStore(c)), WithHistory(ms), WithKeeper(&KeeperHub{Client: c, Migration: mig}))
	return &keeperEnv{caseEnv: e, runner: runner, hosted: hosted, client: c, dir: dir, stop: stop, legacy: legacy, audit: auditLog, auditPath: auditPath, protector: protector}
}

func TestSavedPasswordsStayInTheKeeper(t *testing.T) {
	const sqlSecret, mongoSecret = "sql-NEVER-in-unravis-7731", "mongo-NEVER-in-unravis-5512"
	unravisDrivers := stubDrivers(t)
	// Every log line Unravis writes while this runs, and the case files on disk, are searched for the passwords at the end.
	var logged syncBuffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	casesRoot := filepath.Join(t.TempDir(), "cases")
	casesProtector := storetest.RandomProtector()
	cases, err := sqlite.New(sqlite.Options{Root: casesRoot, Protector: casesProtector, MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	e := newKeeperEnv(t, cases, keeper.Legacy{})
	id := e.newCase("keeper")
	var bodies []string
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		res := e.json(method, path, body)
		bodies = append(bodies, res.Body.String())
		if res.Code != want {
			t.Fatalf("%s %s = %d %s", method, path, res.Code, res.Body)
		}
		return res
	}

	// SQL Server: save with the password, then test, run the fixed checks and read a lane by name only.
	var saved sqlconn.Recent
	decodeBody(t, call(http.MethodPost, "/api/sql-connections", `{"server":"sql01.example.test","auth":"sql","username":"svc_audit","password":"`+sqlSecret+`","rememberPassword":true,"database":"Payroll","encrypt":"mandatory"}`, http.StatusOK), &saved)
	if !saved.HasPassword {
		t.Fatalf("saved = %+v", saved)
	}
	call(http.MethodGet, "/api/sql-connections", "", http.StatusOK)
	res := call(http.MethodPost, "/api/sql-connections/test", `{"savedId":"`+saved.ID+`"}`, http.StatusOK)
	if !strings.Contains(res.Body.String(), `"Payroll"`) {
		t.Fatalf("test = %s", res.Body)
	}
	call(http.MethodPost, "/api/cases/"+id+"/health/run", `{"kind":"sql","sql":{"savedId":"`+saved.ID+`"}}`, http.StatusOK)
	call(http.MethodPost, "/api/cases/"+id+"/lanes/sql", `{"savedId":"`+saved.ID+`",`+laneWindowsJSON+`}`, http.StatusOK)

	// MongoDB: remember with the password, then test and run the fixed checks by the saved id.
	var link history.Recent
	decodeBody(t, call(http.MethodPost, "/api/connections/remember", `{"label":"Prod","connection":{"uri":"mongodb://mongo01.example.test:27017","username":"auditor","password":"`+mongoSecret+`","authSource":"admin"},"collection":"auditLogs","rememberPassword":true}`, http.StatusOK), &link)
	if !link.HasCredentials {
		t.Fatalf("link = %+v", link)
	}
	ref, _ := json.Marshal(audit.ConnectionInput{URI: link.URI, Username: link.Username, AuthSource: link.AuthSource, TLSMode: link.TLSMode, CACertificate: link.CACertificate, SavedConnectionID: link.ID})
	call(http.MethodGet, "/api/connections/recent", "", http.StatusOK)
	res = call(http.MethodPost, "/api/connections/test", string(ref), http.StatusOK)
	if !strings.Contains(res.Body.String(), `"audit"`) {
		t.Fatalf("mongo test = %s", res.Body)
	}
	var mongoRun map[string]any
	_ = json.Unmarshal(ref, &mongoRun)
	mongoRun["database"] = "audit"
	runBody, _ := json.Marshal(map[string]any{"kind": "mongo", "mongo": mongoRun})
	call(http.MethodPost, "/api/cases/"+id+"/health/run", string(runBody), http.StatusOK)

	// An audit source from a saved link: the keeper opens it with the password it stores and answers every read of it (P14b).
	var source heldSourceDTO
	srcBody, _ := json.Marshal(map[string]any{"uri": link.URI, "username": link.Username, "authSource": link.AuthSource, "tlsMode": link.TLSMode, "savedConnectionId": link.ID, "database": "audit", "collection": "auditLogs", "label": "Prod audit"})
	decodeBody(t, call(http.MethodPost, "/api/sources", string(srcBody), http.StatusCreated), &source)
	if source.ID == "" || source.Keeper != keeper.HeldByKeeper {
		t.Fatalf("add source = %+v", source)
	}
	if res = call(http.MethodGet, "/api/sources", "", http.StatusOK); !strings.Contains(res.Body.String(), `"keeper":"held"`) {
		t.Fatalf("sources = %s", res.Body)
	}
	q := testQuery()
	q.SourceIDs = []string{source.ID}
	qBody, _ := json.Marshal(q)
	if res = call(http.MethodPost, "/api/analyze", string(qBody), http.StatusOK); !strings.Contains(res.Body.String(), "answered-by-the-keeper") {
		t.Fatalf("analyze in the keeper = %s", res.Body)
	}
	if got := e.hosted.seen(); len(got) != 1 || got[0] != mongoSecret {
		t.Fatalf("the keeper's audit repository was handed %d passwords", len(got))
	}

	// The work ran in the keeper with the stored passwords.
	e.runner.mu.Lock()
	sqlRuns, mongoRuns := append([]dbhealth.SQLTarget(nil), e.runner.sql...), append([]audit.ConnectionInput(nil), e.runner.mongo...)
	e.runner.mu.Unlock()
	if len(sqlRuns) != 3 || len(mongoRuns) != 2 {
		t.Fatalf("keeper runs: sql %d, mongo %d", len(sqlRuns), len(mongoRuns))
	}
	for _, r := range sqlRuns {
		if r.Password != sqlSecret || r.Database != "Payroll" {
			t.Fatalf("keeper SQL target = %+v", r)
		}
	}
	for _, r := range mongoRuns {
		if r.Password != mongoSecret {
			t.Fatalf("keeper Mongo input = %+v", r)
		}
	}
	// Unravis's own drivers never ran for a saved login, and no answer carried a password.
	if len(*unravisDrivers) != 0 {
		t.Fatalf("Unravis connected itself: %+v", *unravisDrivers)
	}
	for _, b := range bodies {
		if strings.Contains(b, sqlSecret) || strings.Contains(b, mongoSecret) {
			t.Fatalf("a password reached an answer: %s", b)
		}
	}
	// The case files hold no password either.
	res = call(http.MethodGet, "/api/cases/"+id+"/health", "", http.StatusOK)
	if strings.Contains(res.Body.String(), sqlSecret) || strings.Contains(res.Body.String(), mongoSecret) {
		t.Fatalf("case health list: %s", res.Body)
	}

	// Logs and files: Unravis's log output, the keeper's audit log (read plainly), every file of the case store and every file in the
	// keeper's folder, byte for byte and, where a file is one sealed blob, unsealed too. Only the keeper's three saved-connection files may
	// hold a password once unsealed: that is where it is kept. The case database itself is encrypted page by page, so this byte search
	// cannot see inside it: what it stores (health runs, notes, the workspace) is covered only through the API answers searched above.
	e.audit.Flush()
	secrets := []string{sqlSecret, mongoSecret}
	holds := func(b []byte) bool {
		for _, s := range secrets {
			if strings.Contains(string(b), s) {
				return true
			}
		}
		return false
	}
	if holds([]byte(logged.String())) {
		t.Fatalf("a password reached Unravis's log: %s", logged.String())
	}
	auditText, err := os.ReadFile(e.auditPath)
	if err != nil || len(auditText) == 0 {
		t.Fatalf("keeper audit log: %v (%d bytes)", err, len(auditText))
	}
	if holds(auditText) {
		t.Fatalf("a password reached the keeper's audit log: %s", auditText)
	}
	keeperStores := map[string]bool{"sqlconnections.dat": true, "connections.dat": true, "blackboxes.dat": true}
	searched, unsealed, kept := 0, 0, 0
	for _, root := range []struct {
		dir       string
		protector *storetest.Protector
		keeper    bool
	}{{casesRoot, casesProtector, false}, {e.dir, e.protector, true}} {
		err := filepath.WalkDir(root.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			searched++
			if holds(raw) {
				t.Errorf("a password sits in plain bytes in %s", path)
			}
			if plain, err := root.protector.Unprotect(raw); err == nil {
				unsealed++
				switch {
				case holds(plain) && root.keeper && keeperStores[filepath.Base(path)]:
					kept++
				case holds(plain):
					t.Errorf("a password sits sealed in %s", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// The search must have seen the passwords where they are kept, or it proves nothing.
	if searched < 4 || unsealed < 2 || kept != 2 {
		t.Fatalf("searched %d files, unsealed %d, found the kept passwords in %d: the search did not see the stores", searched, unsealed, kept)
	}
}

// syncBuffer is a log destination the server may write from several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestATypedPasswordStillWorksWithTheKeeper(t *testing.T) {
	unravisDrivers := stubDrivers(t)
	e := newKeeperEnv(t, casestore.NewMemory(), keeper.Legacy{})
	var saved sqlconn.Recent
	res := e.json(http.MethodPost, "/api/sql-connections", `{"server":"db01","auth":"sql","username":"u","password":"stored","rememberPassword":true,"encrypt":"mandatory"}`)
	decodeBody(t, res, &saved)
	if res = e.json(http.MethodPost, "/api/sql-connections/test", `{"savedId":"`+saved.ID+`","password":"typed-now"}`); res.Code != http.StatusOK {
		t.Fatalf("typed = %d %s", res.Code, res.Body)
	}
	if got := *unravisDrivers; len(got) != 1 || got[0].Password != "typed-now" {
		t.Fatalf("a typed password runs in Unravis (B19 fallback): %+v", got)
	}
	// A Windows sign-in has no secret: it runs in Unravis as before.
	res = e.json(http.MethodPost, "/api/sql-connections", `{"server":"db02","auth":"windows","encrypt":"mandatory"}`)
	decodeBody(t, res, &saved)
	if res = e.json(http.MethodPost, "/api/sql-connections/test", `{"savedId":"`+saved.ID+`"}`); res.Code != http.StatusOK || len(*unravisDrivers) != 2 {
		t.Fatalf("windows = %d %s", res.Code, res.Body)
	}
}

func TestKeeperStoppedIsSaidPlainly(t *testing.T) {
	stubDrivers(t)
	e := newKeeperEnv(t, casestore.NewMemory(), keeper.Legacy{})
	var saved sqlconn.Recent
	decodeBody(t, e.json(http.MethodPost, "/api/sql-connections", `{"server":"db01","auth":"sql","username":"u","password":"pw","rememberPassword":true,"encrypt":"mandatory"}`), &saved)
	var st keeperStatusDTO
	decodeBody(t, e.get("/api/keeper/status"), &st)
	if st.Mode != "shared" || !st.Running || st.Problem != "" {
		t.Fatalf("status = %+v", st)
	}
	e.stop()
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/sql-connections/test", `{"savedId":"` + saved.ID + `"}`},
		{http.MethodGet, "/api/sql-connections", ""},
		{http.MethodPost, "/api/connections/test", `{"uri":"mongodb://h","savedConnectionId":"x"}`},
	} {
		res := e.json(c.method, c.path, c.body)
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d %s", c.method, c.path, res.Code, res.Body)
		}
	}
	res := e.json(http.MethodPost, "/api/sql-connections/test", `{"savedId":"`+saved.ID+`"}`)
	if !strings.Contains(res.Body.String(), `"keeper-stopped"`) {
		t.Fatalf("stopped = %s", res.Body)
	}
	decodeBody(t, e.get("/api/keeper/status"), &st)
	if st.Running || st.Problem != "stopped" {
		t.Fatalf("status when stopped = %+v", st)
	}
}

func TestMoveOldSavedFilesIntoTheKeeper(t *testing.T) {
	old := t.TempDir()
	lsql, err := sqlconn.New(filepath.Join(old, "sqlconnections.dat"), storetest.RandomProtector())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lsql.Save(sqlconn.Input{Server: "old01", Auth: "sql", Username: "u", Password: "old-pw", RememberPassword: true, Encrypt: "mandatory"}); err != nil {
		t.Fatal(err)
	}
	lmongo, err := history.New(filepath.Join(old, "connections.dat"), storetest.RandomProtector())
	if err != nil {
		t.Fatal(err)
	}
	link, err := lmongo.Remember("Old", audit.ConnectionInput{URI: "mongodb://old.example.test:27017", Username: "a", Password: "old-mongo-pw", AuthSource: "admin"}, "auditLogs", true)
	if err != nil {
		t.Fatal(err)
	}
	e := newKeeperEnv(t, casestore.NewMemory(), keeper.Legacy{SQL: lsql, Mongo: lmongo})

	var st keeperStatusDTO
	decodeBody(t, e.get("/api/keeper/status"), &st)
	if !st.OldFilesPresent || st.Pending.SQL != 1 || st.Pending.Mongo != 1 {
		t.Fatalf("before = %+v", st)
	}
	if res := e.json(http.MethodPost, "/api/keeper/remove-old", `{"confirm":true}`); res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "not-verified") {
		t.Fatalf("remove before the move = %d %s", res.Code, res.Body)
	}
	res := e.json(http.MethodPost, "/api/keeper/migrate", `{}`)
	decodeBody(t, res, &st)
	if res.Code != http.StatusOK || st.Pending.Total() != 0 || !st.OldFilesPresent {
		t.Fatalf("move = %d %s", res.Code, res.Body)
	}
	var moved []history.Recent
	decodeBody(t, e.get("/api/connections/recent"), &moved)
	if len(moved) != 1 || !moved[0].HasCredentials || moved[0].ID != link.ID {
		t.Fatalf("moved links = %+v", moved)
	}
	ref, _ := json.Marshal(audit.ConnectionInput{URI: moved[0].URI, Username: moved[0].Username, AuthSource: moved[0].AuthSource, TLSMode: moved[0].TLSMode, SavedConnectionID: moved[0].ID})
	if res := e.json(http.MethodPost, "/api/sources", string(ref)); res.Code != http.StatusCreated {
		t.Fatalf("source from the old file = %d %s", res.Code, res.Body)
	}
	if res := e.json(http.MethodPost, "/api/keeper/remove-old", `{}`); res.Code != http.StatusBadRequest {
		t.Fatalf("remove without confirm = %d", res.Code)
	}
	if res := e.json(http.MethodPost, "/api/keeper/remove-old", `{"confirm":true}`); res.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", res.Code, res.Body)
	}
	for _, f := range []string{"sqlconnections.dat", "connections.dat"} {
		if _, err := os.Stat(filepath.Join(old, f)); !os.IsNotExist(err) {
			t.Fatalf("%s still there: %v", f, err)
		}
	}
	// With the old file gone the moved link still opens audit sources: the keeper holds its password (P14b).
	if res := e.json(http.MethodPost, "/api/sources", string(ref)); res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"keeper":"held"`) {
		t.Fatalf("source after removal = %d %s", res.Code, res.Body)
	}
	if got := e.hosted.seen(); len(got) != 2 || got[0] != "old-mongo-pw" || got[1] != "old-mongo-pw" {
		t.Fatalf("the keeper opened the moved link's sources with %d passwords", len(got))
	}
}

func TestKeeperStatusOutsideTheSharedLayout(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	var st keeperStatusDTO
	decodeBody(t, e.get("/api/keeper/status"), &st)
	if st.Mode != "user" {
		t.Fatalf("status = %+v", st)
	}
	if res := e.json(http.MethodPost, "/api/keeper/migrate", `{}`); res.Code != http.StatusNotFound {
		t.Fatalf("migrate = %d", res.Code)
	}
	// The screenshot previews answer only in the demo.
	decodeBody(t, e.get("/api/keeper/status?preview=stopped"), &st)
	if st.Mode != "user" {
		t.Fatalf("preview outside the demo = %+v", st)
	}
	demo := &caseEnv{t: t, h: New(&fakeRepository{}, testAssets(), testToken, true)}
	decodeBody(t, demo.get("/api/keeper/status?preview=stopped"), &st)
	if st.Mode != "shared" || st.Problem != "stopped" {
		t.Fatalf("demo preview = %+v", st)
	}
}

// The person's cases from before the shared layout stay in their own folder (P14 ruling): the case list only says how many there are.
func TestOldPerUserCasesAreCountedNotOpened(t *testing.T) {
	type page struct {
		Cases    []caseDTO `json:"cases"`
		OldCases int       `json:"oldCases"`
	}
	n := 3
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{}, WithKeeper(&KeeperHub{OldCases: func() int { return n }}))
	var got page
	decodeBody(t, e.get("/api/cases"), &got)
	if got.OldCases != 3 || len(got.Cases) != 0 {
		t.Fatalf("page = %+v", got)
	}
	n = 0
	res := e.get("/api/cases")
	if strings.Contains(res.Body.String(), "oldCases") {
		t.Fatalf("no old cases still named: %s", res.Body.String())
	}
	plain := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	if res := plain.get("/api/cases"); strings.Contains(res.Body.String(), "oldCases") {
		t.Fatalf("per-user layout names old cases: %s", res.Body.String())
	}
}

func TestKeeperErrorsAreWordedForPeople(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		want string
	}{
		{keeper.ErrNotRunning, http.StatusServiceUnavailable, "keeper-stopped"},
		{keeper.ErrImpostor, http.StatusServiceUnavailable, "keeper-impostor"},
		{keeper.ErrKeeperHeld, http.StatusBadRequest, "keeper-held"},
		{keeper.ErrHeldSourceGone, http.StatusNotFound, "keeper-source-gone"},
		{keeper.ErrMixedSources, http.StatusBadRequest, "keeper-mixed"},
		{&keeper.WireError{Code: keeper.CodeBusy}, http.StatusConflict, "keeper-busy"},
		{&keeper.WireError{Code: keeper.CodeRateLimited}, http.StatusTooManyRequests, "keeper-rate"},
		{&keeper.WireError{Code: keeper.CodeRefused}, http.StatusForbidden, "keeper-refused"},
		{&keeper.WireError{Code: keeper.CodeInternal, Message: "x"}, http.StatusServiceUnavailable, "keeper-error"},
	} {
		w := httptest.NewRecorder()
		if !writeKeeperError(w, c.err) || w.Code != c.code || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%v = %d %s", c.err, w.Code, w.Body)
		}
	}
	if writeKeeperError(httptest.NewRecorder(), &keeper.WireError{Code: keeper.CodeConnect}) {
		t.Error("a connect failure is the handler's to word")
	}
}

// An audit source the keeper opened, when the keeper stops (P14b): it stays listed, marked stopped, its reads say the keeper is not
// running, and removing it only forgets it. A source whose password was typed keeps working in this Unravis.
func TestKeeperHeldAuditSourceWhenTheKeeperStops(t *testing.T) {
	stubDrivers(t)
	e := newKeeperEnv(t, casestore.NewMemory(), keeper.Legacy{})
	var link history.Recent
	decodeBody(t, e.json(http.MethodPost, "/api/connections/remember", `{"label":"Prod","connection":{"uri":"mongodb://mongo01.example.test:27017","username":"auditor","password":"held-pw","authSource":"admin"},"collection":"auditLogs","rememberPassword":true}`), &link)
	add, _ := json.Marshal(map[string]any{"uri": link.URI, "username": link.Username, "authSource": link.AuthSource, "tlsMode": link.TLSMode, "savedConnectionId": link.ID, "database": "audit", "collection": "auditLogs"})
	var source heldSourceDTO
	res := e.json(http.MethodPost, "/api/sources", string(add))
	decodeBody(t, res, &source)
	if res.Code != http.StatusCreated || source.Keeper != keeper.HeldByKeeper {
		t.Fatalf("add = %d %s", res.Code, res.Body)
	}
	// A typed password for the same link opens the source here, not in the keeper.
	typed, _ := json.Marshal(map[string]any{"uri": link.URI, "username": link.Username, "password": "typed-now", "authSource": link.AuthSource, "savedConnectionId": link.ID, "database": "audit", "collection": "other"})
	if res := e.json(http.MethodPost, "/api/sources", string(typed)); res.Code != http.StatusCreated || strings.Contains(res.Body.String(), `"keeper"`) {
		t.Fatalf("typed = %d %s", res.Code, res.Body)
	}
	if got := e.hosted.seen(); len(got) != 1 {
		t.Fatalf("the typed source reached the keeper: %d", len(got))
	}

	e.stop()
	var listed []heldSourceDTO
	res = e.get("/api/sources")
	decodeBody(t, res, &listed)
	if res.Code != http.StatusOK || len(listed) != 1 || listed[0].ID != source.ID || listed[0].Keeper != keeper.HeldButDown {
		t.Fatalf("sources when stopped = %d %s", res.Code, res.Body)
	}
	q := testQuery()
	q.SourceIDs = []string{source.ID}
	qBody, _ := json.Marshal(q)
	if res := e.json(http.MethodPost, "/api/analyze", string(qBody)); res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"keeper-stopped"`) {
		t.Fatalf("analyze when stopped = %d %s", res.Code, res.Body)
	}
	if res := e.json(http.MethodPost, "/api/sources", string(add)); res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"keeper-stopped"`) {
		t.Fatalf("add when stopped = %d %s", res.Code, res.Body)
	}
	if res := e.json(http.MethodDelete, "/api/sources/"+source.ID, ""); res.Code != http.StatusNoContent {
		t.Fatalf("remove when stopped = %d %s", res.Code, res.Body)
	}
	if res = e.get("/api/sources"); strings.Contains(res.Body.String(), source.ID) {
		t.Fatalf("removed source still listed: %s", res.Body)
	}
}

// The screenshot previews of keeper-held audit sources (P14b) answer only in the demo, and only for the named previews.
func TestDemoPreviewsOfKeeperHeldSources(t *testing.T) {
	demo := &caseEnv{t: t, h: New(audit.NewDemo(time.Now()), testAssets(), testToken, true)}
	for preview, want := range map[string]keeper.HeldState{"saved": keeper.HeldByKeeper, "stopped": keeper.HeldButDown, "": "", "migrate": ""} {
		var listed []heldSourceDTO
		decodeBody(t, demo.get("/api/sources?preview="+preview), &listed)
		if len(listed) == 0 {
			t.Fatalf("%q: the demo lists no sources", preview)
		}
		for _, s := range listed {
			if s.Keeper != want {
				t.Fatalf("%q: source %s keeper = %q, want %q", preview, s.ID, s.Keeper, want)
			}
		}
	}
	live := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	if res := live.get("/api/sources?preview=stopped"); res.Code != http.StatusOK || strings.Contains(res.Body.String(), "keeper") {
		t.Fatalf("preview outside the demo = %d %s", res.Code, res.Body)
	}
}
