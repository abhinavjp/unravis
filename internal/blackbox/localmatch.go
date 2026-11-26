package blackbox

import (
	"context"
	"net"
	"os"
	"strings"
	"time"
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

// ResolveHost gives the addresses a host name points to (tests replace it). A name
// that does not resolve gives none, so it is never taken for this PC.
var ResolveHost = func(host string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, _ := net.DefaultResolver.LookupHost(ctx, host)
	return addrs
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
	}
	if ip != nil {
		return false
	}
	// Another spelling of this PC's name (its fully qualified form) points at one of
	// its own addresses. Matching only the first label would take a host in another
	// domain with the same first label (web.partner.example) for this PC.
	for _, a := range ResolveHost(h) {
		r := net.ParseIP(strings.TrimSpace(a))
		if r == nil {
			continue
		}
		if r.IsLoopback() || r.IsUnspecified() {
			return true
		}
		for _, n := range local {
			if other := net.ParseIP(strings.Trim(strings.TrimSpace(n), "[]")); other != nil && other.Equal(r) {
				return true
			}
		}
	}
	return false
}
