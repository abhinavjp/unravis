package server

import (
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/sqlconn"
)

func TestSaveSQLConnectionFullListIsAClearConflict(t *testing.T) {
	saved := &fakeSavedSQL{saveErr: sqlconn.ErrFull}
	healthEnv(t, saved, func(t *testing.T, e *caseEnv, _ string) {
		res := e.json(http.MethodPost, "/api/sql-connections", `{"server":"db01","auth":"windows"}`)
		if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "20 saved") || !strings.Contains(res.Body.String(), "Remove one") {
			t.Fatalf("full = %d %s", res.Code, res.Body)
		}
	})
}
