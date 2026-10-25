package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The reachability probe answers one question for the start wizard: can this
// machine open a TCP connection to host:port? It is user-triggered only (the
// scan itself opens no connection), sends and reads nothing, and returns just a
// yes/no plus one reason from a fixed set, never raw error text, so it cannot be
// used to read banners or to leak resolver and OS details.
//
// It is still a port scanner shaped like an HTTP route, so it is rate limited,
// needs the session token and the local Origin check like every other route,
// bounds the dial to probeDialTimeout, and refuses addresses that name cloud
// instance-metadata services, link-local ranges and the unspecified address.
// Loopback and private ranges are allowed: finding a LAN server is the purpose.

const (
	probeDialTimeout = 3 * time.Second
	// probesPerWindow caps probes per connectionTestWindow. A wizard checks a
	// handful of found services and a few typed addresses; scanning a subnet
	// would need far more.
	probesPerWindow = 20
	maxProbeHostLen = 253
)

// Reasons the probe reports. The set is closed: the browser maps each to plain
// words and nothing else ever reaches it.
const (
	probeRefused  = "refused"
	probeTimedOut = "timed-out"
	probeNoHost   = "no-such-host"
	probeBlocked  = "blocked"
)

// Seams the tests replace; production uses the real resolver and dialer.
var (
	probeTimeout = probeDialTimeout
	probeLookup  = func(ctx context.Context, host string) ([]netip.Addr, error) {
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		return addrs, err
	}
	probeDial = func(ctx context.Context, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", address)
	}
)

// probeLimiters holds one limiter per handler (the handler struct lives outside
// this task's scope, so the limiter hangs off it from here).
var probeLimiters sync.Map // *handler -> *connectionTestLimiter

func (h *handler) probeLimiter() *connectionTestLimiter {
	if existing, ok := probeLimiters.Load(h); ok {
		return existing.(*connectionTestLimiter)
	}
	created, _ := probeLimiters.LoadOrStore(h, &connectionTestLimiter{limit: probesPerWindow})
	return created.(*connectionTestLimiter)
}

type probeRequest struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type probeResponse struct {
	Reachable bool   `json:"reachable"`
	Reason    string `json:"reason"`
}

// probeReachable serves POST /api/probe/reachable.
func (h *handler) probeReachable(w http.ResponseWriter, r *http.Request) {
	var in probeRequest
	if !decodeJSON(w, r, &in) {
		return
	}
	host, literal, ok := normalizeProbeHost(in.Host)
	if !ok {
		writeError(w, http.StatusBadRequest, "Enter a computer name or an IP address, without http://, a path or a port.")
		return
	}
	if in.Port < 1 || in.Port > 65535 {
		writeError(w, http.StatusBadRequest, "Port must be a number from 1 to 65535.")
		return
	}
	if !h.probeLimiter().allow() {
		writeError(w, http.StatusTooManyRequests, "Too many reachability checks in the last minute. Wait a minute, then check again.")
		return
	}
	reachable, reason := probe(r.Context(), host, literal, in.Port)
	writeJSON(w, http.StatusOK, probeResponse{Reachable: reachable, Reason: reason}, 4096)
}

// probe resolves host (unless it is already an address), refuses blocked
// addresses, and dials each remaining one until a connection opens. The address
// that was checked is the address that is dialled, so a name cannot pass the
// check and then resolve elsewhere.
func probe(parent context.Context, host string, literal netip.Addr, port int) (bool, string) {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()

	addrs := []netip.Addr{literal}
	if !literal.IsValid() {
		resolved, err := probeLookup(ctx, host)
		if err != nil || len(resolved) == 0 {
			if isTimeout(ctx, err) {
				return false, probeTimedOut
			}
			return false, probeNoHost
		}
		addrs = resolved
	}
	for _, a := range addrs {
		if probeBlockedAddr(a) {
			return false, probeBlocked
		}
	}
	reason := probeRefused
	for _, a := range addrs {
		conn, err := probeDial(ctx, net.JoinHostPort(a.Unmap().String(), strconv.Itoa(port)))
		if err == nil {
			_ = conn.Close()
			return true, ""
		}
		if isTimeout(ctx, err) {
			reason = probeTimedOut
		} else {
			reason = probeRefused
		}
		if ctx.Err() != nil {
			break
		}
	}
	return false, reason
}

