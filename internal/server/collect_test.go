package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

type collectAnswerBody struct {
	Items []struct {
		ID       string `json:"id"`
		Host     string `json:"host"`
		Title    string `json:"title"`
		Why      string `json:"why"`
		How      string `json:"how"`
		Importer string `json:"importer"`
		Rank     int    `json:"rank"`
	} `json:"items"`
}

// collectEnv registers toy importers under the names the suggestion rules read.
func collectEnv(t *testing.T, names ...string) *caseEnv {
	t.Helper()
	var imps []importer.Importer
	for _, n := range names {
		imps = append(imps, toyImporter{name: n, score: 90})
	}
	return newCaseEnv(t, envConfig{importers: imps})
}

func collectImport(t *testing.T, e *caseEnv, id, name, host string, rows ...string) {
	t.Helper()
	path := "/api/cases/" + id + "/imports?importer=" + name + "&host=" + host
	if res := e.upload(path, ackFields, &upload{name + ".csv", toyCSV(rows...)}); res.Code != http.StatusCreated {
		t.Fatalf("import %s = %d %s", name, res.Code, res.Body)
	}
}

func collectItems(t *testing.T, e *caseEnv, id string) collectAnswerBody {
	t.Helper()
	res := e.get("/api/cases/" + id + "/collect")
	if res.Code != http.StatusOK {
		t.Fatalf("collect = %d %s", res.Code, res.Body)
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type %q", ct)
	}
	if strings.Contains(res.Body.String(), `"items":null`) || !strings.Contains(res.Body.String(), `"items":[`) {
		t.Errorf("items is not a list: %s", res.Body)
	}
	var out collectAnswerBody
	decodeBody(t, res, &out)
	return out
}

func TestCollectEmptyCaseIsAnEmptyList(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Empty collect case")
	res := e.get("/api/cases/" + id + "/collect")
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty case = %d %s", res.Code, res.Body)
	}
}

func TestCollectIgnoresAnUnknownImporter(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e) // imported with the toy importer, which no rule reads
	if out := collectItems(t, e, id); len(out.Items) != 0 {
		t.Errorf("unknown importer gave %+v", out.Items)
	}
}

func TestCollectSuggestsFromWhatIsImported(t *testing.T) {
	e := collectEnv(t, "iis-w3c", "win-counters")
	id := e.newCase("Collect case")
	collectImport(t, e, id, "iis-w3c", "web-a", toyRow(1, "web-a", "a", 10))
	out := collectItems(t, e, id)
	if len(out.Items) != 6 {
		t.Fatalf("items: %+v", out.Items)
	}
	first := out.Items[0]
	if first.ID != "host-counters" || first.Host != "web-a" || first.Importer != "win-counters" || first.Rank != 1 {
		t.Errorf("first item: %+v", first)
	}
	for i, it := range out.Items {
		if it.Rank != i+1 || it.Title == "" || it.Why == "" || it.How == "" || it.Importer == "" {
			t.Errorf("item %d incomplete: %+v", i, it)
		}
		for _, text := range []string{it.Title, it.Why, it.How} {
			if err := evidence.LintCausal(text); err != nil {
				t.Errorf("item %s: %v", it.ID, err)
			}
		}
	}
	// Importing the counters takes that suggestion away.
	collectImport(t, e, id, "win-counters", "web-a", toyRow(2, "web-a", "b", 20))
	for _, it := range collectItems(t, e, id).Items {
		if it.ID == "host-counters" {
			t.Errorf("host counters still suggested: %+v", it)
		}
	}
}

func TestCollectNeverEchoesFileContents(t *testing.T) {
	e := collectEnv(t, "iis-w3c")
	id := e.newCase("Collect secret")
	collectImport(t, e, id, "iis-w3c", "web-a", toyRow(1, "web-a", secretCell, 10))
	res := e.get("/api/cases/" + id + "/collect")
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), secretCell) {
		t.Errorf("collect = %d %s", res.Code, res.Body)
	}
	// Nor does a query value come back.
	if body := e.get("/api/cases/" + id + "/collect?x=" + secretCell).Body.String(); strings.Contains(body, secretCell) {
		t.Errorf("response carries a query value: %s", body)
	}
}

func TestCollectNeedsTheTokenAndTheRightMethod(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Collect auth")
	path := "/api/cases/" + id + "/collect"
	for name, header := range map[string]string{"none": "", "wrong": "not-the-token"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if header != "" {
			req.Header.Set("X-Audit-Token", header)
		}
		if res := e.serve(req); res.Code != http.StatusUnauthorized {
			t.Errorf("%s token = %d %s, want 401", name, res.Code, res.Body)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Audit-Token", testToken)
		if res := e.serve(req); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d %s, want 405", method, res.Code, res.Body)
		}
	}
}

func TestCollectUnknownAndBadCaseIDs(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	res := e.get("/api/cases/" + strings.Repeat("a", 26) + "/collect")
	if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
		t.Errorf("unknown case = %d %s", res.Code, res.Body)
	}
	for _, id := range []string{"BAD", "short", strings.Repeat("a", 27), strings.Repeat("A", 26)} {
		res := e.get("/api/cases/" + id + "/collect")
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-case-id" {
			t.Errorf("id %q = %d %s", id, res.Code, res.Body)
		}
	}
}

func TestCollectWithoutAStoreIs503(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	res := request(h, http.MethodGet, "/api/cases/"+strings.Repeat("a", 26)+"/collect", "", true)
	if res.Code != http.StatusServiceUnavailable {
		t.Errorf("collect without a store = %d", res.Code)
	}
}

func TestCollectAfterEraseIs404(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Collect erase")
	path := "/api/cases/" + id + "/collect"
	if res := e.get(path); res.Code != http.StatusOK {
		t.Fatalf("before erase = %d %s", res.Code, res.Body)
	}
	if res := e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	res := e.get(path)
	if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
		t.Errorf("after erase = %d %s", res.Code, res.Body)
	}
}
