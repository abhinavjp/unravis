package localread

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/iis"
)

const bomStr = "\xef\xbb\xbf" // UTF-8 byte order mark

var winBase = time.Date(2001, 9, 11, 8, 0, 0, 0, time.UTC)

// w3cProbe is the iis-w3c line prober, as the discovery code binds it.
func w3cProbe(t testing.TB) LineTime {
	t.Helper()
	f, ok := importer.ProbeFunc(iis.NewW3C(), importer.Options{})
	if !ok {
		t.Fatal("iis-w3c has no prober")
	}
	return LineTime(f)
}

// row is one W3C row i seconds after winBase.
func row(i int, eol string) string {
	return fmt.Sprintf("%s GET /SENTINEL-7f3a 200 %d%s", winBase.Add(time.Duration(i)*time.Second).Format("2006-01-02 15:04:05"), i, eol)
}

func rows(from, to int, eol string) string {
	var sb strings.Builder
	for i := from; i < to; i++ {
		sb.WriteString(row(i, eol))
	}
	return sb.String()
}

func at(sec int) time.Time { return winBase.Add(time.Duration(sec) * time.Second) }

// rowTimes parses the time of every row of body (header lines skipped).
func rowTimes(t *testing.T, body string) []time.Time {
	t.Helper()
	var out []time.Time
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSuffix(strings.TrimPrefix(l, bomStr), "\r")
		if len(l) < 19 || l[0] == '#' {
			continue
		}
		ts, err := time.Parse("2006-01-02 15:04:05", l[:19])
		if err != nil {
			t.Fatalf("bad row %q", l)
		}
		out = append(out, ts)
	}
	return out
}

func openW(t *testing.T, op Opener, req RangeRequest) *RangeReader {
	t.Helper()
	r, err := OpenRange(context.Background(), op, testPath, testID, req, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWindowInsideFile(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		op := newMemOpener()
		op.add(testPath, testID, rows(0, 20000, eol)) // 20000 rows, ~800 KB
		w := &WindowSpec{From: at(5000), To: at(6200), Probe: w3cProbe(t), Skew: -1}
		r := openW(t, op, RangeRequest{Window: w})
		body := readAll(t, r)
		r.Close()
		if r.Meta.Window != WindowSlice || r.Meta.Empty {
			t.Fatalf("meta %+v", r.Meta)
		}
		if body != rows(5000, 6200, eol) {
			ts := rowTimes(t, body)
			t.Fatalf("eol %q: got %d rows from %v to %v", eol, len(ts), ts[0], ts[len(ts)-1])
		}
		if r.Meta.Probes == 0 || r.Meta.Probes > MaxProbes {
			t.Fatalf("probes %d", r.Meta.Probes)
		}
		if r.Meta.End-r.Meta.Start != int64(len(body)) || r.Meta.Gap != "" {
			t.Fatalf("meta %+v", r.Meta)
		}
	}
}

func TestWindowDefaultSkewWidensTheSlice(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, rows(0, 20000, "\r\n"))
	w := &WindowSpec{From: at(8000), To: at(8600), Probe: w3cProbe(t)}
	r := openW(t, op, RangeRequest{Window: w})
	ts := rowTimes(t, readAll(t, r))
	r.Close()
	if len(ts) < 600 || len(ts) > 600+2*int(DefaultSkew/time.Second)+2 {
		t.Fatalf("rows %d", len(ts))
	}
	if ts[0].After(at(8000)) || !ts[len(ts)-1].After(at(8599)) {
		t.Fatalf("slice %v..%v does not cover the window", ts[0], ts[len(ts)-1])
	}
	if !ts[0].Equal(at(8000).Add(-DefaultSkew)) || !ts[len(ts)-1].Equal(at(8600).Add(DefaultSkew).Add(-time.Second)) {
		t.Fatalf("slice %v..%v is not the skew-widened window", ts[0], ts[len(ts)-1])
	}
}

