//go:build windows

package blackbox

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// utf16z encodes a test path as NUL-terminated UTF-16; the fake paths are
// constants without NUL, so an encoding error is a test bug.
func utf16z(s string) []uint16 {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		panic(err)
	}
	return u
}

// fillExpandBuf mimics PdhExpandWildCardPathW: the nil-buffer call reports the
// needed multi-string size with PDH_MORE_DATA; the second call writes the
// NUL-separated, double-NUL-terminated UTF-16 paths.
func fillExpandBuf(paths []string, buf *uint16, size *uint32) uintptr {
	var u []uint16
	for _, p := range paths {
		u = append(u, utf16z(p)...) // each includes its NUL
	}
	u = append(u, 0) // double-NUL terminator
	if buf == nil {
		*size = uint32(len(u))
		return pdhMoreData
	}
	if uint32(len(u)) > *size {
		*size = uint32(len(u))
		return pdhMoreData
	}
	copy(unsafe.Slice(buf, len(u)), u)
	*size = uint32(len(u))
	return 0
}

// fillCounterInfo mimics PdhGetCounterInfoW: it reports the needed size with
// PDH_MORE_DATA, then writes a PDH_COUNTER_INFO_W whose szFullPath points at
// fullPath encoded inside the same buffer.
func fillCounterInfo(fullPath string, buf *byte, size *uint32) uintptr {
	u := utf16z(fullPath)
	need := uint32(unsafe.Sizeof(pdhCounterInfo{})) + uint32(len(u)*2)
	if buf == nil {
		*size = need
		return pdhMoreData
	}
	if *size < need {
		*size = need
		return pdhMoreData
	}
	info := (*pdhCounterInfo)(unsafe.Pointer(buf))
	*info = pdhCounterInfo{}
	off := int(unsafe.Sizeof(pdhCounterInfo{}))
	copy(unsafe.Slice(buf, int(*size))[off:], unsafe.Slice((*byte)(unsafe.Pointer(&u[0])), len(u)*2))
	info.szFullPath = uintptr(unsafe.Pointer(buf)) + uintptr(off)
	info.dwLength = *size
	return 0
}

// fakePDHRig replaces every native seam PDHSource uses, records the call
// sequence and simulates a provider. expandFn drives PdhExpandWildCardPathW;
// localizedOverride, when set, is the szFullPath PdhGetCounterInfoW returns
// for any handle (simulating a non-English host). Removals of the temporary
// translation handles added by the localization procedure are recorded as
// "removeTemp:" — they are not removals of retained counters.
type fakePDHRig struct {
	events            []string
	added             map[uintptr]string
	temp              map[uintptr]bool
	next              uintptr
	expandFn          func(pattern string, buf *uint16, size *uint32) uintptr
	localizedOverride string
}

func installFakePDH(t *testing.T, expandFn func(string, *uint16, *uint32) uintptr) *fakePDHRig {
	t.Helper()
	r := &fakePDHRig{added: make(map[uintptr]string), temp: make(map[uintptr]bool), next: 100, expandFn: expandFn}
	sAddEng, sAddCtr, sInfo := pdhAddEnglishCall, pdhAddCounterCall, pdhGetInfoCall
	sExp, sRem, sCol := pdhExpandCall, pdhRemoveCall, pdhCollectCall
	t.Cleanup(func() {
		pdhAddEnglishCall, pdhAddCounterCall, pdhGetInfoCall = sAddEng, sAddCtr, sInfo
		pdhExpandCall, pdhRemoveCall, pdhCollectCall = sExp, sRem, sCol
	})
	pdhAddEnglishCall = func(query uintptr, path string) (uintptr, error) {
		r.events = append(r.events, "addEnglish:"+path)
		r.next++
		r.added[r.next] = path
		r.temp[r.next] = true // wildcard translation handle, removed again
		return r.next, nil
	}
	pdhAddCounterCall = func(query uintptr, path string) (uintptr, error) {
		r.events = append(r.events, "addLocalized:"+path)
		r.next++
		r.added[r.next] = path
		return r.next, nil
	}
	pdhGetInfoCall = func(handle uintptr, buf *byte, size *uint32) uintptr {
		r.events = append(r.events, "getInfo")
		path := r.added[handle]
		if r.localizedOverride != "" {
			path = r.localizedOverride
		}
		return fillCounterInfo(path, buf, size)
	}
	pdhExpandCall = func(pattern *uint16, buf *uint16, size *uint32) uintptr {
		p := windows.UTF16PtrToString(pattern)
		r.events = append(r.events, "expand:"+p)
		return r.expandFn(p, buf, size)
	}
	pdhRemoveCall = func(handle uintptr) {
		if r.temp[handle] {
			r.events = append(r.events, fmt.Sprintf("removeTemp:%d", handle))
		} else {
			r.events = append(r.events, fmt.Sprintf("remove:%d", handle))
		}
	}
	pdhCollectCall = func(query uintptr) uintptr {
		r.events = append(r.events, "collect")
		return 0
	}
	return r
}

