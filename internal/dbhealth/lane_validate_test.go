package dbhealth

import (
	"context"
	"strings"
	"testing"
)

// refuseCountQ records every query text it is sent.
type refuseCountQ struct{ sent []string }

func (c *refuseCountQ) Query(_ context.Context, text string) ([]Row, error) {
	c.sent = append(c.sent, text)
	return nil, nil
}

// BL-042: the lane runners check the request themselves, like the server does before calling them. A refused
// request sends no query and fails every check with the reason.
func TestLaneRunnersRefuseAnInvalidRequestWithoutQuerying(t *testing.T) {
	bad := laneReq
	bad.Reference = laneInc // the reference must end before the incident starts
	want := bad.Validate()
	if want == nil {
		t.Fatal("test request should be invalid")
	}

	q := &refuseCountQ{}
	r := RunSQLLane(context.Background(), q, "Prod", bad, fixedNow)
	if len(q.sent) != 0 {
		t.Fatalf("SQL lane sent %d queries for a refused request", len(q.sent))
	}
	checkRefused(t, "sql", r, want.Error())

	m := &fakeMongo{}
	r = RunMongoLane(context.Background(), m, "Prod", bad, fixedNow)
	if len(m.calls) != 0 {
		t.Fatalf("Mongo lane sent %d commands for a refused request", len(m.calls))
	}
	checkRefused(t, "mongo", r, want.Error())
}

func checkRefused(t *testing.T, kind string, r Report, reason string) {
	t.Helper()
	if r.Lane == nil || len(r.Lane.Checks) == 0 {
		t.Fatalf("%s: no checks in the report", kind)
	}
	for _, c := range r.Lane.Checks {
		if c.State != StateFailed || !strings.Contains(c.Summary, reason) {
			t.Errorf("%s %s = %s %q, want Failed with the reason", kind, c.ID, c.State, c.Summary)
		}
	}
	if len(r.Lane.Answers) != 0 {
		t.Errorf("%s answered %v for a refused request", kind, r.Lane.Answers)
	}
}
