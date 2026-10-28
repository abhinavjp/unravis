package setup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Shortcut is a Windows shortcut (.lnk) to a program. Bytes writes it in the Shell Link format ([MS-SHLLINK]) without COM: a header,
// the target as a local path (with its Unicode form, so folders with non-English names work), and the working folder, description and
// icon as Unicode strings.
type Shortcut struct {
	Target      string
	Args        string
	WorkDir     string
	Description string
	Icon        string
	IconIndex   int32
}

const (
	lnkHasLinkInfo     = 0x00000002
	lnkHasName         = 0x00000004
	lnkHasWorkingDir   = 0x00000010
	lnkHasArguments    = 0x00000020
	lnkHasIconLocation = 0x00000040
	lnkIsUnicode       = 0x00000080
)

var lnkCLSID = []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}

// Bytes is the shortcut file.
func (s Shortcut) Bytes() ([]byte, error) {
	target := filepath.Clean(s.Target)
	if len(target) < 3 || target[1] != ':' || target[2] != '\\' {
		return nil, errors.New("a shortcut needs the full path of its program")
	}
	flags := uint32(lnkHasLinkInfo | lnkIsUnicode)
	var strs [][]uint16
	add := func(flag uint32, v string) {
		if v != "" {
			flags |= flag
			strs = append(strs, utf16.Encode([]rune(v)))
		}
	}
	// StringData comes in this order: name, relative path, working folder, arguments, icon.
	add(lnkHasName, s.Description)
	add(lnkHasWorkingDir, s.WorkDir)
	add(lnkHasArguments, s.Args)
	add(lnkHasIconLocation, s.Icon)

	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	// ShellLinkHeader (76 bytes).
	w(uint32(0x4C))
	b.Write(lnkCLSID)
	w(flags)
	w(uint32(0x20)) // FILE_ATTRIBUTE_ARCHIVE
	w([3]uint64{})  // creation, access and write times: unknown
	w(uint32(0))    // file size: unknown
	w(s.IconIndex)
	w(uint32(1)) // SW_SHOWNORMAL
	w(uint16(0)) // no hot key
	w(uint16(0))
	w(uint32(0))
	w(uint32(0))

	b.Write(linkInfo(target))

	for _, u := range strs {
		if len(u) > 0xFFFF {
			return nil, errors.New("a shortcut text is too long")
		}
		w(uint16(len(u)))
		w(u)
	}
	w(uint32(0)) // TerminalBlock
	return b.Bytes(), nil
}

// linkInfo is the LinkInfo structure for a file on a fixed local drive, with the Unicode path offsets (header size 0x24).
func linkInfo(path string) []byte {
	const headerSize = 0x24
	volumeID := []byte{0x11, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0x10, 0, 0, 0, 0} // size 17, DRIVE_FIXED, serial 0, empty label
	ansi := []byte(ansiPath(path) + "\x00")
	suffix := []byte{0}
	var uni bytes.Buffer
	_ = binary.Write(&uni, binary.LittleEndian, utf16.Encode([]rune(path+"\x00")))
	uniSuffix := []byte{0, 0}

	volOff := uint32(headerSize)
	baseOff := volOff + uint32(len(volumeID))
	suffixOff := baseOff + uint32(len(ansi))
	baseUniOff := suffixOff + uint32(len(suffix))
	suffixUniOff := baseUniOff + uint32(uni.Len())
	size := suffixUniOff + uint32(len(uniSuffix))

	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	w(size)
	w(uint32(headerSize))
	w(uint32(1)) // VolumeIDAndLocalBasePath
	w(volOff)
	w(baseOff)
	w(uint32(0)) // no network location
	w(suffixOff)
	w(baseUniOff)
	w(suffixUniOff)
	b.Write(volumeID)
	b.Write(ansi)
	b.Write(suffix)
	b.Write(uni.Bytes())
	b.Write(uniSuffix)
	return b.Bytes()
}

// ansiPath is the path with any character outside ASCII replaced by '?'; Windows reads the Unicode path when it is there.
func ansiPath(p string) string {
	var sb strings.Builder
	for _, r := range p {
		if r < 0x80 {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('?')
		}
	}
	return sb.String()
}

// ReadShortcutTarget reads back the Unicode target path of a shortcut Bytes wrote (for tests and for repair).
func ReadShortcutTarget(data []byte) (string, error) {
	if len(data) < 0x4C+0x24 || binary.LittleEndian.Uint32(data) != 0x4C || !bytes.Equal(data[4:20], lnkCLSID) {
		return "", errors.New("not a shortcut")
	}
	if binary.LittleEndian.Uint32(data[20:])&lnkHasLinkInfo == 0 {
		return "", errors.New("the shortcut has no local path")
	}
	li := data[0x4C:]
	size := binary.LittleEndian.Uint32(li)
	if int(size) > len(li) || binary.LittleEndian.Uint32(li[4:]) < 0x24 {
		return "", errors.New("the shortcut has no Unicode path")
	}
	off := binary.LittleEndian.Uint32(li[28:])
	var u []uint16
	for i := int(off); i+1 < int(size); i += 2 {
		c := binary.LittleEndian.Uint16(li[i:])
		if c == 0 {
			return string(utf16.Decode(u)), nil
		}
		u = append(u, c)
	}
	return "", errors.New("the shortcut path is cut off")
}
