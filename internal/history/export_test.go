package history

import "testing"

func TestExportImportKeepsIDsPasswordsAndSkipsDuplicates(t *testing.T) {
	old, _ := testService(t)
	in := testConnection("mongodb://db.example:27017")
	in.Password = "mongo-secret"
	a, err := old.Remember("Prod", in, "auditlogs", true)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := old.Export()
	if err != nil || len(recs) != 1 || recs[0].Password != "mongo-secret" {
		t.Fatalf("export = %+v, %v", recs, err)
	}
	keeper, _ := testService(t)
	if n, err := keeper.Import(recs); err != nil || n != 1 {
		t.Fatalf("import = %d, %v", n, err)
	}
	lookup := testConnection("mongodb://db.example:27017")
	lookup.SavedConnectionID = a.ID
	got, err := keeper.Resolve(lookup)
	if err != nil || got.Password != "mongo-secret" {
		t.Fatalf("resolve after import: %v", err)
	}
	if n, err := keeper.Import(recs); err != nil || n != 0 {
		t.Fatalf("second import = %d, %v", n, err)
	}
}
