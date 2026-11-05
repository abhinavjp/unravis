package shelllink

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
	lnkHasLinkTargetIDList = 0x00000001
	lnkHasLinkInfo         = 0x00000002
	lnkHasName             = 0x00000004
	lnkHasRelativePath     = 0x00000008
	lnkHasWorkingDir       = 0x00000010
	lnkHasArguments        = 0x00000020
	lnkHasIconLocation     = 0x00000040
	lnkIsUnicode           = 0x00000080
)

var lnkCLSID = []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}

// Bytes is the shortcut file.
func (s Shortcut) Bytes() ([]byte, error) {
	target := filepath.Clean(s.Target)
	if len(target) < 3 || target[1] != ':' || target[2] != '\\' {
		return nil, errors.New("a shortcut needs the full path of its program")
	}
	flags := uint32(lnkHasLinkTargetIDList | lnkHasLinkInfo | lnkIsUnicode)
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

	idl, err := idList(target)
	if err != nil {
		return nil, err
	}
	b.Write(idl)
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

// idList is the LinkTargetIDList: IDListSize, then the item IDs (My Computer, the drive, one item per folder, the file) and the
// terminal ID. Current Windows resolves TargetPath from this list; LinkInfo alone reads back as an empty target.
func idList(path string) ([]byte, error) {
	var items bytes.Buffer
	// "My Computer": a root folder item holding the CLSID of This PC.
	items.Write([]byte{20, 0, 0x1F, 0x50, 0xE0, 0x4F, 0xD0, 0x20, 0xEA, 0x3A, 0x69, 0x10, 0xA2, 0xD8, 0x08, 0x00, 0x2B, 0x30, 0x30, 0x9D})
	// The drive, e.g. "C:\": type 0x2F, the name, padded to 25 bytes.
	drive := make([]byte, 25)
	binary.LittleEndian.PutUint16(drive, 25)
	drive[2] = 0x2F
	copy(drive[3:], path[:3])
	items.Write(drive)

	parts := strings.Split(path[3:], `\`)
	for i, p := range parts {
		if p == "" {
			return nil, errors.New("a shortcut needs the full path of its program")
		}
		item := fileEntryItem(p, i == len(parts)-1)
		if len(item) > 0xFFFF {
			return nil, errors.New("a shortcut path is too long")
		}
		items.Write(item)
	}
	items.Write([]byte{0, 0}) // terminal ID
	if items.Len() > 0xFFFF {
		return nil, errors.New("a shortcut path is too long")
	}
	out := make([]byte, 2, 2+items.Len())
	binary.LittleEndian.PutUint16(out, uint16(items.Len()))
	return append(out, items.Bytes()...), nil
}

// fatDate and fatTime are a fixed timestamp (2020-01-01 00:00), so the bytes are the same every time.
const (
	fatDate = (2020-1980)<<9 | 1<<5 | 1
	fatTime = 0
)

// fileEntryItem is a file-system item ID for a folder or a file: the ANSI name (non-ASCII characters as '?') and an extension block
// 0xBEEF0004 (version 3) with the full Unicode name.
func fileEntryItem(name string, isFile bool) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	w(uint16(0)) // size, set below
	typ, attrs := byte(0x31), uint16(0x10)
	if isFile {
		typ, attrs = 0x32, 0x20
	}
	ascii := ansiPath(name) == name
	if !ascii {
		typ |= 0x04 // the primary name is Unicode
	}
	b.WriteByte(typ)
	b.WriteByte(0)
	w(uint32(0)) // file size: unknown
	w(uint16(fatDate))
	w(uint16(fatTime))
	w(attrs)
	uni := utf16.Encode([]rune(name))
	if ascii {
		b.WriteString(name)
		b.WriteByte(0)
		if b.Len()%2 != 0 {
			b.WriteByte(0)
		}
	} else {
		w(uni)
		w(uint16(0))
	}
	extOff := b.Len()
	extSize := 0x14 + 2*(len(uni)+1) + 2
	w(uint16(extSize))
	w(uint16(3))          // version
	w(uint32(0xBEEF0004)) // signature
	w(uint16(fatDate))    // created
	w(uint16(fatTime))
	w(uint16(fatDate)) // last accessed
	w(uint16(fatTime))
	w(uint16(0x14)) // offset of the long name in the block
	w(uint16(0))
	w(uni)
	w(uint16(0))
	w(uint16(extOff)) // offset of this block in the item
	out := b.Bytes()
	binary.LittleEndian.PutUint16(out, uint16(len(out)))
	return out
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
	if len(data) < 0x4C || binary.LittleEndian.Uint32(data) != 0x4C || !bytes.Equal(data[4:20], lnkCLSID) {
		return "", errors.New("not a shortcut")
	}
	flags := binary.LittleEndian.Uint32(data[20:])
	if flags&lnkHasLinkInfo == 0 {
		return "", errors.New("the shortcut has no local path")
	}
	pos := 0x4C
	if flags&lnkHasLinkTargetIDList != 0 {
		if pos+2 > len(data) {
			return "", errors.New("the shortcut is cut off")
		}
		pos += 2 + int(binary.LittleEndian.Uint16(data[pos:]))
	}
	if pos+0x24 > len(data) {
		return "", errors.New("the shortcut is cut off")
	}
	li := data[pos:]
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

// ReadShortcutArgs reads back the command-line arguments of a shortcut Bytes wrote ("" when it has none). ReadShortcutTarget stops at
// the LinkInfo; this walks on through the StringData, checking every read against the data, so a cut-off or garbled file is an error.
func ReadShortcutArgs(data []byte) (string, error) {
	if len(data) < 0x4C || binary.LittleEndian.Uint32(data) != 0x4C || !bytes.Equal(data[4:20], lnkCLSID) {
		return "", errors.New("not a shortcut")
	}
	flags := binary.LittleEndian.Uint32(data[20:])
	pos := 0x4C
	if flags&lnkHasLinkTargetIDList != 0 {
		if pos+2 > len(data) {
			return "", errors.New("the shortcut is cut off")
		}
		pos += 2 + int(binary.LittleEndian.Uint16(data[pos:]))
	}
	if flags&lnkHasLinkInfo != 0 {
		if pos < 0 || pos+4 > len(data) {
			return "", errors.New("the shortcut is cut off")
		}
		pos += int(binary.LittleEndian.Uint32(data[pos:]))
	}
	// StringData comes in this order: name, relative path, working folder, arguments, icon.
	for _, flag := range []uint32{lnkHasName, lnkHasRelativePath, lnkHasWorkingDir, lnkHasArguments, lnkHasIconLocation} {
		if flags&flag == 0 {
			continue
		}
		if pos < 0 || pos+2 > len(data) {
			return "", errors.New("the shortcut is cut off")
		}
		n := int(binary.LittleEndian.Uint16(data[pos:]))
		pos += 2
		size := n
		if flags&lnkIsUnicode != 0 {
			size = n * 2
		}
		if pos+size > len(data) {
			return "", errors.New("the shortcut is cut off")
		}
		if flag == lnkHasArguments {
			if flags&lnkIsUnicode == 0 {
				return string(data[pos : pos+size]), nil
			}
			u := make([]uint16, n)
			for i := range u {
				u[i] = binary.LittleEndian.Uint16(data[pos+2*i:])
			}
			return string(utf16.Decode(u)), nil
		}
		pos += size
	}
	return "", nil
}
