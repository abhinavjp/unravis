package localread

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"sync"
	"time"
)

// HeadBlock is how many leading bytes are hashed to recognise a rotated or
// rewritten file that kept its name (4 KiB).
const HeadBlock = 4096

// FileKey identifies a file across scans: volume serial, file index and an
// HMAC of the canonical path under the case's pseudonym key. The path itself is
// never stored (ruling D10).
type FileKey struct {
	VolumeSerial uint32
	FileIndex    uint64
	PathHMAC     [32]byte
}

// KeyFor builds a FileKey. The path is lower-cased first so the key does not
// depend on the case the scan happened to see (Windows paths are
// case-insensitive).
func KeyFor(id FileID, hmacKey []byte, canonical string) FileKey {
	return FileKey{VolumeSerial: id.VolumeSerial, FileIndex: id.FileIndex, PathHMAC: PathHMAC(hmacKey, canonical)}
}

// PathHMAC is HMAC-SHA256(key, lower-case canonical path).
func PathHMAC(key []byte, canonical string) [32]byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(strings.ToLower(canonical)))
	var out [32]byte
	copy(out[:], m.Sum(nil))
	return out
}

// Mark is the watermark value for one file: where the next range starts, the
// time of the last line imported (informational), and a hash of the first
// HeadLen bytes (HeadLen = min(4096, Offset)). A file whose first block no
// longer hashes the same, or that is shorter than Offset, was rotated or
// truncated and restarts at 0.
//
// The first-block hash is part of the value rather than the key: a file under
// 4 KiB gets a different hash every time it grows, and a key containing it
// would lose the watermark and import the same bytes twice.
type Mark struct {
	Offset   int64
	LastLine time.Time
	HeadLen  int
	HeadSHA  [32]byte
}

// WatermarkStore persists marks. The case database implements it (task P1f);
// MemoryWatermarks is the in-memory implementation used by tests and as the
// reference. Implementations must be safe for concurrent use.
type WatermarkStore interface {
	Get(FileKey) (Mark, bool, error)
	Put(FileKey, Mark) error
	Delete(FileKey) error
}

// MemoryWatermarks is an in-memory WatermarkStore.
type MemoryWatermarks struct {
	mu sync.Mutex
	m  map[FileKey]Mark
}

// NewMemoryWatermarks returns an empty store.
func NewMemoryWatermarks() *MemoryWatermarks { return &MemoryWatermarks{m: map[FileKey]Mark{}} }

func (s *MemoryWatermarks) Get(k FileKey) (Mark, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.m[k]
	return m, ok, nil
}

func (s *MemoryWatermarks) Put(k FileKey, m Mark) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = m
	return nil
}

func (s *MemoryWatermarks) Delete(k FileKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}

// Len is the number of stored marks.
func (s *MemoryWatermarks) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// Restart reasons reported in RangeInfo.Restart.
const (
	RestartFirstScan        = "first-scan"
	RestartTruncated        = "truncated"
	RestartFirstBlockChange = "first-block-changed"
	RestartMisaligned       = "misaligned"
)

// resume decides where the next range of a file starts: at the stored offset
// when the file still looks like the one that offset belonged to, else at 0.
func resume(ctx context.Context, lim Limiter, store WatermarkStore, key FileKey, src Source, size int64) (start int64, reason string, err error) {
	m, ok, err := store.Get(key)
	if err != nil {
		return 0, "", err
	}
	if !ok {
		return 0, RestartFirstScan, nil
	}
	if m.Offset > size || int64(m.HeadLen) > size || m.Offset < 0 || m.HeadLen < 0 || m.HeadLen > HeadBlock {
		return 0, RestartTruncated, store.Delete(key)
	}
	if m.HeadLen > 0 {
		buf, err := readRegion(ctx, lim, src, 0, m.HeadLen)
		if err != nil {
			return 0, "", err
		}
		if sha256.Sum256(buf) != m.HeadSHA {
			return 0, RestartFirstBlockChange, store.Delete(key)
		}
	}
	return m.Offset, "", nil
}

// Next opens canonical (all Open checks, expect required) and serves the part
// of the file not yet imported: from the stored watermark, or from 0 when the
// file is new, truncated, rotated or its first block changed, up to the last
// full line before the frozen length. After the bytes have been imported
// successfully call Commit on the reader to advance the watermark; a range
// that failed is simply not committed and is served again next time.
func Next(ctx context.Context, op Opener, store WatermarkStore, key FileKey, canonical string, expect FileID, header HeaderKind, opts RangeOptions) (*RangeReader, error) {
	if expect.IsZero() {
		return nil, unsafe("no expected file identity")
	}
	src, info, err := op.OpenSource(canonical, expect)
	if err != nil {
		return nil, err
	}
	rr, err := next(ctx, src, info, store, key, header, nil, opts)
	if err != nil {
		_ = src.Close()
		return nil, err
	}
	return rr, nil
}

func next(ctx context.Context, src Source, info Info, store WatermarkStore, key FileKey, header HeaderKind, win *WindowSpec, opts RangeOptions) (*RangeReader, error) {
	lim := opts.limiter()
	start, reason, err := resume(ctx, lim, store, key, src, info.Size)
	if err != nil {
		return nil, err
	}
	rr, err := buildRange(ctx, src, info, RangeRequest{Offset: start, Header: header, Window: win}, opts)
	if err == ErrMisaligned && start > 0 {
		// The stored offset is not a line start: the file changed under it.
		_ = store.Delete(key)
		start, reason = 0, RestartMisaligned
		rr, err = buildRange(ctx, src, info, RangeRequest{Offset: 0, Header: header, Window: win}, opts)
	}
	if err != nil {
		return nil, err
	}
	rr.Meta.Restart = reason
	rr.key, rr.hasKy = key, true
	return rr, nil
}

// Commit records the end of the served range as the file's watermark. lastLine
// is the time of the last line imported, zero if unknown. A range that served
// nothing and restarted nothing leaves the stored mark alone.
func (r *RangeReader) Commit(store WatermarkStore, lastLine time.Time) error {
	if !r.hasKy {
		return nil
	}
	if r.Meta.Empty && r.Meta.Restart == "" {
		return nil
	}
	m := r.mark
	m.LastLine = lastLine.UTC()
	return store.Put(r.key, m)
}
