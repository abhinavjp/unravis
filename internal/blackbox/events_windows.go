//go:build windows

package blackbox

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wevtapi query: EvtQuery per channel with the allowlist XPath + watermark,
// EvtNext in bounded batches, EvtRender of ONLY EventID/Level/TimeCreated via
// a value-path render context (no message, no EventData, no strings at all).

var (
	wevtapi              = windows.NewLazySystemDLL("wevtapi.dll")
	procEvtQuery         = wevtapi.NewProc("EvtQuery")
	procEvtNext          = wevtapi.NewProc("EvtNext")
	procEvtRender        = wevtapi.NewProc("EvtRender")
	procEvtClose         = wevtapi.NewProc("EvtClose")
	procEvtCreateContext = wevtapi.NewProc("EvtCreateRenderContext")
)

const (
	evtQueryChannelPath      = 0x1
	evtQueryReverseDirection = 0x200 // newest first when the cap truncates
	evtRenderContextValues   = 0
	evtRenderEventValues     = 1
	evtVarTypeByte           = 4
	evtVarTypeUInt16         = 6
	evtVarTypeFileTime       = 17
)

// Render paths: the only fields ever read out of an event.
var (
	renderPathEventID, _ = windows.UTF16PtrFromString("Event/System/EventID")
	renderPathLevel, _   = windows.UTF16PtrFromString("Event/System/Level")
	renderPathTime, _    = windows.UTF16PtrFromString("Event/System/TimeCreated/@SystemTime")
)

// evtVariant is EVT_VARIANT: 8-byte value union, Count, Type (16 bytes).
type evtVariant struct {
	val   uint64
	count uint32
	typ   uint32
}

// eventsSupported reports whether wevtapi is loadable (factory probe).
func eventsSupported() error { return wevtapi.Load() }

// queryEvents reads at most maxEventsPerTick allowlisted rows newer than
// since. Any query failure (e.g. access denied) is returned: the group is
// omitted, never 0.
func queryEvents(since time.Time) ([]rawEvent, error) {
	ms := time.Since(since).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	var rows []rawEvent
	for _, ch := range eventsChannels {
		r, err := queryEventChannel(ch.name, eventsXPath(ch.num, ms), ch.num, maxEventsPerTick-len(rows))
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
		if len(rows) >= maxEventsPerTick {
			break
		}
	}
	return rows, nil
}

func queryEventChannel(name, xpath string, channel uint8, limit int) ([]rawEvent, error) {
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	xp, err := windows.UTF16PtrFromString(xpath)
	if err != nil {
		return nil, err
	}
	q, _, e := procEvtQuery.Call(0, uintptr(unsafe.Pointer(np)), uintptr(unsafe.Pointer(xp)), evtQueryChannelPath|evtQueryReverseDirection)
	if q == 0 {
		return nil, fmt.Errorf("EvtQuery %s: %w", name, e)
	}
	defer procEvtClose.Call(q)
	paths := [...]*uint16{renderPathEventID, renderPathLevel, renderPathTime}
	ctx, _, e := procEvtCreateContext.Call(uintptr(len(paths)), uintptr(unsafe.Pointer(&paths[0])), evtRenderContextValues)
	if ctx == 0 {
		return nil, fmt.Errorf("EvtCreateRenderContext: %w", e)
	}
	defer procEvtClose.Call(ctx)
	var rows []rawEvent
	var hs [64]uintptr
	for len(rows) < limit {
		n := limit - len(rows)
		if n > len(hs) {
			n = len(hs)
		}
		var got uint32
		r, _, e := procEvtNext.Call(q, uintptr(n), uintptr(unsafe.Pointer(&hs[0])), 0, 0, uintptr(unsafe.Pointer(&got)))
		if r == 0 {
			if e == windows.ERROR_NO_MORE_ITEMS || e == windows.ERROR_TIMEOUT {
				break
			}
			return nil, fmt.Errorf("EvtNext %s: %w", name, e)
		}
		for i := 0; i < int(got); i++ {
			if row, ok := renderEventRow(ctx, hs[i], channel); ok {
				rows = append(rows, row)
			}
			procEvtClose.Call(hs[i])
		}
		if int(got) < n {
			break
		}
	}
	return rows, nil
}

// renderEventRow extracts the three numeric values; anything unexpected is
// dropped. No string variant is ever requested or stored.
func renderEventRow(ctx, h uintptr, channel uint8) (rawEvent, bool) {
	var buf [4]evtVariant
	var used, count uint32
	r, _, _ := procEvtRender.Call(ctx, h, evtRenderEventValues, uintptr(unsafe.Sizeof(buf)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&count)))
	if r == 0 || count < 3 {
		return rawEvent{}, false
	}
	id, level, ts := buf[0], buf[1], buf[2]
	if id.typ != evtVarTypeUInt16 || ts.typ != evtVarTypeFileTime || ts.val == 0 {
		return rawEvent{}, false
	}
	var lv uint8
	if level.typ == evtVarTypeByte {
		lv = uint8(level.val)
	}
	ft := windows.Filetime{LowDateTime: uint32(ts.val), HighDateTime: uint32(ts.val >> 32)}
	return rawEvent{Channel: channel, EventID: uint32(id.val), Level: lv, T: time.Unix(0, ft.Nanoseconds())}, true
}
