//go:build windows

package localread

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Real-OS smoke checks of the hardened Windows open. They run only with
// UNRAVIS_WINSMOKE=1 and use a temp folder under the test's own directory.

func winSmoke(t *testing.T) {
	t.Helper()
	if os.Getenv("UNRAVIS_WINSMOKE") != "1" {
		t.Skip("set UNRAVIS_WINSMOKE=1 to run the real-OS smoke checks")
	}
}

// holdOpen starts another process that opens path for write with the given
// .NET FileShare value and holds it until the test ends. It returns once the
// other process says it holds the file.
func holdOpen(t *testing.T, path, share string) {
	t.Helper()
	ready := path + ".ready"
	script := `$f=[IO.File]::Open($env:UNRAVIS_HOLD,'Append','Write',$env:UNRAVIS_SHARE);` +
		`$w=New-Object IO.StreamWriter($f);$w.WriteLine('line from the other process');$w.Flush();` +
		`Set-Content -LiteralPath $env:UNRAVIS_READY -Value 1;Start-Sleep 120`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "UNRAVIS_HOLD="+path, "UNRAVIS_SHARE="+share, "UNRAVIS_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start powershell: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the other process never opened the file")
}

func TestWinSmokeOpenFileHeldForWriteByAnotherProcess(t *testing.T) {
	winSmoke(t)
	for _, share := range []string{"ReadWrite, Delete", "ReadWrite", "Read"} {
		share := share
		t.Run(strings.NewReplacer(", ", "+", " ", "").Replace(share), func(t *testing.T) {
			p := filepath.Join(tempDir(t), "held.log")
			writeFile(t, p, "first line\n")
			holdOpen(t, p, share)
			info, err := Identify(p)
			if err != nil {
				t.Fatalf("Identify while another process writes (share %s): %v", share, err)
			}
			rc, _, err := Open(p, info.ID)
			if err != nil {
				t.Fatalf("Open while another process writes (share %s): %v", share, err)
			}
			defer rc.Close()
			got := readAll(t, rc)
			if !strings.HasPrefix(got, "first line\n") || !strings.Contains(got, "line from the other process") {
				t.Fatalf("read %d bytes, content does not hold both writers' lines", len(got))
			}
		})
	}
}

// A file another process holds exclusively cannot be read by anyone. The open
// must fail cleanly: not as an unsafe path, not with a panic, and not by
// hanging.
func TestWinSmokeOpenExclusivelyHeldFileFailsCleanly(t *testing.T) {
	winSmoke(t)
	p := filepath.Join(tempDir(t), "excl.log")
	writeFile(t, p, "x\n")
	id, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	holdOpen(t, p, "None")
	done := make(chan error, 1)
	go func() {
		_, _, err := Open(p, id.ID)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("opened a file held with no sharing")
		}
		if errors.Is(err, ErrUnsafePath) {
			t.Fatalf("a sharing violation reported as an unsafe path: %v", err)
		}
		if strings.Contains(err.Error(), filepath.Base(p)) || strings.Contains(err.Error(), os.TempDir()) {
			t.Fatalf("error carries the path: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open hangs on an exclusively held file")
	}
}