func TestWindowCoversWholeFileWithoutSearching(t *testing.T) {
	op := newMemOpener()
	all := rows(0, 3000, "\r\n")
	op.add(testPath, testID, all)
	r := openW(t, op, RangeRequest{Window: &WindowSpec{From: at(-3600), To: at(99999), Probe: w3cProbe(t), Skew: -1}})
	defer r.Close()
	if got := readAll(t, r); got != all {
		t.Fatalf("got %d bytes, want the whole file", len(got))
	}
	if r.Meta.Probes > 2 {
		t.Fatalf("a window covering the file took %d probes", r.Meta.Probes)
	}
}

func TestWindowBeforeAndAfterFile(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, rows(1000, 5000, "\r\n"))
	p := w3cProbe(t)

	// The window ends before the file begins.
	r := openW(t, op, RangeRequest{Window: &WindowSpec{From: at(0), To: at(500), Probe: p, Skew: -1}})
	if got := readAll(t, r); got != "" || !r.Meta.Empty || r.Meta.Window != WindowAfter || r.Meta.End != 0 {
		t.Fatalf("got %q %+v", got, r.Meta)
	}
	r.Close()

	// The window begins after the file ends.
	r = openW(t, op, RangeRequest{Window: &WindowSpec{From: at(9000), To: at(9500), Probe: p, Skew: -1}})
	if got := readAll(t, r); got != "" || !r.Meta.Empty || r.Meta.Window != WindowBefore || r.Meta.End != 0 {
		t.Fatalf("got %q %+v", got, r.Meta)
	}
	r.Close()

	// Open ended windows: From only, To only.
	r = openW(t, op, RangeRequest{Window: &WindowSpec{From: at(9000), Probe: p, Skew: -1}})
	if !r.Meta.Empty || r.Meta.Window != WindowBefore {
		t.Fatalf("%+v", r.Meta)
	}
	r.Close()
	r = openW(t, op, RangeRequest{Window: &WindowSpec{To: at(10), Probe: p, Skew: -1}})
	if !r.Meta.Empty || r.Meta.Window != WindowAfter {
		t.Fatalf("%+v", r.Meta)
	}
	r.Close()

	// A window that falls in a hole between lines serves nothing.
	op2 := newMemOpener()
	op2.add(testPath, testID, rows(0, 100, "\n")+rows(5000, 5100, "\n"))
	r = openW(t, op2, RangeRequest{Window: &WindowSpec{From: at(1000), To: at(2000), Probe: p, Skew: -1}})
	if !r.Meta.Empty || r.Meta.Window != WindowSlice {
		t.Fatalf("%+v", r.Meta)
	}
	r.Close()
}

func TestWindowRefusedWhenFileHasNoTimestamps(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, strings.Repeat("this line has no time SENTINEL-7f3a\r\n", 5000))
	w := &WindowSpec{From: at(0), To: at(500), Probe: w3cProbe(t)}
	_, err := OpenRange(context.Background(), op, testPath, testID, RangeRequest{Window: w}, RangeOptions{Limiter: noLimit})
	if !errors.Is(err, ErrNoTimestamps) {
		t.Fatalf("got %v", err)
	}
	if s := err.Error(); strings.Contains(s, "SENTINEL") || strings.Contains(s, testPath) {
		t.Fatalf("error leaks content: %q", s)
	}
	// Directives only, then nothing readable.
	op.add(testPath, testID, "#Software: x\r\n#Fields: date time\r\n"+strings.Repeat("#Remark: SENTINEL-7f3a\r\n", 100))
	_, err = OpenRange(context.Background(), op, testPath, testID, RangeRequest{Window: w}, RangeOptions{Limiter: noLimit})
	if !errors.Is(err, ErrNoTimestamps) {
		t.Fatalf("got %v", err)
	}
	// An open window needs no time, so the same file is served whole.
	r := openW(t, op, RangeRequest{Window: &WindowSpec{Probe: w3cProbe(t)}})
	defer r.Close()
	if r.Meta.Window != WindowAll || r.Meta.Empty {
		t.Fatalf("%+v", r.Meta)
	}
}

