package blackbox

import (
	"errors"
	"fmt"
	"testing"
)

func TestPDHAbsentStatus(t *testing.T) {
	for _, c := range []uint32{0xC0000BB8, 0xC0000BB9, 0xC0000BBF, 0xC0000BC0} {
		if !pdhAbsentStatus(c) {
			t.Errorf("0x%X: want absent", c)
		}
	}
	for _, c := range []uint32{0xC0000BBC, 0x800007D2, 0, 0xFFFFFFFF} {
		if pdhAbsentStatus(c) {
			t.Errorf("0x%X: want not absent", c)
		}
	}
}

func TestPDHStatusErrText(t *testing.T) {
	e := &pdhStatusErr{call: "PdhAddEnglishCounter", path: `\VM Memory\X`, code: 0xC0000BB8}
	old := fmt.Errorf("PdhAddEnglishCounter %q: 0x%x", `\VM Memory\X`, uintptr(0xC0000BB8))
	if e.Error() != old.Error() {
		t.Fatalf("text = %q, want the old text %q", e.Error(), old.Error())
	}
	e2 := &pdhStatusErr{call: "PdhAddCounter", path: `\A\B`, code: 0xC0000BBC}
	if want := fmt.Sprintf("PdhAddCounter %q: 0x%x", `\A\B`, uintptr(0xC0000BBC)); e2.Error() != want {
		t.Fatalf("text = %q, want %q", e2.Error(), want)
	}
	// Is works directly and through a wrap with two %w verbs.
	if !errors.Is(e, errPDHAbsent) {
		t.Fatal("absent status: errors.Is(err, errPDHAbsent) = false")
	}
	if errors.Is(e2, errPDHAbsent) {
		t.Fatal("non-absent status: errors.Is(err, errPDHAbsent) = true")
	}
	other := errors.New("no counters")
	wrapped := fmt.Errorf("%w: %w", other, e)
	if !errors.Is(wrapped, errPDHAbsent) || !errors.Is(wrapped, other) {
		t.Fatal("two-%w wrap lost one of its causes")
	}
	// A pdhStatusErr is not equal to an unrelated target.
	if errors.Is(e, other) {
		t.Fatal("errors.Is matched an unrelated error")
	}
}
