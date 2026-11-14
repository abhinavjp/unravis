//go:build windows

package blackbox

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Reuses the lazy pdh.dll procs, pdhFmtDouble/pdhFmtNoCap100 and
// pdhFmtCounterValue declared in extras_windows.go (same package); only the
// wildcard/localization procs are new.
var (
	procPdhExpandWildCard = pdh.NewProc("PdhExpandWildCardPathW")
	procPdhRemoveCounter  = pdh.NewProc("PdhRemoveCounter")
	procPdhAddCounter     = pdh.NewProc("PdhAddCounterW")
	procPdhGetCounterInfo = pdh.NewProc("PdhGetCounterInfoW")
)

const pdhMoreData = 0x800007D2 // PDH_MORE_DATA: size call, buffer needed

// Bounds for provider-driven work (W2-04). A large supported host runs
// hundreds of processes; proc10+procio together add ~11 counters per process,
// so 500 processes mean ~5500 counters. The ceilings leave generous headroom
// for such a host while keeping one source's native state far inside the
// 50 MiB footprint budget, and they reject a broken or hostile provider
// BEFORE allocating or adding handles.
const (
	// pdhMaxExpandChars caps one expansion buffer in UTF-16 code units
	// (512Ki chars = 1 MiB). A Process instance path is ~60 chars, so this
	// covers ~8000 instances per pattern, an order of magnitude above real
	// hosts.
	pdhMaxExpandChars = 512 * 1024
	// pdhMaxInstancesPerPattern caps the handles retained from one pattern.
	// 4096 covers thousands of processes with margin.
	pdhMaxInstancesPerPattern = 4096
	// pdhMaxCountersPerSource caps the whole query (direct + expanded).
	// 8192 covers ~700 processes x 11 counters with margin.
	pdhMaxCountersPerSource = 8192
	// pdhMaxCounterInfoBytes caps the PdhGetCounterInfoW buffer; a counter
	// info with its full path is a few KiB at most.
	pdhMaxCounterInfoBytes = 64 * 1024
)

// Fixed over-bound errors: callers report unavailability, never partial data.
var (
	errPDHExpandTooLarge      = errors.New("PDH wildcard expansion buffer exceeds the size limit")
	errPDHTooManyInstances    = errors.New("PDH wildcard expansion exceeds the instance limit")
	errPDHTooManyCounters     = errors.New("PDH source exceeds the counter limit")
	errPDHCounterInfoTooLarge = errors.New("PDH counter info exceeds the size limit")
)

// Native-call seams. Every default wires the real pdh.dll proc; tests replace
// them with fakes to observe format flags, provider sizes and call order
// without a live provider. Production code never reassigns them.
var (
	pdhFormatCall = func(handle, format uintptr, out *pdhFmtCounterValue) uintptr {
		r, _, _ := procPdhGetFormattedCtrValue.Call(handle, format, 0, uintptr(unsafe.Pointer(out)))
		return r
	}
	pdhCollectCall = func(query uintptr) uintptr {
		r, _, _ := procPdhCollectQueryData.Call(query)
		return r
	}
	pdhRemoveCall = func(handle uintptr) {
		procPdhRemoveCounter.Call(handle)
	}
	pdhAddEnglishCall = func(query uintptr, path string) (uintptr, error) {
		cp, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return 0, err
		}
		var h uintptr
		if r, _, _ := procPdhAddEnglishCounter.Call(query, uintptr(unsafe.Pointer(cp)), 0, uintptr(unsafe.Pointer(&h))); r != 0 {
			return 0, fmt.Errorf("PdhAddEnglishCounter %q: 0x%x", path, r)
		}
		return h, nil
	}
	pdhAddCounterCall = func(query uintptr, path string) (uintptr, error) {
		cp, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return 0, err
		}
		var h uintptr
		if r, _, _ := procPdhAddCounter.Call(query, uintptr(unsafe.Pointer(cp)), 0, uintptr(unsafe.Pointer(&h))); r != 0 {
			return 0, fmt.Errorf("PdhAddCounter %q: 0x%x", path, r)
		}
		return h, nil
	}
	// pdhGetInfoCall is one PdhGetCounterInfoW invocation; buf == nil is the
	// size-discovery call. Explain text is not retrieved (FALSE).
	pdhGetInfoCall = func(handle uintptr, buf *byte, size *uint32) uintptr {
		r, _, _ := procPdhGetCounterInfo.Call(handle, 0, uintptr(unsafe.Pointer(size)), uintptr(unsafe.Pointer(buf)))
		return r
	}
	// pdhExpandCall is one PdhExpandWildCardPathW invocation; buf == nil is
	// the size-discovery call.
	pdhExpandCall = func(pattern *uint16, buf *uint16, size *uint32) uintptr {
		r, _, _ := procPdhExpandWildCard.Call(0, uintptr(unsafe.Pointer(pattern)), uintptr(unsafe.Pointer(buf)), uintptr(unsafe.Pointer(size)), 0)
		return r
	}
)

