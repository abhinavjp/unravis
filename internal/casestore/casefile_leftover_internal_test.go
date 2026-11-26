package casestore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// If something writes into the placeholder between the "is it empty" check and
// the removal, the file is put back untouched and the target is reported taken.
func TestReclaimLeftoverNeverDeletesContentWrittenInTheGap(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "case"+CaseFileExt)
	if err := os.WriteFile(dst, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(dst, past, past); err != nil {
		t.Fatal(err)
	}
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
	for _, e := range mustReadDir(t, dir) {
		if strings.HasPrefix(e, caseFileAsidePrefix) {
			t.Fatalf("private file %s left behind", e)
		}
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
