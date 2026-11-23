package privacy

import (
	"net/netip"
	"strconv"
	"strings"
)

// maxZone bounds the interface name after "%" in a v6 address.
const maxZone = 64

// maxV6Len is the longest text of an IPv6 address: 8 groups of 4, 7 colons, and an embedded IPv4 tail is shorter.
const maxV6Len = 45

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func isWord(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// isV6Char is a byte that can be part of a run that may hold an IPv6 address.
func isV6Char(c byte) bool { return isHex(c) || c == ':' || c == '.' }

// scanIPs walks in once, left to right, and calls tok for every address; the result is in with those spans replaced. v6 chooses
// the IPv6 scan, otherwise the IPv4 scan. Every scanner moves forward only, and a long run is measured once.
func scanIPs(in string, v6 bool, tok func(canon string) string) string {
	var b strings.Builder
	last := 0
	emit := func(from, to int, canon string) {
		if b.Cap() == 0 {
			b.Grow(len(in))
		}
		b.WriteString(in[last:from])
		b.WriteString(tok(canon))
		last = to
	}
	if v6 {
		scanV6(in, emit)
	} else {
		for i := 0; i < len(in); {
			from, to, canon, ok := nextV4(in, i)
			if !ok {
				break
			}
			emit(from, to, canon)
			i = to
		}
	}
	if last == 0 {
		return in
	}
	b.WriteString(in[last:])
	return b.String()
}

// dottedEnd returns the end of the digits-and-dots number that starts at i (a "." counts only when a digit follows it).
func dottedEnd(s string, i int) int {
	for i < len(s) && (isDigit(s[i]) || (s[i] == '.' && i+1 < len(s) && isDigit(s[i+1]))) {
		i++
	}
	return i
}

// v4Part reads one part of a dotted quad: digits only, at most 8 of them (so "0192" and "000001" count), value at most 255.
func v4Part(p string) (int, bool) {
	if p == "" || len(p) > 8 {
		return 0, false
	}
	v := 0
	for i := 0; i < len(p); i++ {
		v = v*10 + int(p[i]-'0')
		if v > 255 {
			return 0, false
		}
	}
	return v, true
}

// nextV4 finds the next IPv4 address at or after pos. A dotted number is cut at its dots; every four parts in a row that each read as
// 0 to 255 make an address, and when such windows overlap or touch ("5.192.168.0.1", "10.1.2.3.443", the port after a dot as tcpdump
// writes it) the whole stretch from the first window to the last is one span, so no octet of a window is left behind. The tail that
// no window covers stays ("...3" then ".443"). A four-part number that does not read as an address ("10.0.19045.1234", a build
// number) is left; that is the one over-redaction trade the other way. A letter next to the number does not stop it ("ip1.2.3.4").
// Leading zeros are decimal: "010.001.002.003" is 10.1.2.3, and shares its token.
func nextV4(s string, pos int) (from, to int, canon string, ok bool) {
	i := pos
	for i < len(s) {
		if !isDigit(s[i]) {
			i++
			continue
		}
		start := i
		end := dottedEnd(s, start)
		i = end
		parts := strings.Split(s[start:end], ".")
		if len(parts) < 4 {
			continue
		}
		vals := make([]int, len(parts))
		okp := make([]bool, len(parts))
		for k, p := range parts {
			vals[k], okp[k] = v4Part(p)
		}
		first, lastW := -1, -1
		for w := 0; w+4 <= len(parts); w++ {
			if okp[w] && okp[w+1] && okp[w+2] && okp[w+3] {
				if first < 0 {
					first = w
				}
				lastW = w
			}
		}
		if first < 0 {
			continue
		}
		off := start
		for k := 0; k < first; k++ {
			off += len(parts[k]) + 1
		}
		endOff := off
		var key strings.Builder
		for k := first; k <= lastW+3; k++ {
			if k > first {
				key.WriteByte('.')
				endOff++
			}
			endOff += len(parts[k])
			key.WriteString(strconv.Itoa(vals[k]))
		}
		return off, endOff, key.String(), true
	}
	return 0, 0, "", false
}

// scanV6 finds the IPv6 addresses of in, left to right, and calls emit with the span and the canonical text of each. A candidate run
// is a stretch of hex digits, colons and dots, measured once. Inside it every window that starts at the run start, right after a
// ":" or "." or where the last address ended, and ends at the run end or right before a ":" or ".", is tried with net/netip, the
// leftmost start first and, for that start, the longest end; so noise in front ("a.b:2001:db8::5", "label:::1"), behind
// ("2001:db8::1.2.3.4.5") or a port without brackets ("2001:db8::1:8080" is read as one address, a ninth group is cut off) does
// not hide an address. The window must hold a "::" or at least 6 colons (so a time "12:30:45" is never one). A window that touches
// a word byte at the run edge ("std::vector", "Class::method") counts only when it holds a decimal digit (so "::1x" and "1_eth0"
// are tokenized and "d::ec" is not). A zone ("%eth0") after the run end is part of the span. A v4-mapped address gets the token of
// its v4 form.
func scanV6(s string, emit func(from, to int, canon string)) {
	i := 0
	for i < len(s) {
		if !isV6Char(s[i]) {
			i++
			continue
		}
		start, colons := i, 0
		for i < len(s) && isV6Char(s[i]) {
			if s[i] == ':' {
				colons++
			}
			i++
		}
		end := i
		if colons < 2 {
			continue
		}
		for l := start; l < end; {
			f, e, c, ok := v6From(s, l, start, end)
			if ok {
				emit(f, e, c)
				if e > i {
					i = e // a zone ran past the run
				}
				if e >= end {
					break
				}
				l = e
				continue
			}
			// the next window start: right after the next ":" or "."
			p := l
			for p < end && s[p] != ':' && s[p] != '.' {
				p++
			}
			l = p + 1
		}
	}
}

func v6From(s string, l, start, end int) (from, to int, canon string, ok bool) {
	colons := 0
	dbl := false
	bestH := -1
	var best netip.Addr
	for h := l + 1; h <= end && h-l <= maxV6Len; h++ {
		if s[h-1] == ':' {
			colons++
			if h-2 >= l && s[h-2] == ':' {
				dbl = true
			}
			if colons > 8 {
				break
			}
		}
		if !(h == end || s[h] == ':' || s[h] == '.') || colons < 2 || !(dbl || colons >= 6) {
			continue
		}
		w := s[l:h]
		if (l == start && start > 0 && isWord(s[start-1])) || (h == end && end < len(s) && isWord(s[end])) {
			if !hasDigit(w) {
				continue
			}
		}
		if a, err := netip.ParseAddr(w); err == nil {
			best, bestH = a, h
		}
	}
	if bestH < 0 {
		return 0, 0, "", false
	}
	e := bestH
	if e == end && end < len(s) && s[end] == '%' {
		z := end + 1
		for z < len(s) && z-end <= maxZone && (isWord(s[z]) || s[z] == '-' || s[z] == '.' || s[z] == '~') {
			z++
		}
		e = z
	}
	return l, e, best.Unmap().WithZone("").String(), true
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if isDigit(s[i]) {
			return true
		}
	}
	return false
}
