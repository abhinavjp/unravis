package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/discover/evtx"
	"api-audit-explorer/internal/discover/localread"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sealedfile"
)

func sqliteEnv(t *testing.T, mut func(*sqlite.Options)) *caseEnv {
	t.Helper()
	return sqliteEnvAt(t, filepath.Join(t.TempDir(), "cases"), storetest.RandomProtector(), mut)
}

// sqliteEnvAt is sqliteEnv over a given folder and protector, so a second store
// (another window) can open the cases of the first with other options.
func sqliteEnvAt(t *testing.T, root string, prot sealedfile.Protector, mut func(*sqlite.Options)) *caseEnv {
	t.Helper()
	opt := sqlite.Options{Root: root, Protector: prot, MinFreeBytes: -1}
	if mut != nil {
		mut(&opt)
	}
	s, err := sqlite.New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return tagEnv(t, s, &fakeRepository{})
}

// The 413 for a cap says which cap: the file's, or the case's. A cap of neither
// kind keeps the generic reason (PF-B-07).
func TestImportCapReasonsSplitFileAndCase(t *testing.T) {
	rows := toyCSV(toyRow(1, "web-a", "a", 1), toyRow(2, "web-a", "b", 2), toyRow(3, "web-a", "c", 3))

	t.Run("file rows", func(t *testing.T) {
		e := sqliteEnv(t, func(o *sqlite.Options) { o.MaxFileRows = 2 })
		id := e.newCase("cap")
		res := e.upload(commitPath(id), ackFields, &upload{"a.csv", rows})
		if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "file-cap" {
			t.Fatalf("over the file cap = %d %s", res.Code, res.Body)
		}
		if f := failureOf(t, res); f.Error != fileCapText {
			t.Errorf("text = %q", f.Error)
		}
	})
	t.Run("case database", func(t *testing.T) {
		// The case is made by a store with the default caps, then met by a store
		// whose database cap is below what the case already holds.
		root, prot := filepath.Join(t.TempDir(), "cases"), storetest.RandomProtector()
		id := sqliteEnvAt(t, root, prot, nil).newCase("cap")
		e := sqliteEnvAt(t, root, prot, func(o *sqlite.Options) { o.MaxDBBytes = 4096 })
		res := e.upload(commitPath(id), ackFields, &upload{"a.csv", rows})
		if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "case-cap" {
			t.Fatalf("over the case cap = %d %s", res.Code, res.Body)
		}
		if f := failureOf(t, res); f.Error != caseCapText {
			t.Errorf("text = %q", f.Error)
		}
	})
	t.Run("other caps stay generic", func(t *testing.T) {
		e := sqliteEnv(t, func(o *sqlite.Options) { o.MaxTotalBytes = 1 })
		id := e.newCase("cap")
		res := e.upload(commitPath(id), ackFields, &upload{"a.csv", rows})
		if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "cap-exceeded" {
			t.Fatalf("all-cases cap = %d %s", res.Code, res.Body)
		}
	})
}

// A write to a case that an import holds answers a plain 409 with its own
// reason and text, and the case is writable again when the import ends
// (PF-B-06).
func TestWritersGetImportRunning409(t *testing.T) {
	e := sqliteEnv(t, nil)
	id := e.newCase("busy")
	c, err := e.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	release, started, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		close(started)
		<-release
		_, err := io.Copy(io.Discard, r)
		return importer.Report{Importer: "toy"}, err
	}
	go func() {
		_, err := c.Import(context.Background(), casestore.ImportMeta{
			Importer: "toy", FileName: "slow.csv", HostLabel: "web-a", TZRule: "utc-field",
			AckGating: true, AckText: evidence.GatingBanner(),
		}, strings.NewReader("slow body"), run)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the import did not start")
	}

	check := func(name string, res *httptest.ResponseRecorder) {
		t.Helper()
		f := failureOf(t, res)
		if res.Code != http.StatusConflict || f.Reason != "case-import-running" || f.Error != caseImportRunningText {
			t.Errorf("%s = %d %s", name, res.Code, res.Body)
		}
	}
	// A request that blocks on the import fails the test after a bound instead of
	// hanging it; the import is released either way.
	reply := make(chan *httptest.ResponseRecorder, 1)
	asked := func(call func() *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		t.Helper()
		go func() { reply <- call() }()
		select {
		case res := <-reply:
			return res
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatal("a write blocked on the running import instead of answering 409")
			return nil
		}
	}
	start := time.Now()
	check("workspace save", asked(func() *httptest.ResponseRecorder { return e.json(http.MethodPut, workspacePath(id), wsBody) }))
	check("close case", asked(func() *httptest.ResponseRecorder {
		return e.believing(http.MethodPost, "/api/cases/"+id+"/close", "", "open")
	}))
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the refusals took %v, want them at once", d)
	}

	close(release)
	// The held import has no importer rows to store, so it may end in a
	// quarantine; any other error is a real failure.
	if err := <-done; err != nil && !errors.Is(err, importer.ErrQuarantine) {
		t.Errorf("the held import ended with %v", err)
	}
	if res := e.json(http.MethodPut, workspacePath(id), wsBody); res.Code != http.StatusOK {
		t.Fatalf("workspace save after the import = %d %s", res.Code, res.Body)
	}
}

