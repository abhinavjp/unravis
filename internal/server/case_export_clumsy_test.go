package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Clumsy-user cases for the case export: a page that previews, then the case
// moves under it; an empty case; a closed case; double clicks.

func clumsyPreview(t *testing.T, e *intEnv, id, extra string) exportPreviewResponse {
	t.Helper()
	res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"`+extra+`}`)
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	var out exportPreviewResponse
	decodeBody(t, res, &out)
	return out
}

func TestClumsyExportCaseChangesBetweenPreviewAndSave(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Moves under the page")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	p := clumsyPreview(t, e, id, "")

	// Another tab adds a second import.
	e.commit(id, "mongod-json", "mongo-a", intMongod)

	res := e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+p.PreviewID+`","caseId":"`+id+`"}`)
	if res.Code != http.StatusConflict {
		t.Fatalf("confirm after a change = %d %s, want 409", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), exportChangedMessage) {
		t.Errorf("409 lacks the fixed message: %s", res.Body)
	}
	if strings.Contains(res.Body.String(), intSentinel) {
		t.Error("409 body carries client data")
	}
	// A fresh preview then works.
	p2 := clumsyPreview(t, e, id, "")
	if p2.PreviewID == p.PreviewID {
		t.Error("preview id did not change after the case changed")
	}
	if got := e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+p2.PreviewID+`","caseId":"`+id+`"}`); got.Code != http.StatusOK {
		t.Errorf("confirm of the fresh preview = %d %s", got.Code, got.Body)
	}
}

func TestClumsyExportScrubFlippedAfterPreview(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Flip")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	on := clumsyPreview(t, e, id, "")
	off := clumsyPreview(t, e, id, `,"scrubSensitive":false`)
	if on.PreviewID == off.PreviewID {
		t.Fatal("scrub on and off share a preview id")
	}
	cases := map[string]string{
		"preview on, save off":  `{"previewId":"` + on.PreviewID + `","caseId":"` + id + `","scrubSensitive":false}`,
		"preview off, save on":  `{"previewId":"` + off.PreviewID + `","caseId":"` + id + `"}`,
		"preview off, save nil": `{"previewId":"` + off.PreviewID + `","caseId":"` + id + `","scrubSensitive":null}`,
	}
	for name, body := range cases {
		if res := e.json(http.MethodPost, "/api/export/confirm", body); res.Code != http.StatusConflict {
			t.Errorf("%s = %d %s, want 409", name, res.Code, res.Body)
		}
	}
}

func TestClumsyExportEmptyCase(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Nothing imported")
	res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
	if res.Code >= 500 {
		t.Fatalf("empty case preview = %d %s", res.Code, res.Body)
	}
	if res.Code != http.StatusOK {
		t.Logf("empty case preview answers %d", res.Code)
		return
	}
	var p exportPreviewResponse
	decodeBody(t, res, &p)
	c := e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+p.PreviewID+`","caseId":"`+id+`"}`)
	if c.Code >= 500 {
		t.Fatalf("empty case confirm = %d %s", c.Code, c.Body)
	}
}

func TestClumsyExportOddCaseIDs(t *testing.T) {
	e := newIntEnv(t, intRepo())
	for _, id := range []string{"", " ", "nope", "../x", "%00", strings.Repeat("a", 5000), "case-‮"} {
		body := `{"caseId":` + jsonString(id) + `}`
		if res := e.json(http.MethodPost, "/api/export/preview", body); res.Code >= 500 {
			t.Errorf("preview with case id %q... = %d", id[:min(len(id), 8)], res.Code)
		}
		cb := `{"previewId":"` + strings.Repeat("0", 64) + `","caseId":` + jsonString(id) + `}`
		if res := e.json(http.MethodPost, "/api/export/confirm", cb); res.Code >= 500 {
			t.Errorf("confirm with case id %q... = %d", id[:min(len(id), 8)], res.Code)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestClumsyExportClosedCaseStillExportsOrSaysWhy(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Closed then exported")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	if res := e.believing(http.MethodPost, "/api/cases/"+id+"/close", "", "open"); res.Code != http.StatusOK {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
	if res.Code >= 500 {
		t.Fatalf("closed case preview = %d %s", res.Code, res.Body)
	}
	t.Logf("closed case preview answers %d", res.Code)
}

func TestClumsyExportDoubleClickAndConcurrentPreviews(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Double click")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	const n = 6
	ids := make([]string, n)
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
			codes[i] = res.Code
			if res.Code == http.StatusOK {
				var out exportPreviewResponse
				decodeBody(t, res, &out)
				ids[i] = out.PreviewID
			}
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if codes[i] != http.StatusOK || ids[i] != ids[0] {
			t.Fatalf("concurrent preview %d = %d id %q, want 200 and the same id as the first", i, codes[i], ids[i])
		}
	}
	codes = make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+ids[0]+`","caseId":"`+id+`"}`).Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("concurrent confirm %d = %d, want 200", i, c)
		}
	}
}
