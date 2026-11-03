package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/shelllink"
)

// setup makes a fake installed folder (unravis.exe and setup-state.json), an empty Startup folder and an empty temp folder that
// tempDir points at.
func setup(t *testing.T) (startup, exe, tmp string) {
	t.Helper()
	root := t.TempDir()
	inst := filepath.Join(root, "install")
	startup = filepath.Join(root, "Startup")
	tmp = filepath.Join(root, "tmp")
	for _, d := range []string{inst, startup, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exe = filepath.Join(inst, "unravis.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst, "setup-state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTemp, oldRename := tempDir, renameFile
	tempDir = func() string { return tmp }
	t.Cleanup(func() { tempDir, renameFile = oldTemp, oldRename })
	return startup, exe, tmp
}

func lnkBytes(t *testing.T, target, args string) []byte {
	t.Helper()
	b, err := shelllink.Shortcut{Target: target, Args: args}.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readBack(t *testing.T, startup string) (target, args string) {
	t.Helper()
	data, err := os.ReadFile(LinkPath(startup))
	if err != nil {
		t.Fatal(err)
	}
	if target, err = shelllink.ReadShortcutTarget(data); err != nil {
		t.Fatal(err)
	}
	if args, err = shelllink.ReadShortcutArgs(data); err != nil {
		t.Fatal(err)
	}
	return target, args
}

func entries(t *testing.T, dir string) int {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

func TestEnableCreatesTheShortcutWithBackgroundArg(t *testing.T) {
	startup, exe, tmp := setup(t)
	if err := Enable(startup, exe); err != nil {
		t.Fatal(err)
	}
	target, args := readBack(t, startup)
	if !samePath(target, exe) || args != "--background" {
		t.Fatalf("target %q args %q", target, args)
	}
	if !Enabled(startup, exe) {
		t.Fatal("Enabled is false after Enable")
	}
	if entries(t, tmp) != 0 {
		t.Fatal("temp file left behind")
	}
}

func TestEnableReplacesAnOlderShortcutOfOurs(t *testing.T) {
	startup, exe, _ := setup(t)
	if err := os.WriteFile(LinkPath(startup), lnkBytes(t, exe, ""), 0o600); err != nil {
		t.Fatal(err)
	}
	if Enabled(startup, exe) {
		t.Fatal("an old link without the arg must not count as on")
	}
	if err := Enable(startup, exe); err != nil {
		t.Fatal(err)
	}
	if _, args := readBack(t, startup); args != Arg {
		t.Fatalf("args %q", args)
	}
	if !Enabled(startup, exe) {
		t.Fatal("not enabled after replace")
	}
}

func TestEnableRefusesAForeignShortcut(t *testing.T) {
	other := filepath.Join(t.TempDir(), "other.exe")
	cases := map[string]func(t *testing.T, link string) []byte{
		"other target": func(t *testing.T, link string) []byte {
			b := lnkBytes(t, other, "")
			_ = os.WriteFile(link, b, 0o600)
			return b
		},
		"garbage": func(t *testing.T, link string) []byte {
			b := []byte("this is not a shortcut")
			_ = os.WriteFile(link, b, 0o600)
			return b
		},
		"directory": func(t *testing.T, link string) []byte {
			if err := os.Mkdir(link, 0o700); err != nil {
				t.Fatal(err)
			}
			return nil
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			startup, exe, tmp := setup(t)
			link := LinkPath(startup)
			before := mk(t, link)
			if err := Enable(startup, exe); !errors.Is(err, ErrNotOurs) {
				t.Fatalf("err = %v", err)
			}
			fi, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			if before == nil {
				if !fi.IsDir() {
					t.Fatal("directory was replaced")
				}
			} else if got, _ := os.ReadFile(link); string(got) != string(before) {
				t.Fatal("foreign file was changed")
			}
			if entries(t, tmp) != 0 {
				t.Fatal("temp file left behind")
			}
		})
	}
}

func TestEnableRefusesAnExeThatIsNotInstalled(t *testing.T) {
	startup, exe, tmp := setup(t)
	if err := os.Remove(filepath.Join(filepath.Dir(exe), "setup-state.json")); err != nil {
		t.Fatal(err)
	}
	if err := Enable(startup, exe); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v", err)
	}
	if entries(t, startup) != 0 || entries(t, tmp) != 0 {
		t.Fatal("something was written")
	}
}

func TestEnableRefusesRelativeAndWrongNamedExe(t *testing.T) {
	startup, exe, tmp := setup(t)
	inst := filepath.Dir(exe)
	other := filepath.Join(inst, "other.exe")
	if err := os.WriteFile(other, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"relative":    "unravis.exe",
		"wrong name":  other,
		"missing":     filepath.Join(inst, "sub", "unravis.exe"),
		"empty":       "",
		"is a folder": filepath.Join(filepath.Dir(inst), "unravis.exe"),
	} {
		if name == "is a folder" {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(filepath.Dir(p), "setup-state.json"), []byte("{}"), 0o600)
		}
		if err := Enable(startup, p); !errors.Is(err, ErrNotInstalled) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if entries(t, startup) != 0 || entries(t, tmp) != 0 {
		t.Fatal("something was written")
	}
}

func TestEnableWritesItsTempFileOutsideTheStartupFolder(t *testing.T) {
	startup, exe, _ := setup(t)
	var src string
	renameFile = func(from, to string) error {
		src = from
		return os.Rename(from, to)
	}
	if err := Enable(startup, exe); err != nil {
		t.Fatal(err)
	}
	if src == "" {
		t.Fatal("rename was not used")
	}
	if strings.EqualFold(filepath.Clean(filepath.Dir(src)), filepath.Clean(startup)) {
		t.Fatalf("temp file %q is in the Startup folder", src)
	}
	if !strings.HasSuffix(src, ".partial") {
		t.Fatalf("temp file %q", src)
	}
}

func TestEnableRemovesItsTempFileWhenTheRenameFails(t *testing.T) {
	startup, exe, tmp := setup(t)
	renameFile = func(from, to string) error { return errors.New("cross-volume") }
	if err := Enable(startup, exe); err != nil {
		t.Fatal(err)
	}
	if !Enabled(startup, exe) {
		t.Fatal("the direct write did not make the shortcut")
	}
	if entries(t, tmp) != 0 {
		t.Fatal("temp file left behind")
	}
}

func TestEnabledNeedsTheShortcutToPointAtThisExeWithTheArg(t *testing.T) {
	startup, exe, _ := setup(t)
	other := filepath.Join(t.TempDir(), "other.exe")
	ours := lnkBytes(t, exe, Arg)
	big := append(append([]byte{}, ours...), make([]byte, 100<<10)...)
	cases := []struct {
		name string
		data []byte // nil: no file
		want bool
	}{
		{"missing", nil, false},
		{"ours", ours, true},
		{"ours with other case", lnkBytes(t, strings.ToUpper(exe[:1])+strings.ToLower(exe[1:]), Arg), true},
		{"other target", lnkBytes(t, other, Arg), false},
		{"no args", lnkBytes(t, exe, ""), false},
		{"other args", lnkBytes(t, exe, "--x"), false},
		{"garbage", []byte("garbage"), false},
		{"100 KiB", big, false},
	}
	for _, c := range cases {
		_ = os.Remove(LinkPath(startup))
		if c.data != nil {
			if err := os.WriteFile(LinkPath(startup), c.data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := Enabled(startup, exe); got != c.want {
			t.Errorf("%s: Enabled = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDisableRemovesOnlyTheUnravisShortcut(t *testing.T) {
	startup, exe, _ := setup(t)
	if err := Enable(startup, exe); err != nil {
		t.Fatal(err)
	}
	neighbour := filepath.Join(startup, "Other.lnk")
	if err := os.WriteFile(neighbour, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Disable(startup, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(LinkPath(startup)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shortcut still there: %v", err)
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Fatalf("neighbour removed: %v", err)
	}
}

func TestDisableKeepsAShortcutThatPointsElsewhere(t *testing.T) {
	startup, exe, _ := setup(t)
	other := filepath.Join(t.TempDir(), "other.exe")
	for name, data := range map[string][]byte{"other target": lnkBytes(t, other, Arg), "garbage": []byte("garbage")} {
		if err := os.WriteFile(LinkPath(startup), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := Disable(startup, exe); !errors.Is(err, ErrNotOurs) {
			t.Errorf("%s: err = %v", name, err)
		}
		if got, err := os.ReadFile(LinkPath(startup)); err != nil || string(got) != string(data) {
			t.Errorf("%s: file changed: %v", name, err)
		}
	}
	_ = os.Remove(LinkPath(startup))
	if err := os.Mkdir(LinkPath(startup), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Disable(startup, exe); !errors.Is(err, ErrNotOurs) {
		t.Errorf("directory: err = %v", err)
	}
}

func TestDisableWhenMissingIsNotAnError(t *testing.T) {
	startup, exe, _ := setup(t)
	if err := Disable(startup, exe); err != nil {
		t.Fatal(err)
	}
}

func TestAllowedNeedsTheStateFile(t *testing.T) {
	_, exe, _ := setup(t)
	if !Allowed(exe) {
		t.Fatal("installed exe not allowed")
	}
	if err := os.Remove(filepath.Join(filepath.Dir(exe), "setup-state.json")); err != nil {
		t.Fatal(err)
	}
	if Allowed(exe) {
		t.Fatal("allowed without setup-state.json")
	}
}
