package sqlconn

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

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

// Before the fix, Import kept a record's own Server text, so a record that said Host "payroll-db" could still dial "attacker.example".
func TestImportRebuildsServerFromHostAndPortNeverFromTheRecordServer(t *testing.T) {
	keeper, _ := service(t)
	planted := Record{Recent: Recent{
		ID: "abc", Label: "Payroll DB", Server: "attacker.example,1433", Host: "payroll-db", Port: 1433,
		Auth: AuthSQL, Username: "sa", Encrypt: EncryptMandatory,
	}, Password: "pw-for-payroll"}
	planted.HasPassword = true
	if n, err := keeper.Import([]Record{planted}); err != nil || n != 1 {
		t.Fatalf("import = %d, %v", n, err)
	}
	got, err := keeper.Resolve("abc")
	if err != nil {
		t.Fatal(err)
	}
	if got.Server != "payroll-db,1433" || got.Label != "Payroll DB" || got.Password != "pw-for-payroll" {
		t.Fatalf("resolved %+v: the dialled server must come from host and port", got)
	}
	list, err := keeper.List()
	if err != nil || len(list) != 1 || list[0].Server != "payroll-db,1433" || list[0].Host != "payroll-db" || list[0].Port != 1433 {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestImportRefusesRecordsSaveCouldNeverMakeAndWritesNothing(t *testing.T) {
	good := Record{Recent: Recent{ID: "good", Label: "ok", Host: "db1", Port: 1433, Auth: AuthWindows, Encrypt: EncryptMandatory}}
	with := func(edit func(*Record)) Record {
		r := Record{Recent: Recent{ID: "bad", Label: "x", Host: "db2", Port: 1433, Auth: AuthSQL, Username: "sa", Encrypt: EncryptMandatory}}
		edit(&r)
		return r
	}
	cases := map[string]Record{
		"unknown auth":             with(func(r *Record) { r.Auth = "bogus-auth" }),
		"unknown encrypt":          with(func(r *Record) { r.Encrypt = "whatever" }),
		"sql auth without a login": with(func(r *Record) { r.Username = "" }),
		"port too high":            with(func(r *Record) { r.Port = 70000 }),
		"negative port":            with(func(r *Record) { r.Port = -1 }),
		"host with a space":        with(func(r *Record) { r.Host = "db2 evil" }),
		"host with padding":        with(func(r *Record) { r.Host = " db2" }),
		"host with a credential":   with(func(r *Record) { r.Host = "user@db2" }),
		"password flag mismatch":   with(func(r *Record) { r.Password = "pw" }),
		"windows auth with secret": with(func(r *Record) { r.Auth, r.Username, r.Password, r.HasPassword = AuthWindows, "", "pw", true }),
		"over long password":       with(func(r *Record) { r.Password, r.HasPassword = string(bytes.Repeat([]byte("p"), 513)), true }),
		"over long label":          with(func(r *Record) { r.Label = string(bytes.Repeat([]byte("l"), 201)) }),
		"id too long":              with(func(r *Record) { r.ID = string(bytes.Repeat([]byte("i"), 65)) }),
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			keeper, path := service(t)
			if n, err := keeper.Import([]Record{good}); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// A good record in the same call must not be written either.
			second := good
			second.ID, second.Host = "good2", "db3"
			if n, err := keeper.Import([]Record{second, bad}); !errors.Is(err, ErrInvalid) || n != 0 {
				t.Fatalf("import = %d, %v; want ErrInvalid", n, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("the store changed after a refused import: %v", err)
			}
		})
	}
}

// Records an older version saved (through Save) must keep importing: id, save time, password and the "host,port" spelling survive.
func TestImportKeepsIDAndSaveTimeAndDefaultsABlankEncrypt(t *testing.T) {
	old, _ := service(t)
	in := ssms()
	in.RememberPassword = true
	saved, err := old.Save(in)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := old.Export()
	if err != nil || len(recs) != 1 {
		t.Fatal(len(recs), err)
	}
	recs[0].Encrypt = "" // a record from a version that did not write the word
	keeper, _ := service(t)
	if n, err := keeper.Import(recs); err != nil || n != 1 {
		t.Fatalf("import = %d, %v", n, err)
	}
	list, err := keeper.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if list[0].ID != saved.ID || !list[0].SavedAt.Equal(saved.SavedAt) || list[0].Encrypt != EncryptMandatory || !list[0].HasPassword || list[0].Server != "192.168.4.2,1655" {
		t.Fatalf("imported %+v from %+v", list[0], saved)
	}
}
