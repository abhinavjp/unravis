package localread

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"
)

// ErrNoTimestamps means a window was asked for but no line in the head of the
// file has a time the importer can read, so the file cannot be sliced by time.
// The caller reports it as a refusal with this reason (never a path).
var ErrNoTimestamps = errors.New("localread: no line with a readable timestamp, the window cannot be applied")

// GapPartialWindow is the gap code for a file that was too large for the
// importer cap and could not be sliced exactly: only its tail, or the tail of
// the window slice, was imported.
const GapPartialWindow = "partial-window"

// Window outcomes reported in RangeInfo.Window.
const (
	// WindowAll: no time filtering was applied (open window, or no prober on a
	// file that fits the cap).
	WindowAll = "all"
	// WindowSlice: the range is the slice of lines inside the window.
	WindowSlice = "slice"
	// WindowBefore: every line of the file is older than the window.
	WindowBefore = "before"
	// WindowAfter: every line of the file is newer than the window.
	WindowAfter = "after"
	// WindowTail: the file is over the cap and was cut to its tail (GapPartialWindow).
	WindowTail = "tail"
)

const (
	// MaxProbes bounds the file reads the time search may make in total
	// (spec 4.3: at most 64 probes to find a window).
	MaxProbes = 64
	// DefaultSkew widens the window on both sides for the search, so a line
	// logged by a clock a few minutes off, or out of order, is not cut. The
	// importer's own Options.Window then trims to the exact window.
	DefaultSkew = 15 * time.Minute

	probeChunk   = 16 << 10  // bytes read at a search midpoint
	probeBig     = 256 << 10 // second try when a chunk holds no timed line
	edgeChunk    = 64 << 10  // bytes read at the head and the tail
	edgeBig      = 1 << 20   // second try at the head or the tail
	finalMaxSpan = 1 << 20   // longest residual interval scanned linearly
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// errProbeBudget is internal: the probe budget is spent.
var errProbeBudget = errors.New("localread: probe budget spent")

// LineTime reads the time of one physical line (no line break, no BOM). It is
// importer.TimeProber.ProbeTime bound to the import options; see
// importer.ProbeFunc. It must report false for a line with no time of its own.
type LineTime func(line []byte) (time.Time, bool)

// WindowSpec asks for the lines of a file inside the half-open window
// [From, To) (an unset bound is open) without reading the whole file.
type WindowSpec struct {
	From, To time.Time
	// Probe reads a line's time. Nil means the importer has no prober: a file
	// over Cap is then imported from its tail with GapPartialWindow.
	Probe LineTime
	// Cap is the importer's byte cap (Limits.MaxBytes); 0 means no cap. A
	// range larger than Cap is cut to its last Cap bytes, line aligned, and
	// reported as GapPartialWindow.
	Cap int64
	// Skew widens the window for the search; 0 means DefaultSkew, negative
	// means none.
	Skew time.Duration
}

func (w *WindowSpec) skew() time.Duration {
	switch {
	case w.Skew == 0:
		return DefaultSkew
	case w.Skew < 0:
		return 0
	}
	return w.Skew
}

// prober does the bounded reads of the time search.
type prober struct {
	ctx   context.Context
	lim   Limiter
	src   Source
	probe LineTime
	reads int   // probes made (budgeted reads)
	bytes int64 // every byte read by the search, probes and snaps
}

// read is a budgeted probe.
func (p *prober) read(off int64, n int) ([]byte, error) {
	if p.reads >= MaxProbes {
		return nil, errProbeBudget
	}
	p.reads++
	return p.readFree(off, n)
}

// readFree is an unbudgeted but counted read (line snapping, bounded by
// maxLineScan).
func (p *prober) readFree(off int64, n int) ([]byte, error) {
	p.bytes += int64(n)
	return readRegion(p.ctx, p.lim, p.src, off, n)
}

// lineTime reads the time of the line buf[from:to] (line break excluded) that
// starts at file offset abs.
func (p *prober) lineTime(line []byte, abs int64) (time.Time, bool) {
	if abs == 0 {
		line = bytes.TrimPrefix(line, utf8BOM)
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) == 0 {
		return time.Time{}, false
	}
	return p.probe(line)
}

// eachLine calls fn for every complete line of buf (one that ends with '\n'),
// buf[0] being file offset base. fn gets the line start and end (past the
// break) and its time; it returns false to stop.
func (p *prober) eachLine(buf []byte, base int64, fn func(start, end int64, t time.Time, ok bool) bool) {
	pos := 0
	for pos < len(buf) {
		i := bytes.IndexByte(buf[pos:], '\n')
		if i < 0 {
			return
		}
		line := buf[pos : pos+i]
		t, ok := p.lineTime(line, base+int64(pos))
		if !fn(base+int64(pos), base+int64(pos+i+1), t, ok) {
			return
		}
		pos += i + 1
	}
}

// firstTime is the time of the first timed line in [start, end).
func (p *prober) firstTime(start, end int64) (t time.Time, ok bool, err error) {
	for _, n := range []int64{edgeChunk, edgeBig} {
		size := min(n, end-start)
		buf, err := p.read(start, int(size))
		if err != nil {
			return time.Time{}, false, err
		}
		p.eachLine(buf, start, func(_, _ int64, lt time.Time, lok bool) bool {
			if lok {
				t, ok = lt, true
				return false
			}
			return true
		})
		if ok || size == end-start {
			return t, ok, nil
		}
	}
	return time.Time{}, false, nil
}

// lastTime is the time of the last timed line in [start, end), end being the
// end of a full line. ok is false when the tail holds none (the caller then
// assumes the file reaches into the window).
func (p *prober) lastTime(start, end int64) (t time.Time, ok bool, err error) {
	for _, n := range []int64{edgeChunk, edgeBig} {
		size := min(n, end-start)
		off := end - size
		buf, err := p.read(off, int(size))
		if err != nil {
			return time.Time{}, false, err
		}
		base := 0
		if off > start { // the buffer starts inside a line: drop that fragment
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				base = len(buf)
			} else {
				base = i + 1
			}
		}
		data := buf[base:]
		for len(data) > 0 {
			body := data[:len(data)-1] // data ends with '\n'
			j := bytes.LastIndexByte(body, '\n')
			line := body[j+1:]
			abs := off + int64(base) + int64(j+1)
			if lt, lok := p.lineTime(line, abs); lok {
				return lt, true, nil
			}
			data = data[:j+1]
		}
		if size == end-start {
			break
		}
	}
	return time.Time{}, false, nil
}

