package evidence

import "testing"

// The settling observation follows the notes' export policy: it is not exported.
func TestExportOmitsSettlingObservation(t *testing.T) {
	in := baseInput()
	in.Options = Options{IncludeLedger: true}
	in.Ledger[0].SettlingObservation = "Zebrafish check: if the nightly job is paused the stall stays."
	e, raw := mustBuild(t, in)
	if len(e.Body.Ledger) != 1 {
		t.Fatalf("ledger = %+v", e.Body.Ledger)
	}
	assertAbsent(t, raw, "zebrafish", "settling", "nightly job")
}
