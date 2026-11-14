//go:build windows

package blackbox

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Reuses the lazy pdh.dll procs, pdhFmtDouble and pdhFmtCounterValue declared
// in extras_windows.go (same package); only the wildcard procs are new.
var (
	procPdhExpandWildCard = pdh.NewProc("PdhExpandWildCardPathW")
	procPdhRemoveCounter  = pdh.NewProc("PdhRemoveCounter")
	procPdhEnumObjects    = pdh.NewProc("PdhEnumObjectsW")
)

const pdhMoreData = 0x800007D2 // PDH_MORE_DATA: size call, buffer needed

// pdhCounter is one added counter; wildcard marks entries RefreshInstances
// re-expands when the process set changes — never per tick (BL-122).
type pdhCounter struct {
	path     string
	handle   uintptr
	wildcard bool
}

// PDHSource implements CounterSource over PDH: one query handle reused for the
// source lifetime, English counters added once. No goroutines, no tickers, no
// process attach — PDH only reads counters.
type PDHSource struct {
	query     uintptr
	counters  []pdhCounter
	wildcards []string // original patterns, kept for RefreshInstances
	closed    bool
}

var _ CounterSource = (*PDHSource)(nil)

// isWildcard reports whether a path needs instance expansion ("*" or "(*)").
func isWildcard(path string) bool { return strings.Contains(path, "*") }

// NewPDHSource opens one query, adds every path (wildcards expanded once) and
// primes with a single collect. Any open/add failure closes the query.
func NewPDHSource(paths []string) (*PDHSource, error) {
	p := &PDHSource{}
	if err := pdh.Load(); err != nil {
		return nil, err
	}
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&p.query))); r != 0 {
		return nil, fmt.Errorf("PdhOpenQuery: 0x%x", r)
	}
	for _, path := range paths {
		expanded, wild := []string{path}, isWildcard(path)
		if wild {
			p.wildcards = append(p.wildcards, path)
			var err error
			if expanded, err = expandWildCard(path); err != nil {
				p.Close()
				return nil, err
			}
		}
		for _, ep := range expanded {
			if err := p.addCounter(ep, wild); err != nil {
				p.Close()
				return nil, err
			}
		}
	}
	procPdhCollectQueryData.Call(p.query) // one priming collect
	return p, nil
}

// addCounter adds one English counter to the existing query (never localized).
func (p *PDHSource) addCounter(path string, wildcard bool) error {
	cp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var h uintptr
	r, _, _ := procPdhAddEnglishCounter.Call(p.query, uintptr(unsafe.Pointer(cp)), 0, uintptr(unsafe.Pointer(&h)))
	if r != 0 {
		return fmt.Errorf("PdhAddEnglishCounter %q: 0x%x", path, r)
	}
	p.counters = append(p.counters, pdhCounter{path: path, handle: h, wildcard: wildcard})
	return nil
}

// expandWildCard expands one wildcard path into concrete instance paths.
func expandWildCard(path string) ([]string, error) {
	cp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var n uint32
	r, _, _ := procPdhExpandWildCard.Call(0, uintptr(unsafe.Pointer(cp)), 0, uintptr(unsafe.Pointer(&n)), 0)
	if r != 0 && r != pdhMoreData {
		return nil, fmt.Errorf("PdhExpandWildCardPath %q: 0x%x", path, r)
	}
	if n == 0 {
		return nil, fmt.Errorf("PdhExpandWildCardPath %q: no instances", path)
	}
	buf := make([]uint16, n)
	r, _, _ = procPdhExpandWildCard.Call(0, uintptr(unsafe.Pointer(cp)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0)
	if r != 0 {
		return nil, fmt.Errorf("PdhExpandWildCardPath %q: 0x%x", path, r)
	}
	var out []string // multi-string: NUL-separated paths, double NUL at the end
	for start, i := 0, 0; i < int(n); i++ {
		if buf[i] == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("PdhExpandWildCardPath %q: no instances", path)
	}
	return out, nil
}

// Collect takes one query sample and formats every counter. A failed counter
// yields OK=false (missing, never 0); it does not abort the whole collect.
func (p *PDHSource) Collect() ([]CounterReading, error) {
	if p.closed {
		return nil, errors.New("PDH query closed")
	}
	if r, _, _ := procPdhCollectQueryData.Call(p.query); r != 0 {
		return nil, fmt.Errorf("PdhCollectQueryData: 0x%x", r)
	}
	out := make([]CounterReading, 0, len(p.counters))
	for _, c := range p.counters {
		var v pdhFmtCounterValue
		r, _, _ := procPdhGetFormattedCtrValue.Call(c.handle, pdhFmtDouble, 0, uintptr(unsafe.Pointer(&v)))
		out = append(out, readingFrom(c.path, v, r))
	}
	return out, nil
}

// readingFrom maps one formatted result; OK only when both the API call and
// the per-counter CStatus report success.
func readingFrom(path string, v pdhFmtCounterValue, apiStatus uintptr) CounterReading {
	if apiStatus != 0 {
		return CounterReading{Path: path, Status: uint32(apiStatus)}
	}
	return CounterReading{Path: path, Value: v.Double, Status: v.CStatus, OK: v.CStatus == 0}
}

// RefreshInstances re-expands ONLY wildcard counters on the same query handle.
// The resolver calls it when the process set changes, never every tick.
func (p *PDHSource) RefreshInstances() error {
	if p.closed {
		return errors.New("PDH query closed")
	}
	kept := make([]pdhCounter, 0, len(p.counters))
	for _, c := range p.counters {
		if c.wildcard {
			procPdhRemoveCounter.Call(c.handle)
			continue
		}
		kept = append(kept, c) // direct counters keep their handles
	}
	p.counters = kept
	for _, pattern := range p.wildcards {
		expanded, err := expandWildCard(pattern)
		if err != nil {
			return err
		}
		for _, ep := range expanded {
			if err := p.addCounter(ep, true); err != nil {
				return err
			}
		}
	}
	procPdhCollectQueryData.Call(p.query) // prime the re-added counters once
	return nil
}

// Close closes the query once; idempotent and safe on a zero value.
func (p *PDHSource) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if p.query != 0 {
		procPdhCloseQuery.Call(p.query)
	}
	return nil
}

// Paths returns the current expanded paths so groups can build engine specs.
func (p *PDHSource) Paths() []string {
	out := make([]string, len(p.counters))
	for i, c := range p.counters {
		out[i] = c.path
	}
	return out
}