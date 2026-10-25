// Package localread is the one place where Unravis opens a file on the local
// machine for host discovery. It opens read-only with every share flag so the
// application that writes the log is never blocked, refuses anything that is
// not a plain local disk file (reparse points, UNC paths, alternate streams,
// 8.3 aliases, directories, devices), and checks the file identity against the
// one recorded at scan time. On top of that it provides frozen-length readers,
// line-aligned ranges with header replay, a watermark store and a politeness
// rate limiter. Only the open itself is Windows specific; everything else is
// plain Go over the Opener interface and is tested on any OS.
//
// Error text never carries a path: callers show the finding's Display name.
package localread

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"
)

var (
	// ErrNotSupportedOS is returned by the system opener off Windows.
	ErrNotSupportedOS = errors.New("localread: not supported on this OS")
	// ErrUnsafePath means the path or the file behind it failed a safety
	// check: bad shape, reparse point, not a local disk file, final path
	// differs from the expected one, or the file identity changed since the
	// scan. The wrapped message names the check, never the path.
	ErrUnsafePath = errors.New("localread: unsafe path")
	// ErrAccessDenied means the OS denied the open. Detectors map it to a
	// needs-elevation gap. It also satisfies errors.Is(err, fs.ErrPermission).
	ErrAccessDenied = fmt.Errorf("localread: access denied: %w", fs.ErrPermission)
	// ErrNoHeader means a header to replay could not be found.
	ErrNoHeader = errors.New("localread: no header found to replay")
	// ErrMisaligned means a range offset does not start on a line boundary.
	ErrMisaligned = errors.New("localread: offset is not at a line start")
	// ErrLineTooLong means no line break was found within the scan limit.
	ErrLineTooLong = errors.New("localread: line longer than the scan limit")
	// ErrFileShrank means the file got shorter than its frozen length while it
	// was being read.
	ErrFileShrank = errors.New("localread: file shrank while reading")
)

// FileID is the identity of a file on a volume: volume serial number plus the
// file index from GetFileInformationByHandle. The zero value means unknown.
type FileID struct {
	VolumeSerial uint32
	FileIndex    uint64
}

// IsZero reports whether the identity is unset.
func (f FileID) IsZero() bool { return f == FileID{} }

// Info describes a file as seen at open time.
type Info struct {
	ID      FileID
	Size    int64 // frozen length: nothing past this offset is ever read
	ModTime time.Time
	Base    string // base name only, safe to show
}

// Source is the random-access view of an opened file. Callers never read past
// Info.Size.
type Source interface {
	io.ReaderAt
	io.Closer
}

// Opener opens a canonical path as a Source. expect, when not zero, is the
// identity taken at scan time; a mismatch is ErrUnsafePath. A zero expect
// skips the comparison (scan-time identification only).
type Opener interface {
	OpenSource(canonical string, expect FileID) (Source, Info, error)
}

// System returns the opener for the real file system: the hardened Windows
// opener on Windows, a stub that fails with ErrNotSupportedOS elsewhere.
func System() Opener { return systemOpener{} }

// Open opens canonical for sequential reading. expect must be the identity
// recorded at scan time (see Identify); a zero expect is refused so a caller
// bug cannot switch the swap check off. The reader yields at most Info.Size
// bytes, the length at open, so a growing file gives a stable range.
func Open(canonical string, expect FileID) (io.ReadCloser, Info, error) {
	return OpenWith(System(), canonical, expect)
}

// OpenWith is Open over an explicit Opener.
func OpenWith(op Opener, canonical string, expect FileID) (io.ReadCloser, Info, error) {
	if expect.IsZero() {
		return nil, Info{}, unsafe("no expected file identity")
	}
	src, info, err := op.OpenSource(canonical, expect)
	if err != nil {
		return nil, Info{}, err
	}
	return &frozenReader{src: src, r: &exactReader{r: io.NewSectionReader(src, 0, info.Size), want: info.Size}}, info, nil
}

// Identify runs every open-time check and returns the file identity and
// length without reading content. Scans use it to record the FileID that Open
// later compares against.
func Identify(canonical string) (Info, error) { return IdentifyWith(System(), canonical) }

// IdentifyWith is Identify over an explicit Opener.
func IdentifyWith(op Opener, canonical string) (Info, error) {
	src, info, err := op.OpenSource(canonical, FileID{})
	if err != nil {
		return Info{}, err
	}
	_ = src.Close()
	return info, nil
}

type unsafeError struct{ why string }

func (e *unsafeError) Error() string   { return "localread: unsafe path: " + e.why }
func (e *unsafeError) Is(t error) bool { return t == ErrUnsafePath }

func unsafe(why string) error { return &unsafeError{why: why} }

// exactReader yields exactly want bytes: a source that ends early (a file that
// shrank under the reader) is ErrFileShrank, not a silent short read.
type exactReader struct {
	r    io.Reader
	want int64
	got  int64
}

func (e *exactReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	e.got += int64(n)
	if err == io.EOF && e.got < e.want {
		return n, ErrFileShrank
	}
	return n, err
}

// frozenReader is the reader Open returns.
type frozenReader struct {
	src Source
	r   *exactReader
}

func (f *frozenReader) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *frozenReader) Close() error               { return f.src.Close() }
