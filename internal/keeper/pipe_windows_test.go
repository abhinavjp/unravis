//go:build windows

package keeper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func testPipeName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`\\.\pipe\UnravisKeeperTest-%d-%s`, os.Getpid(), hex.EncodeToString(b))
}

// TestRealPipe runs the keeper protocol over a real named pipe owned by this test process (no service, no elevation, nothing installed).
func TestRealPipe(t *testing.T) {
	name := testPipeName(t)
	l, err := ListenPipe(name, PipeListenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ListenPipe(name, PipeListenOptions{}); err == nil {
		t.Fatal("a second keeper on the same pipe name must fail")
	}

	var seen Caller
	var mu sync.Mutex
	s := NewServer(ServerOptions{Log: NewAuditWriter(&syncBuffer{})})
	s.Handle("test.whoami", classRead, 5*time.Second, func(_ context.Context, c *Call) (any, error) {
		mu.Lock()
		seen = c.Caller
		mu.Unlock()
		return c.Caller.SID, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()

	c := NewClient(PipeDialer{Name: name})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sid string
	if err := c.Call(context.Background(), "test.whoami", nil, &sid); err != nil {
		t.Fatal(err)
	}
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if want := u.User.Sid.String(); sid != want {
		t.Fatalf("caller SID %q, want %q", sid, want)
	}
	mu.Lock()
	if seen.PID != uint32(os.Getpid()) || seen.Name == "" {
		t.Fatalf("caller: %+v", seen)
	}
	mu.Unlock()

	// Many callers at once.
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Ping(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ping: %v", err)
		}
	}

	// This test process is not a service, so a dialer that insists on session 0 refuses it.
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err == nil && session != 0 {
		if err := NewClient(PipeDialer{Name: name, RequireSession0: true}).Ping(context.Background()); !errors.Is(err, ErrImpostor) {
			t.Fatalf("want ErrImpostor, got %v", err)
		}
	}

	// The pipe is owned by this test's own account, which a standard user also has: the production owner check refuses it, the same
	// check trusting this account accepts it.
	if err := NewClient(PipeDialer{Name: name, RequireKeeperOwner: true}).Ping(context.Background()); !errors.Is(err, ErrImpostor) {
		t.Fatalf("a pipe owned by a person's account: want ErrImpostor, got %v", err)
	}
	if err := NewClient(PipeDialer{Name: name, RequireKeeperOwner: true, extraOwners: []string{u.User.Sid.String()}}).Ping(context.Background()); err != nil {
		t.Fatalf("owner check trusting this account: %v", err)
	}
	if p := NewPipeDialer(name); !p.RequireSession0 || !p.RequireKeeperOwner {
		t.Fatalf("production dialer must check session and owner: %+v", p)
	}

	if err := NewClient(PipeDialer{Name: name + "-absent"}).Ping(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("absent pipe: %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if err := c.Ping(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("after stop: %v", err)
	}
}
