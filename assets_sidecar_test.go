//go:build tauri_sidecar

package explorer

import (
	"io/fs"
	"testing"
)

func TestAssetsEmptyWithSidecarTag(t *testing.T) {
	if _, err := fs.Stat(Assets(), "index.html"); err == nil {
		t.Fatal("a tauri_sidecar build must not embed index.html")
	}
	entries, err := fs.ReadDir(Assets(), ".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("a tauri_sidecar build must embed nothing, got %d entries (err %v)", len(entries), err)
	}
}
