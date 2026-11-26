//go:build windows

package tray

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	gdi32                  = windows.NewLazySystemDLL("gdi32.dll")
	procGetIconInfo        = user32.NewProc("GetIconInfo")
	procCreateIconIndirect = user32.NewProc("CreateIconIndirect")
	procDestroyIcon        = user32.NewProc("DestroyIcon")
	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procGetDIBits          = gdi32.NewProc("GetDIBits")
	procCreateBitmap       = gdi32.NewProc("CreateBitmap")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procGetObject          = gdi32.NewProc("GetObjectW")
)

// iconInfo mirrors ICONINFO.
type iconInfo struct {
	fIcon    uint32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

// bitmapHeader mirrors BITMAPINFOHEADER.
type bitmapHeader struct {
	size          uint32
	width, height int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPels, yPels  int32
	clrUsed       uint32
	clrImportant  uint32
}

// bitmapObj mirrors BITMAP.
type bitmapObj struct {
	bmType       int32
	bmWidth      int32
	bmHeight     int32
	bmWidthBytes int32
	bmPlanes     uint16
	bmBitsPixel  uint16
	bmBits       uintptr
}

// makeDotIcon returns a copy of base with the attention dot painted on it, or 0 when the icon cannot be read. The caller owns the
// copy and frees it with destroyIcon; base is not touched.
func makeDotIcon(base windows.Handle) windows.Handle {
	var ii iconInfo
	if r, _, _ := procGetIconInfo.Call(uintptr(base), uintptr(unsafe.Pointer(&ii))); r == 0 {
		return 0
	}
	defer func() {
		for _, b := range []uintptr{ii.hbmMask, ii.hbmColor} {
			if b != 0 {
				procDeleteObject.Call(b)
			}
		}
	}()
	if ii.hbmColor == 0 { // a black-and-white icon: nothing sensible to paint on
		return 0
	}
	var bm bitmapObj
	if r, _, _ := procGetObject.Call(ii.hbmColor, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
		return 0
	}
	w, h := int(bm.bmWidth), int(bm.bmHeight)
	if w < 8 || h < 8 || w > 256 || h > 256 {
		return 0
	}
	pix := make([]byte, w*h*4)
	hdr := bitmapHeader{width: int32(w), height: -int32(h), planes: 1, bitCount: 32}
	hdr.size = uint32(unsafe.Sizeof(hdr))
	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return 0
	}
	got, _, _ := procGetDIBits.Call(dc, ii.hbmColor, 0, uintptr(h), uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&hdr)), 0)
	procReleaseDC.Call(0, dc)
	if got == 0 {
		return 0
	}
	opaqueIfNoAlpha(pix)
	paintDot(pix, w, h)
	colour, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 32, uintptr(unsafe.Pointer(&pix[0])))
	maskBits := make([]byte, ((w+15)/16)*2*h) // all zero: every pixel is drawn
	mask, _, _ := procCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	defer func() {
		for _, b := range []uintptr{colour, mask} {
			if b != 0 {
				procDeleteObject.Call(b)
			}
		}
	}()
	if colour == 0 || mask == 0 {
		return 0
	}
	out := iconInfo{fIcon: 1, hbmMask: mask, hbmColor: colour}
	h2, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&out)))
	return windows.Handle(h2)
}

// opaqueIfNoAlpha makes every pixel opaque when the image has no alpha at all (an older icon): otherwise it would come out invisible.
func opaqueIfNoAlpha(pix []byte) {
	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 0 {
			return
		}
	}
	for i := 3; i < len(pix); i += 4 {
		pix[i] = 255
	}
}

func destroyIcon(h windows.Handle) {
	if h != 0 {
		procDestroyIcon.Call(uintptr(h))
	}
}
