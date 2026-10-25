package localread

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/iis"
)

const w3cHead = "#Software: Microsoft Internet Information Services 10.0\r\n" +
	"#Version: 1.0\r\n" +
	"#Date: 2001-09-11 08:00:00\r\n" +
	"#Fields: date time cs-method cs-uri-stem sc-status time-taken\r\n"

func w3cRows(from, n int) string {
	var sb strings.Builder
	for i := from; i < from+n; i++ {
		fmt.Fprintf(&sb, "2001-09-11 08:%02d:%02d GET /api/v1/orders/%d 200 %d\r\n", (i/60)%60, i%60, i, 5+i%7)
	}
	return sb.String()
}

func TestFrozenLengthOnGrowingFile(t *testing.T) {
	op := newMemOpener()
	f := op.add(testPath, testID, "one\ntwo\n")
	rc, info, err := OpenWith(op, testPath, testID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if info.Size != 8 {
		t.Fatalf("size %d", info.Size)
	}
	f.append("three\nfour\n") // the application keeps logging while we read
	if got := readAll(t, rc); got != "one\ntwo\n" {
		t.Fatalf("frozen read got %q", got)
	}
}

func TestFrozenReaderReportsShrink(t *testing.T) {
	op := newMemOpener()
	f := op.add(testPath, testID, "one\ntwo\nthree\n")
	rc, _, err := OpenWith(op, testPath, testID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	f.set("one\n")
	if _, err := io.ReadAll(rc); !errors.Is(err, ErrFileShrank) {
		t.Fatalf("want ErrFileShrank, got %v", err)
	}
}

func TestOpenRefusesZeroExpectAndSwap(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "x\n")
	if _, _, err := OpenWith(op, testPath, FileID{}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("zero expect: %v", err)
	}
	if _, _, err := OpenWith(op, testPath, FileID{VolumeSerial: testID.VolumeSerial, FileIndex: 43}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("swap: %v", err)
	}
	info, err := IdentifyWith(op, testPath)
	if err != nil || info.ID != testID {
		t.Fatalf("identify: %+v %v", info, err)
	}
	if _, err := OpenRange(context.Background(), op, testPath, FileID{}, RangeRequest{}, RangeOptions{Limiter: noLimit}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("OpenRange zero expect: %v", err)
	}
	if _, err := OpenRange(context.Background(), op, testPath, FileID{FileIndex: 1}, RangeRequest{}, RangeOptions{Limiter: noLimit}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("OpenRange swap: %v", err)
	}
}

func openR(t *testing.T, op Opener, req RangeRequest) *RangeReader {
	t.Helper()
	r, err := OpenRange(context.Background(), op, testPath, testID, req, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRangeEndsAtLastFullLine(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "alpha\nbeta\ngam") // last line still being written
	r := openR(t, op, RangeRequest{})
	defer r.Close()
	if got := readAll(t, r); got != "alpha\nbeta\n" {
		t.Fatalf("got %q", got)
	}
	if r.Meta.End != 11 || r.Meta.Start != 0 || r.Meta.Empty {
		t.Fatalf("meta %+v", r.Meta)
	}
}

func TestRangeExplicitEndSnapsBack(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "alpha\nbeta\ngamma\n")
	r := openR(t, op, RangeRequest{Offset: 6, End: 14}) // end is inside "gamma"
	defer r.Close()
	if got := readAll(t, r); got != "beta\n" {
		t.Fatalf("got %q", got)
	}
	if r.Meta.End != 11 {
		t.Fatalf("end %d", r.Meta.End)
	}
}

func TestRangeWithoutFullLineIsEmpty(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "no newline yet")
	r := openR(t, op, RangeRequest{})
	defer r.Close()
	if got := readAll(t, r); got != "" || !r.Meta.Empty || r.Meta.End != 0 {
		t.Fatalf("got %q meta %+v", got, r.Meta)
	}
}

func TestRangeMisalignedOffset(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "alpha\nbeta\n")
	_, err := OpenRange(context.Background(), op, testPath, testID, RangeRequest{Offset: 3}, RangeOptions{Limiter: noLimit})
	if !errors.Is(err, ErrMisaligned) {
		t.Fatalf("got %v", err)
	}
	_, err = OpenRange(context.Background(), op, testPath, testID, RangeRequest{Offset: 99}, RangeOptions{Limiter: noLimit})
	if !errors.Is(err, ErrBadRange) {
		t.Fatalf("got %v", err)
	}
}

