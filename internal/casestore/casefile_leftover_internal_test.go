package casestore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/masterkey"
)

var leftoverTestParams = masterkey.Params{Time: 1, Memory: 16 << 10, Threads: 1}

// leftoverSource is a source that WriteCaseFile accepts: it copies the bytes of
// the database file as they are, so no real case is needed here.
func leftoverSource(t *testing.T) CaseFileSource {
	t.Helper()
	db := filepath.Join(t.TempDir(), "case.db")
	if err := os.WriteFile(db, bytes.Repeat([]byte("db page "), 600), 0o600); err != nil {
		t.Fatal(err)
	}
	return CaseFileSource{
		DBPath: db, CaseID: strings.Repeat("a", 26), Title: "Leftover",
		Keys: Keys{DB: bytes.Repeat([]byte{1}, 32), Pseudo: bytes.Repeat([]byte{2}, 32)},
	}
}

func leftoverSave(src CaseFileSource, dst string) error {
	_, err := WriteCaseFile(context.Background(), dst, src, []byte("pass"), leftoverTestParams)
	return err
}

func mustWrite(t *testing.T, p string, b []byte, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		past := time.Now().Add(-age)
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func asides(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, e := range mustReadDir(t, dir) {
		if isCaseFileAside(e) {
			out = append(out, e)
		}
	}
	return out
}

// If something writes into the placeholder between the "is it empty" check and
// the removal, the file is put back untouched and the target is reported taken.
func TestReclaimLeftoverNeverDeletesContentWrittenInTheGap(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	mustWrite(t, dst, nil, 5*time.Minute)
	content := []byte("written by someone else after the check")
	leftoverRaceHook = func(aside string) {
		if err := os.WriteFile(aside, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { leftoverRaceHook = nil })
	if reclaimLeftover(dst, time.Now()) {
		t.Fatal("reported the target free although it got content")
	}
	got, err := os.ReadFile(dst) //nolint:gosec // G304: a test path
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("content lost: %q %v", got, err)
	}
	if a := asides(t, dir); len(a) != 0 {
		t.Fatalf("private file %v left behind", a)
	}
}

// Linking back is not possible on every file system (FAT, exFAT, some shares):
// the file is then renamed back, onto a name that is still free.
func TestReclaimLeftoverPutsContentBackWhenLinkingFails(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	mustWrite(t, dst, nil, 5*time.Minute)
	content := []byte("a finished case that landed in the gap")
	leftoverRaceHook = func(aside string) { mustWrite(t, aside, content, 0) }
	linkFile = func(string, string) error { return errors.New("links are not supported here") }
	t.Cleanup(func() { leftoverRaceHook = nil; linkFile = os.Link })
	if reclaimLeftover(dst, time.Now()) {
		t.Fatal("reported the target free although it got content")
	}
	got, err := os.ReadFile(dst) //nolint:gosec // G304: a test path
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("content not back at the target: %q %v", got, err)
	}
	if a := asides(t, dir); len(a) != 0 {
		t.Fatalf("private file %v left behind", a)
	}
}

// If linking fails and the name was taken again meanwhile, nothing is replaced
// and the content stays in the private file, which no sweep removes.
func TestReclaimLeftoverKeepsContentWhenItCannotGoBack(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	mustWrite(t, dst, nil, 5*time.Minute)
	content, other := []byte("the case that must survive"), []byte("a newer file with the old name")
	leftoverRaceHook = func(aside string) {
		mustWrite(t, aside, content, 40*time.Hour)
		mustWrite(t, dst, other, 0)
	}
	linkFile = func(string, string) error { return errors.New("links are not supported here") }
	t.Cleanup(func() { leftoverRaceHook = nil; linkFile = os.Link })
	if reclaimLeftover(dst, time.Now()) {
		t.Fatal("reported the target free")
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, other) { //nolint:gosec // G304: a test path
		t.Fatalf("the newer file was replaced: %q", got)
	}
	a := asides(t, dir)
	if len(a) != 1 {
		t.Fatalf("want the content kept in one private file, have %v", a)
	}
	sweepStaleCaseFileTmp(dir, time.Now().Add(72*time.Hour))
	if got, err := os.ReadFile(filepath.Join(dir, a[0])); err != nil || !bytes.Equal(got, content) { //nolint:gosec // G304: a test path
		t.Fatalf("the sweep removed content: %q %v", got, err)
	}
}

// A different empty file in the place of the one that was checked (a save that
// finished and was replaced meanwhile) is not removed either.
func TestReclaimLeftoverDoesNotRemoveADifferentFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	mustWrite(t, dst, nil, 5*time.Minute)
	leftoverRaceHook = func(aside string) {
		// A different empty file under the private name. The new one is made before
		// the old one goes, so the file system cannot give it the old one's identity.
		mustWrite(t, aside+".new", nil, 0)
		if err := os.Rename(aside+".new", aside); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { leftoverRaceHook = nil })
	if reclaimLeftover(dst, time.Now()) {
		t.Fatal("removed a file that is not the one that was checked")
	}
	if !exists(dst) {
		t.Fatal("the other file is gone")
	}
}

