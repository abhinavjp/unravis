package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Rotation by size: writing past the cap rolls the current file into .1, and
// the oldest backup is dropped so the count never passes maxFiles.
func TestRotateWriterRotatesBySizeAndCount(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotateWriter(dir, FileName, 100, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 60) + "\n" // 61 bytes: two lines exceed 100
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	names := readDirNames(t, dir)
	if len(names) != 3 {
		t.Fatalf("kept %d files, want 3 (current + 2 backups): %v", len(names), names)
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > 122 { // at most two lines per file
			t.Errorf("%s is %d bytes, over the 100-byte rotation size plus one line", name, info.Size())
		}
	}
}

// The full-size defaults are the spec's 5 MB x 5 files.
func TestRotationDefaults(t *testing.T) {
	if MaxSize != 5<<20 {
		t.Errorf("MaxSize = %d, want 5 MB", MaxSize)
	}
	if MaxFiles != 5 {
		t.Errorf("MaxFiles = %d, want 5", MaxFiles)
	}
}

// Open writes slog text lines to the file, sanitized, and the file is created
// with owner-only permissions where the platform has them.
func TestOpenWritesSanitizedLinesToFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	f, err := Open(dir, ParseLevel("info"), func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	f.Logger.Info("test event", "detail", "connect mongodb://u:LogPass123@h/db failed", "rows", 12)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "LogPass123") {
		t.Errorf("log file contains a password: %q", text)
	}
	if !strings.Contains(text, "msg=\"test event\"") || !strings.Contains(text, "rows=12") {
		t.Errorf("log line is not the expected slog text form: %q", text)
	}
}

func TestParseLevel(t *testing.T) {
	for name, want := range map[string]string{
		"debug": "DEBUG", "info": "INFO", "": "INFO", "bogus": "INFO", "warn": "WARN", "error": "ERROR",
	} {
		if got := ParseLevel(name).String(); got != want {
			t.Errorf("ParseLevel(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestStatReportsSizeAndNewest(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir, ParseLevel("info"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.Logger.Info("hello")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info := Stat(dir)
	if info.Path != filepath.Join(dir, FileName) || info.Size == 0 || info.Newest.IsZero() {
		t.Errorf("Stat = %+v, want the written file's path, size and time", info)
	}
}
