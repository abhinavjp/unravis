package blackbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/bbsql"
)

// Reading the SQL service's samples back ("SQL charts").
//
// The SQL service writes bb-sql-<hour>.ndjson into <recordings>\sql, a folder only
// SYSTEM, Administrators and the service account may read. Unravis runs as an
// ordinary person, so it cannot open that folder. It reads the same files through
// the recorder instead: the recorder (LocalSystem) serves them on /sql/files/<name>
// behind the same read key as every other file (server.go). A folder or share
// source tries the plain file and says when the folder is locked.
//
// Only numbers, enum states and validated wait types are ever in these files (C9),
// and the reader re-checks that on the way in: a line is accepted only when it has
// a known kind, carries no field the writer's structs do not have, holds no more
// rows than its cap, and every wait type has the stored shape. A line that fails is
// counted corrupt and never charted.

// SQLFileNameFor is the hourly SQL sample file that holds a sample taken at t.
func SQLFileNameFor(t time.Time) string {
	return bbsql.SamplePrefix + t.UTC().Format(bbsql.SampleHourLayout) + bbsql.SampleSuffix
}

// SQLHourOfName reads the hour a SQL sample file name stands for.
func SQLHourOfName(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, bbsql.SamplePrefix) || !strings.HasSuffix(name, bbsql.SampleSuffix) {
		return time.Time{}, false
	}
	mid := name[len(bbsql.SamplePrefix) : len(name)-len(bbsql.SampleSuffix)]
	if len(mid) != len("20060102-15") {
		return time.Time{}, false
	}
	h, err := time.Parse(bbsql.SampleHourLayout, mid)
	if err != nil {
		return time.Time{}, false
	}
	// The name must round-trip: no sign, space or other form time.Parse might admit.
	if SQLFileNameFor(h) != name {
		return time.Time{}, false
	}
	return h.UTC(), true
}

// ValidSQLFileName says whether name is one of the SQL sample files, for downloads.
func ValidSQLFileName(name string) bool {
	_, ok := SQLHourOfName(name)
	return ok
}

// SQLSource is a Source that can also open the SQL service's sample files.
type SQLSource interface {
	// OpenSQLFile opens one hourly SQL sample file by its name.
	OpenSQLFile(ctx context.Context, name string) (io.ReadCloser, error)
}

// Limits on one SQL read. The files are small (a busy hour is a few MB), so these
// are loose, but every one of them is a hard stop.
const (
	maxSQLLineBytes = 256 << 10
	maxSQLReadBytes = 64 << 20
	maxSQLReadLines = 150000
	maxSQLReadHours = MaxReadFiles
)

// SQLSample is one decoded line: the time, the kind and the rows of that kind. Only
// the field of the line's own kind is set. A nil payload (the sampler had nothing to
// report that tick) leaves every field empty and still counts as an observation.
type SQLSample struct {
	T          time.Time
	Kind       string
	Truncated  bool
	Requests   []bbsql.Requests
	Waits      []bbsql.Waits
	Schedulers *bbsql.Schedulers
	Semaphores []bbsql.Semaphores
	Tempdb     *bbsql.Tempdb
	FileStats  []bbsql.FileStats
	AG         []bbsql.AG
}

// SQLReadReport is what one SQL read could and could not read.
type SQLReadReport struct {
	Files   int // hourly files opened
	Samples int // lines kept
	Lines   int // lines read, kept or not
	Missing int // hours with no file (the service was off, or the hour had no sample)
	Dropped int // files that could not be read for another reason
	Corrupt int // lines that did not pass the checks
	Torn    int // files whose last line was cut
	Newer   bool
	// Locked: the folder exists but this account may not read it (a folder or share read).
	Locked bool
	// Unsupported: the source cannot give SQL files at all (a source type that has none).
	// An older recorder with no /sql route answers 404, which looks like Missing.
	Unsupported bool
	// Limited: a byte, line or hour limit stopped the read; the answer is incomplete.
	Limited bool
}

