package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
)

// l7ErrorText reads the "error" text of a JSON error answer.
func l7ErrorText(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("not a JSON error answer: %s", res.Body)
	}
	return out.Error
}

const l7Windows = `"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z","baselineStart":"2026-10-20T09:00:00Z","baselineEnd":"2026-10-20T10:00:00Z","bucketSeconds":60`

func TestScn1L7ValidationNamesTheProblem(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	pick := fmt.Sprintf("Pick 1 to %d sources.", audit.MaxSources)
	cases := []struct{ body, want string }{
		{`{}`, pick},
		{`{"sourceIds":[" "]}`, "One of the chosen sources is blank. Choose the sources again."},
		{`{"sourceIds":["a","a"]}`, "A source is chosen twice. Choose each source once."},
		{`{"sourceIds":["a"]}`, "Every start and end time is needed."},
		{`{"sourceIds":["a"],"start":"2026-10-20T11:00:00Z","end":"2026-10-20T10:00:00Z","baselineStart":"2026-10-20T08:00:00Z","baselineEnd":"2026-10-20T09:00:00Z","bucketSeconds":60}`, "The end of a time window must be after its start."},
		{`{"sourceIds":["a"],"start":"2026-10-01T10:00:00Z","end":"2026-10-20T11:00:00Z","baselineStart":"2026-09-30T09:00:00Z","baselineEnd":"2026-10-01T10:00:00Z","bucketSeconds":60}`, "A time window can be at most 7 days long."},
		{`{"sourceIds":["a"],"start":"2026-10-20T10:00:00Z","end":"2026-10-20T11:00:00Z","baselineStart":"2026-10-20T10:30:00Z","baselineEnd":"2026-10-20T10:45:00Z","bucketSeconds":60}`, "The normal window must end before the slow window starts."},
		{`{"sourceIds":["a"],` + strings.Replace(l7Windows, `"bucketSeconds":60`, `"bucketSeconds":5`, 1) + `}`, "The interval length must be between 10 seconds and 1 day."},
		{`{"sourceIds":["a"],` + l7Windows + `,"kind":"bogus"}`, "One of the filter choices is not valid."},
	}
	for _, c := range cases {
		res := request(h, http.MethodPost, "/api/analyze", c.body, true)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d %s", c.body, res.Code, res.Body)
			continue
		}
		if got := l7ErrorText(t, res); got != c.want {
			t.Errorf("%s: error = %q, want %q", c.body, got, c.want)
		}
		if strings.Contains(res.Body.String(), "Request validation failed") || strings.Contains(res.Body.String(), "bogus") {
			t.Errorf("%s: answer is bare or echoes input: %s", c.body, res.Body)
		}
	}
	for _, path := range []string{"/api/requests", "/api/export"} {
		res := request(h, http.MethodPost, path, `{}`, true)
		if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != pick {
			t.Errorf("%s: code = %d %s", path, res.Code, res.Body)
		}
	}
}

func TestScn1L7InvestigationAndReportNameTheProblem(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("inv")
	path := "/api/cases/" + id + "/investigation"
	cases := []struct{ body, want string }{
		{`{"sourceIds":["a"]}`, "Both windows need a start and an end, and each end must be after its start."},
		{invPost(`,"route":"` + strings.Repeat("x", 300) + `"`), "The route is longer than 256 characters."},
		{invPost(`,"poolLimit":0`), fmt.Sprintf("The pool limit must be a whole number from 1 to %d.", investigationMaxPool)},
		{strings.Replace(invPost(""), `2026-10-20T09:00:00Z`, `2026-09-01T09:00:00Z`, 1), "A time window can be at most 7 days long."},
		{invPost(`,"releases":[{"label":"x"}]`), "Each listed change needs a time."},
		{strings.Replace(invPost(""), `["demo-west"]`, `[]`, 1), fmt.Sprintf("Pick 1 to %d sources.", audit.MaxSources)},
	}
	for _, c := range cases {
		res := e.json(http.MethodPost, path, c.body)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d %s", c.body, res.Code, res.Body)
			continue
		}
		if got := l7ErrorText(t, res); got != c.want {
			t.Errorf("%s: error = %q, want %q", c.body, got, c.want)
		}
	}
	res := e.json(http.MethodPost, "/api/cases/"+id+"/report", `{"format":"pdf"}`)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "The report format must be html or csv." {
		t.Errorf("format: code = %d %s", res.Code, res.Body)
	}
	res = e.json(http.MethodPost, "/api/cases/"+id+"/report", `{"format":"html","request":{"sourceIds":["a"]}}`)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Both windows need a start and an end, and each end must be after its start." {
		t.Errorf("report request: code = %d %s", res.Code, res.Body)
	}
}

func TestScn1L7UnknownFieldIsNotInvalidJSON(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	res := request(h, http.MethodPost, "/api/record", `{"sourceId":"s","id":"r","readKey":"abc"}`, true)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Request body has a field this server does not know." {
		t.Fatalf("unknown field: code = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "readKey") || strings.Contains(res.Body.String(), "abc") {
		t.Errorf("answer echoes the field: %s", res.Body)
	}
	res = request(h, http.MethodPost, "/api/record", `{`, true)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Request body is invalid JSON." {
		t.Errorf("bad json: code = %d %s", res.Code, res.Body)
	}
	res = request(h, http.MethodPost, "/api/record", `{"sourceId":"s","id":"r"} {}`, true)
	if res.Code != http.StatusBadRequest || l7ErrorText(t, res) != "Request body must contain one JSON value." {
		t.Errorf("two values: code = %d %s", res.Code, res.Body)
	}
}
