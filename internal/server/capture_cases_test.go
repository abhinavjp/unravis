package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

func infoBody(t *testing.T, h http.Handler) (string, map[string]any) {
	t.Helper()
	res := request(h, http.MethodGet, "/api/info", "", true)
	if res.Code != http.StatusOK {
		t.Fatalf("/api/info status = %d", res.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return res.Body.String(), got
}

func TestInfoSaysCaptureCasesOnlyForTheCaptureDemo(t *testing.T) {
	plain := New(audit.NewDemo(time.Now()), nil, testToken, true)
	raw, _ := infoBody(t, plain)
	if strings.Contains(raw, "captureCases") {
		t.Errorf("plain --demo answer mentions captureCases: %s", raw)
	}

	capture := New(audit.NewDemo(time.Now()), nil, testToken, true, WithCaptureCases())
	if _, got := infoBody(t, capture); got["captureCases"] != true {
		t.Errorf("capture demo answer = %v", got)
	}

	// Live mode never reports it, even if the option were passed by mistake.
	live := New(audit.NewDemo(time.Now()), nil, testToken, false, WithCaptureCases())
	raw, _ = infoBody(t, live)
	if strings.Contains(raw, "captureCases") {
		t.Errorf("live answer mentions captureCases: %s", raw)
	}
}
