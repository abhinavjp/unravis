package sqlite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// BL-573: the upgrade path of the audit store, checked with a real previous build.
//
// tools/ci/compat-upgrade.sh runs this in two trees:
//   - the branch under test runs TestCompatWriteCase (compat_write_test.go): it makes a case with audit
//     runs, calls, a chain and the code.hash_key, and writes the facts it expects to <dir>/compat-expect.txt;
//   - the last promoted build (develop) gets a copy of THIS file only and runs TestCompatReadCase: it opens that
//     case with the code it ships, checks integrity, reads, imports, closes and reopens, and compares the audit
//     tables with the facts the writer recorded.
//
// So this file may use only what both builds have: casestore/sqlite New, Create, Open, Import, Events,
// IntegrityCheck, SetWorkspaceBlob, Close, CloseCase, the unexported db handle of a Case (read-only queries
// here), and the casestore, evidence, importer and storetest packages. It must not use the code-run API
// (BeginRun and the rest) or any helper from another _test.go file: the writer lives in its own file for that
// reason, and everything this file needs from the helpers is repeated below under a compat name.
// Without UNRAVIS_COMPAT_READ both tests skip, so a normal `go test` is unaffected.
const (
	compatWriteEnv  = "UNRAVIS_COMPAT_WRITE"
	compatReadEnv   = "UNRAVIS_COMPAT_READ"
	compatCasesDir  = "cases"
	compatIDFile    = "case-id.txt"
	compatExpect    = "compat-expect.txt"
	compatKey       = "unravis-compat-test-key-32bytes!" // exactly 32 bytes: a fixed key, so the second build can unseal the case
	compatImporter  = "test.csv"
	compatWriteEvts = 5 // events the writer imports
	compatReadEvts  = 4 // events the reader imports into the case it was given
)

var compatBase = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

func compatOptions(t *testing.T, dir string) Options {
	t.Helper()
	p, err := storetest.NewProtector([]byte(compatKey))
	if err != nil {
		t.Fatalf("storetest.NewProtector with a fixed key: %v", err)
	}
	return Options{Root: filepath.Join(dir, compatCasesDir), Protector: p, MinFreeBytes: -1}
}

func compatMeta(file string) casestore.ImportMeta {
	return casestore.ImportMeta{
		Importer: compatImporter, FileName: file, HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: "Read the gating note for this import.",
	}
}

func compatEvents(id casestore.CaseID, label string, n int) []evidence.Event {
	out := make([]evidence.Event, n)
	for i := range out {
		v := float64(i)
		line := int64(i + 2)
		e := evidence.Event{
			EvidenceID: evidence.EventID(string(id), compatImporter, label, line, 0),
			Source:     compatImporter, Kind: "cpu", Host: "web-a",
			Start: compatBase.Add(time.Duration(i) * time.Second), Value: &v, Unit: "percent", Line: line,
			TZRule: "utc-field", Attrs: map[string]string{"counter": "total"},
		}
		e.End = e.Start.Add(500 * time.Millisecond)
		if i%2 == 1 {
			e.Kind, e.Host = "wait", "db-a"
		}
		out[i] = e
	}
	return out
}

func compatRun(events []evidence.Event) casestore.RunFunc {
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: compatImporter, KnownGaps: []string{"synthetic test data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, e := range events {
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
}

func compatImport(t *testing.T, c *Case, label string, n int) {
	t.Helper()
	body := bytes.NewReader([]byte("synthetic body " + label + "\n"))
	if _, err := c.Import(context.Background(), compatMeta(label+".csv"), body, compatRun(compatEvents(c.Info().ID, label, n))); err != nil {
		t.Fatalf("Import %s: %v", label, err)
	}
}

// compatFacts is what the writer records about the case and the reader compares with. Counts come from
// read-only queries on the case database; the key names are the audit tables of BL-369.
type compatFacts map[string]string

var compatCountKeys = []string{"code_runs", "code_run_calls", "code_chain"}

// compatQuery reads the facts straight from the open case database.
func compatQuery(t *testing.T, c *Case) compatFacts {
	t.Helper()
	f := compatFacts{}
	for _, tbl := range compatCountKeys {
		var n int
		if err := c.db.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&n); err != nil {
			t.Fatalf("the audit table %s is gone or unreadable in this build: %v", tbl, err)
		}
		f[tbl] = strconv.Itoa(n)
	}
	for k, key := range map[string]string{"hash_key_rows": "code.hash_key", "head_rows": "code.chain.head"} {
		var n int
		if err := c.db.QueryRow("SELECT COUNT(*) FROM meta WHERE k = ?", key).Scan(&n); err != nil {
			t.Fatalf("meta %s: %v", key, err)
		}
		f[k] = strconv.Itoa(n)
	}
	var head string
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k = 'code.chain.head'").Scan(&head); err != nil {
		t.Fatalf("meta code.chain.head: %v", err)
	}
	f["head"] = head
	return f
}

