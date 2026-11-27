package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

const (
	pw     = "hunter2-SECRET-PASSWORD"
	pem    = "-----BEGIN OPENSSH PRIVATE KEY-----\nSECRETKEYBODYSECRETKEYBODY\n-----END OPENSSH PRIVATE KEY-----\n"
	fpA    = "SHA256:Zk3Hn1c0uV8mF2rXb9yQe7TgPwLd4oAiS5JhN6vCtMU"
	fpB    = "SHA256:Aa3Hn1c0uV8mF2rXb9yQe7TgPwLd4oAiS5JhN6vCtMU"
	passph = "PASSPHRASE-SECRET" //nolint:gosec // test value, not a real credential
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg", "servers.dat")
	s, err := New(p, xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	s.SetClock(func() time.Time { return time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC) })
	return s, p
}

func good() AddInput {
	return AddInput{
		Name: "web-01", Host: "Web-01.Corp.Example", Port: 22, User: "svc-unravis", OS: "windows",
		Auth: AuthPassword, Secret: pw, Transports: []string{TransportSSH, TransportBlackbox},
		Folders: map[string][]string{FolderBlackbox: {`D:\Blackbox`}, FolderIIS: {`C:\inetpub\logs\LogFiles`}},
	}
}

func TestAddListGetAndTheSecretStaysSealed(t *testing.T) {
	s, path := open(t)
	e, err := s.Add(good())
	if err != nil || !e.HasSecret || e.Host != "web-01.corp.example" || e.Port != 22 || !strings.HasPrefix(e.ID, "srv-") {
		t.Fatalf("%+v %v", e, err)
	}
	list, _ := s.List()
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), pw) || strings.Contains(string(b), `"secret"`) || strings.Contains(string(b), `"passphrase"`) {
		t.Fatalf("a secret is in the list the page sees: %s", b)
	}
	if got, p, err := s.Secret(e.ID); err != nil || got != pw || p != "" {
		t.Fatalf("secret: %v", err)
	}
	// the file on disk is sealed, not readable JSON
	raw, _ := os.ReadFile(path) //nolint:gosec // path comes from a checked, app-owned location
	if bytes.Contains(raw, []byte("hunter2")) || bytes.Contains(raw, []byte("web-01")) {
		t.Fatal("plain text on disk")
	}
	if info, _ := os.Stat(path); info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("file is readable by others: %v", info.Mode())
	}
}

func TestKeyAuthNeedsAPrivateKeyAndKeepsThePassphraseSealed(t *testing.T) {
	s, _ := open(t)
	in := good()
	in.Auth, in.Secret, in.Passphrase = AuthKey, pem, passph
	e, err := s.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "SECRETKEY") || strings.Contains(string(b), passph) {
		t.Fatalf("leak: %s", b)
	}
	if k, p, _ := s.Secret(e.ID); k != pem || p != passph {
		t.Fatal("secret lost")
	}
	bad := good()
	bad.Host = "other"
	bad.Auth, bad.Secret = AuthKey, "just a password"
	if _, err := s.Add(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a password as a key was accepted: %v", err)
	}
	bad.Secret = "PuTTY-User-Key-File-3: ssh-ed25519\n"
	if _, err := s.Add(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf(".ppk text accepted: %v", err)
	}
	pwWithPass := good()
	pwWithPass.Host, pwWithPass.Passphrase = "other", "x"
	if _, err := s.Add(pwWithPass); !errors.Is(err, ErrInvalid) {
		t.Fatalf("passphrase on a password sign-in accepted: %v", err)
	}
}

