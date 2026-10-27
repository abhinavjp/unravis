package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/ledger"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/server"
)

const demoTestToken = "demo-cases-test-token-0123456789abcdef"

var demoTestNow = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func envOf(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func TestDemoEnv(t *testing.T) {
	real := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name      string
		demo      bool
		env       map[string]string
		wantNow   time.Time
		wantCases bool
		wantErr   string
		wantNotes int
	}{
		{name: "plain demo changes nothing", demo: true, wantNow: real},
		{name: "fixed clock", demo: true, env: map[string]string{envDemoNow: "2026-10-20T09:00:00Z"}, wantNow: demoTestNow},
		{name: "fixed clock with an offset becomes UTC", demo: true, env: map[string]string{envDemoNow: "2026-10-20T10:00:00+01:00"}, wantNow: demoTestNow},
		{name: "spaces around the value are fine", demo: true, env: map[string]string{envDemoNow: "  2026-10-20T09:00:00Z "}, wantNow: demoTestNow},
		{name: "bad clock", demo: true, env: map[string]string{envDemoNow: "yesterday"}, wantErr: badDemoNowMessage},
		{name: "date without a time is refused", demo: true, env: map[string]string{envDemoNow: "2026-10-20"}, wantErr: badDemoNowMessage},
		{name: "cases on", demo: true, env: map[string]string{envDemoCases: "1", "AUDIT_CONFIG_DIR": "x"}, wantNow: real, wantCases: true},
		{name: "cases 0 is off", demo: true, env: map[string]string{envDemoCases: "0"}, wantNow: real},
		{name: "cases without a config folder", demo: true, env: map[string]string{envDemoCases: "1"}, wantErr: demoCasesNeedConfigDir},
		{name: "cases with a strange value", demo: true, env: map[string]string{envDemoCases: "yes", "AUDIT_CONFIG_DIR": "x"}, wantErr: badDemoCasesMessage},
		{name: "ignored without demo", demo: false, env: map[string]string{envDemoNow: "yesterday", envDemoCases: "1"}, wantNow: real, wantNotes: 2},
		{name: "unset without demo is silent", demo: false, wantNow: real},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, notes, err := demoEnv(c.demo, envOf(c.env), real)
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !got.now.Equal(c.wantNow) {
				t.Errorf("now = %v, want %v", got.now, c.wantNow)
			}
			if got.cases != c.wantCases {
				t.Errorf("cases = %v, want %v", got.cases, c.wantCases)
			}
			if len(notes) != c.wantNotes {
				t.Errorf("notes = %v, want %d", notes, c.wantNotes)
			}
		})
	}
}

func TestDemoCaseIDsAreValidAndDistinguishedByTheirFirstEightCharacters(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range demoCaseIDs() {
		if !id.Valid() {
			t.Errorf("%q is not a valid case id", id)
		}
		if seen[string(id)[:8]] {
			t.Errorf("%q shares its first eight characters with another demo case", id)
		}
		seen[string(id)[:8]] = true
	}
}

