package localread

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
)

// ErrBadRange means the requested offsets are inconsistent with the file.
var ErrBadRange = errors.New("localread: bad range")

// HeaderKind says which header a range that starts mid-file must replay so it
// parses on its own.
type HeaderKind int

const (
	// HeaderNone replays nothing (formats where every line stands alone).
	HeaderNone HeaderKind = iota
	// HeaderW3C replays the most recent `#Fields:` line before the offset.
	HeaderW3C
	// HeaderCSV replays the first line of the file.
	HeaderCSV
)

const (
	scanChunk       = 64 << 10
	maxHeaderLine   = 64 << 10
	maxLineScan     = 16 << 20
	defaultHdrLimit = 8 << 20
	fieldsPrefix    = "#Fields:"
)

// RangeRequest asks for the half-open byte range [Offset, End) of a file.
// Offset must be 0 or the start of a line (a watermark value). End 0, or past
// the frozen length, means the frozen length. The range actually served ends
// at the last full line before End.
type RangeRequest struct {
	Offset, End int64
	Header      HeaderKind
}

// RangeOptions tunes a range reader. The zero value is the production setting.
type RangeOptions struct {
	// Limiter paces reads; nil means DefaultLimiter (32 MiB/s, process wide).
	Limiter Limiter
	// BufferSize is the read buffer; 0 means 1 MiB.
	BufferSize int
	// HeaderScanLimit bounds the backward search for a W3C header; 0 means 8 MiB.
	// Past the limit the first `#Fields:` line in the file head is used.
	HeaderScanLimit int64
}

// RangeInfo describes the range a RangeReader serves.
type RangeInfo struct {
	File     Info
	Start    int64  // first byte served from the file
	End      int64  // one past the last byte served: the next watermark offset
	Header   int    // bytes of replayed header prepended before the file bytes
	HeaderAt int64  // file offset the replayed header came from, -1 if none
	Empty    bool   // no full line in the range: nothing is served
	Restart  string // why a watermark was ignored: "", "first-scan", "truncated", "first-block-changed", "misaligned"
}

// RangeReader serves an optional replayed header followed by the file bytes
// [Start, End). Header bytes are not part of the watermark.
type RangeReader struct {
	Meta RangeInfo

	r     io.Reader
	src   Source
	once  sync.Once
	cerr  error
	key   FileKey
	hasKy bool
	mark  Mark
}

func (r *RangeReader) Read(p []byte) (int, error) { return r.r.Read(p) }

// Close releases the file handle.
func (r *RangeReader) Close() error {
	r.once.Do(func() { r.cerr = r.src.Close() })
	return r.cerr
}

// OpenRange opens canonical (all Open checks, expect required) and serves the
// requested line-aligned range. Rate limiting applies to every file read.
// Call EnterBackground in the goroutine that consumes the reader.
func OpenRange(ctx context.Context, op Opener, canonical string, expect FileID, req RangeRequest, opts RangeOptions) (*RangeReader, error) {
	if expect.IsZero() {
		return nil, unsafe("no expected file identity")
	}
	src, info, err := op.OpenSource(canonical, expect)
	if err != nil {
		return nil, err
	}
	rr, err := buildRange(ctx, src, info, req, opts)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	return rr, nil
}

func (o RangeOptions) limiter() Limiter {
	if o.Limiter != nil {
		return o.Limiter
	}
	return DefaultLimiter()
}

func (o RangeOptions) buffer() int {
	if o.BufferSize > 0 {
		return o.BufferSize
	}
	return DefaultBuffer
}

func (o RangeOptions) headerLimit() int64 {
	if o.HeaderScanLimit > 0 {
		return o.HeaderScanLimit
	}
	return defaultHdrLimit
}

func buildRange(ctx context.Context, src Source, info Info, req RangeRequest, opts RangeOptions) (*RangeReader, error) {
	lim := opts.limiter()
	start, hardEnd := req.Offset, req.End
	if hardEnd <= 0 || hardEnd > info.Size {
		hardEnd = info.Size
	}
	if start < 0 || start > info.Size || hardEnd < start {
		return nil, ErrBadRange
	}
	if start > 0 {
		b, err := readRegion(ctx, lim, src, start-1, 1)
		if err != nil {
			return nil, err
		}
		if b[0] != '\n' {
			return nil, ErrMisaligned
		}
	}
	end, err := lastLineEnd(ctx, lim, src, start, hardEnd)
	if err != nil {
		return nil, err
	}
	meta := RangeInfo{File: info, Start: start, End: end, HeaderAt: -1, Empty: end == start}

	var header []byte
	if start > 0 && end > start && req.Header != HeaderNone {
		var at int64
		switch req.Header {
		case HeaderW3C:
			header, at, err = findW3CHeader(ctx, lim, src, info.Size, start, opts.headerLimit())
		case HeaderCSV:
			header, at, err = findCSVHeader(ctx, lim, src, start)
		default:
			err = ErrBadRange
		}
		if err != nil {
			return nil, err
		}
		meta.Header, meta.HeaderAt = len(header), at
	}

	mark, err := headMark(ctx, lim, src, end)
	if err != nil {
		return nil, err
	}

	body := io.Reader(bytes.NewReader(nil))
	if end > start {
		sec := io.NewSectionReader(src, start, end-start)
		body = bufio.NewReaderSize(&throttled{ctx: ctx, r: &exactReader{r: sec, want: end - start}, lim: lim, max: opts.buffer()}, opts.buffer())
	}
	rr := &RangeReader{Meta: meta, src: src, mark: mark}
	if len(header) > 0 {
		rr.r = io.MultiReader(bytes.NewReader(header), body)
	} else {
		rr.r = body
	}
	return rr, nil
}

