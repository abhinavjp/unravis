package audit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestActivityIncludesKnownOverlapsWithoutInventingUnknownSpans(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	before := start.Add(-time.Hour)
	during := start.Add(time.Second)
	exact := start
	negative := before.Add(-time.Second)
	for _, tc := range []struct {
		name     string
		at, done *time.Time
		want     bool
	}{{"already_running", &before, &during, true}, {"finished_at_boundary", &before, &exact, false}, {"unknown_old", &before, nil, false}, {"unknown_inside", &during, nil, true}, {"negative_span", &before, &negative, false}, {"after_window", &end, nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ActivityOverlaps(tc.at, tc.done, start, end); got != tc.want {
				t.Fatalf("overlap=%v want%v", got, tc.want)
			}
		})
	}
	if _, err := ActivityQuery(ActivityInput{SourceIDs: []string{"s"}, Start: start, End: start.Add(25 * time.Hour), Limit: 100}); err == nil {
		t.Fatal("unbounded window accepted")
	}
}

func TestActivityQueryMessagesAreReadable(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	_, err := ActivityQuery(ActivityInput{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(25 * time.Hour), Limit: 10})
	if err == nil || !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "at most 24 hours") || strings.Contains(err.Error(), "at most24hours") {
		t.Fatalf("window error = %v", err)
	}
	for _, in := range []ActivityInput{
		{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 0},
		{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 101},
		{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 10, Cursor: strings.Repeat("x", 4097)},
	} {
		_, err := ActivityQuery(in)
		if err == nil || !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "limit must be 1 to 100") || !strings.Contains(err.Error(), "4096 bytes") {
			t.Fatalf("page error for %+v = %v", in, err)
		}
	}
}

func intPtr(n int) *int { return &n }

func TestActivityInputFilterValidation(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	base := ActivityInput{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 10}
	long := strings.Repeat("a", 300)
	for _, tc := range []struct {
		name string
		user *int
		op   string
		ok   bool
	}{
		{"nothing", nil, "", true},
		{"user zero", intPtr(0), "", true},
		{"user seven", intPtr(7), "", true},
		{"operation", nil, "Orders.Get", true},
		{"underscores and digits", nil, "Orders_2.Get_v3", true},
		{"both", intPtr(7), "Orders.Get", true},
		{"negative user", intPtr(-1), "", false},
		{"no dot", nil, "Orders", false},
		{"two dots", nil, "Orders.Get.More", false},
		{"empty side left", nil, ".Get", false},
		{"empty side right", nil, "Orders.", false},
		{"dollar", nil, "Orders.$where", false},
		{"json object", nil, `{"$ne":1}.x`, false},
		{"long name", nil, long + ".Get", false},
		{"long action", nil, "Orders." + long, false},
		{"hundred characters ok", nil, strings.Repeat("a", 100) + "." + strings.Repeat("b", 100), true},
		{"space", nil, "Orders.Get It", false},
		{"nul", nil, "Orders.Get\x00", false},
		{"non ascii", nil, "Orders.Gét", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.UserID, in.Operation = tc.user, tc.op
			_, err := ActivityQuery(in)
			if tc.ok {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("accepted or wrong error: %v", err)
			}
			if tc.op != "" && strings.Contains(err.Error(), tc.op) {
				t.Fatalf("refusal echoes the operation: %q", err.Error())
			}
		})
	}
}

func TestActivityRefusalDoesNotEchoHostileOperation(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, hostile := range []string{"Orders.$where", `{"$ne":1}.x`, "Orders.Get.More", "Evil Name.Get"} {
		_, err := ActivityQuery(ActivityInput{SourceIDs: []string{"s1"}, Start: t0, End: t0.Add(time.Hour), Limit: 10, Operation: hostile})
		if err == nil || strings.Contains(err.Error(), hostile) || strings.Contains(err.Error(), "$where") {
			t.Fatalf("hostile %q: err = %v", hostile, err)
		}
	}
}

func demoActivityRows(t *testing.T, in ActivityInput) []RequestRecord {
	t.Helper()
	d := NewDemo(anchor).(*demoRepository)
	var rows []RequestRecord
	for i := 0; i < 100; i++ {
		page, err := d.Activity(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, page.Rows...)
		if page.NextCursor == "" {
			return rows
		}
		in.Cursor = page.NextCursor
	}
	t.Fatal("paging did not finish")
	return nil
}

func TestDemoActivityHonoursUserAndOperation(t *testing.T) {
	q := demoSlowQuery()
	in := ActivityInput{SourceIDs: q.SourceIDs, Start: q.End.Add(-24 * time.Hour), End: q.End, Limit: 100}
	all := demoActivityRows(t, in)
	if len(all) == 0 {
		t.Fatal("fixture has no activity rows")
	}
	user, op := *all[0].UserID, all[0].Controller+"."+all[0].Action
	wantUser, wantOp, wantBoth := 0, 0, 0
	for _, r := range all {
		u, o := r.UserID != nil && *r.UserID == user, r.Controller+"."+r.Action == op
		if u {
			wantUser++
		}
		if o {
			wantOp++
		}
		if u && o {
			wantBoth++
		}
	}
	if wantUser == len(all) || wantOp == len(all) {
		t.Fatalf("fixture is vacuous: user %d op %d of %d", wantUser, wantOp, len(all))
	}

	byUser := in
	byUser.UserID = &user
	rows := demoActivityRows(t, byUser)
	if len(rows) != wantUser {
		t.Fatalf("user filter returned %d rows, want %d", len(rows), wantUser)
	}
	for _, r := range rows {
		if r.UserID == nil || *r.UserID != user {
			t.Fatalf("row of another user: %+v", r.UserID)
		}
	}

	byOp := in
	byOp.Operation = op
	rows = demoActivityRows(t, byOp)
	if len(rows) != wantOp {
		t.Fatalf("operation filter returned %d rows, want %d", len(rows), wantOp)
	}
	for _, r := range rows {
		if r.Controller+"."+r.Action != op {
			t.Fatalf("row of another API: %s.%s", r.Controller, r.Action)
		}
	}
	lower := in
	lower.Operation = strings.ToLower(op)
	if got := demoActivityRows(t, lower); len(got) != 0 {
		t.Fatalf("operation match is case-sensitive, got %d rows for %q", len(got), lower.Operation)
	}

	both := in
	both.UserID, both.Operation = &user, op
	rows = demoActivityRows(t, both)
	if len(rows) != wantBoth || wantBoth == 0 {
		t.Fatalf("combined filter returned %d rows, want %d", len(rows), wantBoth)
	}

	none := in
	none.UserID = intPtr(999999)
	if got := demoActivityRows(t, none); len(got) != 0 {
		t.Fatalf("unknown user returned %d rows", len(got))
	}
}

func TestDemoActivityCursorIsBoundToTheFilters(t *testing.T) {
	d := NewDemo(anchor).(*demoRepository)
	q := demoSlowQuery()
	in := ActivityInput{SourceIDs: q.SourceIDs, Start: q.End.Add(-24 * time.Hour), End: q.End, Limit: 5}
	p, err := d.Activity(context.Background(), in)
	if err != nil || p.NextCursor == "" {
		t.Fatalf("%v", err)
	}
	other := in
	other.Cursor = p.NextCursor
	other.UserID = intPtr(1001)
	if _, err := d.Activity(context.Background(), other); err == nil {
		t.Fatal("a cursor from another filter was accepted")
	}
}
