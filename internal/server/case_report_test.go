package server

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
)

func reportOf(t *testing.T, res *httptest.ResponseRecorder) reportAnswer {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("report = %d %s", res.Code, res.Body)
	}
	if res.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("report is cacheable: %q", res.Header().Get("Cache-Control"))
	}
	var out reportAnswer
	decodeBody(t, res, &out)
	sum := sha256.Sum256([]byte(out.Content))
	if out.SHA256 != hex.EncodeToString(sum[:]) || out.Bytes != len(out.Content) {
		t.Errorf("checksum or size does not match the content: %+v", out)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(out.Filename) {
		t.Errorf("filename %q is not plain ASCII", out.Filename)
	}
	return out
}

func reportBody(format, extra string) string {
	return `{"format":"` + format + `"` + extra + `}`
}

func TestReportOfAnEmptyCase(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id + "/report"
		h := reportOf(t, e.json(http.MethodPost, base, reportBody("html", "")))
		if h.Mime != "text/html; charset=utf-8" || !strings.HasSuffix(h.Filename, ".html") || !strings.HasPrefix(h.Filename, "unravis-report-") {
			t.Errorf("html answer = %+v", h)
		}
		for _, want := range []string{"<title>", reportNothingText, reportNoPayloads} {
			if !strings.Contains(h.Content, want) {
				t.Errorf("html lacks %q", want)
			}
		}
		c := reportOf(t, e.json(http.MethodPost, base, reportBody("csv", "")))
		if !strings.HasSuffix(c.Filename, ".csv") || !strings.HasPrefix(c.Content, "\xef\xbb\xbf") {
			t.Errorf("csv answer = %q %q", c.Filename, c.Content[:8])
		}
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(c.Content, "\xef\xbb\xbf"))).ReadAll()
		if err != nil || len(rows) < 3 || strings.Join(rows[0], ",") != "section,item,status,normal stretch,slow stretch,text" {
			t.Fatalf("csv = %v %v", rows, err)
		}
		var notice bool
		for _, r := range rows {
			if r[0] == "notice" && strings.Contains(r[5], reportNothingText) {
				notice = true
			}
		}
		if !notice {
			t.Errorf("no notice row in %v", rows)
		}
	})
}

func TestReportRefusesBadRequests(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("r")
	base := "/api/cases/" + id + "/report"
	for name, body := range map[string]string{
		"no format":      `{}`,
		"pdf":            `{"format":"pdf"}`,
		"upper case":     `{"format":"HTML"}`,
		"unknown field":  `{"format":"html","payloads":true}`,
		"bad request":    `{"format":"html","request":{"sourceIds":[]}}`,
		"not json":       `{`,
		"include a list": `{"format":"html","include":{"figures":[1]}}`,
	} {
		if res := e.json(http.MethodPost, base, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, res.Code, res.Body)
		}
	}
	if res := e.get(base); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d", res.Code)
	}
	if res := request(e.h, http.MethodPost, base, reportBody("html", ""), false); res.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d", res.Code)
	}
	if res := e.json(http.MethodPost, "/api/cases/"+strings.Repeat("a", 26)+"/report", reportBody("html", "")); res.Code != http.StatusNotFound {
		t.Errorf("absent case = %d", res.Code)
	}
}

func fullReportRequest(format, include string) string {
	return `{"format":"` + format + `","request":` + invPost(`,"releasesEntered":true`) + include + `}`
}

