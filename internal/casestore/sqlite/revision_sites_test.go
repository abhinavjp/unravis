package sqlite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// wantRevision fails the test unless the case revision is want.
func wantRevision(t *testing.T, c *Case, want int64, when string) {
	t.Helper()
	if got := mustRevision(t, c); got != want {
		t.Fatalf("revision %s = %d, want %d", when, got, want)
	}
}

func newRevisionCase(t *testing.T, title string) *Case {
	t.Helper()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, title)
	return mustOpen(t, s, info.ID)
}

func TestRevisionImportBumpsOnce(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Import bump")
	wantRevision(t, c, 0, "before any import")
	mustImport(t, c, "one", 2)
	wantRevision(t, c, 1, "after one import")
	// The same bytes again are a duplicate: nothing committed, nothing bumped.
	_, err := c.Import(ctx, tMeta("one.csv"), tBody("one"), tRun(tEvents(c, "one", 2)))
	if !errors.Is(err, casestore.ErrDuplicate) {
		t.Fatalf("second import of the same bytes: %v, want ErrDuplicate", err)
	}
	wantRevision(t, c, 1, "after a duplicate import")
}

func TestRevisionRefusedImportDoesNotBump(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Refused import")
	mustImport(t, c, "base", 1)
	wantRevision(t, c, 1, "after the first import")
	boom := errors.New("synthetic importer failure")
	_, err := c.Import(ctx, tMeta("bad.csv"), tBody("bad"), func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		return importer.Report{Importer: testImporter}, boom
	})
	if err == nil {
		t.Fatal("an import whose run function fails returned no error")
	}
	wantRevision(t, c, 1, "after a refused import")
}

func TestRevisionSetEnvsBumps(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Envs bump")
	if err := c.SetEnvs(ctx, []string{"Production"}); err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 1, "after the first SetEnvs")
	// Always bumps, even when the list is unchanged.
	if err := c.SetEnvs(ctx, []string{"Production"}); err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 2, "after the second SetEnvs")
}

func TestRevisionAddAndRemoveSourceBump(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Source bump")
	src, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis", Env: "Production"})
	if err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 1, "after AddSource")
	if err := c.RemoveSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 2, "after RemoveSource")
	if err := c.RemoveSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 2, "after removing an id that is gone")
}

func TestRevisionAddSourceCapDoesNotBump(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Source cap")
	for i := 0; i < casestore.MaxSources; i++ {
		if _, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis", Label: fmt.Sprintf("s%d", i)}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	wantRevision(t, c, int64(casestore.MaxSources), "after filling the sources")
	if _, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis", Label: "one-too-many"}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Fatalf("add over the cap: %v, want ErrCapExceeded", err)
	}
	wantRevision(t, c, int64(casestore.MaxSources), "after a refused add")
}

func TestRevisionSetEnvsRefusalDoesNotBump(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Envs refusal")
	if err := c.SetEnvs(ctx, []string{"Production"}); err != nil {
		t.Fatal(err)
	}
	names := make([]string, casestore.MaxEnvs+1)
	for i := range names {
		names[i] = fmt.Sprintf("env%d", i)
	}
	if err := c.SetEnvs(ctx, names); err == nil {
		t.Fatal("SetEnvs with 33 names returned no error")
	}
	wantRevision(t, c, 1, "after a refused SetEnvs")
}

func TestRevisionCookieKeysBump(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Cookie bump")
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 1, "after the first allowlist (list and key in one call)")
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid"}); err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 1, "after the identical allowlist")
	if _, err := c.SetCookieAllowlist(ctx, []string{"sid", "theme"}); err != nil {
		t.Fatal(err)
	}
	wantRevision(t, c, 2, "after a different allowlist")
	if _, err := c.SetCookieAllowlist(ctx, []string{"a b"}); err == nil {
		t.Fatal("an invalid cookie name returned no error")
	}
	wantRevision(t, c, 2, "after a refused allowlist")
}

func TestRevisionNonExportWritesDoNotBump(t *testing.T) {
	ctx := context.Background()
	c := newRevisionCase(t, "Non export writes")
	mustImport(t, c, "seed", 1)
	base := mustRevision(t, c)
	same := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		wantRevision(t, c, base, "after "+what)
	}
	same("SetWorkspaceBlob", c.SetWorkspaceBlob(ctx, []byte(`{"a":1}`)))
	same("SetLedgerBlob", c.SetLedgerBlob(ctx, []byte(`{"b":2}`)))
	view, err := c.SaveView(ctx, casestore.SavedView{Name: "v", Tool: "filters", Settings: []byte(`{"route":"A.B"}`)})
	same("SaveView", err)
	same("DeleteView", c.DeleteView(ctx, view.ID))
	item, err := c.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "item", Order: casestore.OrderAuto})
	same("PutChecklistItem", err)
	same("DeleteChecklistItem", c.DeleteChecklistItem(ctx, item.ID))
	retry := casestore.RetryItem{Kind: "import", Key: "k1", Label: "a.csv", Reason: "stopped"}
	_, err = c.ReplaceRetries(ctx, []casestore.RetryItem{retry})
	same("ReplaceRetries", err)
	noted, err := c.NoteRetry(ctx, retry)
	same("NoteRetry", err)
	same("RemoveRetry", c.RemoveRetry(ctx, noted.ID))
	_, err = c.AddHealthRun(ctx, casestore.HealthRun{Kind: "sql", Label: "h", Status: "ok", Report: []byte(`{}`)})
	same("AddHealthRun", err)
	key := casestore.DiscoverFileKey{VolumeSerial: 1, FileIndex: 2}
	same("PutDiscoverMark", c.PutDiscoverMark(ctx, key, casestore.DiscoverMark{Offset: 5}))
	same("DeleteDiscoverMark", c.DeleteDiscoverMark(ctx, key))
	same("RecordDiscoverRead", c.RecordDiscoverRead(ctx, casestore.DiscoverRead{At: time.Now(), Detector: "d", Importer: "i", Kind: "file", Display: "x"}))
}

// A write that changes what an export contains must call bumpCaseRevision. The
// tree has no import delete or source update today; if one appears, this test
// fails until its author adds the bump and extends the list of sites.
func TestRevisionEveryExportedWriteHasABump(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v (%d files)", err, len(files))
	}
	banned := []string{"DELETE FROM imports", "DELETE FROM events", "UPDATE case_sources", "UPDATE case_envs"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range banned {
			if strings.Contains(string(b), s) {
				t.Errorf("%s contains %q: add bumpCaseRevision to this write and to WP-65b's list", f, s)
			}
		}
	}
}
