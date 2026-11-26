package update

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func tmpLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			out = append(out, e.Name())
		}
	}
	return out
}

// A stale or hostile file at the old fixed temp name must not block or poison the write (RV-SUP-7, W-38).
func TestWriteJSONIgnoresAStaleFixedTempName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	if err := os.Mkdir(path+".tmp", 0o750); err != nil { // a leftover the old fixed name could never write over
		t.Fatal(err)
	}
	if err := writeJSON(path, healthState{To: "1.1.0", Starts: 2}); err != nil {
		t.Fatalf("a stale .tmp blocked the write: %v", err)
	}
	var got healthState
	if err := readJSON(path, &got); err != nil || got.Starts != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestWriteJSONLeavesNoTempFileWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	if err := os.Mkdir(path, 0o750); err != nil { // a folder under the real name: the rename cannot succeed
		t.Fatal(err)
	}
	if err := writeJSON(path, healthState{To: "1.1.0"}); err == nil {
		t.Fatal("writing over a folder must fail")
	}
	if left := tmpLeftovers(t, dir); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// Two writers at once must leave one whole, valid file and no temp files.
func TestWriteJSONConcurrentWritersLeaveAValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health.json")
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 1; i <= 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// a failed rename on a busy target is allowed (Windows); a torn file is not
			if writeJSON(path, healthState{To: "1.1.0", Starts: n}) == nil {
				ok.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() == 0 {
		t.Fatal("no writer succeeded")
	}
	var got healthState
	if err := readJSON(path, &got); err != nil || got.To != "1.1.0" || got.Starts < 1 || got.Starts > 8 {
		t.Fatalf("file is not a whole state: %+v %v", got, err)
	}
	if left := tmpLeftovers(t, dir); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// A 401-byte message whose last character is two bytes is cut before that character, not through it (RV-SUP-7 nit).
func TestApplyResultMessageIsCutOnACharacterBoundary(t *testing.T) {
	msg := strings.Repeat("x", 399) + "é" // 'é' is two bytes: bytes 399 and 400, so 401 in all
	if len(msg) != 401 {
		t.Fatalf("setup: %d", len(msg))
	}
	dir := t.TempDir()
	if err := WriteApplyResult(dir, ApplyResult{From: "1.0.0", To: "1.1.0", Outcome: "failed", Message: msg}); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadApplyResult(dir)
	if !ok {
		t.Fatal("not read back")
	}
	if !utf8.ValidString(got.Message) || got.Message != strings.Repeat("x", 399) {
		t.Fatalf("message cut badly: %d bytes, valid=%v", len(got.Message), utf8.ValidString(got.Message))
	}
	// a short message is untouched
	if cutMessage("café", 400) != "café" || cutMessage("", 400) != "" {
		t.Fatal("short message changed")
	}
	if g := cutMessage("é", 1); g != "" {
		t.Fatalf("%q", g)
	}
}
