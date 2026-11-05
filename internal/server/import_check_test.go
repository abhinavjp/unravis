package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
