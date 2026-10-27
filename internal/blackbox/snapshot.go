package blackbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// SnapshotsDir is the folder, inside the recordings folder, that holds snapshots.
const SnapshotsDir = "snapshots"

// SnapshotReason names why a snapshot could not be written.
type SnapshotReason string

// Snapshot failure reasons.
const (
	SnapDriveFull SnapshotReason = "drive-full"
	SnapFileOpen  SnapshotReason = "file-open"
	SnapOther     SnapshotReason = "other"
)

// SnapshotError is a snapshot that failed. A half snapshot was removed.
type SnapshotError struct {
	Reason  SnapshotReason
	Message string
	Cause   error
}

func (e *SnapshotError) Error() string { return e.Message }
func (e *SnapshotError) Unwrap() error { return e.Cause }

// SnapshotResult is a finished snapshot.
type SnapshotResult struct {
	Dir        string
	Bytes      int64
	Recordings int
	Logs       int
}

// SnapshotOptions are the machine effects of Snapshot. Zero values are real.
type SnapshotOptions struct {
	Disk DiskSpace
	// CopyFile copies src to dst. Tests replace it to simulate failures.
	CopyFile func(src, dst string) (int64, error)
}

func mb(n uint64) string { return fmt.Sprintf("%d MB", (n+(1<<20)-1)>>20) }

// SnapshotName is the folder name of a snapshot taken at t (local time).
func SnapshotName(name string, t time.Time) string {
	return name + "-" + t.Format("2006-01-02-1504")
}

func isDiskFull(err error) bool {
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not enough space") || strings.Contains(s, "no space left") || strings.Contains(s, "disk is full") || strings.Contains(s, "disk full")
}

func defaultCopy(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, &openErr{err}
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// openErr marks a source file that could not be opened: it is locked by another
// program, or it has gone.
type openErr struct{ error }

func (e *openErr) Unwrap() error { return e.error }

// Snapshot copies what the recorder wrote into <folder>\snapshots\<name>-<time>.
// The recordings and the manifest are copied as they are. Log files are copied
// only when cfg.Record.CopyLogsOnSnapshot is set, and only those written since
// the recordings begin. The read key and any password are never copied: the key
// hash lives outside this folder and no password is stored anywhere in it. When
// anything fails the half snapshot is removed and the folder is left as it was.
func Snapshot(cfg Config, now time.Time, o SnapshotOptions) (res SnapshotResult, err error) {
	if o.CopyFile == nil {
		o.CopyFile = defaultCopy
	}
	if o.Disk == nil {
		o.Disk = OSDisk{}
	}
	ents, rerr := os.ReadDir(cfg.Folder)
	if rerr != nil {
		return res, &SnapshotError{Reason: SnapOther, Message: "The recordings folder cannot be read: " + rerr.Error(), Cause: rerr}
	}
	type item struct {
		src, rel string
		size     int64
	}
	var items []item
	var need int64
	var first time.Time
	haveFirst := false
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !(ValidFileName(n) || n == ManifestName) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		items = append(items, item{filepath.Join(cfg.Folder, n), n, info.Size()})
		need += info.Size()
		if h, ok := HourOfName(n); ok {
			res.Recordings++
			if !haveFirst || h.Before(first) {
				first, haveFirst = h, true
			}
		}
	}
	if res.Recordings == 0 {
		return res, &SnapshotError{Reason: SnapOther, Message: "There are no recordings to copy yet."}
	}
	if cfg.Record.CopyLogsOnSnapshot {
		for _, l := range CatalogueLogs(cfg.LogPaths) {
			if l.MTime.Before(first) {
				continue
			}
			rel := filepath.Join("logs", fmt.Sprintf("%02d-%s", res.Logs+1, filepath.Base(l.Path)))
			items = append(items, item{l.Path, rel, l.Size})
			need += l.Size
			res.Logs++
		}
	}

	if info, derr := o.Disk.Free(cfg.Folder); derr == nil && uint64(need) > info.Free {
		return res, &SnapshotError{Reason: SnapDriveFull, Message: fmt.Sprintf("The drive is full: %s needed, %s free. Free space or choose another folder, then Snapshot again.", mb(uint64(need)), mb(info.Free))}
	}

	base := filepath.Join(cfg.Folder, SnapshotsDir)
	dir := filepath.Join(base, SnapshotName(cfg.Name, now))
	for i := 2; ; i++ { // two snapshots in the same minute do not overwrite each other
		if _, serr := os.Stat(dir); serr != nil {
			break
		}
		dir = filepath.Join(base, fmt.Sprintf("%s-%d", SnapshotName(cfg.Name, now), i))
	}
	baseExisted := true
	if _, serr := os.Stat(base); serr != nil {
		baseExisted = false
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
			if !baseExisted {
				_ = os.Remove(base)
			}
		}
	}()
	if merr := os.MkdirAll(dir, 0o755); merr != nil {
		return res, classifySnapErr(merr, need)
	}
	for _, it := range items {
		dst := filepath.Join(dir, it.rel)
		if merr := os.MkdirAll(filepath.Dir(dst), 0o755); merr != nil {
			return res, classifySnapErr(merr, need)
		}
		n, cerr := o.CopyFile(it.src, dst)
		if cerr != nil {
			return res, classifySnapErr(cerr, need)
		}
		res.Bytes += n
	}
	res.Dir = dir
	return res, nil
}

func classifySnapErr(err error, need int64) error {
	var oe *openErr
	switch {
	case isDiskFull(err):
		return &SnapshotError{Reason: SnapDriveFull, Message: fmt.Sprintf("The drive is full: about %s needed. Free space or choose another folder, then Snapshot again.", mb(uint64(need))), Cause: err}
	case errors.As(err, &oe):
		return &SnapshotError{Reason: SnapFileOpen, Message: "A file is open in another program: close it, then try again. Nothing was changed. A half snapshot is removed.", Cause: err}
	}
	return &SnapshotError{Reason: SnapOther, Message: "The snapshot could not be written: " + err.Error() + " A half snapshot is removed.", Cause: err}
}
