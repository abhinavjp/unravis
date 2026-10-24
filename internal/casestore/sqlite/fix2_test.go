package sqlite

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// The default per-case database cap is 4 GiB, equal to the default MaxCaseBytes
// (batch-2 L3). Twice that was an unreviewed choice.
func TestDefaultMaxDBBytesIs4GiB(t *testing.T) {
	s, _ := newTestStore(t)
	if got, want := s.opt.MaxDBBytes, int64(4<<30); got != want {
		t.Errorf("default MaxDBBytes = %d, want %d", got, want)
	}
	if s.opt.MaxDBBytes != s.opt.MaxCaseBytes {
		t.Errorf("default MaxDBBytes %d differs from MaxCaseBytes %d", s.opt.MaxDBBytes, s.opt.MaxCaseBytes)
	}
}

// A failed import hands back the importer's report from the SQLite store, on
// each error path after the importer ran (batch-2 M1).
func TestSQLiteFailedImportCarriesReport(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t, func(o *Options) { o.MaxFileRows = 3 })
	info := mustCreate(t, s, "report")
	c := mustOpen(t, s, info.ID)
	rep := importer.Report{Importer: testImporter, Rows: 9, Rejected: 4}
	over := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		_, _ = io.Copy(io.Discard, r)
		return rep, nil
	}
	res, err := c.Import(ctx, tMeta("a.csv"), strings.NewReader("one"), over)
	if err == nil || res.Report.Rows != 9 || res.Report.Rejected != 4 {
		t.Errorf("rows over the cap: res.Report %+v, err %v", res.Report, err)
	}
	boom := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		return rep, io.ErrUnexpectedEOF
	}
	res, err = c.Import(ctx, tMeta("b.csv"), strings.NewReader("two"), boom)
	if err == nil || res.Report.Rows != 9 {
		t.Errorf("importer error: res.Report %+v, err %v", res.Report, err)
	}
}

// Reads on a case answer while an import holds its transaction open. This is why
// the pool is not one connection (batch-2 L4): with a single connection every
// read would wait for the import to finish. The pool stays above one.
func TestReadsDoNotWaitForImport(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "reads")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "first", 2)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	defer rel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Import(ctx, tMeta("slow.csv"), strings.NewReader("slow body"),
			func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
				_, _ = io.Copy(io.Discard, r)
				close(started)
				<-release
				return importer.Report{Importer: testImporter}, nil
			})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("import did not start")
	}
	read := make(chan error, 1)
	go func() {
		_, err := c.Events(ctx, casestore.EventQuery{})
		if err == nil {
			_, err = c.Imports(ctx)
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Errorf("read during import: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a read waited for the running import")
	}
	rel()
	if err := <-done; err != nil {
		t.Fatalf("import: %v", err)
	}
}
