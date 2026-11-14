package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

// The check endpoint reads files like a preview and stores nothing. Names in the
// files are synthetic; the test host list carries web-a and mongo-a.

type checkJSONEnv interface {
	json(method, path, body string) *httptest.ResponseRecorder
}

func checkPath(caseID string) string { return "/api/cases/" + caseID + "/imports/check?async=1" }

// startedCheck decodes the 202 answer of an upload to the check endpoint and
// returns it with the finished job.
func startedCheck(t *testing.T, e checkJSONEnv, caseID string, res *httptest.ResponseRecorder) (importJobDTO, importJobDTO) {
	t.Helper()
	if res.Code != http.StatusAccepted {
		t.Fatalf("check = %d %s", res.Code, res.Body)
	}
	var started importJobDTO
	decodeBody(t, res, &started)
	return started, settleCheck(t, e, caseID, started.ID)
}

func settleCheck(t *testing.T, e checkJSONEnv, caseID, job string) importJobDTO {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res := e.json(http.MethodGet, jobPath(caseID, job), "")
		if res.Code != http.StatusOK {
			t.Fatalf("get job = %d %s", res.Code, res.Body)
		}
		var out importJobDTO
		decodeBody(t, res, &out)
		if out.State == jobDone || out.State == jobFailed {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("check job did not finish")
	return importJobDTO{}
}

// onlyVerdict decodes the report of a done check job holding exactly one file.
func onlyVerdict(t *testing.T, job importJobDTO) checkFileVerdict {
	t.Helper()
	if job.State != jobDone || job.Status != http.StatusOK {
		t.Fatalf("job = %s status %d failure %+v result %s", job.State, job.Status, job.Failure, job.Result)
	}
	var rep checkReportDTO
	if err := json.Unmarshal(job.Result, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 1 || rep.CheckedAt.IsZero() {
		t.Fatalf("report = %+v, want one file and a time", rep)
	}
	return rep.Files[0]
}

func TestImportCheckReady(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, ready")
	_, done := startedCheck(t, e, id, e.upload(checkPath(id), nil, &upload{"u_ex010203.log", unlistedW3C("web-a.example.test")}))
	v := onlyVerdict(t, done)
	if v.Verdict != verdictReady || v.Importer != "iis-w3c" || v.Rows != 5 || v.Rejected != 0 || v.ServerName != "" || len(v.Tied) != 0 {
		t.Fatalf("verdict = %+v", v)
	}
	if v.Name != "u_ex010203.log" || v.Size == 0 || v.Key == "" {
		t.Fatalf("verdict identity = %+v", v)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a check stored %d imports", n)
	}
}

func TestImportCheckNeedsComputer(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, unlisted computer")
	_, done := startedCheck(t, e, id, e.upload(checkPath(id), nil, &upload{"u_ex010203.log", unlistedW3C("ISS-PROD-07")}))
	v := onlyVerdict(t, done)
	if v.Verdict != verdictNeedsComputer || v.ServerName != "ISS-PROD-07" || v.Importer != "iis-w3c" {
		t.Fatalf("verdict = %+v", v)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a check stored %d imports", n)
	}
}

// A file that names no computer at all cannot be matched to one.
func TestImportCheckNoComputer(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, no computer")
	_, done := startedCheck(t, e, id, e.upload(checkPath(id), nil, &upload{intFileName, intHTTPERR}))
	v := onlyVerdict(t, done)
	if v.Verdict != verdictNoComputer || v.ServerName != "" {
		t.Fatalf("verdict = %+v", v)
	}
}

func TestImportCheckNotSupported(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, not supported")
	for _, c := range []struct{ name, body, reason string }{
		{"logs.zip", "PK\x03\x04" + strings.Repeat("\x00", 64), "archive-extract-first"},
		{"notes.txt", "just some words\nnothing a log reader knows\n", "unsupported-format"},
	} {
		_, done := startedCheck(t, e, id, e.upload(checkPath(id), nil, &upload{c.name, c.body}))
		v := onlyVerdict(t, done)
		if v.Verdict != verdictNotSupported || v.Reason != c.reason || v.Importer != "" {
			t.Errorf("%s: verdict = %+v, want not-supported %s", c.name, v, c.reason)
		}
	}
}

func TestImportCheckJobKind(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, job kind")
	started, done := startedCheck(t, e, id, e.upload(checkPath(id), nil, &upload{"u_ex010203.log", unlistedW3C("web-a")}))
	if started.Kind != "check" || done.Kind != "check" {
		t.Fatalf("kind = %q then %q, want check", started.Kind, done.Kind)
	}
	// an ordinary import job keeps reporting itself as an import
	res := e.upload("/api/cases/"+id+"/imports?preview=1&async=1&importer=iis-w3c&host=web-a", nil, &upload{"u_ex010203.log", unlistedW3C("web-a")})
	if res.Code != http.StatusAccepted {
		t.Fatalf("preview job = %d %s", res.Code, res.Body)
	}
	var imp importJobDTO
	decodeBody(t, res, &imp)
	if imp.Kind != "import" {
		t.Fatalf("import job kind = %q, want import", imp.Kind)
	}
	if settled := settleCheck(t, e, id, imp.ID); settled.Kind != "import" {
		t.Fatalf("settled import job kind = %q, want import", settled.Kind)
	}
}

/* ------------------------------------------------------------ negatives */

// checkUploadBody builds a multipart body with any number of file parts.
// uploadBody carries only one file; the file-cap negative needs many. Test-only.
func checkUploadBody(t *testing.T, fields []formField, files []upload) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f.name, f.value); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		part, err := mw.CreateFormFile("file", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, f.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func serveCheckFiles(t *testing.T, e *intEnv, id string, fields []formField, files []upload) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := checkUploadBody(t, fields, files)
	req := httptest.NewRequest(http.MethodPost, checkPath(id), body)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", contentType)
	return e.serve(req)
}

// The request-level refusals, each on a fresh case. The check runs the same case
// gate, media check, limiter and form caps as an import, before any file is read.
func TestImportCheckRefusals(t *testing.T) {
	for _, c := range []struct {
		name   string
		code   int
		reason string
		run    func(t *testing.T, e *intEnv, id string) *httptest.ResponseRecorder
	}{
		{
			// openCaseForWrite runs before the media check, so the case must exist.
			name: "not-multipart", code: http.StatusUnsupportedMediaType, reason: "not-multipart",
			run: func(t *testing.T, e *intEnv, id string) *httptest.ResponseRecorder {
				return e.json(http.MethodPost, checkPath(id), "{}")
			},
		},
		{
			name: "case-closed", code: http.StatusConflict, reason: "case-closed",
			run: func(t *testing.T, e *intEnv, id string) *httptest.ResponseRecorder {
				if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
					t.Fatalf("close = %d %s", res.Code, res.Body)
				}
				return e.upload(checkPath(id), nil, &upload{"u_ex010203.log", unlistedW3C("web-a")})
			},
		},
		{
			// 201 file parts: the 201st trips maxCheckFiles before it is spooled.
			name: "too-many-files", code: http.StatusRequestEntityTooLarge, reason: "too-many-files",
			run: func(t *testing.T, e *intEnv, id string) *httptest.ResponseRecorder {
				files := make([]upload, maxCheckFiles+1)
				for i := range files {
					files[i] = upload{"f.log", "x"}
				}
				return serveCheckFiles(t, e, id, nil, files)
			},
		},
		{
			// A key field one byte over maxCheckKeyBytes, before the file part.
			name: "key-too-long", code: http.StatusBadRequest, reason: "bad-form",
			run: func(t *testing.T, e *intEnv, id string) *httptest.ResponseRecorder {
				return e.upload(checkPath(id), []formField{{"key", strings.Repeat("x", maxCheckKeyBytes+1)}}, &upload{"u_ex010203.log", unlistedW3C("web-a")})
			},
		},
		{
			// Hold every limiter slot, as running imports would: tryAcquire refuses.
			name: "import-busy", code: http.StatusTooManyRequests, reason: "import-busy",
			run: func(t *testing.T, e *intEnv, id string) *httptest.ResponseRecorder {
				var slots []func()
				for {
					s, ok := e.handler().imports.tryAcquire()
					if !ok {
						break
					}
					slots = append(slots, s)
				}
				defer func() {
					for _, s := range slots {
						s()
					}
				}()
				return e.upload(checkPath(id), nil, &upload{"u_ex010203.log", unlistedW3C("web-a")})
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newIntEnv(t, intRepo())
			id := e.newCase("Check, " + c.name)
			res := c.run(t, e, id)
			if res.Code != c.code {
				t.Fatalf("%s = %d %s, want %d", c.name, res.Code, res.Body, c.code)
			}
			if f := failureOf(t, res); f.Reason != c.reason {
				t.Fatalf("%s reason = %q, want %q", c.name, f.Reason, c.reason)
			}
			if c.code == http.StatusTooManyRequests && res.Header().Get("Retry-After") == "" {
				t.Errorf("%s: no Retry-After on 429", c.name)
			}
			if n := len(e.handler().jobs.m); n != 0 {
				t.Errorf("%s left %d job(s)", c.name, n)
			}
			if n := e.slotsHeld(); n != 0 {
				t.Errorf("%s left %d limiter slot(s) held", c.name, n)
			}
		})
	}
}

