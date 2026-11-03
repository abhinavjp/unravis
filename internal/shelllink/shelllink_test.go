package shelllink

import (
	"bytes"
	"math/rand"
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
