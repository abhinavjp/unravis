package blackbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The diagnostics log: a small text file in the Blackbox folder that says, in
// numbers, where each round of the recorder went and why it slowed down. It
// exists so a "Running late" can be explained from the file alone.
//
// It holds timings, counts, fixed codes and counter group names only. Never a
// path, a host name, an instance name, a user name, an error text or any
// measured value. Two files of at most diagMaxBytes each (this one and ".1"),
// so it can never grow past about 400 KB.
const (
	DiagFileName = "diagnostics.log"
	diagOldName  = "diagnostics.log.1"
	diagMaxBytes = 192 << 10
	// DiagTailBytes is the most the reader asks for.
	DiagTailBytes = 256 << 10

	diagBeatEvery   = 5 * time.Minute
	diagNotableMin  = time.Second            // a round at least this long is written on its own
	diagGroupMin    = 100 * time.Millisecond // a group at least this long is named in a round
	diagLinesPerMin = 20                     // notable rounds written per minute; the rest are counted
)

// DiagLog writes the diagnostics log. A nil *DiagLog is valid and does nothing,
// and a log that cannot be opened turns itself off: recording never depends on
// it.
type DiagLog struct {
	mu   sync.Mutex
	dir  string
	f    *os.File
	size int64
	now  func() time.Time
	off  bool
	// retryAt: after a failed write or reopen the log is tried again from this
	// time, not given up for the rest of the run.
	retryAt time.Time
	// fsys is the file operations a rotation uses; tests replace it.
	fsys diagFS
	// stuck: a rotation could neither rename nor empty the file, so writing is
	// stopped (the file cannot grow) until a later rotation works (RV-BB-12).
	stuck bool
	// onStuck is called once each time the log gets stuck, and onResumed once each
	// time it writes again after that, both outside the lock.
	onStuck, onResumed func()
}

// diagFS is what a rotation asks of the file system.
type diagFS interface {
	Remove(name string) error
	Rename(oldpath, newpath string) error
	Truncate(name string, size int64) error
}

type osDiagFS struct{}

func (osDiagFS) Remove(name string) error             { return os.Remove(name) }
func (osDiagFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
func (osDiagFS) Truncate(name string, size int64) error {
	return os.Truncate(name, size)
}

// OpenDiagLog opens (or starts) the log in dir. It never fails: on an error the
// returned log is simply silent.
func OpenDiagLog(dir string, now func() time.Time) *DiagLog {
	if now == nil {
		now = time.Now
	}
	d := &DiagLog{dir: dir, now: now, fsys: osDiagFS{}}
	if err := d.open(); err != nil {
		d.off = true
	}
	return d
}

func (d *DiagLog) open() error {
	f, err := os.OpenFile(filepath.Join(d.dir, DiagFileName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	d.f, d.size = f, st.Size()
	return nil
}

// Line writes one line: time, kind, then key=value pairs. A value is made safe
// (no spaces, no line breaks, short). kv alternates keys and values.
func (d *DiagLog) Line(kind string, kv ...any) {
	if d == nil {
		return
	}
	var b strings.Builder
	b.WriteString(d.now().UTC().Format("2006-01-02T15:04:05.000Z"))
	b.WriteByte(' ')
	b.WriteString(kind)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(kv[i]))
		b.WriteByte('=')
		b.WriteString(diagValue(kv[i+1]))
	}
	b.WriteByte('\n')
	d.write(b.String())
}

func diagValue(v any) string {
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case float64:
		s = strconv.FormatFloat(x, 'f', 1, 64)
	case time.Duration:
		s = strconv.FormatFloat(float64(x)/float64(time.Millisecond), 'f', 0, 64)
	default:
		s = fmt.Sprint(v)
	}
	if len(s) > 160 {
		s = s[:160]
	}
	if s == "" {
		return "-"
	}
	return strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return '_'
		}
		return r
	}, s)
}

func (d *DiagLog) write(s string) {
	if after := d.writeLocked(s); after != nil {
		after() // outside the lock: the callback may take other locks, or write a line
	}
}

