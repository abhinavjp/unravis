package investigate

import (
	"testing"
	"time"
)

// The engine words and the app write a moment the same way: "28 Oct 2026 02:56 UTC", never "2026-10-28 02:56 UTC".
func TestFmtTMatchesTheAppDateFormat(t *testing.T) {
	at := time.Date(2026, 10, 8, 2, 56, 0, 0, time.FixedZone("x", 3600))
	if got, want := fmtT(at), "8 Oct 2026 01:56 UTC"; got != want {
		t.Fatalf("fmtT = %q, want %q", got, want)
	}
}
