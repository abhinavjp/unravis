package settingsfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/codemode"
)

func write(t *testing.T, s *Store, content string) {
	t.Helper()
	if err := os.WriteFile(s.Path(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMissingFileIsOffWithoutError(t *testing.T) {
	s := New(t.TempDir())
	set, err := s.Load()
	if err != nil || set != (codemode.Settings{}) {
		t.Fatalf("Load = %+v, %v; want off and no error", set, err)
	}
	if s.Settings().Enabled {
		t.Fatal("a missing file turned code mode on")
	}
}

func TestSaveThenLoadRoundTripAndRereadEveryCall(t *testing.T) {
	s := New(t.TempDir())
	want := codemode.Settings{Enabled: true, AllowDegraded: true, KeepResults: true}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings(); got != want {
		t.Fatalf("Settings = %+v, want %+v", got, want)
	}
	// The off switch is honoured by the next call with no restart and no cache.
	if err := s.Save(codemode.Settings{}); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings(); got.Enabled {
		t.Fatal("Settings still on after the file was saved off")
	}
	// A file edited behind the app's back is read too.
	write(t, s, `{"enabled":true}`)
	if !s.Settings().Enabled {
		t.Fatal("Settings did not re-read the file")
	}
}

func TestDamagedFilesAreOffNeverOn(t *testing.T) {
	cases := map[string]string{
		"not json":       "enabled=true",
		"empty":          "",
		"unknown field":  `{"enabled":true,"extra":1}`,
		"wrong type":     `{"enabled":"yes"}`,
		"two values":     `{"enabled":true}{"enabled":true}`,
		"trailing text":  `{"enabled":true} junk`,
		"array":          `[true]`,
		"null is zero":   `null`,
		"truncated":      `{"enabled":tr`,
		"duplicate keys": `{"enabled":true,"enabled":true,"nope":1}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			s := New(t.TempDir())
			write(t, s, content)
			set, err := s.Load()
			if name == "null is zero" {
				// null decodes to the zero value: still off, still fine.
				if set.Enabled {
					t.Fatal("null turned code mode on")
				}
				return
			}
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Load error = %v, want ErrCorrupt", err)
			}
			if set != (codemode.Settings{}) || s.Settings().Enabled {
				t.Fatalf("a damaged file left code mode on: %+v", set)
			}
		})
	}
}

func TestOversizeFileIsCorruptEvenWhenItStartsWithValidJSON(t *testing.T) {
	s := New(t.TempDir())
	write(t, s, `{"enabled":true}`+strings.Repeat(" ", MaxBytes))
	if _, err := s.Load(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load error = %v, want ErrCorrupt", err)
	}
	if s.Settings().Enabled {
		t.Fatal("an oversize file turned code mode on")
	}
}

func TestFolderInPlaceOfTheFileIsCorruptAndNotReplaced(t *testing.T) {
	s := New(t.TempDir())
	if err := os.Mkdir(s.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load error = %v, want ErrCorrupt", err)
	}
	if err := s.Save(codemode.Settings{Enabled: true}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Save error = %v, want ErrCorrupt", err)
	}
	if fi, err := os.Stat(s.Path()); err != nil || !fi.IsDir() {
		t.Fatal("Save replaced the folder")
	}
}

func TestSaveRepairsADamagedFile(t *testing.T) {
	s := New(t.TempDir())
	write(t, s, "garbage")
	if err := s.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if !s.Settings().Enabled {
		t.Fatal("Save did not repair the file")
	}
}

func TestSaveCreatesTheFolder(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "a", "b"))
	if err := s.Save(codemode.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if !s.Settings().Enabled {
		t.Fatal("not saved")
	}
}

func TestFailedWriteLeavesOldFileAndNoTempFile(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Save(codemode.Settings{Enabled: true, KeepResults: true}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("disk full")
	s.replace = func(from, to string) error { return boom }
	if err := s.Save(codemode.Settings{}); !errors.Is(err, boom) {
		t.Fatalf("Save error = %v, want the replace error", err)
	}
	after, err := os.ReadFile(s.Path())
	if err != nil || string(after) != string(before) {
		t.Fatalf("old file changed: %q -> %q (%v)", before, after, err)
	}
	if got := s.Settings(); !got.Enabled || !got.KeepResults {
		t.Fatalf("old settings lost: %+v", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != FileName {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSaveFailsWhenTheFolderIsAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(blocker, "sub"))
	if err := s.Save(codemode.Settings{Enabled: true}); err == nil {
		t.Fatal("Save into an impossible folder succeeded")
	}
	if s.Settings().Enabled {
		t.Fatal("a failed save turned code mode on")
	}
}
