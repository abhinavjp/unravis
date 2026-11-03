package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

var previewIDShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestCasePreviewIDDeterministicAndSensitive(t *testing.T) {
	base := casePreviewID("case-a", 3, true)
	if !previewIDShape.MatchString(base) {
		t.Fatalf("id is not 64 lowercase hex: %q", base)
	}
	if again := casePreviewID("case-a", 3, true); again != base {
		t.Errorf("same inputs gave %q and %q", base, again)
	}
	for name, other := range map[string]string{
		"case id":  casePreviewID("case-b", 3, true),
		"revision": casePreviewID("case-a", 4, true),
		"scrub":    casePreviewID("case-a", 3, false),
	} {
		if other == base {
			t.Errorf("changing the %s did not change the id", name)
		}
	}
	if !resolveScrub(nil) || !resolveScrub(boolPtr(true)) || resolveScrub(boolPtr(false)) {
		t.Errorf("resolveScrub: nil and true are on, false is off")
	}
}

func boolPtr(b bool) *bool { return &b }

func TestCaseExportScrubDefaultsOn(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Scrub default")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)

	type keys map[string]json.RawMessage
	preview := func(body string) exportPreviewResponse {
		res := e.json(http.MethodPost, "/api/export/preview", body)
		if res.Code != http.StatusOK {
			t.Fatalf("preview %s = %d %s", body, res.Code, res.Body)
		}
		var out exportPreviewResponse
		decodeBody(t, res, &out)
		return out
	}

	out := preview(`{"caseId":"` + id + `"}`)
	if out.ScrubSensitive == nil || !*out.ScrubSensitive {
		t.Errorf("scrubSensitive without the field = %v, want true", out.ScrubSensitive)
	}
	if out.SummarizesBytes == nil || *out.SummarizesBytes {
		t.Errorf("summarizesBytes = %v, want false", out.SummarizesBytes)
	}
	out = preview(`{"caseId":"` + id + `","scrubSensitive":false}`)
	if out.ScrubSensitive == nil || *out.ScrubSensitive {
		t.Errorf("scrubSensitive false = %v, want false", out.ScrubSensitive)
	}

	// A findings preview (no caseId) has neither key.
	installExportFakes(t)
	fr := request(demoExportHandler(), http.MethodPost, "/api/export/preview", exportBody("demo-west"), true)
	if fr.Code != http.StatusOK {
		t.Fatalf("findings preview = %d %s", fr.Code, fr.Body.String())
	}
	var raw keys
	if err := json.Unmarshal(fr.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"scrubSensitive", "summarizesBytes"} {
		if _, found := raw[k]; found {
			t.Errorf("findings preview carries %q", k)
		}
	}
}

func TestCaseExportPreviewIsStateless(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Stateless")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	pid := func() string {
		res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
		if res.Code != http.StatusOK {
			t.Fatalf("preview = %d %s", res.Code, res.Body)
		}
		var out exportPreviewResponse
		decodeBody(t, res, &out)
		return out.PreviewID
	}
	first, second := pid(), pid()
	if !previewIDShape.MatchString(first) || first != second {
		t.Fatalf("previews of an unchanged case gave %q and %q", first, second)
	}
	if n := len(e.handler().exports().items); n != 0 {
		t.Errorf("a case preview stored %d items", n)
	}
	for i := 0; i < 2; i++ {
		res := e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+first+`","caseId":"`+id+`"}`)
		if res.Code != http.StatusOK {
			t.Fatalf("confirm %d = %d %s", i+1, res.Code, res.Body)
		}
	}
	// Without the case id the confirm is a findings confirm and finds nothing.
	if res := e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+first+`"}`); res.Code != http.StatusNotFound {
		t.Errorf("confirm without caseId = %d, want 404", res.Code)
	}
}

func TestCaseExportConfirmWrongOrMissingIDIs409(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Wrong id")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
	var out exportPreviewResponse
	decodeBody(t, res, &out)

	for name, body := range map[string]string{
		"empty id":    `{"previewId":"","caseId":"` + id + `"}`,
		"absent id":   `{"caseId":"` + id + `"}`,
		"random id":   `{"previewId":"` + strings.Repeat("ab", 32) + `","caseId":"` + id + `"}`,
		"other scrub": `{"previewId":"` + out.PreviewID + `","caseId":"` + id + `","scrubSensitive":false}`,
	} {
		got := e.json(http.MethodPost, "/api/export/confirm", body)
		if got.Code != http.StatusConflict {
			t.Errorf("%s: confirm = %d %s, want 409", name, got.Code, got.Body)
			continue
		}
		if !strings.Contains(got.Body.String(), exportChangedMessage) {
			t.Errorf("%s: 409 lacks the fixed message: %s", name, got.Body)
		}
		if strings.Contains(got.Body.String(), strings.Repeat("ab", 32)) {
			t.Errorf("%s: 409 echoes the input", name)
		}
	}
	// The id of the other scrub value is accepted when the request says so.
	res = e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`","scrubSensitive":false}`)
	var off exportPreviewResponse
	decodeBody(t, res, &off)
	other := off.PreviewID
	if got := e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+other+`","caseId":"`+id+`","scrubSensitive":false}`); got.Code != http.StatusOK {
		t.Errorf("matching other-scrub confirm = %d %s", got.Code, got.Body)
	}
}

func TestCaseExportConfirmDoesNotPromiseIdenticalBytes(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Own hashes")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)
	res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"}`)
	var out exportPreviewResponse
	decodeBody(t, res, &out)
	for i := 0; i < 2; i++ {
		res := e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+out.PreviewID+`","caseId":"`+id+`"}`)
		if res.Code != http.StatusOK {
			t.Fatalf("confirm %d = %d %s", i+1, res.Code, res.Body)
		}
		var c exportConfirmResponse
		decodeBody(t, res, &c)
		if c.Content == "" || sha(c.Content) != c.SHA256 {
			t.Errorf("confirm %d: sha256 is not the hash of its own content", i+1)
		}
		if c.EventsCSV != "" && sha(c.EventsCSV) != c.EventsCSVSHA256 {
			t.Errorf("confirm %d: csv sha256 is not the hash of its own csv", i+1)
		}
	}
}
