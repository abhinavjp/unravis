package sealedfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicReplacesAndLeavesNoTempFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.dat")
	for _, content := range []string{"first", "second, longer than the first"} {
		if err := WriteAtomic(path, ".state-*.tmp", []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, []byte(content)) {
			t.Fatalf("content = %q, %v", got, err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
