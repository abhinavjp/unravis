package secretscrub

import "strings"

// MaskURLUserinfo replaces the login of every scheme://login@host in s with mask: user and password both, also when the password
// holds an "@", a "/" is absent, or a space ("mongodb://admin:p@ss w0rd@host/db"). The login is everything between "://" and the
// last "@" before the first "/", "?", "#" or line end. A text with a space in it and no sign of a login (a sentence that mentions a
// host and, further on, an e-mail address) is left alone.
func MaskURLUserinfo(s, mask string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	var edits []edit
	pos := 0
	for {
		i := strings.Index(s[pos:], "://")
		if i < 0 {
			break
		}
		start := pos + i + 3
		end := len(s)
		for k := start; k < len(s); k++ {
			c := s[k]
			if c == '/' || c == '?' || c == '#' || c == '\r' || c == '\n' {
				end = k
				break
			}
		}
		pos = end
		if pos < start {
			pos = start
		}
		auth := s[start:end]
		at := strings.LastIndexByte(auth, '@')
		if at < 0 {
			// "ftp://u:SEK/RIT9@host", "ftp://u:SEK?RIT9@host", "redis://:SEK/RIT9@h": a password with a "/", "?" or "#" ends the
			// authority early. When the text before it reads as a login (a colon, and not host:port) and an "@" follows before the
			// next space, the login runs up to that "@". A host:port that is followed by "/"+text+"@" ("h:1234/SEKRIT9@host", a password of
			// digits) counts too, unless the "@" is the start of a path part ("https://h:8080/users/@me", "/@name").
			if end < len(s) && strings.IndexByte(auth, ':') >= 0 {
				hostPort := isHostPort(auth)
				if !hostPort || s[end] == '/' {
					// Look at most loginReach bytes on, up to the next space. A login that does not end within reach is not read as one.
					lim := end + loginReach
					if lim > len(s) {
						lim = len(s)
					}
					stop := end
					for stop < lim && !isSpace(s[stop]) {
						stop++
					}
					r := strings.LastIndexByte(s[end:stop], '@')
					switch {
					case r < 0:
						if stop < lim || stop == len(s) {
							pos = stop // a whole stretch up to a space (or the end) with no "@": no login in it, whatever URL starts inside
						}
					case hostPort && (r < 2 || strings.IndexByte(s[end+1:end+r], '/') >= 0):
						// a path that holds an "@" ("/users/@me"), not a login
					default:
						edits = append(edits, edit{start, end + r})
						pos = end + r + 1
					}
				}
			}
			continue
		}
		login := auth[:at]
		if sp := strings.IndexAny(login, " \t"); sp >= 0 {
			colon := strings.IndexByte(login, ':')
			if colon < 0 || colon > sp || isHostPort(login[:sp]) {
				// "see mongodb://host and mail ops@corp": not a login. Stop at the first space; a plain login before it still counts.
				login = login[:sp]
				at2 := strings.LastIndexByte(login, '@')
				if at2 < 0 {
					continue
				}
				edits = append(edits, edit{start, start + at2})
				pos = start + at2 + 1
				continue
			}
		}
		if at > 0 {
			edits = append(edits, edit{start, start + at})
		}
	}
	return apply(s, mask, edits)
}

// loginReach is how far past the end of the authority a password with a "/", "?" or "#" in it is looked for.
const loginReach = 512

// isHostPort says whether v is "host:digits".
func isHostPort(v string) bool {
	c := strings.LastIndexByte(v, ':')
	if c < 0 || c == len(v)-1 || len(v)-c-1 > 5 {
		return false
	}
	for i := c + 1; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}
