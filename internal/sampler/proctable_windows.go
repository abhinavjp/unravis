//go:build windows

package sampler

import (
	"errors"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// One NtQuerySystemInformation(SystemProcessInformation) call returns, for every
// process at once: PID, parent, image name, thread and handle counts, creation
// time, kernel and user time, working set and private bytes. These are the same
// numbers OpenProcess + GetProcessTimes + GetProcessMemoryInfo +
// GetProcessHandleCount give one process at a time, so a tick is one system call
// instead of a Toolhelp snapshot plus three queries per process (perf finding
// 2026-11-18). Nothing is opened, so a process the caller cannot open (protected
// ones, when not elevated) is read too. No privilege is requested and no process
// is touched.

const (
	// filetimeToUnixTicks is 1601-01-01 to 1970-01-01 in 100 ns ticks.
	filetimeToUnixTicks = 116444736000000000

	// processReadReuse: a second Processes call this soon after the first is served
	// from the same read. The recorder asks twice per tick (the process-set
	// resolver, then the host collector), a few milliseconds apart. A sampler tick
	// is seconds long, so a real tick always gets a fresh read.
	processReadReuse = 250 * time.Millisecond

	ntProcBufStart = 512 << 10
	ntProcBufMax   = 32 << 20
	statusLenMis   = 0xC0000004 // STATUS_INFO_LENGTH_MISMATCH
	statusBufOver  = 0x80000005 // STATUS_BUFFER_OVERFLOW
	statusTooSmall = 0xC0000023 // STATUS_BUFFER_TOO_SMALL
	nameCacheLimit = 4096
)

// procTable owns the reusable buffers of the system process read and the last
// result. It is safe for concurrent use.
type procTable struct {
	mu    sync.Mutex
	buf   []uint64 // 8-byte aligned, as the kernel lays the entries out
	at    time.Time
	rows  []ProcSample
	names map[string]string
	scr   []byte
}

// appendTo appends every process to dst. The copy happens under the lock because
// the next read reuses the row storage.
func (t *procTable) appendTo(dst []ProcSample, now time.Time) ([]ProcSample, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if age := now.Sub(t.at); t.at.IsZero() || age < 0 || age >= processReadReuse {
		if err := t.query(); err != nil {
			return dst, err
		}
		t.at = now
	}
	return append(dst, t.rows...), nil
}

// query fills t.rows from one system call, growing the buffer when the kernel
// says it is too small (processes can start between the size probe and the read).
func (t *procTable) query() error {
	if len(t.buf) == 0 {
		t.buf = make([]uint64, ntProcBufStart/8)
	}
	for {
		var need uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation,
			unsafe.Pointer(&t.buf[0]), uint32(len(t.buf)*8), &need)
		if err == nil {
			t.parse()
			return nil
		}
		var st windows.NTStatus
		if !errors.As(err, &st) || (uint32(st) != statusLenMis && uint32(st) != statusBufOver && uint32(st) != statusTooSmall) {
			return err
		}
		size := int(need) + 64<<10
		if size <= len(t.buf)*8 {
			size = len(t.buf) * 16
		}
		if size > ntProcBufMax {
			return err
		}
		t.buf = make([]uint64, size/8)
	}
}

func (t *procTable) parse() {
	rows := t.rows[:0]
	base := unsafe.Pointer(&t.buf[0])
	limit := uintptr(len(t.buf) * 8)
	var off uintptr
	for {
		e := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Add(base, off))
		if pid := uint32(e.UniqueProcessID); pid != 0 { // the idle pseudo-process
			p := ProcSample{
				PID:          pid,
				Parent:       uint32(e.InheritedFromUniqueProcessID),
				Image:        t.name(&e.ImageName),
				Threads:      e.NumberOfThreads,
				Have:         HaveTimes | HaveMemory | HaveHandles,
				CPUSeconds:   float64(uint64(e.UserTime)+uint64(e.KernelTime)) / 1e7,
				WorkingSet:   uint64(e.WorkingSetSize),
				PrivateBytes: uint64(e.PrivatePageCount),
				Handles:      e.HandleCount,
			}
			if e.CreateTime > filetimeToUnixTicks {
				p.Start = time.Unix(0, (e.CreateTime-filetimeToUnixTicks)*100).UTC()
			}
			rows = append(rows, p)
		}
		if e.NextEntryOffset == 0 {
			break
		}
		off += uintptr(e.NextEntryOffset)
		if off+unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}) > limit {
			break // a damaged chain: keep what was read
		}
	}
	t.rows = rows
}

// name returns the image name as a string without allocating for a name seen
// before: the lookup is keyed by a reused byte slice. Image names are short and
// almost always ASCII; anything else takes the general path.
func (t *procTable) name(u *windows.NTUnicodeString) string {
	n := int(u.Length / 2)
	if n == 0 || u.Buffer == nil {
		return ""
	}
	w := unsafe.Slice(u.Buffer, n)
	b := t.scr[:0]
	for _, c := range w {
		if c >= 0x80 {
			return windows.UTF16ToString(w)
		}
		b = append(b, byte(c))
	}
	t.scr = b
	if s, ok := t.names[string(b)]; ok { // no allocation: map lookup by []byte conversion
		return s
	}
	if t.names == nil || len(t.names) >= nameCacheLimit {
		t.names = make(map[string]string, 256)
	}
	s := string(b)
	t.names[s] = s
	return s
}