func TestWindowCRLFAndBOMAtFileStart(t *testing.T) {
	op := newMemOpener()
	body := bomStr + rows(0, 5000, "\r\n") // BOM on the very first row
	op.add(testPath, testID, body)
	p := w3cProbe(t)
	// A window that starts after the first row must not refuse the file: the
	// first row is the only timed line the head probe can see.
	r := openW(t, op, RangeRequest{Window: &WindowSpec{From: at(2000), To: at(2100), Probe: p, Skew: -1}})
	got := readAll(t, r)
	r.Close()
	if got != rows(2000, 2100, "\r\n") {
		t.Fatalf("got %d rows", len(rowTimes(t, got)))
	}
	// A window covering the start serves the BOM with the first row.
	r = openW(t, op, RangeRequest{Window: &WindowSpec{From: at(-10), To: at(10), Probe: p, Skew: -1}})
	got = readAll(t, r)
	r.Close()
	if !strings.HasPrefix(got, bomStr) || strings.Count(got, "\n") != 10 {
		t.Fatalf("got %q", got[:40])
	}
	// Only the BOM line and directives before the first row.
	op.add(testPath, testID, bomStr+"#Software: x\r\n#Fields: date time\r\n"+rows(0, 3000, "\r\n"))
	r = openW(t, op, RangeRequest{Window: &WindowSpec{From: at(1000), To: at(1005), Probe: p, Skew: -1}})
	defer r.Close()
	if got := readAll(t, r); got != rows(1000, 1005, "\r\n") {
		t.Fatalf("got %q", got)
	}
}

func TestWindowTornLastLine(t *testing.T) {
	op := newMemOpener()
	// The last line has a full time and is not finished yet.
	torn := "2001-09-11 23:59:59 GET /SENTINEL-7f3a 20"
	op.add(testPath, testID, rows(0, 6000, "\r\n")+torn)
	p := w3cProbe(t)
	r := openW(t, op, RangeRequest{Window: &WindowSpec{From: at(5000), To: at(70000), Probe: p, Skew: -1}})
	got := readAll(t, r)
	r.Close()
	if got != rows(5000, 6000, "\r\n") || strings.Contains(got, "23:59:59") {
		t.Fatalf("torn line served: %d rows", len(rowTimes(t, got)))
	}
	// The torn line's time must not make the file look newer than it is.
	r = openW(t, op, RangeRequest{Window: &WindowSpec{From: at(20000), To: at(30000), Probe: p, Skew: -1}})
	defer r.Close()
	if !r.Meta.Empty || r.Meta.Window != WindowBefore {
		t.Fatalf("%+v", r.Meta)
	}
	if !r.Meta.LastTime.Equal(at(5999)) {
		t.Fatalf("last time %v", r.Meta.LastTime)
	}
}

func TestWindowClockSkewedLines(t *testing.T) {
	op := newMemOpener()
	var sb strings.Builder
	type rec struct {
		start, end int
		t          time.Time
	}
	var recs []rec
	for i := 0; i < 20000; i++ {
		ts := at(i)
		switch {
		case i%97 == 0 && i > 0:
			ts = ts.Add(-6 * time.Minute) // a clock that ran slow
		case i%131 == 0:
			ts = ts.Add(4 * time.Minute) // and one that ran fast
		}
		s := fmt.Sprintf("%s GET /SENTINEL-7f3a 200 %d\r\n", ts.Format("2006-01-02 15:04:05"), i)
		recs = append(recs, rec{sb.Len(), sb.Len() + len(s), ts})
		sb.WriteString(s)
	}
	op.add(testPath, testID, sb.String())
	p := w3cProbe(t)
	for _, win := range []struct{ from, to int }{{3000, 3300}, {9000, 12000}, {15000, 19990}, {50, 400}} {
		w := &WindowSpec{From: at(win.from), To: at(win.to), Probe: p}
		r := openW(t, op, RangeRequest{Window: w})
		readAll(t, r)
		r.Close()
		for _, rc := range recs {
			if !rc.t.Before(w.From) && rc.t.Before(w.To) && (int64(rc.start) < r.Meta.Start || int64(rc.end) > r.Meta.End) {
				t.Fatalf("window %v: row at %d (time %v) is inside the window but outside the slice [%d,%d)", win, rc.start, rc.t, r.Meta.Start, r.Meta.End)
			}
		}
	}
}

