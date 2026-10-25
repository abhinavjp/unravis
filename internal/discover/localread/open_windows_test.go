//go:build windows

package localread

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// tempDir returns a canonical (long-name, final-path) temp directory.
func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckShape(filepath.Join(d, "x.log")); err != nil {
		t.Skipf("temp dir has no canonical local shape: %v", err)
	}
	return d
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// openShareAll opens path the way a well-behaved logger does: share everything.
func openShareAll(t *testing.T, path string, access uint32, create uint32) *os.File {
	t.Helper()
	p, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(p, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, create, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	return os.NewFile(uintptr(h), "test-writer")
}

func TestWindowsOpenReadsFile(t *testing.T) {
	p := filepath.Join(tempDir(t), "u_ex010911.log")
	writeFile(t, p, "alpha\nbeta\n")
	info, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID.IsZero() || info.Size != 11 || info.Base != "u_ex010911.log" || info.ModTime.IsZero() {
		t.Fatalf("info %+v", info)
	}
	rc, info2, err := Open(p, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if info2.ID != info.ID {
		t.Fatalf("ids differ %+v %+v", info, info2)
	}
	if got := readAll(t, rc); got != "alpha\nbeta\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWindowsOpenAcceptsCaseDifference(t *testing.T) {
	d := tempDir(t)
	p := filepath.Join(d, "Mixed.Log")
	writeFile(t, p, "x\n")
	id, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	up := strings.ToUpper(p)
	rc, _, err := Open(up, id.ID)
	if err != nil {
		t.Fatalf("case-different path refused: %v", err)
	}
	rc.Close()
}

func TestWindowsFrozenLengthOnGrowingRealFile(t *testing.T) {
	p := filepath.Join(tempDir(t), "grow.log")
	writeFile(t, p, "one\ntwo\n")
	id, _ := Identify(p)
	rc, info, err := Open(p, id.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	w := openShareAll(t, p, windows.FILE_APPEND_DATA, windows.OPEN_EXISTING)
	defer w.Close()
	if _, err := w.WriteString("three\nfour\n"); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc); got != "one\ntwo\n" || info.Size != 8 {
		t.Fatalf("got %q size %d", got, info.Size)
	}
}

func TestWindowsOpenWhileAnotherHandleWritesAndDeletes(t *testing.T) {
	p := filepath.Join(tempDir(t), "app.log")
	writeFile(t, p, "start\n")
	id, _ := Identify(p)

	// The application's own handle was opened first and shares read+write only
	// (Go's default), exactly the case that breaks readers that ask for less.
	app, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	rc, _, err := Open(p, id.ID)
	if err != nil {
		t.Fatalf("open while the app holds a write handle: %v", err)
	}
	if _, err := app.WriteString("app wrote after we opened\n"); err != nil {
		t.Fatalf("our open blocked the writer: %v", err)
	}
	app.Close()

	// A writer that opens after us, with delete sharing, can write and delete.
	w := openShareAll(t, p, windows.FILE_APPEND_DATA|windows.DELETE, windows.OPEN_EXISTING)
	if _, err := w.WriteString("second writer\n"); err != nil {
		t.Fatalf("our open blocked a second writer: %v", err)
	}
	w.Close()
	if err := os.Remove(p); err != nil {
		t.Fatalf("our open blocked deleting the log (FILE_SHARE_DELETE missing?): %v", err)
	}
	// The bytes up to the frozen length are still readable after the delete.
	if got := readAll(t, rc); got != "start\n" {
		t.Fatalf("got %q", got)
	}
	rc.Close()
	if _, err := Identify(p); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted file: %v", err)
	}
}

func TestWindowsOpenRefusesDirectoryAndMissing(t *testing.T) {
	d := tempDir(t)
	if _, err := Identify(d); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("directory: %v", err)
	}
	if _, err := Identify(filepath.Join(d, "nope.log")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := Open(filepath.Join(d, "nope.log"), FileID{VolumeSerial: 1, FileIndex: 1}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing via Open: %v", err)
	}
}

func TestWindowsOpenRefusesBadShapes(t *testing.T) {
	d := tempDir(t)
	p := filepath.Join(d, "a.log")
	writeFile(t, p, "x\n")
	id, _ := Identify(p)
	for name, bad := range map[string]string{
		"dotdot":  d + `\sub\..\a.log`,
		"ads":     p + ":hidden",
		"unc":     `\\localhost\` + strings.Replace(p, ":", "$", 1),
		"longpfx": `\\?\` + p,
		"device":  `\\.\` + p,
		"forward": strings.ReplaceAll(p, `\`, "/"),
	} {
		if _, _, err := Open(bad, id.ID); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestWindowsOpenRefusesJunctionInParent(t *testing.T) {
	d := tempDir(t)
	target := filepath.Join(d, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target, "a.log"), "inside\n")
	link := filepath.Join(d, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J failed: %v %s", err, out)
	}
	viaLink := filepath.Join(link, "a.log")
	// Sanity: the OS itself would happily read through the junction.
	if _, err := os.Stat(viaLink); err != nil {
		t.Skipf("junction unusable: %v", err)
	}
	direct, err := Identify(filepath.Join(target, "a.log"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(viaLink, direct.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("junction parent: %v", err)
	}
	if _, err := Identify(viaLink); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("junction parent identify: %v", err)
	}
	// The junction itself as the target is a reparse point and a directory.
	if _, err := Identify(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("junction leaf: %v", err)
	}
}

func TestWindowsOpenRefusesSymlinkFile(t *testing.T) {
	d := tempDir(t)
	target := filepath.Join(d, "real.log")
	writeFile(t, target, "inside\n")
	link := filepath.Join(d, "link.log")
	if out, err := exec.Command("cmd", "/c", "mklink", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink (file symlink) failed, needs privilege: %v %s", err, out)
	}
	direct, _ := Identify(target)
	if _, _, err := Open(link, direct.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink file: %v", err)
	}
	if _, err := Identify(link); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink file identify: %v", err)
	}
}

func TestWindowsOpenRefusesShortNameAlias(t *testing.T) {
	d := tempDir(t)
	long := filepath.Join(d, "a-rather-long-file-name.log")
	writeFile(t, long, "x\n")
	lp, _ := windows.UTF16PtrFromString(long)
	buf := make([]uint16, 600)
	n, err := windows.GetShortPathName(lp, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("no short name: %v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, long) || !strings.Contains(short, "~") {
		t.Skip("8.3 names are disabled on this volume")
	}
	id, _ := Identify(long)
	if _, _, err := Open(short, id.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("8.3 alias: %v", err)
	}
}

func TestWindowsSwapDetection(t *testing.T) {
	d := tempDir(t)
	a, b := filepath.Join(d, "a.log"), filepath.Join(d, "b.log")
	writeFile(t, a, "real log\n")
	writeFile(t, b, "something else entirely\n")
	idA, err := Identify(a)
	if err != nil {
		t.Fatal(err)
	}
	// Scan saw a.log. Before the read, a.log is moved away and b.log takes its name.
	if err := os.Rename(a, filepath.Join(d, "a-old.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(b, a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(a, idA.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("swapped file accepted: %v", err)
	}
	// And through the range reader.
	if _, err := OpenRange(context.Background(), System(), a, idA.ID, RangeRequest{}, RangeOptions{Limiter: noLimit}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("swapped file accepted by OpenRange: %v", err)
	}
	// A different volume serial with the same index is also a different file.
	idNow, _ := Identify(a)
	other := idNow.ID
	other.VolumeSerial ^= 1
	if _, _, err := Open(a, other); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("wrong volume serial accepted: %v", err)
	}
	if rc, _, err := Open(a, idNow.ID); err != nil {
		t.Fatalf("correct identity refused: %v", err)
	} else {
		rc.Close()
	}
}

func TestWindowsRangeAndWatermarkOnRealFile(t *testing.T) {
	p := filepath.Join(tempDir(t), "u_ex010911.log")
	writeFile(t, p, w3cHead+w3cRows(0, 20))
	id, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	st := NewMemoryWatermarks()
	key := KeyFor(id.ID, []byte("k"), p)
	ctx := context.Background()
	leave := EnterBackground()
	defer leave()

	r, err := Next(ctx, System(), st, key, p, id.ID, HeaderW3C, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := io.ReadAll(r)
	r.Commit(st, id.ModTime)
	r.Close()
	if string(first) != w3cHead+w3cRows(0, 20) {
		t.Fatalf("first range wrong, %d bytes", len(first))
	}

	w := openShareAll(t, p, windows.FILE_APPEND_DATA, windows.OPEN_EXISTING)
	w.WriteString(w3cRows(20, 5) + "2001-09-11 08:00:25 GET /partial")
	w.Close()
	id2, _ := Identify(p)
	r, err = Next(ctx, System(), st, key, p, id2.ID, HeaderW3C, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	second, _ := io.ReadAll(r)
	want := "#Fields: date time cs-method cs-uri-stem sc-status time-taken\r\n" + w3cRows(20, 5)
	if string(second) != want {
		t.Fatalf("second range:\n%q\nwant\n%q", second, want)
	}
}