// pdhCounterPathElements mirrors PDH_COUNTER_PATH_ELEMENTS_W; it exists only
// to size the union inside pdhCounterInfo. Go's natural alignment reproduces
// the C layout on both 386 and amd64.
type pdhCounterPathElements struct {
	szMachineName    uintptr
	szObjectName     uintptr
	szInstanceName   uintptr
	szParentInstance uintptr
	dwInstanceIndex  uint32
	szCounterName    uintptr
}

// pdhDataItemPathElements mirrors PDH_DATA_ITEM_PATH_ELEMENTS_W (the other
// union member; larger than CounterPath on 386, smaller on amd64).
type pdhDataItemPathElements struct {
	szMachineName  uintptr
	objectGUID     [16]byte
	dwItemID       uint32
	szInstanceName uintptr
}

const pdhCounterInfoUnionSize = max(unsafe.Sizeof(pdhCounterPathElements{}), unsafe.Sizeof(pdhDataItemPathElements{}))

// pdhCounterInfo mirrors PDH_COUNTER_INFO_W (pdh.h, SDK 10.0.22621): six
// DWORDs, two DWORD_PTRs, szFullPath, the path-elements union (opaque here,
// sized to its largest member so every offset holds on 386 and amd64),
// szExplainText and the trailing DataBuffer[1]. Only szFullPath is read.
type pdhCounterInfo struct {
	dwLength        uint32
	dwType          uint32
	cVersion        uint32
	cStatus         uint32
	lScale          int32
	lDefaultScale   int32
	dwUserData      uintptr
	dwQueryUserData uintptr
	szFullPath      uintptr
	union           [pdhCounterInfoUnionSize]byte
	szExplainText   uintptr
	dataBuffer      uint32
}

// pdhCounter is one added counter; wildcard marks entries RefreshInstances
// re-expands when the process set changes — never per tick (BL-122). path is
// always the ENGLISH logical identity, even when the native handle was added
// from a localized path (W2-03): groups parse English prefixes and engine
// specs key on English paths.
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

// NewPDHSource opens one query, adds every path (wildcards expanded once via
// the documented localization procedure) and primes with a single collect.
// Any open/add failure closes the query.
func NewPDHSource(paths []string) (*PDHSource, error) {
	p := &PDHSource{}
	if err := pdh.Load(); err != nil {
		return nil, err
	}
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&p.query))); r != 0 {
		return nil, fmt.Errorf("PdhOpenQuery: 0x%x", r)
	}
	for _, path := range paths {
		if !isWildcard(path) {
			if err := p.roomFor(1); err != nil {
				p.Close()
				return nil, err
			}
			if err := p.addCounter(path, false); err != nil {
				p.Close()
				return nil, err
			}
			continue
		}
		p.wildcards = append(p.wildcards, path)
		expanded, err := p.expandLocalized(path)
		if err != nil {
			p.Close()
			return nil, err
		}
		if err := p.roomFor(len(expanded)); err != nil {
			p.Close()
			return nil, err
		}
		for _, e := range expanded {
			if err := p.addLocalized(e.localized, e.english); err != nil {
				p.Close()
				return nil, err
			}
		}
	}
	pdhCollectCall(p.query) // one priming collect
	return p, nil
}

// roomFor enforces the per-source counter ceiling before handles are added.
func (p *PDHSource) roomFor(n int) error {
	if len(p.counters)+n > pdhMaxCountersPerSource {
		return errPDHTooManyCounters
	}
	return nil
}

// addCounter adds one concrete English counter to the existing query.
func (p *PDHSource) addCounter(path string, wildcard bool) error {
	h, err := pdhAddEnglishCall(p.query, path)
	if err != nil {
		return err
	}
	p.counters = append(p.counters, pdhCounter{path: path, handle: h, wildcard: wildcard})
	return nil
}

// addLocalized adds one expanded, already-localized path with PdhAddCounterW
// while retaining the English logical identity: Paths(), Collect() and the
// engine specs keep keying on English; only the native handle is localized.
func (p *PDHSource) addLocalized(localized, english string) error {
	h, err := pdhAddCounterCall(p.query, localized)
	if err != nil {
		return err
	}
	p.counters = append(p.counters, pdhCounter{path: english, handle: h, wildcard: true})
	return nil
}

