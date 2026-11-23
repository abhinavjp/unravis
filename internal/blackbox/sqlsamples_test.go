package blackbox

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/bbsql"
)

var sqlT0 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func TestSQLFileNameMatchesTheWriter(t *testing.T) {
	for _, tm := range []time.Time{sqlT0, sqlT0.Add(37 * time.Minute), time.Date(2026, 1, 2, 23, 59, 0, 0, time.UTC)} {
		want := filepath.Base(bbsql.SamplePath("x", tm))
		if got := SQLFileNameFor(tm); got != want {
			t.Fatalf("name %q, writer says %q", got, want)
		}
		h, ok := SQLHourOfName(want)
		if !ok || !h.Equal(tm.Truncate(time.Hour)) {
			t.Fatalf("hour of %q = %v %v", want, h, ok)
		}
	}
}

func TestOnlySQLSampleFileNamesAreValid(t *testing.T) {
	for _, n := range []string{
		"", "bb-sql-.ndjson", "bb-sql-20261123-1.ndjson", "bb-sql-20261123-10.json", "bb-sql-20261123-10.ndjson.x", "bb-20261123-10.ndjson",
		"../bb-sql-20261123-10.ndjson", `sql\bb-sql-20261123-10.ndjson`, "bb-sql-20261123-10.ndjson/", "bb-sql- 0261123-10.ndjson", "bb-sql-+0261123-10.ndjson",
		"bb-sql-20261323-10.ndjson", "bb-sql-20261123-25.ndjson", "state.json", "status.json", "bb-sql-20261123-10.ndjson\x00", "bb-sql-20261123-10.ndjson%00",
	} {
		if ValidSQLFileName(n) {
			t.Errorf("%q must not be a SQL sample file name", n)
		}
	}
	if !ValidSQLFileName("bb-sql-20261123-10.ndjson") {
		t.Error("the real name was refused")
	}
	// A host recording name is not a SQL name and the other way round.
	if ValidFileName("bb-sql-20261123-10.ndjson") || ValidSQLFileName("bb-20261123-10.ndjson") {
		t.Error("the two name families must stay apart")
	}
}

func TestDecodeSQLLineEveryKind(t *testing.T) {
	cases := map[string]string{
		"requests":   `[{"waitType":"LCK_M_X","tasks":3,"maxWaitMs":900,"blocked":2,"headBlockers":1,"maxBlockMs":800},{"waitType":"RUNNING","tasks":5,"maxWaitMs":0,"blocked":0,"headBlockers":0,"maxBlockMs":0}]`,
		"waits":      `[{"waitType":"PAGEIOLATCH_SH","deltaMs":1200,"deltaTasks":40}]`,
		"schedulers": `{"currentTasks":12,"runnableTasks":2}`,
		"semaphores": `[{"poolId":2,"resourceSemaphoreId":0,"grantees":1,"waiters":0,"forced":0,"timeouts":0}]`,
		"tempdb":     `{"internalPages":100,"userPages":200}`,
		"filestats":  `[{"databaseId":5,"fileId":1,"readStallMs":10,"writeStallMs":4,"reads":5,"writes":2,"sizeOnDiskBytes":8192}]`,
		"ag":         `[{"databaseId":5,"syncState":2}]`,
	}
	for kind, rows := range cases {
		line := `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"` + kind + `","rows":` + rows + `}`
		s, res := decodeSQLLine([]byte(line))
		if res != sqlLineOK || s.Kind != kind || !s.T.Equal(sqlT0.Add(15*time.Second)) {
			t.Fatalf("%s: %v %+v", kind, res, s)
		}
	}
	s, _ := decodeSQLLine([]byte(`{"v":1,"t":"2026-11-23T10:00:15Z","kind":"requests","rows":` + cases["requests"] + `}`))
	if len(s.Requests) != 2 || s.Requests[0].Blocked != 2 || s.Requests[1].WaitType != "RUNNING" {
		t.Fatalf("requests decoded wrong: %+v", s.Requests)
	}
	s, _ = decodeSQLLine([]byte(`{"v":1,"t":"2026-11-23T10:00:15Z","kind":"schedulers","rows":` + cases["schedulers"] + `}`))
	if s.Schedulers == nil || s.Schedulers.RunnableTasks != 2 {
		t.Fatalf("schedulers: %+v", s.Schedulers)
	}
}