func compatEncode(f compatFacts) []byte {
	var b strings.Builder
	for _, k := range []string{"code_runs", "code_run_calls", "code_chain", "hash_key_rows", "head_rows", "head", "events", "case_id"} {
		fmt.Fprintf(&b, "%s=%s\n", k, f[k])
	}
	return []byte(b.String())
}

func compatDecode(t *testing.T, raw []byte) compatFacts {
	t.Helper()
	f := compatFacts{}
	for _, line := range strings.Split(strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n")), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("bad line in %s: %q", compatExpect, line)
		}
		f[k] = v
	}
	return f
}

// TestCompatReadCase runs in the PREVIOUS build's tree. See the comment at the top of this file.
func TestCompatReadCase(t *testing.T) {
	dir := os.Getenv(compatReadEnv)
	if dir == "" {
		t.Skip(compatReadEnv + " is not set: this test runs from tools/ci/compat-upgrade.sh")
	}
	ctx := context.Background()
	rawID, err := os.ReadFile(filepath.Join(dir, compatIDFile))
	if err != nil {
		t.Fatalf("the writer left no case id: %v", err)
	}
	id := casestore.CaseID(strings.TrimSpace(string(rawID)))
	rawFacts, err := os.ReadFile(filepath.Join(dir, compatExpect))
	if err != nil {
		t.Fatalf("the writer left no expectations: %v", err)
	}
	want := compatDecode(t, rawFacts)
	if want["case_id"] != string(id) {
		t.Fatalf("case id %q does not match the expectations (%q)", id, want["case_id"])
	}
	wantEvents, err := strconv.Atoi(want["events"])
	if err != nil {
		t.Fatalf("events in the expectations: %v", err)
	}
	if want["code_runs"] == "0" || want["code_run_calls"] == "0" || want["code_chain"] == "0" || want["hash_key_rows"] != "1" {
		t.Fatalf("the writer made no audit data, so this run would prove nothing: %v", want)
	}

	opt := compatOptions(t, dir)
	s, err := New(opt)
	if err != nil {
		t.Fatalf("the previous build cannot open the store: %v", err)
	}
	oc, err := s.Open(ctx, id)
	if err != nil {
		t.Fatalf("the previous build cannot open the case the branch wrote: %v", err)
	}
	c := oc.(*Case)
	t.Cleanup(func() { _ = c.Close() })

	if err := c.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck on the case the branch wrote: %v", err)
	}
	got, err := c.Events(ctx, casestore.EventQuery{Limit: 100})
	if err != nil || len(got) != wantEvents {
		t.Fatalf("Events = %d (%v), want %d", len(got), err, wantEvents)
	}
	// The audit tables are still there, with what the writer recorded (read-only queries).
	if gotFacts := compatQuery(t, c); !compatSame(gotFacts, want) {
		t.Fatalf("audit tables before the import: got %v, want %v", gotFacts, want)
	}

	compatImport(t, c, "after-upgrade", compatReadEvts)
	if got, err := c.Events(ctx, casestore.EventQuery{Limit: 100}); err != nil || len(got) != wantEvents+compatReadEvts {
		t.Fatalf("Events after the import = %d (%v), want %d", len(got), err, wantEvents+compatReadEvts)
	}
	if err := c.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck after the import: %v", err)
	}
	if err := c.SetWorkspaceBlob(ctx, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("workspace blob write: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.CloseCase(ctx, id); err != nil {
		t.Fatalf("CloseCase: %v", err)
	}

	s2, err := New(opt)
	if err != nil {
		t.Fatalf("a fresh store on the same folder: %v", err)
	}
	oc2, err := s2.Open(ctx, id)
	if err != nil {
		t.Fatalf("reopen after the previous build wrote to the case: %v", err)
	}
	c2 := oc2.(*Case)
	t.Cleanup(func() { _ = c2.Close() })
	if err := c2.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck after reopen: %v", err)
	}
	if gotFacts := compatQuery(t, c2); !compatSame(gotFacts, want) {
		t.Fatalf("audit tables after the previous build wrote to the case: got %v, want %v", gotFacts, want)
	}
	if got, err := c2.Events(ctx, casestore.EventQuery{Limit: 100}); err != nil || len(got) != wantEvents+compatReadEvts {
		t.Fatalf("Events after reopen = %d (%v), want %d", len(got), err, wantEvents+compatReadEvts)
	}
}

// compatSame compares the audit facts (not the event count or case id, which the reader changes or already checked).
func compatSame(got, want compatFacts) bool {
	for _, k := range []string{"code_runs", "code_run_calls", "code_chain", "hash_key_rows", "head_rows", "head"} {
		if got[k] != want[k] {
			return false
		}
	}
	return true
}