// pdhExpanded pairs one expanded localized path with its English identity.
type pdhExpanded struct {
	english   string
	localized string
}

// expandLocalized implements the documented PdhAddEnglishCounterW wildcard
// procedure (W2-03): add the English wildcard, read its localized full path
// back with PdhGetCounterInfoW (szFullPath still contains the wildcard),
// expand THAT localized pattern, and pair every resulting localized path with
// its English identity. The temporary wildcard handle is removed again; only
// concrete instances are retained.
func (p *PDHSource) expandLocalized(pattern string) ([]pdhExpanded, error) {
	h, err := pdhAddEnglishCall(p.query, pattern)
	if err != nil {
		return nil, err
	}
	localizedPattern, err := pdhLocalizedPath(h)
	pdhRemoveCall(h) // temporary translation handle, never collected
	if err != nil {
		return nil, err
	}
	paths, err := expandWildCard(localizedPattern)
	if err != nil {
		return nil, err
	}
	out := make([]pdhExpanded, 0, len(paths))
	for _, lp := range paths {
		ep, ok := englishPathFor(pattern, lp)
		if !ok {
			return nil, fmt.Errorf("PDH wildcard %q: no English identity for %q", pattern, lp)
		}
		out = append(out, pdhExpanded{english: ep, localized: lp})
	}
	return out, nil
}

// pdhLocalizedPath reads the localized full counter path back from a counter
// handle using the two-call PdhGetCounterInfoW size pattern. The provider's
// size is bounded before allocation and the returned pointer is validated to
// lie inside the buffer (W2-04).
func pdhLocalizedPath(handle uintptr) (string, error) {
	var size uint32
	if r := pdhGetInfoCall(handle, nil, &size); r != pdhMoreData {
		return "", fmt.Errorf("PdhGetCounterInfo size: 0x%x", r)
	}
	if size == 0 {
		return "", errors.New("PdhGetCounterInfo: empty counter info")
	}
	if size > pdhMaxCounterInfoBytes {
		return "", errPDHCounterInfoTooLarge
	}
	buf := make([]byte, size)
	if r := pdhGetInfoCall(handle, &buf[0], &size); r != 0 {
		return "", fmt.Errorf("PdhGetCounterInfo: 0x%x", r)
	}
	if size < uint32(unsafe.Sizeof(pdhCounterInfo{})) {
		return "", errors.New("PdhGetCounterInfo: buffer smaller than PDH_COUNTER_INFO_W")
	}
	info := (*pdhCounterInfo)(unsafe.Pointer(&buf[0]))
	base := uintptr(unsafe.Pointer(&buf[0]))
	if info.szFullPath < base || info.szFullPath >= base+uintptr(len(buf)) || (info.szFullPath-base)%2 != 0 {
		return "", errors.New("PdhGetCounterInfo: full path outside buffer")
	}
	off := int(info.szFullPath - base)
	u := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[off])), (len(buf)-off)/2)
	n := 0
	for n < len(u) && u[n] != 0 {
		n++
	}
	if n == len(u) {
		return "", errors.New("PdhGetCounterInfo: unterminated full path")
	}
	return windows.UTF16ToString(u[:n]), nil
}

