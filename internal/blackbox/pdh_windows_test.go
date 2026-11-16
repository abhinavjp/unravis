//go:build windows

package blackbox

import (
	"reflect"
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

// W1-01: NEW_DATA is a success status at the native boundary too. The engine and
// the source must agree, or a valid reading is dropped before it can be used.
func TestReadingFromNewDataIsOK(t *testing.T) {
	r := readingFrom(`\X\Y`, pdhFmtCounterValue{CStatus: 1, Double: 3.5}, 0)
	if !r.OK || r.Value != 3.5 || r.Status != 1 {
		t.Errorf("NEW_DATA reading: got %+v, want OK=true Value=3.5 Status=1", r)
	}
	// An API failure still wins over a success-looking CStatus.
	if r := readingFrom(`\X\Y`, pdhFmtCounterValue{CStatus: 1, Double: 3.5}, 0xC0000BB9); r.OK {
		t.Errorf("API failure with NEW_DATA CStatus: got %+v, want OK=false", r)
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

// W2-02: the exact format flags must reach PdhGetFormattedCounterValue.
// PDH_FMT_NOCAP100 is 0x8000 per pdh.h (0x1000 would be PDH_FMT_NOSCALE);
// without it PDH caps percentages at 100 before the engine sees them.
func TestCollectFormatsWithNoCap100(t *testing.T) {
	if pdhFmtNoCap100 != 0x00008000 {
		t.Fatalf("pdhFmtNoCap100 = 0x%x, want 0x8000 (PDH_FMT_NOCAP100 per pdh.h)", pdhFmtNoCap100)
	}
	var gotFormat, gotHandle uintptr
	sFormat, sCollect := pdhFormatCall, pdhCollectCall
	t.Cleanup(func() { pdhFormatCall, pdhCollectCall = sFormat, sCollect })
	pdhFormatCall = func(handle, format uintptr, out *pdhFmtCounterValue) uintptr {
		gotHandle, gotFormat = handle, format
		*out = pdhFmtCounterValue{Double: 350}
		return 0
	}
	pdhCollectCall = func(query uintptr) uintptr { return 0 }

	p := &PDHSource{counters: []pdhCounter{{path: `\Process(app)\% Processor Time`, handle: 7}}}
	rs, err := p.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if gotFormat != 0x8200 { // PDH_FMT_DOUBLE (0x200) | PDH_FMT_NOCAP100 (0x8000)
		t.Fatalf("native format flags = 0x%x, want 0x8200 (DOUBLE|NOCAP100)", gotFormat)
	}
	if gotHandle != 7 {
		t.Fatalf("formatted handle = %d, want 7", gotHandle)
	}
	if len(rs) != 1 || !rs[0].OK || rs[0].Value != 350 {
		t.Fatalf("readings = %+v, want the uncapped 350 passed through", rs)
	}
}

// W2-03: the documented localization sequence must run in order — add the
// English wildcard, read back the localized full path (two-call size
// pattern), expand THAT localized pattern (two-call again), then add the
// localized paths — while Paths() and Collect() keep returning ENGLISH
// identities for the group parsers and engine specs.
func TestWildcardLocalizationCallOrder(t *testing.T) {
	const english = `\Process(*)\% Processor Time`
	const localizedPattern = `\Prozess(*)\% Prozessorzeit`
	expanded := []string{`\Prozess(app#1)\% Prozessorzeit`, `\Prozess(app#2)\% Prozessorzeit`}
	rig := installFakePDH(t, func(pattern string, buf *uint16, size *uint32) uintptr {
		return fillExpandBuf(expanded, buf, size)
	})
	rig.localizedOverride = localizedPattern

	p := &PDHSource{query: 42, wildcards: []string{english}}
	if err := p.RefreshInstances(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"addEnglish:" + english,
		"getInfo", "getInfo",
		"removeTemp:101",
		"expand:" + localizedPattern, "expand:" + localizedPattern,
		"addLocalized:" + expanded[0],
		"addLocalized:" + expanded[1],
		"collect",
	}
	if !reflect.DeepEqual(rig.events, want) {
		t.Fatalf("call order:\n got %v\nwant %v", rig.events, want)
	}
	wantPaths := []string{`\Process(app#1)\% Processor Time`, `\Process(app#2)\% Processor Time`}
	if !reflect.DeepEqual(p.Paths(), wantPaths) {
		t.Fatalf("Paths() = %v, want English identities %v", p.Paths(), wantPaths)
	}
	// Collect reports the same English identities from the localized handles.
	sFormat, sCollect := pdhFormatCall, pdhCollectCall
	t.Cleanup(func() { pdhFormatCall, pdhCollectCall = sFormat, sCollect })
	pdhFormatCall = func(handle, format uintptr, out *pdhFmtCounterValue) uintptr {
		*out = pdhFmtCounterValue{Double: 1}
		return 0
	}
	pdhCollectCall = func(query uintptr) uintptr { return 0 }
	rs, err := p.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Path != wantPaths[0] || rs[1].Path != wantPaths[1] {
		t.Fatalf("Collect paths = %+v, want %v", rs, wantPaths)
	}
}

// W2-03: English identity reconstruction takes object/counter from the
// English pattern and the instance from the localized expansion (instance
// names are data, not translated text). Partial wildcards and parentheses
// inside instance names must survive.
func TestEnglishPathFor(t *testing.T) {
	cases := []struct {
		pattern, localized, want string
		ok                       bool
	}{
		{`\Process(*)\% Processor Time`, `\Prozess(app#1)\% Prozessorzeit`, `\Process(app#1)\% Processor Time`, true},
		{`\Process(chrome*)\ID Process`, `\Prozess(chrome#3)\ID-Prozess`, `\Process(chrome#3)\ID Process`, true},
		{`\PhysicalDisk(*)\Disk Reads/sec`, `\Physikalischer Datenträger(0 C:)\Lesevorgänge/s`, `\PhysicalDisk(0 C:)\Disk Reads/sec`, true},
		{`\W3SVC_W3WP(*)\Active Requests`, `\W3SVC_W3WP(pool (x86))\Aktive Anforderungen`, `\W3SVC_W3WP(pool (x86))\Active Requests`, true},
		{`\Process(*)\X`, `\Prozess\X`, "", false},
	}
	for _, c := range cases {
		got, ok := englishPathFor(c.pattern, c.localized)
		if ok != c.ok || got != c.want {
			t.Errorf("englishPathFor(%q, %q) = %q,%v want %q,%v", c.pattern, c.localized, got, ok, c.want, c.ok)
		}
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
