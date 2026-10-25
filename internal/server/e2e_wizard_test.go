package server

// TSK-21 item 1: the cold-start wizard end to end, over the HTTP handler.
//
// The host is the discover package's fake (the same seam discover_test.go
// uses); the IIS and log4net fixtures are written into a t.TempDir() folder and
// loaded from there into that fake host, so nothing here needs real IIS, Mongo,
// Redis or SQL. The case store is a real SQLite store with a real master key,
// so the export and restore steps move real sealed files.
//
// Everything is synthetic. Helper names start with e2e to stay clear of the
// other test files in the package.

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
	"api-audit-explorer/internal/masterkey"
)

const (
	e2ePass  = "violet harbor lantern maple"
	e2eTitle = "E2E Checkout slowdown"
	// e2eLogMarker is in every fixture log line; it must never reach a scan answer.
	e2eLogMarker = "Synthetic.Api.OrdersController"

	e2eSiteA = `D:\Sites\web-a`
	e2eSiteB = `D:\Sites\web-b`
	e2eIISB  = `D:\Logs\portal-b\IIS`
)

const e2eLog4netConfig = `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <log4net>
    <appender name="Rolling" type="log4net.Appender.RollingFileAppender">
      <file value="logs\app.log" />
      <datePattern value="'.'yyyyMMdd" />
      <layout type="log4net.Layout.PatternLayout"><conversionPattern value="%date [%thread] %-5level %logger - %message%newline" /></layout>
    </appender>
    <root><level value="INFO" /><appender-ref ref="Rolling" /></root>
  </log4net>
</configuration>`

func e2eLog4Line(day, sec int) string {
	return fmt.Sprintf("2001-09-%02d 14:02:%02d,950 [12] INFO  %s - Request handled order-%d\n", day, sec, e2eLogMarker, sec)
}

// e2eFix is a server over a real SQLite case store and a real master key, with
// the fixture host of newDiscFix plus a log4net app, a second site whose IIS
// log is not an IIS log, and a second log4net app whose log is not a log4net log.
type e2eFix struct {
	*discFix
	sess   *MasterKeySession
	store  *sqlite.Store
	mkDir  string
	root   string
	fixDir string
	logs   *bytes.Buffer
}