// pdhInstanceSection extracts the instance text between the first '(' and the
// following ')\'. Splitting on ')\', not the first ')', tolerates parentheses
// inside instance names (same rule as parseProc10Path). PDH path structure is
// locale-invariant; only object and counter names are translated.
func pdhInstanceSection(path string) (string, bool) {
	open := strings.IndexByte(path, '(')
	if open < 0 {
		return "", false
	}
	rest := path[open+1:]
	end := strings.Index(rest, `)\`)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// englishPathFor rebuilds the English logical path for one localized expanded
// path: object and counter come from the original English pattern, the
// instance from the expansion. Instance names are data (process image names,
// disk numbers, pool names), not translated text, so the result is the same
// identifier an English expansion produces on an English host.
func englishPathFor(pattern, localized string) (string, bool) {
	inst, ok := pdhInstanceSection(localized)
	if !ok {
		return "", false
	}
	open := strings.IndexByte(pattern, '(')
	if open < 0 {
		return "", false
	}
	rest := pattern[open+1:]
	end := strings.Index(rest, `)\`)
	if end < 0 {
		return "", false
	}
	return pattern[:open+1] + inst + pattern[open+1+end:], true
}

// expandWildCard expands one (already localized) wildcard pattern into
// concrete instance paths. Both the provider-reported buffer size and the
// instance count are bounded BEFORE allocating/retaining (W2-04).
func expandWildCard(path string) ([]string, error) {
	cp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var n uint32
	r := pdhExpandCall(cp, nil, &n)
	if r != 0 && r != pdhMoreData {
		return nil, fmt.Errorf("PdhExpandWildCardPath %q: 0x%x", path, r)
	}
	if n == 0 {
		return nil, fmt.Errorf("PdhExpandWildCardPath %q: no instances", path)
	}
	if n > pdhMaxExpandChars {
		return nil, errPDHExpandTooLarge
	}
	buf := make([]uint16, n)
	r = pdhExpandCall(cp, &buf[0], &n)
	if r != 0 {
		return nil, fmt.Errorf("PdhExpandWildCardPath %q: 0x%x", path, r)
	}
	var out []string // multi-string: NUL-separated paths, double NUL at the end
	for start, i := 0, 0; i < int(n); i++ {
		if buf[i] == 0 {
			if i > start {
				if len(out) >= pdhMaxInstancesPerPattern {
					return nil, errPDHTooManyInstances
				}
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
// PDH_FMT_NOCAP100 is mandatory: without it PDH itself caps percentages at
// 100 and multi-core CPU is truncated before the uncapped engine sees it
// (W2-02).
func (p *PDHSource) Collect() ([]CounterReading, error) {
	if p.closed {
		return nil, errors.New("PDH query closed")
	}
	if r := pdhCollectCall(p.query); r != 0 {
		return nil, fmt.Errorf("PdhCollectQueryData: 0x%x", r)
	}
	out := make([]CounterReading, 0, len(p.counters))
	for _, c := range p.counters {
		var v pdhFmtCounterValue
		r := pdhFormatCall(c.handle, pdhFmtDouble|pdhFmtNoCap100, &v)
		out = append(out, readingFrom(c.path, v, r))
	}
	return out, nil
}

// readingFrom maps one formatted result; OK only when both the API call and
// the per-counter CStatus report success. NEW_DATA (1) is a success status, so
// it is accepted here exactly as the engine accepts it.
func readingFrom(path string, v pdhFmtCounterValue, apiStatus uintptr) CounterReading {
	if apiStatus != 0 {
		return CounterReading{Path: path, Status: uint32(apiStatus)}
	}
	return CounterReading{Path: path, Value: v.Double, Status: v.CStatus, OK: CounterStatusOK(v.CStatus)}
}

// RefreshInstances reconciles ONLY wildcard counters on the same query handle:
// an instance that is still present keeps its handle (and therefore its PDH
// baseline), a departed instance is removed, a new one is added. The resolver
// calls it when the process set changes, never every tick.
//
// Every pattern is expanded (via the localization procedure) before the query
// is touched, and the total-counter ceiling is checked before any removal, so
// a failed or over-cap expansion leaves the working counters intact instead
// of stripping the source down to its direct counters.
func (p *PDHSource) RefreshInstances() error {
	if p.closed {
		return errors.New("PDH query closed")
	}
	direct := 0
	for _, c := range p.counters {
		if !c.wildcard {
			direct++
		}
	}
	desired := make(map[string]string) // English identity -> localized path
	var order []string
	for _, pattern := range p.wildcards {
		expanded, err := p.expandLocalized(pattern)
		if err != nil {
			return err
		}
		for _, e := range expanded {
			if _, dup := desired[e.english]; !dup {
				desired[e.english] = e.localized
				order = append(order, e.english)
			}
		}
	}
	if direct+len(order) > pdhMaxCountersPerSource {
		return errPDHTooManyCounters
	}

	kept := make([]pdhCounter, 0, len(p.counters))
	present := make(map[string]bool, len(p.counters))
	changed := false
	for _, c := range p.counters {
		if !c.wildcard {
			kept = append(kept, c) // direct counters keep their handles
			continue
		}
		if _, ok := desired[c.path]; ok {
			kept = append(kept, c)
			present[c.path] = true
			continue
		}
		pdhRemoveCall(c.handle)
		changed = true
	}
	p.counters = kept
	for _, ep := range order {
		if present[ep] {
			continue
		}
		if err := p.addLocalized(desired[ep], ep); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		pdhCollectCall(p.query) // prime only the newly added counters
	}
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
// These are always ENGLISH logical identities, even on a localized host where
// the native handles were added from localized paths (W2-03).
func (p *PDHSource) Paths() []string {
	out := make([]string, len(p.counters))
	for i, c := range p.counters {
		out[i] = c.path
	}
	return out
}
