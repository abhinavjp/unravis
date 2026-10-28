package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/sealedfile"
)

const (
	cfsPassA  = "alpha bravo charlie delta"
	cfsPassB  = "echo foxtrot golf hotel"
	cfsMarker = "SENTINEL-server-casefile-4b1e"
)

// cfsFast keeps the case file's Argon2id cheap; the masterkey.dat check on
// export still runs the real cost, once per export.
var cfsFast = masterkey.Params{Time: 1, Memory: 16 << 10, Threads: 1}

// cfsPC is one "PC": its own session folder, case folder and master key.
type cfsPC struct {
	t     *testing.T
	h     http.Handler
	sess  *MasterKeySession
	store *sqlite.Store
	prot  sealedfile.Protector // the DPAPI stand-in
	dir   string
	root  string
	pass  string
	logs  *bytes.Buffer
}

// newCFSPC builds a PC. A non-empty pass sets up the master key and, when
// unlock is true, unlocks it.
func newCFSPC(t *testing.T, pass string, unlock bool) *cfsPC {
	t.Helper()
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	oldParams := caseFileParams
	caseFileParams = cfsFast
	t.Cleanup(func() { caseFileParams = oldParams })

	p := &cfsPC{t: t, pass: pass, dir: t.TempDir(), prot: storetest.RandomProtector(), logs: logs}
	p.root = filepath.Join(t.TempDir(), "cases")
	if _, ok := checkCasePath(p.root); !ok {
		t.Skip("temp directory is not a path the case-file routes accept")
	}
	p.sess = NewMasterKeySession(p.dir)
	store, err := sqlite.New(sqlite.Options{
		Root: p.root, Protector: p.prot, MinFreeBytes: -1,
		Protectors: p.sess.Lookup, NewCaseKind: p.sess.NewCaseKind,
		LegacyCheck: p.sess.LegacyCheck, LegacyDone: p.sess.LegacyDone,
		KeyParams: cfsFast,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	p.store = store
	p.h = New(&fakeRepository{}, testAssets(), testToken, false,
		WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithMasterKey(p.sess),
	)
	if pass != "" {
		p.setKey(unlock)
	}
	return p
}

func (p *cfsPC) setKey(unlock bool) {
	p.t.Helper()
	if err := masterkey.Setup(p.dir, []byte(p.pass)); err != nil {
		p.t.Fatal(err)
	}
	if unlock {
		if res := p.do(http.MethodPost, "/api/master-key/unlock", unlockBody(p.pass)); res.Code != http.StatusOK {
			p.t.Fatalf("unlock = %d %s", res.Code, res.Body)
		}
	}
}

func (p *cfsPC) do(method, path, body string) *httptest.ResponseRecorder {
	return request(p.h, method, path, body, true)
}

func (p *cfsPC) export(id, path, pass string) *httptest.ResponseRecorder {
	return p.do(http.MethodPost, "/api/cases/"+id+"/export", `{"path":`+q(path)+`,"passphrase":`+q(pass)+`}`)
}

func (p *cfsPC) restore(path, pass string) *httptest.ResponseRecorder {
	return p.do(http.MethodPost, "/api/cases/restore", `{"path":`+q(path)+`,"passphrase":`+q(pass)+`}`)
}

// seed creates a case through the API and fills it with n events.
func (p *cfsPC) seed(title string, n int) string {
	p.t.Helper()
	return p.seedBody(title, n, `{"title":`+q(title)+`}`)
}

// seedBody is seed with the create request named (a case with its own key, say).
func (p *cfsPC) seedBody(title string, n int, body string) string {
	p.t.Helper()
	res := p.do(http.MethodPost, "/api/cases", body)
	if res.Code != http.StatusCreated {
		p.t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	var dto caseDTO
	decodeBody(p.t, res, &dto)
	c, err := p.store.Open(context.Background(), casestore.CaseID(dto.ID))
	if err != nil {
		p.t.Fatal(err)
	}
	events := make([]evidence.Event, n)
	base := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	for i := range events {
		v := float64(i)
		line := int64(i + 2)
		events[i] = evidence.Event{
			EvidenceID: evidence.EventID(dto.ID, "test.csv", "first", line, 0),
			Source:     "test.csv", Kind: "cpu", Host: "web-a",
			Start: base.Add(time.Duration(i) * time.Second), Value: &v, Unit: "percent", Line: line,
			TZRule: "utc-field", Attrs: map[string]string{"note": cfsMarker},
		}
		events[i].End = events[i].Start.Add(time.Second)
	}
	run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: "test.csv", KnownGaps: []string{"synthetic"}}
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
	meta := casestore.ImportMeta{Importer: "test.csv", FileName: "first.csv", HostLabel: "web-a", TZRule: "utc-field",
		AckGating: true, AckText: "Read the gating note for this import."}
	if _, err := c.Import(context.Background(), meta, strings.NewReader("synthetic body\n"), run); err != nil {
		p.t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		p.t.Fatal(err)
	}
	return dto.ID
}

func (p *cfsPC) eventCount(id string) int {
	p.t.Helper()
	c, err := p.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		p.t.Fatal(err)
	}
	defer c.Close()
	evs, err := c.Events(context.Background(), casestore.EventQuery{Limit: 50000})
	if err != nil {
		p.t.Fatal(err)
	}
	return len(evs)
}

func (p *cfsPC) caseIDs() map[string]bool {
	p.t.Helper()
	list, err := p.store.List(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range list {
		out[string(c.ID)] = true
	}
	return out
}

func (p *cfsPC) kind(id string) string {
	p.t.Helper()
	k, err := p.store.CaseKind(casestore.CaseID(id))
	if err != nil {
		p.t.Fatal(err)
	}
	return k
}

func cfsReason(t *testing.T, res *httptest.ResponseRecorder, code int, reason string) caseFailure {
	t.Helper()
	if res.Code != code {
		t.Fatalf("status = %d %s, want %d %s", res.Code, res.Body, code, reason)
	}
	var f caseFailure
	if err := json.Unmarshal(res.Body.Bytes(), &f); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	if f.Reason != reason {
		t.Fatalf("reason = %q (%s), want %q", f.Reason, res.Body, reason)
	}
	return f
}

// t0Dir is a fresh folder outside every case folder to export into.
func t0Dir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if _, ok := checkCasePath(d); !ok {
		t.Skip("temp directory is not a path the case-file routes accept")
	}
	return d
}

/* --------------------------------------------------------------- tests */

func TestCaseFileRoutesFollowTheKeyState(t *testing.T) {
	out := t0Dir(t)
	file := filepath.Join(out, "x"+casestore.CaseFileExt)
	nid, err := casestore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	id := string(nid)

	// No master-key session at all.
	bare := New(&fakeRepository{}, testAssets(), testToken, false)
	cfsReason(t, request(bare, http.MethodPost, "/api/cases/restore", `{"path":`+q(file)+`,"passphrase":"x"}`, true), http.StatusServiceUnavailable, "master-key-unavailable")

	// Key state none: both routes say to set a key first.
	none := newCFSPC(t, "", false)
	cfsReason(t, none.restore(file, "x"), http.StatusConflict, "no-master-key")
	cfsReason(t, none.export(id, file, "x"), http.StatusConflict, "no-master-key")

	// Locked: the gate answers 423 before any handler runs.
	locked := newCFSPC(t, cfsPassA, false)
	for _, res := range []*httptest.ResponseRecorder{locked.restore(file, cfsPassA), locked.export(id, file, cfsPassA)} {
		cfsReason(t, res, http.StatusLocked, "locked")
	}
	// And locking an unlocked PC closes the routes again.
	open := newCFSPC(t, cfsPassA, true)
	if res := open.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d", res.Code)
	}
	cfsReason(t, open.restore(file, cfsPassA), http.StatusLocked, "locked")
}

