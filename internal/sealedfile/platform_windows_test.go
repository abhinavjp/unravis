//go:build windows

package sealedfile

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalMachineRoundTripAndScopesDiffer(t *testing.T) {
	machine, err := LocalMachine()
	if err != nil {
		t.Fatal(err)
	}
	user, err := CurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("shared folder document")
	sealed, err := machine.Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("machine-sealed data holds the plain text")
	}
	got, err := machine.Unprotect(sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("machine round trip = %q, %v", got, err)
	}
	userSealed, err := user.Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := user.Unprotect(userSealed); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("user round trip = %q, %v", got, err)
	}
}

// Inside the shared data folder the lock is a machine-wide (Global) mutex any signed-in account may wait on, so two people signed in at
// once serialise on the same shared file; a person's own files keep the per-session lock.
func TestSharedFolderLocksAreMachineWide(t *testing.T) {
	root := t.TempDir()
	SetSharedRoot(root)
	t.Cleanup(func() { SetSharedRoot("") })
	inside := filepath.Join(root, "cases", "hosts.dat")
	if name, global := mutexName(inside); !global || !strings.HasPrefix(name, `Global\Unravis-`) {
		t.Fatalf("shared path lock = %s", name)
	}
	if upper, _ := mutexName(strings.ToUpper(inside)); upper != func() string { n, _ := mutexName(inside); return n }() {
		t.Fatal("the same shared file under another letter case must take the same lock")
	}
	if name, global := mutexName(filepath.Join(filepath.Dir(root), "elsewhere", "hosts.dat")); global || !strings.HasPrefix(name, `Local\`) {
		t.Fatalf("own file lock = %s", name)
	}
	if _, global := mutexName(root + "-sibling"); global {
		t.Fatal("a sibling folder that merely starts with the same name is not shared")
	}

	unlock, err := Lock(inside)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		start := time.Now()
		release, err := Lock(inside)
		if err == nil {
			release()
			if time.Since(start) < 150*time.Millisecond {
				err = errors.New("the second holder did not wait")
			}
		}
		got <- err
	}()
	time.Sleep(300 * time.Millisecond)
	unlock()
	if err := <-got; err != nil {
		t.Fatal(err)
	}
}

// LockWait outlasts the 5 s wait of Lock: a holder (an import) that keeps the
// folder longer does not make it give up, and a context that ends first ends it.
func TestLockWaitOutlastsFiveSeconds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case")
	held, stop := make(chan struct{}), make(chan struct{})
	go func() {
		unlock, err := Lock(path)
		if err != nil {
			close(held)
			return
		}
		close(held)
		<-stop
		unlock() // the goroutine that took the lock releases it (the lock belongs to its OS thread)
	}()
	<-held
	if _, err := Lock(path); err == nil {
		t.Fatal("Lock must give up after 5 s while another thread holds it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	if _, err := LockWait(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("LockWait with a short context = %v", err)
	}
	cancel()
	go func() { time.Sleep(6 * time.Second); close(stop) }()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	release, err := LockWait(ctx, path)
	if err != nil {
		t.Fatalf("LockWait = %v", err)
	}
	release()
	if time.Since(start) < 5*time.Second {
		t.Fatalf("LockWait returned after %v, before the holder let go", time.Since(start))
	}
}
