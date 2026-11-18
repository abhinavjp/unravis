//go:build windows

package blackbox

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func writeHelper(t *testing.T, content string) (path, sum string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "unravis-blackbox.exe")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(s[:])
}

func withPin(t *testing.T, pin string) {
	t.Helper()
	old := PinnedHelperSHA256
	PinnedHelperSHA256 = pin
	t.Cleanup(func() { PinnedHelperSHA256 = old })
}

// SEC-01: the program is locked against changes from the check until release.
func TestGuardHelperLocksTheFileUntilReleased(t *testing.T) {
	path, sum := writeHelper(t, "MZ not really a program")
	withPin(t, sum)
	release, err := GuardHelper(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("swapped"), 0o755); err == nil {
		t.Fatal("the program could be rewritten while it was locked")
	}
	if err := os.Remove(path); err == nil {
		t.Fatal("the program could be deleted while it was locked")
	}
	if err := os.Rename(path, path+".old"); err == nil {
		t.Fatal("the program could be renamed while it was locked")
	}
	release()
	release2, err := GuardHelper(path)
	if err != nil {
		t.Fatalf("not usable after release: %v", err)
	}
	release2()
	if err := os.WriteFile(path, []byte("swapped"), 0o755); err != nil {
		t.Fatalf("still locked after release: %v", err)
	}
}

func TestGuardHelperPinnedHash(t *testing.T) {
	path, sum := writeHelper(t, "MZ pinned")
	withPin(t, sum)
	release, err := GuardHelper(path)
	if err != nil {
		t.Fatalf("the program with the pinned hash was refused: %v", err)
	}
	release()
	withPin(t, strings.Repeat("0", 64))
	if release, err := GuardHelper(path); err == nil {
		release()
		t.Fatal("a program that is not the pinned one was accepted")
	}
}

// Without a pin an unsigned developer build is accepted; a missing file is not.
func TestGuardHelperUnsignedBuildAndMissingFile(t *testing.T) {
	withPin(t, "")
	path, _ := writeHelper(t, "MZ unsigned developer build")
	release, err := GuardHelper(path)
	if err != nil {
		t.Fatalf("unsigned build refused without a pin: %v", err)
	}
	release()
	if release, err := GuardHelper(filepath.Join(t.TempDir(), "missing.exe")); err == nil {
		release()
		t.Fatal("a missing program was accepted")
	}
}

// SEC-02: the helper is started directly. Symbols a command interpreter would
// act on are plain text in an argument; the answer file is the last argument.
func TestElevatedParamsAreQuotedAndEndWithTheAnswerFile(t *testing.T) {
	got := elevatedParams([]string{"install", "--json", "--config", `C:\Temp\a b\x&calc.json`, "--x", "50%"}, `C:\Temp\out\answer.json`)
	if !strings.HasSuffix(got, AnswerFileFlag+` C:\Temp\out\answer.json`) {
		t.Fatalf("answer file not last: %s", got)
	}
	if !strings.Contains(got, `"C:\Temp\a b\x&calc.json"`) {
		t.Fatalf("an argument with a space was not quoted: %s", got)
	}
	if strings.Contains(got, "cmd") {
		t.Fatalf("a command interpreter is in the line: %s", got)
	}
}

func TestElevatedRunRefusesControlCharactersOnly(t *testing.T) {
	// Symbols such as & are fine; a line break cannot be carried on a command line.
	_, err := OSElevator{}.Run(t.Context(), `C:\x\unravis-blackbox.exe`, []string{"install", "a\nb"}, true)
	if err == nil || !strings.Contains(err.Error(), "control character") {
		t.Fatalf("%v", err)
	}
}

// trustOf opens path the way GuardHelper does and returns the Authenticode
// result code of verifyTrust.
func trustOf(t *testing.T, path string) uint32 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	return verifyTrust(path, h)
}

// signedSystemExe finds a Windows program with a signature inside the file
// (notepad.exe and cmd.exe are signed through a catalog instead, which
// WinVerifyTrust reports as "no signature" for the bare file).
func signedSystemExe(t *testing.T) string {
	t.Helper()
	root := os.Getenv("SystemRoot")
	for _, n := range []string{`System32\svchost.exe`, `explorer.exe`, `System32\winlogon.exe`, `System32\services.exe`} {
		p := filepath.Join(root, n)
		if _, err := os.Stat(p); err == nil && trustOf(t, p) == 0 {
			return p
		}
	}
	t.Skip("no Windows program with an embedded signature was found on this PC")
	return ""
}

// SEC-FIX-02: the trust check is given the file path, so a validly signed system
// program verifies, and a file with no signature does not.
func TestVerifyTrustAcceptsASignedProgramAndNotAnUnsignedFile(t *testing.T) {
	signed := signedSystemExe(t)
	if code := trustOf(t, signed); code != 0 {
		t.Fatalf("a signed Windows program did not verify: 0x%08X", code)
	}
	plain, _ := writeHelper(t, "MZ not signed at all")
	code := trustOf(t, plain)
	if code == 0 {
		t.Fatal("an unsigned file verified as signed")
	}
	if code != trustNoSignature && code != trustSubjectFormatUnk && code != trustProviderUnknown {
		t.Fatalf("an unsigned file gave 0x%08X, not a plain 'no signature' answer", code)
	}
}

// SEC-FIX-02: a signed program changed after signing is refused, even with no pin,
// which proves the signature is really checked and not skipped as "unknown".
func TestGuardHelperRefusesATamperedSignedProgram(t *testing.T) {
	withPin(t, "")
	signed := signedSystemExe(t)
	data, err := os.ReadFile(signed)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.exe")
	if err := os.WriteFile(good, data, 0o755); err != nil {
		t.Fatal(err)
	}
	release, err := GuardHelper(good)
	if err != nil {
		t.Fatalf("an unchanged signed program was refused: %v", err)
	}
	release()
	data[len(data)/3] ^= 0xFF
	bad := filepath.Join(dir, "bad.exe")
	if err := os.WriteFile(bad, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if release, err := GuardHelper(bad); err == nil {
		release()
		t.Fatal("a signed program with changed bytes was accepted")
	}
}