func TestRangeHandlesCRLFAndLongLine(t *testing.T) {
	op := newMemOpener()
	long := strings.Repeat("x", 3*scanChunk)
	op.add(testPath, testID, "a\r\n"+long+"\r\npartial")
	r := openR(t, op, RangeRequest{})
	defer r.Close()
	if got := readAll(t, r); got != "a\r\n"+long+"\r\n" {
		t.Fatalf("len %d", len(got))
	}
}

func TestHeaderReplayW3CParses(t *testing.T) {
	op := newMemOpener()
	all := w3cHead + w3cRows(0, 100)
	f := op.add(testPath, testID, all)
	// First half, then the rest from the middle of the file.
	mid := len(w3cHead) + len(w3cRows(0, 40))
	r := openR(t, op, RangeRequest{Offset: int64(mid), Header: HeaderW3C})
	defer r.Close()
	body := readAll(t, r)
	if !strings.HasPrefix(body, "#Fields: date time") {
		t.Fatalf("range does not start with the replayed header: %.60q", body)
	}
	if r.Meta.Header == 0 || r.Meta.HeaderAt != int64(len(w3cHead)-len("#Fields: date time cs-method cs-uri-stem sc-status time-taken\r\n")) {
		t.Fatalf("meta %+v", r.Meta)
	}
	if r.Meta.Start != int64(mid) || r.Meta.End != int64(f.len()) {
		t.Fatalf("meta %+v", r.Meta)
	}
	evs, _, err := importer.Collect(context.Background(), iis.NewW3C(), strings.NewReader(body), importOpts())
	if err != nil {
		t.Fatalf("replayed range does not parse: %v", err)
	}
	if len(evs) != 60 {
		t.Fatalf("events %d, want 60", len(evs))
	}
	// Without the replay the raw tail has no #Fields and yields nothing.
	tail := all[mid:]
	evs, _, err = importer.Collect(context.Background(), iis.NewW3C(), strings.NewReader(tail), importOpts())
	if err == nil && len(evs) != 0 {
		t.Fatalf("headerless tail unexpectedly parsed %d events", len(evs))
	}
}

func importOpts() importer.Options {
	return importer.Options{
		CaseID: "abcdefgh234567abcdefgh2345", ImportNonce: "nonce-1", FileName: "u_ex010911.log",
		Host:         &importer.HostHint{Label: "web-a", Role: "iis", Runtime: "iis-10"},
		Pseudonymize: func(kind, v string) string { return kind + "-0001" },
	}
}

func TestHeaderReplayUsesMostRecentW3CHeader(t *testing.T) {
	op := newMemOpener()
	h2 := "#Fields: date time cs-uri-stem sc-status\r\n"
	all := w3cHead + w3cRows(0, 10) + "#Software: x\r\n" + h2 + "2001-09-11 09:00:00 /a 200\r\n2001-09-11 09:00:01 /b 200\r\n"
	op.add(testPath, testID, all)
	mid := strings.Index(all, "2001-09-11 09:00:01")
	r := openR(t, op, RangeRequest{Offset: int64(mid), Header: HeaderW3C})
	defer r.Close()
	if got := readAll(t, r); got != h2+"2001-09-11 09:00:01 /b 200\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestW3CHeaderFoundAcrossChunkBoundary(t *testing.T) {
	op := newMemOpener()
	hdr := "#Fields: x\n" // 11 bytes
	// Place the header so the backward chunk [offset-scanChunk, offset) cuts
	// through the "#Fields:" token three bytes in.
	p := 2 * 100 // even, line aligned
	offset := p + scanChunk + 3
	content := strings.Repeat("r\n", p/2) + hdr + strings.Repeat("r\n", (offset-p-len(hdr))/2) + strings.Repeat("t\n", 10)
	if (offset-p-len(hdr))%2 != 0 {
		t.Fatal("test arithmetic")
	}
	op.add(testPath, testID, content)
	r := openR(t, op, RangeRequest{Offset: int64(offset), Header: HeaderW3C})
	defer r.Close()
	if r.Meta.HeaderAt != int64(p) {
		t.Fatalf("header at %d, want %d", r.Meta.HeaderAt, p)
	}
	if got := readAll(t, r); !strings.HasPrefix(got, hdr+"t\n") {
		t.Fatalf("got %.30q", got)
	}
}