func TestCaseFileBadBodiesAndPaths(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("paths", 3)
	out := t0Dir(t)
	okFile := filepath.Join(out, "ok"+casestore.CaseFileExt)
	url := "/api/cases/" + id + "/export"

	// Body shapes.
	req := func(ct, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
		r.Host = "127.0.0.1:8765"
		r.Header.Set("X-Audit-Token", testToken)
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		pc.h.ServeHTTP(w, r)
		return w
	}
	cfsReason(t, req("text/plain", `{}`), http.StatusUnsupportedMediaType, "bad-content-type")
	cfsReason(t, req("application/json", `not json`), http.StatusBadRequest, "bad-request")
	cfsReason(t, req("application/json", `{"path":"x","extra":1}`), http.StatusBadRequest, "bad-request")
	cfsReason(t, req("application/json", `{"path":5}`), http.StatusBadRequest, "bad-request")
	cfsReason(t, req("application/json", `{"passphrase":5}`), http.StatusBadRequest, "bad-request")
	cfsReason(t, req("application/json", `{"path":"`+strings.Repeat("a", maxCaseFileBody)+`"}`), http.StatusRequestEntityTooLarge, "too-large")
	cfsReason(t, req("application/json", `{"path":"`+strings.Repeat("a", 600)+`","passphrase":"`+cfsPassA+`"}`), http.StatusBadRequest, "path-too-long")
	missing, err := casestore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	cfsReason(t, pc.do(http.MethodPost, "/api/cases/not-an-id/export", `{}`), http.StatusBadRequest, "bad-case-id")
	cfsReason(t, pc.export(string(missing), okFile, cfsPassA), http.StatusNotFound, "case-not-found")

	// Paths. A path with a bad shape is refused before the passphrase is tried.
	existingFile := filepath.Join(out, "plain.txt")
	if err := os.WriteFile(existingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	shapeBad := []string{
		"",
		"relative" + casestore.CaseFileExt,
		`\host\share\x` + casestore.CaseFileExt,
		`\?\C:\x` + casestore.CaseFileExt,
		out + `\..\x` + casestore.CaseFileExt,
		filepath.Join(out, "x"+casestore.CaseFileExt) + ":stream",
	}
	// These need the right passphrase first: the folder is only probed for
	// someone who holds the key.
	placeBad := []string{
		filepath.Join(out, "missing-dir", "x"+casestore.CaseFileExt),
		filepath.Join(out, "missing-folder"),
		existingFile,
		filepath.Join(existingFile, "x"+casestore.CaseFileExt),
		filepath.Join(pc.root, "x"+casestore.CaseFileExt),
		pc.root,
	}
	for i, p := range append(append([]string(nil), shapeBad...), placeBad...) {
		pass := "wrong passphrase, never evaluated"
		if i >= len(shapeBad) {
			pass = cfsPassA
		}
		res := pc.export(id, p, pass)
		f := cfsReason(t, res, http.StatusBadRequest, "bad-path")
		if strings.Contains(res.Body.String(), "never evaluated") || (p != "" && strings.Contains(f.Error, filepath.Base(p))) {
			t.Errorf("bad-path answer echoes input for %q: %s", p, res.Body)
		}
	}
	if _, err := os.Stat(okFile); err == nil {
		t.Fatal("a refused export wrote a file")
	}

	// Restore wants an existing file named *.unravis-case.
	for _, p := range []string{"", "rel" + casestore.CaseFileExt, existingFile, `\\h\s\x` + casestore.CaseFileExt, out + `\..\x` + casestore.CaseFileExt} {
		cfsReason(t, pc.restore(p, cfsPassA), http.StatusBadRequest, "bad-path")
	}
}

func TestCaseFileExportWrongPassphraseIsRefusedAndThrottled(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("throttle", 3)
	out := t0Dir(t)

	res := pc.export(id, out, cfsPassB)
	f := cfsReason(t, res, http.StatusUnauthorized, "wrong-key")
	if strings.Contains(res.Body.String(), cfsPassB) || strings.Contains(f.Error, out) {
		t.Fatalf("answer echoes input: %s", res.Body)
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Fatalf("a refused export left %d entries", len(ents))
	}
	// Empty and over-long passphrases are the same answer and cost no hashing.
	cfsReason(t, pc.export(id, out, ""), http.StatusUnauthorized, "wrong-key")
	cfsReason(t, pc.export(id, out, strings.Repeat("p", maxPassphraseBytes+1)), http.StatusUnauthorized, "wrong-key")
	for i := 0; i < 2; i++ {
		cfsReason(t, pc.export(id, out, ""), http.StatusUnauthorized, "wrong-key")
	}
	// Five failures so far: the sixth try is not evaluated.
	res = pc.export(id, out, cfsPassA)
	cfsReason(t, res, http.StatusTooManyRequests, "throttled")
	if res.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After on 429")
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Fatalf("a throttled export wrote %d entries", len(ents))
	}
}

