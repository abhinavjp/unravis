//go:build windows

package tray

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Menu flags and TrackPopupMenu options.
const (
	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfDisabled  = 0x0002
	mfChecked   = 0x0008
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmNoNotify    = 0x0080
	tpmReturnCmd   = 0x0100

	wmNull = 0x0000
)

var (
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenu          = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetMenuItemCount    = user32.NewProc("GetMenuItemCount")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procPostMessage         = user32.NewProc("PostMessageW")
)

// point mirrors the Win32 POINT structure.
type point struct{ X, Y int32 }

// menuFlags maps an item to AppendMenu flags.
func menuFlags(it Item) uint32 {
	if it.Separator {
		return mfSeparator
	}
	f := uint32(mfString)
	if it.Checked {
		f |= mfChecked
	}
	if it.Disabled {
		f |= mfGrayed | mfDisabled
	}
	return f
}

// buildMenu makes a popup menu with one entry per item. The caller owns the
// returned handle and must destroy it.
func buildMenu(items []Item) (windows.Handle, error) {
	h, _, err := procCreatePopupMenu.Call()
	if h == 0 {
		return 0, err
	}
	menu := windows.Handle(h)
	for _, it := range items {
		// p stays a typed pointer until the Call expression converts it, and
		// KeepAlive pins the UTF-16 buffer past the call, so the GC cannot free it
		// while AppendMenuW reads it. A separator has no text (nil, passed as 0).
		var p *uint16
		if !it.Separator {
			var perr error
			p, perr = windows.UTF16PtrFromString(it.Text)
			if perr != nil {
				destroyMenu(menu)
				return 0, perr
			}
		}
		r, _, aerr := procAppendMenu.Call(h, uintptr(menuFlags(it)), uintptr(it.ID), uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
		if r == 0 {
			destroyMenu(menu)
			return 0, aerr
		}
	}
	return menu, nil
}

func destroyMenu(menu windows.Handle) {
	procDestroyMenu.Call(uintptr(menu))
}

// menuItemCount returns the number of entries in the menu (-1 on error).
func menuItemCount(menu windows.Handle) int {
	r, _, _ := procGetMenuItemCount.Call(uintptr(menu))
	return int(int32(r))
}

// showMenu shows the items at the cursor and returns the chosen ID. ok is false
// when the items are invalid, the menu could not be built, or it was dismissed.
func showMenu(hwnd windows.Handle, items []Item) (id uint32, ok bool) {
	if err := ValidateItems(items); err != nil {
		return 0, false
	}
	menu, err := buildMenu(items)
	if err != nil {
		return 0, false
	}
	defer destroyMenu(menu)

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(hwnd))
	r, _, _ := procTrackPopupMenu.Call(uintptr(menu), tpmReturnCmd|tpmRightButton|tpmNoNotify,
		uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
	// Documented fix so the menu closes when the user clicks elsewhere.
	procPostMessage.Call(uintptr(hwnd), wmNull, 0, 0)
	if r == 0 {
		return 0, false
	}
	return uint32(r), true
}
