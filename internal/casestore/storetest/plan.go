package storetest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func planner(t *testing.T, c casestore.Case) casestore.PlanStore {
	t.Helper()
	p, ok := c.(casestore.PlanStore)
	if !ok {
		t.Fatalf("%T does not implement casestore.PlanStore", c)
	}
	return p
}

// planContract is the contract of the saved views and the checklist: none until
// saved, an id and a time made by the store, replace by id, delete, order, the
// bounds, kept per case, readable on a closed case but not writable, and refused
// on a closed handle or an erased case.
func planContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a, b := e.newCase("a"), e.newCase("b")
	pa, pb := planner(t, a), planner(t, b)
	ctx := e.ctx

	// Fresh case: nothing, and the lists are empty (not nil) so they encode as [].
	if v, err := pa.SavedViews(ctx); err != nil || v == nil || len(v) != 0 {
		t.Fatalf("fresh SavedViews = %v %v", v, err)
	}
	if v, err := pa.ChecklistItems(ctx); err != nil || v == nil || len(v) != 0 {
		t.Fatalf("fresh ChecklistItems = %v %v", v, err)
	}

	// Views: the store makes the id and the time; the settings come back compacted.
	v1, err := pa.SaveView(ctx, casestore.SavedView{Name: "  Slow checkout  ", Tool: "filters", Settings: []byte(`{ "route": "Checkout.Pay",  "minMs": 500 }`)})
	if err != nil {
		t.Fatal(err)
	}
	if !casestore.ValidPlanID(v1.ID) || v1.Saved.IsZero() || v1.Name != "Slow checkout" || string(v1.Settings) != `{"route":"Checkout.Pay","minMs":500}` {
		t.Fatalf("saved view = %+v", v1)
	}
	v2, err := pa.SaveView(ctx, casestore.SavedView{Name: "Second", Tool: "filters"})
	if err != nil || string(v2.Settings) != `{}` {
		t.Fatalf("view without settings = %+v %v", v2, err)
	}
	got, _ := pa.SavedViews(ctx)
	if len(got) != 2 {
		t.Fatalf("views = %+v", got)
	}
	// Replace by id.
	repl, err := pa.SaveView(ctx, casestore.SavedView{ID: v1.ID, Name: "Renamed", Tool: "filters", Settings: []byte(`{"x":1}`)})
	if err != nil || repl.ID != v1.ID {
		t.Fatalf("replace = %+v %v", repl, err)
	}
	got, _ = pa.SavedViews(ctx)
	if len(got) != 2 {
		t.Fatalf("replace changed the count: %d", len(got))
	}
	for _, v := range got {
		if v.ID == v1.ID && (v.Name != "Renamed" || string(v.Settings) != `{"x":1}`) {
			t.Errorf("replaced view reads %+v", v)
		}
	}
	// An id that does not exist is not created by a save.
	if _, err := pa.SaveView(ctx, casestore.SavedView{ID: strings.Repeat("a", 32), Name: "Ghost", Tool: "filters"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("save of an unknown id: %v", err)
	}
	// Rules.
	for name, bad := range map[string]casestore.SavedView{
		"empty name":      {Name: "  ", Tool: "filters"},
		"long name":       {Name: strings.Repeat("n", casestore.MaxViewName+1), Tool: "filters"},
		"control in name": {Name: "a\x00b", Tool: "filters"},
		"empty tool":      {Name: "x"},
		"odd tool":        {Name: "x", Tool: "a b/c"},
		"settings array":  {Name: "x", Tool: "t", Settings: []byte(`[1]`)},
		"settings text":   {Name: "x", Tool: "t", Settings: []byte(`nope`)},
		"settings null":   {Name: "x", Tool: "t", Settings: []byte(`null`)},
		"bad id":          {ID: "not-an-id", Name: "x", Tool: "t"},
	} {
		if _, err := pa.SaveView(ctx, bad); !errors.Is(err, casestore.ErrBadPlan) {
			t.Errorf("%s: %v", name, err)
		}
	}
	big := `{"k":"` + strings.Repeat("a", casestore.MaxViewSettings) + `"}`
	if _, err := pa.SaveView(ctx, casestore.SavedView{Name: "big", Tool: "t", Settings: []byte(big)}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("settings over the cap: %v", err)
	}
	// Delete.
	if err := pa.DeleteView(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	if err := pa.DeleteView(ctx, v2.ID); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	if err := pa.DeleteView(ctx, "nonsense"); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("delete of a malformed id: %v", err)
	}
	// The cap: MaxSavedViews views fit, one more does not, and a replace still works at the cap.
	for i := 1; i < casestore.MaxSavedViews; i++ {
		if _, err := pb.SaveView(ctx, casestore.SavedView{Name: fmt.Sprintf("v%d", i), Tool: "filters"}); err != nil {
			t.Fatalf("view %d: %v", i, err)
		}
	}
	last, err := pb.SaveView(ctx, casestore.SavedView{Name: "last", Tool: "filters"})
	if err != nil {
		t.Fatalf("view at the cap: %v", err)
	}
	if _, err := pb.SaveView(ctx, casestore.SavedView{Name: "over", Tool: "filters"}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("view over the cap: %v", err)
	}
	if _, err := pb.SaveView(ctx, casestore.SavedView{ID: last.ID, Name: "last again", Tool: "filters"}); err != nil {
		t.Errorf("replace at the cap: %v", err)
	}
	if all, _ := pb.SavedViews(ctx); len(all) != casestore.MaxSavedViews {
		t.Errorf("cap case holds %d views", len(all))
	}
	if all, _ := pa.SavedViews(ctx); len(all) != 1 {
		t.Errorf("views leaked between cases: %d", len(all))
	}

	// Checklist.
	i1, err := pa.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListDoing, Text: " Check the pool ", Suspect: "N1", Order: 2})
	if err != nil || !casestore.ValidPlanID(i1.ID) || i1.Created.IsZero() || i1.Text != "Check the pool" {
		t.Fatalf("item = %+v %v", i1, err)
	}
	i2, _ := pa.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "Ask for the IIS logs", Order: 5})
	i3, _ := pa.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "Ask about the release", Order: 1})
	i4, _ := pa.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListRuledOut, Text: "The network"})
	i5, _ := pa.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListLearned, Text: "It began at 09:12"})
	items, _ := pa.ChecklistItems(ctx)
	var order []string
	for _, it := range items {
		order = append(order, it.ID)
	}
	want := []string{i3.ID, i2.ID, i1.ID, i5.ID, i4.ID}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("item order = %v, want %v", order, want)
	}
	// Move to another list, keep the creation time.
	moved, err := pa.PutChecklistItem(ctx, casestore.ChecklistItem{ID: i1.ID, List: casestore.ListLearned, Text: "The pool was full", Suspect: "N1"})
	if err != nil || !moved.Created.Equal(i1.Created) || moved.List != casestore.ListLearned {
		t.Fatalf("move = %+v %v", moved, err)
	}
	if _, err := pa.PutChecklistItem(ctx, casestore.ChecklistItem{ID: strings.Repeat("b", 32), List: casestore.ListToDo, Text: "ghost"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("put of an unknown id: %v", err)
	}
	for name, bad := range map[string]casestore.ChecklistItem{
		"no list":      {Text: "x"},
		"odd list":     {List: "done", Text: "x"},
		"empty text":   {List: casestore.ListToDo, Text: " "},
		"long text":    {List: casestore.ListToDo, Text: strings.Repeat("t", casestore.MaxChecklistText+1)},
		"odd suspect":  {List: casestore.ListToDo, Text: "x", Suspect: "N 1"},
		"long suspect": {List: casestore.ListToDo, Text: "x", Suspect: strings.Repeat("N", 40)},
		"negative":     {List: casestore.ListToDo, Text: "x", Order: -2},
		"bad id":       {ID: "zz", List: casestore.ListToDo, Text: "x"},
	} {
		if _, err := pa.PutChecklistItem(ctx, bad); !errors.Is(err, casestore.ErrBadPlan) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// OrderAuto is chosen inside the write: adds made at the same moment never share a
	// place, a move goes last in the new list, and an edit in place keeps its place.
	const adds = 8
	var wg sync.WaitGroup
	par := make([]casestore.ChecklistItem, adds)
	for i := 0; i < adds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			par[i], _ = pa.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListDoing, Text: fmt.Sprintf("parallel %d", i), Order: casestore.OrderAuto})
		}(i)
	}
	wg.Wait()
	places := map[int]bool{}
	for _, it := range par {
		if it.ID == "" {
			t.Fatalf("parallel add failed: %+v", it)
		}
		if places[it.Order] {
			t.Errorf("two parallel adds share the place %d", it.Order)
		}
		places[it.Order] = true
	}
	stay, err := pa.PutChecklistItem(ctx, casestore.ChecklistItem{ID: par[3].ID, List: casestore.ListDoing, Text: "parallel 3, edited", Order: casestore.OrderAuto})
	if err != nil || stay.Order != par[3].Order {
		t.Errorf("an edit in place moved the item: %+v then %+v (%v)", par[3], stay, err)
	}
	tail, err2 := pa.PutChecklistItem(ctx, casestore.ChecklistItem{ID: par[0].ID, List: casestore.ListRuledOut, Text: "parallel 0, moved", Order: casestore.OrderAuto})
	if err2 != nil || tail.Order <= i4.Order {
		t.Errorf("a moved item did not go last in its new list: %+v (%v)", tail, err2)
	}
	if err := pa.DeleteChecklistItem(ctx, i4.ID); err != nil {
		t.Fatal(err)
	}
	if err := pa.DeleteChecklistItem(ctx, i4.ID); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("second item delete: %v", err)
	}
	for i := 0; i < casestore.MaxChecklistItems; i++ {
		if _, err := pb.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: fmt.Sprintf("item %d", i), Order: i}); err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
	}
	if _, err := pb.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "over"}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("item over the cap: %v", err)
	}

	// A cancelled context stops a write before it changes anything.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := pa.SaveView(cctx, casestore.SavedView{Name: "never", Tool: "t"}); err == nil {
		t.Error("SaveView ran on a cancelled context")
	}
	if got, _ := pa.SavedViews(ctx); len(got) != 1 {
		t.Errorf("cancelled write changed the views: %d", len(got))
	}

	// Another handle sees both; a closed handle refuses.
	again, err := e.store.Open(ctx, a.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := planner(t, again).SavedViews(ctx); len(got) != 1 {
		t.Errorf("second handle reads %d views", len(got))
	}
	_ = again.Close()
	if _, err := planner(t, again).SavedViews(ctx); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle read: %v", err)
	}
	if _, err := planner(t, again).SaveView(ctx, casestore.SavedView{Name: "x", Tool: "t"}); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle write: %v", err)
	}

	// A closed case keeps both readable and takes no more.
	before, _ := pa.ChecklistItems(ctx)
	if err := e.store.CloseCase(ctx, a.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pa.SaveView(ctx, casestore.SavedView{Name: "late", Tool: "t"}); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("SaveView on a closed case: %v", err)
	}
	if err := pa.DeleteView(ctx, v1.ID); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("DeleteView on a closed case: %v", err)
	}
	if _, err := pa.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "late"}); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("PutChecklistItem on a closed case: %v", err)
	}
	if err := pa.DeleteChecklistItem(ctx, i2.ID); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("DeleteChecklistItem on a closed case: %v", err)
	}
	if got, err := pa.SavedViews(ctx); err != nil || len(got) != 1 {
		t.Errorf("closed case views = %d %v", len(got), err)
	}
	if got, err := pa.ChecklistItems(ctx); err != nil || len(got) != len(before) {
		t.Errorf("closed case items = %d %v", len(got), err)
	}

	// An erased case answers ErrNotFound and keeps nothing.
	if err := e.store.Erase(ctx, b.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pb.SavedViews(ctx); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased SavedViews: %v", err)
	}
	if _, err := pb.SaveView(ctx, casestore.SavedView{Name: "x", Tool: "t"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased SaveView: %v", err)
	}
	if _, err := pb.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "x"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased PutChecklistItem: %v", err)
	}
}
