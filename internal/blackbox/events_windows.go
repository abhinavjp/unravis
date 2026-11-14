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
	evtQueryReverseDirection = 0x200 // newest first: only the reset probe uses it
	evtRenderContextValues   = 0
	evtRenderEventValues     = 1
	evtVarTypeByte           = 4
	evtVarTypeUInt16         = 6
	evtVarTypeUInt64         = 10
	evtVarTypeFileTime       = 17
)

// Render paths: the only fields ever read out of an event. EventRecordID is the
// stable per-channel identity used for the forward cursor (W6-01).
var (
	renderPathEventID, _  = windows.UTF16PtrFromString("Event/System/EventID")
	renderPathLevel, _    = windows.UTF16PtrFromString("Event/System/Level")
	renderPathTime, _     = windows.UTF16PtrFromString("Event/System/TimeCreated/@SystemTime")
	renderPathRecordID, _ = windows.UTF16PtrFromString("Event/System/EventRecordID")
)

// evtVariant is EVT_VARIANT: 8-byte value union, Count, Type (16 bytes).
type evtVariant struct {
	val   uint64
	count uint32
	typ   uint32
}

// eventsSupported reports whether wevtapi is loadable (factory probe).
func eventsSupported() error { return wevtapi.Load() }

// eventsChannelName maps the fixed channel enum to its log name.
func eventsChannelName(channel uint8) string {
	for _, ch := range eventsChannels {
		if ch.num == channel {
			return ch.name
		}
	}
	return ""
}

// queryEventChannel reads one channel forward from afterID, attempting at most
// budget records (rendered or not, W6-02) and stopping cooperatively at deadline
// between bounded native calls. The first read (afterID==0) uses the no-backfill
// time window; later reads use a strict record-ID cursor (W6-01). Any query
// failure (e.g. access denied) is returned: the group is omitted, never 0.
func queryEventChannel(channel uint8, afterID uint64, floor time.Time, budget int, deadline time.Time) (eventChannelRead, error) {
	res := eventChannelRead{lastID: afterID}
	if budget <= 0 {
		return res, nil
	}
	name := eventsChannelName(channel)
	xpath := eventsXPathAfter(channel, afterID)
	if afterID == 0 {
		ms := time.Since(floor).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		xpath = eventsXPath(channel, ms)
	}
	rows, lastID, attempted, failed, trunc, err := readEventQuery(name, xpath, channel, budget, deadline)
	if err != nil {
		return res, err
	}
	res.rows, res.lastID, res.attempted, res.failed, res.truncated = rows, lastID, attempted, failed, trunc
	// Log clear/reset: a forward query past the cursor that finds nothing may
	// mean record IDs restarted. Probe the newest ID once; if it is below the
	// cursor, re-read the new generation from the start and mark the loss.
	if afterID > 0 && attempted == 0 {
		if newest, ok := newestRecordID(name); ok && newest < afterID {
			res.reset, res.lost = true, afterID
			ms := time.Since(floor).Milliseconds()
			if ms < 0 {
				ms = 0
			}
			rows2, lastID2, att2, fail2, trunc2, err2 := readEventQuery(name, eventsXPath(channel, ms), channel, budget, deadline)
			if err2 != nil {
				return res, err2
			}
			res.rows, res.lastID, res.attempted, res.failed, res.truncated = rows2, lastID2, att2, fail2, trunc2
		}
	}
	return res, nil
}

