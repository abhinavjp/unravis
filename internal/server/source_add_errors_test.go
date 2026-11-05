package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
	"context"
)

type addErrorRepository struct {
	*fakeRepository
	err error
}

func (r *addErrorRepository) AddSource(context.Context, audit.SourceInput) (audit.Source, error) {
	return audit.Source{}, r.err
}

func TestAddSourceFailuresAnswerWithTheRealReason(t *testing.T) {
	body := `{"label":"p","uri":"mongodb://db.example","database":"Payroll","collection":"audit"}`
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		hints  []string
	}{
		{"collection missing", &audit.ConnectionError{Stage: "source", Code: "collection_missing", Message: "no such collection", Collections: []string{"SystemApiAuditLog"}}, http.StatusBadRequest, "collection_missing", []string{"SystemApiAuditLog"}},
		{"no access", &audit.ConnectionError{Stage: "source", Code: "permission_denied", Message: "not allowed"}, http.StatusBadRequest, "permission_denied", nil},
		{"password rejected", &audit.ConnectionError{Stage: "authentication", Code: "credentials_rejected", Message: "rejected"}, http.StatusBadRequest, "credentials_rejected", nil},
		{"old server", &audit.ConnectionError{Stage: "source", Code: "unsupported_version", Message: "old"}, http.StatusBadRequest, "unsupported_version", nil},
		{"duplicate", fmt.Errorf("%w: source profile already exists: %w", audit.ErrInvalidInput, audit.ErrDuplicate), http.StatusConflict, "source-exists", nil},
		{"unknown", fmt.Errorf("%w: MongoDB source validation failed", audit.ErrInvalidInput), http.StatusBadRequest, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(&addErrorRepository{fakeRepository: &fakeRepository{}, err: tc.err}, nil, testToken, false)
			res := request(h, http.MethodPost, "/api/sources", body, true)
			if res.Code != tc.status {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			var out struct {
				Error       string   `json:"error"`
				Code        string   `json:"code"`
				Collections []string `json:"collections"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Code != tc.code || strings.Join(out.Collections, ",") != strings.Join(tc.hints, ",") {
				t.Fatalf("body=%s", res.Body.String())
			}
			if tc.code == "" && out.Error != "Request validation failed." {
				t.Fatalf("an unnamed failure keeps the generic text: %s", res.Body.String())
			}
		})
	}
}
