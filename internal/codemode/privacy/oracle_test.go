package privacy

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// The leak oracle for the tests. It does not call the package scanners: it finds addresses with its own regular expressions and
// net/netip, and it is stricter than the scanner in one way on purpose (a window of four parts inside a longer dotted number counts).
// Policy it shares with the package, so a leak is a leak and not a taste: a window at the edge of a run that touches a letter or "_"
// counts only when it holds a decimal digit ("std::vector" is text, "::1x" is an address).

var (
	oracleDotted = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)*`)
	oracleV6Run  = regexp.MustCompile(`[0-9A-Fa-f:.]+`)
	oracleWord   = regexp.MustCompile(`[A-Za-z0-9_]`)
	oracleNum    = regexp.MustCompile(`[0-9]+`)
)

// leakIP returns a description of the first address in s, or "".
func leakIP(s string) string {
	for _, run := range oracleDotted.FindAllString(s, -1) {
		parts := strings.Split(run, ".")
		for w := 0; w+4 <= len(parts); w++ {
			good := true
			for _, p := range parts[w : w+4] {
				v, err := strconv.ParseUint(p, 10, 32)
				if err != nil || v > 255 || len(p) > 8 {
					good = false
					break
				}
			}
			if good {
				return "v4 " + strings.Join(parts[w:w+4], ".") + " in " + run
			}
		}
	}
	for _, loc := range oracleV6Run.FindAllStringIndex(s, -1) {
		run := s[loc[0]:loc[1]]
		if strings.Count(run, ":") < 2 {
			continue
		}
		for l := 0; l < len(run); l++ {
			if l > 0 && run[l-1] != ':' && run[l-1] != '.' {
				continue
			}
			for h := l + 2; h <= len(run) && h-l <= 45; h++ {
				if h < len(run) && run[h] != ':' && run[h] != '.' {
					continue
				}
				w := run[l:h]
				if strings.Count(w, ":") < 2 {
					continue
				}
				if _, err := netip.ParseAddr(w); err != nil {
					continue
				}
				leftWord := l == 0 && loc[0] > 0 && oracleWord.MatchString(s[loc[0]-1:loc[0]])
				rightWord := h == len(run) && loc[1] < len(s) && oracleWord.MatchString(s[loc[1]:loc[1]+1])
				if (leftWord || rightWord) && !strings.ContainsAny(w, "0123456789") {
					continue
				}
				return "v6 " + w + " in " + run
			}
		}
	}
	return ""
}

// leakLearned returns a learned value that is still in s, ignoring ASCII case, or "".
func leakLearned(s string, raws []string) string {
	low := strings.ToLower(s)
	for _, r := range raws {
		if r != "" && strings.Contains(low, strings.ToLower(r)) {
			return r
		}
	}
	return ""
}

// leakPartialV4 reports two consecutive octets of the planted address a.b.c.d (any zero padding) that are still in out, or "". The
// octets of a planted address are chosen so that they are not in the surrounding text.
func leakPartialV4(out string, o [4]int) string {
	norm := oracleNum.ReplaceAllStringFunc(out, func(m string) string {
		v, err := strconv.Atoi(m)
		if err != nil {
			return m
		}
		return strconv.Itoa(v)
	})
	for i := 0; i < 3; i++ {
		pair := strconv.Itoa(o[i]) + "." + strconv.Itoa(o[i+1])
		if strings.Contains(norm, pair) {
			return pair
		}
	}
	return ""
}

// leakTriple reports three consecutive octets of an address of in (four octets in a row, any zero padding) that are still in out
// next to each other. It is the partial leak a cut address leaves: "[IP_1].168.001.050" from "::ffff:192.168.001.050".
func leakTriple(in, out string) string {
	norm := func(s string) string {
		return oracleNum.ReplaceAllStringFunc(s, func(m string) string {
			v, err := strconv.Atoi(m)
			if err != nil || len(m) > 8 {
				return m
			}
			return strconv.Itoa(v)
		})
	}
	nout := norm(out)
	runs := oracleDotted.FindAllString(in, -1)
	if len(runs) != 1 {
		return "" // with several dotted numbers the leftovers cannot be told apart by their text
	}
	for _, run := range runs {
		parts := strings.Split(run, ".")
		for w := 0; w+4 <= len(parts); w++ {
			var v [4]int
			good := true
			for k, p := range parts[w : w+4] {
				n, err := strconv.ParseUint(p, 10, 32)
				if err != nil || n > 255 || len(p) > 8 {
					good = false
					break
				}
				v[k] = int(n)
			}
			if !good {
				continue
			}
			for _, tri := range []string{fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]), fmt.Sprintf("%d.%d.%d", v[1], v[2], v[3])} {
				if standalone(nout, tri) && !strings.Contains(norm(strings.ReplaceAll(in, run, "")), tri) {
					return tri
				}
			}
		}
	}
	return ""
}

// standalone reports whether tri is in s as a dotted number of its own: not the middle of a longer one. A token right in front of it
// ("[IP_1].168.1.50") does not count as part of the number.
func standalone(s, tri string) bool {
	for from := 0; ; {
		j := strings.Index(s[from:], tri)
		if j < 0 {
			return false
		}
		a := from + j
		b := a + len(tri)
		leftOK := a == 0 || s[a-1] == ']' || ((s[a-1] < '0' || s[a-1] > '9') && s[a-1] != '.')
		if a > 0 && s[a-1] == '.' && a > 1 && s[a-2] == ']' {
			leftOK = true
		}
		rightOK := b == len(s) || ((s[b] < '0' || s[b] > '9') && (s[b] != '.' || b+1 >= len(s) || ((s[b+1] < '0' || s[b+1] > '9') && s[b+1] != '[')))
		if leftOK && rightOK {
			return true
		}
		from = a + 1
	}
}
