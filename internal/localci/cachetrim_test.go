package localci

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeCacheFile(t *testing.T, path string, size int, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestTrimCachesRemovesOldestFirstAndKeepsRecentOnes(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	gb := filepath.Join(s.CachesPath(), "go-build")
	writeCacheFile(t, filepath.Join(gb, "README"), 10, now.Add(-90*24*time.Hour))
	writeCacheFile(t, filepath.Join(gb, "aa", "old-a"), 400, now.Add(-72*time.Hour))
	writeCacheFile(t, filepath.Join(gb, "bb", "mid-a"), 400, now.Add(-48*time.Hour))
	writeCacheFile(t, filepath.Join(gb, "cc", "new-a"), 400, now.Add(-3*time.Hour))
	writeCacheFile(t, filepath.Join(gb, "dd", "fresh-a"), 400, now.Add(-10*time.Minute))
	mod := filepath.Join(s.CachesPath(), "go-mod", "m", "f.go")
	writeCacheFile(t, mod, 5000, now.Add(-400*time.Hour))

	freed, err := s.TrimCaches(context.Background(), 800, now)
	if err != nil {
		t.Fatal(err)
	}
	if freed != 800 {
		t.Fatalf("freed %d bytes, want 800 (the two oldest files)", freed)
	}
	for p, want := range map[string]bool{
		filepath.Join(gb, "README"):        true,
		filepath.Join(gb, "aa", "old-a"):   false,
		filepath.Join(gb, "bb", "mid-a"):   false,
		filepath.Join(gb, "cc", "new-a"):   true,
		filepath.Join(gb, "dd", "fresh-a"): true,
		mod:                                true,
	} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s: exists=%v, want %v", p, err == nil, want)
		}
	}
}

func TestTrimCachesNeverTouchesFilesUsedInTheLastTwoHours(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	p := filepath.Join(s.CachesPath(), "go-build", "aa", "busy")
	writeCacheFile(t, p, 5000, now.Add(-time.Hour))
	freed, err := s.TrimCaches(context.Background(), 1, now)
	if err != nil || freed != 0 {
		t.Fatalf("freed %d, err %v: want nothing removed", freed, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("a file in use was removed")
	}
}

func TestTrimCachesGivesTheUntrustedCacheHalfTheLimit(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-24 * time.Hour)
	for _, root := range []string{s.CachesPath(), s.UntrustedCachesPath()} {
		writeCacheFile(t, filepath.Join(root, "go-build", "aa", "x"), 300, old)
		writeCacheFile(t, filepath.Join(root, "go-build", "bb", "y"), 300, old.Add(time.Hour))
	}
	if _, err := s.TrimCaches(context.Background(), 600, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.CachesPath(), "go-build", "aa", "x")); err != nil {
		t.Error("the owner's cache fits in 600 bytes and must stay whole")
	}
	if _, err := os.Stat(filepath.Join(s.UntrustedCachesPath(), "go-build", "aa", "x")); err == nil {
		t.Error("the untrusted cache is over 300 bytes: its oldest file must go")
	}
}

func TestTrimCachesWithoutACacheFolderIsFine(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if freed, err := s.TrimCaches(context.Background(), 1, time.Now()); err != nil || freed != 0 {
		t.Fatalf("freed %d, err %v", freed, err)
	}
}