// seek returns the start of the first timed line in [lo, hi] whose time is not
// before target, or hi when there is none. lo and hi are line starts (or the
// end of the last full line). Lines are assumed to be in time order; the
// caller widens target by the skew. unknownBefore says how a stretch with no
// readable time is treated: as older than target (true) or not older (false).
// When the probe budget is spent the answer is the conservative bound: lo for
// a lower search, hi for an upper one.
func (p *prober) seek(target time.Time, lo, hi int64, unknownBefore bool) (int64, error) {
	conservative := lo
	if unknownBefore {
		conservative = hi
	}
	for hi-lo > probeChunk {
		mid := lo + (hi-lo)/2
		moved := false
		for _, chunk := range []int64{probeChunk, probeBig} {
			n := min(chunk, hi-mid)
			buf, err := p.read(mid, int(n))
			if errors.Is(err, errProbeBudget) {
				return conservative, nil
			}
			if err != nil {
				return 0, err
			}
			nl := bytes.IndexByte(buf, '\n')
			if nl < 0 {
				continue // a line longer than the chunk: look further
			}
			s := mid + int64(nl) + 1 // first line start after mid
			var (
				found   bool
				foundAt int64
				foundEn int64
				foundT  time.Time
				lastEnd int64
			)
			p.eachLine(buf[nl+1:], s, func(st, en int64, t time.Time, ok bool) bool {
				lastEnd = en
				if ok {
					found, foundAt, foundEn, foundT = true, st, en, t
					return false
				}
				return true
			})
			switch {
			case found && !foundT.Before(target):
				hi = foundAt
				moved = true
			case found:
				lo = foundEn
				moved = true
			default:
				if chunk == probeChunk && mid+n < hi {
					continue // nothing timed yet: read a bigger chunk
				}
				// No timed line in all the lines this read could see: decide.
				if unknownBefore && lastEnd > lo {
					lo, moved = lastEnd, true
				} else if !unknownBefore && s < hi {
					hi, moved = s, true
				}
			}
			break
		}
		if !moved {
			break
		}
	}
	// The interval is small (or cannot be split): scan it.
	if hi <= lo {
		return lo, nil
	}
	if hi-lo > finalMaxSpan {
		return conservative, nil
	}
	buf, err := p.read(lo, int(hi-lo))
	if errors.Is(err, errProbeBudget) {
		return conservative, nil
	}
	if err != nil {
		return 0, err
	}
	res := hi
	p.eachLine(buf, lo, func(st, _ int64, t time.Time, ok bool) bool {
		if ok && !t.Before(target) {
			res = st
			return false
		}
		return true
	})
	return res, nil
}

