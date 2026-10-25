package localread

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// memFile is an in-memory "file" that tests can grow, shrink and replace.
type memFile struct {
	mu   sync.Mutex
	data []byte
	id   FileID
}

func (f *memFile) append(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = append(f.data, s...)
}

func (f *memFile) set(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = []byte(s)
}

func (f *memFile) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

type memSource struct {
	f      *memFile
	closed bool
}

func (s *memSource) ReadAt(p []byte, off int64) (int, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	if off >= int64(len(s.f.data)) {
		return 0, io.EOF
	}
	n := copy(p, s.f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *memSource) Close() error { s.closed = true; return nil }

// memOpener implements Opener over memFiles keyed by canonical path. It makes
// the same identity check as the Windows opener.
type memOpener struct {
	mu    sync.Mutex
	files map[string]*memFile
	opens int
}

func newMemOpener() *memOpener { return &memOpener{files: map[string]*memFile{}} }

func (o *memOpener) add(path string, id FileID, content string) *memFile {
	o.mu.Lock()
	defer o.mu.Unlock()
	f := &memFile{data: []byte(content), id: id}
	o.files[path] = f
	return f
}

func (o *memOpener) OpenSource(canonical string, expect FileID) (Source, Info, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opens++
	if err := CheckShape(canonical); err != nil {
		return nil, Info{}, err
	}
	f, ok := o.files[canonical]
	if !ok {
		return nil, Info{}, ErrAccessDenied
	}
	if !expect.IsZero() && expect != f.id {
		return nil, Info{}, unsafe("file identity changed since the scan")
	}
	return &memSource{f: f}, Info{ID: f.id, Size: int64(f.len()), Base: baseName(canonical)}, nil
}

// fakeClock is a virtual clock: Sleep advances time instead of waiting.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
	// slept is the total virtual time slept.
	slept time.Duration
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2001, 9, 11, 8, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.slept += d
	c.mu.Unlock()
	return nil
}

// countLimiter records the bytes charged and never waits.
type countLimiter struct {
	mu    sync.Mutex
	total int
	calls int
	max   int
}

func (l *countLimiter) Wait(_ context.Context, n int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total += n
	l.calls++
	if n > l.max {
		l.max = n
	}
	return nil
}

var noLimit = &countLimiter{}

const testPath = `C:\inetpub\logs\LogFiles\W3SVC1\u_ex010911.log`

var testID = FileID{VolumeSerial: 0xABCD1234, FileIndex: 42}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func lines(n int, prefix string) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(prefix)
		sb.WriteString(string(rune('a' + i%26)))
		sb.WriteString("\n")
	}
	return sb.String()
}
