package blackbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestHelperDecision(t *testing.T) {
	const sum = "ab12"
	cases := []struct {
		name   string
		pinned string
		trust  uint32
		ok     bool
	}{
		{"unsigned developer build", "", trustNoSignature, true},
		{"unknown format", "", trustSubjectFormatUnk, true},
		{"valid signature", "", 0, true},
		{"broken signature", "", 0x80096010, false},
		{"untrusted publisher", "", 0x800B0109, false},
		{"pin matches", sum, trustNoSignature, true},
		{"pin matches, any case", "AB12", 0x80096010, true},
		{"pin differs, signature valid", "ffff", 0, false},
	}
	for _, c := range cases {
		err := helperDecision(c.pinned, sum, c.trust)
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
		if err != nil && !errors.Is(err, errHelperRefused) {
			t.Errorf("%s: not an integrity error: %v", c.name, err)
		}
	}
}

// guardElev records whether the permission prompt would have been shown.
type guardElev struct {
	fakeElev
	ran bool
}

func (g *guardElev) Run(ctx context.Context, exe string, args []string, elevated bool) ([]byte, error) {
	g.ran = true
	return g.fakeElev.Run(ctx, exe, args, elevated)
}

// SEC-01: a program that fails the check never gets a permission prompt.
func TestGuardRefusalStopsBeforeThePermissionPrompt(t *testing.T) {
	f := &guardElev{fakeElev: fakeElev{answer: `{"ok":true,"readKey":"ubk-TESTKEY"}`}}
	tp := &ThisPC{Elev: f, Program: `C:\Unravis\unravis-blackbox.exe`, TempRoot: t.TempDir(),
		Guard: func(string) (func(), error) { return nil, errHelperRefused }}
	_, err := tp.Install(t.Context(), packCfg())
	var te *ThisPCError
	if !errors.As(err, &te) || te.Reason != ReasonHelperChanged {
		t.Fatalf("%v", err)
	}
	if f.ran {
		t.Fatal("the permission prompt was shown for a program that failed the check")
	}
	if what, next := FailureWords(ReasonHelperChanged); what == "" || next == "" {
		t.Fatal("no words for the refusal")
	}
	if strings.Contains(te.Message, "ubk-") {
		t.Fatal("key in message")
	}
}

// SEC-01: the lock is held for the whole call and let go at the end; an unelevated
// call (snapshot) does not need the check.
func TestGuardIsReleasedAfterTheCall(t *testing.T) {
	f := &guardElev{fakeElev: fakeElev{answer: `{"ok":true,"readKey":"ubk-TESTKEY","service":"UnravisBlackbox","port":7788,"name":"web-b"}`}}
	checked, released := 0, 0
	tp := &ThisPC{Elev: f, Program: `C:\Unravis\unravis-blackbox.exe`, TempRoot: t.TempDir(),
		Guard: func(string) (func(), error) {
			checked++
			return func() { released++ }, nil
		}}
	if _, err := tp.Install(t.Context(), packCfg()); err != nil {
		t.Fatal(err)
	}
	if checked != 1 || released != 1 {
		t.Fatalf("checked %d released %d", checked, released)
	}
}

// SEC-05: the helper is told which settings file the person approved.
func TestHelperGetsTheHashOfTheSettingsFile(t *testing.T) {
	f := &fakeElev{answer: `{"ok":true,"readKey":"ubk-TESTKEY","service":"UnravisBlackbox","port":7788,"name":"web-b"}`}
	tp := newThisPC(f, t)
	if _, err := tp.Install(t.Context(), packCfg()); err != nil {
		t.Fatal(err)
	}
	args := f.calls[0].args
	var want string
	for i, a := range args {
		if a == "--config-sha256" && i+1 < len(args) {
			want = args[i+1]
		}
	}
	sum := sha256.Sum256([]byte(f.cfgSeen))
	if want == "" || want != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash argument %q does not match the file that was read: %v", want, args)
	}
}

// SEC-FIX-15: a pin mismatch stops install and apply, but never stops or removes
// a Blackbox that is already there.
func TestGuardRefusalBlocksOnlyInstallAndApply(t *testing.T) {
	refuse := func(string) (func(), error) { return nil, errHelperRefused }
	ans := `{"ok":true,"readKey":"ubk-TESTKEY","service":"UnravisBlackbox","port":7788,"name":"web-b","restarted":true,"serviceRemoved":true}`
	cases := []struct {
		name    string
		call    func(tp *ThisPC) error
		blocked bool
	}{
		{"install", func(tp *ThisPC) error { _, err := tp.Install(t.Context(), packCfg()); return err }, true},
		{"apply", func(tp *ThisPC) error { _, err := tp.Apply(t.Context(), packCfg()); return err }, true},
		{"update", func(tp *ThisPC) error { _, err := tp.Update(t.Context()); return err }, true},
		{"stop", func(tp *ThisPC) error { return tp.Stop(t.Context()) }, false},
		{"start", func(tp *ThisPC) error { return tp.Start(t.Context()) }, false},
		{"remove", func(tp *ThisPC) error { _, err := tp.Remove(t.Context(), "web-b", false, ""); return err }, false},
	}
	for _, c := range cases {
		f := &guardElev{fakeElev: fakeElev{answer: ans}}
		tp := &ThisPC{Elev: f, Program: `C:\Unravis\unravis-blackbox.exe`, TempRoot: t.TempDir(), Guard: refuse}
		err := c.call(tp)
		var te *ThisPCError
		if c.blocked {
			if !errors.As(err, &te) || te.Reason != ReasonHelperChanged || f.ran {
				t.Errorf("%s: should be refused before the prompt: %v ran=%v", c.name, err, f.ran)
			}
			continue
		}
		if err != nil || !f.ran {
			t.Errorf("%s: should not depend on the pin: %v ran=%v", c.name, err, f.ran)
		}
	}
}
