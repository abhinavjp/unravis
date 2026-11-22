//go:build windows

package blackbox

import (
	"errors"
	"testing"
)

// installAddStatus makes every plain add answer from status: a path missing from
// the map is added, a path with a code fails with that PDH status. It rides on
// the fake rig so no real counter is touched.
func installAddStatus(t *testing.T, status map[string]uint32) {
	t.Helper()
	installFakePDH(t, nil)
	add := pdhAddEnglishCall
	pdhAddEnglishCall = func(query uintptr, path string) (uintptr, error) {
		if code, bad := status[path]; bad {
			return 0, &pdhStatusErr{call: "PdhAddEnglishCounter", path: path, code: code}
		}
		return add(query, path)
	}
}

func TestPDHTolerantAllAbsentIsErrPDHAbsent(t *testing.T) {
	a, b := `\VM Memory\A`, `\VM Memory\B`

	installAddStatus(t, map[string]uint32{a: 0xC0000BB8, b: 0xC0000BB9})
	src, err := NewPDHSourceTolerant([]string{a, b})
	if err == nil {
		src.Close()
		t.Fatal("all adds absent: want an error")
	}
	if !errors.Is(err, errPDHNoCounters) || !errors.Is(err, errPDHAbsent) {
		t.Fatalf("all absent: err = %v, want both errPDHNoCounters and errPDHAbsent", err)
	}

	// A mixed failure (one absent, one other status) is not "absent".
	installAddStatus(t, map[string]uint32{a: 0xC0000BB8, b: 0xC0000BBC})
	src, err = NewPDHSourceTolerant([]string{a, b})
	if err == nil {
		src.Close()
		t.Fatal("mixed failure: want an error")
	}
	if !errors.Is(err, errPDHNoCounters) || errors.Is(err, errPDHAbsent) {
		t.Fatalf("mixed: err = %v, want errPDHNoCounters only", err)
	}

	// One counter added is a success even when the other is absent.
	installAddStatus(t, map[string]uint32{a: 0xC0000BB8})
	src, err = NewPDHSourceTolerant([]string{a, b})
	if err != nil {
		t.Fatalf("one counter added: err = %v, want success", err)
	}
	if got := src.Paths(); len(got) != 1 || got[0] != b {
		t.Errorf("Paths = %v, want only %q", got, b)
	}
	src.Close()
}
