//go:build unix

package cmdtree

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A named pipe as the --request file must not hold the program: it is refused before it is opened.
func TestRequestFileThatIsAPipeIsRefused(t *testing.T) {
	o := newOwner(t, nil)
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skip("no fifo here:", err)
	}
	done := make(chan result, 1)
	go func() { done <- o.run("audit", "guide", "--request", p, "--timeout", "1s", "--json") }()
	select {
	case r := <-done:
		if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
			t.Errorf("%+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the command hung on a named pipe")
	}
}
