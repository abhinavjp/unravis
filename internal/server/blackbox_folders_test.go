package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRec(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanBlackboxFoldersRanksNewestAndRecommends(t *testing.T) {
	root := t.TempDir()
	writeRec(t, filepath.Join(root, "OldBlackbox"), "bb-20250101-10.ndjson")
	writeRec(t, filepath.Join(root, "NewBlackbox"), "bb-20250301-09.ndjson")
	writeRec(t, filepath.Join(root, "NewBlackbox"), "bb-20250301-11.ndjson")
	writeRec(t, filepath.Join(root, "Unravis", "recordings"), "bb-20240101-00.ndjson") // one level down
	writeRec(t, filepath.Join(root, "Other"), "bb-20260101-00.ndjson")                 // name does not match: not searched
	writeRec(t, filepath.Join(root, "EmptyBlackbox"), "notes.txt")                     // no recordings: not offered

	got, complete := scanBlackboxFolders(context.Background(), []string{root}, "")
	if !complete || len(got) != 3 {
		t.Fatalf("complete=%v folders=%v", complete, got)
	}
	if filepath.Base(got[0].Path) != "NewBlackbox" || !got[0].Recommended || got[0].Recordings != 2 {
		t.Fatalf("first = %+v", got[0])
	}
	if want := time.Date(2025, 3, 1, 11, 0, 0, 0, time.UTC); got[0].Newest == nil || !got[0].Newest.Equal(want) {
		t.Fatalf("newest = %v", got[0].Newest)
	}
	if got[1].Recommended || got[2].Recommended {
		t.Fatalf("only one is recommended: %+v", got)
	}
}

func TestScanBlackboxFoldersServiceFolderLeadsAndCancelStops(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(t.TempDir(), "svc")
	writeRec(t, svc, "bb-20240101-00.ndjson")
	writeRec(t, filepath.Join(root, "NewBlackbox"), "bb-20250301-09.ndjson")
	got, _ := scanBlackboxFolders(context.Background(), []string{root}, svc)
	if len(got) != 2 || !got[0].FromService || !got[0].Recommended {
		t.Fatalf("service folder should lead: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, complete := scanBlackboxFolders(ctx, []string{root}, "")
	if complete || len(got) != 0 {
		t.Fatalf("cancelled scan: complete=%v %v", complete, got)
	}
}
