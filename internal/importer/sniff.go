package importer

import (
	"bytes"
	"io"
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
func Sniff(head []byte) error {
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
	return nil
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
