package all_test

import (
	"os"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
)

// The log4net block log is picked on its own content under any upload name,
// the single-line default layout stays with log4net-text, and neither steals
// the other's file.
func TestLog4netAutoDetect(t *testing.T) {
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > importer.HeadSize {
			b = b[:importer.HeadSize]
		}
		return b
	}
	reg := all.Registry()
	blocks := read("log4net/testdata/good-blocks.txt")
	text := read("log4net/testdata/good.log")

	for _, name := range []string{"log.txt", "log.txt.3", "app-log.txt", "orders.dat", "upload"} {
		best, _, tied := reg.Detect(blocks, name)
		if best == nil || best.Name() != "log4net-blocks" || tied != nil {
			t.Errorf("block log named %q: got %v (tied %v), want log4net-blocks alone", name, best, tied)
		}
	}
	for _, name := range []string{"app.log", "log.txt", "upload"} {
		best, _, tied := reg.Detect(text, name)
		if best == nil || best.Name() != "log4net-text" || tied != nil {
			t.Errorf("default layout named %q: got %v (tied %v), want log4net-text alone", name, best, tied)
		}
	}
	if best, _, _ := reg.Detect([]byte("just some prose\nwith two lines\n"), "log.txt"); best != nil {
		t.Errorf("plain prose claimed by %q", best.Name())
	}
}
