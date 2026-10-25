//go:build !windows

package evtx

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryNotSupportedOffWindows(t *testing.T) {
	_, err := Query(context.Background(), &bytes.Buffer{}, "Application", time.Time{}, time.Time{}, 0)
	if !errors.Is(err, ErrNotSupportedOS) {
		t.Fatalf("err %v", err)
	}
}