// A 0-byte placeholder that is read-only must not make the name unusable.
func TestReclaimLeftoverClearsAReadOnlyPlaceholder(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	mustWrite(t, dst, nil, 5*time.Minute)
	if err := os.Chmod(dst, 0o444); err != nil {
		t.Fatal(err)
	}
	if !reclaimLeftover(dst, time.Now()) {
		t.Fatal("a read-only left-over was not cleared")
	}
	if exists(dst) {
		t.Fatal("the name is still taken")
	}
	if err := leftoverSave(leftoverSource(t), dst); err != nil {
		t.Fatalf("save after clearing it: %v", err)
	}
}

// The sweep removes stale temporary files and empty stale placeholders set aside,
// and never a set-aside file with content.
func TestSweepNeverRemovesAsideFileWithContent(t *testing.T) {
	dir := t.TempDir()
	day := 30 * time.Hour
	mustWrite(t, filepath.Join(dir, "unravis-case-aside-aaaa.aside"), []byte("a case"), day)
	mustWrite(t, filepath.Join(dir, "unravis-case-aside-bbbb.aside"), nil, day)
	mustWrite(t, filepath.Join(dir, "unravis-case-aside-cccc.aside"), nil, 0)
	mustWrite(t, filepath.Join(dir, "unravis-case-xxxx.tmp"), []byte("half"), day)
	sweepStaleCaseFileTmp(dir, time.Now())
	for name, want := range map[string]bool{
		"unravis-case-aside-aaaa.aside": true, "unravis-case-aside-bbbb.aside": false,
		"unravis-case-aside-cccc.aside": true, "unravis-case-xxxx.tmp": false,
	} {
		if exists(filepath.Join(dir, name)) != want {
			t.Errorf("%s: present=%v, want %v", name, !want, want)
		}
	}
	if isCaseFileTmp("unravis-case-aside-aaaa.aside") {
		t.Error("a set-aside file looks like a temporary file")
	}
}

// A temporary file of a save to another name is not a sign that this target's
// empty placeholder is a left-over: it may belong to a save that is running.
func TestTmpOfAnotherTargetDoesNotMakeAPlaceholderALeftover(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "mine"+CaseFileExt)
	other := filepath.Join(dir, "yours"+CaseFileExt)
	if caseFileTmpTag(dst) == caseFileTmpTag(other) {
		t.Fatal("tags collide")
	}
	mustWrite(t, dst, nil, 0)
	mustWrite(t, filepath.Join(dir, "unravis-case-"+caseFileTmpTag(other)+"-123456.tmp"), []byte("being written"), 0)
	if hasCaseFileTmp(dir, dst) {
		t.Fatal("the other target's temporary file counts for this one")
	}
	if err := leftoverSave(leftoverSource(t), dst); !errors.Is(err, ErrCaseFileExists) {
		t.Fatalf("fresh placeholder, other target's tmp beside it: %v, want ErrCaseFileExists", err)
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() != 0 {
		t.Fatalf("the placeholder was changed: %v %v", fi, err)
	}
	// The same name's own tmp, and a tmp from before tags existed, still count.
	mustWrite(t, filepath.Join(dir, "unravis-case-"+caseFileTmpTag(dst)+"-777.tmp"), []byte("x"), 0)
	if !hasCaseFileTmp(dir, dst) {
		t.Fatal("this target's own temporary file does not count")
	}
	legacy := t.TempDir()
	mustWrite(t, filepath.Join(legacy, "unravis-case-123456.tmp"), []byte("x"), 0)
	if !hasCaseFileTmp(legacy, dst) {
		t.Fatal("an untagged temporary file does not count")
	}
}

// The failure cleanup removes the placeholder only when it is the file this save
// made.
func TestRemoveOwnPlaceholderLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	mk := func() os.FileInfo {
		f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		return fi
	}
	mine := mk()
	removeOwnPlaceholder(dst, mine)
	if exists(dst) {
		t.Fatal("the save's own placeholder was not removed")
	}
	// Someone else's finished case under the same name.
	mine = mk()
	if err := os.Remove(dst); err != nil {
		t.Fatal(err)
	}
	content := []byte("a finished case from another save")
	mustWrite(t, dst, content, 0)
	removeOwnPlaceholder(dst, mine)
	if got, err := os.ReadFile(dst); err != nil || !bytes.Equal(got, content) { //nolint:gosec // G304: a test path
		t.Fatalf("another save's file was removed: %q %v", got, err)
	}
	// Someone else's empty file under the same name.
	mustWrite(t, dst+".new", nil, 0)
	if err := os.Rename(dst+".new", dst); err != nil {
		t.Fatal(err)
	}
	removeOwnPlaceholder(dst, mine)
	if !exists(dst) {
		t.Fatal("another save's empty file was removed")
	}
}

