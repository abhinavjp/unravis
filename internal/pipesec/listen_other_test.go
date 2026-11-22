//go:build !windows

package pipesec

import (
	"context"
	"errors"
	"testing"
)

func TestOtherBuildStubs(t *testing.T) {
	if l, err := Listen(ListenConfig{Name: `\\.\pipe\x`}); l != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Listen: %v %v", l, err)
	}
	if c, _, err := DialVerified(context.Background(), `\\.\pipe\x`, ServerPolicy{}); c != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("DialVerified: %v %v", c, err)
	}
}
