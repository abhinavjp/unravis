package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/sqlconn"
)

func TestScn1L3DemoSQLSaveReadsServersLikeTheRealStore(t *testing.T) {
	s := newDemoSQLStore(lday(20, 9))
	for _, c := range []struct {
		server, host string
		port         int
		shown        string
	}{
		{"tcp:db1,1655", "db1", 1655, "db1,1655"},
		{"db2:1444", "db2", 1444, ""},
		{`db3\SQL`, `db3\SQL`, 1433, ""},
	} {
		rec, err := s.Save(sqlconn.Input{Server: c.server, Auth: sqlconn.AuthWindows})
		if err != nil {
			t.Fatalf("%s: %v", c.server, err)
		}
		if rec.Host != c.host || rec.Port != c.port || (c.shown != "" && rec.Server != c.shown) {
			t.Fatalf("%s: host %q port %d server %q", c.server, rec.Host, rec.Port, rec.Server)
		}
	}
	if _, err := s.Save(sqlconn.Input{Server: "db4", Auth: sqlconn.AuthSQL}); !errors.Is(err, sqlconn.ErrInvalid) {
		t.Fatalf("SQL login with no user: err = %v, want ErrInvalid", err)
	}
}

func TestScn1L3DemoSQLSaveUsesTheRealLimits(t *testing.T) {
	s := newDemoSQLStore(lday(20, 9))
	for name, in := range map[string]sqlconn.Input{
		"long password": {Server: "dbl", Auth: sqlconn.AuthSQL, Username: "u", Password: strings.Repeat("a", 513)},
		"bad encrypt":   {Server: "dbl", Auth: sqlconn.AuthWindows, Encrypt: "bogus"},
		"long label":    {Server: "dbl", Auth: sqlconn.AuthWindows, Label: strings.Repeat("a", 201)},
	} {
		if _, err := s.Save(in); !errors.Is(err, sqlconn.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestScn1L3DemoSQLSaveKeepsAPasswordFlagOnABlankEdit(t *testing.T) {
	s := newDemoSQLStore(lday(20, 9))
	base := sqlconn.Input{Server: "dbk", Auth: sqlconn.AuthSQL, Username: "u", Password: "p", RememberPassword: true, Encrypt: sqlconn.EncryptMandatory}
	first, err := s.Save(base)
	if err != nil || !first.HasPassword {
		t.Fatalf("first = %+v, %v", first, err)
	}
	blank := base
	blank.Password = ""
	again, err := s.Save(blank)
	if err != nil || !again.HasPassword || again.ID != first.ID {
		t.Fatalf("blank edit = %+v, %v", again, err)
	}
	blank.RememberPassword = false
	cleared, err := s.Save(blank)
	if err != nil || cleared.HasPassword {
		t.Fatalf("remember off = %+v, %v", cleared, err)
	}
	blank.RememberPassword = true
	still, err := s.Save(blank)
	if err != nil || still.HasPassword {
		t.Fatalf("flag was false, blank save with remember = %+v, %v", still, err)
	}
	if _, err := s.Save(base); err != nil {
		t.Fatal(err)
	}
	weaker := blank
	weaker.Encrypt = sqlconn.EncryptOptional
	if _, err := s.Save(weaker); !errors.Is(err, sqlconn.ErrPasswordAgain) {
		t.Fatalf("weaker encryption: err = %v, want ErrPasswordAgain", err)
	}
	items, _ := s.List()
	found := false
	for _, r := range items {
		if r.Host == "dbk" {
			found = true
			if !r.HasPassword || r.Encrypt != sqlconn.EncryptMandatory {
				t.Fatalf("the earlier item changed: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("the earlier item was removed")
	}
}

func TestScn1L3DemoSQLHTTPBlankEditKeepsThePasswordFlag(t *testing.T) {
	e, _ := demoLaneEnv(t)
	const typed = `{"label":"K","server":"db.example:1444","auth":"sql","username":"u","password":"typed-secret","rememberPassword":true,"database":"AuditDb"}`
	if res := e.json(http.MethodPost, "/api/sql-connections", typed); res.Code != http.StatusOK && res.Code != http.StatusCreated {
		t.Fatalf("first save = %d %s", res.Code, res.Body)
	}
	const blank = `{"label":"K","server":"db.example:1444","auth":"sql","username":"u","rememberPassword":true,"database":"AuditDb"`
	res := e.json(http.MethodPost, "/api/sql-connections", blank+`}`)
	if res.Code != http.StatusOK {
		t.Fatalf("blank save = %d %s", res.Code, res.Body)
	}
	var rec sqlconn.Recent
	decodeBody(t, res, &rec)
	if !rec.HasPassword || strings.Contains(res.Body.String(), "typed-secret") {
		t.Fatalf("blank save = %+v %s", rec, res.Body)
	}
	res = e.json(http.MethodPost, "/api/sql-connections", blank+`,"encrypt":"optional"}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "Type the password again") {
		t.Fatalf("weaker save = %d %s", res.Code, res.Body)
	}
	list := e.get("/api/sql-connections").Body.String()
	if strings.Count(list, `"label":"K"`) != 1 || !strings.Contains(list, `"hasPassword":true`) {
		t.Fatalf("list = %s", list)
	}
}