// SetOnStuck sets what happens when the log cannot be kept under its cap and
// stops writing. It is called once per time that happens.
func (d *DiagLog) SetOnStuck(f func()) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.onStuck = f
	d.mu.Unlock()
}

// SetOnResumed sets what happens when a stuck log can be rotated again and
// writing starts again. It is called once per time that happens.
func (d *DiagLog) SetOnResumed(f func()) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.onResumed = f
	d.mu.Unlock()
}

// writeLocked writes one line under the lock. It returns what the caller must do
// after the lock is released (raise the stuck or the resumed event), or nil. The
// callbacks are copied out under the lock, so a SetOnStuck or SetOnResumed from
// another goroutine cannot race with the read.
func (d *DiagLog) writeLocked(s string) (after func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.off {
		return nil
	}
	if d.stuck {
		// Writing is stopped so the file cannot grow; look again once a minute.
		if now := d.now(); now.Before(d.retryAt) {
			return nil
		}
		d.rotateLocked()
		if d.stuck {
			return nil // still stuck: the event was raised when it began
		}
		after = d.onResumed // writing starts again: say so once
	}
	if d.f == nil {
		// A failed write or reopen: try again once a minute.
		if now := d.now(); now.Before(d.retryAt) {
			return after
		}
		if d.open() != nil {
			d.retryAt = d.now().Add(time.Minute)
			return after
		}
	}
	if d.size+int64(len(s)) > diagMaxBytes {
		d.rotateLocked()
		if d.stuck {
			return d.onStuck
		}
		if d.f == nil {
			return after
		}
	}
	n, err := d.f.WriteString(s)
	d.size += int64(n)
	if err != nil {
		_ = d.f.Close()
		d.f = nil
		d.retryAt = d.now().Add(time.Minute)
	}
	return after
}

// rotateLocked keeps the newest log as ".1" and starts a fresh file. A reader
// holding a file open can make the rename fail on Windows; the cap still holds
// then, because the current file is emptied instead. If that fails too the log
// is stuck: nothing more is written to the file, so it cannot grow, and a later
// write tries the rotation again once a minute (RV-BB-12). The old ".1" is kept
// until the rename or the emptying has worked: it is never removed first.
func (d *DiagLog) rotateLocked() {
	if d.f != nil {
		_ = d.f.Close()
		d.f = nil
	}
	cur := filepath.Join(d.dir, DiagFileName)
	old := filepath.Join(d.dir, diagOldName)
	err := d.fsys.Rename(cur, old)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// Some systems will not rename over an existing file. Move the old ".1"
		// aside, try again, and put it back if the rename still fails.
		aside := old + ".tmp"
		_ = d.fsys.Remove(aside)
		if d.fsys.Rename(old, aside) == nil {
			if err = d.fsys.Rename(cur, old); err == nil {
				_ = d.fsys.Remove(aside)
			} else {
				_ = d.fsys.Rename(aside, old)
			}
		}
	}
	// A current file that is already gone has nothing to cap: open starts it.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if err := d.fsys.Truncate(cur, 0); err != nil {
			d.stuck = true
			d.retryAt = d.now().Add(time.Minute)
			return
		}
	}
	d.stuck = false
	if err := d.open(); err != nil {
		d.retryAt = d.now().Add(time.Minute)
	}
}

// Close closes the file.
func (d *DiagLog) Close() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f != nil {
		d.f.Close()
		d.f = nil
	}
}

// Tail returns the newest max bytes of the log (the old file first, then the
// current one), cut to whole lines.
func (d *DiagLog) Tail(max int) []byte {
	if d == nil {
		return nil
	}
	return ReadDiagTail(d.dir, max)
}

// ReadDiagTail reads the newest max bytes of the diagnostics log in dir.
func ReadDiagTail(dir string, max int) []byte {
	var parts [][]byte
	total := 0
	for _, name := range []string{DiagFileName, diagOldName} { // newest first
		b := readTailFile(filepath.Join(dir, name), max-total)
		if len(b) == 0 {
			continue
		}
		parts = append([][]byte{b}, parts...)
		total += len(b)
		if total >= max {
			break
		}
	}
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return wholeLines(out, total >= max)
}