// snapForward returns the smallest line start >= pos that is below limit, or
// limit when the rest of the range is one line. pos > 0.
func (p *prober) snapForward(pos, limit int64) (int64, error) {
	at := pos - 1
	for at < limit {
		if at-(pos-1) >= maxLineScan {
			return 0, ErrLineTooLong
		}
		n := min(int64(scanChunk), limit-at)
		buf, err := p.readFree(at, int(n))
		if err != nil {
			return 0, err
		}
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			return min(at+int64(i)+1, limit), nil
		}
		at += n
	}
	return limit, nil
}

// buildWindow serves the lines of the file inside req.Window.
func buildWindow(ctx context.Context, src Source, info Info, req RangeRequest, opts RangeOptions) (*RangeReader, error) {
	w := req.Window
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
	p := &prober{ctx: ctx, lim: lim, src: src, probe: w.Probe}
	fin := func(lo, hi int64, outcome, gap string, first, last time.Time) (*RangeReader, error) {
		var rr *RangeReader
		if hi <= lo {
			mark, err := headMark(ctx, lim, src, lo)
			if err != nil {
				return nil, err
			}
			rr = &RangeReader{src: src, mark: mark, r: bytes.NewReader(nil),
				Meta: RangeInfo{File: info, Start: lo, End: lo, HeaderAt: -1, Empty: true}}
		} else {
			var err error
			rr, err = buildPlain(ctx, src, info, RangeRequest{Offset: lo, End: hi, Header: req.Header}, opts)
			if err != nil {
				return nil, err
			}
		}
		rr.Meta.Window, rr.Meta.Gap = outcome, gap
		rr.Meta.Probes, rr.Meta.ProbeBytes = p.reads, p.bytes
		rr.Meta.FirstTime, rr.Meta.LastTime = first, last
		return rr, nil
	}
	if end == start {
		return fin(start, start, WindowAll, "", time.Time{}, time.Time{})
	}

	lo, hi := start, end
	outcome := WindowAll
	var first, last time.Time
	if w.Probe != nil && (!w.From.IsZero() || !w.To.IsZero()) {
		ft, fok, err := p.firstTime(start, end)
		if err != nil {
			return nil, err
		}
		if !fok {
			return nil, ErrNoTimestamps
		}
		lt, lok, err := p.lastTime(start, end)
		if err != nil {
			return nil, err
		}
		first = ft
		if lok {
			last = lt
		}
		skew := w.skew()
		from, to := w.From.Add(-skew), w.To.Add(skew)
		switch {
		case !w.From.IsZero() && lok && lt.Before(from):
			return fin(start, start, WindowBefore, "", first, last)
		case !w.To.IsZero() && !ft.Before(to):
			return fin(start, start, WindowAfter, "", first, last)
		}
		outcome = WindowSlice
		if !w.From.IsZero() && ft.Before(from) {
			if lo, err = p.seek(from, lo, hi, false); err != nil {
				return nil, err
			}
		}
		if !w.To.IsZero() && (!lok || !lt.Before(to)) {
			if hi, err = p.seek(to, lo, hi, true); err != nil {
				return nil, err
			}
		}
	}

	gap := ""
	if w.Cap > 0 && hi-lo > w.Cap {
		nlo, err := p.snapForward(hi-w.Cap, hi)
		if err != nil {
			return nil, err
		}
		lo, gap = nlo, GapPartialWindow
		if w.Probe == nil || outcome == WindowAll {
			outcome = WindowTail
		}
	}
	return fin(lo, hi, outcome, gap, first, last)
}

// NextWindow is Next restricted to a time window: it serves the part of the
// file after the stored watermark that lies inside win, cutting the file by
// binary search with win.Probe instead of reading it through. The slice never
// starts before the watermark, so successive pulls do not overlap; a file whose
// head hash changed, or that shrank, restarts at 0 exactly as in Next. After
// the bytes have been imported call Commit: the watermark moves to the end of
// the slice, which also marks the older lines skipped before the window as
// pulled. A pull that finds the file wholly before or after the window serves
// nothing and leaves the watermark where it was.
//
// A file with no readable timestamp is refused with ErrNoTimestamps.
func NextWindow(ctx context.Context, op Opener, store WatermarkStore, key FileKey, canonical string, expect FileID, header HeaderKind, win WindowSpec, opts RangeOptions) (*RangeReader, error) {
	if expect.IsZero() {
		return nil, unsafe("no expected file identity")
	}
	src, info, err := op.OpenSource(canonical, expect)
	if err != nil {
		return nil, err
	}
	rr, err := next(ctx, src, info, store, key, header, &win, opts)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	return rr, nil
}

var _ io.Reader = (*RangeReader)(nil)
