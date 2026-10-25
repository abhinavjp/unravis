package localread

import (
	"context"
	"strings"
	"testing"
	"time"
)

var wmKey = KeyFor(testID, []byte("case-pseudonym-key"), testPath)

func nextRange(t *testing.T, op Opener, st WatermarkStore, hdr HeaderKind) (*RangeReader, string) {
	t.Helper()
	r, err := Next(context.Background(), op, st, wmKey, testPath, testID, hdr, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return r, readAll(t, r)
}

func TestConsecutiveRangesNeverOverlapOrGap(t *testing.T) {
	op := newMemOpener()
	f := op.add(testPath, testID, w3cHead+w3cRows(0, 7))
	st := NewMemoryWatermarks()
	var got strings.Builder // file bytes only, replayed headers cut off
	var prevEnd int64
	for step := 0; step < 6; step++ {
		r, body := nextRange(t, op, st, HeaderW3C)
		if r.Meta.Start != prevEnd {
			t.Fatalf("step %d: range starts at %d, previous ended at %d", step, r.Meta.Start, prevEnd)
		}
		got.WriteString(body[r.Meta.Header:])
		if err := r.Commit(st, time.Date(2001, 9, 11, 8, 0, step, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		r.Close()
		prevEnd = r.Meta.End
		if step%2 == 0 {
			f.append(w3cRows(100+step*10, 5))
		} else {
			f.append("2001-09-11 09:00:00 GET /partial") // half-written line: waits for its newline
		}
		if step%2 == 1 {
			f.append(" 200 1\r\n")
		}
	}
	r, body := nextRange(t, op, st, HeaderW3C)
	got.WriteString(body[r.Meta.Header:])
	r.Close()
	if got.String() != string(f.data) {
		t.Fatalf("concatenated ranges differ from the file:\n got %d bytes\nwant %d bytes", got.Len(), len(f.data))
	}
}

func TestSecondScanOfUnchangedFileImportsNothing(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, w3cHead+w3cRows(0, 5))
	st := NewMemoryWatermarks()
	r, body := nextRange(t, op, st, HeaderW3C)
	if body == "" || r.Meta.Restart != RestartFirstScan {
		t.Fatalf("first: %q %+v", body, r.Meta)
	}
	r.Commit(st, time.Time{})
	r.Close()
	r, body = nextRange(t, op, st, HeaderW3C)
	defer r.Close()
	if body != "" || !r.Meta.Empty || r.Meta.Restart != "" {
		t.Fatalf("second scan: %q %+v", body, r.Meta)
	}
}

func TestUncommittedRangeIsServedAgain(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "a\nb\nc\n")
	st := NewMemoryWatermarks()
	r, first := nextRange(t, op, st, HeaderNone)
	r.Close() // import failed: no Commit
	r, again := nextRange(t, op, st, HeaderNone)
	r.Close()
	if first != "a\nb\nc\n" || again != first {
		t.Fatalf("%q then %q", first, again)
	}
}

func TestWatermarkRestartsOnTruncate(t *testing.T) {
	op := newMemOpener()
	f := op.add(testPath, testID, lines(50, "row-"))
	st := NewMemoryWatermarks()
	r, _ := nextRange(t, op, st, HeaderNone)
	r.Commit(st, time.Time{})
	r.Close()
	f.set("new-1\nnew-2\n") // truncated in place, same file ID, now shorter than the offset
	r, body := nextRange(t, op, st, HeaderNone)
	defer r.Close()
	if body != "new-1\nnew-2\n" || r.Meta.Restart != RestartTruncated || r.Meta.Start != 0 {
		t.Fatalf("%q %+v", body, r.Meta)
	}
}

func TestWatermarkRestartsOnRotateWithSameName(t *testing.T) {
	op := newMemOpener()
	f := op.add(testPath, testID, "old-log-line-1\nold-log-line-2\n")
	st := NewMemoryWatermarks()
	r, _ := nextRange(t, op, st, HeaderNone)
	r.Commit(st, time.Time{})
	r.Close()
	// Rotated: a different, longer file now lives under the same name and ID.
	f.set("fresh-line-0001\nfresh-line-0002\nfresh-line-0003\n")
	r, body := nextRange(t, op, st, HeaderNone)
	defer r.Close()
	if body != "fresh-line-0001\nfresh-line-0002\nfresh-line-0003\n" || r.Meta.Restart != RestartFirstBlockChange {
		t.Fatalf("%q %+v", body, r.Meta)
	}
}

func TestWatermarkSurvivesGrowthOfSmallFile(t *testing.T) {
	// Under 4 KiB the first-block hash must cover only what was imported, or
	// every append would look like a rotation and import the file twice.
	op := newMemOpener()
	f := op.add(testPath, testID, "l1\nl2\n")
	st := NewMemoryWatermarks()
	r, _ := nextRange(t, op, st, HeaderNone)
	r.Commit(st, time.Time{})
	r.Close()
	f.append("l3\nl4\n")
	r, body := nextRange(t, op, st, HeaderNone)
	defer r.Close()
	if body != "l3\nl4\n" || r.Meta.Restart != "" {
		t.Fatalf("%q %+v", body, r.Meta)
	}
}

func TestWatermarkRestartsWhenStoredOffsetIsMidLine(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "alpha\nbeta\n")
	st := NewMemoryWatermarks()
	m, err := headMark(context.Background(), noLimit, mustSource(t, op), 3)
	if err != nil {
		t.Fatal(err)
	}
	st.Put(wmKey, m) // offset 3 is inside "alpha"
	r, body := nextRange(t, op, st, HeaderNone)
	defer r.Close()
	if body != "alpha\nbeta\n" || r.Meta.Restart != RestartMisaligned {
		t.Fatalf("%q %+v", body, r.Meta)
	}
}

