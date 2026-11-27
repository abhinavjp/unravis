// Package service is the one remote layer the screen, the CLI and code mode all call. It joins the sealed server list, the SSH connection, the
// Blackbox tunnel and the pull job, and holds the rules that sit between them: no connection without a pinned host key, the host key pinned is
// the one the person compared, every folder read is one the person allowed, and nothing is ever written to a server.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/remote/bbtunnel"
	"api-audit-explorer/internal/remote/pull"
	"api-audit-explorer/internal/remote/registry"
	"api-audit-explorer/internal/remote/sshdirect"
	"api-audit-explorer/internal/remote/transport"
)

// Conn is a signed-in server: it reads files and can carry the Blackbox tunnel.
type Conn interface {
	transport.Transport
	bbtunnel.Tunneler
}

// Deps are what the service needs. Connect and FetchKeys default to the real SSH ones; tests give fakes.
type Deps struct {
	Reg *registry.Store
	// BBKey returns the Blackbox read key stored under a saved Blackbox id.
	BBKey func(id string) (string, error)
	// StagingRoot is the folder pulls land in: StagingRoot/<server id>/<job id>.
	StagingRoot string
	Limits      pull.Limits
	Free        func(dir string) (int64, error)

	Connect   func(ctx context.Context, c sshdirect.Config) (Conn, error)
	FetchKeys func(ctx context.Context, c sshdirect.Config) ([]sshdirect.HostKey, error)
}

// Service is safe for concurrent use.
type Service struct{ d Deps }

// New returns a service. Reg is required.
func New(d Deps) (*Service, error) {
	if d.Reg == nil {
		return nil, errors.New("service: the server list is required")
	}
	if d.Connect == nil {
		d.Connect = func(ctx context.Context, c sshdirect.Config) (Conn, error) {
			cl, err := sshdirect.Connect(ctx, c)
			if err != nil {
				return nil, err
			}
			return cl, nil
		}
	}
	if d.FetchKeys == nil {
		d.FetchKeys = sshdirect.FetchHostKey
	}
	return &Service{d: d}, nil
}

// Plain sentences the screen shows as they are.
var (
	ErrNeedHostKey   = errors.New("Check this server's host key first. Unravis connects only to a server whose key you have compared and pinned.")
	ErrNoSSH         = errors.New("This server is set up without an SSH or Salt connection, so there is nothing to browse or pull. Add one to use it.")
	ErrKeyNotShown   = errors.New("The server is now presenting a different host key than the one you compared. Nothing was pinned. Compare the key again.")
	ErrFolderNotKind = errors.New("That folder is not on this server's list for that kind of log. Add it to the allowed folders first.")
	ErrNoBlackbox    = errors.New("This server has no Blackbox set up. Add its Blackbox key first.")
)

// entry loads a server.
func (s *Service) entry(id string) (registry.Entry, error) { return s.d.Reg.Get(id) }

func hasTransport(e registry.Entry, name string) bool {
	for _, t := range e.Transports {
		if t == name {
			return true
		}
	}
	return false
}

