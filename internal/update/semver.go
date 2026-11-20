// Package update holds the trust rules for the auto-updater (docs/auto-updater/SPEC.md).
// It has no network code: it only parses and verifies signed update manifests, so the same
// checks can be used by the app, the Blackbox service, the installer and the release tool.
package update

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version: vMAJOR.MINOR.PATCH with an optional -pre.release part.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 []string // dot-separated pre-release identifiers, empty for a final release
}

var semverRE = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// ParseVersion reads "1.2.3", "v1.2.3" or "1.2.3-rc.47". Build metadata (+x) is refused: it has no place in a release tag.
func ParseVersion(s string) (Version, error) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("not a version: %q", s)
	}
	var v Version
	var err error
	if v.Major, err = strconv.ParseUint(m[1], 10, 32); err != nil {
		return Version{}, errors.New("major version too large")
	}
	if v.Minor, err = strconv.ParseUint(m[2], 10, 32); err != nil {
		return Version{}, errors.New("minor version too large")
	}
	if v.Patch, err = strconv.ParseUint(m[3], 10, 32); err != nil {
		return Version{}, errors.New("patch version too large")
	}
	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
		for _, id := range v.Pre {
			if isNumeric(id) && len(id) > 1 && id[0] == '0' {
				return Version{}, fmt.Errorf("pre-release number %q has a leading zero", id)
			}
		}
	}
	return v, nil
}

// IsPrerelease is true for 1.2.3-rc.1.
func (v Version) IsPrerelease() bool { return len(v.Pre) > 0 }

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Compare returns -1, 0 or 1 by semantic version precedence (1.0.0-rc.2 < 1.0.0-rc.10 < 1.0.0).
func Compare(a, b Version) int {
	for _, p := range [][2]uint64{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		x, y := a.Pre[i], b.Pre[i]
		xn, yn := isNumeric(x), isNumeric(y)
		switch {
		case xn && yn:
			// Numbers have no leading zeros (checked on parse), so a longer one is larger, and equal lengths compare as text.
			if len(x) != len(y) {
				if len(x) < len(y) {
					return -1
				}
				return 1
			}
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		case xn:
			return -1
		case yn:
			return 1
		default:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		}
	}
	switch {
	case len(a.Pre) < len(b.Pre):
		return -1
	case len(a.Pre) > len(b.Pre):
		return 1
	}
	return 0
}