// ReadSQLSamples reads the SQL samples taken from `from` up to (not including) `to`, in
// time order. It never fails the whole read for one bad hour: the report says what was
// missing, locked, dropped or corrupt. The error is only for a cancelled read or a read
// key that was refused (those would fail every hour).
func ReadSQLSamples(ctx context.Context, src Source, from, to time.Time) ([]SQLSample, SQLReadReport, error) {
	var rep SQLReadReport
	ss, ok := src.(SQLSource)
	if !ok {
		rep.Unsupported = true
		return nil, rep, nil
	}
	if !to.After(from) {
		return nil, rep, nil
	}
	release, got := acquireSlot(ctx, readAdmit)
	if !got {
		return nil, rep, readErr(ReadBusy, "Too many Blackbox reads are running at once. Try again in a moment.", ctx.Err())
	}
	defer release()

	var hours []time.Time
	for h := from.UTC().Truncate(time.Hour); h.Before(to); h = h.Add(time.Hour) {
		hours = append(hours, h)
	}
	if len(hours) > maxSQLReadHours {
		rep.Limited = true
		hours = hours[len(hours)-maxSQLReadHours:]
	}
	var out []SQLSample
	bytesLeft, linesLeft := int64(maxSQLReadBytes), maxSQLReadLines
	// Newest hour first: when a byte or line limit stops the read, the part that is missing is the START of the range
	// (the note says so), never the most recent hours. The result is sorted back into time order below.
	for i := len(hours) - 1; i >= 0; i-- {
		h := hours[i]
		if err := ctx.Err(); err != nil {
			return out, rep, readErr(ReadUnreachable, "The read was cancelled.", err)
		}
		rc, err := ss.OpenSQLFile(ctx, SQLFileNameFor(h))
		if err != nil {
			switch {
			case ctx.Err() != nil:
				return out, rep, readErr(ReadUnreachable, "The read was cancelled.", ctx.Err())
			case KindOf(err) == ReadKeyRefused || KindOf(err) == ReadThrottled:
				return out, rep, err
			case errors.Is(err, fs.ErrPermission):
				rep.Locked = true
			case KindOf(err) == ReadNotFound:
				rep.Missing++
			default:
				rep.Dropped++
			}
			continue
		}
		rep.Files++
		part, st := readSQLFile(rc, from, to, &bytesLeft, &linesLeft)
		_ = rc.Close()
		out = append(out, part...)
		rep.Lines += st.lines
		rep.Corrupt += st.corrupt
		if st.torn {
			rep.Torn++
		}
		rep.Newer = rep.Newer || st.newer
		if st.err {
			rep.Dropped++
		}
		if st.limited {
			rep.Limited = true
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	rep.Samples = len(out)
	return out, rep, nil
}

type sqlFileStatus struct {
	lines, corrupt       int
	torn, newer, limited bool
	err                  bool
}

// sqlLine is the strict shape of one stored line (bbsql.SampleDoc as written).
type sqlLine struct {
	V         int             `json:"v"`
	T         time.Time       `json:"t"`
	Kind      string          `json:"kind"`
	Truncated bool            `json:"truncated"`
	Rows      json.RawMessage `json:"rows"`
}

func readSQLFile(r io.Reader, from, to time.Time, bytesLeft *int64, linesLeft *int) ([]SQLSample, sqlFileStatus) {
	var st sqlFileStatus
	var out []SQLSample
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, tooLong, terminated, err := readBoundedLine(br, maxSQLLineBytes, bytesLeft)
		if len(bytes.TrimSpace(line)) > 0 || tooLong {
			*linesLeft--
			st.lines++
			if tooLong {
				st.corrupt++
			} else {
				s, res := decodeSQLLine(line)
				switch res {
				case sqlLineOK:
					if !s.T.Before(from) && s.T.Before(to) {
						out = append(out, s)
					}
				case sqlLineNewer:
					st.newer = true
				default:
					if !terminated {
						st.torn = true // no newline ended it: the writer was in the middle of this line
					} else {
						st.corrupt++
					}
				}
			}
		}
		if *bytesLeft < 0 || *linesLeft < 0 {
			st.limited = true
			return out, st
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				st.err = true
			}
			return out, st
		}
	}
}

