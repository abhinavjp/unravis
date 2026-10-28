package keeper

import (
	"testing"
	"time"
)

// The defaults the docs and comments state, pinned in one place (D-C-05). A change to any of them must change this table on purpose.
func TestKeeperDefaultsMatchTheDocumentedNumbers(t *testing.T) {
	ints := []struct {
		name string
		got  int64
		want int64
	}{
		{"audit log rotates at (DefaultAuditMaxBytes)", DefaultAuditMaxBytes, 4 << 20},
		{"largest audit answer (MaxAuditAnswer)", MaxAuditAnswer, 64 << 20},
		{"audit answer cap variable (auditAnswerCap)", int64(auditAnswerCap), 64 << 20},
		{"seats in all (DefaultMaxSeats)", DefaultMaxSeats, 16},
		{"seats per person (DefaultSeatsPerPerson)", DefaultSeatsPerPerson, 4},
		{"audit connections (defaultAuditConns)", defaultAuditConns, 6},
		{"audit runs at once (defaultAuditRuns)", defaultAuditRuns, 4},
		{"rate read", int64(DefaultLimits[classRead]), 600},
		{"rate write", int64(DefaultLimits[classWrite]), 60},
		{"rate reach", int64(DefaultLimits[classReach]), 30},
		{"rate blob", int64(DefaultLimits[classBlob]), 3000},
		{"rate audit", int64(DefaultLimits[classAudit]), 600},
		{"rate bad", int64(DefaultLimits[classBad]), 30},
		{"number of rate classes", int64(len(DefaultLimits)), 6},
	}
	for _, c := range ints {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	durations := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"rate window (DefaultWindow)", DefaultWindow, time.Minute},
		{"audit read cap (auditReadCap)", auditReadCap, 90 * time.Second},
		{"audit query timeout (auditQueryTimeout)", auditQueryTimeout, 90 * time.Second},
		{"wait for an audit slot (defaultAuditWait)", defaultAuditWait, 20 * time.Second},
	}
	for _, c := range durations {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}
}

// NewServer fills the pool numbers that the options leave out.
func TestNewServerDefaultsAreTheDocumentedPool(t *testing.T) {
	s := NewServer(ServerOptions{})
	if cap(s.sem) != 16 {
		t.Errorf("connections = %d, want 16", cap(s.sem))
	}
	if s.maxPer != 8 {
		t.Errorf("connections per Windows session = %d, want 8", s.maxPer)
	}
	if s.readTO != 5*time.Second {
		t.Errorf("read timeout = %s, want 5s", s.readTO)
	}
	if cap(s.auditConns) != 6 || cap(s.auditRuns) != 4 || s.auditWait != 20*time.Second {
		t.Errorf("audit budget = %d connections, %d runs, %s wait; want 6, 4, 20s", cap(s.auditConns), cap(s.auditRuns), s.auditWait)
	}
}
