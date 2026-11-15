package server

import (
	"api-audit-explorer/internal/audit"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type activityTestRepository struct {
	fakeRepository
	calls int
}

func (r *activityTestRepository) Activity(context.Context, audit.ActivityInput) (audit.Page, error) {
	r.calls++
	return audit.Page{Rows: []audit.RequestRecord{{URL: "private-query", Controller: "Payroll"}}, Sources: []audit.SourceResult{{SourceID: "s", OK: true}}}, nil
}
func TestActivityEndpointValidatesAndStripsURL(t *testing.T) {
	repo := &activityTestRepository{}
	h := New(repo, nil, testToken, false)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	input := audit.ActivityInput{SourceIDs: []string{"s"}, Start: at, End: at.Add(time.Minute), Limit: 100}
	body, _ := json.Marshal(input)
	res := request(h, http.MethodPost, "/api/activity", string(body), true)
	if res.Code != 200 || repo.calls != 1 {
		t.Fatalf("status%d calls%d", res.Code, repo.calls)
	}
	var page audit.Page
	if json.Unmarshal(res.Body.Bytes(), &page) != nil || page.Rows[0].URL != "" {
		t.Fatal("activity leaked URL")
	}
	input.End = at.Add(48 * time.Hour)
	body, _ = json.Marshal(input)
	res = request(h, http.MethodPost, "/api/activity", string(body), true)
	if res.Code != 400 || repo.calls != 1 {
		t.Fatal("unbounded activity executed")
	}
}

// activityDemoBody builds the JSON the screen sends. extra is spliced in as
// already-encoded `"key":value` pairs.
func activityDemoBody(now time.Time, extra string) string {
	body := `{"sourceIds":["demo-west"],"start":"` + now.Add(-30*time.Minute).Format(time.RFC3339) +
		`","end":"` + now.Add(time.Minute).Format(time.RFC3339) + `","limit":100`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

func activityDemoPage(t *testing.T, h http.Handler, body string) audit.Page {
	t.Helper()
	res := request(h, http.MethodPost, "/api/activity", body, true)
	if res.Code != 200 {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var page audit.Page
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestActivityEndpointUserAndOperationFilters(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h := New(audit.NewDemo(now), nil, testToken, true)

	all := activityDemoPage(t, h, activityDemoBody(now, ""))
	if len(all.Rows) == 0 || all.Rows[0].UserID == nil {
		t.Fatalf("demo gave no rows with a user: %d rows", len(all.Rows))
	}
	user, operation := *all.Rows[0].UserID, all.Rows[0].Controller+"."+all.Rows[0].Action

	both := activityDemoPage(t, h, activityDemoBody(now, `"userId":`+strconv.Itoa(user)+`,"operation":"`+operation+`"`))
	if len(both.Rows) == 0 || len(both.Rows) > len(all.Rows) {
		t.Fatalf("filtered rows %d of %d", len(both.Rows), len(all.Rows))
	}
	for _, r := range both.Rows {
		if r.UserID == nil || *r.UserID != user || r.Controller+"."+r.Action != operation {
			t.Fatal("a row does not match both filters")
		}
	}

	// Regression: the same body without the new fields, and with them written empty, gives the same rows.
	again := activityDemoPage(t, h, activityDemoBody(now, ""))
	if len(again.Rows) != len(all.Rows) || again.Rows[0].ID != all.Rows[0].ID {
		t.Fatal("body without the new fields changed")
	}
	if p := activityDemoPage(t, h, activityDemoBody(now, `"operation":""`)); len(p.Rows) != len(all.Rows) {
		t.Fatalf("empty operation gave %d rows, want %d", len(p.Rows), len(all.Rows))
	}
}

func TestActivityEndpointRefusesBadFilters(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h := New(audit.NewDemo(now), nil, testToken, true)
	for name, extra := range map[string]string{
		"negative user":         `"userId":-1`,
		"operation no dot":      `"operation":"Orders"`,
		"operation with dollar": `"operation":"A.$where"`,
		"operation as object":   `"operation":{"$ne":1}`,
		"user as text":          `"userId":"7"`,
		"unknown key":           `"user":7`,
	} {
		res := request(h, http.MethodPost, "/api/activity", activityDemoBody(now, extra), true)
		if res.Code != 400 {
			t.Errorf("%s: status %d, want 400", name, res.Code)
		}
		if strings.Contains(res.Body.String(), "$where") || strings.Contains(res.Body.String(), "$ne") {
			t.Errorf("%s: the error body echoes the bad value: %s", name, res.Body)
		}
	}
}