// The whole-request spool is capped at what one case can hold. The ContentLength
// pre-check refuses an over-cap body without spooling a byte, so the new 4 GiB
// bound is proved cheaply: no gigabytes are written.
func TestImportCheckSpoolCap(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, spool cap")
	limit := e.handler().uploadCap(nil)
	uncapped := limit * checkBodyFactor
	total := min(uncapped, casestore.DefaultCaps.MaxCaseBytes)
	if total != casestore.DefaultCaps.MaxCaseBytes {
		t.Fatalf("total = %d, want MaxCaseBytes %d", total, casestore.DefaultCaps.MaxCaseBytes)
	}
	// The cap must actually clamp: limit*checkBodyFactor alone allows more.
	if uncapped <= total {
		t.Fatalf("limit*checkBodyFactor = %d, want > %d so the cap binds", uncapped, total)
	}
	// total+1 is over the new cap but under the old bound, so the old code would
	// have accepted it; 5 GiB sits in the same gap. Each is refused up front.
	for _, cl := range []int64{total + 1, 5 << 30, uncapped} {
		req := e.uploadRequest(checkPath(id), nil, &upload{"u_ex010203.log", unlistedW3C("web-a")})
		req.ContentLength = cl
		res := e.serve(req)
		if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "request-too-large" {
			t.Errorf("ContentLength %d = %d %s, want 413 request-too-large", cl, res.Code, res.Body)
		}
	}
}

