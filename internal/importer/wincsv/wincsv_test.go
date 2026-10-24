package wincsv

import (
	"errors"
	"io"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

func readAll(t *testing.T, s string, lim importer.Limits) (string, error) {
	t.Helper()
	r, err := Prepare(strings.NewReader(s), MergeLimits(lim, importer.Limits{}))
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(r)
	return string(b), err
}

func TestMergeLimits(t *testing.T) {
	got := MergeLimits(importer.Limits{MaxRows: 7}, importer.Limits{MaxBytes: 100, MaxRows: 9})
	if got.MaxBytes != 100 || got.MaxRows != 7 || got.MaxLineBytes != DefaultMaxLineBytes ||
		got.MaxFields != DefaultMaxFields || got.MaxRejectFraction != DefaultRejectFrac {
		t.Errorf("%+v", got)
	}
}

func TestPrepareRefusals(t *testing.T) {
	for name, in := range map[string]string{
		"zip": "PK\x03\x04abc", "gzip": "\x1f\x8bxx", "nul": "a,b\x00c\n", "evtx": "ElfFile\x00zz",
	} {
		_, err := readAll(t, in, importer.Limits{})
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPrepareDecodesBOMs(t *testing.T) {
	u16 := "\xff\xfea\x00,\x00b\x00\n\x00"
	for name, in := range map[string]string{"plain": "a,b\n", "utf8 bom": "\xef\xbb\xbfa,b\n", "utf16le": u16, "utf16be": "\xfe\xff\x00a\x00,\x00b\x00\n"} {
		got, err := readAll(t, in, importer.Limits{})
		if err != nil || got != "a,b\n" {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
}

func TestGuards(t *testing.T) {
	_, err := readAll(t, strings.Repeat("x", 50)+"\n", importer.Limits{MaxLineBytes: 10})
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "line" {
		t.Errorf("long line: %v", err)
	}
	// An unterminated quote must not pull the rest of the file into one record.
	_, err = readAll(t, "\"open\n"+strings.Repeat("yyyyyyyy\n", 100), importer.Limits{MaxLineBytes: 10})
	if !errors.As(err, &le) || le.Limit != "record" {
		t.Errorf("unterminated quote: %v", err)
	}
	// A legitimate multi-line quoted cell passes.
	if _, err = readAll(t, "\"a\nb\nc\",d\n", importer.Limits{MaxLineBytes: 10}); err != nil {
		t.Errorf("multi-line cell: %v", err)
	}
	_, err = readAll(t, strings.Repeat("z", 100), importer.Limits{MaxBytes: 50})
	if !errors.As(err, &le) || le.Limit != "bytes" {
		t.Errorf("bytes: %v", err)
	}
}

func TestHeadAndClip(t *testing.T) {
	if HeadText([]byte("\xef\xbb\xbfabc")) != "abc" || HeadText([]byte("\xff\xfea\x00b\x00")) != "ab" || HeadText([]byte("a\xffb")) != "ab" {
		t.Error("HeadText")
	}
	long := strings.Repeat("é", 200) // 400 bytes
	if c := ClipAttr(long); len(c) > 256 || len(c)%2 != 0 || ClipAttr("=x") != "=x" {
		t.Errorf("ClipAttr len %d", len(c))
	}
}

func TestAccFinish(t *testing.T) {
	a := NewAcc("x", MergeLimits(importer.Limits{}, importer.Limits{}))
	for i := 0; i < 100; i++ {
		_ = a.Row(int64(i))
	}
	for i := 0; i < 6; i++ {
		a.Reject(int64(i), importer.ReasonBadTime)
	}
	a.Observe("h2")
	a.Observe("h1")
	a.Gap("g")
	a.Gap("g")
	rep, err := a.Finish()
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonRejectFraction {
		t.Errorf("err %v", err)
	}
	if len(rep.KnownGaps) != 1 || len(rep.ObservedHosts) != 2 || rep.ObservedHosts[0] != "h1" {
		t.Errorf("%+v", rep)
	}
	b := NewAcc("x", importer.Limits{MaxRows: 2, MaxRejectFraction: 0.5})
	_ = b.Row(1)
	_ = b.Row(2)
	if err := b.Row(3); !errors.Is(err, importer.ErrLimit) {
		t.Errorf("row cap: %v", err)
	}
	for i := 0; i < 200; i++ {
		b.Reject(int64(i), importer.ReasonBadTime)
	}
	if len(b.Rep.Errors) != 100 {
		t.Errorf("errors kept %d", len(b.Rep.Errors))
	}
}