func TestCaseFileExportThenRestoreOnAnotherPC(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	dst := newCFSPC(t, cfsPassB, true) // a different PC with a different master key
	id := src.seed("Checkout slowdown", 40)
	before := src.eventCount(id)
	if before != 40 {
		t.Fatalf("seeded %d events", before)
	}
	out := t0Dir(t)

	// Export into a folder: the file is named by the case code.
	res := src.export(id, out, cfsPassA)
	if res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	var ex caseFileExportReply
	decodeBody(t, res, &ex)
	wantFile := filepath.Join(out, id+casestore.CaseFileExt)
	if ex.Path != wantFile || ex.Name != id+casestore.CaseFileExt || ex.Bytes <= 0 {
		t.Fatalf("export reply = %+v, want path %s", ex, wantFile)
	}
	var raw map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &raw); err != nil || len(raw) != 3 {
		t.Fatalf("export reply fields = %v", raw)
	}
	fi, err := os.Stat(wantFile)
	if err != nil || fi.Size() != ex.Bytes {
		t.Fatalf("file %v size %v vs %d", err, fi, ex.Bytes)
	}
	// Nothing readable in the file, and no temp files left beside it.
	data, err := os.ReadFile(wantFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{cfsMarker, "Checkout slowdown", cfsPassA, "SQLite format 3", "web-a"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("case file contains plaintext %q", secret)
		}
	}
	if ents, _ := os.ReadDir(out); len(ents) != 1 {
		t.Fatalf("export folder has %d entries, want 1", len(ents))
	}
	// The source case is untouched and still open for work.
	if got := src.eventCount(id); got != before {
		t.Fatalf("source has %d events after export, want %d", got, before)
	}

	// Restore on the other PC needs the source PC's passphrase, not its own.
	cfsReason(t, dst.restore(wantFile, cfsPassB), http.StatusUnprocessableEntity, "bad-case-file")
	if len(dst.caseIDs()) != 0 {
		t.Fatal("a refused restore created a case")
	}
	res = dst.restore(wantFile, cfsPassA)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore = %d %s", res.Code, res.Body)
	}
	var got caseDTO
	decodeBody(t, res, &got)
	if got.ID == "" || got.ID == id || got.Title != "Checkout slowdown" {
		t.Fatalf("restored = %+v (source %s)", got, id)
	}
	if n := dst.eventCount(got.ID); n != before {
		t.Fatalf("restored case has %d events, want %d", n, before)
	}
	if k := dst.kind(got.ID); k != casestore.KindMasterKey {
		t.Fatalf("restored case protector = %q, want the destination master key", k)
	}
	// Restoring the same file again makes another new case, never an overwrite.
	res = dst.restore(wantFile, cfsPassA)
	if res.Code != http.StatusCreated {
		t.Fatalf("second restore = %d %s", res.Code, res.Body)
	}
	var again caseDTO
	decodeBody(t, res, &again)
	if again.ID == got.ID || again.ID == id || len(dst.caseIDs()) != 2 {
		t.Fatalf("second restore = %+v, cases = %v", again, dst.caseIDs())
	}
	if n := dst.eventCount(got.ID); n != before {
		t.Fatalf("first restored case has %d events after the second restore", n)
	}
	// The same PC can restore its own file as a copy.
	res = src.restore(wantFile, cfsPassA)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore on the source PC = %d %s", res.Code, res.Body)
	}
	if len(src.caseIDs()) != 2 || src.eventCount(id) != before {
		t.Fatalf("source PC cases = %v", src.caseIDs())
	}

	// Neither passphrase reached a response body or the log.
	for _, pc := range []*cfsPC{src, dst} {
		if s := pc.logs.String(); strings.Contains(s, cfsPassA) || strings.Contains(s, cfsPassB) {
			t.Fatalf("passphrase in log: %s", s)
		}
	}

	// Export to an explicit file name works too, and never overwrites.
	named := filepath.Join(out, "handover"+casestore.CaseFileExt)
	if res := src.export(id, named, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("named export = %d %s", res.Code, res.Body)
	}
	before2, _ := os.ReadFile(named)
	for _, target := range []string{named, out} { // out now holds <id>.unravis-case too
		f := cfsReason(t, src.export(id, target, cfsPassA), http.StatusConflict, "file-exists")
		if strings.Contains(f.Error, out) {
			t.Fatalf("answer echoes a path: %s", f.Error)
		}
	}
	if after, _ := os.ReadFile(named); !bytes.Equal(before2, after) {
		t.Fatal("a refused export changed the existing file")
	}
}

