//go:build windows

package blackbox

import (
	"errors"
	"os"
	"testing"
	"time"
	"unsafe"
)

// rawVMAddStatus opens a PDH query and asks for the balloon counter exactly as the
// factory does, so the real status of a missing object is seen, not guessed.
func rawVMAddStatus(t *testing.T) error {
	t.Helper()
	if err := pdh.Load(); err != nil {
		t.Fatalf("pdh.dll: %v", err)
	}
	var q uintptr
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q))); r != 0 {
		t.Fatalf("PdhOpenQuery: 0x%x", r)
	}
	defer procPdhCloseQuery.Call(q)
	h, err := pdhAddEnglishCall(q, vmBalloonPath)
	if err == nil {
		pdhRemoveCall(h)
	}
	return err
}

// TestRealVMNotApplicableOnHostedRunner is the real-PDH proof of the "not
// applicable" path. The GitHub hosted Windows runner is an Azure VM without
// VMware Tools, so the VM Memory object must be absent there.
func TestRealVMNotApplicableOnHostedRunner(t *testing.T) {
	// RUNNER_ENVIRONMENT is "github-hosted" on GitHub's own runners; a self-hosted
	// runner also sets GITHUB_ACTIONS and could be a VMware guest.
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" {
		t.Skip("runs on the GitHub hosted runner only")
	}
	addErr := rawVMAddStatus(t)
	var se *pdhStatusErr
	if !errors.As(addErr, &se) {
		t.Fatalf("PdhAddEnglishCounter(%s) on the hosted runner: err = %v, want a PDH status error (the object is absent)", vmBalloonPath, addErr)
	}
	t.Logf("real PDH status for %s on the hosted runner: 0x%X", vmBalloonPath, se.code)
	if !pdhAbsentStatus(se.code) {
		t.Fatalf("real PDH status 0x%X for a missing VM Memory object is not in the absent set; add it to pdhAbsentStatus", se.code)
	}
	g, err := newVMGroup(GroupDeps{})
	if !errors.Is(err, errNotApplicable) || g != nil {
		t.Fatalf("newVMGroup on the hosted runner = (%v, %v), want (nil, errNotApplicable)", g, err)
	}
}

// TestRealVMOnThisPC logs what this PC does with the vm group: not applicable
// (no VMware Tools) or the balloon value. Opt-in.
func TestRealVMOnThisPC(t *testing.T) {
	if os.Getenv("UNRAVIS_REALTICK") == "" {
		t.Skip("set UNRAVIS_REALTICK=1")
	}
	t.Logf("raw add status: %v", rawVMAddStatus(t))
	g, err := newVMGroup(GroupDeps{Cores: 4})
	if err != nil {
		t.Logf("newVMGroup: %v (not applicable: %v)", err, err == errNotApplicable)
		return
	}
	defer g.(*vmGroup).Close()
	for i := 0; i < 3; i++ {
		v, err := g.Collect(time.Now(), 1)
		if p, ok := v.(*VMGroup); ok && p != nil && p.BalloonBytes != nil {
			t.Logf("tick %d: balloonBytes=%d err=%v", i, *p.BalloonBytes, err)
		} else {
			t.Logf("tick %d: no reading, err=%v", i, err)
		}
		time.Sleep(time.Second)
	}
}