func newE2E(t *testing.T) *e2eFix {
	t.Helper()
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	oldParams := caseFileParams
	caseFileParams = cfsFast
	t.Cleanup(func() { caseFileParams = oldParams })

	x := &e2eFix{logs: logs}
	x.mkDir = t.TempDir()
	x.root = filepath.Join(t.TempDir(), "cases")
	if _, ok := checkCasePath(x.root); !ok {
		t.Skip("temp directory is not a path the case-file routes accept")
	}
	x.fixDir = t.TempDir()
	x.sess = NewMasterKeySession(x.mkDir)
	store, err := sqlite.New(sqlite.Options{
		Root: x.root, Protector: storetest.RandomProtector(), MinFreeBytes: -1,
		Protectors: x.sess.Lookup, NewCaseKind: x.sess.NewCaseKind,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	x.store = store
	x.discFix = newDiscFix(t, nil,
		WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(all.Importers()...)),
		WithMasterKey(x.sess),
	)
	x.addFixtures()
	if err := masterkey.Setup(x.mkDir, []byte(e2ePass)); err != nil {
		t.Fatal(err)
	}
	if res := x.do(http.MethodPost, "/api/master-key/unlock", unlockBody(e2ePass)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	return x
}

func (x *e2eFix) do(method, path, body string) *httptest.ResponseRecorder {
	return x.e.json(method, path, body)
}

// put writes a fixture file into the temp folder, reads it back and places it
// in the fake host at the Windows path.
func (x *e2eFix) put(win, content string, mod time.Time) {
	x.t.Helper()
	local := filepath.Join(x.fixDir, strings.NewReplacer(":", "", `\`, "_").Replace(win))
	if err := os.WriteFile(local, []byte(content), 0o600); err != nil {
		x.t.Fatal(err)
	}
	b, err := os.ReadFile(local)
	if err != nil {
		x.t.Fatal(err)
	}
	x.fe.FS.AddFile(win, int64(len(b)), mod, b)
	x.op.put(win, string(b), mod)
}

func (x *e2eFix) addFixtures() {
	// Two IIS sites: web-a keeps its readable logs (from newDiscFix); web-b's
	// "log" is binary junk that merely carries an IIS file name.
	xml := discAppHostXML(
		discSiteXML("1", e2eSiteA, `<logFile directory="`+discLogDir+`" />`) +
			discSiteXML("2", e2eSiteB, `<logFile directory="`+e2eIISB+`" />`))
	x.fe.FS.AddFile(discAppHost, int64(len(xml)), discDay, []byte(xml))
	x.put(e2eIISB+`\W3SVC2\u_ex010911.log`, "\x00\x01\x02\xff\xfe junk \x00\x00 not a log \x03\x04", discDay)

	// web-a: a log4net app with a rolled file (readable).
	x.put(e2eSiteA+`\web.config`, e2eLog4netConfig, discDay)
	x.put(e2eSiteA+`\logs\app.log.20010910`, e2eLog4Line(10, 1)+e2eLog4Line(10, 2), discDay.AddDate(0, 0, -1))
	x.put(e2eSiteA+`\logs\app.log`, e2eLog4Line(11, 3)+e2eLog4Line(11, 4)+e2eLog4Line(11, 5), discDay)

	// web-b: a log4net app whose log file is binary junk.
	x.put(e2eSiteB+`\web.config`, e2eLog4netConfig, discDay)
	x.put(e2eSiteB+`\logs\app.log`, "\x00\x01\x02\xff\xfe not a log4net file \x00\x00\x03", discDay)
}

/* --------------------------------------------------------------- helpers */

func (x *e2eFix) caseID(title string) string {
	x.t.Helper()
	res := x.do(http.MethodPost, "/api/cases", `{"title":`+q(title)+`}`)
	if res.Code != http.StatusCreated {
		x.t.Fatalf("create case = %d %s", res.Code, res.Body)
	}
	var dto caseDTO
	decodeBody(x.t, res, &dto)
	return dto.ID
}

// e2eScan is one finished scan with its findings sorted into the groups the
// wizard cares about.
type e2eScan struct {
	id       string
	scan     scanDTO
	iisA     discover.Finding // readable IIS set of web-a
	appA     discover.Finding // readable log4net set of web-a
	iisB     discover.Finding // IIS-named junk
	appB     discover.Finding // log4net file that cannot be opened
	ready    []discover.Finding
	notReady []discover.Finding
}

func isFileKind(k discover.Kind) bool { return k == discover.KindLogFile || k == discover.KindLogSet }

func (x *e2eFix) scanNow() e2eScan {
	x.t.Helper()
	id, sc := x.scan()
	out := e2eScan{id: id, scan: sc}
	for _, fd := range sc.Findings {
		if !isFileKind(fd.Kind) {
			continue
		}
		if fd.Verified {
			out.ready = append(out.ready, fd)
		} else {
			out.notReady = append(out.notReady, fd)
		}
	}
	for _, fd := range sc.Findings {
		switch {
		case fd.Verified && fd.Importer == "iis-w3c":
			out.iisA = fd
		case fd.Verified && fd.Importer == "log4net-text":
			out.appA = fd
		case !fd.Verified && isFileKind(fd.Kind) && strings.Contains(fd.Display, "W3SVC2"):
			out.iisB = fd
		case !fd.Verified && isFileKind(fd.Kind) && strings.Contains(fd.Display, "app.log"):
			out.appB = fd
		}
	}
	return out
}

func (x *e2eFix) wantScanShape(s e2eScan) {
	x.t.Helper()
	for name, fd := range map[string]discover.Finding{"iisA": s.iisA, "appA": s.appA, "iisB": s.iisB, "appB": s.appB} {
		if fd.ID == "" {
			x.t.Fatalf("scan has no %s finding; file-kind findings: %s", name, e2eDescribe(s.scan.Findings))
		}
	}
	// A finding is selectable only when it is verified and names an importer.
	for _, fd := range s.notReady {
		if fd.Importer != "" || fd.Verified || fd.Unreadable == "" {
			x.t.Errorf("an unreadable finding is selectable: %+v", fd)
		}
	}
	for _, fd := range s.ready {
		if fd.Importer == "" || fd.Unreadable != "" {
			x.t.Errorf("a verified finding is not selectable: %+v", fd)
		}
	}
	// The answer holds no path and none of the log text.
	res := x.do(http.MethodGet, "/api/discover/scans/"+s.id, "")
	body := strings.ToLower(res.Body.String())
	for _, bad := range []string{`d:\`, `c:\`, `\\\\`, strings.ToLower(e2eLogMarker), "order-"} {
		if strings.Contains(body, bad) {
			x.t.Errorf("the scan answer holds %q", bad)
		}
	}
}

func e2eDescribe(fs []discover.Finding) string {
	var parts []string
	for _, f := range fs {
		parts = append(parts, fmt.Sprintf("{%s %s importer=%q verified=%v unreadable=%q files=%d}", f.Kind, f.Display, f.Importer, f.Verified, f.Unreadable, f.Files))
	}
	return strings.Join(parts, " ")
}

func (x *e2eFix) postJSON(path, body string, into any) *httptest.ResponseRecorder {
	x.t.Helper()
	method := http.MethodPost
	res := x.do(method, path, body)
	if into != nil && res.Code < 300 {
		decodeBody(x.t, res, into)
	}
	return res
}

func (x *e2eFix) tagsOf(caseID string) []sourceTagDTO {
	x.t.Helper()
	res := x.do(http.MethodGet, "/api/cases/"+caseID+"/sources", "")
	if res.Code != http.StatusOK {
		x.t.Fatalf("sources = %d %s", res.Code, res.Body)
	}
	var out struct {
		Sources []sourceTagDTO `json:"sources"`
	}
	decodeBody(x.t, res, &out)
	return out.Sources
}

func (x *e2eFix) envsOf(caseID string) []string {
	x.t.Helper()
	res := x.do(http.MethodGet, "/api/cases/"+caseID+"/environments", "")
	if res.Code != http.StatusOK {
		x.t.Fatalf("environments = %d %s", res.Code, res.Body)
	}
	var out struct {
		Environments []string `json:"environments"`
	}
	decodeBody(x.t, res, &out)
	return out.Environments
}

// e2eWizard is what a finished wizard run leaves behind.
type e2eWizard struct {
	caseID  string
	scan    e2eScan
	records map[string]importRecordDTO // by importer, the newest file of each
}

// runWizard does what the create wizard does for a scan with two ready
// sources: create the case, set the environments, tag each source, import the
// selected findings, and link each tag to the import it produced.
func (x *e2eFix) runWizard() e2eWizard {
	x.t.Helper()
	s := x.scanNow()
	x.wantScanShape(s)
	cid := x.caseID(e2eTitle)

	// Environments: trimmed, blanks dropped, case-insensitive duplicates collapsed.
	res := x.do(http.MethodPut, "/api/cases/"+cid+"/environments", `{"environments":["Production","  Staging ","","production"]}`)
	if res.Code != http.StatusOK {
		x.t.Fatalf("put environments = %d %s", res.Code, res.Body)
	}
	if got := x.envsOf(cid); !reflect.DeepEqual(got, []string{"Production", "Staging"}) {
		x.t.Fatalf("environments = %v", got)
	}

	// Import the two ready findings together.
	jobs := x.importJobs(cid, s.id, []string{s.iisA.ID, s.appA.ID}, discFrom, discTo)
	w := e2eWizard{caseID: cid, scan: s, records: map[string]importRecordDTO{}}
	for i, j := range jobs {
		r := x.wantDone(j)
		if r.Record == nil || r.Record.Rows == 0 || r.Files == 0 {
			x.t.Fatalf("job %d result = %+v", i, r)
		}
		w.records[r.Importer] = *r.Record
	}
	for _, imp := range []string{"iis-w3c", "log4net-text"} {
		if _, ok := w.records[imp]; !ok {
			x.t.Fatalf("no %s import record in %v", imp, w.records)
		}
	}

	// Tag each source, linked to the import it produced.
	tags := []struct {
		fd                 discover.Finding
		env, portal, api   string
		wantKind, wantFile string
	}{
		{s.iisA, "Production", "Orders portal", "Orders API", "discover", "iis-w3c"},
		{s.appA, "Staging", "Orders portal", "Orders worker", "discover", "log4net-text"},
	}
	for _, tg := range tags {
		body := fmt.Sprintf(`{"importId":%d,"kind":%s,"label":%s,"env":%s,"portal":%s,"api":%s,"classification":""}`,
			w.records[tg.wantFile].ID, q(tg.wantKind), q(tg.fd.Display), q(tg.env), q(tg.portal), q(tg.api))
		res := x.do(http.MethodPost, "/api/cases/"+cid+"/sources", body)
		if res.Code != http.StatusCreated {
			x.t.Fatalf("post source = %d %s", res.Code, res.Body)
		}
	}
	return w
}

// e2eEventKey is what must survive an export and a restore. EvidenceID is
// left out on purpose: it is derived from the case id.
type e2eEventKey struct {
	Source, Kind, Host, Unit, TZRule, Attrs string
	Start                                   time.Time
	Line                                    int64
}

func e2eEventKeys(evs []eventDTO) []e2eEventKey {
	out := make([]e2eEventKey, len(evs))
	for i, e := range evs {
		keys := make([]string, 0, len(e.Attrs))
		for k := range e.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var attrs strings.Builder
		for _, k := range keys {
			attrs.WriteString(k + "=" + e.Attrs[k] + ";")
		}
		out[i] = e2eEventKey{e.Source, e.Kind, e.Host, e.Unit, e.TZRule, attrs.String(), e.Start, e.Line}
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].Start.Equal(out[b].Start) {
			return out[a].Start.Before(out[b].Start)
		}
		if out[a].Source != out[b].Source {
			return out[a].Source < out[b].Source
		}
		return out[a].Line < out[b].Line
	})
	return out
}

/* ------------------------------------------------- wizard flow, happy path */

func TestE2E_WizardScanCreateTagImport(t *testing.T) {
	x := newE2E(t)
	w := x.runWizard()
	cid := w.caseID

	// Events from both sources arrived under the mapped host, from the right importers.
	evs, truncated := x.e.eventsOf(cid, "")
	if truncated {
		t.Fatal("events truncated")
	}
	// 6 IIS requests and 5 log4net lines.
	var iis, app int
	for _, e := range evs {
		if e.Host != "web-a" {
			t.Errorf("event host = %q, want web-a", e.Host)
		}
		switch {
		case strings.HasPrefix(e.Source, "iis"):
			iis++
		case strings.Contains(e.Source, "log4net") || strings.Contains(e.Source, "app.log"):
			app++
		}
	}
	if len(evs) != 8 || iis != 6 || app != 2 {
		var srcs []string
		for _, e := range evs {
			srcs = append(srcs, e.Source+"/"+e.Kind)
		}
		t.Fatalf("events = %d (iis %d, app %d), want 8 (6, 2): %v", len(evs), iis, app, srcs)
	}

	// The case holds the environments and both tags, each tied to its import.
	if got := x.envsOf(cid); !reflect.DeepEqual(got, []string{"Production", "Staging"}) {
		t.Fatalf("environments = %v", got)
	}
	tags := x.tagsOf(cid)
	if len(tags) != 2 {
		t.Fatalf("tags = %+v", tags)
	}
	wantTag := map[string][3]string{"iis-w3c": {"Production", "Orders portal", "Orders API"}, "log4net-text": {"Staging", "Orders portal", "Orders worker"}}
	imports := x.e.importsOf(cid)
	byID := map[int64]importRecordDTO{}
	for _, r := range imports {
		byID[r.ID] = r
	}
	seen := map[string]bool{}
	for _, tg := range tags {
		rec, ok := byID[tg.ImportID]
		if !ok {
			t.Fatalf("tag %+v names an import that does not exist", tg)
		}
		want, ok := wantTag[rec.Importer]
		if !ok || [3]string{tg.Env, tg.Portal, tg.API} != want || tg.Kind != "discover" {
			t.Errorf("tag for %s = %+v, want %v", rec.Importer, tg, want)
		}
		seen[rec.Importer] = true
		if rec.Rows == 0 {
			t.Errorf("import %d behind tag %+v has no rows", rec.ID, tg)
		}
	}
	if !seen["iis-w3c"] || !seen["log4net-text"] {
		t.Fatalf("tags cover %v", seen)
	}
	// The case counts agree.
	res := x.do(http.MethodGet, "/api/cases/"+cid, "")
	var got struct {
		Envs    int `json:"envs"`
		Sources int `json:"sources"`
	}
	decodeBody(t, res, &got)
	if res.Code != http.StatusOK || got.Envs != 2 || got.Sources != 2 {
		t.Fatalf("case = %d %s", res.Code, res.Body)
	}

	// Removing a tag removes the tag only: every event stays.
	res = x.do(http.MethodDelete, fmt.Sprintf("/api/cases/%s/sources/%d", cid, tags[0].ID), "")
	if res.Code != http.StatusOK {
		t.Fatalf("delete tag = %d %s", res.Code, res.Body)
	}
	if after, _ := x.e.eventsOf(cid, ""); len(after) != 8 {
		t.Fatalf("events after removing a tag = %d, want 8", len(after))
	}
	if n := len(x.tagsOf(cid)); n != 1 {
		t.Fatalf("tags after delete = %d", n)
	}

	// No secret, passphrase or fixture path in the log.
	if s := x.logs.String(); strings.Contains(s, e2ePass) || strings.Contains(s, e2eLogMarker) || strings.Contains(strings.ToLower(s), `d:\`) {
		t.Fatalf("log holds a secret, log text or path: %s", s)
	}
}

/* ---------------------------------------- unreadable findings are refused */

func TestE2E_UnreadableFindingIsNeverImportable(t *testing.T) {
	x := newE2E(t)
	s := x.scanNow()
	x.wantScanShape(s)
	cid := x.caseID("refusals")

	// Each unreadable finding on its own, and next to a good one: the server
	// refuses the whole request and stores nothing.
	for name, bad := range map[string]discover.Finding{"iis junk": s.iisB, "log4net junk": s.appB} {
		for _, ids := range [][]string{{bad.ID}, {s.iisA.ID, bad.ID}, {bad.ID, s.appA.ID}} {
			res := x.do(http.MethodPost, importsPath(cid), discBody(s.id, ids, discFrom, discTo))
			wantReason(t, res, http.StatusUnprocessableEntity, "not-importable")
			if strings.Contains(strings.ToLower(res.Body.String()), `d:\`) {
				t.Errorf("%s: refusal names a path: %s", name, res.Body)
			}
		}
	}
	if n := len(x.e.importsOf(cid)); n != 0 {
		t.Fatalf("refused imports stored %d imports", n)
	}
	if evs, _ := x.e.eventsOf(cid, ""); len(evs) != 0 {
		t.Fatalf("refused imports stored %d events", len(evs))
	}
	if x.e.slotsHeld() != 0 {
		t.Fatalf("limiter slots held = %d", x.e.slotsHeld())
	}

	// Locating the unreadable log4net source: a folder with no logs, a folder
	// that is not logs, and a path that is not there all leave it unusable.
	locate := func(finding, path string) (*httptest.ResponseRecorder, locateResponse) {
		res := x.do(http.MethodPost, "/api/discover/scans/"+s.id+"/findings/"+finding+"/locate", `{"path":`+q(path)+`}`)
		var out locateResponse
		if res.Code == http.StatusOK {
			decodeBody(t, res, &out)
		}
		return res, out
	}
	for path, reason := range map[string]string{
		e2eSiteB:                "no-log-files", // the folder holds web.config and logs\, no log file
		e2eSiteB + `\logs`:      "wrong-format", // the one file is junk
		`D:\Sites\nowhere\logs`: "not-found",
		e2eIISB + `\W3SVC2`:     "wrong-format", // junk
	} {
		res, out := locate(s.appB.ID, path)
		if res.Code != http.StatusOK || out.Verified || out.Reason != reason {
			t.Errorf("locate %s = %d %s, want unverified %q", path, res.Code, res.Body, reason)
		}
	}
	wantReason(t, x.do(http.MethodPost, importsPath(cid), discBody(s.id, []string{s.appB.ID}, discFrom, discTo)), http.StatusUnprocessableEntity, "not-importable")

	// Pointing it at a folder that does hold readable logs completes it, and only
	// then can it be imported.
	res, out := locate(s.appB.ID, e2eSiteA+`\logs`)
	if res.Code != http.StatusOK || !out.Verified || out.Finding == nil || out.Finding.Importer != "log4net-text" || !out.Finding.Verified || out.Finding.Unreadable != "" {
		t.Fatalf("locate on a readable folder = %d %s", res.Code, res.Body)
	}
	// The completed finding shows as ready in the scan now.
	var seen discover.Finding
	for _, fd := range x.getScan(s.id).Findings {
		if fd.ID == s.appB.ID {
			seen = fd
		}
	}
	if !seen.Verified || seen.Importer != "log4net-text" {
		t.Fatalf("scan after locate = %+v", seen)
	}
	r := x.wantDone(x.importJobs(cid, s.id, []string{s.appB.ID}, discFrom, discTo)[0])
	if r.Importer != "log4net-text" || r.Record == nil || r.Record.Rows != 5 {
		t.Fatalf("import after locate = %+v", r)
	}
	// The junk IIS set stays refused throughout.
	wantReason(t, x.do(http.MethodPost, importsPath(cid), discBody(s.id, []string{s.iisB.ID}, discFrom, discTo)), http.StatusUnprocessableEntity, "not-importable")
}

// A finding id from another scan, a forged id, and an id sent as a path are
// refused by the server whatever the page showed.
func TestE2E_ForgedFindingsAreRefused(t *testing.T) {
	x := newE2E(t)
	s := x.scanNow()
	cid := x.caseID("forged")
	for _, ids := range [][]string{{"forged-id-1234"}, {s.iisA.ID, "forged-id-1234"}, {`D:\Logs\portal-a\IIS\W3SVC1\u_ex010911.log`}} {
		res := x.do(http.MethodPost, importsPath(cid), discBody(s.id, ids, discFrom, discTo))
		if res.Code != http.StatusBadRequest && res.Code != http.StatusNotFound {
			t.Errorf("%v = %d %s", ids, res.Code, res.Body)
		}
	}
	if n := len(x.e.importsOf(cid)); n != 0 {
		t.Fatalf("imports = %d", n)
	}
}

/* ------------------------------------------ export, restore, same events */

func TestE2E_ExportRestoreKeepsEventsAndTags(t *testing.T) {
	x := newE2E(t)
	w := x.runWizard()
	cid := w.caseID
	before, _ := x.e.eventsOf(cid, "")
	if len(before) != 8 {
		t.Fatalf("events = %d", len(before))
	}
	tagsBefore := x.tagsOf(cid)
	out := t0Dir(t)

	res := x.do(http.MethodPost, "/api/cases/"+cid+"/export", `{"path":`+q(out)+`,"passphrase":`+q(e2ePass)+`}`)
	if res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	var ex caseFileExportReply
	decodeBody(t, res, &ex)
	if ex.Path != filepath.Join(out, cid+casestore.CaseFileExt) || ex.Bytes <= 0 {
		t.Fatalf("export reply = %+v", ex)
	}
	data, err := os.ReadFile(ex.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{e2eTitle, e2eLogMarker, "Orders portal", "Production", e2ePass, "SQLite format 3", "web-a"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("the case file holds %q in plain", secret)
		}
	}

	// Restoring needs the passphrase of the PC that wrote the file, and nothing
	// is created for a wrong one.
	casesBefore := len(x.caseIDsE2E())
	cfsReason(t, x.doNoLeak(http.MethodPost, "/api/cases/restore", `{"path":`+q(ex.Path)+`,"passphrase":"not the passphrase at all"}`),
		http.StatusUnprocessableEntity, "bad-case-file")
	if got := len(x.caseIDsE2E()); got != casesBefore {
		t.Fatalf("a refused restore created a case (%d -> %d)", casesBefore, got)
	}

	res = x.do(http.MethodPost, "/api/cases/restore", `{"path":`+q(ex.Path)+`,"passphrase":`+q(e2ePass)+`}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("restore = %d %s", res.Code, res.Body)
	}
	var restored caseDTO
	decodeBody(t, res, &restored)
	if restored.ID == "" || restored.ID == cid || restored.Title != e2eTitle {
		t.Fatalf("restored = %+v (source %s)", restored, cid)
	}

	after, _ := x.e.eventsOf(restored.ID, "")
	if len(after) != len(before) {
		t.Fatalf("restored events = %d, want %d", len(after), len(before))
	}
	if !reflect.DeepEqual(e2eEventKeys(before), e2eEventKeys(after)) {
		t.Fatalf("restored events differ from the source's")
	}
	if len(x.e.importsOf(restored.ID)) != len(x.e.importsOf(cid)) {
		t.Fatalf("import records: %d restored, %d source", len(x.e.importsOf(restored.ID)), len(x.e.importsOf(cid)))
	}
	// The wizard's environments and tags travel with the case.
	if got := x.envsOf(restored.ID); !reflect.DeepEqual(got, []string{"Production", "Staging"}) {
		t.Errorf("restored environments = %v", got)
	}
	tagsAfter := x.tagsOf(restored.ID)
	if len(tagsAfter) != len(tagsBefore) {
		t.Fatalf("restored tags = %+v, want %+v", tagsAfter, tagsBefore)
	}
	for i := range tagsBefore {
		a, b := tagsBefore[i], tagsAfter[i]
		if a.Kind != b.Kind || a.Label != b.Label || a.Env != b.Env || a.Portal != b.Portal || a.API != b.API {
			t.Errorf("restored tag %d = %+v, want %+v", i, b, a)
		}
	}
	// The source case is untouched.
	if got, _ := x.e.eventsOf(cid, ""); len(got) != len(before) {
		t.Fatalf("source events after export and restore = %d", len(got))
	}
	// The passphrase reached no log.
	if s := x.logs.String(); strings.Contains(s, e2ePass) {
		t.Fatalf("passphrase in log: %s", s)
	}
}

func (x *e2eFix) caseIDsE2E() map[string]bool {
	x.t.Helper()
	list, err := x.store.List(context.Background())
	if err != nil {
		x.t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range list {
		out[string(c.ID)] = true
	}
	return out
}

func (x *e2eFix) doNoLeak(method, path, body string) *httptest.ResponseRecorder {
	return x.do(method, path, body)
}

/* --------------------------------- every new route refuses a foreign caller */

type e2eRoute struct {
	name, method, path, body string
}

func (x *e2eFix) e2eNewRoutes(scanID, findingID, caseID, outDir, restoreFile string) []e2eRoute {
	pw := q(e2ePass)
	return []e2eRoute{
		{"locate", http.MethodPost, "/api/discover/scans/" + scanID + "/findings/" + findingID + "/locate", `{"path":` + q(e2eSiteA+`\logs`) + `}`},
		{"probe reachable", http.MethodPost, "/api/probe/reachable", `{"host":"127.0.0.1","port":1}`},
		{"get case", http.MethodGet, "/api/cases/" + caseID, ""},
		{"get environments", http.MethodGet, "/api/cases/" + caseID + "/environments", ""},
		{"put environments", http.MethodPut, "/api/cases/" + caseID + "/environments", `{"environments":["Hijacked"]}`},
		{"get sources", http.MethodGet, "/api/cases/" + caseID + "/sources", ""},
		{"post source", http.MethodPost, "/api/cases/" + caseID + "/sources", `{"importId":0,"kind":"discover","label":"hijack","env":"x","portal":"x","api":"x","classification":""}`},
		{"delete source", http.MethodDelete, "/api/cases/" + caseID + "/sources/1", ""},
		{"mongo sources", http.MethodGet, "/api/cases/" + caseID + "/mongo-sources", ""},
		{"master-key status", http.MethodGet, "/api/master-key/status", ""},
		{"master-key setup", http.MethodPost, "/api/master-key/setup", setupBody("a hijacking passphrase", "a hijacking passphrase", true)},
		{"master-key unlock", http.MethodPost, "/api/master-key/unlock", unlockBody(e2ePass)},
		{"master-key lock", http.MethodPost, "/api/master-key/lock", ""},
		{"master-key rewrap", http.MethodPost, "/api/master-key/rewrap", `{}`},
		{"master-key reset-damaged", http.MethodPost, "/api/master-key/reset-damaged", `{"acknowledged":true}`},
		{"export case", http.MethodPost, "/api/cases/" + caseID + "/export", `{"path":` + q(outDir) + `,"passphrase":` + pw + `}`},
		{"restore case", http.MethodPost, "/api/cases/restore", `{"path":` + q(restoreFile) + `,"passphrase":` + pw + `}`},
	}
}

// e2eReq sends one request with exactly the headers given.
func (x *e2eFix) e2eReq(r e2eRoute, token, origin, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	req.Host = host
	if token != "" {
		req.Header.Set("X-Audit-Token", token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if r.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	x.e.h.ServeHTTP(res, req)
	return res
}

func TestE2E_NewRoutesRefuseWithoutTokenAndWithForeignOrigin(t *testing.T) {
	x := newE2E(t)
	w := x.runWizard()
	cid := w.caseID
	// A real case file for restore to be tempted by.
	out := t0Dir(t)
	exported := filepath.Join(out, "bait"+casestore.CaseFileExt)
	if res := x.do(http.MethodPost, "/api/cases/"+cid+"/export", `{"path":`+q(exported)+`,"passphrase":`+q(e2ePass)+`}`); res.Code != http.StatusOK {
		t.Fatalf("export = %d %s", res.Code, res.Body)
	}
	emptyOut := t0Dir(t)

	envsBefore := x.envsOf(cid)
	tagsBefore := x.tagsOf(cid)
	casesBefore := x.caseIDsE2E()
	keyBefore, err := os.ReadFile(filepath.Join(x.mkDir, "masterkey.dat"))
	if err != nil {
		t.Fatal(err)
	}
	const local = "127.0.0.1:8765"
	routes := x.e2eNewRoutes(w.scan.id, w.scan.iisA.ID, cid, emptyOut, exported)

	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			var bodies []string
			check := func(what string, res *httptest.ResponseRecorder, want ...int) {
				t.Helper()
				ok := false
				for _, c := range want {
					ok = ok || res.Code == c
				}
				if !ok {
					t.Errorf("%s = %d %s, want one of %v", what, res.Code, res.Body, want)
				}
				bodies = append(bodies, res.Body.String())
			}
			check("no token", x.e2eReq(r, "", "", local), http.StatusUnauthorized)
			check("no token, local origin", x.e2eReq(r, "", "http://"+local, local), http.StatusUnauthorized)
			check("wrong token", x.e2eReq(r, "wrong-token", "", local), http.StatusUnauthorized)
			check("token of the wrong length", x.e2eReq(r, testToken[:len(testToken)-1], "", local), http.StatusUnauthorized)
			check("token with a space added", x.e2eReq(r, testToken+" ", "", local), http.StatusUnauthorized)
			for _, origin := range []string{
				"http://evil.example", "http://evil.example:8765", "http://127.0.0.1:1", "http://localhost:8765",
				"https://" + local, "null", "http://" + local + "/x", "http://user@" + local, "file://",
			} {
				check("foreign origin "+origin, x.e2eReq(r, testToken, origin, local), http.StatusForbidden)
				check("foreign origin and no token "+origin, x.e2eReq(r, "", origin, local), http.StatusForbidden, http.StatusUnauthorized)
			}
			for _, host := range []string{"evil.example", "evil.example:8765", "127.0.0.1.evil.example:8765", "[::1", "0.0.0.0:8765"} {
				check("foreign host "+host, x.e2eReq(r, testToken, "", host), http.StatusForbidden)
			}
			// A refusal says nothing about the case, and echoes no passphrase or path.
			for _, b := range bodies {
				for _, leak := range []string{e2eTitle, cid, e2ePass, exported, emptyOut, "Orders portal", e2eLogMarker} {
					if strings.Contains(b, leak) {
						t.Errorf("a refusal body holds %q: %s", leak, b)
					}
				}
			}
		})
	}

	// None of the refused calls did anything.
	if got := x.envsOf(cid); !reflect.DeepEqual(got, envsBefore) {
		t.Errorf("environments changed: %v -> %v", envsBefore, got)
	}
	if got := x.tagsOf(cid); !reflect.DeepEqual(got, tagsBefore) {
		t.Errorf("tags changed: %+v -> %+v", tagsBefore, got)
	}
	if got := x.caseIDsE2E(); !reflect.DeepEqual(got, casesBefore) {
		t.Errorf("cases changed: %v -> %v", casesBefore, got)
	}
	if ents, _ := os.ReadDir(emptyOut); len(ents) != 0 {
		t.Errorf("a refused export wrote %d entries", len(ents))
	}
	if keyAfter, err := os.ReadFile(filepath.Join(x.mkDir, "masterkey.dat")); err != nil || !bytes.Equal(keyBefore, keyAfter) {
		t.Errorf("masterkey.dat changed (read error %v)", err)
	}
	res := x.do(http.MethodGet, "/api/master-key/status", "")
	var st mkState
	decodeBody(t, res, &st)
	if st.State != "unlocked" {
		t.Errorf("a refused lock changed the key state to %q", st.State)
	}
	// Tokenless probing never counted against the real caller's allowance, and a
	// real call still works.
	if res := x.do(http.MethodGet, "/api/cases/"+cid, ""); res.Code != http.StatusOK {
		t.Errorf("get case after the refusals = %d %s", res.Code, res.Body)
	}
}

// A PC with no master key yet: a foreign page must not be able to set one.
func TestE2E_MasterKeySetupRefusesForeignOrigin(t *testing.T) {
	pc := newCFSPC(t, "", false)
	body := setupBody(cfsPassA, cfsPassA, true)
	send := func(token, origin, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/master-key/setup", strings.NewReader(body))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-Audit-Token", token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res := httptest.NewRecorder()
		pc.h.ServeHTTP(res, req)
		return res
	}
	local := "127.0.0.1:8765"
	for what, res := range map[string]*httptest.ResponseRecorder{
		"no token":        send("", "", local),
		"foreign origin":  send(testToken, "http://evil.example", local),
		"foreign host":    send(testToken, "", "evil.example"),
		"null origin":     send(testToken, "null", local),
		"wrong token":     send("nope", "", local),
		"foreign + wrong": send("nope", "http://evil.example", local),
	} {
		if res.Code != http.StatusUnauthorized && res.Code != http.StatusForbidden {
			t.Errorf("%s = %d %s", what, res.Code, res.Body)
		}
		if strings.Contains(res.Body.String(), cfsPassA) {
			t.Errorf("%s echoes the passphrase", what)
		}
	}
	if _, err := os.Stat(filepath.Join(pc.dir, "masterkey.dat")); err == nil {
		t.Fatal("a refused setup wrote masterkey.dat")
	}
	res := pc.do(http.MethodGet, "/api/master-key/status", "")
	var st mkState
	decodeBody(t, res, &st)
	if st.State != "none" {
		t.Fatalf("state = %q after the refusals, want none", st.State)
	}
}

// The server answers with no session at all (no token on the page) the same
// way for a route that exists and one that does not.
func TestE2E_RefusalDoesNotRevealWhichCaseExists(t *testing.T) {
	x := newE2E(t)
	cid := x.caseID("exists")
	missing, err := casestore.NewID()
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/environments", "/sources", "/mongo-sources", "/export"} {
		a := x.e2eReq(e2eRoute{method: http.MethodGet, path: "/api/cases/" + cid + suffix}, "", "", "127.0.0.1:8765")
		b := x.e2eReq(e2eRoute{method: http.MethodGet, path: "/api/cases/" + string(missing) + suffix}, "", "", "127.0.0.1:8765")
		if a.Code != http.StatusUnauthorized || b.Code != http.StatusUnauthorized || a.Body.String() != b.Body.String() {
			t.Errorf("%q: %d %s vs %d %s", suffix, a.Code, a.Body, b.Code, b.Body)
		}
	}
}