func TestWindowOnlyStartsAtOrAfterOffset(t *testing.T) {
	op := newMemOpener()
	all := rows(0, 10000, "\r\n")
	op.add(testPath, testID, all)
	off := int64(len(rows(0, 4000, "\r\n")))
	r := openW(t, op, RangeRequest{Offset: off, Window: &WindowSpec{From: at(1000), To: at(7000), Probe: w3cProbe(t), Skew: -1}})
	defer r.Close()
	if got := readAll(t, r); got != rows(4000, 7000, "\r\n") || r.Meta.Start != off {
		t.Fatalf("start %d rows %d", r.Meta.Start, len(rowTimes(t, got)))
	}
}

func TestWindowHeaderReplayParses(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, w3cHead+w3cRows(0, 3000))
	w := &WindowSpec{From: time.Date(2001, 9, 11, 8, 10, 0, 0, time.UTC), To: time.Date(2001, 9, 11, 8, 20, 0, 0, time.UTC), Probe: w3cProbe(t), Skew: -1}
	r := openW(t, op, RangeRequest{Header: HeaderW3C, Window: w})
	defer r.Close()
	body := readAll(t, r)
	if !strings.HasPrefix(body, "#Fields:") || r.Meta.Header == 0 {
		t.Fatalf("no header replayed: %+v", r.Meta)
	}
	evs, _, err := importer.Collect(context.Background(), iis.NewW3C(), strings.NewReader(body), importOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	for _, e := range evs {
		if e.Start.Before(w.From.Add(-time.Hour)) || e.Start.After(w.To.Add(time.Hour)) {
			t.Fatalf("event outside the slice: %v", e.Start)
		}
	}
}

func TestWindowWithoutProberImportsTailWithGap(t *testing.T) {
	op := newMemOpener()
	all := rows(0, 1000, "\r\n") // ~40 KB
	op.add(testPath, testID, all)
	w := &WindowSpec{From: at(100), To: at(200), Cap: 8 << 10}
	r := openW(t, op, RangeRequest{Window: w})
	got := readAll(t, r)
	r.Close()
	if r.Meta.Gap != GapPartialWindow || r.Meta.Window != WindowTail {
		t.Fatalf("%+v", r.Meta)
	}
	if len(got) == 0 || len(got) > 8<<10 || !strings.HasSuffix(all, got) || !strings.HasPrefix(got, "2001-") {
		t.Fatalf("tail %d bytes %.30q", len(got), got)
	}
	if r.Meta.End != int64(len(all)) || r.Meta.Probes != 0 {
		t.Fatalf("%+v", r.Meta)
	}
	// Fits the cap: the whole file, no gap.
	w.Cap = 1 << 20
	r = openW(t, op, RangeRequest{Window: w})
	defer r.Close()
	if got := readAll(t, r); got != all || r.Meta.Gap != "" || r.Meta.Window != WindowAll {
		t.Fatalf("%+v", r.Meta)
	}
}

func TestWindowSliceOverCapKeepsItsTail(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, rows(0, 20000, "\r\n"))
	w := &WindowSpec{From: at(2000), To: at(12000), Probe: w3cProbe(t), Skew: -1, Cap: 64 << 10}
	r := openW(t, op, RangeRequest{Window: w})
	defer r.Close()
	got := readAll(t, r)
	if r.Meta.Gap != GapPartialWindow || len(got) > 64<<10 || len(got) < 60<<10 {
		t.Fatalf("%d bytes %+v", len(got), r.Meta)
	}
	ts := rowTimes(t, got)
	if !ts[len(ts)-1].Equal(at(11999)) || ts[0].Before(at(2000)) {
		t.Fatalf("slice %v..%v", ts[0], ts[len(ts)-1])
	}
}

