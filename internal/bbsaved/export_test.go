package bbsaved

import "testing"

func TestExportImportKeepsIDsKeysAndSkipsDuplicates(t *testing.T) {
	old, _ := open(t)
	e, err := old.Add(AddInput{Name: "web-b", Location: loc(t, "web-b:7788"), Key: secret})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := old.Export()
	if err != nil || len(recs) != 1 {
		t.Fatalf("export: %v", err)
	}
	keeper, _ := open(t)
	if n, err := keeper.Import(recs); err != nil || n != 1 {
		t.Fatalf("import = %d, %v", n, err)
	}
	if k, err := keeper.Key(e.ID); err != nil || k != secret {
		t.Fatalf("key after import: %v", err)
	}
	if n, err := keeper.Import(recs); err != nil || n != 0 {
		t.Fatalf("second import = %d, %v", n, err)
	}
}
