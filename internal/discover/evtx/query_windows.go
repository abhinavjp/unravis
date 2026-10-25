//go:build windows

package evtx

import (
	"context"
	"io"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	batchSize = 256
	// nextTimeoutMs bounds one EvtNext wait so a cancelled ctx is noticed.
	nextTimeoutMs = 1000

	appPoolPath = "Event/EventData/Data[@Name='AppPoolID']"
)

// queryChannel runs xpath on channel and writes every event as a CSV row. The
// render calls ask for System values, and for WAS events the AppPoolID value,
// and nothing else: EvtFormatMessage is never called.
func queryChannel(ctx context.Context, w io.Writer, channel, xpath string) (Result, error) {
	q, err := evtQuery(channel, xpath)
	if err != nil {
		return Result{}, err
	}
	defer evtClose(q)
	sysCtx, err := createRenderContext(nil, evtRenderContextSystem)
	if err != nil {
		return Result{}, err
	}
	defer evtClose(sysCtx)
	var poolCtx uintptr
	defer func() { evtClose(poolCtx) }()

	rw := NewRowWriter(w)
	res := Result{}
	finish := func(err error) (Result, error) {
		res.Rows, res.LastRecordID = rw.Rows(), rw.LastRecordID()
		if ferr := rw.Flush(); err == nil {
			err = ferr
		}
		return res, err
	}

	events := make([]uintptr, batchSize)
	buf := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return finish(err)
		}
		var returned uint32
		r, _, e := procEvtNext.Call(q, batchSize, uintptr(unsafe.Pointer(&events[0])), nextTimeoutMs, 0, uintptr(unsafe.Pointer(&returned)))
		if r == 0 {
			switch e {
			case windows.ERROR_NO_MORE_ITEMS:
				return finish(nil)
			case windows.ERROR_TIMEOUT:
				continue
			}
			return finish(mapErr("next", e))
		}
		var stop bool
		var failure error
		for i := uint32(0); i < returned; i++ {
			if stop || failure != nil {
				evtClose(events[i])
				continue
			}
			rec, ok := renderSystem(sysCtx, events[i], &buf)
			if !ok {
				res.Skipped++
				evtClose(events[i])
				continue
			}
			if IsWAS(rec.Provider) {
				if poolCtx == 0 {
					if poolCtx, err = createRenderContext([]string{appPoolPath}, evtRenderContextValues); err != nil {
						failure = err
						evtClose(events[i])
						continue
					}
				}
				if n, base, rerr := renderValues(poolCtx, events[i], &buf); rerr == nil {
					rec.AppPool = decodeAppPool(buf, base, n)
				}
			}
			evtClose(events[i])
			if err := rw.Write(rec); err != nil {
				if err == ErrRowCap {
					res.Truncated, stop = true, true
				} else {
					failure = err
				}
			}
		}
		if failure != nil {
			return finish(failure)
		}
		if stop {
			return finish(nil)
		}
	}
}

// renderSystem reads and decodes the System values of one event. ok is false
// when the event cannot be read; the caller counts it as skipped.
func renderSystem(sysCtx, event uintptr, buf *[]byte) (Record, bool) {
	n, base, err := renderValues(sysCtx, event, buf)
	if err != nil {
		return Record{}, false
	}
	rec, err := decodeSystem(*buf, base, n)
	if err != nil {
		return Record{}, false
	}
	return rec, true
}
