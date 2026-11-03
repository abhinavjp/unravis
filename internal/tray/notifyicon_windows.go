//go:build windows

package tray

import (
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Shell_NotifyIcon commands, flags and versions.
const (
	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4

	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	nifShowTip = 0x80

	notifyIconVersion4 = 4

	tipUnits = 128 // szTip capacity, including the terminating zero
)

// Icon loading constants.
const (
	imageIcon     = 1
	lrShared      = 0x8000
	smCxSmIcon    = 49
	smCySmIcon    = 50
	idiApplication = 32512
	exeIconRes    = 1
)

var (
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	user32               = windows.NewLazySystemDLL("user32.dll")
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
	procShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	procLoadImage        = user32.NewProc("LoadImageW")
	procLoadIcon         = user32.NewProc("LoadIconW")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

// notifyIconData mirrors NOTIFYICONDATAW (976 bytes on amd64).
type notifyIconData struct {
	cbSize            uint32
	hWnd              uintptr
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             uintptr
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          windows.GUID
	hBalloonIcon      uintptr
}

// fillTip writes tip into dst as zero-terminated UTF-16, keeping at most 127
// units and never leaving half of a surrogate pair at the end.
func fillTip(dst *[128]uint16, tip string) {
	*dst = [128]uint16{}
	units := utf16.Encode([]rune(tip))
	if len(units) > tipUnits-1 {
		units = units[:tipUnits-1]
		if last := units[len(units)-1]; last >= 0xD800 && last <= 0xDBFF {
			units = units[:len(units)-1]
		}
	}
	copy(dst[:], units)
}

func newNotifyIconData(hwnd windows.Handle, id uint32) notifyIconData {
	var d notifyIconData
	d.cbSize = uint32(unsafe.Sizeof(d))
	d.hWnd = uintptr(hwnd)
	d.uID = id
	return d
}

func shellNotify(cmd uintptr, d *notifyIconData) error {
	r, _, err := procShellNotifyIcon.Call(cmd, uintptr(unsafe.Pointer(d)))
	if r == 0 {
		return err
	}
	return nil
}

// addIcon adds the notification-area icon and asks for version-4 behaviour. A
// failed set-version is ignored.
func addIcon(hwnd windows.Handle, id uint32, callbackMsg uint32, icon windows.Handle, tip string) error {
	d := newNotifyIconData(hwnd, id)
	d.uFlags = nifMessage | nifIcon | nifTip | nifShowTip
	d.uCallbackMessage = callbackMsg
	d.hIcon = uintptr(icon)
	fillTip(&d.szTip, tip)
	if err := shellNotify(nimAdd, &d); err != nil {
		return fmt.Errorf("tray: add notification icon: %w", err)
	}
	v := newNotifyIconData(hwnd, id)
	v.uTimeoutOrVersion = notifyIconVersion4
	_ = shellNotify(nimSetVersion, &v)
	return nil
}

// modifyTip changes the tooltip of an existing icon.
func modifyTip(hwnd windows.Handle, id uint32, tip string) error {
	d := newNotifyIconData(hwnd, id)
	d.uFlags = nifTip | nifShowTip
	fillTip(&d.szTip, tip)
	if err := shellNotify(nimModify, &d); err != nil {
		return fmt.Errorf("tray: modify notification icon: %w", err)
	}
	return nil
}

// deleteIcon removes the icon; errors are ignored.
func deleteIcon(hwnd windows.Handle, id uint32) {
	d := newNotifyIconData(hwnd, id)
	_ = shellNotify(nimDelete, &d)
}

// exeIcon loads the small app icon (resource 1) from the running exe, falling
// back to the stock application icon.
func exeIcon() windows.Handle {
	if mod, _, _ := procGetModuleHandle.Call(0); mod != 0 {
		cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
		cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
		h, _, _ := procLoadImage.Call(mod, uintptr(exeIconRes), imageIcon, cx, cy, lrShared)
		if h != 0 {
			return windows.Handle(h)
		}
	}
	h, _, _ := procLoadIcon.Call(0, uintptr(idiApplication))
	return windows.Handle(h)
}
