package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
)

// Synthetic canaries for the confirm snapshot tests: none may reach a 409 body or
// a log line.
const (
	snapCanarySite = "CANARY_SITE_65"
	snapCanaryFile = "CANARY_FILE_65.log"
	snapClientIP   = "192.0.2.77"
)

// logBuf gives the env a handler that logs into the returned buffer. newIntEnv
// builds its handler without a logger.
func logBuf(e *intEnv) *bytes.Buffer {
	var buf bytes.Buffer
	e.h = New(intRepo(), testAssets(), testToken, false,
		WithCases(e.store, mustHosts(e.t, intHostsDoc), all.Registry()), WithLedger(e.ledger), WithLogger(bufLogger(&buf)))
	return &buf
}

// snapImport adds one iis-w3c import with one event to the case through a handle
// of its own, as a writer outside the HTTP handlers would.
func snapImport(t *testing.T, e *intEnv, id, fileName, nonce string, attrs map[string]string) {
	t.Helper()
	ctx := context.Background()
	c, err := e.store.Open(ctx, casestore.CaseID(id))
	if err != nil {
		t.Error(err)
		return
	}
	defer c.Close()
	start := time.Date(2001, 3, 17, 10, 0, 0, 0, time.UTC)
	ev := evidence.Event{
		EvidenceID: evidence.EventID(id, "iis-w3c", nonce, 2, 0),
		Source:     "iis-w3c", Kind: "request", Host: "web-a",
		Start: start, End: start.Add(time.Second), Line: 2, TZRule: "utc-field", Attrs: attrs,
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "iis-w3c", KnownGaps: []string{"synthetic test data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		rep.Rows++
		if err := emit(ev); err != nil {
			return rep, err
		}
		rep.Emitted++
		return rep, nil
	}
	meta := casestore.ImportMeta{
		Importer: "iis-w3c", FileName: fileName, HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: "Read the gating note for this import.",
	}
	if _, err := c.Import(ctx, meta, strings.NewReader("synthetic body "+nonce+"\n"), run); err != nil {
		t.Error(err)
	}
}

// snapCase makes a case holding the canary iis-w3c event and file name.
func snapCase(t *testing.T) (*intEnv, string) {
	t.Helper()
	e := newIntEnv(t, intRepo())
	id := e.newCase("Snapshot case")
	snapImport(t, e, id, snapCanaryFile, "snap-nonce-1", map[string]string{
		"site": snapCanarySite, "client-ip": snapClientIP, "referer": "https://ref.example.test/x",
		"cookie:sid": "ck-sentinel-65", "status": "200", "method": "GET",
	})
	if t.Failed() {
		t.FailNow()
	}
	return e, id
}

func snapPreview(t *testing.T, e *intEnv, id string, scrub bool) string {
	t.Helper()
	body := `{"caseId":"` + id + `"`
	if !scrub {
		body += `,"scrubSensitive":false`
	}
	res := e.json(http.MethodPost, "/api/export/preview", body+`}`)
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	var pv exportPreviewResponse
	decodeBody(t, res, &pv)
	return pv.PreviewID
}

func snapConfirmBody(id, previewID string, scrub bool) string {
	body := `{"previewId":"` + previewID + `","caseId":"` + id + `"`
	if !scrub {
		body += `,"scrubSensitive":false`
	}
	return body + `}`
}

// setSnapshotHook installs fn for the "after-reads" stage and restores nil.
func setSnapshotHook(t *testing.T, fn func()) {
	t.Helper()
	if caseExportSnapshotHook != nil {
		t.Fatal("snapshot hook is set before the test")
	}
	caseExportSnapshotHook = func(stage string) {
		if stage == "after-reads" {
			fn()
		}
	}
	t.Cleanup(func() { caseExportSnapshotHook = nil })
}

func wantChanged409(t *testing.T, what string, code int, body string) {
	t.Helper()
	if code != http.StatusConflict {
		t.Fatalf("%s: status = %d, want 409 (%s)", what, code, body)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil || len(m) != 1 || m["error"] != exportChangedMessage {
		t.Fatalf("%s: body = %s, want only the fixed error text", what, body)
	}
	for _, banned := range []string{"content", "sha256", "CANARY_"} {
		if strings.Contains(body, banned) {
			t.Fatalf("%s: 409 body holds %q: %s", what, banned, body)
		}
	}
}

// confirmWriteDuringReads: SetEnvs lands between the reads and the build.
func confirmWriteDuringReads(t *testing.T) (body string, logs string) {
	e, id := snapCase(t)
	buf := logBuf(e)
	pv := snapPreview(t, e, id, true)
	before := revisionOf(t, e.caseEnv, id)
	done := false
	setSnapshotHook(t, func() {
		if done {
			return
		}
		done = true
		c, err := e.store.Open(context.Background(), casestore.CaseID(id))
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		tg, ok := c.(casestore.Tagger)
		if !ok {
			t.Error("case has no SetEnvs")
			return
		}
		if err := tg.SetEnvs(context.Background(), []string{"Production-65"}); err != nil {
			t.Error(err)
		}
	})
	res := e.json(http.MethodPost, "/api/export/confirm", snapConfirmBody(id, pv, true))
	caseExportSnapshotHook = nil
	if !done {
		t.Fatal("the snapshot hook never ran")
	}
	if after := revisionOf(t, e.caseEnv, id); after <= before {
		t.Fatalf("revision %d did not rise from %d", after, before)
	}
	wantChanged409(t, "write during reads", res.Code, res.Body.String())
	return res.Body.String(), buf.String()
}

// confirmImportDuringReads: an import lands between the reads and the build.
func confirmImportDuringReads(t *testing.T) string {
	e, id := snapCase(t)
	pv := snapPreview(t, e, id, true)
	before := revisionOf(t, e.caseEnv, id)
	done := false
	setSnapshotHook(t, func() {
		if done {
			return
		}
		done = true
		snapImport(t, e, id, "snap-fresh-65.log", "snap-nonce-2", map[string]string{"status": "500"})
	})
	res := e.json(http.MethodPost, "/api/export/confirm", snapConfirmBody(id, pv, true))
	caseExportSnapshotHook = nil
	if !done {
		t.Fatal("the snapshot hook never ran")
	}
	wantRevisionDelta(t, e.caseEnv, id, before, 1, "after the import during the reads")
	wantChanged409(t, "import during reads", res.Code, res.Body.String())
	return res.Body.String()
}

// confirmAfterImport: the case changed between the preview and the confirm.
func confirmAfterImport(t *testing.T) string {
	e, id := snapCase(t)
	pv := snapPreview(t, e, id, true)
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	res := e.json(http.MethodPost, "/api/export/confirm", snapConfirmBody(id, pv, true))
	wantChanged409(t, "import before start", res.Code, res.Body.String())
	return res.Body.String()
}

func TestCaseConfirmWriteDuringReadsIs409(t *testing.T) {
	_, logs := confirmWriteDuringReads(t)
	if strings.Contains(logs, "export_finished") {
		t.Fatalf("a 409 wrote an export_finished line: %s", logs)
	}
}

func TestCaseConfirmImportDuringReadsIs409(t *testing.T) {
	confirmImportDuringReads(t)
}

func TestCaseConfirmReplayOnUnchangedCaseSucceeds(t *testing.T) {
	e, id := snapCase(t)
	pv := snapPreview(t, e, id, true)
	for i := 0; i < 2; i++ {
		res := e.json(http.MethodPost, "/api/export/confirm", snapConfirmBody(id, pv, true))
		if res.Code != http.StatusOK {
			t.Fatalf("confirm %d = %d %s", i, res.Code, res.Body)
		}
		var cf exportConfirmResponse
		decodeBody(t, res, &cf)
		sum := sha256.Sum256([]byte(cf.Content))
		if cf.Content == "" || cf.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("confirm %d: sha256 %q does not match its content", i, cf.SHA256)
		}
	}
}

func TestCaseConfirmParallelSucceeds(t *testing.T) {
	e, id := snapCase(t)
	pv := snapPreview(t, e, id, true)
	const n = 8
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := e.json(http.MethodPost, "/api/export/confirm", snapConfirmBody(id, pv, true))
			codes[i], bodies[i] = res.Code, res.Body.String()
		}(i)
	}
	wg.Wait()
	for i := range codes {
		if codes[i] != http.StatusOK {
			t.Errorf("parallel confirm %d = %d %.200s", i, codes[i], bodies[i])
		}
	}
}