// If the name was taken by another copy of the app while the save ran, the save
// reports it and leaves that file alone, on a failure and on success alike. On
// Windows the open placeholder cannot be taken, which is the stronger guarantee
// and is checked as such.
func TestSaveNeverTouchesAFileAnotherCopyPutAtTheTarget(t *testing.T) {
	for _, fail := range []bool{true, false} {
		name := "save would succeed"
		if fail {
			name = "save fails"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "case"+CaseFileExt)
			src := leftoverSource(t)
			content := []byte("the other copy's finished case")
			took := false
			src.Hold = func() (func(), error) {
				if os.Remove(dst) == nil {
					mustWrite(t, dst, content, 0)
					took = true
				}
				if fail {
					return nil, errors.New("export failed")
				}
				return func() {}, nil
			}
			err := leftoverSave(src, dst)
			if !took {
				if runtime.GOOS != "windows" {
					t.Fatal("the placeholder could not be taken on a system that allows it")
				}
				if fail {
					if err == nil || exists(dst) {
						t.Fatalf("failed save left %v / err %v", exists(dst), err)
					}
					return
				}
				if err != nil {
					t.Fatalf("save: %v", err)
				}
				mustOpenCaseFile(t, dst)
				return
			}
			if fail && err == nil || !fail && !errors.Is(err, ErrCaseFileExists) {
				t.Fatalf("err = %v", err)
			}
			if got, rerr := os.ReadFile(dst); rerr != nil || !bytes.Equal(got, content) { //nolint:gosec // G304: a test path
				t.Fatalf("the other copy's file was changed: %q %v", got, rerr)
			}
			for _, e := range mustReadDir(t, dir) {
				if isCaseFileTmp(e) || isCaseFileAside(e) {
					t.Fatalf("%s left behind", e)
				}
			}
		})
	}
}

// While a save runs, its placeholder cannot be moved aside by another process on
// Windows (the handle is open without share-delete). Everywhere, the second save
// ends without damaging the first one's result.
func TestSecondProcessCannotTakeOverARunningSave(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	src := leftoverSource(t)
	var secondErr error
	src.Hold = func() (func(), error) {
		// What a second copy of the app does: it does not know this process's
		// claims, and sees an empty target with a temporary file beside it.
		savesInFlight.Range(func(k, _ any) bool { savesInFlight.Delete(k); return true })
		if runtime.GOOS == "windows" && reclaimLeftover(dst, time.Now().Add(time.Hour)) {
			t.Error("the placeholder of a running save was taken over")
		}
		secondErr = leftoverSave(leftoverSource(t), dst)
		return func() {}, nil
	}
	firstErr := leftoverSave(src, dst)
	if runtime.GOOS == "windows" {
		if firstErr != nil || !errors.Is(secondErr, ErrCaseFileExists) {
			t.Fatalf("first %v, second %v", firstErr, secondErr)
		}
	} else if firstErr != nil && secondErr != nil {
		t.Fatalf("both saves failed: %v / %v", firstErr, secondErr)
	}
	mustOpenCaseFile(t, dst)
	for _, e := range mustReadDir(t, dir) {
		if e != "case"+CaseFileExt {
			t.Errorf("%s left behind", e)
		}
	}
}

// A running save keeps touching its placeholder, so another copy of the app
// never sees a save that runs for minutes as a left-over.
func TestRunningSaveRefreshesItsPlaceholder(t *testing.T) {
	old := placeholderRefreshEvery
	placeholderRefreshEvery = 20 * time.Millisecond
	t.Cleanup(func() { placeholderRefreshEvery = old })
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	src := leftoverSource(t)
	var fresh bool
	src.Hold = func() (func(), error) {
		past := time.Now().Add(-10 * time.Minute)
		if err := os.Chtimes(dst, past, past); err != nil {
			t.Errorf("chtimes: %v", err)
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if fi, err := os.Lstat(dst); err == nil && time.Since(fi.ModTime()) < time.Minute {
				fresh = true
				break
			}
		}
		return func() {}, nil
	}
	if err := leftoverSave(src, dst); err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("the placeholder of a running save was not refreshed")
	}
}

func TestClaimSavePathRefusesASecondSaveToTheSameName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "case"+CaseFileExt)
	if !claimSavePath(p) {
		t.Fatal("first claim failed")
	}
	if claimSavePath(p) {
		t.Fatal("second save to the same name was allowed")
	}
	releaseSavePath(p)
	if !claimSavePath(p) {
		t.Fatal("claim not released")
	}
	releaseSavePath(p)
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// mustOpenCaseFile checks that path is a complete case file and closes it again
// (an open file cannot be deleted on Windows).
func mustOpenCaseFile(t *testing.T, path string) {
	t.Helper()
	cf, err := OpenCaseFile(path, []byte("pass"))
	if err != nil {
		t.Fatalf("the saved file does not open: %v", err)
	}
	_ = cf.Close()
}
