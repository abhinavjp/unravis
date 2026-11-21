//go:build windows

package setup

import "golang.org/x/sys/windows"

// ShowNotice shows a small message box. It is for the one case where an update nobody watched left the person without a running
// program, so there is no other place to tell them. The result is ignored.
func ShowNotice(title, text string) {
	t, err1 := windows.UTF16PtrFromString(title)
	c, err2 := windows.UTF16PtrFromString(text)
	if err1 != nil || err2 != nil {
		return
	}
	windows.MessageBox(0, c, t, windows.MB_OK|windows.MB_ICONWARNING|windows.MB_SETFOREGROUND)
}
