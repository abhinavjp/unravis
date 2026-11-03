//go:build windows

package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/instance"
)

// testNames are names no real Unravis uses, so these tests never signal a running app.
func testNames(t *testing.T) instance.Names {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	n, err := instance.NewNames("UnravisTest"+hex.EncodeToString(b), "S-1-5-21-1-2-3")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAskQuitSignalsTheQuitEvent(t *testing.T) {
	names := testNames(t)
	quit := make(chan struct{}, 1)
	stop, err := instance.Listen(context.Background(), names, func() {}, func() {
		select {
		case quit <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer stop()
	if err := askQuitFor(names)(); err != nil {
		t.Fatalf("AskQuit: %v", err)
	}
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("the quit callback never ran")
	}
}

func TestAskQuitWithNoListenerReturnsAnError(t *testing.T) {
	err := askQuitFor(testNames(t))()
	if !errors.Is(err, instance.ErrNoInstance) {
		t.Fatalf("err = %v, want ErrNoInstance", err)
	}
}

func TestNewOSSystemWiresAskQuit(t *testing.T) {
	s, err := NewOSSystem()
	if err != nil {
		t.Fatal(err)
	}
	if s.AskQuit == nil {
		t.Fatal("AskQuit is not wired")
	}
}

func TestNewOSSystemDoesNotUseTheDemoNames(t *testing.T) {
	src, err := os.ReadFile("os_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, "instance.DefaultApp") || strings.Contains(text, "instance.DemoApp") {
		t.Fatal("os_windows.go must use instance.DefaultApp and never instance.DemoApp")
	}
}
