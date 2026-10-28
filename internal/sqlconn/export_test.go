package sqlconn

import "testing"

func TestExportImportKeepsIDsPasswordsAndSkipsDuplicates(t *testing.T) {
	old, _ := service(t)
	in := ssms()
	in.RememberPassword = true
	a, err := old.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Save(Input{Server: "db2", Auth: AuthWindows}); err != nil {
		t.Fatal(err)
	}
	recs, err := old.Export()
	if err != nil || len(recs) != 2 {
		t.Fatalf("export = %d, %v", len(recs), err)
	}
	keeper, _ := service(t)
	n, err := keeper.Import(recs)
	if err != nil || n != 2 {
		t.Fatalf("import = %d, %v", n, err)
	}
	got, err := keeper.Resolve(a.ID)
	if err != nil || got.Password != in.Password {
		t.Fatalf("resolve after import: %+v, %v", got, err)
	}
	if n, err := keeper.Import(recs); err != nil || n != 0 {
		t.Fatalf("second import = %d, %v", n, err)
	}
	bad := recs[0]
	bad.ID = ""
	if _, err := keeper.Import([]Record{bad}); err != ErrInvalid {
		t.Fatalf("invalid record: %v", err)
	}
}