func mustSource(t *testing.T, op Opener) Source {
	t.Helper()
	s, _, err := op.OpenSource(testPath, FileID{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWatermarkKeysAreIndependentPerFile(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "a\n")
	other := `C:\inetpub\logs\LogFiles\W3SVC2\u_ex010911.log`
	idB := FileID{VolumeSerial: testID.VolumeSerial, FileIndex: 77}
	op.add(other, idB, "b\n")
	st := NewMemoryWatermarks()
	r, _ := nextRange(t, op, st, HeaderNone)
	r.Commit(st, time.Time{})
	r.Close()
	rb, err := Next(context.Background(), op, st, KeyFor(idB, []byte("case-pseudonym-key"), other), other, idB, HeaderNone, RangeOptions{Limiter: noLimit})
	if err != nil {
		t.Fatal(err)
	}
	defer rb.Close()
	if got := readAll(t, rb); got != "b\n" {
		t.Fatalf("second file served %q", got)
	}
	if st.Len() != 1 {
		t.Fatalf("store has %d marks", st.Len())
	}
}

func TestPathHMACIsKeyedAndCaseInsensitive(t *testing.T) {
	a := PathHMAC([]byte("k1"), `C:\Logs\A.log`)
	if a != PathHMAC([]byte("k1"), `c:\logs\a.LOG`) {
		t.Fatal("case must not change the key")
	}
	if a == PathHMAC([]byte("k2"), `C:\Logs\A.log`) {
		t.Fatal("different case keys must differ")
	}
	if a == PathHMAC([]byte("k1"), `C:\Logs\B.log`) {
		t.Fatal("different paths must differ")
	}
	if strings.Contains(string(a[:]), "Logs") {
		t.Fatal("path leaked into the hmac")
	}
}

func TestNextRequiresExpectedIdentity(t *testing.T) {
	op := newMemOpener()
	op.add(testPath, testID, "a\n")
	st := NewMemoryWatermarks()
	if _, err := Next(context.Background(), op, st, wmKey, testPath, FileID{}, HeaderNone, RangeOptions{Limiter: noLimit}); err == nil {
		t.Fatal("zero expect accepted")
	}
	if _, err := Next(context.Background(), op, st, wmKey, testPath, FileID{FileIndex: 9}, HeaderNone, RangeOptions{Limiter: noLimit}); err == nil {
		t.Fatal("swapped identity accepted")
	}
}
