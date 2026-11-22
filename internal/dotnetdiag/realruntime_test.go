package dotnetdiag

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestRealRuntimeProbe runs the probe against a REAL running .NET process.
// It is opt-in: it does nothing unless UNRAVIS_DIAG_PID names a process.
//
//	UNRAVIS_DIAG_PID=<pid> go test -run TestRealRuntimeProbe -v ./internal/dotnetdiag
//
// It starts one System.Runtime counters session in that process for
// UNRAVIS_DIAG_SECONDS (default 8) and stops it. Nothing is written to disk
// and no stream bytes are printed: only the fixed Result fields.
func TestRealRuntimeProbe(t *testing.T) {
	v := os.Getenv("UNRAVIS_DIAG_PID")
	if v == "" {
		t.Skip("set UNRAVIS_DIAG_PID to a running .NET process to run this")
	}
	pid, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		t.Fatalf("UNRAVIS_DIAG_PID: %v", err)
	}
	o := DefaultOptions()
	o.Duration = 8 * time.Second
	if s := os.Getenv("UNRAVIS_DIAG_SECONDS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 60 {
			t.Fatalf("UNRAVIS_DIAG_SECONDS must be 1..60")
		}
		o.Duration = time.Duration(n) * time.Second
	}
	res, perr := Probe(context.Background(), DefaultDialer(), Target{PID: uint32(pid)}, o)
	t.Logf("result: %+v", res)
	t.Logf("sentence: %s", Sentence(res.Reason))
	if perr != nil {
		t.Fatalf("probe failed: %v", perr)
	}
	if !res.PipeOpened || res.ServerPID != uint32(pid) || !res.PreambleOK || !res.StopAck || !res.StopSession {
		t.Fatalf("probe did not complete cleanly: %+v", res)
	}
	if res.BytesBefore == 0 {
		t.Fatal("no stream bytes in the counters session")
	}
}
