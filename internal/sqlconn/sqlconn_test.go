package sqlconn

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type xorProtector struct{}

func (xorProtector) Protect(v []byte) ([]byte, error) {
	out := append([]byte("sealed:"), v...)
	for i := len("sealed:"); i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}
func (xorProtector) Unprotect(v []byte) ([]byte, error) {
	if !bytes.HasPrefix(v, []byte("sealed:")) {
		return nil, errors.New("bad")
	}
	out := append([]byte(nil), v[len("sealed:"):]...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

func service(t *testing.T) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg", "sqlconnections.dat")
	s, err := New(path, xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func ssms() Input {
	return Input{Server: "192.168.4.2,1655", Auth: AuthSQL, Username: "sa", Password: "hunter2-secret", Encrypt: EncryptMandatory, TrustServerCertificate: true}
}

func TestSplitServer(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		ok   bool
	}{
		{"192.168.4.2,1655", "192.168.4.2", 1655, true},
		{"db1:1433", "db1", 1433, true},
		{"tcp:db1,1500", "db1", 1500, true},
		{"db1", "db1", 0, true},
		{`db1\SQLEXPRESS`, `db1\SQLEXPRESS`, 0, true},
		{"", "", 0, false},
		{"db1,99999", "", 0, false},
		{"db1,abc", "", 0, false},
		{"a b", "", 0, false},
		{"server=x;password=y", "", 0, false},
	}
	for _, c := range cases {
		host, port, ok := SplitServer(c.in)
		if ok != c.ok || host != c.host || port != c.port {
			t.Errorf("%q: got %q %d %v", c.in, host, port, ok)
		}
	}
}

func TestPasswordOnlyKeptWhenAsked(t *testing.T) {
	s, path := service(t)
	in := ssms()
	rec, err := s.Save(in)
	if err != nil || rec.HasPassword || rec.Host != "192.168.4.2" || rec.Port != 1655 || rec.Server != "192.168.4.2,1655" {
		t.Fatalf("not remembered: %+v %v", rec, err)
	}
	raw, _ := os.ReadFile(path)
	plain, _ := xorProtector{}.Unprotect(raw)
	if bytes.Contains(plain, []byte("hunter2")) || bytes.Contains(raw, []byte("hunter2")) {
		t.Fatal("password was written without Remember password")
	}
	in.RememberPassword = true
	rec, err = s.Save(in)
	if err != nil || !rec.HasPassword {
		t.Fatalf("remembered: %+v %v", rec, err)
	}
	raw, _ = os.ReadFile(path)
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Fatal("password is stored in the clear")
	}
	items, _ := s.List()
	if len(items) != 1 {
		t.Fatalf("same server and login should replace, got %d", len(items))
	}
}

func TestBlankPasswordKeepsRememberedOne(t *testing.T) {
	s, _ := service(t)
	in := ssms()
	in.RememberPassword = true
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	in.Password = ""
	rec, err := s.Save(in)
	if err != nil || !rec.HasPassword {
		t.Fatalf("kept: %+v %v", rec, err)
	}
	in.RememberPassword = false
	if rec, _ = s.Save(in); rec.HasPassword {
		t.Fatal("unticking Remember password must drop the stored one")
	}
}

func TestWindowsAuthDropsLogin(t *testing.T) {
	s, _ := service(t)
	rec, err := s.Save(Input{Server: "db1", Auth: AuthWindows, Username: "ignored", Password: "x", RememberPassword: true})
	if err != nil || rec.Username != "" || rec.HasPassword || rec.Port != 1433 {
		t.Fatalf("%+v %v", rec, err)
	}
}

func TestInvalidInputs(t *testing.T) {
	s, _ := service(t)
	for _, in := range []Input{
		{Server: "", Auth: AuthSQL, Username: "sa"},
		{Server: "db1", Auth: AuthSQL},
		{Server: "db1", Auth: "kerberos"},
		{Server: "db1", Auth: AuthWindows, Encrypt: "maybe"},
	} {
		if _, err := s.Save(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
}

func TestDeleteAndLimit(t *testing.T) {
	s, _ := service(t)
	first, _ := s.Save(Input{Server: "h0", Auth: AuthWindows})
	for i := 1; i <= MaxRecords+3; i++ {
		if _, err := s.Save(Input{Server: "h" + strconv.Itoa(i), Auth: AuthWindows}); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := s.List()
	if len(items) != MaxRecords {
		t.Fatalf("limit: %d", len(items))
	}
	if err := s.Delete(items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("first was pushed out: %v", err)
	}
}

func TestCorruptFileIsLeftAlone(t *testing.T) {
	s, path := service(t)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("garbage"), 0o600)
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Save(ssms()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "garbage" {
		t.Fatal("file was changed")
	}
}
