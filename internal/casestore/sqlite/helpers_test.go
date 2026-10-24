package sqlite

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sealedfile"
)

// sentinel is a synthetic marker, never client data.
const sentinel = "SENTINEL-7f3a-unravis-plaintext"

func newTestStore(t *testing.T, mut ...func(*Options)) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "cases")
	return newStoreAt(t, root, storetest.RandomProtector(), mut...), root
}

func newStoreAt(t *testing.T, root string, p sealedfile.Protector, mut ...func(*Options)) *Store {
	t.Helper()
	opt := Options{Root: root, Protector: p, MinFreeBytes: -1}
	for _, m := range mut {
		m(&opt)
	}
	s, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustCreate(t *testing.T, s *Store, title string) casestore.CaseInfo {
	t.Helper()
	info, err := s.Create(context.Background(), title)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func mustOpen(t *testing.T, s *Store, id casestore.CaseID) *Case {
	t.Helper()
	c, err := s.Open(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c.(*Case)
}

// testImporter is the importer name the helpers import under; events carry it
// as Source and in their evidence IDs, as F0's ValidateFor requires.
const testImporter = "test.csv"

var testBase = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

// tMeta is a valid import request for importer testImporter.
func tMeta(file string) casestore.ImportMeta {
	return casestore.ImportMeta{
		Importer: testImporter, FileName: file, HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: "Read the gating note for this import.",
	}
}

// tEvents builds n valid events for case c, one second apart from testBase:
// kind "cpu" on host "web-a" for even indexes, "wait" on "db-a" for odd. IDs
// come from evidence.EventID, so the same label gives the same IDs.
func tEvents(c casestore.Case, label string, n int) []evidence.Event {
	out := make([]evidence.Event, n)
	for i := range out {
		v := float64(i)
		line := int64(i + 2)
		e := evidence.Event{
			EvidenceID: evidence.EventID(string(c.Info().ID), testImporter, label, line, 0),
			Source:     testImporter, Kind: "cpu", Host: "web-a",
			Start: testBase.Add(time.Duration(i) * time.Second), Value: &v, Unit: "percent", Line: line,
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

// tRun is a RunFunc that drains the body (so the hash covers it) and emits
// events, stopping at the first emit error.
func tRun(events []evidence.Event) casestore.RunFunc {
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: testImporter, KnownGaps: []string{"synthetic test data"}}
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

// tBody is a distinct body per label, so its hash is distinct.
func tBody(label string) *bytes.Reader {
	return bytes.NewReader([]byte("synthetic body " + label + "\n"))
}

func mustImport(t *testing.T, c *Case, label string, n int) casestore.ImportResult {
	t.Helper()
	res, err := c.Import(context.Background(), tMeta(label+".csv"), tBody(label), tRun(tEvents(c, label, n)))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func counts(t *testing.T, c *Case) (imports, events, quarantined int) {
	t.Helper()
	ctx := context.Background()
	for q, dst := range map[string]*int{
		"SELECT COUNT(*) FROM imports": &imports, "SELECT COUNT(*) FROM events": &events,
		"SELECT COUNT(*) FROM quarantine": &quarantined,
	} {
		if err := c.db.QueryRowContext(ctx, q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sizedReader reports its length, as an uploaded file would.
type sizedReader struct {
	io.Reader
	n int64
}

func (s sizedReader) Size() int64 { return s.n }

func sized(n int) io.Reader {
	return sizedReader{Reader: bytes.NewReader(make([]byte, 16)), n: int64(n)}
}

func hasAny(haystack []byte, needles ...[]byte) bool {
	for _, n := range needles {
		if len(n) > 0 && bytes.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