// readRegion reads exactly n bytes at off through the limiter.
func readRegion(ctx context.Context, lim Limiter, src Source, off int64, n int) ([]byte, error) {
	buf := make([]byte, n)
	m, err := src.ReadAt(buf, off)
	if m < n {
		if err == nil || err == io.EOF {
			err = ErrFileShrank
		}
		return nil, err
	}
	if err := lim.Wait(ctx, m); err != nil {
		return nil, err
	}
	return buf, nil
}

// lastLineEnd returns the offset just after the last '\n' in [start, hardEnd),
// or start when the range holds no full line.
func lastLineEnd(ctx context.Context, lim Limiter, src Source, start, hardEnd int64) (int64, error) {
	hi := hardEnd
	for hi > start {
		if hardEnd-hi >= maxLineScan {
			return 0, ErrLineTooLong
		}
		lo := hi - scanChunk
		if lo < start {
			lo = start
		}
		buf, err := readRegion(ctx, lim, src, lo, int(hi-lo))
		if err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			return lo + int64(i) + 1, nil
		}
		hi = lo
	}
	return start, nil
}

// findCSVHeader returns the first line of the file, with its line break.
func findCSVHeader(ctx context.Context, lim Limiter, src Source, before int64) ([]byte, int64, error) {
	n := before
	if n > maxHeaderLine {
		n = maxHeaderLine
	}
	buf, err := readRegion(ctx, lim, src, 0, int(n))
	if err != nil {
		return nil, 0, err
	}
	i := bytes.IndexByte(buf, '\n')
	if i < 0 {
		return nil, 0, ErrNoHeader
	}
	return buf[:i+1], 0, nil
}

// findW3CHeader returns the last `#Fields:` line that starts before offset.
// It searches backwards from offset up to limit bytes, then falls back to the
// first such line in the head of the file.
func findW3CHeader(ctx context.Context, lim Limiter, src Source, size, offset, limit int64) ([]byte, int64, error) {
	pat := []byte(fieldsPrefix)
	hi := offset
	var scanned int64
	for hi > 0 && scanned < limit {
		lo := hi - scanChunk
		if lo < 0 {
			lo = 0
		}
		from := lo
		if from > 0 {
			from-- // one byte of context to tell a line start at lo
		}
		buf, err := readRegion(ctx, lim, src, from, int(hi-from))
		if err != nil {
			return nil, 0, err
		}
		if at, ok := lastLineStartWith(buf, from, pat); ok {
			return readLine(ctx, lim, src, at, offset)
		}
		scanned += hi - lo
		if lo == 0 {
			return nil, 0, ErrNoHeader
		}
		hi = lo + int64(len(pat)) - 1 // overlap so a prefix split across chunks is seen
	}
	// Fallback: the head of the file.
	n := offset
	if n > maxHeaderLine {
		n = maxHeaderLine
	}
	buf, err := readRegion(ctx, lim, src, 0, int(n))
	if err != nil {
		return nil, 0, err
	}
	if at, ok := firstLineStartWith(buf, pat); ok {
		return readLine(ctx, lim, src, at, offset)
	}
	return nil, 0, ErrNoHeader
}

// lastLineStartWith finds the last position p in buf (file offset base+p)
// where pat starts a line: p is 0 with base 0, or buf[p-1] is '\n'.
func lastLineStartWith(buf []byte, base int64, pat []byte) (int64, bool) {
	for end := len(buf); end >= len(pat); {
		i := bytes.LastIndex(buf[:end], pat)
		if i < 0 {
			return 0, false
		}
		if (i == 0 && base == 0) || (i > 0 && buf[i-1] == '\n') {
			return base + int64(i), true
		}
		end = i + len(pat) - 1
		if end < len(pat) {
			break
		}
	}
	return 0, false
}

func firstLineStartWith(buf []byte, pat []byte) (int64, bool) {
	off := 0
	for off+len(pat) <= len(buf) {
		i := bytes.Index(buf[off:], pat)
		if i < 0 {
			return 0, false
		}
		p := off + i
		if p == 0 || buf[p-1] == '\n' {
			return int64(p), true
		}
		off = p + 1
	}
	return 0, false
}

// readLine reads the line starting at at (it must end before limit) including
// its line break.
func readLine(ctx context.Context, lim Limiter, src Source, at, limit int64) ([]byte, int64, error) {
	n := limit - at
	if n > maxHeaderLine {
		n = maxHeaderLine
	}
	buf, err := readRegion(ctx, lim, src, at, int(n))
	if err != nil {
		return nil, 0, err
	}
	i := bytes.IndexByte(buf, '\n')
	if i < 0 {
		return nil, 0, ErrNoHeader
	}
	return buf[:i+1], at, nil
}

// headMark builds the watermark value for a range that ends at end: the end
// offset plus a SHA-256 over the first min(4 KiB, end) bytes of the file.
func headMark(ctx context.Context, lim Limiter, src Source, end int64) (Mark, error) {
	n := int(end)
	if end > HeadBlock {
		n = HeadBlock
	}
	m := Mark{Offset: end, HeadLen: n}
	if n == 0 {
		m.HeadSHA = sha256.Sum256(nil)
		return m, nil
	}
	buf, err := readRegion(ctx, lim, src, 0, n)
	if err != nil {
		return Mark{}, err
	}
	m.HeadSHA = sha256.Sum256(buf)
	return m, nil
}
