//go:build !windows

package localread

import (
	"errors"
	"testing"
)

func TestSystemOpenNotSupportedOffWindows(t *testing.T) {
	if _, _, err := Open(testPath, testID); !errors.Is(err, ErrNotSupportedOS) {
		t.Fatalf("Open: %v", err)
	}
	if _, err := Identify(testPath); !errors.Is(err, ErrNotSupportedOS) {
		t.Fatalf("Identify: %v", err)
	}
}
