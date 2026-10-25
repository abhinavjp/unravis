package importer

import (
	"bytes"
	"io"
	"strings"
)

var archiveMagics = [][]byte{
	[]byte("PK\x03\x04"),               // zip
	[]byte("PK\x05\x06"),               // zip, empty
	[]byte("PK\x07\x08"),               // zip, spanned
	{0x1f, 0x8b},                       // gzip
	{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}, // 7z
	[]byte("Rar!\x1a\x07"),             // rar
	{0xfd, '7', 'z', 'X', 'Z', 0x00},   // xz
	[]byte("MSCF\x00\x00\x00\x00"),     // cab
}

// Sniff refuses archives by magic bytes (zip, gzip, 7z, rar, xz, bzip2, cab)
// with a QuarantineError{ReasonArchive}: the user must extract first. Nothing
// is ever decompressed. Plain text returns nil.
//
// It also refuses the binary families that cannot be read in pure Go (see
// SniffNamed), by content alone.
func Sniff(head []byte) error { return SniffNamed(head, "") }

// binarySniffBytes bounds how much of the head the binary checks look at. Every
// signature is in the first few dozen bytes; the NUL probe needs a little more.
const binarySniffBytes = 4 << 10

// binaryExts maps a file extension to its binary family. An extension is only a
// tie-break, see SniffNamed.
var binaryExts = map[string]Reason{
	".blg":      ReasonBinaryBLG,
	".etl":      ReasonBinaryETL,
	".nettrace": ReasonBinaryNettrace,
	".evtx":     ReasonBinaryEVTX,
	".bson":     ReasonBinaryBSON,
	".xel":      ReasonBinaryXEL,
	".rdb":      ReasonBinaryRDB,
}

// SniffNamed is Sniff with the upload's file name. Archives are refused first
// (ReasonArchive). Then a binary family is refused with its own reason, from the
// first few KiB only, never echoing the name or any byte:
//
//   - by content: a known signature (evtx "ElfFile", nettrace "Nettrace", Redis
//     "REDIS" plus a version, perfmon "PERF", or a BSON document header). A file
//     named .csv that starts with one is still refused as its real family.
//   - by name, only as a tie-break: a file whose extension names a family (.blg,
//     .etl, .xel, ...) AND whose head is binary (a NUL byte in the first KiB, no
//     UTF-16 byte order mark). A text file named .blg is never refused for its
//     name; it goes on to the importers, which judge it by content.
//
// name may be empty (content only). It is compared against a fixed table and
// never returned.
func SniffNamed(head []byte, name string) error {
	if len(head) > HeadSize {
		head = head[:HeadSize]
	}
	for _, m := range archiveMagics {
		if bytes.HasPrefix(head, m) {
			return &QuarantineError{Reason: ReasonArchive}
		}
	}
	// bzip2: "BZh" then a block-size digit 1..9.
	if len(head) >= 4 && bytes.HasPrefix(head, []byte("BZh")) && head[3] >= '1' && head[3] <= '9' {
		return &QuarantineError{Reason: ReasonArchive}
	}
	if len(head) > binarySniffBytes {
		head = head[:binarySniffBytes]
	}
	if r, ok := binaryByMagic(head); ok {
		return &QuarantineError{Reason: r}
	}
	if r, ok := binaryExts[fileExt(name)]; ok && looksBinary(head) {
		return &QuarantineError{Reason: r}
	}
	return nil
}

// fileExt is the lower-case extension of the last path element, with the
// trailing spaces and dots Windows ignores removed. "" when there is none.
func fileExt(name string) string {
	name = strings.TrimRight(name, " .")
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return strings.ToLower(name[i:])
}

