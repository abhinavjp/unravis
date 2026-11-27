package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/remote/pull"
	"api-audit-explorer/internal/remote/registry"
	"api-audit-explorer/internal/remote/sshdirect"
	"api-audit-explorer/internal/remote/transport"
)

type xorProtector struct{}

func (xorProtector) Protect(v []byte) ([]byte, error) { return append([]byte("sealed:"), v...), nil }
func (xorProtector) Unprotect(v []byte) ([]byte, error) {
	if !bytes.HasPrefix(v, []byte("sealed:")) {
		return nil, errors.New("bad")
	}
	return v[len("sealed:"):], nil
}

const (
	pw  = "hunter2-SECRET-PASSWORD"
	fpA = "SHA256:Zk3Hn1c0uV8mF2rXb9yQe7TgPwLd4oAiS5JhN6vCtMU"
	fpB = "SHA256:Aa3Hn1c0uV8mF2rXb9yQe7TgPwLd4oAiS5JhN6vCtMU"
)

// fakeConn is a signed-in server with a few files.
type fakeConn struct {
	files  map[string]map[string]string // dir -> name -> body
	closed bool
}

func (f *fakeConn) Probe(context.Context) (transport.HostInfo, error) {
	return transport.HostInfo{OS: "windows", Description: "Windows Server 2022"}, nil
}
func (f *fakeConn) List(_ context.Context, dir string) ([]transport.FileInfo, error) {
	d, ok := f.files[dir]
	if !ok {
		return nil, transport.ErrNotAllowed
	}
	var out []transport.FileInfo
	for n, b := range d {
		out = append(out, transport.FileInfo{Name: n, Size: int64(len(b)), MTime: time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)})
	}
	return out, nil
}
func (f *fakeConn) body(p string) (string, bool) {
	i := strings.LastIndexAny(p, `\/`)
	b, ok := f.files[p[:i]][p[i+1:]]
	return b, ok
}
func (f *fakeConn) Open(_ context.Context, p string, off int64) (io.ReadCloser, error) {
	b, ok := f.body(p)
	if !ok {
		return nil, transport.ErrNotAllowed
	}
	return io.NopCloser(strings.NewReader(b[off:])), nil
}
func (f *fakeConn) Digest(_ context.Context, p string) (string, error) {
	b, _ := f.body(p)
	h := sha256.Sum256([]byte(b))
	return hex.EncodeToString(h[:]), nil
}
func (f *fakeConn) Close() error { f.closed = true; return nil }
func (f *fakeConn) DialTunnel(context.Context) (net.Conn, error) {
	return nil, errors.New("no tunnel in this test")
}

type rig struct {
	svc      *Service
	reg      *registry.Store
	conn     *fakeConn
	id       string
	cfgSeen  []sshdirect.Config
	keysSeen []sshdirect.Config
	offered  []sshdirect.HostKey
	staging  string
}