// touchedQuery reports whether any event removed a RETAINED counter, added a
// localized counter or collected — the operations a failed refresh must never
// reach. Temporary translation-handle removals are part of expansion itself.
func touchedQuery(events []string) bool {
	for _, e := range events {
		if strings.HasPrefix(e, "remove:") || strings.HasPrefix(e, "addLocalized:") || e == "collect" {
			return true
		}
	}
	return false
}

// W2-04: an oversized provider-reported expansion size must be rejected
// BEFORE any buffer is allocated — the size call is the only native call.
func TestExpandWildCardOversizeRejectedBeforeAllocation(t *testing.T) {
	sExp := pdhExpandCall
	t.Cleanup(func() { pdhExpandCall = sExp })
	calls := 0
	pdhExpandCall = func(pattern *uint16, buf *uint16, size *uint32) uintptr {
		calls++
		if buf != nil {
			t.Error("buffer call reached: oversize must be rejected before allocation")
		}
		*size = pdhMaxExpandChars + 1
		return pdhMoreData
	}
	_, err := expandWildCard(`\Process(*)\ID Process`)
	if !errors.Is(err, errPDHExpandTooLarge) {
		t.Fatalf("err = %v, want errPDHExpandTooLarge", err)
	}
	if calls != 1 {
		t.Fatalf("native calls = %d, want exactly the size-discovery call", calls)
	}
}

// W2-04: more instances than the per-pattern cap is rejected; exactly the cap
// is still accepted.
func TestExpandWildCardInstanceCap(t *testing.T) {
	sExp := pdhExpandCall
	t.Cleanup(func() { pdhExpandCall = sExp })
	makePaths := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf(`\Process(p%d)\ID Process`, i)
		}
		return out
	}
	pdhExpandCall = func(pattern *uint16, buf *uint16, size *uint32) uintptr {
		return fillExpandBuf(makePaths(pdhMaxInstancesPerPattern+1), buf, size)
	}
	if _, err := expandWildCard(`\Process(*)\ID Process`); !errors.Is(err, errPDHTooManyInstances) {
		t.Fatalf("over cap: err = %v, want errPDHTooManyInstances", err)
	}
	pdhExpandCall = func(pattern *uint16, buf *uint16, size *uint32) uintptr {
		return fillExpandBuf(makePaths(pdhMaxInstancesPerPattern), buf, size)
	}
	got, err := expandWildCard(`\Process(*)\ID Process`)
	if err != nil || len(got) != pdhMaxInstancesPerPattern {
		t.Fatalf("at cap: got %d paths, err %v; want %d paths, nil error", len(got), err, pdhMaxInstancesPerPattern)
	}
}

// W2-04: the counter-info buffer is bounded too (same provider-size trust
// problem as expansion).
func TestLocalizedPathOversizeRejected(t *testing.T) {
	sInfo := pdhGetInfoCall
	t.Cleanup(func() { pdhGetInfoCall = sInfo })
	pdhGetInfoCall = func(handle uintptr, buf *byte, size *uint32) uintptr {
		if buf != nil {
			t.Error("buffer call reached: oversize must be rejected before allocation")
		}
		*size = pdhMaxCounterInfoBytes + 1
		return pdhMoreData
	}
	if _, err := pdhLocalizedPath(1); !errors.Is(err, errPDHCounterInfoTooLarge) {
		t.Fatalf("err = %v, want errPDHCounterInfoTooLarge", err)
	}
}