// looksBinary reports a NUL byte in the head of something that does not start
// with a UTF-16 byte order mark (UTF-16 text exports from Windows are legitimate).
func looksBinary(head []byte) bool {
	if bytes.HasPrefix(head, []byte{0xFF, 0xFE}) || bytes.HasPrefix(head, []byte{0xFE, 0xFF}) {
		return false
	}
	if len(head) > 1<<10 {
		head = head[:1<<10]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// isTextByte is a byte that plain text may hold: tab, line breaks, printable ASCII.
func isTextByte(b byte) bool { return b == '\t' || b == '\n' || b == '\r' || (b >= 0x20 && b <= 0x7e) }

// binaryByMagic recognises a binary family by its signature.
func binaryByMagic(head []byte) (Reason, bool) {
	switch {
	case bytes.HasPrefix(head, []byte("ElfFile\x00")):
		return ReasonBinaryEVTX, true
	case bytes.HasPrefix(head, []byte("Nettrace")) &&
		(bytes.IndexByte(head[:min(len(head), 16)], 0) >= 0 || bytes.Contains(head[:min(len(head), 40)], []byte("!FastSerialization"))):
		// The word alone could start a text file; the real header has a NUL or the stream name next.
		return ReasonBinaryNettrace, true
	case isRDB(head):
		return ReasonBinaryRDB, true
	case bytes.HasPrefix(head, []byte("PERF")) && bytes.IndexByte(head[:min(len(head), 16)], 0) >= 0:
		return ReasonBinaryBLG, true
	case isBSON(head):
		return ReasonBinaryBSON, true
	}
	return "", false
}

// isRDB: "REDIS", four version digits, then a byte plain text would not hold
// (the snapshot goes straight on with an opcode). A bare 9-byte head counts.
func isRDB(head []byte) bool {
	if len(head) < 9 || !bytes.HasPrefix(head, []byte("REDIS")) {
		return false
	}
	for _, c := range head[5:9] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(head) == 9 || !isTextByte(head[9])
}

// isBSON: a BSON document starts with its int32 length (little endian, at most
// 16 MiB so the top byte is 0), then an element type byte and a printable field
// name ended by NUL. mongodump .bson and FTDC files start this way.
func isBSON(head []byte) bool {
	if len(head) < 7 || head[3] != 0 {
		return false
	}
	size := uint32(head[0]) | uint32(head[1])<<8 | uint32(head[2])<<16 // the top byte is 0: under 16 MiB
	if size < 8 {
		return false
	}
	t := head[4]
	if !(t >= 0x01 && t <= 0x13) && t != 0x7f && t != 0xff {
		return false
	}
	end := bytes.IndexByte(head[5:], 0)
	if end < 1 || end > 64 {
		return false
	}
	for _, c := range head[5 : 5+end] {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// LimitReader returns a reader that yields at most max bytes and then fails
// with *LimitError if the source has more. Exactly max bytes is fine. A max
// of zero or less means no limit.
func LimitReader(r io.Reader, max int64) io.Reader {
	if max <= 0 {
		return r
	}
	return &limitReader{r: r, max: max}
}

// maxEmptyReads is how many (0, nil) reads the probe tolerates before giving up
// with io.ErrNoProgress, so a broken reader cannot hang an import.
const maxEmptyReads = 100

type limitReader struct {
	r    io.Reader
	max  int64
	n    int64
	over bool
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.over {
		return 0, &LimitError{Limit: "bytes", Max: l.max, At: l.max + 1}
	}
	if len(p) == 0 {
		return 0, nil
	}
	if l.n >= l.max {
		// Probe for one more byte to tell "exactly max" from "over".
		var one [1]byte
		for empty := 0; empty < maxEmptyReads; empty++ {
			n, err := l.r.Read(one[:])
			if n > 0 {
				l.over = true
				return 0, &LimitError{Limit: "bytes", Max: l.max, At: l.max + 1}
			}
			if err != nil {
				return 0, err
			}
		}
		return 0, io.ErrNoProgress
	}
	if rest := l.max - l.n; int64(len(p)) > rest {
		p = p[:rest]
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	return n, err
}
