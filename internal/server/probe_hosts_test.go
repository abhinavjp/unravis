package server

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNormalizeProbeHost(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHost string
		wantAddr netip.Addr
		wantOK   bool
	}{
		{"hostname", "example.com", "example.com", netip.Addr{}, true},
		{"trailing dot", "example.com.", "example.com", netip.Addr{}, true},
		{"IPv4", "192.168.1.1", "192.168.1.1", netip.MustParseAddr("192.168.1.1"), true},
		{"IPv6 bracketed", "[::1]", "::1", netip.MustParseAddr("::1"), true},
		{"IPv6 unbracketed", "::1", "::1", netip.MustParseAddr("::1"), true},
		{"whitespace", "  host.com  ", "host.com", netip.Addr{}, true},
		{"scheme", "http://host.com", "", netip.Addr{}, false},
		{"path", "host.com/path", "", netip.Addr{}, false},
		{"port", "host.com:8080", "", netip.Addr{}, false},
		{"userinfo", "u:p@host.com", "", netip.Addr{}, false},
		{"zone ID", "fe80::1%eth0", "", netip.Addr{}, false},
		{"all-digit label", "2130706433", "", netip.Addr{}, false},
		{"64-char label", strings.Repeat("a", 64)+".com", "", netip.Addr{}, false},
		{"254-char host", strings.Repeat("a", 254), "", netip.Addr{}, false},
		{"leading hyphen", "-host.com", "", netip.Addr{}, false},
		{"trailing hyphen", "host-.com", "", netip.Addr{}, false},
		{"mid hyphen ok", "ho-st.com", "ho-st.com", netip.Addr{}, true},
		{"empty", "", "", netip.Addr{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, a, ok := normalizeProbeHost(tt.input)
			if ok != tt.wantOK || (ok && (h != tt.wantHost || a != tt.wantAddr)) {
				t.Errorf("normalizeProbeHost(%q) = (%q, %v, %v), want (%q, %v, %v)", tt.input, h, a, ok, tt.wantHost, tt.wantAddr, tt.wantOK)
			}
		})
	}
}

func TestValidHostname(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple", "example.com", true},
		{"trailing dot", "example.com.", true},
		{"underscore", "my_host.com", true},
		{"63-char label", "a"+strings.Repeat("b", 62)+".com", true},
		{"all-digit last", "2130706433", false},
		{"64-char label", strings.Repeat("a", 64)+".com", false},
		{"254-char host", strings.Repeat("a", 254), false},
		{"long name of short labels", strings.Repeat("abcdefghi.", 25) + "com", true},
		{"leading hyphen", "-host.com", false},
		{"trailing hyphen", "host-.com", false},
		{"mid hyphen ok", "ho-st.com", true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validHostname(tt.input); got != tt.want {
				t.Errorf("validHostname(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestProbeBlockedAddr(t *testing.T) {
	blocked := []string{"169.254.169.254", "0.0.0.0", "255.255.255.255", "224.0.0.1", "fd00:ec2::254", "100.100.100.200", "::ffff:169.254.169.254"}
	allowed := []string{"127.0.0.1", "10.0.0.5", "::1"}
	for _, a := range blocked {
		t.Run(a+"_blocked", func(t *testing.T) {
			if !probeBlockedAddr(netip.MustParseAddr(a)) {
				t.Errorf("probeBlockedAddr(%s) = false, want true", a)
			}
		})
	}
	for _, a := range allowed {
		t.Run(a+"_allowed", func(t *testing.T) {
			if probeBlockedAddr(netip.MustParseAddr(a)) {
				t.Errorf("probeBlockedAddr(%s) = true, want false", a)
			}
		})
	}
}