// readBoundedLine reads one line of at most max bytes and takes the bytes it reads off *left.
// A longer line is read to its end without keeping it and reported tooLong. The line has no
// trailing newline; terminated says a newline ended it (a last line with none is the one the
// writer may still be writing). err is io.EOF at the end of the data. It stops at once when
// *left goes negative, so one endless line cannot be read to its end.
func readBoundedLine(br *bufio.Reader, max int, left *int64) (line []byte, tooLong, terminated bool, err error) {
	var buf []byte
	for {
		chunk, rerr := br.ReadSlice('\n')
		*left -= int64(len(chunk))
		if rerr == nil {
			terminated = true
			chunk = bytes.TrimSuffix(chunk, []byte("\n"))
			chunk = bytes.TrimSuffix(chunk, []byte("\r"))
		}
		if !tooLong {
			if len(buf)+len(chunk) > max {
				tooLong = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch {
		case rerr == nil:
			return buf, tooLong, true, nil
		case errors.Is(rerr, bufio.ErrBufferFull):
			if *left < 0 {
				return buf, tooLong, false, nil
			}
			continue
		default:
			return buf, tooLong, false, rerr
		}
	}
}

type sqlLineResult int

const (
	sqlLineBad sqlLineResult = iota
	sqlLineOK
	sqlLineNewer
)

func strictJSON(b []byte, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		return false
	}
	_, err := dec.Token() // exactly one value
	return err == io.EOF
}

// decodeSQLLine checks one line against everything the writer guarantees.
func decodeSQLLine(b []byte) (SQLSample, sqlLineResult) {
	// The version is read loosely first: a newer writer may add fields the strict decode below refuses, and that is "newer", not "damaged".
	var head struct {
		V int `json:"v"`
	}
	if json.Unmarshal(b, &head) == nil && head.V > 1 {
		return SQLSample{}, sqlLineNewer
	}
	var l sqlLine
	if !strictJSON(b, &l) {
		return SQLSample{}, sqlLineBad
	}
	limit, known := bbsql.SampleKinds[l.Kind]
	if l.V != 1 || !known || l.T.IsZero() {
		return SQLSample{}, sqlLineBad
	}
	s := SQLSample{T: l.T.UTC(), Kind: l.Kind, Truncated: l.Truncated}
	rows := bytes.TrimSpace(l.Rows)
	if len(rows) == 0 || bytes.Equal(rows, []byte("null")) {
		return s, sqlLineOK // the sampler had nothing to say this tick
	}
	ok := false
	switch l.Kind {
	case "requests":
		ok = strictJSON(rows, &s.Requests) && len(s.Requests) <= limit
		for _, r := range s.Requests {
			ok = ok && bbsql.ValidWaitType(r.WaitType)
		}
	case "waits":
		ok = strictJSON(rows, &s.Waits) && len(s.Waits) <= limit
		for _, r := range s.Waits {
			ok = ok && bbsql.ValidWaitType(r.WaitType)
		}
	case "schedulers":
		var v bbsql.Schedulers
		if ok = strictJSON(rows, &v); ok {
			s.Schedulers = &v
		}
	case "semaphores":
		ok = strictJSON(rows, &s.Semaphores) && len(s.Semaphores) <= limit
	case "tempdb":
		var v bbsql.Tempdb
		if ok = strictJSON(rows, &v); ok {
			s.Tempdb = &v
		}
	case "filestats":
		ok = strictJSON(rows, &s.FileStats) && len(s.FileStats) <= limit
	case "ag":
		ok = strictJSON(rows, &s.AG) && len(s.AG) <= limit
		for _, r := range s.AG {
			ok = ok && bbsql.ValidSyncState(r.SyncState)
		}
	}
	if !ok {
		return SQLSample{}, sqlLineBad
	}
	return s, sqlLineOK
}

// OpenSQLFile opens sql/<name> inside the recordings folder as a plain file (the same
// containment rules as a recording file). A folder this account may not read comes back as
// an error that satisfies errors.Is(err, fs.ErrPermission), which ReadSQLSamples reports as Locked.
func (s *FolderSource) OpenSQLFile(ctx context.Context, name string) (io.ReadCloser, error) {
	if !ValidSQLFileName(name) {
		return nil, readErr(ReadBadData, "That is not a SQL sample file name.", nil)
	}
	da, ok := s.Access.(DirAccess)
	if !ok {
		// A test access or another file system: it has no SQL folder to offer.
		return nil, readErr(ReadNotFound, "No SQL samples are in that "+s.place()+".", fs.ErrNotExist)
	}
	sub := DirAccess{Dir: filepath.Join(da.Dir, SQLDataSubdir), Trusted: da.Trusted}
	f, err := openFileCtx(ctx, sub, name, s.sourceSlots())
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, readErr(ReadUnreachable, "The SQL samples are locked to administrators on this "+s.place()+". Read this Blackbox by its address to see them.", err)
		}
		return nil, s.classifyFile(err, name)
	}
	return f, nil
}

// OpenSQLFile opens /sql/files/<name>.
func (s *HTTPSource) OpenSQLFile(ctx context.Context, name string) (io.ReadCloser, error) {
	if !ValidSQLFileName(name) {
		return nil, readErr(ReadBadData, "That is not a SQL sample file name.", nil)
	}
	rc, _, rerr := s.open(ctx, "/sql/files/"+name)
	if rerr != nil {
		return nil, rerr
	}
	return rc, nil
}
