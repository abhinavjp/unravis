package tray

import (
	"strings"
	"testing"
)

func TestValidateNotice(t *testing.T) {
	ok := []Notice{
		{Title: "Unravis has been updated to 1.4.0"},
		{Title: "t", Text: "line one\nline two"},
		{Title: strings.Repeat("é", MaxNoticeTitleRunes), Text: strings.Repeat("é", MaxNoticeTextRunes)},
	}
	for i, n := range ok {
		if err := ValidateNotice(n); err != nil {
			t.Errorf("good notice %d rejected: %v", i, err)
		}
	}
	bad := map[string]Notice{
		"no title":         {Text: "x"},
		"long title":       {Title: strings.Repeat("a", MaxNoticeTitleRunes+1)},
		"long text":        {Title: "t", Text: strings.Repeat("a", MaxNoticeTextRunes+1)},
		"control in title": {Title: "a\nb"},
		"control in text":  {Title: "t", Text: "a\x00b"},
		"tab in text":      {Title: "t", Text: "a\tb"},
	}
	for name, n := range bad {
		if err := ValidateNotice(n); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPaintDotDrawsOnlyInTheBottomRightCorner(t *testing.T) {
	const w, h = 32, 32
	pix := make([]byte, w*h*4)
	paintDot(pix, w, h)
	at := func(x, y int) []byte { return pix[(y*w+x)*4 : (y*w+x)*4+4] }
	if c := at(0, 0); c[3] != 0 {
		t.Errorf("top-left pixel touched: %v", c)
	}
	c := at(w-5, h-5) // inside the disc
	if c[3] != 255 || c[2] < 0xC0 || c[0] > 0x40 {
		t.Errorf("disc centre = BGRA %v, want opaque orange-red", c)
	}
	// Outside the disc box nothing is drawn.
	for y := 0; y < h/2; y++ {
		for x := 0; x < w; x++ {
			if at(x, y)[3] != 0 {
				t.Fatalf("pixel (%d,%d) in the top half was drawn", x, y)
			}
		}
	}
}

func TestPaintDotIgnoresTinyOrShortBuffers(t *testing.T) {
	small := make([]byte, 4*4*4)
	paintDot(small, 4, 4)
	for _, b := range small {
		if b != 0 {
			t.Fatal("a 4x4 icon was drawn on")
		}
	}
	short := make([]byte, 10)
	paintDot(short, 32, 32) // must not panic
}

func TestHasAlpha(t *testing.T) {
	if hasAlpha(make([]byte, 16)) {
		t.Fatal("an all-zero image has no alpha")
	}
	px := make([]byte, 16)
	px[11] = 255
	if !hasAlpha(px) {
		t.Fatal("an image with one opaque pixel has alpha")
	}
}
