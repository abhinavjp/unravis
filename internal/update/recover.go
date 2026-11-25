package update

import (
	"bytes"
	"os"
)

// RecoverMarker is text that only a setup program that knows --recover contains (its help line). The app checks for it before it
// starts `unravis-setup.exe --recover`: an older setup would not know the flag and would open its window. A test in cmd/unravis-setup
// keeps the help line and this text equal.
const RecoverMarker = "a setup run that was cut off (power loss"

// SetupCanRecover says whether the setup program at path knows --recover. It reads the file in pieces (setup carries the whole program
// inside, so it is large) and keeps nothing.
func SetupCanRecover(path string) bool { return FileHasAll(path, RecoverMarker) }

// FileHasAll says whether the regular file at path holds every one of the texts. It reads the file in pieces of 1 MiB and keeps one
// piece in memory, however large the file is (a setup program is tens of MB), and it stops as soon as every text has been seen. A text
// that runs over the end of one piece is found. With no texts it is true for any regular file that opens (nothing is read).
func FileHasAll(path string, texts ...string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return false
	}
	need := make([][]byte, 0, len(texts))
	keep := 0
	for _, t := range texts {
		need = append(need, []byte(t))
		keep = max(keep, len(t)-1)
	}
	if len(need) == 0 {
		return true
	}
	const piece = 1 << 20
	buf := make([]byte, piece+keep)
	carry := 0
	for {
		n, err := f.Read(buf[carry : carry+piece])
		rest := need[:0]
		for _, m := range need {
			if !bytes.Contains(buf[:carry+n], m) {
				rest = append(rest, m)
			}
		}
		need = rest
		if len(need) == 0 {
			return true
		}
		if err != nil {
			return false // end of file (or a read error) before every text was seen
		}
		k := min(keep, carry+n)
		copy(buf, buf[carry+n-k:carry+n])
		carry = k
	}
}
