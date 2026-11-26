package blackbox

import (
	"net"
	"os"
	"strings"
)

// LocalNames is every way this PC is called on a network: its host name, and
// the addresses of its network cards. It is read at the moment of use; a failure
// gives what could be read (the loopback names are always known without it).
func LocalNames() []string {
	var out []string
	if h, err := os.Hostname(); err == nil && h != "" {
		out = append(out, h)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				out = append(out, n.IP.String())
			}
		}
	}
	return out
}

// IsEntry says whether a saved place (and the recorder it was read from) is the
// Blackbox this look found on this PC, however the person added it: by its
// folder, by an address that reaches this PC, or by a share whose recorder is
// this PC's. local is LocalNames (tests give their own). Nothing is claimed for
// a Blackbox that was not found, or whose settings could not be read.
func (f LocalFound) IsEntry(loc Location, recHost, recName string, local []string) bool {
	if !f.Installed {
		return false
	}
	// The same recorder, on this PC: whatever way the place was typed.
	if f.Name != "" && strings.EqualFold(strings.TrimSpace(recName), f.Name) && isLocalName(recHost, local) {
		return true
	}
	switch loc.Kind {
	case LocFolder:
		if f.Folder == "" {
			return false
		}
		mine, err := ParseLocation(f.Folder)
		return err == nil && mine.Kind == LocFolder && mine.Canonical() == loc.Canonical()
	case LocURL:
		return f.Port != 0 && loc.Port == f.Port && !loc.TLS && isLocalName(loc.Host, local)
	}
	return false
}

// isLocalName says a host name or address stands for this PC.
func isLocalName(host string, local []string) bool {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if h == "" {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	if ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return true
	}
	for _, n := range local {
		n = strings.ToLower(strings.Trim(strings.TrimSpace(n), "[]"))
		if n == "" {
			continue
		}
		if n == h {
			return true
		}
		if ip != nil {
			if other := net.ParseIP(n); other != nil && other.Equal(ip) {
				return true
			}
			continue
		}
		// A short name and its fully qualified form are one PC.
		if net.ParseIP(n) == nil && shortName(n) == shortName(h) {
			return true
		}
	}
	return false
}

func shortName(s string) string {
	if i := strings.IndexByte(s, '.'); i > 0 {
		return s[:i]
	}
	return s
}
