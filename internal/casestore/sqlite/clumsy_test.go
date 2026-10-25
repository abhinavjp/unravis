package sqlite

import (
	"context"
	"io"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// A series query on an old case (no series-key indexes yet) must not sit behind
// a running import: the index build needs the import lock, and the caller's
// context cannot interrupt the wait. The query runs without the indexes instead,
// as the documented fallback says, and a later one builds them.
func TestSeriesQueryDoesNotWaitForARunningImport(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Old")
	c := mustOpen(t, s, info.ID)
	importSeries(t, c, "one", 60, 20)
	for _, stmt := range []string{
		"DROP INDEX ev_kind_counter", "DROP INDEX ev_kind_metric",
		"UPDATE meta SET v='2' WHERE k='schema_version'",
	} {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = mustOpen(t, s, info.ID)

	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.Import(ctx, tMeta("slow.csv"), tBody("slow"), func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			close(started)
			<-release
			_, _ = io.Copy(io.Discard, r)
			return importer.Report{Importer: testImporter, KnownGaps: []string{"synthetic test data"}}, nil
		})
		done <- err
	}()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-done
	})
	<-started

	q := casestore.EventQuery{Kinds: []string{"counter"}, Attrs: map[string]string{"counter": "c1", "object": "Memory", "instance": "i1"}}
	res := make(chan int, 1)
	go func() {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		n := 0
		_ = c.Each(qctx, q, func(evidence.Event) error { n++; return nil })
		res <- n
	}()
	select {
	case n := <-res:
		if n == 0 {
			t.Fatal("the query returned nothing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the series query is stuck behind the running import")
	}
}
