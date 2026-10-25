package localread

import "strings"

// MaxPath is the longest canonical path accepted. Long-path prefixes are
// refused outright, so this is the classic MAX_PATH limit.
const MaxPath = 260

// CheckShape reports whether p has the shape of a canonical Windows path on a
// local drive: `X:\dir\file`, backslashes only, no `.` or `..` or empty
// segment, no UNC, device or long-path prefix, no `:` after the drive letter
// (alternate data streams), no wildcard, control or reserved characters, and
// no segment ending in a dot or a space (Win32 would strip it and open a
// different name). It needs no OS, so it runs the same everywhere. A nil
// return does not make a path safe: the Windows opener still compares the
// final path, the drive type and the reparse state.
func CheckShape(p string) error {
	if len(p) >= MaxPath {
		return unsafe("path too long")
	}
	if len(p) < 4 || !isLetter(p[0]) || p[1] != ':' || p[2] != '\\' {
		return unsafe("not an absolute local drive path")
	}
	rest := p[3:]
	for _, seg := range strings.Split(rest, "\\") {
		switch seg {
		case "":
			return unsafe("empty path segment")
		case ".", "..":
			return unsafe("relative path segment")
		}
		if seg[len(seg)-1] == '.' || seg[len(seg)-1] == ' ' {
			return unsafe("segment ends with dot or space")
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			if c < 0x20 || strings.IndexByte(`<>:"/|?*`, c) >= 0 {
				if c == ':' {
					return unsafe("stream or drive separator in path")
				}
				return unsafe("reserved character in path")
			}
		}
	}
	return nil
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// baseName returns the last segment of a canonical path.
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}
