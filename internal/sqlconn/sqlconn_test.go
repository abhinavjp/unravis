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
	s, path := service(t)
	first, err := s.Save(Input{Server: "h0", Auth: AuthWindows})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < MaxRecords; i++ {
		if _, err := s.Save(Input{Server: "h" + strconv.Itoa(i), Auth: AuthWindows}); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(path)
	// The 21st is refused outright. Nothing older is dropped to make room and the sealed file is not rewritten.
	if _, err := s.Save(Input{Server: "one-too-many", Auth: AuthWindows}); !errors.Is(err, ErrFull) {
		t.Fatalf("21st connection: %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("a refused save changed the file")
	}
	items, _ := s.List()
	if len(items) != MaxRecords {
		t.Fatalf("limit: %d", len(items))
	}
	for _, it := range items {
		if it.Server == "one-too-many:1433" || it.Host == "one-too-many" {
			t.Fatal("the refused connection was stored")
		}
	}
	// Saving an existing connection again is an edit, not a new one, so it still works when the list is full.
	if _, err := s.Save(Input{Server: "h0", Auth: AuthWindows, Label: "renamed"}); err != nil {
		t.Fatalf("editing a saved connection when full: %v", err)
	}
	// Deleting one makes room.
	if err := s.Delete(first.ID); err != nil {
		// h0 was re-saved under the same id, so it is still there to delete
		t.Fatal(err)
	}
	if _, err := s.Save(Input{Server: "one-too-many", Auth: AuthWindows}); err != nil {
		t.Fatalf("after deleting one: %v", err)
	}
	if err := s.Delete(first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("already deleted: %v", err)
	}
}

func TestFullListKeepsPasswordsAndNeverReturnsThem(t *testing.T) {
	s, path := service(t)
	for i := 0; i < MaxRecords; i++ {
		in := ssms()
		in.Server = "h" + strconv.Itoa(i)
		in.RememberPassword = true
		if _, err := s.Save(in); err != nil {
			t.Fatal(err)
		}
	}
	in := ssms()
	in.Server = "extra"
	in.RememberPassword = true
	_, err := s.Save(in)
	if !errors.Is(err, ErrFull) {
		t.Fatalf("%v", err)
	}
	if msg := err.Error(); bytes.Contains([]byte(msg), []byte("hunter2")) {
		t.Fatal("the refusal carries a password")
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("hunter2-secret")) {
		t.Fatal("password is in the file in clear")
	}
	items, _ := s.List()
	for _, it := range items {
		if !it.HasPassword {
			t.Fatal("a kept password was lost")
		}
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