// The error maps of the import and the discovery pull know the new errors too.
func TestImportRunningAndCapErrorsMap(t *testing.T) {
	h := &handler{}
	for _, tc := range []struct {
		err    error
		status int
		reason string
	}{
		{fmt.Errorf("x: %w", casestore.ErrImportRunning), http.StatusConflict, "case-import-running"},
		{fmt.Errorf("x: %w", casestore.ErrFileCapExceeded), http.StatusRequestEntityTooLarge, "file-cap"},
		{fmt.Errorf("x: %w", casestore.ErrCaseCapExceeded), http.StatusRequestEntityTooLarge, "case-cap"},
		{fmt.Errorf("x: %w", casestore.ErrCapExceeded), http.StatusRequestEntityTooLarge, "cap-exceeded"},
	} {
		rec := httptest.NewRecorder()
		h.writeImportError(rec, tc.err, nil, casestore.ImportResult{})
		if rec.Code != tc.status || failureOf(t, rec).Reason != tc.reason {
			t.Errorf("%v = %d %s, want %d %s", tc.err, rec.Code, rec.Body, tc.status, tc.reason)
		}
	}
	rec := httptest.NewRecorder()
	writeWorkspaceError(rec, fmt.Errorf("x: %w", casestore.ErrImportRunning))
	if rec.Code != http.StatusConflict || failureOf(t, rec).Reason != "case-import-running" {
		t.Errorf("workspace = %d %s", rec.Code, rec.Body)
	}
}

// The discovery pull maps the new errors, and the old ones it already knew, to
// the same status and reason as an upload (PF-B-06, PF-B-07).
func TestDiscoverPullFailErrMapsImportErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{"import running", fmt.Errorf("x: %w", casestore.ErrImportRunning), http.StatusConflict, "case-import-running"},
		{"file cap", fmt.Errorf("x: %w", casestore.ErrFileCapExceeded), http.StatusRequestEntityTooLarge, "file-cap"},
		{"case cap", fmt.Errorf("x: %w", casestore.ErrCaseCapExceeded), http.StatusRequestEntityTooLarge, "case-cap"},
		{"generic cap", fmt.Errorf("x: %w", casestore.ErrCapExceeded), http.StatusRequestEntityTooLarge, "cap-exceeded"},
		{"canceled", context.Canceled, http.StatusBadRequest, "canceled"},
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "timeout"},
		{"access denied", localread.ErrAccessDenied, http.StatusForbidden, "needs-elevation"},
		{"evtx access denied", evtx.ErrAccessDenied, http.StatusForbidden, "needs-elevation"},
		{"permission", fs.ErrPermission, http.StatusForbidden, "needs-elevation"},
		{"unsafe path", localread.ErrUnsafePath, http.StatusUnprocessableEntity, "unsafe-path"},
		{"file shrank", localread.ErrFileShrank, http.StatusConflict, "file-changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &discoverPull{w: &captureWriter{}}
			p.failErr(tc.err)
			rec := httptest.NewRecorder()
			rec.Code = p.w.status
			rec.Body.Write(p.w.buf.Bytes())
			if p.w.status != tc.status || failureOf(t, rec).Reason != tc.reason {
				t.Errorf("%v = %d %s, want %d %s", tc.err, p.w.status, p.w.buf.String(), tc.status, tc.reason)
			}
		})
	}
}
