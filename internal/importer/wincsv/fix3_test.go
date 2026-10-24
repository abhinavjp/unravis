package wincsv

import (
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
)

func TestMaxEventsMergeAndCheck(t *testing.T) {
	got := MergeLimits(importer.Limits{}, importer.Limits{MaxEvents: 50})
	if got.MaxEvents != 50 {
		t.Errorf("default MaxEvents not applied: %+v", got)
	}
	if MergeLimits(importer.Limits{MaxEvents: 7}, importer.Limits{MaxEvents: 50}).MaxEvents != 7 {
		t.Error("caller MaxEvents overridden")
	}
	if MergeLimits(importer.Limits{MaxEvents: -1}, importer.Limits{MaxEvents: 50}).MaxEvents != -1 {
		t.Error("negative MaxEvents (no limit) not kept")
	}
	a := NewAcc("x", MergeLimits(importer.Limits{MaxEvents: 3}, importer.Limits{}))
	if err := a.CheckEmit(3); err != nil {
		t.Errorf("3 of 3: %v", err)
	}
	var le *importer.LimitError
	if err := a.CheckEmit(4); !errors.As(err, &le) || le.Limit != "events" || le.Max != 3 || le.At != 4 {
		t.Errorf("4 of 3: %v", err)
	}
	a.Emitted(time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))
	if err := a.CheckEmit(3); !errors.As(err, &le) {
		t.Errorf("emitted events must count: %v", err)
	}
	if err := NewAcc("x", MergeLimits(importer.Limits{MaxEvents: -1}, importer.Limits{})).CheckEmit(1 << 40); err != nil {
		t.Errorf("no limit: %v", err)
	}
}

func TestSaneTime(t *testing.T) {
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{
		{time.Date(2001, 10, 24, 0, 0, 0, 0, time.UTC), true},
		{time.Time{}, false},
		{time.Date(1601, 1, 1, 0, 0, 0, 0, time.UTC), false},
		{time.Date(2101, 1, 1, 0, 0, 0, 0, time.UTC), false},
	} {
		if SaneTime(tc.t) != tc.want {
			t.Errorf("SaneTime(%v) != %v", tc.t, tc.want)
		}
	}
}

func TestLooksLikeCultureTime(t *testing.T) {
	for cell, want := range map[string]bool{
		"3/1/2001 10:00:00 AM": true,
		"01/03/2001 10:00:00":  true,
		"01.03.2001 10:00:00":  true,
		"1/3/2001, 10:00":      true,
		"2001-03-01T10:00:00":  false,
		"garbage":              false,
		"":                     false,
	} {
		if LooksLikeCultureTime(cell) != want {
			t.Errorf("%q: want %v", cell, want)
		}
	}
}

func TestOpenCSVSkipTypeLine(t *testing.T) {
	enc16 := func(s string, be bool) string {
		out := []byte{0xFF, 0xFE}
		if be {
			out = []byte{0xFE, 0xFF}
		}
		for _, r := range s { // ASCII only
			if be {
				out = append(out, 0, byte(r))
			} else {
				out = append(out, byte(r), 0)
			}
		}
		return string(out)
	}
	body := "a,b\n#hash row,1\n2,3\n"
	lim := MergeLimits(importer.Limits{}, importer.Limits{})
	for name, in := range map[string]string{
		"lf":       "#TYPE Selected.Synthetic\n" + body,
		"crlf":     "#TYPE Selected.Synthetic\r\na,b\r\n#hash row,1\r\n2,3\r\n",
		"utf8 bom": "\xef\xbb\xbf#TYPE Selected.Synthetic\n" + body,
		"utf16le":  enc16("#TYPE Selected.Synthetic\n"+body, false),
		"utf16be":  enc16("#TYPE Selected.Synthetic\n"+body, true),
		"no #TYPE": body,
	} {
		cr, err := OpenCSVSkipTypeLine(strings.NewReader(in), lim)
		if err != nil || strings.Join(cr.Header(), "|") != "a|b" {
			t.Fatalf("%s: header %v err %v", name, cr, err)
		}
		// The row that starts with '#' is a data row, never a comment.
		rec, line, err := cr.Next()
		wantLine := int64(3)
		if name == "no #TYPE" {
			wantLine = 2
		}
		if err != nil || rec[0] != "#hash row" || line != wantLine {
			t.Errorf("%s: row %v line %d err %v", name, rec, line, err)
		}
		if rec, _, err = cr.Next(); err != nil || rec[0] != "2" {
			t.Errorf("%s: second row %v %v", name, rec, err)
		}
	}
	// Only a leading #TYPE line is skipped: a later one is data.
	cr, err := OpenCSVSkipTypeLine(strings.NewReader("a,b\n#TYPE x,1\n"), lim)
	if err != nil {
		t.Fatal(err)
	}
	if rec, _, err := cr.Next(); err != nil || rec[0] != "#TYPE x" {
		t.Errorf("later #TYPE line: %v %v", rec, err)
	}
	// Archives are still refused.
	if _, err := OpenCSVSkipTypeLine(strings.NewReader("PK\x03\x04abc"), lim); !errors.Is(err, importer.ErrQuarantine) {
		t.Errorf("zip: %v", err)
	}
}
