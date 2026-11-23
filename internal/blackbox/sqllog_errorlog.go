package blackbox

import (
	"bytes"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// The ERRORLOG reader. See sqllog.go for the rules: numbers and fixed words only.

// logRead is the result of one forward read of one ERRORLOG.
type logRead struct {
	events     []SQLLogEvent
	cur        sqlLogCursor
	truncated  bool
	unreadable bool
	lost       uint64
}

var (
	// A line: "2026-11-23 10:15:32.12 spid53      <message>". The source word is skipped.
	logLineRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})(?:\.\d+)?\s+\S+\s+(.*)$`)
	// "Error: 18456, Severity: 14, State: 8."
	logErrorRe = regexp.MustCompile(`^Error: (\d{1,10}), Severity: (\d{1,3}), State: (\d{1,3})\.?\s*$`)
	// "SQL Server has encountered 3 occurrence(s) of I/O requests taking longer than
	// 15 seconds to complete on file [...] in database [...] (5)."
	logSlowIORe = regexp.MustCompile(`^SQL Server has encountered (\d{1,9}) occurrence\(s\) of I/O requests taking longer than (\d{1,6}) seconds to complete on file \[.*\] in database .*\((\d{1,5})\)\.`)
)

// classifyLogLine turns one ERRORLOG line into at most one event. Only digits are
// copied out of the text; the kind is chosen from a fixed list.
func classifyLogLine(line string, loc *time.Location) (SQLLogEvent, bool) {
	m := logLineRe.FindStringSubmatch(line)
	if m == nil {
		return SQLLogEvent{}, false // a continuation line, a banner, anything else: ignored
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], loc)
	if err != nil {
		return SQLLogEvent{}, false
	}
	ev := SQLLogEvent{T: t.UTC(), Count: 1}
	msg := m[2]
	switch {
	case logErrorRe.MatchString(msg):
		e := logErrorRe.FindStringSubmatch(msg)
		num, e1 := strconv.ParseUint(e[1], 10, 32)
		sev, e2 := strconv.ParseUint(e[2], 10, 8)
		st, e3 := strconv.ParseUint(e[3], 10, 8)
		if e1 != nil || e2 != nil || e3 != nil || sev > 25 {
			return SQLLogEvent{}, false
		}
		ev.Kind, ev.Number, ev.Severity, ev.State = SQLLogKindError, uint32(num), uint8(sev), uint8(st)
	case strings.HasPrefix(msg, "SQL Server is starting"):
		ev.Kind = SQLLogKindStartup
	case strings.HasPrefix(msg, "SQL Server is terminating"):
		ev.Kind = SQLLogKindShutdown
	case logSlowIORe.MatchString(msg):
		e := logSlowIORe.FindStringSubmatch(msg)
		n, e1 := strconv.ParseUint(e[1], 10, 32)
		secs, e2 := strconv.ParseUint(e[2], 10, 32)
		db, e3 := strconv.ParseUint(e[3], 10, 32)
		if e1 != nil || e2 != nil || e3 != nil {
			return SQLLogEvent{}, false
		}
		ev.Kind, ev.Count, ev.Seconds, ev.Database = SQLLogKindSlowIO, uint32(n), uint32(secs), uint32(db)
	default:
		return SQLLogEvent{}, false
	}
	return ev, true
}

// parseLogText classifies every line of a decoded chunk.
func parseLogText(text string, loc *time.Location) []SQLLogEvent {
	var out []SQLLogEvent
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if ev, ok := classifyLogLine(line, loc); ok {
			out = append(out, ev)
		}
	}
	return out
}

// openPlainLog opens a log file for reading: a regular file only (a link, junction
// or device is refused), read only. It returns the size and the first bytes.
func openPlainLog(path string) (f *os.File, size int64, head []byte, err error) {
	li, err := os.Lstat(path)
	if err != nil {
		return nil, 0, nil, err
	}
	if !li.Mode().IsRegular() || li.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return nil, 0, nil, fs.ErrInvalid
	}
	f, err = os.Open(path) //nolint:gosec // path comes from the SQL instance registry key and is checked by sqlLogPathOK
	if err != nil {
		return nil, 0, nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, 0, nil, fs.ErrInvalid
	}
	buf := make([]byte, sqlLogHeadBytes)
	n, _ := f.ReadAt(buf, 0)
	return f, st.Size(), buf[:n], nil
}

// isUTF16 says the file is UTF-16 little endian (a BOM, or ASCII text with a zero
// high byte). SQL Server on Windows writes the ERRORLOG that way.
func isUTF16(head []byte) bool {
	if len(head) >= 2 && head[0] == 0xFF && head[1] == 0xFE {
		return true
	}
	return len(head) >= 4 && head[0] != 0 && head[1] == 0 && head[2] != 0 && head[3] == 0
}

// headMatches says the file still begins with the bytes the cursor saw. A file that
// was cycled (a new ERRORLOG) starts with a different first line.
func headMatches(head []byte, c sqlLogCursor) bool {
	n := min(len(head), c.HeadLen)
	return bytes.Equal(head[:n], c.Head[:n])
}

func newHead(head []byte) (h [sqlLogHeadBytes]byte, n int) {
	n = copy(h[:], head)
	return h, n
}

// endPosition is where a first look starts: the end of the file, on a character
// boundary (no backfill).
func endPosition(size int64, head []byte) int64 {
	if isUTF16(head) {
		return size &^ 1
	}
	return size
}

// readChunk reads from off up to budget bytes and returns the text of the complete
// lines in it plus how many bytes those lines took. A last line that is still being
// written is left for the next poll.
func readChunk(f *os.File, off, size, budget int64, utf bool) (text string, consumed int64, more bool) {
	n := min(size-off, budget)
	if utf {
		n &^= 1
	}
	if n <= 0 {
		return "", 0, false
	}
	more = off+n < size
	buf := make([]byte, n)
	got, _ := f.ReadAt(buf, off)
	buf = buf[:got]
	if utf {
		buf = buf[:len(buf)&^1]
	}
	cut := -1
	if utf {
		for i := len(buf) - 2; i >= 0; i -= 2 {
			if buf[i] == 0x0A && buf[i+1] == 0 {
				cut = i + 2
				break
			}
		}
	} else if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		cut = i + 1
	}
	if cut < 0 {
		if more { // one line longer than the whole budget: skip it, never stall on it
			cut = len(buf)
		} else {
			return "", 0, false // an unfinished line: wait
		}
	}
	buf = buf[:cut]
	if utf {
		u := make([]uint16, 0, len(buf)/2)
		for i := 0; i+1 < len(buf); i += 2 {
			u = append(u, uint16(buf[i])|uint16(buf[i+1])<<8)
		}
		if len(u) > 0 && u[0] == 0xFEFF {
			u = u[1:]
		}
		return string(utf16.Decode(u)), int64(cut), more
	}
	return string(bytes.ToValidUTF8(buf, []byte("?"))), int64(cut), more
}

// readErrorLog reads the new complete lines of one ERRORLOG, at most budget bytes.
// It never writes the file. See sqllog.go for what happens at a first look and when
// SQL Server cycles the log (ERRORLOG becomes ERRORLOG.1).
func readErrorLog(path string, cur sqlLogCursor, budget int64, loc *time.Location) (out logRead) {
	out.cur = cur
	f, size, head, err := openPlainLog(path)
	if err != nil {
		out.unreadable = true
		return out
	}
	defer f.Close()
	if !cur.Seen {
		h, n := newHead(head)
		out.cur = sqlLogCursor{Seen: true, Offset: endPosition(size, head), Head: h, HeadLen: n}
		return out
	}
	utf := isUTF16(head)
	cycled := size < cur.Offset || (cur.HeadLen > 0 && len(head) > 0 && !headMatches(head, cur))
	if cycled {
		// SQL Server renamed the log we were reading to ERRORLOG.1: finish its tail first.
		old, osize, ohead, oerr := openPlainLog(path + ".1")
		if oerr == nil && osize >= cur.Offset && headMatches(ohead, cur) {
			text, _, more := readChunk(old, cur.Offset, osize, budget, isUTF16(ohead))
			out.events = append(out.events, parseLogText(text, loc)...)
			if more {
				out.lost++ // the rest of the old file did not fit this poll: it is skipped
			}
		} else {
			out.lost++
		}
		if old != nil {
			_ = old.Close()
		}
		cur = sqlLogCursor{Seen: true}
	}
	text, consumed, more := readChunk(f, cur.Offset, size, budget, utf)
	out.events = append(out.events, parseLogText(text, loc)...)
	h, n := newHead(head)
	out.cur = sqlLogCursor{Seen: true, Offset: cur.Offset + consumed, Head: h, HeadLen: n}
	out.truncated = more
	return out
}
