//go:build !tauri_sidecar

package explorer

import (
	"io/fs"
	"testing"
)

// The normal build embeds the dashboard (BL-422 keeps this true: only the tauri_sidecar tag removes it).
func TestAssetsEmbedTheDashboard(t *testing.T) {
	if _, err := fs.Stat(Assets(), "index.html"); err != nil {
		t.Fatalf("the normal build must embed index.html: %v", err)
	}
}