func TestDecodeSQLLineKeepsAMissingReadingMissing(t *testing.T) {
	for _, rows := range []string{`null`, ``} {
		line := `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":` + rows + `}`
		if rows == "" {
			line = `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits"}`
		}
		s, res := decodeSQLLine([]byte(line))
		if res != sqlLineOK || s.Waits != nil || s.Kind != "waits" {
			t.Fatalf("%q: %v %+v", rows, res, s)
		}
	}
}

func TestDecodeSQLLineRefusesWhatTheWriterNeverWrites(t *testing.T) {
	bad := map[string]string{
		"unknown kind":         `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"sqltext","rows":[]}`,
		"unknown line field":   `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[],"login":"sa"}`,
		"unknown row field":    `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[{"waitType":"X","deltaMs":1,"deltaTasks":1,"sql_text":"select 1"}]}`,
		"query text as a wait": `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[{"waitType":"select * from t","deltaMs":1,"deltaTasks":1}]}`,
		"lowercase wait":       `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"requests","rows":[{"waitType":"lck_m_x","tasks":1,"maxWaitMs":0,"blocked":0,"headBlockers":0,"maxBlockMs":0}]}`,
		"wait too long":        `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[{"waitType":"` + strings.Repeat("A", 61) + `","deltaMs":1,"deltaTasks":1}]}`,
		"sync state out":       `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"ag","rows":[{"databaseId":1,"syncState":9}]}`,
		"string for a number":  `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"tempdb","rows":{"internalPages":"1","userPages":2}}`,
		"negative count":       `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"tempdb","rows":{"internalPages":-1,"userPages":2}}`,
		"object for a list":    `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":{"waitType":"X"}}`,
		"list for an object":   `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"schedulers","rows":[{"currentTasks":1,"runnableTasks":1}]}`,
		"no version":           `{"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[]}`,
		"no time":              `{"v":1,"kind":"waits","rows":[]}`,
		"two values":           `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[]} {"v":1}`,
		"trailing text":        `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[]}x`,
		"not json":             `hello`,
		"too many waits":       `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[` + strings.TrimSuffix(strings.Repeat(`{"waitType":"X","deltaMs":1,"deltaTasks":1},`, bbsql.CapWaits+1), ",") + `]}`,
		"too many semaphores":  `{"v":1,"t":"2026-11-23T10:00:15Z","kind":"semaphores","rows":[` + strings.TrimSuffix(strings.Repeat(`{"poolId":1,"resourceSemaphoreId":0,"grantees":0,"waiters":0},`, bbsql.CapSemaphores+1), ",") + `]}`,
	}
	for name, line := range bad {
		if _, res := decodeSQLLine([]byte(line)); res != sqlLineBad {
			t.Errorf("%s: accepted (%v)", name, res)
		}
	}
	// A newer writer is told apart from a damaged line, so the view can say "update Unravis".
	if _, res := decodeSQLLine([]byte(`{"v":2,"t":"2026-11-23T10:00:15Z","kind":"waits","rows":[]}`)); res != sqlLineNewer {
		t.Errorf("v2 = %v", res)
	}
}

// fakeSQLSource serves hourly files from a map; an error entry is returned as the open error.
type fakeSQLSource struct {
	files map[string]string
	errs  map[string]error
	opens []string
}

func (f *fakeSQLSource) Manifest(context.Context) (ManifestRead, error) { return ManifestRead{}, nil }
func (f *fakeSQLSource) Status(context.Context) (*StatusDoc, error)     { return nil, nil }
func (f *fakeSQLSource) OpenFile(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}
func (f *fakeSQLSource) OpenSQLFile(_ context.Context, name string) (io.ReadCloser, error) {
	f.opens = append(f.opens, name)
	if e, ok := f.errs[name]; ok {
		return nil, e
	}
	b, ok := f.files[name]
	if !ok {
		return nil, readErr(ReadNotFound, "gone", fs.ErrNotExist)
	}
	return io.NopCloser(strings.NewReader(b)), nil
}