// readEventQuery is the bounded native read loop: EvtQuery (forward, oldest
// first), EvtNext in batches, EvtRender of ONLY the numeric system values. Every
// fetched record charges the budget whether or not it renders (W6-02), and the
// deadline is checked between batches. lastID advances over every fetched record
// ID so an unreadable record is never re-scanned forever.
func readEventQuery(name, xpath string, channel uint8, budget int, deadline time.Time) (rows []rawEvent, lastID uint64, attempted, failed int, truncated bool, err error) {
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, 0, 0, 0, false, err
	}
	xp, err := windows.UTF16PtrFromString(xpath)
	if err != nil {
		return nil, 0, 0, 0, false, err
	}
	q, _, e := procEvtQuery.Call(0, uintptr(unsafe.Pointer(np)), uintptr(unsafe.Pointer(xp)), evtQueryChannelPath)
	if q == 0 {
		return nil, 0, 0, 0, false, fmt.Errorf("EvtQuery: %w", e)
	}
	defer procEvtClose.Call(q)
	paths := [...]*uint16{renderPathEventID, renderPathLevel, renderPathTime, renderPathRecordID}
	ctx, _, e := procEvtCreateContext.Call(uintptr(len(paths)), uintptr(unsafe.Pointer(&paths[0])), evtRenderContextValues)
	if ctx == 0 {
		return nil, 0, 0, 0, false, fmt.Errorf("EvtCreateRenderContext: %w", e)
	}
	defer procEvtClose.Call(ctx)
	var hs [64]uintptr
	for attempted < budget {
		if time.Now().After(deadline) {
			truncated = true // cooperative elapsed-work budget (W6-02)
			break
		}
		n := budget - attempted
		if n > len(hs) {
			n = len(hs)
		}
		var got uint32
		r, _, e := procEvtNext.Call(q, uintptr(n), uintptr(unsafe.Pointer(&hs[0])), 0, 0, uintptr(unsafe.Pointer(&got)))
		if r == 0 {
			if e == windows.ERROR_NO_MORE_ITEMS || e == windows.ERROR_TIMEOUT {
				break
			}
			return rows, lastID, attempted, failed, truncated, fmt.Errorf("EvtNext: %w", e)
		}
		for i := 0; i < int(got); i++ {
			attempted++ // charge every fetched record, rendered or not
			row, ok := renderEventRow(ctx, hs[i], channel)
			if row.RecordID > lastID {
				lastID = row.RecordID // advance past unreadable records too
			}
			if ok {
				rows = append(rows, row)
			} else {
				failed++
			}
			procEvtClose.Call(hs[i])
		}
		if int(got) < n {
			break // no more items
		}
		if attempted >= budget {
			truncated = true // budget exhausted with a full batch: backlog may remain
		}
	}
	return rows, lastID, attempted, failed, truncated, nil
}

// newestRecordID probes the channel's newest record ID (reverse, one record) to
// detect a log clear. It reads only EventRecordID; no text. ok=false on any
// failure (the caller then keeps the cursor rather than guessing a reset).
func newestRecordID(name string) (uint64, bool) {
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}
	q, _, _ := procEvtQuery.Call(0, uintptr(unsafe.Pointer(np)), 0, evtQueryChannelPath|evtQueryReverseDirection)
	if q == 0 {
		return 0, false
	}
	defer procEvtClose.Call(q)
	var hs [1]uintptr
	var got uint32
	r, _, _ := procEvtNext.Call(q, 1, uintptr(unsafe.Pointer(&hs[0])), 0, 0, uintptr(unsafe.Pointer(&got)))
	if r == 0 || got == 0 {
		return 0, false
	}
	defer procEvtClose.Call(hs[0])
	paths := [...]*uint16{renderPathRecordID}
	ctx, _, _ := procEvtCreateContext.Call(1, uintptr(unsafe.Pointer(&paths[0])), evtRenderContextValues)
	if ctx == 0 {
		return 0, false
	}
	defer procEvtClose.Call(ctx)
	var buf [1]evtVariant
	var used, count uint32
	rr, _, _ := procEvtRender.Call(ctx, hs[0], evtRenderEventValues, uintptr(unsafe.Sizeof(buf)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&count)))
	if rr == 0 || count < 1 || buf[0].typ != evtVarTypeUInt64 {
		return 0, false
	}
	return buf[0].val, true
}

// renderEventRow extracts the numeric system values; anything unexpected is
// dropped (missing, never 0). The record ID is returned even when the other
// fields are unreadable so the cursor can advance past the record. No string
// variant is ever requested or stored. An unreadable Level is NOT defaulted to 0
// (W6-02): the row is dropped and counted as a failed read.
func renderEventRow(ctx, h uintptr, channel uint8) (rawEvent, bool) {
	var buf [4]evtVariant
	var used, count uint32
	r, _, _ := procEvtRender.Call(ctx, h, evtRenderEventValues, uintptr(unsafe.Sizeof(buf)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&count)))
	if r == 0 || count < 4 {
		return rawEvent{}, false
	}
	var recID uint64
	if buf[3].typ == evtVarTypeUInt64 {
		recID = buf[3].val
	}
	out := rawEvent{Channel: channel, RecordID: recID}
	id, level, ts := buf[0], buf[1], buf[2]
	if id.typ != evtVarTypeUInt16 || ts.typ != evtVarTypeFileTime || ts.val == 0 {
		return out, false
	}
	if level.typ != evtVarTypeByte {
		return out, false // unreadable Level: missing, never 0
	}
	ft := windows.Filetime{LowDateTime: uint32(ts.val), HighDateTime: uint32(ts.val >> 32)}
	out.EventID = uint32(id.val)
	out.Level = uint8(level.val)
	out.T = time.Unix(0, ft.Nanoseconds())
	return out, true
}