func TestW3CHeaderFallsBackToHeadPastScanLimit(t *testing.T) {
	op := newMemOpener()
	content := w3cHead + w3cRows(0, 3000)
	op.add(testPath, testID, content)
	mid := len(w3cHead) + len(w3cRows(0, 2500))
	r, err := OpenRange(context.Background(), op, testPath, testID,
		RangeRequest{Offset: int64(mid), Header: HeaderW3C}, RangeOptions{Limiter: noLimit, HeaderScanLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Meta.Header == 0 || !strings.HasPrefix(readAll(t, r), "#Fields:") {
		t.Fatalf("meta %+v", r.Meta)
	}
}

func TestW3CHeaderMissing(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "a\nb\nc\nd\n")
	_, err := OpenRange(context.Background(), op, testPath, testID, RangeRequest{Offset: 4, Header: HeaderW3C}, RangeOptions{Limiter: noLimit})
	if !errors.Is(err, ErrNoHeader) {
		t.Fatalf("got %v", err)
	}
}

func TestHeaderReplayCSVParses(t *testing.T) {
	op := newMemOpener()
	var sb strings.Builder
	sb.WriteString("\"Time\",\"Level\",\"Id\"\r\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "\"2001-09-11T08:00:%02dZ\",\"Error\",\"%d\"\r\n", i, 1000+i)
	}
	all := sb.String()
	op.add(testPath, testID, all)
	mid := strings.Index(all, "\"2001-09-11T08:00:20Z\"")
	r := openR(t, op, RangeRequest{Offset: int64(mid), Header: HeaderCSV})
	defer r.Close()
	recs, err := csv.NewReader(r).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	if len(recs) != 1+30 || recs[0][0] != "Time" || recs[1][2] != "1020" {
		t.Fatalf("records %d first %v second %v", len(recs), recs[0], recs[1])
	}
}

func TestHeaderNotReplayedAtOffsetZeroOrEmptyRange(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, w3cHead+w3cRows(0, 3))
	r := openR(t, op, RangeRequest{Header: HeaderW3C})
	if r.Meta.Header != 0 {
		t.Fatalf("header replayed at offset 0: %+v", r.Meta)
	}
	readAll(t, r)
	r.Close()
	// Nothing new past the offset: no header either.
	size := int64(len(w3cHead + w3cRows(0, 3)))
	r = openR(t, op, RangeRequest{Offset: size, Header: HeaderW3C})
	defer r.Close()
	if got := readAll(t, r); got != "" || !r.Meta.Empty {
		t.Fatalf("got %q %+v", got, r.Meta)
	}
}

func TestRangeReaderChargesLimiterInBufferSizedReads(t *testing.T) {
	op := newMemOpener()
	data := strings.Repeat("0123456789abcde\n", 64<<10) // 1 MiB
	op.add(testPath, testID, data+data+data)
	lim := &countLimiter{}
	r, err := OpenRange(context.Background(), op, testPath, testID, RangeRequest{}, RangeOptions{Limiter: lim, BufferSize: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	n, err := io.Copy(io.Discard, r)
	if err != nil || n != int64(3*len(data)) {
		t.Fatalf("copied %d err %v", n, err)
	}
	if lim.max > 64<<10 {
		t.Fatalf("a single read of %d bytes exceeded the buffer", lim.max)
	}
	if lim.total < int(n) { // plus a few bytes for the end scan and head hash
		t.Fatalf("limiter saw %d of %d bytes", lim.total, n)
	}
}

func TestRangeCancelledByContext(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, strings.Repeat("line\n", 100000))
	ctx, cancel := context.WithCancel(context.Background())
	clk := newFakeClock()
	tb := NewTokenBucket(1024, 1024, clk)
	r, err := OpenRange(ctx, op, testPath, testID, RangeRequest{}, RangeOptions{Limiter: tb, BufferSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	buf := make([]byte, 100)
	if _, err := r.Read(buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := io.ReadAll(r); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
