//go:build windows

package shelllink

import (
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procGetLongPathName = modkernel32.NewProc("GetLongPathNameW")
)

// longPath returns p in its long form (any 8.3 short names resolved), so the
// paths the shell link reads back (the shell always reports the long form)
// match what the test expected from t.TempDir().
func longPath(p string) string {
	if p == "" {
		return p
	}
	ptr, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	n, _, _ := procGetLongPathName.Call(uintptr(unsafe.Pointer(ptr)), 0, 0)
	if n == 0 || n > 32768 {
		return p // the call failed, or the reply would be absurd: keep the original path
	}
	buf := make([]uint16, n)
	m, _, _ := procGetLongPathName.Call(uintptr(unsafe.Pointer(ptr)), uintptr(unsafe.Pointer(&buf[0])), n)
	if m == 0 || m > n {
		return p
	}
	return windows.UTF16ToString(buf[:m])
}

// shellRead reads the .lnk through the real Windows shell (WScript.Shell). Each field comes back base64 of its UTF-8 bytes, so
// non-ASCII text survives the console code page.
func shellRead(t *testing.T, lnk string) map[string]string {
	t.Helper()
	const script = `$ErrorActionPreference='Stop'
$s=(New-Object -ComObject WScript.Shell).CreateShortcut($env:SHELLLINK_TEST_LNK)
foreach($n in 'TargetPath','Arguments','WorkingDirectory','IconLocation','Description'){
  $v=[string]$s.$n
  Write-Output ($n+'='+[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($v)))
}`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "SHELLLINK_TEST_LNK="+lnk)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("powershell: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			t.Fatalf("bad field %q: %v", line, err)
		}
		got[k] = string(raw)
	}
	return got
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Skipf("cannot open %s: %v", from, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}

// ansi is what WScript.Shell gives back for text outside the ANSI code page: it is an ANSI COM object, so characters it cannot hold
// (here the Chinese ones) read as '?'. The shortcut itself holds them; this is only the limit of the reader.
func ansi(s string) string {
	return strings.Map(func(r rune) rune {
		if r > 0xFF {
			return '?'
		}
		return r
	}, s)
}

// TestShellReadsShortcut checks the Windows shell itself resolves what Bytes writes: target, arguments, folder, icon and description.
func TestShellReadsShortcut(t *testing.T) {
	notepad := filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe")
	if _, err := os.Stat(notepad); err != nil {
		t.Skip("no notepad.exe")
	}
	root := longPath(t.TempDir())
	cases := map[string]string{
		"plain":    filepath.Join(root, "Unravis", "unravis.exe"),
		"spaces":   filepath.Join(root, "Program Files", "Unravis App", "unravis.exe"),
		"nonascii": filepath.Join(root, "Ünravis 文件", "ünravis.exe"),
	}
	for name, exe := range cases {
		t.Run(name, func(t *testing.T) {
			if acp := windows.GetACP(); name == "nonascii" && acp != 1252 {
				t.Skipf("the shell reads shortcut text on the ANSI code page, here %d, not 1252: the non-ASCII folder name cannot round-trip", acp)
			}
			copyFile(t, notepad, exe)
			dir := filepath.Dir(exe)
			sc := Shortcut{Target: exe, Args: "--quit", WorkDir: dir, Description: "Unravis " + name, Icon: exe, IconIndex: 0}
			data, err := sc.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			lnk := filepath.Join(root, name+".lnk")
			if err := os.WriteFile(lnk, data, 0o600); err != nil {
				t.Fatal(err)
			}
			got := shellRead(t, lnk)
			want := map[string]string{
				"TargetPath":       ansi(exe),
				"Arguments":        "--quit",
				"WorkingDirectory": ansi(dir),
				"IconLocation":     ansi(exe) + ",0",
				"Description":      "Unravis " + name,
			}
			for k, w := range want {
				if !strings.EqualFold(got[k], w) {
					t.Errorf("%s = %q, want %q", k, got[k], w)
				}
			}
		})
	}
}
