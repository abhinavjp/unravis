package wincsv

import (
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
)

func open(t *testing.T, s string, lim importer.Limits) (*textio.CSVReader, error) {
	t.Helper()
	return OpenCSV(strings.NewReader(s), MergeLimits(lim, importer.Limits{}), 0)
}

func TestMergeLimits(t *testing.T) {
	got := MergeLimits(importer.Limits{MaxRows: 7}, importer.Limits{MaxBytes: 100, MaxRows: 9})
	if got.MaxBytes != 100 || got.MaxRows != 7 || got.MaxLineBytes != DefaultMaxLineBytes ||
		got.MaxFields != DefaultMaxFields || got.MaxRejectFraction != DefaultRejectFrac {
		t.Errorf("%+v", got)
	}
	if MergeLimits(importer.Limits{MaxBytes: -1}, importer.Limits{MaxBytes: 5}).MaxBytes != -1 {
		t.Error("negative MaxBytes (no limit) not kept")
	}
}

func TestPrepareRefusals(t *testing.T) {
	for name, in := range map[string]struct {
		data   string
		reason importer.Reason
	}{
		"zip":  {"PK\x03\x04abc", importer.ReasonArchive},
		"gzip": {"\x1f\x8bxx", importer.ReasonArchive},
		"nul":  {"a,b\x00c\n", importer.ReasonUnsupportedFormat},
		"evtx": {"ElfFile\x00zz", importer.ReasonBinaryEVTX},
	} {
		_, err := open(t, in.data, importer.Limits{})
		var qe *importer.QuarantineError
		if !errors.As(err, &qe) || qe.Reason != in.reason {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOpenCSVDecodesBOMs(t *testing.T) {
	u16 := "\xff\xfea\x00,\x00b\x00\n\x00"
	for name, in := range map[string]string{"plain": "a,b\n", "utf8 bom": "\xef\xbb\xbfa,b\n", "utf16le": u16, "utf16be": "\xfe\xff\x00a\x00,\x00b\x00\n"} {
		cr, err := open(t, in, importer.Limits{})
		if err != nil || strings.Join(cr.Header(), "|") != "a|b" {
			t.Errorf("%s: %v %v", name, cr, err)
		}
	}
}

func TestOpenCSVEmpty(t *testing.T) {
	if _, err := open(t, "", importer.Limits{}); err != textio.ErrEmpty {
		t.Errorf("empty input: %v", err)
	}
}

func TestGuards(t *testing.T) {
	var le *importer.LimitError
	_, err := open(t, strings.Repeat("x", 50)+"\n", importer.Limits{MaxLineBytes: 10})
	if !errors.As(err, &le) || le.Limit != "line-bytes" {
		t.Errorf("long line: %v", err)
	}
	// An unterminated quote must not pull the rest of the file into one record.
	cr, err := open(t, "h\n\"open\n"+strings.Repeat("yyyyyyyy\n", 5000), importer.Limits{MaxLineBytes: 10})
	if err == nil {
		for err == nil {
			_, _, err = cr.Next()
		}
	}
	if !errors.As(err, &le) || le.Limit != "record-bytes" {
		t.Errorf("unterminated quote: %v", err)
	}
	// A legitimate multi-line quoted cell passes.
	cr, err = open(t, "h,i\n\"a\nb\nc\",d\n", importer.Limits{MaxLineBytes: 10})
	if err != nil {
		t.Fatalf("multi-line cell: %v", err)
	}
	if rec, _, err := cr.Next(); err != nil || len(rec) != 2 {
		t.Errorf("multi-line cell: %v %v", rec, err)
	}
	_, err = open(t, strings.Repeat("z", 100), importer.Limits{MaxBytes: 50})
	if !errors.As(err, &le) || le.Limit != "bytes" {
		t.Errorf("bytes: %v", err)
	}
	_, err = open(t, "a,b,c,d\n", importer.Limits{MaxFields: 3})
	if !errors.As(err, &le) || le.Limit != "fields" {
		t.Errorf("fields: %v", err)
	}
}

func TestRowReason(t *testing.T) {
	cr, err := open(t, "a,b\n1\n2,3\n", importer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	_, line, err := cr.Next()
	if r, ok := RowReason(err); !ok || r != importer.ReasonFieldCount || line != 2 {
		t.Errorf("short row: %v line %d", err, line)
	}
	if _, _, err = cr.Next(); err != nil {
		t.Errorf("next good row: %v", err)
	}
	if _, ok := RowReason(&importer.LimitError{}); ok {
		t.Error("a limit error is not a row reject")
	}
}

func TestErrorsAreFixed(t *testing.T) {
	var qe *importer.QuarantineError
	if err := TZUnlabelled(); !errors.Is(err, importer.ErrTimezoneUnlabelled) || !errors.As(err, &qe) || qe.Reason != importer.ReasonTZUnlabelled {
		t.Errorf("TZUnlabelled: %v", err)
	}
	if err := MissingColumn("Id"); !errors.As(err, &qe) || qe.Column != "Id" || !errors.Is(err, importer.ErrQuarantine) {
		t.Errorf("MissingColumn: %v", err)
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
	if a.Observe("h2") != nil || a.Observe("h1") != nil || a.Observe("") != nil || a.Observe("h1") != nil {
		t.Error("observe")
	}
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
	if len(b.Rep.Errors) != importer.MaxReportErrors {
		t.Errorf("errors kept %d", len(b.Rep.Errors))
	}
	// Too many distinct host names is a limit error, not silent truncation.
	c := NewAcc("x", importer.Limits{})
	var herr error
	for i := 0; i <= MaxObservedHosts && herr == nil; i++ {
		herr = c.Observe(strings.Repeat("h", 1) + string(rune('a'+i%26)) + strings.Repeat("x", i/26) + "y")
	}
	if !errors.Is(herr, importer.ErrLimit) {
		t.Errorf("host cap: %v", herr)
	}
}
