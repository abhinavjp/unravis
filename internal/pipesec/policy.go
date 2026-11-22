package pipesec

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// Errors returned by the package.
var (
	// ErrPeerDenied means the client failed the server's peer policy.
	ErrPeerDenied = errors.New("pipesec: peer denied")
	// ErrServerMismatch means the server end is not the expected process.
	ErrServerMismatch = errors.New("pipesec: pipe server is not the expected process")
	// ErrPipeBusy means every instance stayed busy until the context ended.
	ErrPipeBusy = errors.New("pipesec: pipe busy")
)

// Peer is what the OS says about the other end of a connection.
type Peer struct {
	PID, Session uint32
	Image        string // full image path from QueryFullProcessImageNameW
	UserSID      string
	LogonSID     string
}

// PeerPolicy decides which clients a listener accepts.
type PeerPolicy struct {
	// InstallDir is the directory a client image must sit in. `<InstallDir>\bin` is allowed too (PLAN section 1).
	InstallDir string
	// AllowedBase lists the permitted image base names.
	AllowedBase []string
	// RequireSameLogon demands the client's logon SID equals the server's.
	RequireSameLogon bool
}

// defaultAllowedBase is the spec 02 row 2.8 list.
var defaultAllowedBase = []string{"unravis.exe", "unravis-desktop.exe", "unravis-core.exe"}

// DefaultPeerPolicy allows the Unravis executables in the directory of the running executable. If that directory cannot be found,
// InstallDir stays empty and every client is denied (fail closed).
func DefaultPeerPolicy() PeerPolicy {
	p := PeerPolicy{AllowedBase: append([]string(nil), defaultAllowedBase...), RequireSameLogon: true}
	if exe, err := os.Executable(); err == nil {
		p.InstallDir = winDir(exe)
	}
	return p
}

// self is the server's own identity, the baseline a peer is compared with.
type self struct {
	Session  uint32
	LogonSID string
}

// check applies the policy to one peer. It is pure so it runs on every OS.
func (p PeerPolicy) check(peer Peer, me self) error {
	deny := func(why string) error { return fmt.Errorf("%w: %s", ErrPeerDenied, why) }
	if peer.Session != me.Session {
		return deny("different session")
	}
	if p.RequireSameLogon && (peer.LogonSID == "" || !strings.EqualFold(peer.LogonSID, me.LogonSID)) {
		return deny("different logon")
	}
	if peer.Image == "" {
		return deny("no image path")
	}
	if extraAllowed(peer.Image) {
		return nil
	}
	if err := imageInDir(peer.Image, p.InstallDir, p.AllowedBase); err != nil {
		return deny(err.Error())
	}
	return nil
}

// imageInDir checks that image is `<dir>\<base>` or `<dir>\bin\<base>` with base in allowed. An empty dir or list denies.
func imageInDir(image, dir string, allowed []string) error {
	if dir == "" || len(allowed) == 0 {
		return errors.New("no allowed location")
	}
	d, base := winSplit(image)
	d, dir = winClean(d), winClean(dir)
	if !strings.EqualFold(d, dir) && !strings.EqualFold(d, dir+`\bin`) {
		return errors.New("image outside the install directory")
	}
	for _, a := range allowed {
		if strings.EqualFold(a, base) {
			return nil
		}
	}
	return errors.New("image name not allowed")
}

// winSplit splits a Windows path at its last separator without touching the OS path rules, so tests run anywhere.
func winSplit(p string) (dir, base string) {
	p = strings.ReplaceAll(p, "/", `\`)
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

func winDir(p string) string { d, _ := winSplit(p); return winClean(d) }

// winClean drops the extended-length prefix and trailing separators and collapses doubled separators.
func winClean(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	p = strings.TrimPrefix(p, `\\?\`)
	for strings.Contains(p[min(len(p), 2):], `\\`) { // keep a leading UNC pair
		p = p[:min(len(p), 2)] + strings.ReplaceAll(p[min(len(p), 2):], `\\`, `\`)
	}
	return strings.TrimRight(p, `\`)
}

// ServerPolicy is what a client demands from the server end before it sends anything.
type ServerPolicy struct {
	ExpectedImage   string // full path of the server image; compared case-insensitively
	InstallDir      string // the image must also lie in this directory (or its bin); empty skips the directory check
	ExpectedSession uint32 // the server's session id
}

func (sp ServerPolicy) check(srv Peer) error {
	bad := func(why string) error { return fmt.Errorf("%w: %s", ErrServerMismatch, why) }
	if srv.Session != sp.ExpectedSession {
		return bad("different session")
	}
	if sp.ExpectedImage == "" || !strings.EqualFold(winClean(srv.Image), winClean(sp.ExpectedImage)) {
		return bad("different image")
	}
	if sp.InstallDir != "" {
		_, base := winSplit(srv.Image)
		if err := imageInDir(srv.Image, sp.InstallDir, []string{base}); err != nil {
			return bad(err.Error())
		}
	}
	return nil
}

var denied atomic.Uint64

// Metrics returns how many peers have been denied since the process started.
func Metrics() (deniedCount uint64) { return denied.Load() }
