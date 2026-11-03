//go:build windows

package tray

import (
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func TestNotifyIconDataSize(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("size is only pinned on amd64")
	}
	if got := unsafe.Sizeof(notifyIconData{}); got != 976 {
		t.Fatalf("notifyIconData is %d bytes, want 976", got)
	}
}

func TestFillTipTruncatesTo127UTF16Units(t *testing.T) {
	var dst [128]uint16
	fillTip(&dst, strings.Repeat("a", 300))
	for i := 0; i < 127; i++ {
		if dst[i] == 0 {
			t.Fatalf("unit %d is zero, want non-zero", i)
		}
	}
	if dst[127] != 0 {
		t.Fatalf("unit 127 = %d, want 0", dst[127])
	}

	// An emoji (2 units) starting at index 126 would straddle the limit.
	fillTip(&dst, strings.Repeat("a", 126)+"\U0001F600")
	if dst[126] != 0 || dst[127] != 0 {
		t.Fatalf("straddling emoji kept: units 126,127 = %#x,%#x", dst[126], dst[127])
	}
	for i := 0; i < 126; i++ {
		if dst[i] != 'a' {
			t.Fatalf("unit %d = %#x, want 'a'", i, dst[i])
		}
	}
}

func TestFillTipShortStringIsZeroTerminated(t *testing.T) {
	var dst [128]uint16
	for i := range dst {
		dst[i] = 0xFFFF // stale content must be cleared
	}
	fillTip(&dst, "Unravis")
	want := []uint16{'U', 'n', 'r', 'a', 'v', 'i', 's'}
	for i, w := range want {
		if dst[i] != w {
			t.Fatalf("unit %d = %#x, want %#x", i, dst[i], w)
		}
	}
	for i := len(want); i < len(dst); i++ {
		if dst[i] != 0 {
			t.Fatalf("unit %d = %#x, want 0", i, dst[i])
		}
	}
}

func TestExeIconAlwaysReturnsAHandle(t *testing.T) {
	if h := exeIcon(); h == 0 {
		t.Fatal("exeIcon returned 0")
	}
}