func TestCaseFileRestoreFailuresAreOneAnswer(t *testing.T) {
	src := newCFSPC(t, cfsPassA, true)
	id := src.seed("failures", 5)
	out := t0Dir(t)
	good := filepath.Join(out, "good"+casestore.CaseFileExt)
	if res := src.export(id, good, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(out, name+casestore.CaseFileExt)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	flip := func(i int) []byte {
		c := append([]byte(nil), data...)
		c[i] ^= 0x40
		return c
	}
	asDir := filepath.Join(out, "dir"+casestore.CaseFileExt)
	if err := os.Mkdir(asDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		path, pass string
	}{
		"wrong passphrase": {good, cfsPassB},
		"case changed":     {good, strings.ToUpper(cfsPassA)},
		"empty passphrase": {good, ""},
		"huge passphrase":  {good, strings.Repeat("p", maxPassphraseBytes+1)},
		"flipped magic":    {write("magic", flip(0)), cfsPassA},
		"flipped header":   {write("header", flip(60)), cfsPassA},
		"flipped database": {write("db", flip(len(data)-5)), cfsPassA},
		"truncated":        {write("short", data[:len(data)-9]), cfsPassA},
		"trailing bytes":   {write("long", append(append([]byte(nil), data...), 0)), cfsPassA},
		"empty file":       {write("empty", nil), cfsPassA},
		"missing file":     {filepath.Join(out, "missing"+casestore.CaseFileExt), cfsPassA},
		"a folder":         {asDir, cfsPassA},
	}
	var first string
	for name, c := range cases {
		// A fresh destination per attempt keeps the throttle out of the comparison.
		dst := newCFSPC(t, cfsPassB, true)
		res := dst.restore(c.path, c.pass)
		cfsReason(t, res, http.StatusUnprocessableEntity, "bad-case-file")
		var f caseFailure
		decodeBody(t, res, &f)
		if f.Error != casestore.CaseFileMessage {
			t.Errorf("%s: message = %q", name, f.Error)
		}
		body := res.Body.String()
		if first == "" {
			first = body
		} else if body != first {
			t.Errorf("%s: body %s differs from %s", name, body, first)
		}
		for _, leak := range []string{c.path, filepath.Base(c.path), c.pass} {
			if leak != "" && strings.Contains(body, leak) {
				t.Errorf("%s: body echoes %q", name, leak)
			}
		}
		if n := len(dst.caseIDs()); n != 0 {
			t.Errorf("%s: %d case(s) created", name, n)
		}
		ents, _ := os.ReadDir(dst.root)
		for _, e := range ents {
			t.Errorf("%s: left %s in the case folder", name, e.Name())
		}
	}

	// Repeated failures are throttled like any passphrase guess.
	dst := newCFSPC(t, cfsPassB, true)
	for i := 0; i < 5; i++ {
		cfsReason(t, dst.restore(good, ""), http.StatusUnprocessableEntity, "bad-case-file")
	}
	cfsReason(t, dst.restore(good, cfsPassA), http.StatusTooManyRequests, "throttled")
	if len(dst.caseIDs()) != 0 {
		t.Fatal("a throttled restore created a case")
	}
}

func TestCaseFileFromADPAPISealedCase(t *testing.T) {
	// A case made before any master key existed is sealed with DPAPI. Exporting
	// it once a key is set reads it through the DPAPI protector.
	pc := newCFSPC(t, "", false)
	pc.pass = cfsPassA
	id := pc.seed("old case", 6)
	if k := pc.kind(id); k == casestore.KindMasterKey {
		t.Fatalf("seeded case is already %q", k)
	}
	pc.setKey(true)
	out := t0Dir(t)

	old := caseFileDPAPI
	t.Cleanup(func() { caseFileDPAPI = old })
	caseFileDPAPI = func() (sealedfile.Protector, error) { return nil, sealedfile.ErrUnavailable }
	cfsReason(t, pc.export(id, out, cfsPassA), http.StatusLocked, "locked")
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Fatalf("failed export left %d entries", len(ents))
	}

	caseFileDPAPI = func() (sealedfile.Protector, error) { return pc.prot, nil }
	if res := pc.export(id, out, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	other := newCFSPC(t, cfsPassB, true)
	res := other.restore(filepath.Join(out, id+casestore.CaseFileExt), cfsPassA)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore = %d %s", res.Code, res.Body)
	}
	var got caseDTO
	decodeBody(t, res, &got)
	if other.eventCount(got.ID) != 6 || other.kind(got.ID) != casestore.KindMasterKey {
		t.Fatalf("restored %+v kind %q", got, other.kind(got.ID))
	}
}

func TestCaseFileOneTransferAtATime(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("busy", 3)
	out := t0Dir(t)
	st := pc.h.(*handler).caseFiles()
	st.busy.Store(true)
	cfsReason(t, pc.export(id, out, cfsPassA), http.StatusConflict, "busy")
	cfsReason(t, pc.restore(filepath.Join(out, "x"+casestore.CaseFileExt), cfsPassA), http.StatusConflict, "busy")
	st.busy.Store(false)
	if res := pc.export(id, out, cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("export after busy = %d %s", res.Code, res.Body)
	}
}

func TestCaseFileOnlyAcceptsPost(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("methods", 1)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, p := range []string{"/api/cases/restore", "/api/cases/" + id + "/export"} {
			if res := pc.do(m, p, ""); res.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d", m, p, res.Code)
			}
		}
	}
	// "restore" is not a case id for the plain case routes.
	if res := pc.do(http.MethodGet, "/api/cases/restore", ""); res.Code == http.StatusOK {
		t.Fatal("GET /api/cases/restore answered 200")
	}
	if res := request(pc.h, http.MethodPost, "/api/cases/restore", `{}`, false); res.Code != http.StatusUnauthorized {
		t.Fatalf("restore without token = %d", res.Code)
	}
}
