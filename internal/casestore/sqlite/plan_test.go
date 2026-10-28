package sqlite

import (
	"context"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
)

// A case made before saved views existed has neither table; it reads as "none
// saved" and the first write creates them. What is saved survives a reopen of the
// store,.
func TestPlanTablesAreCreatedOnFirstWriteAndSurviveReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := storetest.RandomProtector()
	s := newStoreAt(t, root, p)
	info := mustCreate(t, s, "plan case")
	c := mustOpen(t, s, info.ID)

	var tables int
	db, _ := c.handle()
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('saved_views','checklist_items')").Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("fresh case has %d plan tables (%v), want none", tables, err)
	}
	if v, err := c.SavedViews(ctx); err != nil || len(v) != 0 {
		t.Fatalf("fresh case views = %v %v", v, err)
	}
	if v, err := c.ChecklistItems(ctx); err != nil || len(v) != 0 {
		t.Fatalf("fresh case items = %v %v", v, err)
	}
	if _, err := c.SaveView(ctx, casestore.SavedView{Name: sentinel + "-view", Tool: "filters", Settings: []byte(`{"route":"A.B"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: sentinel + "-item"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := newStoreAt(t, root, p)
	c2 := mustOpen(t, s2, info.ID)
	views, err := c2.SavedViews(ctx)
	if err != nil || len(views) != 1 || !strings.HasSuffix(views[0].Name, "-view") || string(views[0].Settings) != `{"route":"A.B"}` {
		t.Fatalf("reopened views = %+v %v", views, err)
	}
	items, err := c2.ChecklistItems(ctx)
	if err != nil || len(items) != 1 || !strings.HasSuffix(items[0].Text, "-item") {
		t.Fatalf("reopened items = %+v %v", items, err)
	}
}
