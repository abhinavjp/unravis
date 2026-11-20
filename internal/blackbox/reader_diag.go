package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// tailWindow is how much of the newest recording file is read to find the real
// time of the newest sample. A sample line is a few KB; 64 KB holds several.
const tailWindow = 64 << 10

// TailAccess is a FileAccess that can also read the end of a file.
type TailAccess interface {
	Tail(name string, max int64) ([]byte, error)
}

// Tail reads the last max bytes of one file of the folder, under the same
// containment rules as ReadFile.
func (d DirAccess) Tail(name string, max int64) ([]byte, error) {
	rc, err := d.openContained(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if sk, ok := rc.(io.Seeker); ok {
		if end, err := sk.Seek(0, io.SeekEnd); err == nil {
			off := end - max
			if off < 0 {
				off = 0
			}
			if _, err := sk.Seek(off, io.SeekStart); err != nil {
				return nil, err
			}
		}
	}
	return io.ReadAll(io.LimitReader(rc, max+1<<10))
}

func tailFileCtx(ctx context.Context, ta TailAccess, name string, max int64, local chan struct{}) ([]byte, error) {
	release, ok := acquireOS(ctx, local)
	if !ok {
		return nil, errReadCancelled
	}
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		defer release()
		b, err := ta.Tail(name, max)
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-ctx.Done():
		return nil, errReadCancelled
	}
}

// newestSampleTime is the time of the last whole sample line in tail, if any.
// Bytes after the final newline are a line the recorder is still writing (or a
// cut one) and are ignored.
func newestSampleTime(tail []byte) (time.Time, bool) {
	end := bytes.LastIndexByte(tail, '\n')
	for end > 0 {
		start := bytes.LastIndexByte(tail[:end], '\n') + 1
		var s struct {
			T time.Time `json:"t"`
		}
		if json.Unmarshal(tail[start:end], &s) == nil && !s.T.IsZero() {
			return s.T.UTC(), true
		}
		end = start - 1
	}
	return time.Time{}, false
}

// freshLast moves LastSample forward to the newest sample actually in the
// newest recording file. The recorder rewrites manifest.json only every five
// minutes, so read as a folder or a share the manifest can be minutes behind;
// judged on that, a recorder ticking every few seconds always looked "Running
// late". An HTTP reader gets the live manifest from the recorder and needs no
// help. Any failure leaves the manifest as it was.
func (s *FolderSource) freshLast(ctx context.Context, m *Manifest) {
	ta, ok := s.Access.(TailAccess)
	if !ok || m.State == ManifestStopped || len(m.Files) == 0 {
		return
	}
	newest := m.Files[0]
	for _, f := range m.Files[1:] {
		if f.Hour.After(newest.Hour) {
			newest = f
		}
	}
	if !ValidFileName(newest.Name) {
		return
	}
	tail, err := tailFileCtx(ctx, ta, newest.Name, tailWindow, s.sourceSlots())
	if err != nil {
		return
	}
	if t, ok := newestSampleTime(tail); ok && (m.LastSample == nil || t.After(*m.LastSample)) {
		m.LastSample = &t
	}
}

// DiagSource is a Source that can also give the diagnostics log.
type DiagSource interface {
	// Diagnostics returns the newest part of the log, or nil when there is none
	// (a Blackbox from before the log existed).
	Diagnostics(ctx context.Context) ([]byte, error)
}

// Diagnostics reads the log from the recordings folder.
func (s *FolderSource) Diagnostics(ctx context.Context) ([]byte, error) {
	var out []byte
	for _, name := range []string{diagOldName, DiagFileName} {
		b, err := readFileCtx(ctx, s.Access, name, s.sourceSlots())
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if len(out) == 0 && name == DiagFileName {
				return nil, s.classifyFile(err, name)
			}
			continue
		}
		out = append(out, b...)
	}
	return lastBytesWholeLines(out, DiagTailBytes), nil
}

// Diagnostics reads /diag.
func (s *HTTPSource) Diagnostics(ctx context.Context) ([]byte, error) {
	b, _, rerr := s.get(ctx, "/diag", DiagTailBytes+1<<10)
	if rerr != nil {
		if rerr.Kind == ReadNotFound {
			return nil, nil
		}
		return nil, rerr
	}
	return b, nil
}

func lastBytesWholeLines(b []byte, max int) []byte {
	if len(b) <= max {
		return b
	}
	b = b[len(b)-max:]
	for i, c := range b {
		if c == '\n' {
			return b[i+1:]
		}
	}
	return nil
}
