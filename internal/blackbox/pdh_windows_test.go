//go:build windows

package blackbox

import (
	"testing"
	"time"
)

// Pure bookkeeping: classification decides which entries RefreshInstances
// re-expands (wildcards) and which stay untouched (direct counters).
func TestIsWildcard(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`\Processor(_Total)\% Processor Time`, false},
		{`\Memory\Available Bytes`, false},
		{`\Process(*)\% Processor Time`, true},
		{`\Process(chrome*)\ID Process`, true},
	}
	counters := make([]pdhCounter, 0, len(cases))
	for _, c := range cases {
		counters = append(counters, pdhCounter{path: c.path, wildcard: isWildcard(c.path)})
	}
	for i, c := range counters {
		if c.wildcard != cases[i].want {
			t.Errorf("isWildcard(%q) = %v, want %v", c.path, c.wildcard, cases[i].want)
		}
	}
}

// A failed counter must yield OK=false, never Value=0 with OK=true.
func TestReadingFromFailedCounterNotOK(t *testing.T) {
	if r := readingFrom(`\X\Y`, pdhFmtCounterValue{CStatus: 0x800007D5, Double: 42}, 0); r.OK || r.Status == 0 {
		t.Errorf("bad CStatus: got %+v, want OK=false with non-zero Status", r)
	}
	if r := readingFrom(`\X\Y`, pdhFmtCounterValue{}, 0xC0000BB9); r.OK {
		t.Errorf("API failure: got %+v, want OK=false", r)
	}
	if r := readingFrom(`\X\Y`, pdhFmtCounterValue{Double: 3.5}, 0); !r.OK || r.Value != 3.5 || r.Status != 0 {
		t.Errorf("good reading: got %+v, want OK=true Value=3.5 Status=0", r)
	}
}

func TestNewPDHSourceBadPathReturnsError(t *testing.T) {
	src, err := NewPDHSource([]string{`\NoSuchObject(_Total)\NoSuchCounter`})
	if err == nil {
		src.Close()
		t.Fatal("expected error for an invalid counter path")
	}
	if src != nil {
		t.Fatalf("expected nil source on error, got %+v", src)
	}
	// The constructor closed the query on failure; Close stays safe afterwards.
	var p PDHSource
	if err := p.Close(); err != nil {
		t.Fatalf("Close on zero value: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestPDHSourceClosedRejectsWork(t *testing.T) {
	p := &PDHSource{closed: true}
	if _, err := p.Collect(); err == nil {
		t.Fatal("Collect after Close must fail")
	}
	if err := p.RefreshInstances(); err == nil {
		t.Fatal("RefreshInstances after Close must fail")
	}
}

func TestPDHSourcePaths(t *testing.T) {
	p := &PDHSource{counters: []pdhCounter{{path: `\A\B`}, {path: `\C\D`}}}
	got := p.Paths()
	if len(got) != 2 || got[0] != `\A\B` || got[1] != `\C\D` {
		t.Fatalf("Paths() = %v, want the expanded paths in order", got)
	}
}

// LIVE CHECK (owner runs on a real Windows server): two collects of the
// processor total must report CStatus 0 and a non-zero rate.
func TestPDHSourceLiveCollect(t *testing.T) {
	t.Skip("needs live PDH; owner runs on a real server")
	src, err := NewPDHSource([]string{`\Processor(_Total)\% Processor Time`})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.Collect(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	rs, err := src.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || !rs[0].OK || rs[0].Status != 0 {
		t.Fatalf("readings = %+v, want one OK reading with CStatus 0", rs)
	}
	if rs[0].Value <= 0 {
		t.Fatalf("expected a non-zero processor rate, got %v", rs[0].Value)
	}
}

// LIVE CHECK: wildcard expansion and RefreshInstances on real processes.
func TestPDHSourceLiveWildcardRefresh(t *testing.T) {
	t.Skip("needs live PDH; owner runs on a real server")
	src, err := NewPDHSource([]string{`\Process(*)\ID Process`})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if len(src.Paths()) == 0 {
		t.Fatal("wildcard expanded to no instances")
	}
	if err := src.RefreshInstances(); err != nil {
		t.Fatal(err)
	}
	if len(src.Paths()) == 0 {
		t.Fatal("RefreshInstances lost all instances")
	}
}