func TestReportOfAnAnalysedCase(t *testing.T) {
	repo := &fakeRepository{requestPages: []audit.Page{okPage(invRows())}}
	e := tagEnv(t, casestore.NewMemory(), repo)
	id := e.newCase("Checkout slowdown")
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/checklist", `{"list":"doing","text":"Ask the database team","suspect":"H1"}`); res.Code != http.StatusOK {
		t.Fatal(res.Body)
	}
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/views", `{"name":"Slow hour","tool":"routes","settings":{"minMs":500}}`); res.Code != http.StatusOK {
		t.Fatal(res.Body)
	}
	base := "/api/cases/" + id + "/report"

	h := reportOf(t, e.json(http.MethodPost, base, fullReportRequest("html", "")))
	for _, want := range []string{
		"In a nutshell", "Checkout slowdown", "Ask the database team", "Slow hour",
		"Action time (C)", "Slow-end time (P95)", "Q1 Clock", "Trust data", reportNoPayloads,
	} {
		if !strings.Contains(h.Content, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(h.Content, reportNothingText) {
		t.Error("an analysed report carries the nothing-analysed notice")
	}
	if strings.Contains(h.Content, "<script") {
		t.Error("the report runs script")
	}

	// Parts can be left out; the always-printed ones stay.
	off := `,"include":{"figures":false,"suspects":false,"checklist":false,"sources":false}`
	h2 := reportOf(t, e.json(http.MethodPost, base, fullReportRequest("html", off)))
	if strings.Contains(h2.Content, "Ask the database team") || strings.Contains(h2.Content, "Slow-end time (P95)") || strings.Contains(h2.Content, "Q1 Clock") || strings.Contains(h2.Content, "Trust data") {
		t.Error("include=false still printed the part")
	}
	if !strings.Contains(h2.Content, "In a nutshell") || !strings.Contains(h2.Content, reportNoPayloads) {
		t.Error("the always-printed parts went missing")
	}
	if len(h2.Content) >= len(h.Content) {
		t.Errorf("leaving parts out did not shrink the report: %d >= %d", len(h2.Content), len(h.Content))
	}

	c := reportOf(t, e.json(http.MethodPost, base, fullReportRequest("csv", "")))
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(c.Content, "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatalf("csv does not parse: %v", err)
	}
	seen := map[string]int{}
	for _, r := range rows {
		if len(r) != 6 {
			t.Fatalf("row with %d cells: %v", len(r), r)
		}
		seen[r[0]]++
	}
	for _, sec := range []string{"case", "headline", "confidence", "origin", "suspect", "step", "checklist", "saved-view", "footer"} {
		if seen[sec] == 0 {
			t.Errorf("csv has no %q row (have %v)", sec, seen)
		}
	}
}

// The export is built from a narrow struct. This fails if a field that could
// carry a payload, a header, a person or a connection ever joins it.
func TestReportDocHasNoRoomForPayloads(t *testing.T) {
	banned := regexp.MustCompile(`(?i)payload|body|header|cookie|token|password|secret|user|client|agent|address|url|connection|ip$`)
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Slice || rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Array {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if banned.MatchString(f.Name) {
				t.Errorf("%s.%s looks like it could carry private data", path, f.Name)
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(reportDoc{}), "reportDoc")
	if len(seen) < 5 {
		t.Fatalf("walked only %d structs; the check is not looking at the document", len(seen))
	}
}

// End to end: private values placed in every audit field that holds them must
// not reach either file.
func TestReportNeverCarriesAuditPrivateFields(t *testing.T) {
	const (
		ip    = "203.0.113.77"
		agent = "SentinelAgent/9.9 (Secret-Browser)"
		query = "token=SENTINEL_QUERY_SECRET"
	)
	rows := invRows()
	uid := 987654321
	for i := range rows {
		rows[i].UserID = &uid
		rows[i].UserIP = ip
		rows[i].UserAgent = agent
		rows[i].URL = "/api/payroll/run?" + query
		rows[i].Status = "SENTINEL_STATUS_TEXT"
	}
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{requestPages: []audit.Page{okPage(rows)}})
	id := e.newCase("Sentinels")
	for _, format := range []string{"html", "csv"} {
		out := reportOf(t, e.json(http.MethodPost, "/api/cases/"+id+"/report", fullReportRequest(format, "")))
		for _, s := range []string{ip, agent, "SENTINEL_QUERY_SECRET", "SENTINEL_STATUS_TEXT", "987654321", "Secret-Browser"} {
			if strings.Contains(out.Content, s) {
				t.Errorf("%s report carries %q", format, s)
			}
		}
		if !strings.Contains(out.Content, "Action time (C)") {
			t.Errorf("%s report lost the route name; the sentinel test is not looking at a real report", format)
		}
	}
}

func TestReportCSVNeutralisesFormulas(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("=HYPERLINK(\"http://x\")")
	for _, text := range []string{`=1+1`, `+cmd`, `-2+3`, `@SUM(A1)`} {
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/checklist", `{"list":"todo","text":"`+text+`"}`); res.Code != http.StatusOK {
			t.Fatal(res.Body)
		}
	}
	out := reportOf(t, e.json(http.MethodPost, "/api/cases/"+id+"/report", reportBody("csv", "")))
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(out.Content, "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		for _, cell := range r {
			if cell != "" && strings.ContainsAny(cell[:1], "=+-@\t\r") {
				t.Errorf("cell %q would run as a formula", cell)
			}
		}
	}
}

func TestReportHTMLEscapesText(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase(`<img src=x onerror=alert(1)>`)
	if res := e.json(http.MethodPost, "/api/cases/"+id+"/checklist", `{"list":"todo","text":"<script>alert(2)</script>"}`); res.Code != http.StatusOK {
		t.Fatal(res.Body)
	}
	out := reportOf(t, e.json(http.MethodPost, "/api/cases/"+id+"/report", reportBody("html", "")))
	if strings.Contains(out.Content, "<img src=x") || strings.Contains(out.Content, "<script>alert") {
		t.Error("case text reached the report unescaped")
	}
}

func TestReportOfAClosedCaseStillWorks(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
			t.Fatalf("close = %d", res.Code)
		}
		out := reportOf(t, e.json(http.MethodPost, "/api/cases/"+id+"/report", reportBody("html", "")))
		if !strings.Contains(strings.ToLower(out.Content), "closed") {
			t.Error("a closed case's report does not say it is closed")
		}
	})
}