// allowed lists every folder the person allowed on this server, once each.
func allowed(e registry.Entry) []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range []string{registry.FolderBlackbox, registry.FolderIIS, registry.FolderRedis, registry.FolderSQL, registry.FolderOther} {
		for _, f := range e.Folders[k] {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// base is the connection config without a secret. It is all that is needed to read the host key.
func base(e registry.Entry) sshdirect.Config {
	return sshdirect.Config{Host: e.Host, Port: e.Port, User: e.User, OS: e.OS}
}

// signIn is the config that signs in. It refuses a server whose key is not pinned.
func (s *Service) signIn(e registry.Entry) (sshdirect.Config, error) {
	if !hasJump(e) {
		return sshdirect.Config{}, ErrNoSSH
	}
	if e.HostKey == "" {
		return sshdirect.Config{}, ErrNeedHostKey
	}
	secret, pass, err := s.d.Reg.Secret(e.ID)
	if err != nil {
		return sshdirect.Config{}, err
	}
	c := base(e)
	c.Auth, c.Secret, c.Passphrase, c.HostKey, c.Allowed = e.Auth, secret, pass, e.HostKey, allowed(e)
	if hasTransport(e, registry.TransportBlackbox) {
		c.Tunnel = e.BlackboxAddr
		if c.Tunnel == "" {
			c.Tunnel = bbtunnel.DefaultAddr
		}
	}
	return c, nil
}

func (s *Service) connect(ctx context.Context, id string) (registry.Entry, Conn, error) {
	e, err := s.entry(id)
	if err != nil {
		return e, nil, err
	}
	c, err := s.signIn(e)
	if err != nil {
		return e, nil, err
	}
	cn, err := s.d.Connect(ctx, c)
	return e, cn, err
}

// HostKeys shows the keys the server presents. No secret is sent: the handshake stops at the host key.
func (s *Service) HostKeys(ctx context.Context, id string) ([]sshdirect.HostKey, error) {
	e, err := s.entry(id)
	if err != nil {
		return nil, err
	}
	if !hasJump(e) {
		return nil, ErrNoSSH
	}
	return s.d.FetchKeys(ctx, base(e))
}

// presents reports whether the server offers the fingerprint right now.
func (s *Service) presents(ctx context.Context, e registry.Entry, fp string) error {
	keys, err := s.d.FetchKeys(ctx, base(e))
	if err != nil {
		return err
	}
	for _, k := range keys {
		if k.Fingerprint == fp {
			return nil
		}
	}
	return ErrKeyNotShown
}

// Pin records the fingerprint the person compared. It looks again first, so only a key the server really offers is pinned.
func (s *Service) Pin(ctx context.Context, id, fingerprint string) error {
	e, err := s.entry(id)
	if err != nil {
		return err
	}
	if !hasJump(e) {
		return ErrNoSSH
	}
	if err := s.presents(ctx, e, fingerprint); err != nil {
		return err
	}
	return s.d.Reg.PinHostKey(id, fingerprint)
}

// ReplaceKey swaps the pinned key for a new one after the person saw both. oldFingerprint must be the one pinned now.
func (s *Service) ReplaceKey(ctx context.Context, id, oldFingerprint, newFingerprint string) error {
	e, err := s.entry(id)
	if err != nil {
		return err
	}
	if err := s.presents(ctx, e, newFingerprint); err != nil {
		return err
	}
	return s.d.Reg.ReplaceHostKey(id, oldFingerprint, newFingerprint)
}

// FolderCheck is one allowed folder seen from the server.
type FolderCheck struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	OK    bool   `json:"ok"`
	Files int    `json:"files"`
	Error string `json:"error,omitempty"`
}

// BlackboxCheck is the recorder as seen through the tunnel.
type BlackboxCheck struct {
	OK     bool                `json:"ok"`
	Status *blackbox.StatusDoc `json:"status,omitempty"`
	Error  string              `json:"error,omitempty"`
}

// TestResult is what Test learned. A failure of one step never hides the steps before it.
type TestResult struct {
	Host     transport.HostInfo `json:"host"`
	Folders  []FolderCheck      `json:"folders"`
	Blackbox *BlackboxCheck     `json:"blackbox,omitempty"`
	// Machine is the Salt machine that was read; empty for a server read directly.
	Machine string `json:"machine,omitempty"`
}

// Test signs in, reads the host details, lists each allowed folder and asks the recorder for its status. It changes nothing. On a Salt
// server, machine names the machine to read (it may be empty when only one is ticked).
func (s *Service) Test(ctx context.Context, id, machine string) (TestResult, error) {
	if e, err := s.entry(id); err == nil && !hasJump(e) && hasTransport(e, registry.TransportBlackbox) {
		return s.testDirect(ctx, e), nil
	}
	t, err := s.open(ctx, id, machine)
	if err != nil {
		return TestResult{}, err
	}
	defer t.close()
	e, cn := t.e, t.tr
	r := TestResult{Machine: t.machine}
	if r.Host, err = cn.Probe(ctx); err != nil {
		return r, err
	}
	for _, k := range []string{registry.FolderBlackbox, registry.FolderIIS, registry.FolderRedis, registry.FolderSQL, registry.FolderOther} {
		for _, f := range e.Folders[k] {
			fc := FolderCheck{Kind: k, Path: f}
			if fis, err := cn.List(ctx, f); err != nil {
				fc.Error = err.Error()
			} else {
				fc.OK, fc.Files = true, len(fis)
			}
			r.Folders = append(r.Folders, fc)
		}
	}
	if t.machine == "" && hasTransport(e, registry.TransportBlackbox) {
		bc := &BlackboxCheck{}
		src, err := s.bbSource(t.conn, e)
		if err == nil {
			var st *blackbox.StatusDoc
			if st, err = src.Status(ctx); err == nil {
				bc.OK, bc.Status = true, st
			}
		}
		if err != nil {
			bc.Error = err.Error()
		}
		r.Blackbox = bc
	}
	return r, nil
}

func (s *Service) bbSource(cn Conn, e registry.Entry) (blackbox.Source, error) {
	if e.BlackboxID == "" || s.d.BBKey == nil {
		return nil, ErrNoBlackbox
	}
	key, err := s.d.BBKey(e.BlackboxID)
	if err != nil {
		return nil, err
	}
	addr := e.BlackboxAddr
	if addr == "" {
		addr = bbtunnel.DefaultAddr
	}
	return bbtunnel.NewSource(cn, addr, key)
}

// BlackboxSource opens the recorder on this server as a Blackbox source, through the SSH tunnel. Close the returned closer when done.
func (s *Service) BlackboxSource(ctx context.Context, id string) (blackbox.Source, io.Closer, error) {
	if e, err := s.entry(id); err == nil && !hasJump(e) && hasTransport(e, registry.TransportBlackbox) {
		src, err := s.directSource(e)
		return src, noClose{}, err
	}
	e, cn, err := s.connect(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	src, err := s.bbSource(cn, e)
	if err != nil {
		cn.Close()
		return nil, nil, err
	}
	return src, cn, nil
}

// Browse lists one allowed folder (on the named machine, for a Salt server).
func (s *Service) Browse(ctx context.Context, id, machine, dir string) ([]transport.FileInfo, error) {
	t, err := s.open(ctx, id, machine)
	if err != nil {
		return nil, err
	}
	defer t.close()
	return t.tr.List(ctx, dir)
}

// folderKind maps what a pull is for to the allowlist group its folder must come from.
var folderKind = map[string]string{
	"blackbox":  registry.FolderBlackbox,
	"iis":       registry.FolderIIS,
	"redis":     registry.FolderRedis,
	"sqlserver": registry.FolderSQL,
	"applog":    registry.FolderOther,
}

// BlackboxPattern is what a Blackbox pull takes: the hourly files and the recorder's own manifest.
const BlackboxPattern = blackbox.ManifestName + "|bb-*.ndjson"

func (s *Service) request(e registry.Entry, req pull.Request) (pull.Request, error) {
	fk, ok := folderKind[req.Kind]
	if !ok {
		return req, fmt.Errorf("Pulling %q is not supported. Choose Blackbox, IIS, Redis, SQL Server or other logs.", req.Kind)
	}
	found := false
	for _, f := range e.Folders[fk] {
		if f == req.Dir {
			found = true
		}
	}
	if !found {
		return req, ErrFolderNotKind
	}
	if req.Kind == "blackbox" && strings.TrimSpace(req.Pattern) == "" {
		req.Pattern = BlackboxPattern
	}
	return req, nil
}

// Plan says what a pull would copy, without copying anything.
func (s *Service) Plan(ctx context.Context, id, machine string, req pull.Request) (pull.Plan, error) {
	t, err := s.open(ctx, id, machine)
	if err != nil {
		return pull.Plan{}, err
	}
	defer t.close()
	if req, err = s.request(t.e, req); err != nil {
		return pull.Plan{}, err
	}
	return pull.BuildPlan(ctx, t.tr, req, s.d.Limits)
}

// Pulled is a finished or interrupted job and where its files are.
type Pulled struct {
	pull.Result
	// BlackboxFolder is set when the job pulled a Blackbox: a folder the Blackbox view reads as it is.
	BlackboxFolder *blackbox.Location `json:"blackboxFolder,omitempty"`
}

// Pull plans and runs one job. The same jobID resumes an interrupted one.
func (s *Service) Pull(ctx context.Context, id, machine string, req pull.Request, jobID string, progress func(pull.Progress)) (Pulled, error) {
	t, err := s.open(ctx, id, machine)
	if err != nil {
		return Pulled{}, err
	}
	defer t.close()
	e := t.e
	if req, err = s.request(e, req); err != nil {
		return Pulled{}, err
	}
	plan, err := pull.BuildPlan(ctx, t.tr, req, s.d.Limits)
	if err != nil {
		return Pulled{}, err
	}
	label := e.Name
	if t.machine != "" {
		label += " / " + t.machine
	}
	res, err := pull.Run(ctx, t.tr, req, plan, pull.Options{
		StagingDir: filepath.Join(s.d.StagingRoot, e.ID),
		JobID:      jobID,
		Server:     label,
		Origin:     t.machine + "|" + req.Kind + "|" + req.Dir,
		Limits:     s.d.Limits,
		Free:       s.d.Free,
		Progress:   progress,
	})
	out := Pulled{Result: res}
	if err == nil && req.Kind == "blackbox" && res.StagedAt != "" {
		out.BlackboxFolder = &blackbox.Location{Kind: blackbox.LocFolder, Path: res.StagedAt}
	}
	return out, err
}

// directSource reads a recorder that is reachable from this PC without SSH (the Blackbox-only, Lite case). The address is host:port; the host
// is the server's own when only a port is given.
func (s *Service) directSource(e registry.Entry) (blackbox.Source, error) {
	if e.BlackboxID == "" || s.d.BBKey == nil {
		return nil, ErrNoBlackbox
	}
	key, err := s.d.BBKey(e.BlackboxID)
	if err != nil {
		return nil, err
	}
	addr := e.BlackboxAddr
	if addr == "" {
		addr = net.JoinHostPort(e.Host, "7790")
	}
	loc, err := blackbox.ParseLocation("http://" + addr)
	if err != nil {
		return nil, err
	}
	return blackbox.NewHTTPSource(loc, key, nil), nil
}

func (s *Service) testDirect(ctx context.Context, e registry.Entry) TestResult {
	bc := &BlackboxCheck{}
	src, err := s.directSource(e)
	if err == nil {
		var st *blackbox.StatusDoc
		if st, err = src.Status(ctx); err == nil {
			bc.OK, bc.Status = true, st
		}
	}
	if err != nil {
		bc.Error = err.Error()
	}
	return TestResult{Blackbox: bc}
}

// noClose is the closer of a source that holds no connection of its own.
type noClose struct{}

func (noClose) Close() error { return nil }
