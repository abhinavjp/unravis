package blackbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"api-audit-explorer/internal/update"
)

func TestReadUpdateSource(t *testing.T) {
	dir := t.TempDir()
	if got, err := ReadUpdateSource(dir); got != "" || err != nil {
		t.Fatalf("no file: %q %v", got, err)
	}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, UpdateSourceFileName), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for in, want := range map[string]string{
		"\\\\fileserver\\feed\r\n":      "\\\\fileserver\\feed",
		"\ufeff\"D:\\updates\"\r\nmore": "D:\\updates",
		"\r\n\r\nC:\\x y\\z\r\n":        "C:\\x y\\z",
		"   \r\n":                       "",
		"/srv/feed\n":                   "/srv/feed",
	} {
		write(in)
		if got, err := ReadUpdateSource(dir); got != want || err != nil {
			t.Errorf("%q: got %q, %v want %q", in, got, err, want)
		}
	}
	if runtime.GOOS != "windows" { // a rooted path is a full path only off Windows
		write("/srv/feed\n")
		if got, err := ReadUpdateSource(dir); got != "/srv/feed" || err != nil {
			t.Errorf("rooted path: %q %v", got, err)
		}
	} else {
		write("/srv/feed\n")
		if got, err := ReadUpdateSource(dir); got != "" || err == nil {
			t.Errorf("rooted path accepted on Windows: %q", got)
		}
	}
	for _, bad := range []string{"feed", "..\\x", "D:feed", "D:\\a\\..\\b", "\\\\", "\\\\server", "\\\\?\\C:\\x", "\\\\.\\pipe\\x", "C:\\a\x00b", "http://example.com/feed"} {
		write(bad)
		if got, err := ReadUpdateSource(dir); got != "" || err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestUpdateFolderSentence(t *testing.T) {
	r := resultSentence(update.Result{Status: update.StatusUnreachable, Detail: "the update folder has no rc.json"})
	if r != "The update folder has no rc.json." {
		t.Fatal(r)
	}
}
