package server

import (
	"os"
	"strings"
	"testing"
)

// SEC-07 (third handoff): postDiscoverImport starts its background batch with
// "go h.runDiscoverBatch". That start must be counted in the wait group only
// through beginRun, which does the closed-check and the Add together under j.mu.
// A bare wg.Add(1) outside the lock lets shutdown set closed and return from
// wg.Wait between the check and the Add (Add-after-Wait).
//
// Harness limit: the handoff is not reachable deterministically from a handler
// test. After shutdown, create() already refuses with 429 (import-busy), and
// between the last create() and the handoff there is no call a test could hook to
// land shutdown in that gap. So the guard is proven here by reading the handoff
// site (structural), and the refusal/counting behaviour of beginRun itself is
// proven by TestBeginRunRefusesAfterShutdown and TestBeginRunAddIsCounted.

// discoverHandoffSource returns the text of postDiscoverImport's tail: from the
// last job creation to the goroutine start, with CRLF normalised.
func discoverHandoffSource(t *testing.T) (before string, whole string) {
	t.Helper()
	raw, err := os.ReadFile("discover_import.go")
	if err != nil {
		t.Fatalf("read discover_import.go: %v", err)
	}
	whole = strings.ReplaceAll(string(raw), "\r\n", "\n")
	const start = "go h.runDiscoverBatch("
	at := strings.Index(whole, start)
	if at < 0 {
		t.Fatalf("handoff %q not found in discover_import.go", start)
	}
	if strings.Count(whole, start) != 1 {
		t.Fatalf("expected exactly one %q", start)
	}
	return whole[:at], whole
}

func TestDiscoverHandoffUsesBeginRunBeforeTheGoroutine(t *testing.T) {
	before, whole := discoverHandoffSource(t)

	// No bare wg.Add anywhere in the discover handoff file: the only legal Add is
	// the one inside beginRun (import_jobs.go).
	if strings.Contains(whole, "wg.Add(") {
		t.Fatal("discover_import.go has a bare wg.Add: use h.jobs.beginRun() so the closed-check and the Add share j.mu")
	}

	// The goroutine start is guarded by a refusing beginRun, directly before it.
	const guard = "if !h.jobs.beginRun() {"
	gi := strings.LastIndex(before, guard)
	if gi < 0 {
		t.Fatal("no `if !h.jobs.beginRun()` guard before the runDiscoverBatch goroutine")
	}
	// Nothing but the refusal block may sit between the guard and the goroutine,
	// and that block must give back everything the handoff holds.
	block := before[gi:]
	for _, want := range []string{
		"h.jobs.discard(items[j].job)", // every created job
		"slot()",                       // the limiter slot
		"release()",                    // the case gate
		"http.StatusServiceUnavailable",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("beginRun refusal block is missing %q", want)
		}
	}
	// The guard must come after the job-creation loop (it discards all of them).
	if ci := strings.LastIndex(before, "h.jobs.create(id)"); ci < 0 || ci > gi {
		t.Error("beginRun guard must sit after the job-creation loop")
	}
}