func TestWindowLongLineCapSnap(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, row(0, "\r\n")+strings.Repeat("x", 200<<10)+"\r\n")
	r := openW(t, op, RangeRequest{Window: &WindowSpec{Cap: 4 << 10}})
	defer r.Close()
	if got := readAll(t, r); got != "" || r.Meta.Gap != GapPartialWindow || !r.Meta.Empty {
		t.Fatalf("got %d bytes %+v", len(got), r.Meta)
	}
}

// A stretch of the file with no readable time (a run of junk lines) must not
// make the slice lose lines: the search falls back to the wider bound.
func TestWindowSurvivesUntimedStretch(t *testing.T) {
	op := newMemOpener()
	junk := strings.Repeat("junk line without a time SENTINEL-7f3a\r\n", 60000) // ~2.3 MB
	op.add(testPath, testID, rows(0, 4000, "\r\n")+junk+rows(4000, 9000, "\r\n"))
	w := &WindowSpec{From: at(6000), To: at(6500), Probe: w3cProbe(t), Skew: -1}
	r := openW(t, op, RangeRequest{Window: w})
	defer r.Close()
	got := readAll(t, r)
	if r.Meta.Probes > MaxProbes {
		t.Fatalf("probes %d", r.Meta.Probes)
	}
	if !strings.Contains(got, row(6000, "\r\n")) || !strings.Contains(got, row(6499, "\r\n")) {
		t.Fatalf("slice lost window rows: %d bytes", len(got))
	}
	// And a window inside the first timed block.
	r2 := openW(t, op, RangeRequest{Window: &WindowSpec{From: at(1000), To: at(1100), Probe: w3cProbe(t), Skew: -1}})
	defer r2.Close()
	got = readAll(t, r2)
	if !strings.Contains(got, row(1000, "\r\n")) || !strings.Contains(got, row(1099, "\r\n")) {
		t.Fatalf("slice lost window rows: %d bytes", len(got))
	}
}

// ---- huge file ----------------------------------------------------------------

const (
	hugeSize  = int64(40) << 30
	hugeLine  = 64
	hugeBase  = "2001-09-11 08:00:00"
	hugeLines = hugeSize / hugeLine
)

// hugeFile is a 40 GiB W3C log that exists only as a formula: line 0 is the
// #Fields directive, line i (i >= 1) is a row i-1 seconds after winBase, every
// line exactly 64 bytes with CRLF. It counts the bytes asked of it.
type hugeFile struct {
	mu    sync.Mutex
	bytes int64
	reads int
	max   int
	past  bool // a read went beyond the file
}

func hugeLineBytes(i int64) []byte {
	var s string
	if i == 0 {
		s = "#Fields: date time cs-method cs-uri-stem sc-status time-taken"
	} else {
		s = winBase.Add(time.Duration(i-1)*time.Second).Format("2006-01-02 15:04:05") + " GET /SENTINEL-7f3a 200 1"
	}
	b := make([]byte, hugeLine)
	for k := range b {
		b[k] = ' '
	}
	copy(b, s)
	b[hugeLine-2], b[hugeLine-1] = '\r', '\n'
	return b
}

