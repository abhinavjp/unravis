package blackbox

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Client side of the Blackbox: where a recorder is, how Unravis reads it and what
// state it is in. Nothing here writes to a recorder or to its folder.

// LocationKind is how a Blackbox is reached (decision A34): a folder on this PC,
// a UNC share, or URL:port. There is no network scan.
type LocationKind string

// Location kinds.
const (
	LocFolder LocationKind = "folder"
	LocShare  LocationKind = "share"
	LocURL    LocationKind = "url"
)

// Location is a parsed place a Blackbox can be read from. Keep it free of
// secrets: the read key is stored apart, under Canonical.
type Location struct {
	Kind LocationKind `json:"kind"`
	// Path is the folder or the UNC path (backslashes, no trailing one).
	Path string `json:"path,omitempty"`
	// Host and Port are set for a URL. TLS is true for https.
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`
	TLS  bool   `json:"tls,omitempty"`
}

// Errors of ParseLocation. Their text is shown to the person as it is.
var (
	ErrLocationEmpty      = errors.New("Type a folder, a share such as \\\\server\\share, or an address such as http://web-b:7788.")
	ErrLocationKeyInURL   = errors.New("The address must not carry a user name, password or key. Type the key in the key box.")
	ErrLocationBadURL     = errors.New("That address cannot be used. Use the form http://server:7788 (the port is 7788 unless the Blackbox was set up with another).")
	ErrLocationBadPath    = errors.New("That is not a full folder path. Use a drive path such as D:\\UnravisBlackbox or a share such as \\\\server\\share.")
	ErrLocationBadShare   = errors.New("That share name is not complete. Use \\\\server\\share.")
	ErrLocationBadPort    = errors.New("The port must be a number from 1 to 65535.")
	ErrLocationUnsafePath = errors.New("That path has characters that are not allowed in a folder path.")
)

var (
	driveRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	hostRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]*[A-Za-z0-9])?$`)
)

// maxLocationLen bounds what a typed location can be.
const maxLocationLen = 520

// ParseLocation reads what the person typed. A path that starts with two
// backslashes is a share, a drive path is a folder, and anything with a scheme
// or a host:port is a URL. A URL may not hold a user name, a password, a query
// or a key: the key goes in the X-Blackbox-Key header only.
func ParseLocation(input string) (Location, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return Location{}, ErrLocationEmpty
	}
	if len(s) > maxLocationLen {
		return Location{}, ErrLocationBadPath
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return Location{}, ErrLocationUnsafePath
		}
	}
	switch {
	case strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, "//"):
		return parseShare(s)
	case driveRe.MatchString(s) || (len(s) == 2 && s[1] == ':' && isLetter(s[0])):
		return parseFolder(s)
	case strings.Contains(s, "://") || looksLikeHostPort(s):
		return parseURL(s)
	}
	return Location{}, ErrLocationBadPath
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func looksLikeHostPort(s string) bool {
	if strings.ContainsAny(s, `\ `) {
		return false
	}
	h := s
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "@"); i >= 0 {
		return true // user information: parseURL refuses it with the right words
	}
	host, port, err := net.SplitHostPort(h)
	return err == nil && host != "" && port != ""
}

func cleanWinPath(s string) (string, error) {
	s = strings.ReplaceAll(s, "/", `\`)
	if strings.ContainsAny(s, `<>"|?*`) {
		return "", ErrLocationUnsafePath
	}
	for _, seg := range strings.Split(s, `\`) {
		if seg == ".." {
			return "", ErrLocationUnsafePath
		}
	}
	return s, nil
}

func parseFolder(s string) (Location, error) {
	p, err := cleanWinPath(s)
	if err != nil {
		return Location{}, err
	}
	if len(p) < 3 || p[1] != ':' {
		return Location{}, ErrLocationBadPath
	}
	// "D:\" is a drive, not a folder of its own: the recordings need a folder.
	p = squeeze(p[:2]+`\`+strings.Trim(p[2:], `\`), 2)
	return Location{Kind: LocFolder, Path: strings.TrimRight(p, `\`)}, nil
}

// squeeze collapses runs of backslashes after the first keep characters.
func squeeze(p string, keep int) string {
	head, tail := p[:keep], p[keep:]
	for strings.Contains(tail, `\\`) {
		tail = strings.ReplaceAll(tail, `\\`, `\`)
	}
	return head + tail
}

func parseShare(s string) (Location, error) {
	p, err := cleanWinPath(s)
	if err != nil {
		return Location{}, err
	}
	rest := strings.TrimLeft(p, `\`)
	parts := strings.Split(strings.TrimRight(rest, `\`), `\`)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return Location{}, ErrLocationBadShare
	}
	if !hostRe.MatchString(parts[0]) {
		// IPv6 literals and odd names are not supported in a share path.
		return Location{}, ErrLocationBadShare
	}
	for _, seg := range parts[2:] {
		if seg == "" {
			return Location{}, ErrLocationBadShare
		}
	}
	return Location{Kind: LocShare, Path: `\\` + strings.Join(parts, `\`)}, nil
}

func parseURL(s string) (Location, error) {
	raw := s
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Location{}, ErrLocationBadURL
	}
	if u.User != nil {
		return Location{}, ErrLocationKeyInURL
	}
	if u.RawQuery != "" || u.Fragment != "" || strings.Contains(strings.ToLower(raw), "key=") {
		return Location{}, ErrLocationKeyInURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Location{}, ErrLocationBadURL
	}
	if p := strings.Trim(u.Path, "/"); p != "" {
		return Location{}, ErrLocationBadURL
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || (!hostRe.MatchString(host) && net.ParseIP(host) == nil) {
		return Location{}, ErrLocationBadURL
	}
	port := DefaultPort
	if ps := u.Port(); ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 65535 {
			return Location{}, ErrLocationBadPort
		}
		port = n
	}
	return Location{Kind: LocURL, Host: host, Port: port, TLS: u.Scheme == "https"}, nil
}

// Canonical is the one spelling used to tell two entries apart and to key the
// sealed read key. A share or folder is compared without case (Windows paths
// are), a URL by scheme, lower-cased host and port.
func (l Location) Canonical() string {
	switch l.Kind {
	case LocURL:
		scheme := "http"
		if l.TLS {
			scheme = "https"
		}
		return scheme + "://" + net.JoinHostPort(l.Host, strconv.Itoa(l.Port))
	default:
		return strings.ToLower(l.Path)
	}
}

// Display is how the location reads on screen.
func (l Location) Display() string {
	switch l.Kind {
	case LocURL:
		scheme := "http"
		if l.TLS {
			scheme = "https"
		}
		return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(l.Host, strconv.Itoa(l.Port)))
	default:
		return l.Path
	}
}

// BaseURL is the address requests go to (URL kind only).
func (l Location) BaseURL() string { return l.Display() }

// NeedsKey says whether reading this place takes a read key. A folder or share
// is read as files, with the person's own rights; a URL needs the key.
func (l Location) NeedsKey() bool { return l.Kind == LocURL }