func sqlLineAt(tm time.Time, kind, rows string) string {
	return `{"v":1,"t":"` + tm.UTC().Format(time.RFC3339Nano) + `","kind":"` + kind + `","rows":` + rows + "}\n"
}

func TestReadSQLSamplesReadsTheHoursOfTheWindowInOrder(t *testing.T) {
	f := &fakeSQLSource{files: map[string]string{
		SQLFileNameFor(sqlT0):                    sqlLineAt(sqlT0.Add(45*time.Minute), "tempdb", `{"internalPages":1,"userPages":2}`) + sqlLineAt(sqlT0.Add(5*time.Minute), "tempdb", `{"internalPages":3,"userPages":4}`),
		SQLFileNameFor(sqlT0.Add(time.Hour)):     sqlLineAt(sqlT0.Add(65*time.Minute), "schedulers", `{"currentTasks":1,"runnableTasks":0}`),
		SQLFileNameFor(sqlT0.Add(-time.Hour)):    sqlLineAt(sqlT0.Add(-5*time.Minute), "tempdb", `{"internalPages":9,"userPages":9}`),
		SQLFileNameFor(sqlT0.Add(2 * time.Hour)): sqlLineAt(sqlT0.Add(125*time.Minute), "tempdb", `{"internalPages":9,"userPages":9}`),
	}}
	from, to := sqlT0.Add(10*time.Minute), sqlT0.Add(90*time.Minute)
	got, rep, err := ReadSQLSamples(context.Background(), f, from, to)
	if err != nil {
		t.Fatal(err)
	}
	// 10:10 to 11:30 touches the 10:00 and 11:00 files only; the 10:05 line is before the window.
	if len(f.opens) != 2 {
		t.Fatalf("opened %v", f.opens)
	}
	if len(got) != 2 || !got[0].T.Equal(sqlT0.Add(45*time.Minute)) || !got[1].T.Equal(sqlT0.Add(65*time.Minute)) {
		t.Fatalf("samples = %+v", got)
	}
	if rep.Files != 2 || rep.Samples != 2 || rep.Missing != 0 || rep.Locked || rep.Corrupt != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestReadSQLSamplesSaysWhyThereIsNothing(t *testing.T) {
	from, to := sqlT0, sqlT0.Add(3*time.Hour)
	// The service never ran: every hour is simply missing, which is not an error.
	f := &fakeSQLSource{}
	got, rep, err := ReadSQLSamples(context.Background(), f, from, to)
	if err != nil || len(got) != 0 || rep.Missing != 3 || rep.Locked || rep.Dropped != 0 {
		t.Fatalf("missing: %v %+v %+v", err, got, rep)
	}
	// A locked folder.
	f = &fakeSQLSource{errs: map[string]error{SQLFileNameFor(sqlT0): readErr(ReadUnreachable, "locked", fs.ErrPermission)}}
	_, rep, err = ReadSQLSamples(context.Background(), f, from, to)
	if err != nil || !rep.Locked {
		t.Fatalf("locked: %v %+v", err, rep)
	}
	// Another failure in one hour does not hide the others.
	f = &fakeSQLSource{
		errs:  map[string]error{SQLFileNameFor(sqlT0): readErr(ReadUnreachable, "dropped", errors.New("x"))},
		files: map[string]string{SQLFileNameFor(sqlT0.Add(time.Hour)): sqlLineAt(sqlT0.Add(61*time.Minute), "tempdb", `{"internalPages":1,"userPages":1}`)},
	}
	got, rep, err = ReadSQLSamples(context.Background(), f, from, to)
	if err != nil || len(got) != 1 || rep.Dropped != 1 || rep.Missing != 1 {
		t.Fatalf("dropped: %v %+v %+v", err, got, rep)
	}
	// A refused key fails the read: it would fail every hour.
	f = &fakeSQLSource{errs: map[string]error{SQLFileNameFor(sqlT0): readErr(ReadKeyRefused, "key", nil)}}
	if _, _, err = ReadSQLSamples(context.Background(), f, from, to); KindOf(err) != ReadKeyRefused {
		t.Fatalf("key: %v", err)
	}
	// A source with no SQL files at all says so.
	_, rep, err = ReadSQLSamples(context.Background(), noSQLSource{}, from, to)
	if err != nil || !rep.Unsupported {
		t.Fatalf("unsupported: %v %+v", err, rep)
	}
	// An empty or backwards window reads nothing.
	if _, rep, _ = ReadSQLSamples(context.Background(), &fakeSQLSource{}, to, from); rep.Missing != 0 {
		t.Fatalf("backwards: %+v", rep)
	}
}

// noSQLSource is a Source with no SQL files to offer (it does not implement SQLSource).
type noSQLSource struct{}

func (noSQLSource) Manifest(context.Context) (ManifestRead, error) { return ManifestRead{}, nil }
func (noSQLSource) Status(context.Context) (*StatusDoc, error)     { return nil, nil }
func (noSQLSource) OpenFile(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}

func TestReadSQLSamplesCountsDamageAndCutLines(t *testing.T) {
	good := sqlLineAt(sqlT0.Add(time.Minute), "tempdb", `{"internalPages":1,"userPages":1}`)
	body := good + "not json\n" + `{"v":1,"t":"2026-11-23T10:02:00Z","kind":"waits","rows":[{"waitType":"x y","deltaMs":1,"deltaTasks":1}]}` + "\n" +
		`{"v":2,"t":"2026-11-23T10:03:00Z","kind":"waits","rows":[]}` + "\n" + good +
		`{"v":1,"t":"2026-11-23T10:04:00Z","kind":"tempdb","rows":{"inter` // cut: the writer is in the middle of this line
	f := &fakeSQLSource{files: map[string]string{SQLFileNameFor(sqlT0): body}}
	got, rep, err := ReadSQLSamples(context.Background(), f, sqlT0, sqlT0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || rep.Corrupt != 2 || rep.Torn != 1 || !rep.Newer {
		t.Fatalf("got %d, report %+v", len(got), rep)
	}
}

func TestReadSQLSamplesBoundsALineAndTheWholeRead(t *testing.T) {
	// A line longer than the cap is skipped to its end and counted; the next line is read.
	long := `{"v":1,"t":"2026-11-23T10:01:00Z","kind":"waits","rows":[` + strings.Repeat(" ", maxSQLLineBytes+10) + `]}` + "\n"
	body := long + sqlLineAt(sqlT0.Add(2*time.Minute), "tempdb", `{"internalPages":1,"userPages":1}`)
	f := &fakeSQLSource{files: map[string]string{SQLFileNameFor(sqlT0): body}}
	got, rep, _ := ReadSQLSamples(context.Background(), f, sqlT0, sqlT0.Add(time.Hour))
	if len(got) != 1 || rep.Corrupt != 1 {
		t.Fatalf("long line: %d %+v", len(got), rep)
	}
	// A byte budget stops the read and says it is incomplete.
	var left int64 = 100
	var lines = 1000
	_, st := readSQLFile(strings.NewReader(strings.Repeat(good1, 50)), sqlT0, sqlT0.Add(time.Hour), &left, &lines)
	if !st.limited {
		t.Fatalf("byte budget did not stop the read: %+v", st)
	}
	left, lines = 1<<30, 3
	_, st = readSQLFile(strings.NewReader(strings.Repeat(good1, 50)), sqlT0, sqlT0.Add(time.Hour), &left, &lines)
	if !st.limited {
		t.Fatalf("line budget did not stop the read: %+v", st)
	}
	// More hours than the cap keeps the newest and says so.
	f = &fakeSQLSource{}
	_, rep, _ = ReadSQLSamples(context.Background(), f, sqlT0, sqlT0.Add(time.Duration(maxSQLReadHours+5)*time.Hour))
	if !rep.Limited || len(f.opens) != maxSQLReadHours {
		t.Fatalf("hours: limited=%v opened=%d", rep.Limited, len(f.opens))
	}
	// The newest hour is read first, so a byte or line limit costs the start of the range, never the latest hours.
	if f.opens[0] != SQLFileNameFor(sqlT0.Add(time.Duration(maxSQLReadHours+4)*time.Hour)) {
		t.Fatalf("newest hour not read first: %s", f.opens[0])
	}
	if f.opens[len(f.opens)-1] != SQLFileNameFor(sqlT0.Add(5*time.Hour)) {
		t.Fatalf("oldest kept hour: %s", f.opens[len(f.opens)-1])
	}
}

var good1 = sqlLineAt(sqlT0.Add(time.Minute), "tempdb", `{"internalPages":1,"userPages":1}`)

func TestReadSQLSamplesStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := ReadSQLSamples(ctx, &fakeSQLSource{}, sqlT0, sqlT0.Add(time.Hour))
	if err == nil {
		t.Fatal("a cancelled read must say so")
	}
}

/* ---------------------------------------------------------- folder source */

func TestFolderSourceReadsTheSQLSubfolderAndWordsALockedOne(t *testing.T) {
	dir := t.TempDir()
	loc := Location{Kind: LocFolder, Path: dir}
	src := NewFolderSource(loc)
	// No sql folder: the hours are missing, not an error.
	_, rep, err := ReadSQLSamples(context.Background(), src, sqlT0, sqlT0.Add(time.Hour))
	if err != nil || rep.Missing != 1 || rep.Locked {
		t.Fatalf("no folder: %v %+v", err, rep)
	}
	sub := filepath.Join(dir, SQLDataSubdir)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, SQLFileNameFor(sqlT0)), []byte(good1), 0o644); err != nil {
		t.Fatal(err)
	}
	got, rep, err := ReadSQLSamples(context.Background(), src, sqlT0, sqlT0.Add(time.Hour))
	if err != nil || len(got) != 1 || rep.Files != 1 {
		t.Fatalf("folder: %v %d %+v", err, len(got), rep)
	}
	// A name that is not an SQL sample file is refused before any file is touched.
	for _, n := range []string{"state.json", "status.json", "../bb-20261123-10.ndjson", "bb-20261123-10.ndjson"} {
		if _, err := src.OpenSQLFile(context.Background(), n); KindOf(err) != ReadBadData {
			t.Errorf("%q: %v", n, err)
		}
	}
	// A link in place of the file is refused, never followed.
	if err := os.Symlink(filepath.Join(sub, SQLFileNameFor(sqlT0)), filepath.Join(sub, SQLFileNameFor(sqlT0.Add(time.Hour)))); err == nil {
		if rc, err := src.OpenSQLFile(context.Background(), SQLFileNameFor(sqlT0.Add(time.Hour))); err == nil {
			rc.Close()
			t.Error("a link was opened")
		}
	}
}

