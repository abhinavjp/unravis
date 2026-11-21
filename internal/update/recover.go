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
func SetupCanRecover(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return false
	}
	m := []byte(RecoverMarker)
	buf := make([]byte, 1<<20+len(m))
	carry := 0
	for {
		n, err := f.Read(buf[carry : carry+1<<20])
		if bytes.Contains(buf[:carry+n], m) {
			return true
		}
		if err != nil {
			return false // end of file (or a read error) without the text
		}
		keep := len(m) - 1
		if carry+n < keep {
			keep = carry + n
		}
		copy(buf, buf[carry+n-keep:carry+n])
		carry = keep
	}
}
