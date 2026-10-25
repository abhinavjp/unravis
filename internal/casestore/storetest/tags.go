package storetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func tagger(t *testing.T, c casestore.Case) casestore.Tagger {
	t.Helper()
	g, ok := c.(casestore.Tagger)
	if !ok {
		t.Fatalf("%T does not implement casestore.Tagger", c)
	}
	return g
}

// tagsContract is the contract of the environment list and the source tags: add,
// list, remove, ordering, bounds, duplicates, isolation between cases, counts in
// Info, and refusal of writes on a closed case, a closed handle and an erased case.
func tagsContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a, b := e.newCase("a"), e.newCase("b")
	ta, tb := tagger(t, a), tagger(t, b)
	ctx := e.ctx

	// A new case has nothing, and says so as empty lists, not errors.
	if envs, err := ta.Envs(ctx); err != nil || len(envs) != 0 {
		t.Fatalf("fresh Envs: %v %v", envs, err)
	}
	if src, err := ta.Sources(ctx); err != nil || len(src) != 0 {
		t.Fatalf("fresh Sources: %v %v", src, err)
	}
	if i := a.Info(); i.Envs != 0 || i.Sources != 0 {
		t.Fatalf("fresh Info counts: %+v", i)
	}

	// Environments: order kept, trimmed, blanks dropped, case-insensitive duplicates
	// collapse to the first spelling, a second SetEnvs replaces the first.
	if err := ta.SetEnvs(ctx, []string{"  Production ", "", "staging", "PRODUCTION", "Dev", "   ", "STAGING"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"Production", "staging", "Dev"}
	if got, err := ta.Envs(ctx); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Envs = %v %v, want %v", got, err, want)
	}
	if i := a.Info(); i.Envs != 3 {
		t.Errorf("Info.Envs = %d, want 3", i.Envs)
	}
	if got, _ := tb.Envs(ctx); len(got) != 0 {
		t.Errorf("environments leaked into another case: %v", got)
	}
	if err := ta.SetEnvs(ctx, []string{"QA"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := ta.Envs(ctx); !reflect.DeepEqual(got, []string{"QA"}) || a.Info().Envs != 1 {
		t.Errorf("replace: %v, count %d", got, a.Info().Envs)
	}
	got, _ := ta.Envs(ctx)
	got[0] = "mutated"
	if again, _ := ta.Envs(ctx); again[0] != "QA" {
		t.Error("Envs returned the stored slice, not a copy")
	}

	// Environment bounds. A refused call changes nothing.
	for name, in := range map[string][]string{
		"too long":       {strings.Repeat("x", casestore.MaxEnvNameChars+1)},
		"control":        {"pro\x00d"},
		"newline":        {"pro\nd"},
		"bidi override":  {"pro‮d"},
		"invalid utf-8":  {"pro\xffd"},
		"one bad in set": {"fine", "tab\there"},
	} {
		if err := ta.SetEnvs(ctx, in); !errors.Is(err, casestore.ErrBadTag) {
			t.Errorf("%s: SetEnvs = %v, want ErrBadTag", name, err)
		}
	}
	if err := ta.SetEnvs(ctx, []string{strings.Repeat("é", casestore.MaxEnvNameChars)}); err != nil {
		t.Errorf("a name of exactly %d characters (multi-byte) was refused: %v", casestore.MaxEnvNameChars, err)
	}
	var many []string
	for i := 0; i < casestore.MaxEnvs+1; i++ {
		many = append(many, fmt.Sprintf("env-%d", i))
	}
	if err := ta.SetEnvs(ctx, many[:casestore.MaxEnvs]); err != nil {
		t.Fatalf("%d environments refused: %v", casestore.MaxEnvs, err)
	}
	if err := ta.SetEnvs(ctx, many); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("%d environments: %v, want ErrCapExceeded", len(many), err)
	}
	// Duplicates do not count against the cap.
	if err := ta.SetEnvs(ctx, append(append([]string{}, many[:casestore.MaxEnvs]...), "ENV-0", "Env-1")); err != nil {
		t.Errorf("duplicates counted against the cap: %v", err)
	}
	if got, _ := ta.Envs(ctx); len(got) != casestore.MaxEnvs || got[0] != "env-0" {
		t.Errorf("after refused calls: %d environments, first %q", len(got), got[0])
	}
	if err := ta.SetEnvs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := ta.Envs(ctx); len(got) != 0 || a.Info().Envs != 0 {
		t.Errorf("clear: %v, count %d", got, a.Info().Envs)
	}

	// Sources: ids, order, trimming, round trip.
	first, err := ta.AddSource(ctx, casestore.SourceTag{
		Kind: " mongo-db ", Label: " orders ", Env: " Production ", Portal: "Admin", API: "Orders", Classification: casestore.ClassAudit,
		ID: 99, // ignored
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID <= 0 || first.ID == 99 || first.Kind != "mongo-db" || first.Label != "orders" || first.Env != "Production" ||
		first.Portal != "Admin" || first.API != "Orders" || first.Classification != casestore.ClassAudit || first.Created.IsZero() {
		t.Fatalf("AddSource returned %+v", first)
	}
	second, err := ta.AddSource(ctx, casestore.SourceTag{Kind: "iis", ImportID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID <= first.ID || second.Label != "" || second.Env != "" || second.Classification != "" || second.ImportID != 7 {
		t.Fatalf("second source %+v", second)
	}
	list, err := ta.Sources(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("Sources: %v %v", list, err)
	}
	if list[0].ID != first.ID || list[1].ID != second.ID {
		t.Errorf("Sources order: %d %d", list[0].ID, list[1].ID)
	}
	if !reflect.DeepEqual(list[0], first) || !reflect.DeepEqual(list[1], second) {
		t.Errorf("round trip differs:\n got %+v\nwant %+v / %+v", list, first, second)
	}
	if i := a.Info(); i.Sources != 2 {
		t.Errorf("Info.Sources = %d, want 2", i.Sources)
	}
	if got, _ := tb.Sources(ctx); len(got) != 0 {
		t.Errorf("sources leaked into another case: %v", got)
	}
	if i := b.Info(); i.Sources != 0 {
		t.Errorf("another case's count moved: %+v", i)
	}

	// Source bounds. A refused call stores nothing.
	long := strings.Repeat("y", casestore.MaxTagTextChars+1)
	okLong := strings.Repeat("y", casestore.MaxTagTextChars)
	for name, s := range map[string]casestore.SourceTag{
		"no kind":          {},
		"blank kind":       {Kind: "  "},
		"long kind":        {Kind: long},
		"long label":       {Kind: "k", Label: long},
		"long portal":      {Kind: "k", Portal: long},
		"long api":         {Kind: "k", API: long},
		"long env":         {Kind: "k", Env: strings.Repeat("y", casestore.MaxEnvNameChars+1)},
		"control in label": {Kind: "k", Label: "a\x07b"},
		"newline in api":   {Kind: "k", API: "a\r\nb"},
		"bidi in portal":   {Kind: "k", Portal: "a‮b"},
		"bad class":        {Kind: "k", Classification: "Audit"},
		"unknown class":    {Kind: "k", Classification: "secret"},
		"negative import":  {Kind: "k", ImportID: -1},
	} {
		if _, err := ta.AddSource(ctx, s); !errors.Is(err, casestore.ErrBadTag) {
			t.Errorf("%s: AddSource = %v, want ErrBadTag", name, err)
		}
	}
	for _, class := range []string{"", casestore.ClassAudit, casestore.ClassOther, casestore.ClassUnused} {
		if _, err := tb.AddSource(ctx, casestore.SourceTag{Kind: "k", Classification: class, Label: okLong, Portal: okLong, API: okLong}); err != nil {
			t.Errorf("classification %q refused: %v", class, err)
		}
	}
	if got, _ := ta.Sources(ctx); len(got) != 2 {
		t.Errorf("refused sources were stored: %d", len(got))
	}
	// No error echoes what was typed.
	_, err = ta.AddSource(ctx, casestore.SourceTag{Kind: "k", Label: "SENTINEL-\x00-secret"})
	if err == nil || strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("error %v echoes the input", err)
	}

	// Remove: the right one goes, a missing id is not an error, ids are not reused
	// for a live row.
	if err := ta.RemoveSource(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := ta.Sources(ctx); len(got) != 1 || got[0].ID != second.ID || a.Info().Sources != 1 {
		t.Errorf("after remove: %v, count %d", got, a.Info().Sources)
	}
	if err := ta.RemoveSource(ctx, first.ID); err != nil {
		t.Errorf("removing a missing source: %v", err)
	}
	if err := ta.RemoveSource(ctx, -5); err != nil {
		t.Errorf("removing a nonsense id: %v", err)
	}
	if got, _ := tb.Sources(ctx); len(got) != 4 {
		t.Errorf("remove touched another case: %d sources", len(got))
	}

	// Cap on sources.
	c := e.newCase("cap")
	tc := tagger(t, c)
	for i := 0; i < casestore.MaxSources; i++ {
		if _, err := tc.AddSource(ctx, casestore.SourceTag{Kind: "k", Label: fmt.Sprint(i)}); err != nil {
			t.Fatalf("source %d: %v", i, err)
		}
	}
	if _, err := tc.AddSource(ctx, casestore.SourceTag{Kind: "k"}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("source %d: %v, want ErrCapExceeded", casestore.MaxSources+1, err)
	}
	if c.Info().Sources != casestore.MaxSources {
		t.Errorf("Info.Sources = %d", c.Info().Sources)
	}
	all, _ := tc.Sources(ctx)
	if err := tc.RemoveSource(ctx, all[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.AddSource(ctx, casestore.SourceTag{Kind: "k"}); err != nil {
		t.Errorf("adding after a remove at the cap: %v", err)
	}

	// A cancelled context stops a write before it changes anything.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := ta.SetEnvs(cctx, []string{"never"}); err == nil {
		t.Error("SetEnvs ran on a cancelled context")
	}
	if _, err := ta.AddSource(cctx, casestore.SourceTag{Kind: "k"}); err == nil {
		t.Error("AddSource ran on a cancelled context")
	}

	// Another handle of the same case sees the same tags; a closed handle refuses.
	again, err := e.store.Open(ctx, a.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := tagger(t, again).Sources(ctx); len(got) != 1 {
		t.Errorf("second handle sees %d sources, want 1", len(got))
	}
	if again.Info().Sources != 1 {
		t.Errorf("second handle Info.Sources = %d, want 1", again.Info().Sources)
	}
	_ = again.Close()
	if _, err := tagger(t, again).Sources(ctx); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle read: %v", err)
	}
	if err := tagger(t, again).SetEnvs(ctx, []string{"x"}); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle write: %v", err)
	}

	// A closed case keeps its tags readable and takes no more.
	if err := ta.SetEnvs(ctx, []string{"Live"}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.CloseCase(ctx, a.Info().ID); err != nil {
		t.Fatal(err)
	}
	if err := ta.SetEnvs(ctx, []string{"Other"}); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("SetEnvs on a closed case: %v", err)
	}
	if _, err := ta.AddSource(ctx, casestore.SourceTag{Kind: "k"}); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("AddSource on a closed case: %v", err)
	}
	if err := ta.RemoveSource(ctx, second.ID); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("RemoveSource on a closed case: %v", err)
	}
	if envs, err := ta.Envs(ctx); err != nil || !reflect.DeepEqual(envs, []string{"Live"}) {
		t.Errorf("closed case Envs: %v %v", envs, err)
	}
	if src, err := ta.Sources(ctx); err != nil || len(src) != 1 {
		t.Errorf("closed case Sources: %v %v", src, err)
	}

	// An erased case answers ErrNotFound to every call and keeps nothing.
	if err := e.store.Erase(ctx, b.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tb.Sources(ctx); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased Sources: %v", err)
	}
	if _, err := tb.Envs(ctx); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased Envs: %v", err)
	}
	if err := tb.SetEnvs(ctx, []string{"x"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased SetEnvs: %v", err)
	}
	if _, err := tb.AddSource(ctx, casestore.SourceTag{Kind: "k"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased AddSource: %v", err)
	}
	if err := tb.RemoveSource(ctx, 1); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased RemoveSource: %v", err)
	}
}
