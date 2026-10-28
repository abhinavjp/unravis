package sqlite

import (
	"os"
	"strings"
)

// caseFolders lists what is in a store root apart from the store's own dot
// folders (the case index), so a test can say "no case folder was left behind".
func caseFolders(root string) []os.DirEntry {
	entries, _ := os.ReadDir(root)
	var out []os.DirEntry
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e)
		}
	}
	return out
}
