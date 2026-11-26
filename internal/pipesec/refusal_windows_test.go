//go:build windows

package pipesec

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/pipesec/pipesectest"
)

const (
	floodEnv      = "UNRAVIS_PIPESEC_REFUSAL_FLOOD"
	floodConnects = 1000 // refused connects per flooding process
	floodProcs    = 4
)

// TestRefusalFloodHelper is the client side of TestRefusedPeerFloodStillServesLegitClient. Run by itself it does nothing; the parent test
// starts copies of the test program from another folder (so the peer policy refuses them) with floodEnv set to the pipe name.
func TestRefusalFloodHelper(t *testing.T) {
	name := os.Getenv(floodEnv)
	if name == "" {
		t.Skip("helper of TestRefusedPeerFloodStillServesLegitClient")
	}
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second) // winok: a give-up limit for a helper process, not a proof of ordering
	for n := 0; n < floodConnects; {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d connects went through", n, floodConnects)
		}
		h, err := windows.CreateFile(namep, clientAccess, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil { // every instance is busy for the moment
			time.Sleep(time.Millisecond) // winok: a pause between retries, nothing waits on its length
			continue
		}
		_ = windows.CloseHandle(h)
		n++
	}
}

// TestRefusedPeerFloodStillServesLegitClient (RV-SEC-6): four processes the policy refuses connect 1,000 times each, in a loop; a peer
// the policy allows must still be served, each round trip inside one second, and the refusals are counted. The review item asked
// whether a refused peer can keep the listener from serving a legitimate one; on the listener as it is, it cannot, so this test guards that.
func TestRefusedPeerFloodStillServesLegitClient(t *testing.T) {
	name := pipesectest.Name(t)
	l := listen(t, name, selfPolicy(t), 0)
	go echo(l)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe) //nolint:gosec // G304: the running test program
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "refused.test.exe")
	if err := os.WriteFile(other, data, 0o700); err != nil { //nolint:gosec // G306: a runnable copy of the test program
		t.Fatal(err)
	}

	before := Metrics()
	done := make(chan error, floodProcs)
	var outs [floodProcs]bytes.Buffer
	for i := range floodProcs {
		cmd := exec.Command(other, "-test.run=^TestRefusalFloodHelper$", "-test.count=1") //nolint:gosec // G204: the copy made above
		cmd.Env = append(os.Environ(), floodEnv+"="+name)
		cmd.Stdout, cmd.Stderr = &outs[i], &outs[i]
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW} // never a console window (THREAD-GATE rule 9)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cmd.Process.Kill() }()
		go func() { done <- cmd.Wait() }()
	}

	var worst time.Duration
	rounds, finished := 0, 0
	limit := time.After(90 * time.Second) // winok: a give-up limit for the flood, not a proof of ordering
	for finished < floodProcs || rounds < 3 {
		began := time.Now()
		roundTrip(t, name, "hello")
		if d := time.Since(began); d > worst {
			worst = d
		}
		rounds++
		select {
		case err := <-done:
			finished++
			if err != nil {
				t.Fatalf("a flood helper failed: %v", err)
			}
		case <-limit:
			t.Fatal("the flood did not finish in 90 s")
		default:
		}
	}
	got := Metrics() - before
	t.Logf("%d legitimate round trips during the flood, slowest %v, refusals counted %d", rounds, worst, got)
	if worst > time.Second {
		t.Fatalf("a legitimate round trip took %v during the flood", worst)
	}
	if got < floodProcs*floodConnects/2 {
		t.Fatalf("only %d refusals were counted for %d refused connects", got, floodProcs*floodConnects)
	}
}