func newRig(t *testing.T, mod func(*registry.AddInput)) *rig {
	t.Helper()
	reg, err := registry.New(filepath.Join(t.TempDir(), "servers.dat"), xorProtector{})
	if err != nil {
		t.Fatal(err)
	}
	in := registry.AddInput{
		Name: "web-01", Host: "web-01.corp.example", Port: 22, User: "svc-unravis", OS: "windows", Auth: registry.AuthPassword, Secret: pw,
		Transports: []string{registry.TransportSSH, registry.TransportBlackbox},
		Folders: map[string][]string{
			registry.FolderBlackbox: {`D:\Blackbox`},
			registry.FolderIIS:      {`C:\inetpub\logs\LogFiles\W3SVC1`},
		},
		BlackboxID: "bb-1",
	}
	if mod != nil {
		mod(&in)
	}
	e, err := reg.Add(in)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{reg: reg, id: e.ID, staging: t.TempDir(), offered: []sshdirect.HostKey{{Algorithm: "ssh-ed25519", Fingerprint: fpA}}}
	r.conn = &fakeConn{files: map[string]map[string]string{
		`D:\Blackbox`: {
			"manifest.json":         `{"format":1,"name":"web-01"}` + "\n",
			"bb-20261126-07.ndjson": `{"t":"2026-11-26T07:00:00Z"}` + "\n" + `{"t":"2026-11-26T07:00:05Z"}` + "\n",
			"recorder.log":          "not wanted\n",
		},
		`C:\inetpub\logs\LogFiles\W3SVC1`: {"u_ex261126.log": "2026-11-26 08:00:00 GET /a 200\n"},
	}}
	r.svc, err = New(Deps{
		Reg:         reg,
		BBKey:       func(id string) (string, error) { return "readkey", nil },
		StagingRoot: r.staging,
		Free:        func(string) (int64, error) { return 1 << 40, nil },
		Connect: func(_ context.Context, c sshdirect.Config) (Conn, error) {
			r.cfgSeen = append(r.cfgSeen, c)
			return r.conn, nil
		},
		FetchKeys: func(_ context.Context, c sshdirect.Config) ([]sshdirect.HostKey, error) {
			r.keysSeen = append(r.keysSeen, c)
			return r.offered, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) pin(t *testing.T) {
	t.Helper()
	if err := r.svc.Pin(context.Background(), r.id, fpA); err != nil {
		t.Fatal(err)
	}
}

func TestNothingConnectsBeforeTheHostKeyIsPinned(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	if _, err := r.svc.Test(ctx, r.id, ""); !errors.Is(err, ErrNeedHostKey) {
		t.Errorf("Test: %v", err)
	}
	if _, err := r.svc.Browse(ctx, r.id, "", `D:\Blackbox`); !errors.Is(err, ErrNeedHostKey) {
		t.Errorf("Browse: %v", err)
	}
	if _, err := r.svc.Plan(ctx, r.id, "", pull.Request{Kind: "blackbox", Dir: `D:\Blackbox`}); !errors.Is(err, ErrNeedHostKey) {
		t.Errorf("Plan: %v", err)
	}
	if _, err := r.svc.Pull(ctx, r.id, "", pull.Request{Kind: "blackbox", Dir: `D:\Blackbox`}, "j1", nil); !errors.Is(err, ErrNeedHostKey) {
		t.Errorf("Pull: %v", err)
	}
	if _, _, err := r.svc.BlackboxSource(ctx, r.id); !errors.Is(err, ErrNeedHostKey) {
		t.Errorf("BlackboxSource: %v", err)
	}
	if len(r.cfgSeen) != 0 {
		t.Fatal("a connection was tried with no pinned key")
	}
}

func TestSeeingTheHostKeySendsNoSecret(t *testing.T) {
	r := newRig(t, nil)
	keys, err := r.svc.HostKeys(context.Background(), r.id)
	if err != nil || len(keys) != 1 || keys[0].Fingerprint != fpA {
		t.Fatalf("%v %v", keys, err)
	}
	c := r.keysSeen[0]
	if c.Secret != "" || c.Passphrase != "" || c.Auth != "" || len(c.Allowed) != 0 {
		t.Fatalf("the host key look carried more than the address: %+v", c)
	}
}

func TestPinsOnlyAKeyTheServerReallyOffers(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	if err := r.svc.Pin(ctx, r.id, fpB); !errors.Is(err, ErrKeyNotShown) {
		t.Fatalf("pinned a key the server did not offer: %v", err)
	}
	if e, _ := r.reg.Get(r.id); e.HostKey != "" {
		t.Fatal("a key was pinned anyway")
	}
	r.pin(t)
	if e, _ := r.reg.Get(r.id); e.HostKey != fpA {
		t.Fatal("not pinned")
	}
	// A second pin never overwrites: a changed key goes through ReplaceKey.
	r.offered = []sshdirect.HostKey{{Fingerprint: fpB}}
	if err := r.svc.Pin(ctx, r.id, fpB); !errors.Is(err, registry.ErrHostKeySet) {
		t.Fatalf("overwrote a pinned key: %v", err)
	}
	if err := r.svc.ReplaceKey(ctx, r.id, fpA, fpB); err != nil {
		t.Fatal(err)
	}
	if e, _ := r.reg.Get(r.id); e.HostKey != fpB {
		t.Fatal("not replaced")
	}
	if err := r.svc.ReplaceKey(ctx, r.id, fpA, fpA); !errors.Is(err, ErrKeyNotShown) {
		t.Fatalf("replaced with a key the server does not offer: %v", err)
	}
}

func TestSignInConfigIsBuiltFromTheSealedListAndAllowsOnlyTheListedFolders(t *testing.T) {
	r := newRig(t, nil)
	r.pin(t)
	if _, err := r.svc.Browse(context.Background(), r.id, "", `D:\Blackbox`); err != nil {
		t.Fatal(err)
	}
	c := r.cfgSeen[0]
	if c.Secret != pw || c.HostKey != fpA || c.Auth != registry.AuthPassword || c.User != "svc-unravis" {
		t.Fatalf("config: %+v", c)
	}
	if len(c.Allowed) != 2 || c.Tunnel != "127.0.0.1:7790" {
		t.Fatalf("allowed/tunnel: %+v", c)
	}
	if !r.conn.closed {
		t.Fatal("the connection was left open")
	}
}

func TestTestReportsEachFolderAndTheMissingBlackboxKey(t *testing.T) {
	r := newRig(t, func(in *registry.AddInput) { in.BlackboxID = "" })
	r.pin(t)
	res, err := r.svc.Test(context.Background(), r.id, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Host.Description == "" || len(res.Folders) != 2 {
		t.Fatalf("%+v", res)
	}
	for _, f := range res.Folders {
		if !f.OK || f.Files == 0 {
			t.Errorf("folder %+v", f)
		}
	}
	if res.Blackbox == nil || res.Blackbox.OK || res.Blackbox.Error != ErrNoBlackbox.Error() {
		t.Fatalf("blackbox: %+v", res.Blackbox)
	}
}

func TestBlackboxPullKeepsTheRecordersOwnManifestAndIsReadAsAFolder(t *testing.T) {
	r := newRig(t, nil)
	r.pin(t)
	res, err := r.svc.Pull(context.Background(), r.id, "", pull.Request{Kind: "blackbox", Dir: `D:\Blackbox`}, "bb1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() || res.BlackboxFolder == nil || res.BlackboxFolder.Kind != "folder" {
		t.Fatalf("%+v", res)
	}
	dir := res.BlackboxFolder.Path
	if got, _ := os.ReadFile(filepath.Join(dir, "manifest.json")); !strings.Contains(string(got), `"web-01"`) {
		t.Fatalf("the recorder's manifest.json is not there as it was: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "bb-20261126-07.ndjson")); err != nil {
		t.Fatal("hourly file missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "recorder.log")); err == nil {
		t.Fatal("a file outside the Blackbox pattern was pulled")
	}
	if _, err := os.Stat(filepath.Join(dir, pull.RecordName)); err != nil {
		t.Fatal("no provenance record")
	}
	if !strings.HasPrefix(dir, filepath.Join(r.staging, r.id)) {
		t.Fatalf("staged outside the server's folder: %s", dir)
	}
}

func TestIISPullHasNoBlackboxFolderAndKeepsEveryFile(t *testing.T) {
	r := newRig(t, nil)
	r.pin(t)
	res, err := r.svc.Pull(context.Background(), r.id, "", pull.Request{Kind: "iis", Dir: `C:\inetpub\logs\LogFiles\W3SVC1`}, "iis1", nil)
	if err != nil || !res.OK() || res.BlackboxFolder != nil {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestPullRefusesAFolderThatIsNotOnTheListForThatKind(t *testing.T) {
	r := newRig(t, nil)
	r.pin(t)
	ctx := context.Background()
	if _, err := r.svc.Pull(ctx, r.id, "", pull.Request{Kind: "iis", Dir: `D:\Blackbox`}, "x1", nil); !errors.Is(err, ErrFolderNotKind) {
		t.Errorf("a Blackbox folder was pulled as IIS: %v", err)
	}
	if _, err := r.svc.Plan(ctx, r.id, "", pull.Request{Kind: "blackbox", Dir: `C:\Windows`}); !errors.Is(err, ErrFolderNotKind) {
		t.Errorf("an unlisted folder was planned: %v", err)
	}
	if _, err := r.svc.Plan(ctx, r.id, "", pull.Request{Kind: "shell", Dir: `D:\Blackbox`}); err == nil {
		t.Error("an unknown kind was accepted")
	}
}

func TestPlanListsWhatWouldBeCopiedAndCopiesNothing(t *testing.T) {
	r := newRig(t, nil)
	r.pin(t)
	p, err := r.svc.Plan(context.Background(), r.id, "", pull.Request{Kind: "blackbox", Dir: `D:\Blackbox`})
	if err != nil || len(p.Files) != 2 || p.Problem != "" {
		t.Fatalf("%v %+v", err, p)
	}
	if entries, _ := os.ReadDir(r.staging); len(entries) != 0 {
		t.Fatal("a plan wrote to disk")
	}
}

func TestServerWithoutSSHCannotBrowseOrPull(t *testing.T) {
	r := newRig(t, func(in *registry.AddInput) {
		in.Transports, in.Folders, in.Auth, in.Secret, in.User = []string{registry.TransportBlackbox}, nil, "", "", ""
	})
	ctx := context.Background()
	if _, err := r.svc.Browse(ctx, r.id, "", `D:\x`); !errors.Is(err, ErrNoSSH) {
		t.Errorf("Browse: %v", err)
	}
	if _, err := r.svc.HostKeys(ctx, r.id); !errors.Is(err, ErrNoSSH) {
		t.Errorf("HostKeys: %v", err)
	}
	if _, cl, err := r.svc.BlackboxSource(ctx, r.id); err != nil || cl.Close() != nil {
		t.Errorf("the Blackbox-only server should still open its recorder: %v", err)
	}
	if len(r.cfgSeen) != 0 {
		t.Error("an SSH connection was tried")
	}
}