func (h *hugeFile) ReadAt(p []byte, off int64) (int, error) {
	h.mu.Lock()
	h.bytes += int64(len(p))
	h.reads++
	if len(p) > h.max {
		h.max = len(p)
	}
	if off+int64(len(p)) > hugeSize {
		h.past = true
	}
	h.mu.Unlock()
	if off >= hugeSize {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off+int64(n) < hugeSize {
		pos := off + int64(n)
		line := hugeLineBytes(pos / hugeLine)
		n += copy(p[n:], line[pos%hugeLine:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (h *hugeFile) Close() error { return nil }

type hugeOpener struct{ f *hugeFile }

func (o hugeOpener) OpenSource(canonical string, expect FileID) (Source, Info, error) {
	return o.f, Info{ID: testID, Size: hugeSize, Base: "u_ex010911.log"}, nil
}

func TestWindowHugeFileReadsAreBounded(t *testing.T) {
	cases := []struct {
		name     string
		from, to int64 // seconds after winBase
		header   HeaderKind
	}{
		{"middle", 300_000_000, 300_000_600, HeaderNone},
		{"middle with header replay", 300_000_000, 300_000_600, HeaderW3C},
		{"near the start", 5000, 5600, HeaderNone},
		{"near the end", hugeLines - 4000, hugeLines - 3400, HeaderNone},
		{"open end", 500_000_000, 0, HeaderNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &hugeFile{}
			w := &WindowSpec{From: at(int(c.from)), Probe: w3cProbe(t), Skew: -1}
			if c.to != 0 {
				w.To = at(int(c.to))
			}
			r, err := OpenRange(context.Background(), hugeOpener{h}, testPath, testID, RangeRequest{Header: c.header, Window: w},
				RangeOptions{Limiter: noLimit, HeaderScanLimit: 128 << 10})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if r.Meta.Probes == 0 || r.Meta.Probes > MaxProbes {
				t.Fatalf("probes %d", r.Meta.Probes)
			}
			openBytes := h.bytes
			if openBytes > 3<<20 {
				t.Fatalf("opening a window of a 40 GiB file read %d bytes in %d reads", openBytes, h.reads)
			}
			if h.past {
				t.Fatal("a read went past the frozen length")
			}
			// Serve the slice (a few KB for a 10 minute window) and check it.
			if c.to != 0 {
				body, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				ts := rowTimes(t, string(body))
				if int64(len(ts)) != c.to-c.from {
					t.Fatalf("rows %d, want %d", len(ts), c.to-c.from)
				}
				if !ts[0].Equal(at(int(c.from))) || !ts[len(ts)-1].Equal(at(int(c.to)-1)) {
					t.Fatalf("slice %v..%v", ts[0], ts[len(ts)-1])
				}
				if c.header == HeaderW3C && !strings.HasPrefix(string(body), "#Fields:") {
					t.Fatal("header not replayed")
				}
			} else {
				if r.Meta.End != hugeSize {
					t.Fatalf("meta %+v", r.Meta)
				}
				if want := (c.from + 1) * hugeLine; r.Meta.Start != want {
					t.Fatalf("start %d, want %d", r.Meta.Start, want)
				}
			}
		})
	}
}

func TestWindowHugeFileBeforeAfterTakeFewProbes(t *testing.T) {
	h := &hugeFile{}
	p := w3cProbe(t)
	for _, w := range []*WindowSpec{
		{From: at(-100000), To: at(-50000), Probe: p, Skew: -1},
		{From: at(900_000_000), To: at(900_000_100), Probe: p, Skew: -1},
	} {
		r, err := OpenRange(context.Background(), hugeOpener{h}, testPath, testID, RangeRequest{Window: w}, RangeOptions{Limiter: noLimit})
		if err != nil {
			t.Fatal(err)
		}
		if !r.Meta.Empty || r.Meta.Probes > 4 {
			t.Fatalf("%+v", r.Meta)
		}
		r.Close()
	}
	if h.bytes > 1<<20 {
		t.Fatalf("read %d bytes", h.bytes)
	}
}

// ---- watermark: non-overlapping pulls --------------------------------------------

func TestNextWindowPullsDoNotOverlap(t *testing.T) {
	op := newMemOpener()
	f := op.add(testPath, testID, rows(0, 6000, "\r\n"))
	store := NewMemoryWatermarks()
	key := KeyFor(testID, []byte("k"), testPath)
	p := w3cProbe(t)
	w := WindowSpec{From: at(1000), Probe: p, Skew: -1}
	ctx := context.Background()
	pull := func() (string, *RangeReader) {
		r, err := NextWindow(ctx, op, store, key, testPath, testID, HeaderNone, w, RangeOptions{Limiter: noLimit})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		body := readAll(t, r)
		if err := r.Commit(store, time.Time{}); err != nil {
			t.Fatal(err)
		}
		return body, r
	}
	b1, r1 := pull()
	if b1 != rows(1000, 6000, "\r\n") || r1.Meta.Restart != RestartFirstScan {
		t.Fatalf("first pull: %d rows %+v", len(rowTimes(t, b1)), r1.Meta)
	}
	// Nothing new: nothing served, mark untouched.
	if b, r := pull(); b != "" || !r.Meta.Empty {
		t.Fatalf("second pull served %d bytes", len(b))
	}
	f.append(rows(6000, 6500, "\r\n"))
	if b, _ := pull(); b != rows(6000, 6500, "\r\n") {
		t.Fatalf("third pull: %d rows", len(rowTimes(t, b)))
	}
	// A bounded window consumed once is not served again, and a file grown
	// past it adds nothing.
	w2 := WindowSpec{From: at(100), To: at(200), Probe: p, Skew: -1}
	key2 := KeyFor(testID, []byte("k2"), testPath)
	r, err := NextWindow(ctx, op, store, key2, testPath, testID, HeaderNone, w2, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != rows(100, 200, "\r\n") {
		t.Fatalf("got %d rows", len(rowTimes(t, got)))
	}
	if err := r.Commit(store, time.Time{}); err != nil {
		t.Fatal(err)
	}
	r.Close()
	f.append(rows(6500, 7000, "\r\n"))
	r, err = NextWindow(ctx, op, store, key2, testPath, testID, HeaderNone, w2, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if got := readAll(t, r); got != "" || !r.Meta.Empty || r.Meta.Window != WindowAfter {
		t.Fatalf("window pulled twice: %d bytes %+v", len(got), r.Meta)
	}
}

func TestNextWindowRotatedAndTruncatedFileRestarts(t *testing.T) {
	op := newMemOpener()
	f := op.add(testPath, testID, rows(0, 6000, "\r\n"))
	store := NewMemoryWatermarks()
	key := KeyFor(testID, []byte("k"), testPath)
	w := WindowSpec{From: at(0), Probe: w3cProbe(t), Skew: -1}
	ctx := context.Background()
	pull := func() (string, *RangeReader) {
		r, err := NextWindow(ctx, op, store, key, testPath, testID, HeaderNone, w, RangeOptions{Limiter: noLimit})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		body := readAll(t, r)
		if err := r.Commit(store, time.Time{}); err != nil {
			t.Fatal(err)
		}
		return body, r
	}
	pull()

	// Rotation: same name, new content, longer than the old offset's file.
	f.set(rows(100000, 100010, "\r\n"))
	rotated := rows(100000, 100010, "\r\n")
	b, r := pull()
	if b != rotated || r.Meta.Restart != RestartTruncated {
		t.Fatalf("truncated: %q %+v", b, r.Meta)
	}

	// Rotation by a longer replacement: size alone does not show it, the head does.
	f.set(rows(200000, 200400, "\r\n"))
	b, r = pull()
	if b != rows(200000, 200400, "\r\n") || r.Meta.Restart != RestartFirstBlockChange {
		t.Fatalf("rotated: %d bytes %+v", len(b), r.Meta)
	}
	// And the window still applies after a restart.
	f.set(rows(0, 6000, "\r\n"))
	w.From = at(5900)
	if b, r := pull(); b != rows(5900, 6000, "\r\n") || r.Meta.Restart == "" {
		t.Fatalf("restart with a window: %d rows %+v", len(rowTimes(t, b)), r.Meta)
	}
}

func TestWindowRangeReaderCancelledByContext(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, rows(0, 20000, "\r\n"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenRange(ctx, op, testPath, testID, RangeRequest{Window: &WindowSpec{From: at(5), To: at(9000), Probe: w3cProbe(t)}},
		RangeOptions{Limiter: NewTokenBucket(1<<20, 1<<20, newFakeClock())})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