func TestFolderSourceWithAnotherFileSystemOffersNoSQLFolder(t *testing.T) {
	src := &FolderSource{Loc: Location{Kind: LocFolder, Path: `D:\x`}, Access: fakeAccess{}}
	_, rep, err := ReadSQLSamples(context.Background(), src, sqlT0, sqlT0.Add(time.Hour))
	if err != nil || rep.Missing != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
}

type fakeAccess struct{}

func (fakeAccess) ReadFile(string) ([]byte, error)    { return nil, fs.ErrNotExist }
func (fakeAccess) Open(string) (io.ReadCloser, error) { return nil, fs.ErrNotExist }

/* ------------------------------------------------------------ recorder route */

func writeSQLFile(t *testing.T, g *srvRig, name, body string) {
	t.Helper()
	sub := SQLDataDir(g.s.Dir())
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSQLFilesAreServedBehindTheReadKeyOnly(t *testing.T) {
	g := newSrvRig(t)
	name := SQLFileNameFor(sqlT0)
	writeSQLFile(t, g, name, good1)
	resp, body := g.get(t, "/sql/files/"+name, g.key)
	if resp.StatusCode != http.StatusOK || string(body) != good1 {
		t.Fatalf("with key: %d %q", resp.StatusCode, body)
	}
	for _, key := range []string{"", "wrong"} {
		resp, body = g.get(t, "/sql/files/"+name, key)
		if resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(body), "internalPages") {
			t.Fatalf("key %q: %d %q", key, resp.StatusCode, body)
		}
	}
}

