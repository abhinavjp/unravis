package bbredis

import (
	"errors"
	"strings"
)

// ErrUntrustedListener means whatever listens on the Redis port is not a program an administrator installed or a
// Windows service account. The password is then NOT sent: any user may open a port above 1024 on Windows, so a
// loopback address alone does not say who is on the other end.
var ErrUntrustedListener = errors.New("bbredis: the program on the Redis port is not trusted")

// ListenerCheck says whether the program listening on 127.0.0.1:port may be given the password. It is a variable so
// tests run without Windows. On Windows it reads the owner of every listener on that port.
var ListenerCheck = listenerTrusted

// pathUnder reports whether file is inside dir (case-insensitive, either slash). Pure string work, so it is the same
// on every platform and can be tested anywhere.
func pathUnder(file, dir string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "/", `\`)) }
	f, d := norm(file), strings.TrimRight(norm(dir), `\`)
	if d == "" || strings.Contains(f, `\..\`) || strings.HasSuffix(f, `\..`) {
		return false
	}
	return strings.HasPrefix(f, d+`\`)
}
