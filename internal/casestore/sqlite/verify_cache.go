package sqlite

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// The open-time PRAGMA quick_check reads the whole database: seconds on a large
// case, and the server opens a case for every request. This file keeps the
// guarantee (a case file is fully checked before this process trusts it) while
// paying for it once per process per case file, not once per open.
//
// The rule, conservative on purpose. An open skips quick_check only when this
// process already passed a quick_check on the same case.db path with the same
// database key and one of these holds:
//
//   - a handle on the case is open right now (this process holds the file, and
//     every change since the check was made through this process); or
//   - every handle has closed since, and case.db is the same file (os.SameFile),
//     with the same size and modification time, and case.db-wal has the same size,
//     as recorded after the last handle closed (after its checkpoint). Anything that
//     changed the file since, in another process or by hand, breaks the match.
//     A stamp whose modification time is within verifyRacyWindow of the moment it
//     was taken is never trusted, because a coarse file clock could give a later
//     same-size rewrite the same time ("racy" stamps, as in git's index).
//
// A failed check is never remembered: the entry is cleared, and so is it by any
// error in the rest of verify, by Erase, by freeing the working copy, and when
// the case is created. Only successful checks are recorded. The first open in a
// process always does the full check; concurrent first opens of one case share it
// (the second waits for the first, then finds the result). Cases do not wait on
// each other: the check runs under that case's own lock only.
//
// Everything else in verify (case id, schema version, migration, manifest cross
// check) still runs on every open; it is cheap.
var verified = &verifyCache{m: map[string]*verifyEntry{}}

// verifyRacyWindow is how recent a recorded modification time may be and still
// not be trusted. A variable so tests can shorten it.
var verifyRacyWindow = 2 * time.Second

// quickCheckRuns counts the quick_checks actually run (tests read the delta).
var quickCheckRuns atomic.Int64

type verifyCache struct {
	mu sync.Mutex
	m  map[string]*verifyEntry
}

type verifyEntry struct {
	path  string
	chk   sync.Mutex  // serialises checks of this one case
	stale atomic.Bool // dropped from the cache; a holder must look the entry up again

	mu      sync.Mutex // guards the fields below; never held across a check
	ok      bool
	keyHash [sha256.Size]byte
	open    int // handles that passed through acquire and have not released
	haveID  bool
	id      fileStamp
}

type fileStamp struct {
	info    os.FileInfo
	size    int64
	mtime   time.Time
	walSize int64 // 0 when there is no -wal file (an open connection creates an empty one)
	racy    bool
}

// cacheKey names a case.db for the cache: absolute, cleaned, and folded on Windows.
func cacheKey(dir string) string {
	p := filepath.Join(dir, dbName)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// statStamp reads the identity of case.db through an open handle. On Windows,
// os.Stat leaves the file ID to be read lazily from the path when os.SameFile first
// needs it, so a stamp taken that way would compare the file as it is at check time
// with itself and a replaced file would pass. FileInfo from an open handle carries
// the volume and file index already, which pins the stamp to the file seen now.
func statStamp(path string) (fileStamp, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileStamp{}, err
	}
	fi, err := f.Stat()
	_ = f.Close()
	if err != nil {
		return fileStamp{}, err
	}
	_ = os.SameFile(fi, fi) // fetches a still-missing file ID now, not at comparison time
	st := fileStamp{info: fi, size: fi.Size(), mtime: fi.ModTime()}
	if w, err := os.Stat(path + "-wal"); err == nil {
		st.walSize = w.Size()
	} else if !os.IsNotExist(err) {
		return fileStamp{}, err
	}
	return st, nil
}

func (a fileStamp) sameAs(b fileStamp) bool {
	return !a.racy && os.SameFile(a.info, b.info) && a.size == b.size && a.mtime.Equal(b.mtime) && a.walSize == b.walSize
}

func (v *verifyCache) lookup(key string) *verifyEntry {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := v.m[key]
	if e == nil {
		e = &verifyEntry{path: key}
		v.m[key] = e
	}
	return e
}

// acquire reports whether the quick_check can be skipped, running check when it
// cannot. On success the caller holds one open on the returned entry and must
// pass it to release or abort exactly once; on error nothing is held.
func (v *verifyCache) acquire(dir string, keyHash [sha256.Size]byte, check func() error) (*verifyEntry, bool, error) {
	key := cacheKey(dir)
	for {
		e := v.lookup(key)
		e.chk.Lock()
		if e.stale.Load() {
			e.chk.Unlock()
			continue
		}
		if e.trusted(keyHash) {
			e.chk.Unlock()
			return e, true, nil
		}
		err := check()
		e.mu.Lock()
		e.haveID = false
		if err != nil {
			e.ok = false
		} else {
			e.ok, e.keyHash = true, keyHash
			e.open++
		}
		e.mu.Unlock()
		e.chk.Unlock()
		if err != nil {
			return nil, false, err
		}
		return e, false, nil
	}
}

// trusted decides the skip and, when it says yes, counts the new open. The caller
// holds e.chk.
func (e *verifyEntry) trusted(keyHash [sha256.Size]byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.ok || e.keyHash != keyHash {
		return false
	}
	if e.open == 0 {
		if !e.haveID {
			return false
		}
		now, err := statStamp(e.path)
		if err != nil || !e.id.sameAs(now) {
			e.ok, e.haveID = false, false
			return false
		}
	}
	e.open++
	return true
}

// release is the last use of a handle's database, after it was closed. closedClean
// says the database closed without error; only then is the file stamped.
func (v *verifyCache) release(e *verifyEntry, closedClean bool) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.open--
	if e.open > 0 || e.stale.Load() {
		return
	}
	e.haveID = false
	if !e.ok || !closedClean {
		return
	}
	st, err := statStamp(e.path)
	if err != nil {
		return
	}
	st.racy = time.Since(st.mtime) < verifyRacyWindow
	e.id, e.haveID = st, true
}

// abort undoes an acquire when the open fails later: the case is not trusted.
func (v *verifyCache) abort(e *verifyEntry) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.open--
	e.ok, e.haveID = false, false
	e.mu.Unlock()
}

// drop forgets a case file: it was erased, freed or replaced.
func (v *verifyCache) drop(dir string) {
	key := cacheKey(dir)
	v.mu.Lock()
	e := v.m[key]
	delete(v.m, key)
	v.mu.Unlock()
	if e != nil {
		e.stale.Store(true)
	}
	timelineFailed.Delete(key)
}

// isDamage reports whether err says the database file itself is bad: a corrupt
// page, a failed data read, a file that is not a database, or this package's
// ErrDamaged.
func isDamage(err error) bool {
	return err != nil && (errors.Is(err, ErrDamaged) || errors.Is(err, sqlite3.CORRUPT) ||
		errors.Is(err, sqlite3.IOERR_DATA) || errors.Is(err, sqlite3.NOTADB))
}

// noteDamage is called wherever a live case meets damage (IntegrityCheck, a query
// reading a bad page). The case file is then no longer trusted: the cached check is
// dropped, so the handle's shutdown does not stamp it as verified and the next open
// runs the full quick_check. It returns err unchanged, so a call can wrap a return.
func (c *caseState) noteDamage(err error) error {
	if isDamage(err) {
		verified.drop(c.dir)
	}
	return err
}