func TestOnlySQLSampleFilesAreServedFromTheLockedFolder(t *testing.T) {
	g := newSrvRig(t)
	writeSQLFile(t, g, "state.json", `{"secret":"state"}`)
	writeSQLFile(t, g, "status.json", `{"secret":"status"}`)
	writeSQLFile(t, g, SQLFileNameFor(sqlT0), good1)
	for _, p := range []string{
		"/sql/files/state.json", "/sql/files/status.json", "/sql/files/", "/sql/files", "/sql/files/..%2Fmanifest.json", "/sql/files/../manifest.json",
		"/sql/files/" + SQLFileNameFor(sqlT0.Add(time.Hour)), // an hour with no file
		"/files/" + SQLFileNameFor(sqlT0),                    // the host route must not serve it
		"/sql/", "/sql/state.json",
	} {
		resp, body := g.get(t, p, g.key)
		if resp.StatusCode == http.StatusOK || strings.Contains(string(body), "secret") || strings.Contains(string(body), "internalPages") {
			t.Errorf("%s: %d %q", p, resp.StatusCode, body)
		}
	}
	// The host route still refuses an SQL file name, and the SQL route refuses a host file name.
	resp, _ := g.get(t, "/sql/files/"+FileNameFor(g.clk.t), g.key)
	if resp.StatusCode == http.StatusOK {
		t.Error("the SQL route served a host recording")
	}
}