func TestRefusesWhatTheGrammarRefuses(t *testing.T) {
	s, _ := open(t)
	mut := map[string]func(*AddInput){
		"host injection":    func(a *AddInput) { a.Host = "-oProxyCommand=x" },
		"host with space":   func(a *AddInput) { a.Host = "a b" },
		"port zero":         func(a *AddInput) { a.Port = -1 },
		"port high":         func(a *AddInput) { a.Port = 70000 },
		"user option":       func(a *AddInput) { a.User = "-oFoo" },
		"no name":           func(a *AddInput) { a.Name = " " },
		"bad os":            func(a *AddInput) { a.OS = "plan9" },
		"bad auth":          func(a *AddInput) { a.Auth = "none" },
		"no transport":      func(a *AddInput) { a.Transports = nil },
		"unknown transport": func(a *AddInput) { a.Transports = []string{"telnet"} },
		"salt no minion":    func(a *AddInput) { a.Transports = []string{TransportSalt} },
		"wild minion":       func(a *AddInput) { a.Transports = []string{TransportSalt}; a.MinionIDs = []string{"web-*"} },
		"dotdot folder":     func(a *AddInput) { a.Folders = map[string][]string{FolderIIS: {`C:\inetpub\..\Windows`}} },
		"shell folder":      func(a *AddInput) { a.Folders = map[string][]string{FolderIIS: {`C:\x$HOME`}} },
		"unknown kind":      func(a *AddInput) { a.Folders = map[string][]string{"secrets": {`C:\x`}} },
		"empty password":    func(a *AddInput) { a.Secret = "" },
		"newline password":  func(a *AddInput) { a.Secret = "a\nb" },
	}
	for name, m := range mut {
		in := good()
		m(&in)
		if _, err := s.Add(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("a refused server was saved: %v", list)
	}
}

func TestDuplicateIsOneServerAndKeepsTheSealedSecret(t *testing.T) {
	s, _ := open(t)
	first, _ := s.Add(good())
	again := good()
	again.Host, again.Secret = "WEB-01.corp.example", "another"
	e, err := s.Add(again)
	if !errors.Is(err, ErrDuplicate) || e.ID != first.ID {
		t.Fatalf("%v %v", e, err)
	}
	if k, _, _ := s.Secret(first.ID); k != pw {
		t.Fatal("the earlier secret was replaced by a duplicate add")
	}
}

func TestHostKeyIsPinnedOnceAndReplacedOnlyDeliberately(t *testing.T) {
	s, _ := open(t)
	e, _ := s.Add(good())
	if err := s.PinHostKey(e.ID, "SHA256:short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad fingerprint accepted: %v", err)
	}
	if err := s.PinHostKey(e.ID, fpA); err != nil {
		t.Fatal(err)
	}
	if err := s.PinHostKey(e.ID, fpB); !errors.Is(err, ErrHostKeySet) {
		t.Fatalf("pin overwrote a pinned key: %v", err)
	}
	if got, _ := s.Get(e.ID); got.HostKey != fpA || got.HostKeyPinnedAt == nil {
		t.Fatalf("%+v", got)
	}
	if err := s.ReplaceHostKey(e.ID, fpB, fpA); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replace with the wrong old key: %v", err)
	}
	if err := s.ReplaceHostKey(e.ID, fpA, fpB); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(e.ID); got.HostKey != fpB {
		t.Fatal("not replaced")
	}
	e2, _ := s.Add(func() AddInput { a := good(); a.Host = "web-02"; return a }())
	if err := s.ReplaceHostKey(e2.ID, fpA, fpB); !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("replace on an unpinned server: %v", err)
	}
}

