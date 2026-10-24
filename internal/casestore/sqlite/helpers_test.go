package sqlite

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
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

func mustImport(t *testing.T, c *Case, label string, n int) casestore.ImportResult {
	t.Helper()
	res, err := c.Import(context.Background(), storetest.Meta(label+".csv"), storetest.Body(label), storetest.Run(storetest.Events(label, n)))
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
