// Package evtx reads Windows event channels through wevtapi and writes the rows
// the win-events importer reads.
//
// What is read is fixed: time, computer, channel, provider, event id, level and
// record id from the System section of each event, and, for the WAS provider
// only, the AppPoolID data value. The rendered message text and every other
// EventData value are never requested, so they cannot reach the output. The
// row writer and the renderer in this package build on any OS so they are
// tested everywhere; only the calls into wevtapi sit in query_windows.go.
package evtx

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// RowCap is the most rows one Query writes. It matches the win-events importer
// limit (2 M rows), so a spool that hits it still imports.
const RowCap = 2_000_000

// ErrRowCap is returned by RowWriter.Write when the cap is reached.
var ErrRowCap = errors.New("event row cap reached")

// ErrNotSupportedOS is returned where there is no event log API.
var ErrNotSupportedOS = errors.New("event log reading is not supported on this OS")

// Header is the column list of the win-events CSV, in order.
var Header = []string{"TimeCreatedUtc", "MachineName", "LogName", "ProviderName", "Id", "Level", "AppPool"}

// maxTextRunes bounds every text cell. The importer clips attributes further.
const maxTextRunes = 256

// WASProvider is the one provider whose AppPoolID value is read.
const WASProvider = "Microsoft-Windows-WAS"

// IsWAS reports whether provider is the WAS provider (case-insensitive).
func IsWAS(provider string) bool { return strings.EqualFold(provider, WASProvider) }

// Record is one event reduced to what the importer reads.
type Record struct {
	Time     time.Time
	Computer string
	Channel  string
	Provider string
	EventID  uint32
	Level    uint8
	RecordID uint64
	// AppPool is set for WAS events only.
	AppPool string
}

// RowWriter writes Records as the Get-WinEvent CSV. Every cell is quoted, so no
// cell value can start a new column or row. Call Flush when done.
type RowWriter struct {
	w    *bufio.Writer
	rows int
	cap  int
	last uint64
	err  error
}

// NewRowWriter starts a CSV on w and writes the header. The row cap is RowCap.
func NewRowWriter(w io.Writer) *RowWriter { return newRowWriter(w, RowCap) }

func newRowWriter(w io.Writer, rowCap int) *RowWriter {
	rw := &RowWriter{w: bufio.NewWriterSize(w, 64<<10), cap: rowCap}
	rw.writeCells(Header)
	return rw
}

// Rows is the number of data rows written.
func (rw *RowWriter) Rows() int { return rw.rows }

// LastRecordID is the highest record ID written, or 0 when none.
func (rw *RowWriter) LastRecordID() uint64 { return rw.last }

// Write adds one row. It returns ErrRowCap, and writes nothing, once the cap is
// reached. Any earlier write error is returned again.
func (rw *RowWriter) Write(r Record) error {
	if rw.err != nil {
		return rw.err
	}
	if rw.rows >= rw.cap {
		return ErrRowCap
	}
	rw.writeCells([]string{
		r.Time.UTC().Format("2006-01-02T15:04:05.0000000Z"),
		cleanText(r.Computer),
		cleanText(r.Channel),
		cleanText(r.Provider),
		strconv.FormatUint(uint64(r.EventID), 10),
		strconv.FormatUint(uint64(r.Level), 10),
		cleanText(r.AppPool),
	})
	if rw.err != nil {
		return rw.err
	}
	rw.rows++
	if r.RecordID > rw.last {
		rw.last = r.RecordID
	}
	return nil
}

// Flush writes buffered rows to the underlying writer.
func (rw *RowWriter) Flush() error {
	if rw.err != nil {
		return rw.err
	}
	rw.err = rw.w.Flush()
	return rw.err
}

func (rw *RowWriter) writeCells(cells []string) {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(c, `"`, `""`))
		b.WriteByte('"')
	}
	b.WriteString("\r\n")
	if _, err := rw.w.WriteString(b.String()); err != nil {
		rw.err = err
	}
}

// cleanText makes a value safe for one CSV cell: invalid UTF-8 becomes "?",
// control and format characters (line breaks, bidi marks, zero-width marks) are
// dropped, and the length is capped.
func cleanText(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ToValidUTF8(s, "?")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			continue
		}
		if n++; n > maxTextRunes {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// NextWatermark returns the record ID to store after a pull. A pull that wrote
// no row keeps the old watermark; one that wrote rows moves it to the last
// record written, which is the highest ID because Query reads oldest first.
func NextWatermark(prev, last uint64, rows int) uint64 {
	if rows <= 0 || last == 0 {
		return prev
	}
	return last
}