func TestCaseConfirmAfterImportBeforeStartIs409(t *testing.T) {
	confirmAfterImport(t)
}

func TestCaseConfirmHookIsNilInProduction(t *testing.T) {
	if caseExportSnapshotHook != nil {
		t.Fatal("caseExportSnapshotHook is set at test start")
	}
	assign := regexp.MustCompile(`caseExportSnapshotHook\s*(:=|=[^=])`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if assign.Match(src) {
			t.Errorf("%s assigns caseExportSnapshotHook; only tests may", name)
		}
	}
}

func TestCaseConfirmLogsFinishedWithSizeOnly(t *testing.T) {
	e, id := snapCase(t)
	buf := logBuf(e)
	pv := snapPreview(t, e, id, false)
	res := e.json(http.MethodPost, "/api/export/confirm", snapConfirmBody(id, pv, false))
	if res.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", res.Code, res.Body)
	}
	var cf exportConfirmResponse
	decodeBody(t, res, &cf)
	if !strings.Contains(cf.Content, snapClientIP) {
		t.Fatalf("scrub-off content lacks the sensitive attribute; the test does not exercise it")
	}
	want := len(cf.Content) + len(cf.EventsCSV)

	finished := func(logs string) []string {
		var out []string
		for _, line := range strings.Split(logs, "\n") {
			if strings.Contains(line, "msg=export_finished") {
				out = append(out, line)
			}
		}
		return out
	}
	lines := finished(buf.String())
	if len(lines) != 1 {
		t.Fatalf("export_finished lines = %d, want 1: %s", len(lines), buf.String())
	}
	allowed := map[string]bool{"time": true, "level": true, "msg": true, "req_id": true, "kind": true, "size": true}
	fields := map[string]string{}
	for _, f := range strings.Fields(lines[0]) {
		k, v, ok := strings.Cut(f, "=")
		if !ok || !allowed[k] {
			t.Errorf("unexpected field %q in %s", f, lines[0])
		}
		fields[k] = v
	}
	if fields["kind"] != "evidence" || fields["size"] != itoa(want) {
		t.Errorf("kind=%q size=%q, want evidence and %d: %s", fields["kind"], fields["size"], want, lines[0])
	}
	logs := buf.String()
	for _, banned := range []string{pv, cf.SHA256, "client-ip", "referer", "cookie:", "CANARY_", "192.0.2."} {
		if strings.Contains(logs, banned) {
			t.Errorf("log text holds %q: %s", banned, logs)
		}
	}

	// The case changes; the old id now answers 409 and logs no second finish.
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	res = e.json(http.MethodPost, "/api/export/confirm", snapConfirmBody(id, pv, false))
	wantChanged409(t, "stale id", res.Code, res.Body.String())
	if n := len(finished(buf.String())); n != 1 {
		t.Errorf("export_finished lines after the 409 = %d, want 1", n)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestCaseConfirm409LeaksNothing(t *testing.T) {
	digits := regexp.MustCompile(`[0-9]{10,}`)
	write, _ := confirmWriteDuringReads(t)
	bodies := map[string]string{
		"write during reads":  write,
		"import during reads": confirmImportDuringReads(t),
		"after import":        confirmAfterImport(t),
	}
	for name, body := range bodies {
		if strings.Contains(body, "CANARY_") || strings.Contains(body, ".log") || digits.MatchString(body) {
			t.Errorf("%s: 409 body leaks: %s", name, body)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(body), &m); err != nil || len(m) != 1 {
			t.Errorf("%s: body is not JSON with one key: %s", name, body)
		} else if _, ok := m["error"]; !ok {
			t.Errorf("%s: body lacks the error key: %s", name, body)
		}
	}
}