func TestSQLRouteRefusesALinkedFolderOrFile(t *testing.T) {
	g := newSrvRig(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, SQLFileNameFor(sqlT0)), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The sql folder is a link to somewhere else.
	if err := os.Symlink(outside, SQLDataDir(g.s.Dir())); err != nil {
		t.Skip("symlinks not available: " + err.Error())
	}
	resp, body := g.get(t, "/sql/files/"+SQLFileNameFor(sqlT0), g.key)
	if resp.StatusCode == http.StatusOK || strings.Contains(string(body), "outside") {
		t.Fatalf("linked folder served: %d %q", resp.StatusCode, body)
	}
}

func TestHTTPSourceReadsSQLSamplesFromTheRecorder(t *testing.T) {
	g := newSrvRig(t)
	writeSQLFile(t, g, SQLFileNameFor(sqlT0), good1)
	loc, err := ParseLocation(g.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	src := NewHTTPSource(loc, g.key, nil)
	got, rep, err := ReadSQLSamples(context.Background(), src, sqlT0, sqlT0.Add(time.Hour))
	if err != nil || len(got) != 1 || rep.Files != 1 || got[0].Tempdb == nil || got[0].Tempdb.UserPages != 1 {
		t.Fatalf("%v %+v %+v", err, got, rep)
	}
	// An hour the recorder has no file for (or an older recorder with no route) is a missing hour.
	_, rep, err = ReadSQLSamples(context.Background(), src, sqlT0.Add(time.Hour), sqlT0.Add(2*time.Hour))
	if err != nil || rep.Missing != 1 {
		t.Fatalf("missing: %v %+v", err, rep)
	}
	// A wrong key fails the read with the key words.
	bad := NewHTTPSource(loc, "wrong", nil)
	if _, _, err = ReadSQLSamples(context.Background(), bad, sqlT0, sqlT0.Add(time.Hour)); KindOf(err) != ReadKeyRefused {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestDecodeSQLLineFromANewerWriterIsNewerNotDamaged(t *testing.T) {
	_, res := decodeSQLLine([]byte(`{"v":2,"t":"2026-11-23T10:00:00Z","kind":"tempdb","rows":null,"addedLater":true}`))
	if res != sqlLineNewer {
		t.Fatalf("got %v", res)
	}
	if _, res := decodeSQLLine([]byte(`{"v":1,"t":"2026-11-23T10:00:00Z","kind":"tempdb","rows":null,"extra":1}`)); res != sqlLineBad {
		t.Fatalf("v1 with an unknown field must stay damaged: %v", res)
	}
}
