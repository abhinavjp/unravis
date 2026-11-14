//go:build windows

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"

	"api-audit-explorer/internal/blackbox"
)

// The SQL service loop accepts Stop and Shutdown, ends the run cleanly and
// never starts the host recorder: no HTTP server in sql-service mode.
func TestSQLServiceHandlerStopsCleanly(t *testing.T) {
	g := newSQLRig(t)
	blocked := make(chan struct{})
	g.d.sqlRun = func(ctx context.Context, o sqlRunOptions) error {
		if o.Dir != blackbox.SQLDataDir(filepath.Join(g.dir, "rec")) {
			t.Errorf("runner dir %q", o.Dir)
		}
		close(blocked)
		<-ctx.Done()
		return nil
	}
	h := &sqlHandler{cfgPath: filepath.Join(g.env.prog, defaultConfigName), d: g.d}
	req := make(chan svc.ChangeRequest)
	status := make(chan svc.Status)
	type result struct {
		ok   bool
		code uint32
	}
	done := make(chan result, 1)
	go func() { ok, code := h.Execute(nil, req, status); done <- result{ok, code} }()
	waitState := func(want svc.State) svc.Status {
		t.Helper()
		select {
		case st := <-status:
			if st.State != want {
				t.Fatalf("state %v, want %v", st.State, want)
			}
			return st
		case <-time.After(5 * time.Second):
			t.Fatalf("no state %v arrived", want)
			return svc.Status{}
		}
	}
	waitState(svc.StartPending)
	st := waitState(svc.Running)
	if st.Accepts&svc.AcceptStop == 0 || st.Accepts&svc.AcceptShutdown == 0 {
		t.Fatalf("the SQL service must accept Stop and Shutdown: %v", st.Accepts)
	}
	<-blocked
	req <- svc.ChangeRequest{Cmd: svc.Stop}
	waitState(svc.StopPending)
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("the service exited with %d after a clean Stop", r.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not stop")
	}
	if g.hostRuns != 0 {
		t.Fatal("the host recorder (and its HTTP server) ran in sql-service mode")
	}
	if len(g.elev.calls) != 0 {
		t.Fatalf("the service used the elevate helper: %v", g.elev.calls)
	}
}