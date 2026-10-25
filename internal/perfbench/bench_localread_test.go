package perfbench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/discover/localread"
)

// countingOpener wraps an Opener and counts the bytes and calls of every ReadAt
// on the sources it opens: the bytes the process actually asked the OS for.
type countingOpener struct {
	inner localread.Opener
	bytes atomic.Int64
	calls atomic.Int64
}

func (c *countingOpener) OpenSource(canonical string, expect localread.FileID) (localread.Source, localread.Info, error) {
	src, info, err := c.inner.OpenSource(canonical, expect)
	if err != nil {
		return nil, info, err
	}
	return &countingSource{Source: src, c: c}, info, nil
}

type countingSource struct {
	localread.Source
	c *countingOpener
}

func (s *countingSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := s.Source.ReadAt(p, off)
	s.c.bytes.Add(int64(n))
	s.c.calls.Add(1)
	return n, err
}

type noLimit struct{}

func (noLimit) Wait(context.Context, int) error { return nil }

// TestBenchLocalRead reads windows of a 2 GiB synthetic W3C file through
// localread.OpenRange, the only reader discovery uses, and reports the bytes
// actually read from the file against the file size.
func TestBenchLocalRead(t *testing.T) {
	need(t)
	dir := benchDir(t)
	path := filepath.Join(dir, "u_ex011025.log")
	sizeMiB := int64(envInt("UNRAVIS_BENCH_BIG_MIB", 2048))
	size := sizeMiB << 20
	g0 := time.Now()
	marks, lines, err := genBigIIS(path, size, 100, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %d MiB, %d lines, %d marks in %s (not part of the measurement)", sizeMiB, lines, len(marks), time.Since(g0).Round(time.Millisecond))

	info, err := localread.Identify(path)
	if err != nil {
		if errors.Is(err, localread.ErrNotSupportedOS) {
			t.Skip("the local reader opens files on Windows only")
		}
		t.Fatalf("identify: %v", err)
	}
	t.Logf("file size %d bytes, id %+v", info.Size, info.ID)

	type window struct {
		name     string
		off, end int64
		lim      localread.Limiter
	}
	mid := marks[len(marks)/2]
	last := marks[len(marks)-1]
	windows := []window{
		{"64 MiB window at ~1 GiB, default 32 MiB/s limiter", mid, mid + 64<<20, nil},
		{"64 MiB window at ~1 GiB, no limiter", mid, mid + 64<<20, noLimit{}},
		{"1 MiB window at ~1 GiB, no limiter (header replay cost)", mid, mid + 1<<20, noLimit{}},
		{"tail window (last ~128 MiB), no limiter", last, 0, noLimit{}},
		{"window at the very start (offset 0), no limiter, 16 MiB", 0, 16 << 20, noLimit{}},
	}
	for _, w := range windows {
		op := &countingOpener{inner: localread.System()}
		m := startMeter("")
		rr, err := localread.OpenRange(context.Background(), op, path, info.ID,
			localread.RangeRequest{Offset: w.off, End: w.end, Header: localread.HeaderW3C},
			localread.RangeOptions{Limiter: w.lim})
		if err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		served, err := io.Copy(io.Discard, rr)
		if err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		meta := rr.Meta
		_ = rr.Close()
		meas := m.finish()
		read := op.bytes.Load()
		report(t, "localread "+w.name, meas, fmt.Sprintf("file=%d bytes served=%d bytes (of which header %d) read-from-file=%d bytes (%.3f%% of file; %.2fx served) readAt-calls=%d served-MiB/s=%.1f restart=%q headerAt=%d",
			info.Size, served, meta.Header, read, 100*float64(read)/float64(info.Size), float64(read)/float64(max(served, 1)), op.calls.Load(),
			float64(served)/(1<<20)/meas.Wall.Seconds(), meta.Restart, meta.HeaderAt))
		// The window must never cost more than a little over what it serves.
		if read > served+16<<20 {
			t.Errorf("%s: read %d bytes for %d served", w.name, read, served)
		}
	}
}
