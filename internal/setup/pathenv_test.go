package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pathEntry is a full path that is valid on the system the test runs on.
func pathEntry() string { return filepath.Join(os.TempDir(), "Unravis", "bin") }

func TestEnvKeyFor(t *testing.T) {
	if envKeyFor(HKCU) != EnvKeyUser || envKeyFor(HKLM) != EnvKeyMachine {
		t.Fatalf("%q %q", envKeyFor(HKCU), envKeyFor(HKLM))
	}
}

func TestPathEntryKey(t *testing.T) {
	e := pathEntry()
	for _, v := range []string{e, strings.ToUpper(e), e + `\`, `"` + e + `"`, "  " + e + "  ", ` "` + e + `\" `} {
		if got := pathEntryKey(v); got != pathEntryKey(e) {
			t.Errorf("%q -> %q, want %q", v, got, pathEntryKey(e))
		}
	}
	if pathEntryKey("") != "" || pathEntryKey(`""`) != "" {
		t.Error("an empty entry has a key")
	}
}

func TestCheckPathEntry(t *testing.T) {
	sep := string(filepath.Separator)
	tmp := os.TempDir()
	bad := map[string]string{
		"empty":     "",
		"relative":  "bin",
		"semicolon": filepath.Join(tmp, "a;b"),
		"quote":     filepath.Join(tmp, `a"b`),
		"dotdot":    tmp + sep + ".." + sep + "x",
		"percent":   filepath.Join(tmp, "%PATH%"),
	}
	for name, v := range bad {
		if err := checkPathEntry(v); err == nil {
			t.Errorf("%s: %q was accepted", name, v)
		}
	}
	if err := checkPathEntry(pathEntry()); err != nil {
		t.Errorf("a good entry was refused: %v", err)
	}
}

func TestAddToPathValue(t *testing.T) {
	e := pathEntry()
	up := strings.ToUpper(e)
	long := strings.Repeat("a", MaxPathValue-5)
	tests := []struct {
		name, value, entry     string
		want                   string
		added, changed, tooBig bool
	}{
		{name: "empty value", value: "", entry: e, want: e, added: true, changed: true},
		{name: "add to end", value: `C:\A;C:\B`, entry: e, want: `C:\A;C:\B;` + e, added: true, changed: true},
		{name: "value already ends with a semicolon", value: `C:\A;`, entry: e, want: `C:\A;` + e, added: true, changed: true},
		{name: "entry written without a trailing backslash", value: `C:\A`, entry: e + `\`, want: `C:\A;` + e, added: true, changed: true},
		{name: "present with other case and a trailing backslash", value: `C:\A;` + up + `\;C:\B`, entry: e, want: `C:\A;` + up + `\;C:\B`},
		{name: "present and quoted", value: `C:\A;"` + e + `";C:\B`, entry: e, want: `C:\A;"` + e + `";C:\B`},
		{name: "duplicates dropped, first kept", value: `C:\A;` + e + `;C:\B;` + up + `;` + e + `\;`, entry: e, want: `C:\A;` + e + `;C:\B;`, changed: true},
		{name: "others untouched byte for byte", value: ` %SystemRoot%\system32;;  C:\X ;`, entry: e, want: ` %SystemRoot%\system32;;  C:\X ;` + e, added: true, changed: true},
		{name: "too long is refused and the value kept", value: long, entry: e, want: long, tooBig: true},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			got, added, changed, err := addToPathValue(c.value, c.entry)
			if c.tooBig {
				if !errors.Is(err, ErrPathTooLong) {
					t.Fatalf("err %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got != c.want || added != c.added || changed != c.changed {
				t.Errorf("got %q added=%v changed=%v\nwant %q added=%v changed=%v", got, added, changed, c.want, c.added, c.changed)
			}
		})
	}
	if _, _, _, err := addToPathValue("", ""); err == nil {
		t.Error("an empty entry was added")
	}
}

func TestRemoveFromPathValue(t *testing.T) {
	e := pathEntry()
	up := strings.ToUpper(e)
	tests := []struct {
		name, value, want string
		removed           bool
	}{
		{"first", e + `;C:\A;C:\B`, `C:\A;C:\B`, true},
		{"middle", `C:\A;` + e + `;C:\B`, `C:\A;C:\B`, true},
		{"last", `C:\A;C:\B;` + e, `C:\A;C:\B`, true},
		{"only", e, "", true},
		{"not present", `C:\A;%SystemRoot%\system32;`, `C:\A;%SystemRoot%\system32;`, false},
		{"empty value", "", "", false},
		{"other case and trailing backslash", `C:\A;` + up + `\;C:\B`, `C:\A;C:\B`, true},
		{"every copy", e + `;C:\A;"` + e + `"`, `C:\A`, true},
		{"the semicolon at the end stays", `C:\A;` + e + `;`, `C:\A;`, true},
		{"others untouched byte for byte", ` %SystemRoot%\system32;;  C:\X ;` + e, ` %SystemRoot%\system32;;  C:\X `, true},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			got, removed := removeFromPathValue(c.value, e)
			if got != c.want || removed != c.removed {
				t.Errorf("got %q removed=%v, want %q removed=%v", got, removed, c.want, c.removed)
			}
		})
	}
}