func TestWinSmokeJunctions(t *testing.T) {
	winSmoke(t)
	d := tempDir(t)
	real := filepath.Join(d, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(real, "a.log"), "inside\n")
	writeFile(t, filepath.Join(real, "sub", "b.log"), "deep\n")
	direct, err := Identify(filepath.Join(real, "a.log"))
	if err != nil {
		t.Fatal(err)
	}
	directDeep, _ := Identify(filepath.Join(real, "sub", "b.log"))

	mk := func(link, target string) bool {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Skipf("mklink /J: %v %s", err, out)
		}
		return true
	}
	link := filepath.Join(d, "junc")
	mk(link, real)
	// Junction as a parent, as the first segment under the temp root, with a
	// different letter case, and two levels down.
	for name, p := range map[string]string{
		"parent":     filepath.Join(link, "a.log"),
		"upper":      filepath.Join(d, "JUNC", "a.log"),
		"deep":       filepath.Join(link, "sub", "b.log"),
		"leaf-dir":   link,
		"leaf-slash": link + `\`,
	} {
		id := direct.ID
		if name == "deep" {
			id = directDeep.ID
		}
		if _, _, err := Open(p, id); err == nil {
			t.Errorf("%s: opened through a junction", name)
		} else if name != "leaf-slash" && !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := Identify(p); err == nil {
			t.Errorf("%s: identified through a junction", name)
		}
	}

	// A junction chain: junction to a junction.
	link2 := filepath.Join(d, "junc2")
	mk(link2, link)
	if _, _, err := Open(filepath.Join(link2, "a.log"), direct.ID); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("junction chain: %v", err)
	}

	// A dangling junction: the target is gone. Must be a clean refusal or
	// not-found, never a success.
	gone := filepath.Join(d, "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(d, "dangling")
	mk(dangling, gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := Identify(filepath.Join(dangling, "a.log")); err == nil {
		t.Error("dangling junction: identified")
	}

	// A directory symbolic link needs a privilege; check it when it can be made.
	sym := filepath.Join(d, "sym")
	if out, err := exec.Command("cmd", "/c", "mklink", "/D", sym, real).CombinedOutput(); err == nil {
		if _, _, err := Open(filepath.Join(sym, "a.log"), direct.ID); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("directory symlink: %v", err)
		}
	} else {
		t.Logf("directory symlink not creatable here (%s)", strings.TrimSpace(string(out)))
	}

	// The real path still opens, so the refusals above were about the link.
	if rc, _, err := Open(filepath.Join(real, "a.log"), direct.ID); err != nil {
		t.Fatalf("the real path no longer opens: %v", err)
	} else {
		rc.Close()
	}
}

func TestWinSmokeLongPaths(t *testing.T) {
	winSmoke(t)
	d := tempDir(t)

	// A path of 259 characters is the longest the classic API takes and the
	// longest CheckShape allows; it opens.
	mkPathOfLen := func(base string, n int) string {
		dir := base
		for len(dir) < 200 {
			dir = filepath.Join(dir, strings.Repeat("d", 40))
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Skipf("cannot make the folder: %v", err)
		}
		name := n - len(dir) - 1
		if name < 1 {
			t.Skip("temp folder is too deep for this check")
		}
		return filepath.Join(dir, strings.Repeat("f", name-4)+".log")
	}
	ok := mkPathOfLen(filepath.Join(d, "a"), 259)
	if len(ok) != 259 {
		t.Fatalf("test path is %d long", len(ok))
	}
	writeFile(t, ok, "long ok\n")
	info, err := Identify(ok)
	if err != nil {
		t.Fatalf("a 259 character path: %v", err)
	}
	rc, _, err := Open(ok, info.ID)
	if err != nil {
		t.Fatalf("open of a 259 character path: %v", err)
	}
	if got := readAll(t, rc); got != "long ok\n" {
		t.Fatalf("read %d bytes", len(got))
	}
	rc.Close()

	// 260 and more characters: refused up front, though the file exists (made
	// with the long-path prefix) and Windows 11 itself can read it.
	for _, n := range []int{260, 300, 400} {
		long := mkPathOfLen(filepath.Join(d, "b"), n)
		if err := os.MkdirAll(`\\?\`+filepath.Dir(long), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(`\\?\`+long, []byte("too long\n"), 0o600); err != nil {
			t.Skipf("cannot create a long path file: %v", err)
		}
		if _, err := Identify(long); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%d character path: %v", len(long), err)
		}
		if _, _, err := Open(long, FileID{VolumeSerial: 1, FileIndex: 1}); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%d character path Open: %v", len(long), err)
		}
	}
}

func TestWinSmokeShortNames(t *testing.T) {
	winSmoke(t)
	d := tempDir(t)

	// A '~' in a real long name is not an alias and must open.
	p := filepath.Join(d, "my~file~1.log")
	writeFile(t, p, "tilde\n")
	id, err := Identify(p)
	if err != nil {
		t.Fatalf("a name with a tilde that is not an alias: %v", err)
	}
	rc, _, err := Open(p, id.ID)
	if err != nil {
		t.Fatal(err)
	}
	rc.Close()

	// An alias for the file, and one for a folder in the middle of the path.
	dir := filepath.Join(d, "a-folder-with-a-long-name")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	long := filepath.Join(dir, "a-file-with-a-long-name.log")
	writeFile(t, long, "alias\n")
	lid, _ := Identify(long)
	short := func(p string) string {
		lp, _ := windows.UTF16PtrFromString(p)
		buf := make([]uint16, 600)
		n, err := windows.GetShortPathName(lp, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return ""
		}
		return windows.UTF16ToString(buf[:n])
	}
	full := short(long)
	if full == "" || strings.EqualFold(full, long) {
		t.Skip("8.3 names are not generated on this volume")
	}
	shortDirOnly := filepath.Join(short(dir), "a-file-with-a-long-name.log")
	shortFileOnly := filepath.Join(dir, filepath.Base(full))
	for name, alias := range map[string]string{"whole": full, "folder": shortDirOnly, "file": shortFileOnly} {
		if _, _, err := Open(alias, lid.ID); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s alias: %v", name, err)
		}
		if _, err := Identify(alias); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s alias identify: %v", name, err)
		}
	}
	// Lower and upper case of the alias are aliases too.
	if _, err := Identify(strings.ToLower(full)); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("lower-case alias: %v", err)
	}
}

func TestWinSmokeAlternateDataStreams(t *testing.T) {
	winSmoke(t)
	d := tempDir(t)
	p := filepath.Join(d, "a.log")
	writeFile(t, p, "main stream\n")
	if err := os.WriteFile(p+":secret", []byte("hidden stream\n"), 0o600); err != nil {
		t.Skipf("no alternate data streams on this volume: %v", err)
	}
	id, _ := Identify(p)
	for name, bad := range map[string]string{
		"named":      p + ":secret",
		"typed":      p + ":secret:$DATA",
		"default":    p + "::$DATA",
		"dir-stream": d + ":x",
		"index":      d + `::$INDEX_ALLOCATION`,
	} {
		if _, _, err := Open(bad, id.ID); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := Identify(bad); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s identify: %v", name, err)
		}
	}
	// The plain name reads only the main stream.
	rc, _, err := Open(p, id.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if got := readAll(t, rc); got != "main stream\n" {
		t.Fatalf("read %d bytes, expected the main stream only", len(got))
	}
}

// Reserved device names, in every spelling Win32 maps to a device, must never
// be opened. Each probe runs against a deadline so a device that blocks shows
// up as a failure and not a hung run.
func TestWinSmokeDeviceNames(t *testing.T) {
	winSmoke(t)
	d := tempDir(t)
	names := []string{
		"NUL", "nul", "CON", "PRN", "AUX", "COM1", "COM9", "LPT1", "LPT9",
		"NUL.log", "nul.txt", "con.log", "aux.log.old", "COM1.log", "CONIN$", "CONOUT$",
		"NUL ", "NUL.", "COM\u00b9", "COM\u00b2", "LPT\u00b3",
	}
	var cases []string
	for _, n := range names {
		cases = append(cases, filepath.Join(d, n))
		cases = append(cases, d+`\sub\`+n) // a device name under a folder that does not exist
	}
	cases = append(cases, `C:\NUL`, `C:\CON`, `\\.\NUL`, `\\.\COM1`, `\\.\PhysicalDrive0`, `\\.\pipe\x`, `\\?\GLOBALROOT\Device\Null`, `\\.\C:`, `C:`, `C:\`)
	for _, p := range cases {
		p := p
		res := make(chan error, 2)
		go func() { _, err := Identify(p); res <- err }()
		go func() { _, _, err := Open(p, FileID{VolumeSerial: 1, FileIndex: 1}); res <- err }()
		for i := 0; i < 2; i++ {
			select {
			case err := <-res:
				if err == nil {
					t.Errorf("device-style name opened (len %d, tail %q)", len(p), tail(p))
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("a device-style name blocks (tail %q)", tail(p))
			}
		}
	}
	// Names that merely contain a device name are ordinary files.
	for _, n := range []string{"null.log", "console.log", "com10.log", "auxiliary.log", "nul-x.log", "lpt.log"} {
		p := filepath.Join(d, n)
		writeFile(t, p, "ok\n")
		if _, err := Identify(p); err != nil {
			t.Errorf("ordinary name %q refused: %v", n, err)
		}
	}
}

func tail(p string) string {
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// A file replaced after the scan, and in a race while Open runs, must never hand
// back the bytes of the replacement or of a redirected folder.
func TestWinSmokeReplacedFileRace(t *testing.T) {
	winSmoke(t)
	d := tempDir(t)
	a := filepath.Join(d, "a.log")
	writeFile(t, a, "ORIGINAL ORIGINAL ORIGINAL\n")
	orig, err := Identify(a)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(d, "other.log")
	writeFile(t, other, "REPLACEMENT REPLACEMENT\n")
	if err := os.Rename(a, filepath.Join(d, "keep.log")); err != nil {
		t.Fatal(err)
	}
	// Same size, same content, a different file: the identity must still differ.
	writeFile(t, a, "ORIGINAL ORIGINAL ORIGINAL\n")
	if _, _, err := Open(a, orig.ID); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("an identical copy in the same place passed the identity check: %v", err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(d, "keep.log"), a); err != nil {
		t.Fatal(err)
	}

	// The race: one goroutine moves the original and the replacement over the
	// name in turn; the other opens with the original's identity.
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		hold := filepath.Join(d, "hold.log")
		for !stop.Load() {
			// a <- replacement, original parked.
			if os.Rename(a, hold) != nil {
				continue
			}
			if os.Rename(other, a) != nil {
				os.Rename(hold, a)
				continue
			}
			time.Sleep(time.Millisecond)
			// back: replacement parked, original restored.
			os.Rename(a, other)
			os.Rename(hold, a)
		}
	}()
	var opened, refused, failed int
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		rc, _, err := Open(a, orig.ID)
		switch {
		case err == nil:
			b, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.HasPrefix(string(b), "ORIGINAL") {
				stop.Store(true)
				wg.Wait()
				t.Fatalf("Open with the original identity returned other bytes (%d bytes)", len(b))
			}
			opened++
		case errors.Is(err, ErrUnsafePath):
			refused++
		default:
			failed++ // not found or sharing violation while the name is mid-swap
		}
	}
	stop.Store(true)
	wg.Wait()
	t.Logf("race: opened=%d refused=%d other-failures=%d", opened, refused, failed)
	if opened+refused == 0 {
		t.Fatal("the race never reached the opener")
	}
}

// A folder swapped for a junction while Open runs must never read through it.
func TestWinSmokeFolderSwappedForJunctionRace(t *testing.T) {
	winSmoke(t)
	d := tempDir(t)
	logs := filepath.Join(d, "logs")
	elsewhere := filepath.Join(d, "elsewhere")
	for _, dir := range []string{logs, elsewhere} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(logs, "a.log")
	writeFile(t, p, "INSIDE INSIDE INSIDE\n")
	writeFile(t, filepath.Join(elsewhere, "a.log"), "OUTSIDE OUTSIDE OUTSIDE\n")
	id, err := Identify(p)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(d, "probe"), elsewhere).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	parked := filepath.Join(d, "parked")
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if os.Rename(logs, parked) != nil {
				continue
			}
			if exec.Command("cmd", "/c", "mklink", "/J", logs, elsewhere).Run() == nil {
				time.Sleep(2 * time.Millisecond)
				os.Remove(logs) // removes the junction, not its target
			}
			os.Rename(parked, logs)
		}
	}()
	var opened, refused, failed int
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		rc, _, err := Open(p, id.ID)
		switch {
		case err == nil:
			b, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.HasPrefix(string(b), "INSIDE") {
				stop.Store(true)
				wg.Wait()
				t.Fatalf("Open read through a junction (%d bytes)", len(b))
			}
			opened++
		case errors.Is(err, ErrUnsafePath):
			refused++
		default:
			failed++
		}
	}
	stop.Store(true)
	wg.Wait()
	t.Logf("junction race: opened=%d refused=%d other-failures=%d", opened, refused, failed)
	if _, err := os.Stat(filepath.Join(elsewhere, "a.log")); err != nil {
		t.Fatalf("the junction removal reached the target: %v", err)
	}
}
