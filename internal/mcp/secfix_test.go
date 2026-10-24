package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// L6: the session file is restricted to the user before the token is in it.

const secfixToken = "0123456789abcdef0123456789abcdef0123"

func TestSecuredSessionFileIsRestrictedBeforeTheTokenIsWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	var seen string
	var sizeAtSecure int64 = -1
	secure := func(file string) error {
		seen = file
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		sizeAtSecure = info.Size()
		return nil
	}
	if err := WriteSessionFileSecured(path, 8123, secfixToken, secure); err != nil {
		t.Fatal(err)
	}
	if seen == "" || seen == path {
		t.Fatalf("secure was called on %q, want the temporary file", seen)
	}
	if sizeAtSecure != 0 {
		t.Fatalf("the temporary file held %d bytes when it was restricted, want 0", sizeAtSecure)
	}
	port, token, err := ReadSessionFile(path)
	if err != nil || port != 8123 || token != secfixToken {
		t.Fatalf("read back: %d %q %v", port, token, err)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".session-*.tmp"))
	if len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestSessionFileWritesNothingWhenItCannotBeRestricted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	boom := errors.New("acl failure with sentinel-detail")
	err := WriteSessionFileSecured(path, 8123, secfixToken, func(string) error { return boom })
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "sentinel-detail") || strings.Contains(err.Error(), secfixToken) {
		t.Fatalf("error leaks detail: %v", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("session file exists after a failed restriction: %v", statErr)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".session-*.tmp"))
	if len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}