func isTimeout(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return errors.Is(ctx.Err(), context.DeadlineExceeded)
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// probeBlockedAddr reports addresses the probe will not touch: the unspecified
// and "this network" addresses, link-local unicast (which holds the AWS, Azure
// and GCP metadata address 169.254.169.254), multicast and broadcast, and the
// other well-known metadata endpoints outside link-local space.
func probeBlockedAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() {
		return true
	}
	if a.Is4() {
		b := a.As4()
		if b[0] == 0 || a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return true
		}
		if a == netip.AddrFrom4([4]byte{100, 100, 100, 200}) { // Alibaba Cloud metadata
			return true
		}
		if a == netip.AddrFrom4([4]byte{168, 63, 129, 16}) { // Azure WireServer
			return true
		}
		return false
	}
	if a == netip.MustParseAddr("fd00:ec2::254") { // AWS IPv6 metadata
		return true
	}
	// An IPv6 address that carries an IPv4 one (NAT64, 6to4) reaches that IPv4
	// host through a translator, so the embedded address is judged like a plain one.
	for _, v4 := range embeddedIPv4(a) {
		if probeBlockedAddr(v4) {
			return true
		}
	}
	return false
}

var (
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalUse  = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour      = netip.MustParsePrefix("2002::/16")
)

// embeddedIPv4 returns the IPv4 addresses a with a translator prefix could stand
// for: the last 32 bits for 64:ff9b::/96, bits 16..47 for 6to4 (2002::/16) and,
// for the local-use NAT64 block 64:ff9b:1::/48 whose operator picks any RFC 6052
// prefix length, every RFC 6052 position (prefix lengths 32, 40, 48, 56, 64, 96).
func embeddedIPv4(a netip.Addr) []netip.Addr {
	if !a.Is6() {
		return nil
	}
	b := a.As16()
	at := func(idx ...int) netip.Addr {
		return netip.AddrFrom4([4]byte{b[idx[0]], b[idx[1]], b[idx[2]], b[idx[3]]})
	}
	switch {
	case nat64WellKnown.Contains(a):
		return []netip.Addr{at(12, 13, 14, 15)}
	case sixToFour.Contains(a):
		return []netip.Addr{at(2, 3, 4, 5)}
	case nat64LocalUse.Contains(a):
		return []netip.Addr{
			at(4, 5, 6, 7),     // /32
			at(5, 6, 7, 9),     // /40
			at(6, 7, 9, 10),    // /48
			at(7, 9, 10, 11),   // /56
			at(9, 10, 11, 12),  // /64
			at(12, 13, 14, 15), // /96
		}
	}
	return nil
}

// normalizeProbeHost validates a typed host. It accepts a hostname or an IP
// literal (IPv6 optionally in brackets) and rejects schemes, paths, userinfo,
// ports, whitespace and anything else that is not a name. For an address it
// also returns the parsed address.
func normalizeProbeHost(raw string) (string, netip.Addr, bool) {
	host := strings.TrimSpace(raw)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if host == "" || len(host) > maxProbeHostLen {
		return "", netip.Addr{}, false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return "", netip.Addr{}, false
		}
		return host, addr, true
	}
	if !validHostname(host) {
		return "", netip.Addr{}, false
	}
	return strings.TrimSuffix(host, "."), netip.Addr{}, true
}

func validHostname(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	// A name whose last label is all digits is a malformed number such as
	// 2130706433 or 10.1, which some resolvers read as an address; reject it.
	last := labels[len(labels)-1]
	allDigits := true
	for i := 0; i < len(last); i++ {
		if last[i] < '0' || last[i] > '9' {
			allDigits = false
			break
		}
	}
	return !allDigits
}
