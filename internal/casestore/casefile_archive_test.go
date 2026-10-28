package casestore_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
)

func arKey(t *testing.T) casestore.FileKey {
	t.Helper()
	salt := bytes.Repeat([]byte{7}, 16)
	key, err := masterkey.DeriveKey([]byte(cfPass), salt, fastParams)
	if err != nil {
		t.Fatal(err)
	}
	return casestore.FileKey{Key: key, Salt: salt, Params: fastParams}
}

func arSource(t *testing.T, e *cfEnv, info casestore.CaseInfo) casestore.CaseFileSource {
	t.Helper()
	dir := filepath.Join(e.root, string(info.ID))
	keys, _, err := casestore.ReadKeyFileAny(filepath.Join(dir, casestore.KeyFileName), e.lookup)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(keys.Zero)
	db := filepath.Join(dir, "case.db")
	return casestore.CaseFileSource{
		DBPath: db, CaseID: string(info.ID), Title: info.Title, Created: info.Created, Keys: keys,
		Hold: func() (func(), error) { return casestore.HoldCaseDB(context.Background(), db, keys.DB) },
	}
}

func TestArchivePartRoundTripChecksAndCounts(t *testing.T) {
	e := newCFEnv(t)
	info := cfSeed(t, e, "Round trip", 7)
	fk := arKey(t)
	out := t.TempDir()
	part := filepath.Join(out, "round-trip.unravis-case.part")

	var written int64
	w, err := casestore.WriteArchivePart(context.Background(), part, arSource(t, e, info), fk, func(n int64) { written += n })
	if err != nil {
		t.Fatalf("WriteArchivePart: %v", err)
	}
	if written == 0 {
		t.Error("no progress was reported")
	}
	size, sum, err := casestore.HashFile(context.Background(), part, nil)
	if err != nil || size != w.Bytes || sum != w.SHA256 {
		t.Fatalf("read back: %d %q %v, writer said %d %q", size, sum, err, w.Bytes, w.SHA256)
	}

	scratch := filepath.Join(out, ".verify")
	res, cleanup, err := casestore.CheckCaseFile(context.Background(), part, scratch, casestore.FixedKey(fk.Key), nil)
	if err != nil || cleanup != nil {
		t.Fatalf("CheckCaseFile: %v (cleanup %v)", err, cleanup)
	}
	if res.Counts.Imports != 1 || res.Counts.Events != 7 || res.Info.CaseID != string(info.ID) {
		t.Fatalf("counts %+v info %+v", res.Counts, res.Info)
	}
	if left, _ := os.ReadDir(scratch); len(left) != 0 {
		t.Fatalf("scratch folder left %d entries", len(left))
	}
	// The same file opens with the passphrase typed on another PC.
	if _, _, err := casestore.CheckCaseFile(context.Background(), part, scratch, casestore.PassphraseKey([]byte(cfPass)), nil); err != nil {
		t.Fatalf("typed passphrase: %v", err)
	}
	if _, _, err := casestore.CheckCaseFile(context.Background(), part, scratch, casestore.PassphraseKey([]byte("wrong wrong wrong")), nil); !errors.Is(err, casestore.ErrCaseFile) {
		t.Fatalf("wrong passphrase: %v, want ErrCaseFile", err)
	}
}

func TestArchivePartNeverReplacesAndCleansUp(t *testing.T) {
	e := newCFEnv(t)
	info := cfSeed(t, e, "No replace", 3)
	fk := arKey(t)
	dir := t.TempDir()

	part := filepath.Join(dir, "a.part")
	if err := os.WriteFile(part, []byte("someone else's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := casestore.WriteArchivePart(context.Background(), part, arSource(t, e, info), fk, nil); !errors.Is(err, casestore.ErrCaseFileExists) {
		t.Fatalf("existing part: %v, want ErrCaseFileExists", err)
	}
	if b, _ := os.ReadFile(part); string(b) != "someone else's" {
		t.Fatal("an existing file was changed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gone := filepath.Join(dir, "b.part")
	if _, err := casestore.WriteArchivePart(ctx, gone, arSource(t, e, info), fk, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("cancelled write left %v", err)
	}

	// A step that cancels at the end of the first pass leaves nothing either.
	ctx2, cancel2 := context.WithCancel(context.Background())
	var seen int64
	gone2 := filepath.Join(dir, "c.part")
	_, err := casestore.WriteArchivePart(ctx2, gone2, arSource(t, e, info), fk, func(n int64) {
		seen += n
		cancel2()
	})
	if !errors.Is(err, context.Canceled) || seen == 0 {
		t.Fatalf("cancel mid-write: %v (seen %d)", err, seen)
	}
	if _, err := os.Stat(gone2); !os.IsNotExist(err) {
		t.Fatalf("cancel mid-write left %v", err)
	}
}

func TestCheckCaseFileFindsAFlippedByteAndCleansScratch(t *testing.T) {
	e := newCFEnv(t)
	info := cfSeed(t, e, "Flip", 4)
	fk := arKey(t)
	dir := t.TempDir()
	part := filepath.Join(dir, "flip.unravis-case")
	if _, err := casestore.WriteArchivePart(context.Background(), part, arSource(t, e, info), fk, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(part)
	b[len(b)-100] ^= 0xff
	if err := os.WriteFile(part, b, 0o600); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(dir, ".verify")
	if _, _, err := casestore.CheckCaseFile(context.Background(), part, scratch, casestore.FixedKey(fk.Key), nil); !errors.Is(err, casestore.ErrCaseFile) {
		t.Fatalf("flipped byte: %v, want ErrCaseFile", err)
	}
	if left, _ := os.ReadDir(scratch); len(left) != 0 {
		t.Fatalf("scratch folder left %d entries", len(left))
	}
}

func TestRestoreCaseFileWithAHeldKeyMakesANewCase(t *testing.T) {
	e := newCFEnv(t)
	info := cfSeed(t, e, "Held key", 5)
	fk := arKey(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "held.unravis-case")
	if _, err := casestore.WriteArchivePart(context.Background(), file, arSource(t, e, info), fk, nil); err != nil {
		t.Fatal(err)
	}
	got, err := casestore.RestoreCaseFileWith(context.Background(), e.st, e.lookup, file, casestore.FixedKey(fk.Key))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got.ID == info.ID || len(cfEvents2(t, e, got.ID)) != 5 {
		t.Fatalf("restored %v (original %v)", got.ID, info.ID)
	}
	if _, err := casestore.RestoreCaseFileWith(context.Background(), e.st, e.lookup, file, casestore.FixedKey(bytes.Repeat([]byte{9}, 32))); !errors.Is(err, casestore.ErrCaseFile) {
		t.Fatalf("wrong key: %v, want ErrCaseFile", err)
	}
}

func TestExistingExportStillOpensWithThePassphrase(t *testing.T) {
	// Files made before P3 (WriteCaseFile) keep opening through the new reader.
	e := newCFEnv(t)
	info := cfSeed(t, e, "Old export", 3)
	file := filepath.Join(t.TempDir(), "old.unravis-case")
	if _, err := cfExport(t, e, info, file, cfPass); err != nil {
		t.Fatal(err)
	}
	res, _, err := casestore.CheckCaseFile(context.Background(), file, filepath.Join(filepath.Dir(file), ".verify"), casestore.PassphraseKey([]byte(cfPass)), nil)
	if err != nil || res.Counts.Events != 3 {
		t.Fatalf("%+v %v", res, err)
	}
}
