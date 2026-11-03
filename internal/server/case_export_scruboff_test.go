package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestCaseExportScrubFieldAbsentOrNullIsOn(t *testing.T) {
	const onText = "Client IP addresses, referers and cookie digests are not in this file."
	const offText = "since scrubbing was turned off"
	e := newIntEnv(t, intRepo())
	id := e.newCase("Scrub field")
	e.commit(id, "iis-httperr", "web-a", intHTTPERR)

	for _, c := range []struct {
		name, field string
		wantScrub   bool
	}{
		{"absent", "", true},
		{"null", `,"scrubSensitive":null`, true},
		{"true", `,"scrubSensitive":true`, true},
		{"false", `,"scrubSensitive":false`, false},
	} {
		res := e.json(http.MethodPost, "/api/export/preview", `{"caseId":"`+id+`"`+c.field+`}`)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: preview = %d %s", c.name, res.Code, res.Body)
		}
		var pv exportPreviewResponse
		decodeBody(t, res, &pv)
		if pv.ScrubSensitive == nil || *pv.ScrubSensitive != c.wantScrub {
			t.Errorf("%s: preview scrubSensitive = %v, want %v", c.name, pv.ScrubSensitive, c.wantScrub)
		}
		res = e.json(http.MethodPost, "/api/export/confirm", `{"previewId":"`+pv.PreviewID+`","caseId":"`+id+`"`+c.field+`}`)
		if res.Code != http.StatusOK {
			t.Fatalf("%s: confirm = %d %s", c.name, res.Code, res.Body)
		}
		var cf exportConfirmResponse
		decodeBody(t, res, &cf)
		if got := strings.Contains(cf.Content, onText); got != c.wantScrub {
			t.Errorf("%s: content has the scrub-on statement = %v, want %v", c.name, got, c.wantScrub)
		}
		if got := strings.Contains(cf.Content, offText); got != !c.wantScrub {
			t.Errorf("%s: content has the scrub-off statement = %v, want %v", c.name, got, !c.wantScrub)
		}
	}
}
