package discover_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer/all"
)

// Helpers shared by the Redis, MongoDB and SQL Server detector tests. Every name
// starts with dbt so it cannot clash with another test file in this package.

// dbtMarker is a fake secret planted in every config the tests feed a detector.
// It must never appear in a finding, a gap or their JSON.
const dbtMarker = "SENTINEL-7f3a"

var dbtTime = time.Date(2001, 6, 15, 8, 0, 0, 0, time.UTC)

// dbtServices adds the optional CommandLine method to the fake services list.
type dbtServices struct {
	*fake.Services
	Lines map[string]string
	Err   error
}

func (s *dbtServices) CommandLine(name string) (string, error) {
	if s.Err != nil {
		return "", s.Err
	}
	l, ok := s.Lines[name]
	if !ok {
		return "", fs.ErrNotExist
	}
	return l, nil
}

// dbtEnv builds the Env over the fakes. With a non-nil lines map the Services
// can answer CommandLine; with nil they cannot, as with today's real Services.
func dbtEnv(e *fake.Env, lines map[string]string) discover.Env {
	env := e.Env(all.Registry())
	if lines != nil {
		env.Services = &dbtServices{Services: e.Services, Lines: lines}
	}
	return env
}

func dbtAddService(e *fake.Env, name, image, dir string) {
	e.Services.Items = append(e.Services.Items, discover.Service{Name: name, State: "running", BinaryDir: dir, Image: image})
}

// dbtFile adds a file whose size is its content length.
func dbtFile(e *fake.Env, path, content string) {
	e.FS.AddFile(path, int64(len(content)), dbtTime, []byte(content))
}

func dbtLines(lines ...string) string { return strings.Join(lines, "\r\n") + "\r\n" }

// dbtAssertClean fails when the marker shows in anything a finding or a gap
// carries: its fields, its paths or its JSON.
func dbtAssertClean(t *testing.T, findings []discover.Finding, gaps []discover.Gap) {
	t.Helper()
	b, err := json.Marshal(struct {
		F []discover.Finding
		G []discover.Gap
	}{findings, gaps})
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	sb.Write(b)
	for _, f := range findings {
		sb.WriteString(fmt.Sprintf("%+v", f))
		sb.WriteString(strings.Join(f.Paths(), "|"))
	}
	for _, g := range gaps {
		sb.WriteString(fmt.Sprintf("%+v", g))
	}
	if strings.Contains(strings.ToLower(sb.String()), "sentinel") {
		t.Errorf("secret marker leaked into the result: %s", b)
	}
}

func dbtOneGap(t *testing.T, gaps []discover.Gap, want discover.Reason) {
	t.Helper()
	if len(gaps) != 1 || gaps[0].Reason != want {
		t.Fatalf("gaps = %+v, want one gap with reason %s", gaps, want)
	}
}

// dbtRecFS records every path the detector touches through any FS method.
type dbtRecFS struct {
	discover.FS
	mu      sync.Mutex
	touched []string
}

func (r *dbtRecFS) note(p string) {
	r.mu.Lock()
	r.touched = append(r.touched, p)
	r.mu.Unlock()
}
func (r *dbtRecFS) Lstat(p string) (discover.FileInfo, error) { r.note(p); return r.FS.Lstat(p) }
func (r *dbtRecFS) Stat(p string) (discover.FileInfo, error)  { r.note(p); return r.FS.Stat(p) }
func (r *dbtRecFS) ReadDir(p string) ([]discover.FileInfo, error) {
	r.note(p)
	return r.FS.ReadDir(p)
}
func (r *dbtRecFS) Final(p string) (string, bool, error) { r.note(p); return r.FS.Final(p) }
func (r *dbtRecFS) Head(p string, n int) ([]byte, error) { r.note(p); return r.FS.Head(p, n) }

// Sample log text the importer registry recognises. All synthetic.
const (
	dbtRedisLog = "1234:M 25 Oct 2001 14:02:30.120 * Background saving started by pid 1240\n" +
		"1240:C 25 Oct 2001 14:02:31.900 * DB saved on disk\n" +
		"1234:M 25 Oct 2001 14:02:32.004 * Background saving terminated with success\n" +
		"1234:M 25 Oct 2001 14:05:00.000 # User requested shutdown...\n" +
		"1300:M 25 Oct 2001 14:05:03.100 * Ready to accept connections tcp\n"

	dbtMongoLog = `{"t":{"$date":"2001-09-09T01:40:00.000+00:00"},"s":"I","c":"CONTROL","id":4615611,"ctx":"initandlisten","msg":"MongoDB starting","attr":{"pid":4242,"port":27017,"dbPath":"/data/db","architecture":"64-bit","host":"mongo-a.example.test"}}` + "\n" +
		`{"t":{"$date":"2001-09-09T01:40:01.000+00:00"},"s":"I","c":"NETWORK","id":22943,"ctx":"listener","msg":"Connection accepted","attr":{"remote":"192.0.2.10:50000","connectionId":1,"connectionCount":1}}` + "\n"

	dbtSQLLog = "2001-06-15 08:00:01.23 Server      Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64)\n" +
		"\tSynthetic build line for tests\n" +
		"\tCopyright (C) 2019 Microsoft Corporation\n" +
		"2001-06-15 08:00:01.25 Server      UTC adjustment: 12:00\n" +
		"2001-06-15 08:00:01.25 Server      (c) Microsoft Corporation.\n" +
		"2001-06-15 08:00:01.30 Server      SQL Server is starting at normal priority base (=7). This is an informational message only. No user action is required.\n" +
		"2001-06-15 08:00:02.10 spid9s      Starting up database 'db-alpha'.\n" +
		"2001-06-15 13:30:00.00 Logon       Error: 18456, Severity: 14, State: 8.\n"
)