func TestUpdateChangesOnlyAllowedFieldsAndRevalidates(t *testing.T) {
	s, _ := open(t)
	e, _ := s.Add(good())
	name := "Web 1"
	folders := map[string][]string{FolderRedis: {"D:/Redis/logs"}}
	got, err := s.Update(e.ID, Patch{Name: &name, Folders: &folders})
	if err != nil || got.Name != "Web 1" || got.Folders[FolderRedis][0] != "D:/Redis/logs" || got.Host != e.Host || got.HostKey != "" {
		t.Fatalf("%+v %v", got, err)
	}
	badF := map[string][]string{FolderRedis: {"../etc"}}
	if _, err := s.Update(e.ID, Patch{Folders: &badF}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad folder accepted: %v", err)
	}
	if again, _ := s.Get(e.ID); again.Folders[FolderRedis][0] != "D:/Redis/logs" {
		t.Fatal("a refused update changed the entry")
	}
	if _, err := s.Update("nope", Patch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestBlackboxOnlyServerKeepsNoSecretOrAccount(t *testing.T) {
	s, path := open(t)
	in := AddInput{Name: "lite", Host: "lite.corp.example", Port: 22, OS: "windows", Transports: []string{TransportBlackbox}, BlackboxID: "bb-1"}
	e, err := s.Add(in)
	if err != nil || e.HasSecret || e.User != "" || e.Auth != "" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := New(path, xorProtector{}); err != nil {
		t.Fatalf("a saved Blackbox-only server must load back: %v", err)
	}
	// A secret or an account on a server that does not sign in is refused, not silently kept.
	bad := in
	bad.Host, bad.Secret = "lite2.corp.example", pw
	if _, err := s.Add(bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("secret on a Blackbox-only server: %v", err)
	}
	// It cannot be given a sign-in secret, and cannot be switched to SSH without one.
	if err := s.SetSecret(e.ID, AuthPassword, pw, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("SetSecret: %v", err)
	}
	ssh := []string{TransportSSH, TransportBlackbox}
	if _, err := s.Update(e.ID, Patch{Transports: &ssh}); err == nil {
		t.Error("switched to SSH with no secret held")
	}
}

func TestSetSecretRemoveAndLimits(t *testing.T) {
	s, _ := open(t)
	e, _ := s.Add(good())
	if err := s.SetSecret(e.ID, AuthPassword, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := s.SetSecret(e.ID, AuthKey, pem, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(e.ID); got.Auth != AuthKey {
		t.Fatal("auth kind not updated")
	}
	if err := s.Remove(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Secret(e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("secret survived remove: %v", err)
	}
	for i := 0; i < MaxEntries; i++ {
		a := good()
		a.Host = "h" + strings.Repeat("a", i%5) + string(rune('a'+i/5)) + ".example"
		if _, err := s.Add(a); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	a := good()
	a.Host = "overflow.example"
	if _, err := s.Add(a); !errors.Is(err, ErrTooMany) {
		t.Fatalf("%v", err)
	}
}

func TestDamagedOrEditedFileIsLeftAloneAndNotUsed(t *testing.T) {
	s, path := open(t)
	e, _ := s.Add(good())
	// a sealed file that decodes but holds a host the grammar refuses
	bad := `{"version":1,"records":[{"id":"srv-1","name":"x","host":"-oProxyCommand=x","port":22,"user":"u","os":"linux","auth":"password","hasSecret":true,"transports":["ssh"],"addedAt":"2026-11-26T09:00:00Z","secret":"p"}]}`
	sealed, _ := xorProtector{}.Protect([]byte(bad))
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("hand-edited host accepted: %v", err)
	}
	if _, err := s.Add(good()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("add over a damaged file: %v", err)
	}
	if raw, _ := os.ReadFile(path); !bytes.Equal(raw, sealed) { //nolint:gosec // path comes from a checked, app-owned location
		t.Fatal("the damaged file was rewritten")
	}
	_ = e
	for name, content := range map[string][]byte{"garbage": []byte("nonsense"), "empty": {}} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
	}
	unknown, _ := xorProtector{}.Protect([]byte(`{"version":1,"records":[],"extra":1}`))
	os.WriteFile(path, unknown, 0o600) //nolint:errcheck // test helper, error not needed
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
		t.Errorf("unknown field accepted: %v", err)
	}
}

func TestConcurrentAddsAllLand(t *testing.T) {
	s, _ := open(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := good()
			a.Host = "node" + string(rune('a'+i)) + ".example" //nolint:gosec // value is range-checked or small by construction
			if _, err := s.Add(a); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if list, _ := s.List(); len(list) != 8 {
		t.Fatalf("got %d", len(list))
	}
}

func TestNewRefusesNoPathOrNoProtector(t *testing.T) {
	if _, err := New("", xorProtector{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := New("x", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
