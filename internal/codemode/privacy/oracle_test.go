package privacy

import (
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
