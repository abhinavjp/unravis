package setup

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// The PATH of a PC is one text value in the registry. These functions edit that text and nothing else, so they run and are tested on
// every system. They keep every entry they do not own byte for byte (%VAR% entries, empty segments, odd spacing).

// Where the PATH lives.
const (
	EnvKeyUser    = `Environment`
	EnvKeyMachine = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	EnvPathName   = "Path"
	// MaxPathValue is the longest PATH value, in characters, that Windows accepts.
	MaxPathValue = 32767
)

// ErrPathTooLong is what addToPathValue returns when the new value would not fit.
var ErrPathTooLong = errors.New("setup: the PATH value would be longer than Windows allows")

// envKeyFor is the registry key that holds the PATH for the hive.
func envKeyFor(root Root) string {
	if root == HKLM {
		return EnvKeyMachine
	}
	return EnvKeyUser
}

// cleanPathEntry is one PATH entry as setup would write it: no spaces or quotes around it, cleaned, no trailing backslash (a drive
// root such as C:\ keeps its one).
func cleanPathEntry(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = filepath.Clean(s)
	if strings.HasSuffix(s, `\`) && !(len(s) == 3 && s[1] == ':') {
		s = strings.TrimSuffix(s, `\`)
	}
	return s
}

// pathEntryKey is the form used to compare two PATH entries: the same folder written with other case, quotes or a trailing
// backslash gives the same key. An empty entry gives "".
func pathEntryKey(s string) string {
	return strings.ToLower(cleanPathEntry(s))
}

// checkPathEntry refuses an entry setup would not write: it must be a full path, and must not hold anything that changes how the
// PATH is read (a separator, a quote, a %VAR%, a parent folder part).
func checkPathEntry(entry string) error {
	e := strings.TrimSpace(entry)
	switch {
	case e == "":
		return errors.New("the PATH entry is empty")
	case strings.ContainsAny(e, `;"`):
		return fmt.Errorf("the PATH entry %q holds a semicolon or a quote", entry)
	case strings.Contains(e, "%"):
		return fmt.Errorf("the PATH entry %q holds a percent sign", entry)
	case !filepath.IsAbs(e):
		return fmt.Errorf("the PATH entry %q is not a full path", entry)
	}
	for _, part := range strings.FieldsFunc(e, func(r rune) bool { return r == '\\' || r == '/' }) {
		if part == ".." {
			return fmt.Errorf("the PATH entry %q holds a '..' part", entry)
		}
	}
	return nil
}

// addToPathValue puts entry at the end of the PATH value. When it is already there the first copy stays and later copies are dropped
// (changed says that happened). added says the entry was not there. A value that would pass MaxPathValue is refused and returned
// unchanged.
func addToPathValue(value, entry string) (out string, added bool, changed bool, err error) {
	key := pathEntryKey(entry)
	if key == "" {
		return value, false, false, errors.New("the PATH entry is empty")
	}
	segs := strings.Split(value, ";")
	kept := make([]string, 0, len(segs))
	found := false
	for _, s := range segs {
		if pathEntryKey(s) == key {
			if found {
				changed = true
				continue
			}
			found = true
		}
		kept = append(kept, s)
	}
	if found {
		if !changed {
			return value, false, false, nil
		}
		return strings.Join(kept, ";"), false, true, nil
	}
	e := cleanPathEntry(entry)
	switch {
	case value == "":
		out = e
	case strings.HasSuffix(value, ";"):
		out = value + e
	default:
		out = value + ";" + e
	}
	if len(out) > MaxPathValue {
		return value, false, false, ErrPathTooLong
	}
	return out, true, true, nil
}

// removeFromPathValue removes every copy of entry and the semicolon that went with it. Everything else stays byte for byte.
func removeFromPathValue(value, entry string) (out string, removed bool) {
	key := pathEntryKey(entry)
	if key == "" {
		return value, false
	}
	segs := strings.Split(value, ";")
	kept := make([]string, 0, len(segs))
	for _, s := range segs {
		if pathEntryKey(s) == key {
			removed = true
			continue
		}
		kept = append(kept, s)
	}
	if !removed {
		return value, false
	}
	return strings.Join(kept, ";"), true
}
