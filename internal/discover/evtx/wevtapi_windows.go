//go:build windows

package evtx

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wevtapi is loaded lazily from the system folder, so a host without it fails
// at the first call and not at start-up.
var (
	modWevtapi = windows.NewLazySystemDLL("wevtapi.dll")

	procEvtOpenChannelEnum = modWevtapi.NewProc("EvtOpenChannelEnum")
	procEvtNextChannelPath = modWevtapi.NewProc("EvtNextChannelPath")
	procEvtQuery           = modWevtapi.NewProc("EvtQuery")
	procEvtNext            = modWevtapi.NewProc("EvtNext")
	procEvtCreateRenderCtx = modWevtapi.NewProc("EvtCreateRenderContext")
	procEvtRender          = modWevtapi.NewProc("EvtRender")
	procEvtClose           = modWevtapi.NewProc("EvtClose")
)

const (
	evtQueryChannelPath      = 0x1
	evtQueryForwardDirection = 0x100

	evtRenderContextValues = 0
	evtRenderContextSystem = 1
	evtRenderEventValues   = 0

	errorEvtChannelNotFound = syscall.Errno(15007)

	// maxChannels bounds one listing; a host has a few hundred.
	maxChannels = 5000
)

// mapErr turns a wevtapi error into one the scan contract knows. The text never
// carries a channel name or query.
func mapErr(op string, err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case windows.ERROR_ACCESS_DENIED, windows.ERROR_PRIVILEGE_NOT_HELD:
			return fmt.Errorf("evtx %s: %w", op, ErrAccessDenied)
		case errorEvtChannelNotFound, windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND:
			return fmt.Errorf("evtx %s: %w", op, fs.ErrNotExist)
		}
		return fmt.Errorf("evtx %s: system error %d", op, uintptr(errno))
	}
	return fmt.Errorf("evtx %s: %w", op, err)
}

func evtClose(h uintptr) {
	if h != 0 {
		procEvtClose.Call(h)
	}
}

// ListChannels returns the name of every event channel on this computer.
func ListChannels() ([]string, error) {
	h, _, e := procEvtOpenChannelEnum.Call(0, 0)
	if h == 0 {
		return nil, mapErr("enum", e)
	}
	defer evtClose(h)
	out := []string{}
	buf := make([]uint16, 256)
	for len(out) < maxChannels {
		var used uint32
		r, _, e := procEvtNextChannelPath.Call(h, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&used)))
		if r == 0 {
			switch e {
			case windows.ERROR_NO_MORE_ITEMS:
				return out, nil
			case windows.ERROR_INSUFFICIENT_BUFFER:
				if used == 0 || used > 4096 {
					return out, mapErr("enum", e)
				}
				buf = make([]uint16, used)
				continue
			}
			return out, mapErr("enum", e)
		}
		if used > uint32(len(buf)) {
			return out, mapErr("enum", windows.ERROR_INVALID_DATA)
		}
		out = append(out, windows.UTF16ToString(buf[:used]))
	}
	return out, nil
}

// CanRead opens a tiny query on channel and closes it again. Access denied
// comes back as an error wrapping fs.ErrPermission; a channel that does not
// exist wraps fs.ErrNotExist. No event is read.
func CanRead(channel string) error {
	if err := ValidateChannel(channel); err != nil {
		return err
	}
	h, err := evtQuery(channel, "*[System[EventRecordID>0]]")
	if err != nil {
		return err
	}
	evtClose(h)
	return nil
}

func evtQuery(channel, xpath string) (uintptr, error) {
	path, err := windows.UTF16PtrFromString(channel)
	if err != nil {
		return 0, ErrChannelRefused
	}
	q, err := windows.UTF16PtrFromString(xpath)
	if err != nil {
		return 0, ErrChannelRefused
	}
	h, _, e := procEvtQuery.Call(0, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(q)),
		evtQueryChannelPath|evtQueryForwardDirection)
	if h == 0 {
		return 0, mapErr("query", e)
	}
	return h, nil
}

func createRenderContext(paths []string, flags uintptr) (uintptr, error) {
	var ptrs []*uint16
	for _, p := range paths {
		u, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return 0, ErrChannelRefused
		}
		ptrs = append(ptrs, u)
	}
	var first uintptr
	if len(ptrs) > 0 {
		first = uintptr(unsafe.Pointer(&ptrs[0]))
	}
	h, _, e := procEvtCreateRenderCtx.Call(uintptr(len(ptrs)), first, flags)
	runtime.KeepAlive(ptrs) // first is a bare uintptr; keep the slice and its strings alive until the call returns
	if h == 0 {
		return 0, mapErr("render-context", e)
	}
	return h, nil
}

// renderValues fills *buf with the values of event under ctx. It returns the
// property count and the address the buffer had, which decodeSystem needs to
// turn string pointers into offsets.
func renderValues(ctx, event uintptr, buf *[]byte) (count int, base uint64, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		var used, props uint32
		r, _, e := procEvtRender.Call(ctx, event, evtRenderEventValues,
			uintptr(len(*buf)), uintptr(unsafe.Pointer(&(*buf)[0])),
			uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
		if r != 0 {
			return int(props), uint64(uintptr(unsafe.Pointer(&(*buf)[0]))), nil
		}
		if e == windows.ERROR_INSUFFICIENT_BUFFER && used > 0 && used <= 1<<20 {
			*buf = make([]byte, used)
			continue
		}
		return 0, 0, mapErr("render", e)
	}
	return 0, 0, mapErr("render", windows.ERROR_INSUFFICIENT_BUFFER)
}
