package shelllink

import (
	"bytes"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestBytesRoundTripsTargetAndArgs(t *testing.T) {
	target := `C:\Program Files\Unravis\unravis.exe`
	data, err := Shortcut{Target: target, Args: "--background", WorkDir: `C:\Program Files\Unravis`, Description: "Unravis", Icon: target}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ReadShortcutTarget(data); err != nil || got != target {
		t.Errorf("target = %q %v", got, err)
	}
	if got, err := ReadShortcutArgs(data); err != nil || got != "--background" {
		t.Errorf("args = %q %v", got, err)
	}
}

func TestReadShortcutArgsEmptyWhenNoArgs(t *testing.T) {
	data, err := Shortcut{Target: `C:\Unravis\unravis.exe`, WorkDir: `C:\Unravis`, Description: "Unravis", Icon: `C:\Unravis\unravis.exe`}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ReadShortcutArgs(data); err != nil || got != "" {
		t.Errorf("args = %q %v, want none", got, err)
	}
}

func TestReadShortcutArgsRejectsGarbage(t *testing.T) {
	data, err := Shortcut{Target: `C:\Unravis\unravis.exe`, Args: "--background", WorkDir: `C:\Unravis`, Description: "Unravis"}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	junk := make([]byte, 10)
	rand.New(rand.NewSource(1)).Read(junk)
	for name, in := range map[string][]byte{"empty": {}, "random": junk, "half": data[:len(data)/2]} {
		if got, err := ReadShortcutArgs(in); err == nil {
			t.Errorf("%s read as %q", name, got)
		}
	}
}

func TestReadShortcutArgsRejectsTruncatedStringData(t *testing.T) {
	const args = "--background"
	data, err := Shortcut{Target: `C:\Unravis\unravis.exe`, Args: args}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// With only the arguments set, StringData is the 2-byte count, the characters and the 4-byte terminal block.
	cut := len(data) - 4 - 1 // one byte inside the last character
	if cut <= 0 || !bytes.Equal(data[len(data)-4-2*len(args):len(data)-4], utf16Bytes(args)) {
		t.Fatal("the arguments are not where the test expects them")
	}
	if got, err := ReadShortcutArgs(data[:cut]); err == nil {
		t.Errorf("a cut-off string read as %q", got)
	}
	// The whole string without the terminal block still reads.
	if got, err := ReadShortcutArgs(data[:len(data)-4]); err != nil || got != args {
		t.Errorf("args = %q %v", got, err)
	}
}

func TestBytesIsDeterministic(t *testing.T) {
	s := Shortcut{Target: `C:\Unravis\unravis.exe`, Args: "--background", WorkDir: `C:\Unravis`, Description: "Unravis", Icon: `C:\Unravis\unravis.exe`, IconIndex: 1}
	a, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("the same shortcut gave different bytes")
	}
}

func TestUnicodeTargetAndArgsRoundTrip(t *testing.T) {
	target, args := `D:\Ünravis 文件\unravis.exe`, `--case "文件 Ü"`
	data, err := Shortcut{Target: target, Args: args, WorkDir: `D:\Ünravis 文件`}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ReadShortcutTarget(data); err != nil || got != target {
		t.Errorf("target = %q %v", got, err)
	}
	if got, err := ReadShortcutArgs(data); err != nil || got != args {
		t.Errorf("args = %q %v", got, err)
	}
}

func utf16Bytes(s string) []byte {
	var out []byte
	for _, c := range utf16.Encode([]rune(s)) {
		out = append(out, byte(c), byte(c>>8))
	}
	return out
}

func TestSamePathIgnoresCaseAndRefusesEmpty(t *testing.T) {
	if !SamePath(`C:\Unravis\unravis.exe`, `c:\UNRAVIS\.\unravis.exe`) {
		t.Error("case and dots should not matter")
	}
	if SamePath(`C:\a.exe`, `C:\b.exe`) || SamePath("", "") || SamePath(`C:\a.exe`, "") {
		t.Error("different or empty paths matched")
	}
}

func TestTargetIsOnlyForAShortcutToThatExe(t *testing.T) {
	exe := `C:\Unravis\unravis.exe`
	ours, err := Shortcut{Target: exe, Args: BackgroundArg}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	other, err := Shortcut{Target: `C:\Other\other.exe`}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !TargetIs(ours, exe) || TargetIs(other, exe) || TargetIs([]byte("garbage"), exe) || TargetIs(nil, exe) {
		t.Error("TargetIs gave the wrong answer")
	}
}

func TestReadFileGuards(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.lnk")
	if err := os.WriteFile(good, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(good, nil); err != nil || string(data) != "abc" {
		t.Fatalf("good = %q %v", data, err)
	}
	if _, err := ReadFile(filepath.Join(dir, "missing.lnk"), nil); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	big := filepath.Join(dir, "big.lnk")
	if err := os.WriteFile(big, make([]byte, MaxFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(string) ([]byte, error) { t.Fatal("a file that must be refused was read"); return nil, nil }
	if _, err := ReadFile(big, read); !errors.Is(err, ErrNotShortcutFile) {
		t.Errorf("oversized: %v", err)
	}
	if _, err := ReadFile(dir, read); !errors.Is(err, ErrNotShortcutFile) {
		t.Errorf("directory: %v", err)
	}
	// A file that grows between the stat and the read is still refused.
	grown := func(string) ([]byte, error) { return make([]byte, MaxFileSize+1), nil }
	if _, err := ReadFile(good, grown); !errors.Is(err, ErrNotShortcutFile) {
		t.Errorf("grown: %v", err)
	}
	// A read error comes back as it is.
	boom := errors.New("boom")
	if _, err := ReadFile(good, func(string) ([]byte, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
}
