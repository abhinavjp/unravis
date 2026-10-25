package casestore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

// Host discovery keeps two small tables in the case: the file watermarks that
// make a second pull of a live log non-overlapping, and an audit row for every
// local file read. Neither holds a path. A file is named by its volume serial,
// its file index and an HMAC of the canonical path under a key derived from the
// case key (ruling D10), so the table says "this file" without saying where it
// is. Both tables live in the case database, so Erase destroys them with the
// case, and neither is part of any export.

// DiscoverFileKey names one file in the watermark table.
type DiscoverFileKey struct {
	VolumeSerial uint32
	FileIndex    uint64
	PathHMAC     [32]byte
}

// DiscoverMark is the watermark value for one file: where the next range starts,
// the time of the last line imported (informational), and a hash of the first
// HeadLen bytes. It mirrors localread.Mark; casestore does not import that
// package so the store stays free of discovery code.
type DiscoverMark struct {
	Offset   int64
	LastLine time.Time
	HeadLen  int
	HeadSHA  [32]byte
}

// MaxDiscoverField bounds each text field of a DiscoverRead.
const MaxDiscoverField = 256

// DiscoverRead is the audit row for one read of a local file or channel. It
// never holds the path: PathHMAC is the hex HMAC-SHA256 of the canonical path
// under the case's discovery key.
type DiscoverRead struct {
	At       time.Time
	Detector string
	Importer string
	Kind     string // finding kind: log-file, log-set, event-channel
	Display  string // the finding's display text, which carries no path
	PathHMAC string // 64 lower-case hex characters
	// Range read, for a file. Zero for a channel.
	RangeStart, RangeEnd int64
	FileSize             int64
	FileModified         time.Time
	// BytesSHA256 is the hex SHA-256 of the bytes handed to the importer.
	BytesSHA256 string
	Elevated    bool
	// Window is the time window the user asked for, UTC.
	WindowFrom, WindowTo time.Time
	// Result is "ok", "duplicate", "empty" or a reason from the import.
	Result string
}

// Discoverer is implemented by a Case that keeps the discovery tables. It is a
// separate interface, like Quarantiner, so adding it breaks no Case. Both
// stores in this repository implement it.
type Discoverer interface {
	// DiscoverKey returns a copy of the key that HMACs a canonical path for this
	// case. It is derived from the case's secret key and differs for every case.
	// After the case is closed or erased it returns nil.
	DiscoverKey() []byte
	DiscoverMark(ctx context.Context, k DiscoverFileKey) (DiscoverMark, bool, error)
	PutDiscoverMark(ctx context.Context, k DiscoverFileKey, m DiscoverMark) error
	DeleteDiscoverMark(ctx context.Context, k DiscoverFileKey) error
	RecordDiscoverRead(ctx context.Context, r DiscoverRead) error
	DiscoverReads(ctx context.Context) ([]DiscoverRead, error)
}

// ErrBadDiscoverRead is returned by RecordDiscoverRead for a row with a bad
// field. It never carries the field.
var ErrBadDiscoverRead = errors.New("invalid discovery audit row")

// MaxDiscoverMarks bounds the watermark table of one case.
const MaxDiscoverMarks = 100_000

// MaxDiscoverReads bounds the audit table of one case. Past it a new row is
// refused with ErrCapExceeded.
const MaxDiscoverReads = 200_000

// DeriveDiscoverKey returns the path HMAC key for a case from its pseudonym
// key: HMAC-SHA256(pseudo, "discover-path-v1"). Both stores use it.
func DeriveDiscoverKey(pseudo []byte) []byte {
	m := hmac.New(sha256.New, pseudo)
	m.Write([]byte("discover-path-v1"))
	return m.Sum(nil)
}

// CleanDiscoverRead checks and normalises an audit row. The times are UTC.
func CleanDiscoverRead(r DiscoverRead) (DiscoverRead, error) {
	if len(r.PathHMAC) != 0 && len(r.PathHMAC) != 64 {
		return DiscoverRead{}, ErrBadDiscoverRead
	}
	if r.PathHMAC != "" {
		if _, err := hex.DecodeString(r.PathHMAC); err != nil {
			return DiscoverRead{}, ErrBadDiscoverRead
		}
	}
	if r.BytesSHA256 != "" && len(r.BytesSHA256) != 64 {
		return DiscoverRead{}, ErrBadDiscoverRead
	}
	for _, s := range []string{r.Detector, r.Importer, r.Kind, r.Display, r.Result} {
		if len(s) > MaxDiscoverField {
			return DiscoverRead{}, ErrBadDiscoverRead
		}
	}
	r.At = r.At.UTC()
	r.FileModified = r.FileModified.UTC()
	r.WindowFrom, r.WindowTo = r.WindowFrom.UTC(), r.WindowTo.UTC()
	return r, nil
}

// memDiscover is the in-memory half of a case's discovery tables.
type memDiscover struct {
	mu    sync.Mutex
	marks map[DiscoverFileKey]DiscoverMark
	reads []DiscoverRead
}

var _ Discoverer = (*memCase)(nil)

// DiscoverKey is derived from the case key. nil once the case is erased.
func (c *memCase) DiscoverKey() []byte {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	c.d.mu.RLock()
	key, erased := c.d.key, c.d.erased
	c.d.mu.RUnlock()
	if closed || erased || key == nil {
		return nil
	}
	return DeriveDiscoverKey(key)
}

func (c *memCase) disc() *memDiscover {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if c.d.disc == nil {
		c.d.disc = &memDiscover{marks: map[DiscoverFileKey]DiscoverMark{}}
	}
	return c.d.disc
}

func (c *memCase) DiscoverMark(ctx context.Context, k DiscoverFileKey) (DiscoverMark, bool, error) {
	if err := c.check(ctx); err != nil {
		return DiscoverMark{}, false, err
	}
	d := c.disc()
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.marks[k]
	return m, ok, nil
}

func (c *memCase) PutDiscoverMark(ctx context.Context, k DiscoverFileKey, m DiscoverMark) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	d := c.disc()
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.marks[k]; !ok && len(d.marks) >= MaxDiscoverMarks {
		return ErrCapExceeded
	}
	m.LastLine = m.LastLine.UTC()
	d.marks[k] = m
	return nil
}

func (c *memCase) DeleteDiscoverMark(ctx context.Context, k DiscoverFileKey) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	d := c.disc()
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.marks, k)
	return nil
}

func (c *memCase) RecordDiscoverRead(ctx context.Context, r DiscoverRead) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	r, err := CleanDiscoverRead(r)
	if err != nil {
		return err
	}
	d := c.disc()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.reads) >= MaxDiscoverReads {
		return ErrCapExceeded
	}
	d.reads = append(d.reads, r)
	return nil
}

func (c *memCase) DiscoverReads(ctx context.Context) ([]DiscoverRead, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	d := c.disc()
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]DiscoverRead(nil), d.reads...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// SameDiscoverKey compares two keys in constant time. A helper for tests and
// callers that must not leak timing on a key check.
func SameDiscoverKey(a, b []byte) bool { return len(a) > 0 && subtle.ConstantTimeCompare(a, b) == 1 }