// checkErrorlog is a synthetic SQL Server ERRORLOG: host-local times, no zone.
const checkErrorlog = "2001-03-17 10:00:00.00 Server      Microsoft SQL Server 2019 - synthetic build\n" +
	"2001-03-17 10:00:01.00 Server      Synthetic startup line\n" +
	"2001-03-17 10:00:02.00 spid1s      Synthetic ready line\n"

// A SQL errorlog is written in the host's local clock. The check picks no host,
// so the importer refuses it for want of a zone (tz-unlabelled): needs-zone.
func TestImportCheckNeedsZone(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, needs zone")
	_, done := startedCheck(t, e, id, e.upload(checkPath(id), nil, &upload{"ERRORLOG", checkErrorlog}))
	v := onlyVerdict(t, done)
	if v.Verdict != verdictNeedsZone || !v.NeedsZone || v.Importer != "sql-errorlog" {
		t.Fatalf("verdict = %+v, want needs-zone for sql-errorlog", v)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a check stored %d imports", n)
	}
}

// checkCombined is a synthetic plain combined access log: one format both nginx
// and Apache write, with no trailing duration to tell them apart.
const checkCombined = "192.0.2.10 - - [03/Feb/2001:04:05:06 +0000] \"GET /api/v1/orders/1000 HTTP/1.1\" 200 1200\n" +
	"192.0.2.11 - - [03/Feb/2001:04:05:07 +0000] \"GET /api/v1/orders/1001 HTTP/1.1\" 200 1201\n"

// The registry cannot choose between the two access-log importers, so the check
// reports the tie (not-supported with a non-empty Tied) and the page asks which.
func TestImportCheckTied(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Check, tied")
	_, done := startedCheck(t, e, id, e.upload(checkPath(id), nil, &upload{"access.log", checkCombined}))
	v := onlyVerdict(t, done)
	if v.Verdict != verdictNotSupported || len(v.Tied) < 2 {
		t.Fatalf("verdict = %+v, want not-supported with a tie of two", v)
	}
	seen := map[string]bool{}
	for _, n := range v.Tied {
		seen[n] = true
	}
	if !seen["apache-combined"] || !seen["nginx-access"] {
		t.Fatalf("tied = %v, want apache-combined and nginx-access", v.Tied)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a check stored %d imports", n)
	}
}

// Cancelling a running check job ends it with the canceled failure and stores
// nothing. The holding importer parks the parse so the cancel is deterministic.
func TestImportCheckCanceled(t *testing.T) {
	imp, entered, _ := holdingImporter()
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{imp}})
	id := e.newCase("Check, cancel")
	res := e.upload(checkPath(id), nil, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusAccepted {
		t.Fatalf("check = %d %s", res.Code, res.Body)
	}
	var started importJobDTO
	decodeBody(t, res, &started)
	<-entered // the check job is parked inside the parse
	if r := e.json(http.MethodDelete, jobPath(id, started.ID), ""); r.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", r.Code, r.Body)
	}
	done := e.settle(id, started.ID)
	if done.State != jobFailed || done.Failure == nil || done.Failure.Reason != "canceled" || done.Status != http.StatusBadRequest {
		t.Fatalf("job = %+v, want failed canceled 400", done)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a cancelled check stored %d imports", n)
	}
	e.waitSlotsHeld(0)
}