// listCases asks a server built the way run() builds it for its case list.
func listCases(t *testing.T, h http.Handler) (int, []map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/cases", nil)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", demoTestToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		return res.Code, nil
	}
	var out struct {
		Cases []map[string]any `json:"cases"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v: %s", err, res.Body)
	}
	return res.Code, out.Cases
}

func demoServer(t *testing.T, options []server.Option) http.Handler {
	t.Helper()
	assets := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><html><head></head><body>ok</body></html>")}}
	return server.New(audit.NewDemoWithPatterns(demoTestNow), assets, demoTestToken, true, options...)
}

func newDemoLedger(t *testing.T) ledger.Store {
	t.Helper()
	led, err := ledger.Open("")
	if err != nil {
		t.Fatal(err)
	}
	return led
}

func entriesOf(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestPlainDemoWiresNoCasesWritesNothingAndTheCaseRoutesAnswer503(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir})
	cfg, _, err := demoEnv(true, getenv, demoTestNow)
	if err != nil {
		t.Fatal(err)
	}
	options, closeCases, err := wireCases(true, cfg, getenv, newDemoLedger(t))
	if err != nil {
		t.Fatal(err)
	}
	defer closeCases()
	if len(options) != 0 {
		t.Fatalf("plain demo gave the server %d case options", len(options))
	}
	if names := entriesOf(t, dir); len(names) != 0 {
		t.Fatalf("plain demo wrote %v into the config folder", names)
	}
	h := demoServer(t, options)
	for _, path := range []string{"/api/cases", "/api/hosts"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", demoTestToken)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503", path, res.Code)
		}
	}
}

func TestRunRefusesABadDemoNowBeforeWritingAnything(t *testing.T) {
	dir := t.TempDir()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, envDemoNow: "yesterday"})
	err := run([]string{"--demo", "--no-browser", "--port", "0"}, getenv)
	if err == nil || err.Error() != badDemoNowMessage {
		t.Fatalf("run = %v, want %q", err, badDemoNowMessage)
	}
	if names := entriesOf(t, dir); len(names) != 0 {
		t.Fatalf("a refused start wrote %v", names)
	}
}

func TestRunRefusesDemoCasesWithoutAConfigFolder(t *testing.T) {
	err := run([]string{"--demo", "--no-browser", "--port", "0"}, envOf(map[string]string{envDemoCases: "1"}))
	if err == nil || err.Error() != demoCasesNeedConfigDir {
		t.Fatalf("run = %v, want %q", err, demoCasesNeedConfigDir)
	}
}

func TestDemoVariablesAreIgnoredWithoutDemoAndDoNotStopTheStart(t *testing.T) {
	// demoEnv is all run() calls before it goes live; the bad values must not become errors.
	_, notes, err := demoEnv(false, envOf(map[string]string{envDemoNow: "yesterday", envDemoCases: "banana"}), demoTestNow)
	if err != nil || len(notes) != 2 {
		t.Fatalf("err = %v, notes = %v", err, notes)
	}
}

// seededServer seeds demo cases into dir and returns a server wired like run().
func seededServer(t *testing.T, dir string) (http.Handler, func()) {
	t.Helper()
	getenv := envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, envDemoNow: demoTestNow.Format(time.RFC3339), envDemoCases: "1"})
	cfg, _, err := demoEnv(true, getenv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	options, closeCases, err := wireCases(true, cfg, getenv, newDemoLedger(t))
	if err != nil {
		if strings.Contains(err.Error(), "unavailable on this platform") {
			t.Skip("encrypted storage is not available on this platform")
		}
		t.Fatal(err)
	}
	return demoServer(t, options), closeCases
}

func TestDemoCasesSeedFourCasesWithFixedIDsTitlesAndTimes(t *testing.T) {
	h, closeCases := seededServer(t, t.TempDir())
	defer closeCases()
	code, list := listCases(t, h)
	if code != http.StatusOK {
		t.Fatalf("GET /api/cases = %d", code)
	}
	byID := map[string]map[string]any{}
	for _, c := range list {
		byID[c["id"].(string)] = c
	}
	if len(list) != 4 {
		t.Fatalf("got %d cases, want 4: %v", len(list), list)
	}
	want := func(id, title string, created time.Time, closed *time.Time, imports float64) {
		t.Helper()
		c := byID[id]
		if c == nil {
			t.Fatalf("case %s missing", id)
		}
		if c["title"] != title {
			t.Errorf("%s title = %v, want %q", id, c["title"], title)
		}
		if got, _ := time.Parse(time.RFC3339Nano, c["created"].(string)); !got.Equal(created) {
			t.Errorf("%s created = %v, want %v", id, c["created"], created)
		}
		switch got := c["closed"].(type) {
		case nil:
			if closed != nil {
				t.Errorf("%s is open, want closed at %v", id, closed)
			}
		case string:
			at, _ := time.Parse(time.RFC3339Nano, got)
			if closed == nil || !at.Equal(*closed) {
				t.Errorf("%s closed = %v, want %v", id, got, closed)
			}
		}
		if c["imports"] != imports {
			t.Errorf("%s imports = %v, want %v", id, c["imports"], imports)
		}
	}
	closedAt := demoTestNow.Add(-4 * day)
	want(string(demoCaseOpenID), "Checkout slowdown, 17 October", demoTestNow.Add(-3*day), nil, 1)
	want(string(demoCaseClosedID), "Night batch overrun, 10 October", demoTestNow.Add(-10*day), &closedAt, 1)
	// The refused file is a quarantine, not an import, so it is not counted here.
	want(string(demoCaseFailedID), "Report export timeouts, 18 October", demoTestNow.Add(-2*day), nil, 1)
	if u := byID[string(demoCaseUntrustedID)]; u == nil || u["untrusted"] != true {
		t.Errorf("the untrusted demo case = %v, want untrusted", u)
	}
}

func TestDemoCasesAreTheSameOnEveryRunAndOnASecondStart(t *testing.T) {
	first := t.TempDir()
	h1, close1 := seededServer(t, first)
	_, a := listCases(t, h1)
	close1()
	h2, close2 := seededServer(t, t.TempDir())
	_, b := listCases(t, h2)
	close2()
	if !reflect.DeepEqual(a, b) {
		t.Errorf("two runs differ:\n%v\n%v", a, b)
	}
	// Starting again on the folder the first run used leaves the same four cases.
	h3, close3 := seededServer(t, first)
	defer close3()
	_, c := listCases(t, h3)
	if !reflect.DeepEqual(a, c) {
		t.Errorf("a second start changed the cases:\n%v\n%v", a, c)
	}
}

func TestDemoCasesRefuseAFolderWithRealCasesOrAMasterKey(t *testing.T) {
	getenvFor := func(dir string) func(string) string {
		return envOf(map[string]string{"AUDIT_CONFIG_DIR": dir, envDemoCases: "1"})
	}
	realCase := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realCase, "cases", "abcdefghijklmnopqrstuvwxyz"), 0o700); err != nil {
		t.Fatal(err)
	}
	withKey := t.TempDir()
	if err := os.WriteFile(filepath.Join(withKey, masterkey.FileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"a real case folder": realCase, "a master key": withKey} {
		getenv := getenvFor(dir)
		cfg, _, err := demoEnv(true, getenv, demoTestNow)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = wireCases(true, cfg, getenv, newDemoLedger(t))
		if err == nil || err.Error() != demoCasesNeedEmptyDir {
			t.Errorf("%s: err = %v, want %q", name, err, demoCasesNeedEmptyDir)
		}
	}
	// Nothing was added beside the real case.
	if names := entriesOf(t, filepath.Join(realCase, "cases")); len(names) != 1 {
		t.Errorf("the refused start left %v in the real cases folder", names)
	}
}

func TestDemoIDSourceGivesTheForcedIDOnceThenFreshValidDistinctOnes(t *testing.T) {
	taken := map[casestore.CaseID]bool{demoNewID(1): true}
	ids := &demoIDSource{exists: func(id casestore.CaseID) bool { return taken[id] }}
	ids.force(demoCaseOpenID)
	first, _ := ids.next()
	if first != demoCaseOpenID {
		t.Fatalf("first = %s, want the forced ID", first)
	}
	seen := map[casestore.CaseID]bool{}
	firstEight := map[string]bool{}
	for i := 0; i < 40; i++ {
		id, err := ids.next()
		if err != nil {
			t.Fatal(err)
		}
		if !id.Valid() {
			t.Fatalf("%q is not a valid case ID", id)
		}
		if seen[id] || taken[id] || id == demoCaseOpenID {
			t.Fatalf("%s was given twice or is taken", id)
		}
		seen[id] = true
		firstEight[string(id)[:8]] = true
	}
	if len(firstEight) < 31 {
		t.Errorf("only %d distinct first-eight prefixes in 40 IDs; evidence IDs carry only those", len(firstEight))
	}
	// Same on every run.
	again := &demoIDSource{exists: func(id casestore.CaseID) bool { return taken[id] }}
	a, _ := again.next()
	if a != demoNewID(0) || a != demoNewID(0) {
		t.Errorf("the counter is not fixed: %s", a)
	}
}

func TestDemoIDSourceIsSafeForManyGoroutinesAndRunsOut(t *testing.T) {
	ids := &demoIDSource{}
	var mu sync.Mutex
	got := map[casestore.CaseID]bool{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id, err := ids.next()
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				if got[id] {
					t.Errorf("%s given twice", id)
				}
				got[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	ids.count = demoNewIDLimit
	if _, err := ids.next(); err == nil {
		t.Error("an empty counter must refuse, not repeat an ID")
	}
}

func createCase(t *testing.T, h http.Handler, title string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/cases", strings.NewReader(`{"title":"`+title+`"}`))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", demoTestToken)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create case = %d %s", res.Code, res.Body)
	}
	var dto struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	return dto.ID
}

func TestCreatingACaseAfterSeedingGetsAFreshValidIDAndSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	h, closeCases := seededServer(t, dir)
	a := createCase(t, h, "First made in the demo")
	b := createCase(t, h, "Second made in the demo")
	closeCases()
	for _, id := range []string{a, b} {
		if !casestore.CaseID(id).Valid() {
			t.Errorf("%q is not a valid case ID", id)
		}
		for _, seeded := range demoCaseIDs() {
			if id == string(seeded) {
				t.Errorf("a new case got the seeded ID %s", id)
			}
		}
	}
	if a == b {
		t.Fatalf("two new cases got the same ID %s", a)
	}
	// A restart on the same folder is not refused for the cases made before, and a
	// case made after it does not collide with them.
	h2, close2 := seededServer(t, dir)
	defer close2()
	c := createCase(t, h2, "Made after a restart")
	if c == a || c == b {
		t.Errorf("after a restart a new case got the old ID %s", c)
	}
}

// The P7 "nothing read" screen needs a held lane day whose every check is
// "Cannot test yet" with no fix. Only the failed-import demo case holds one.
func TestDemoCasesSeedOneNothingReadLaneDay(t *testing.T) {
	h, closeCases := seededServer(t, t.TempDir())
	defer closeCases()
	_, list := listCases(t, h)
	held := map[string]int{}
	for _, c := range list {
		id, _ := c["id"].(string)
		req := httptest.NewRequest(http.MethodGet, "/api/cases/"+id+"/lanes", nil)
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", demoTestToken)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code == http.StatusConflict && strings.HasPrefix(id, string(demoCaseUntrustedID)) {
			continue // not opened by design
		}
		if res.Code != http.StatusOK {
			t.Fatalf("GET lanes for %s = %d: %s", id, res.Code, res.Body)
		}
		var out struct {
			Days []struct {
				Kind string `json:"kind"`
				Lane struct {
					Checks []struct {
						State string `json:"state"`
						Fix   any    `json:"fix"`
					} `json:"checks"`
				} `json:"lane"`
			} `json:"days"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v: %s", id, err, res.Body)
		}
		held[id] = len(out.Days)
		for _, l := range out.Days {
			if l.Kind != "sql" || len(l.Lane.Checks) == 0 {
				t.Fatalf("%s: unexpected lane %+v", id, l)
			}
			for _, ck := range l.Lane.Checks {
				if ck.State != "Cannot test yet" || ck.Fix != nil {
					t.Fatalf("%s: check %+v is not a nothing-read check", id, ck)
				}
			}
		}
	}
	for id, n := range held {
		want := 0
		if id == string(demoCaseFailedID) {
			want = 1
		}
		if n != want {
			t.Errorf("case %s holds %d lane days, want %d", id, n, want)
		}
	}
}
