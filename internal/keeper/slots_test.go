package keeper

import (
	"context"
	"testing"
	"time"
)

// A connection's slot is freed by the server only after it has finished with the connection (it drains what the client sent), which
// can be after the client has read the answer. A test that opens connections back to back and is not about the connection cap would
// then sometimes meet "busy" on a slow machine, for a reason that has nothing to do with what it checks. These helpers make such a
// test wait for the slot instead of guessing a delay. Tests about the cap itself (MaxPerSession, MaxConns) dial with the plain
// listener dialer and keep asserting busy.

// slotsOpen reports how many connections the server counts as open for the Windows session, and whether a new one would be taken.
func (s *Server) slotsOpen(session uint32) (open int, free bool) {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	open = s.open[session]
	return open, open < s.maxPer && len(s.sem) < cap(s.sem)
}

// waitOpenAtMost waits until the server counts at most n open connections for the session: the ones a test holds on purpose, with
// every earlier connection already released. It fails after a generous cap.
func (s *Server) waitOpenAtMost(t testing.TB, session uint32, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		open, _ := s.slotsOpen(session)
		if open <= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the keeper still counts %d open connections for session %d, want at most %d", open, session, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// slotDialer dials only when the server has a free slot for the caller's session, so a sequence of connections is never refused busy
// because an earlier one is still being released. Connections made concurrently are not covered: that is what the cap tests are for.
type slotDialer struct {
	s      *Server
	caller Caller
	inner  Dialer
}

func (s *Server) slotDialer(l *MemoryListener, caller Caller) Dialer {
	return slotDialer{s: s, caller: caller, inner: l.Dialer(caller)}
}

func (d slotDialer) Dial(ctx context.Context) (ClientConn, error) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if _, free := d.s.slotsOpen(d.caller.Session); free {
			break
		}
		time.Sleep(time.Millisecond)
	}
	return d.inner.Dial(ctx)
}
