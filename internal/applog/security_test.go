package applog

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setProfile points every profile spelling at dir for the test.
func setProfile(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
}

func TestSanitizeProfilePathSurvivesLowercaseLengthChanges(t *testing.T) {
	const dir = `C:\Users\` + "\u0130" + `lker`
	setProfile(t, dir)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"dotted capital I", `open ` + dir + `\x failed`, `open %USERPROFILE%\x failed`},
		{"normal case", `open C:\Users\ilker\x`, `open C:\Users\ilker\x`},
		{"upper ascii fold", `open C:\USERS\` + "\u0130" + `LKER\x`, `open %USERPROFILE%\x`},
		{"U+023A before the path", "\u023a\u023a " + dir + `\x`, "\u023a\u023a %USERPROFILE%\\x"},
		{"invalid utf-8 before the path", "\xff\xfe " + dir + `\x`, "\xff\xfe %USERPROFILE%\\x"},
		{"invalid utf-8 after the path", dir + "\\x \xc0\xaf", "%USERPROFILE%\\x \xc0\xaf"},
		{"invalid utf-8 inside the path", `C:\Users\` + "\xff" + `lker\x`, `C:\Users\` + "\xff" + `lker\x`},
		{"path cut short at the end", `C:\Users\` + "\u0130", `C:\Users\` + "\u0130"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Sanitize(tc.in)
			if got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestReplaceFoldNeverPanicsOnOddInput(t *testing.T) {
	inputs := []string{"", "\xff", "\xff\xff\xff", "\u0130\u023a\u0130", "ab\xe2\x82", strings.Repeat("\u023a", 50)}
	olds := []string{"a", "\u0130", "\u023a", "\xff", "ab\xe2"}
	for _, s := range inputs {
		for _, old := range olds {
			if out := replaceFold(s, old, "X"); !strings.Contains(s, old) && strings.Contains(out, "X") && !strings.EqualFold(s, old) && len(s) == len(old) {
				t.Errorf("replaceFold(%q, %q) = %q: replaced without a match", s, old, out)
			}
		}
	}
}

func TestStdlibLogLinesAreSanitized(t *testing.T) {
	const secret = "lc-Zk39Qw71X"
	RegisterEphemeralSecret(secret)
	dir := filepath.Join(t.TempDir(), "logs")
	f, err := Open(dir, ParseLevel("info"), func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	std := log.New(f.Writer(), "", 0)
	std.Print("launch code " + secret + " issued")
	std.Printf("dial failed: Server=db;Password='a b c';Database=x")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leaked := range []string{secret, "a b c"} {
		if strings.Contains(text, leaked) {
			t.Errorf("log file leaked %q: %q", leaked, text)
		}
	}
	if !strings.Contains(text, "launch code") {
		t.Errorf("log file lost the line: %q", text)
	}
}

func TestWriteAfterCloseDoesNotRecreateTheFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	f, err := Open(dir, ParseLevel("info"), func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Writer().Write([]byte("late line\n")); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Writer().Write after Close err = %v, want os.ErrClosed", err)
	}
	log.New(f.Writer(), "", 0).Print("fatal after close")
	f.Logger.Error("error after close")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("log file exists after Close and a late write (stat err = %v)", err)
	}
}

func TestSanitizeCoversBasicAuthQuotedValuesAndAccountKey(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		absent []string
		want   string // exact output when set
	}{
		{"authorization basic", "sent Authorization: Basic dXNlcjpwYXNz next", []string{"dXNlcjpwYXNz"}, "sent Authorization: Basic [redacted] next"},
		{"proxy authorization basic lower", "proxy-authorization: basic dXNlcjpwYXNzMg==", []string{"dXNlcjpwYXNzMg"}, ""},
		{"basic without header kept", "a basic configuration was used", nil, "a basic configuration was used"},
		{"single quoted with space", "cs Password='a b';Next=1", []string{"a b"}, "cs Password=[redacted];Next=1"},
		{"double quoted with space", `cs Password="a b";Next=1`, []string{"a b"}, "cs Password=[redacted];Next=1"},
		{"braced with semicolon", "cs PWD={x;y};Next=1", []string{"x;y", "y}"}, "cs PWD=[redacted];Next=1"},
		{"single quoted with semicolon", "cs pwd='p;q r' end", []string{"p;q r", "q r"}, "cs pwd=[redacted] end"},
		{"unterminated quote goes to the end", `cs Password="a b c`, []string{"a b c", "b c"}, ""},
		{"unterminated brace goes to the end", `cs Password={a;b`, []string{"a;b"}, ""},
		{"account key", "DefaultEndpointsProtocol=https;AccountKey=Zm9vYmFy;EndpointSuffix=x", []string{"Zm9vYmFy"}, "DefaultEndpointsProtocol=https;AccountKey=[redacted];EndpointSuffix=x"},
		{"account key glued to a prefix", "StorageAccountKey=Zm9vYmFy", []string{"Zm9vYmFy"}, ""},
		{"shared access key", "SharedAccessKey=abc123", []string{"abc123"}, ""},
		{"json account key", `{"AccountKey":"Zm9vYmFy"}`, []string{"Zm9vYmFy"}, ""},
		{"unquoted still stops at semicolon", "Password=Hunter2;Database=audit", []string{"Hunter2"}, "Password=[redacted];Database=audit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Sanitize(tc.in)
			for _, gone := range tc.absent {
				if strings.Contains(out, gone) {
					t.Errorf("Sanitize(%q) = %q, still contains %q", tc.in, out, gone)
				}
			}
			if tc.want != "" && out != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, out, tc.want)
			}
		})
	}
}