// W2-04: patterns individually within the per-pattern cap must still be
// rejected when together they exceed the per-source counter cap. Only the
// query open/close are real native calls here; every add is faked.
func TestNewPDHSourceTotalCounterCap(t *testing.T) {
	const perPattern = 3000 // 3 x 3000 = 9000 > pdhMaxCountersPerSource (8192)
	installFakePDH(t, func(pattern string, buf *uint16, size *uint32) uintptr {
		counter := strings.TrimPrefix(pattern, `\Process(*)\`)
		paths := make([]string, perPattern)
		for i := range paths {
			paths[i] = fmt.Sprintf(`\Process(p%d)\%s`, i, counter)
		}
		return fillExpandBuf(paths, buf, size)
	})
	src, err := NewPDHSource([]string{`\Process(*)\A`, `\Process(*)\B`, `\Process(*)\C`})
	if !errors.Is(err, errPDHTooManyCounters) {
		t.Fatalf("err = %v, want errPDHTooManyCounters", err)
	}
	if src != nil {
		src.Close()
		t.Fatal("expected nil source when the counter cap is exceeded")
	}
}

// W2-04: an over-cap refresh fails BEFORE touching the query, leaving every
// previously working counter (and its PDH baseline handle) intact.
func TestRefreshTotalCounterCapKeepsCounters(t *testing.T) {
	// Two patterns at the per-pattern cap plus one direct counter:
	// 4096 + 4096 + 1 = 8193 > pdhMaxCountersPerSource (8192).
	installFakePDH(t, func(pattern string, buf *uint16, size *uint32) uintptr {
		counter := strings.TrimPrefix(pattern, `\Process(*)\`)
		paths := make([]string, pdhMaxInstancesPerPattern)
		for i := range paths {
			paths[i] = fmt.Sprintf(`\Process(p%d)\%s`, i, counter)
		}
		return fillExpandBuf(paths, buf, size)
	})
	before := []pdhCounter{
		{path: `\Process(existing)\A`, handle: 500, wildcard: true},
		{path: `\Memory\Available Bytes`, handle: 1},
	}
	p := &PDHSource{
		query:     42,
		counters:  append([]pdhCounter(nil), before...),
		wildcards: []string{`\Process(*)\A`, `\Process(*)\B`},
	}
	if err := p.RefreshInstances(); !errors.Is(err, errPDHTooManyCounters) {
		t.Fatalf("err = %v, want errPDHTooManyCounters", err)
	}
	if !reflect.DeepEqual(p.counters, before) {
		t.Fatalf("over-cap refresh changed counters: %+v", p.counters)
	}
}

// W2-04: a partial refresh failure (first pattern expands, second fails
// natively) must leave the previously working counters intact — no removal,
// no addition, no priming collect.
func TestRefreshExpansionFailureKeepsWorkingCounters(t *testing.T) {
	rig := installFakePDH(t, func(pattern string, buf *uint16, size *uint32) uintptr {
		if strings.HasSuffix(pattern, `\B`) {
			return 0xC0000BB9 // native expansion failure on the second pattern
		}
		return fillExpandBuf([]string{`\Process(app#1)\A`, `\Process(app#2)\A`}, buf, size)
	})
	before := []pdhCounter{
		{path: `\Process(app#1)\A`, handle: 500, wildcard: true},
		{path: `\Process(app#2)\A`, handle: 501, wildcard: true},
		{path: `\Memory\Available Bytes`, handle: 1},
	}
	p := &PDHSource{
		query:     42,
		counters:  append([]pdhCounter(nil), before...),
		wildcards: []string{`\Process(*)\A`, `\Process(*)\B`},
	}
	if err := p.RefreshInstances(); err == nil {
		t.Fatal("expected the native expansion failure to propagate")
	}
	if !reflect.DeepEqual(p.counters, before) {
		t.Fatalf("failed refresh changed counters: %+v", p.counters)
	}
	if touchedQuery(rig.events) {
		t.Fatalf("failed refresh touched the query: %v", rig.events)
	}
}

// The incremental refresh contract: an unchanged instance keeps its handle
// (and PDH baseline), a departed one is removed, a new one is added, and the
// query is primed exactly once.
func TestRefreshKeepsUnchangedHandles(t *testing.T) {
	rig := installFakePDH(t, func(pattern string, buf *uint16, size *uint32) uintptr {
		return fillExpandBuf([]string{`\Process(app#1)\A`, `\Process(app#3)\A`}, buf, size)
	})
	p := &PDHSource{
		query: 42,
		counters: []pdhCounter{
			{path: `\Process(app#1)\A`, handle: 500, wildcard: true},
			{path: `\Process(app#2)\A`, handle: 501, wildcard: true},
			{path: `\Memory\Available Bytes`, handle: 1},
		},
		wildcards: []string{`\Process(*)\A`},
	}
	if err := p.RefreshInstances(); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{`\Process(app#1)\A`, `\Memory\Available Bytes`, `\Process(app#3)\A`}
	if !reflect.DeepEqual(p.Paths(), wantPaths) {
		t.Fatalf("Paths() = %v, want %v", p.Paths(), wantPaths)
	}
	if p.counters[0].handle != 500 || p.counters[1].handle != 1 {
		t.Fatalf("unchanged counters lost their handles: %+v", p.counters)
	}
	var removes, adds, collects int
	for _, e := range rig.events {
		switch {
		case e == "remove:501":
			removes++
		case strings.HasPrefix(e, "remove:"):
			t.Fatalf("unexpected removal: %s", e)
		case strings.HasPrefix(e, "addLocalized:"):
			adds++
		case e == "collect":
			collects++
		}
	}
	if removes != 1 || adds != 1 || collects != 1 {
		t.Fatalf("events = %v, want one remove, one add, one priming collect", rig.events)
	}
}