func readTailFile(path string, max int) []byte {
	if max <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	off := st.Size() - int64(max)
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(f, int64(max)))
	return b
}

// wholeLines drops a cut first line when the data was cut at the front.
func wholeLines(b []byte, cut bool) []byte {
	if cut {
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b
}

/* ----------------------------------------------------------- per round */

// groupTime is one counter group's time in one round.
type groupTime struct {
	Name    string
	Dur     time.Duration
	Outcome string // ok, timeout, busy, skipped, empty, error
}

// roundDiag is where one round went.
type roundDiag struct {
	Seq       uint64
	Lag       time.Duration // how late the round started against its deadline
	Missed    int           // deadlines skipped because the previous round ran over
	HK        time.Duration
	Feed      time.Duration
	Collect   time.Duration
	Host      time.Duration
	Resolve   time.Duration // resolving the selected targets, before the host read
	GroupsAll time.Duration // all counter groups together
	Append    time.Duration
	Total     time.Duration
	Groups    []groupTime // only the slow or failed ones
	Err       string      // fixed code, empty when the round was fine
}

func (r roundDiag) groupsText() string {
	if len(r.Groups) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(r.Groups))
	for _, g := range r.Groups {
		parts = append(parts, g.Name+":"+strconv.FormatInt(g.Dur.Milliseconds(), 10)+":"+g.Outcome)
	}
	return strings.Join(parts, ",")
}

// diagAgg sums the rounds between two heartbeats.
type diagAgg struct {
	since      time.Time
	rounds     int
	sumTotal   time.Duration
	maxTotal   time.Duration
	maxLag     time.Duration
	missed     int
	errs       int
	maxHK      time.Duration
	maxAppend  time.Duration
	suppressed int
	slow       map[string]*groupAgg
}

type groupAgg struct {
	count int
	max   time.Duration
}

func (a *diagAgg) add(r roundDiag) {
	a.rounds++
	a.sumTotal += r.Total
	if r.Total > a.maxTotal {
		a.maxTotal = r.Total
	}
	if r.Lag > a.maxLag {
		a.maxLag = r.Lag
	}
	a.missed += r.Missed
	if r.Err != "" {
		a.errs++
	}
	if r.HK > a.maxHK {
		a.maxHK = r.HK
	}
	if r.Append > a.maxAppend {
		a.maxAppend = r.Append
	}
	for _, g := range r.Groups {
		// Groups that are simply absent (no IIS, no SQL on this PC) answer
		// empty/error/skipped every round: that is not slowness.
		if g.Outcome != "ok" && g.Outcome != "timeout" && g.Outcome != "busy" {
			continue
		}
		if a.slow == nil {
			a.slow = map[string]*groupAgg{}
		}
		e := a.slow[g.Name]
		if e == nil {
			e = &groupAgg{}
			a.slow[g.Name] = e
		}
		e.count++
		if g.Dur > e.max {
			e.max = g.Dur
		}
	}
}

// slowText lists the groups seen slow, most often first, at most five.
func (a *diagAgg) slowText() string {
	if len(a.slow) == 0 {
		return "-"
	}
	type kv struct {
		n string
		g *groupAgg
	}
	var all []kv
	for n, g := range a.slow {
		all = append(all, kv{n, g})
	}
	for i := 1; i < len(all); i++ { // few entries: insertion sort
		for j := i; j > 0 && (all[j].g.count > all[j-1].g.count ||
			(all[j].g.count == all[j-1].g.count && all[j].n < all[j-1].n)); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > 5 {
		all = all[:5]
	}
	parts := make([]string, 0, len(all))
	for _, e := range all {
		parts = append(parts, fmt.Sprintf("%s:%dms:x%d", e.n, e.g.max.Milliseconds(), e.g.count))
	}
	return strings.Join(parts, ",")
}
