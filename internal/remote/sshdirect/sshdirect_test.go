package sshdirect

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"api-audit-explorer/internal/remote/transport"
)

// fakeServer is an SSH server in this process. handler answers each exec request.
type fakeServer struct {
	t        *testing.T
	ln       net.Listener
	hostKeys []ssh.Signer
	password string
	pubKey   ssh.PublicKey
	kiPrompt string // when set, only keyboard-interactive is offered, with this prompt
	handler  func(cmd string) (out []byte, errText string, exit int)
	onTCP    func(host string, port uint32) net.Conn // direct-tcpip; nil refuses
	tcpSeen  []string

	authAttempts atomic.Int32
	secretSeen   atomic.Int32 // times a password or KI answer arrived
	mu           sync.Mutex
	cmds         []string
}

func newSigner(t *testing.T, kind string) ssh.Signer {
	t.Helper()
	var s ssh.Signer
	var err error
	switch kind {
	case "ed25519":
		_, priv, _ := ed25519.GenerateKey(rand.Reader)
		s, err = ssh.NewSignerFromKey(priv)
	case "ecdsa":
		priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		s, err = ssh.NewSignerFromKey(priv)
	}
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func startServer(t *testing.T, fs *fakeServer) *fakeServer {
	t.Helper()
	fs.t = t
	if len(fs.hostKeys) == 0 {
		fs.hostKeys = []ssh.Signer{newSigner(t, "ed25519")}
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			fs.authAttempts.Add(1)
			fs.secretSeen.Add(1)
			if fs.kiPrompt == "" && string(pw) == fs.password {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			fs.authAttempts.Add(1)
			if fs.pubKey != nil && bytes.Equal(key.Marshal(), fs.pubKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	if fs.kiPrompt != "" {
		cfg.PasswordCallback = nil
		cfg.KeyboardInteractiveCallback = func(_ ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			fs.authAttempts.Add(1)
			ans, err := ch("", "", []string{fs.kiPrompt}, []bool{false})
			if err != nil || len(ans) != 1 {
				return nil, errors.New("denied")
			}
			fs.secretSeen.Add(1)
			if ans[0] == fs.password {
				return nil, nil
			}
			return nil, errors.New("denied")
		}
	}
	for _, k := range fs.hostKeys {
		cfg.AddHostKey(k)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fs.ln = ln
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go fs.serve(c, cfg)
		}
	}()
	return fs
}

func (fs *fakeServer) serve(c net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() == "direct-tcpip" {
			var p struct {
				Host     string
				Port     uint32
				OrigHost string
				OrigPort uint32
			}
			ssh.Unmarshal(nc.ExtraData(), &p)
			fs.mu.Lock()
			fs.tcpSeen = append(fs.tcpSeen, net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
			fs.mu.Unlock()
			if fs.onTCP == nil {
				nc.Reject(ssh.Prohibited, "no")
				continue
			}
			back := fs.onTCP(p.Host, p.Port)
			if back == nil {
				nc.Reject(ssh.ConnectionFailed, "refused")
				continue
			}
			ch, _, err := nc.Accept()
			if err != nil {
				back.Close()
				continue
			}
			go func() { io.Copy(ch, back); ch.CloseWrite() }()
			go func() { io.Copy(back, ch); back.Close() }()
			continue
		}
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for r := range creqs {
				if r.Type != "exec" {
					r.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				ssh.Unmarshal(r.Payload, &p)
				r.Reply(true, nil)
				fs.mu.Lock()
				fs.cmds = append(fs.cmds, p.Command)
				fs.mu.Unlock()
				out, errText, exit := fs.handler(p.Command)
				ch.Write(out)
				if errText != "" {
					ch.Stderr().Write([]byte(errText))
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ S uint32 }{uint32(exit)}))
				ch.Close()
				return
			}
		}()
	}
}

func (fs *fakeServer) commands() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]string(nil), fs.cmds...)
}

func (fs *fakeServer) cfg() Config {
	_, port, _ := net.SplitHostPort(fs.ln.Addr().String())
	n, _ := strconv.Atoi(port)
	return Config{
		Host: "127.0.0.1", Port: n, User: "svc", OS: "linux", Auth: "password", Secret: fs.password,
		HostKey: ssh.FingerprintSHA256(fs.hostKeys[0].PublicKey()), Allowed: []string{"/var/log"}, DialTimeout: 5 * time.Second,
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestFetchHostKeyShowsTheKeyAndSendsNoSecret(t *testing.T) {
	fs := startServer(t, &fakeServer{password: "pw", hostKeys: []ssh.Signer{newSigner(t, "ed25519"), newSigner(t, "ecdsa")}})
	keys, err := FetchHostKey(ctx(t), fs.cfg())
	if err != nil || len(keys) != 2 {
		t.Fatalf("%v %v", keys, err)
	}
	want := map[string]bool{}
	for _, k := range fs.hostKeys {
		want[ssh.FingerprintSHA256(k.PublicKey())] = true
	}
	for _, k := range keys {
		if !want[k.Fingerprint] {
			t.Fatalf("unexpected key %v", k)
		}
	}
	if fs.authAttempts.Load() != 0 || fs.secretSeen.Load() != 0 {
		t.Fatalf("a sign-in was attempted while only showing the key: %d", fs.authAttempts.Load())
	}
}

func TestConnectNeedsAPinAndRefusesADifferentKeyBeforeSigningIn(t *testing.T) {
	fs := startServer(t, &fakeServer{password: "pw", handler: func(string) ([]byte, string, int) { return nil, "", 0 }})
	c := fs.cfg()
	c.HostKey = ""
	if _, err := Connect(ctx(t), c); !errors.Is(err, ErrNoHostKey) {
		t.Fatalf("no pin: %v", err)
	}
	c.HostKey = ssh.FingerprintSHA256(newSigner(t, "ed25519").PublicKey())
	_, err := Connect(ctx(t), c)
	var hk *HostKeyChanged
	if !errors.As(err, &hk) || hk.Pinned != c.HostKey || hk.Presented == c.HostKey {
		t.Fatalf("want HostKeyChanged, got %v", err)
	}
	if fs.secretSeen.Load() != 0 || fs.authAttempts.Load() != 0 {
		t.Fatal("the password was sent to a server whose key did not match")
	}
}

func TestConnectMatchesAnyOfTheServersKeyTypes(t *testing.T) {
	ed, ec := newSigner(t, "ed25519"), newSigner(t, "ecdsa")
	fs := startServer(t, &fakeServer{password: "pw", hostKeys: []ssh.Signer{ed, ec}, handler: func(string) ([]byte, string, int) { return []byte("Linux 6.1\n1\n"), "", 0 }})
	c := fs.cfg()
	c.HostKey = ssh.FingerprintSHA256(ec.PublicKey()) // the admin handed over the ECDSA fingerprint
	cl, err := Connect(ctx(t), c)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if info, err := cl.Probe(ctx(t)); err != nil || info.Description != "Linux 6.1" {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestWrongPasswordIsASignInErrorWithoutTheSecret(t *testing.T) {
	fs := startServer(t, &fakeServer{password: "right"})
	c := fs.cfg()
	c.Secret = "SECRET-WRONG-PASSWORD"
	_, err := Connect(ctx(t), c)
	if !errors.Is(err, ErrSignIn) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("%v", err)
	}
}

func pemKey(t *testing.T, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var blk *pem.Block
	var err error
	if passphrase != "" {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	} else {
		blk, err = ssh.MarshalPrivateKey(priv, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	sp, _ := ssh.NewPublicKey(pub)
	return string(pem.EncodeToMemory(blk)), sp
}

func TestKeyAuthWithAndWithoutPassphrase(t *testing.T) {
	for _, pass := range []string{"", "open sesame"} {
		secret, pub := pemKey(t, pass)
		fs := startServer(t, &fakeServer{pubKey: pub, handler: func(string) ([]byte, string, int) { return []byte("x\n1\n"), "", 0 }})
		c := fs.cfg()
		c.Auth, c.Secret, c.Passphrase = "key", secret, pass
		cl, err := Connect(ctx(t), c)
		if err != nil {
			t.Fatalf("pass=%q: %v", pass, err)
		}
		cl.Close()
		if pass != "" {
			c.Passphrase = "wrong"
			if _, err := Connect(ctx(t), c); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("wrong passphrase: %v", err)
			}
		}
	}
	_ = x509.ParsePKCS8PrivateKey
}

func TestKeyboardInteractiveAnswersOnlyAPasswordPrompt(t *testing.T) {
	fs := startServer(t, &fakeServer{password: "pw", kiPrompt: "Password: ", handler: func(string) ([]byte, string, int) { return []byte("x\n1\n"), "", 0 }})
	cl, err := Connect(ctx(t), fs.cfg())
	if err != nil {
		t.Fatalf("password prompt: %v", err)
	}
	cl.Close()

	fs2 := startServer(t, &fakeServer{password: "pw", kiPrompt: "Verification code: "})
	if _, err := Connect(ctx(t), fs2.cfg()); err == nil {
		t.Fatal("signed in through a one-time-code prompt")
	}
	if fs2.secretSeen.Load() != 0 {
		t.Fatal("the stored password was given to a prompt that did not ask for a password")
	}
}

func TestLinuxCommandsAreExactAndConfinedToAllowedFolders(t *testing.T) {
	var escapeResolve atomic.Bool
	fs := startServer(t, &fakeServer{password: "pw"})
	fs.handler = func(cmd string) ([]byte, string, int) {
		switch {
		case strings.HasPrefix(cmd, "realpath -e -- '/var/log/link'"):
			if escapeResolve.Load() {
				return []byte("/etc/shadow\n"), "", 0
			}
			return []byte("/var/log/link\n"), "", 0
		case strings.HasPrefix(cmd, "realpath -e -- "):
			return []byte(strings.TrimSuffix(strings.TrimPrefix(cmd, "realpath -e -- '"), "'") + "\n"), "", 0
		case strings.HasPrefix(cmd, "find "):
			return []byte("120\t1700000000.5\tbb-20261126-09.ndjson\x005\t1700000001\tbad/name\x007\t1700000002\ttwo\nwords.log\x00"), "", 0
		case strings.HasPrefix(cmd, "sha256sum -- "):
			return []byte(strings.Repeat("ab", 32) + "  /var/log/redis.log\n"), "", 0
		case strings.HasPrefix(cmd, "tail -c +"):
			return []byte("hello"), "", 0
		}
		return nil, "unexpected", 127
	}
	cl, err := Connect(ctx(t), fs.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	files, err := cl.List(ctx(t), "/var/log")
	if err != nil || len(files) != 2 || files[0].Name != "bb-20261126-09.ndjson" || files[0].Size != 120 || files[1].Name != "two\nwords.log" {
		t.Fatalf("%+v %v", files, err)
	}
	if got := fs.commands(); got[len(got)-1] != "find '/var/log' -maxdepth 1 -type f -printf '%s\\t%T@\\t%f\\0'" {
		t.Fatalf("list command: %q", got[len(got)-1])
	}
	d, err := cl.Digest(ctx(t), "/var/log/redis.log")
	if err != nil || d != strings.Repeat("ab", 32) {
		t.Fatalf("%v %v", d, err)
	}
	rc, err := cl.Open(ctx(t), "/var/log/redis.log", 100)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(b) != "hello" {
		t.Fatalf("%q %v", b, err)
	}
	if got := fs.commands(); got[len(got)-1] != "tail -c +101 -- '/var/log/redis.log'" {
		t.Fatalf("open command: %q", got[len(got)-1])
	}

	before := len(fs.commands())
	for _, p := range []string{"/etc/passwd", "/var/log/../etc/passwd", "/var/logx/a", "/var/log/*", "/var/log/a'b", "/var/log/$HOME", "relative/path", ""} {
		if _, err := cl.Open(ctx(t), p, 0); !errors.Is(err, transport.ErrNotAllowed) {
			t.Errorf("Open(%q) = %v", p, err)
		}
		if _, err := cl.Digest(ctx(t), p); !errors.Is(err, transport.ErrNotAllowed) {
			t.Errorf("Digest(%q) = %v", p, err)
		}
		if _, err := cl.List(ctx(t), p); !errors.Is(err, transport.ErrNotAllowed) {
			t.Errorf("List(%q) = %v", p, err)
		}
	}
	if _, err := cl.Open(ctx(t), "/var/log/a", -1); !errors.Is(err, transport.ErrNotAllowed) {
		t.Errorf("negative offset: %v", err)
	}
	if len(fs.commands()) != before {
		t.Fatalf("a refused path still reached the server: %v", fs.commands()[before:])
	}

	escapeResolve.Store(true) // a link inside /var/log that points at /etc/shadow
	if _, err := cl.Open(ctx(t), "/var/log/link", 0); !errors.Is(err, transport.ErrNotAllowed) {
		t.Fatalf("a link out of the allowed folder was followed: %v", err)
	}
	for _, c := range fs.commands() {
		if strings.HasPrefix(c, "tail") && strings.Contains(c, "shadow") {
			t.Fatal("read the escaped path")
		}
	}
}

func TestATruncatedStreamNeverLooksComplete(t *testing.T) {
	fs := startServer(t, &fakeServer{password: "pw"})
	fs.handler = func(cmd string) ([]byte, string, int) {
		if strings.HasPrefix(cmd, "realpath") {
			return []byte("/var/log/big.log\n"), "", 0
		}
		return []byte("partial"), "Input/output error", 1
	}
	cl, err := Connect(ctx(t), fs.cfg())
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	rc, err := cl.Open(ctx(t), "/var/log/big.log", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if _, err := io.ReadAll(rc); !errors.Is(err, ErrCommand) {
		t.Fatalf("a failed read ended as a clean end of file: %v", err)
	}
}

// Windows: the PowerShell scripts are run for real on this machine through the fake server.
func TestWindowsScriptsAgainstARealPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs Windows PowerShell")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("no powershell.exe")
	}
	dir := t.TempDir()
	content := []byte(strings.Repeat("0123456789", 5000)) // 50,000 bytes, crosses the 64 KiB copy size only partly
	content = append(content, bytes.Repeat([]byte{0, 255, 13, 10}, 20000)...)
	if err := os.WriteFile(filepath.Join(dir, "a b.log"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "sub"), 0o700)
	fs := startServer(t, &fakeServer{password: "pw"})
	fs.handler = func(cmd string) ([]byte, string, int) {
		args := strings.Fields(cmd)
		if len(args) == 0 || args[0] != "powershell" {
			return nil, "not powershell", 1
		}
		c := exec.Command(ps, args[1:]...)
		var out, er bytes.Buffer
		c.Stdout, c.Stderr = &out, &er
		code := 0
		if err := c.Run(); err != nil {
			code = 1
		}
		return out.Bytes(), er.String(), code
	}
	cfg := fs.cfg()
	cfg.OS, cfg.Allowed = "windows", []string{dir}
	cl, err := Connect(ctx(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	info, err := cl.Probe(ctx(t))
	if err != nil || info.Description == "" || info.ClockOffset > 5*time.Second || info.ClockOffset < -5*time.Second {
		t.Fatalf("%+v %v", info, err)
	}
	files, err := cl.List(ctx(t), dir)
	if err != nil || len(files) != 1 || files[0].Name != "a b.log" || files[0].Size != int64(len(content)) || time.Since(files[0].MTime) > time.Hour {
		t.Fatalf("%+v %v", files, err)
	}
	file := filepath.Join(dir, "a b.log")
	sum := sha256.Sum256(content)
	if d, err := cl.Digest(ctx(t), file); err != nil || d != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest %v %v", d, err)
	}
	for _, off := range []int64{0, 7, 50000, int64(len(content)) - 3} {
		rc, err := cl.Open(ctx(t), file, off)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || !bytes.Equal(got, content[off:]) {
			t.Fatalf("off %d: got %d bytes, want %d (%v)", off, len(got), len(content[off:]), err)
		}
	}
	if _, err := cl.Open(ctx(t), filepath.Join(dir, "missing.log"), 0); err == nil {
		t.Fatal("a missing file opened")
	}
	if _, err := cl.Open(ctx(t), filepath.Join(dir, "..", "x.log"), 0); !errors.Is(err, transport.ErrNotAllowed) {
		t.Fatalf("%v", err)
	}
}

func TestDialTunnelReachesOnlyTheConfiguredAddress(t *testing.T) {
	rec, srvSide := net.Pipe()
	go func() {
		buf := make([]byte, 4)
		io.ReadFull(srvSide, buf)
		srvSide.Write(append([]byte("echo:"), buf...))
		srvSide.Close()
	}()
	fs := startServer(t, &fakeServer{password: "pw", onTCP: func(string, uint32) net.Conn { return rec }})
	c := fs.cfg()
	cl, err := Connect(ctx(t), c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.DialTunnel(ctx(t)); !errors.Is(err, ErrNoTunnel) {
		t.Fatalf("no tunnel configured: %v", err)
	}
	cl.Close()

	c.Tunnel = "127.0.0.1:7790"
	cl, err = Connect(ctx(t), c)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	conn, err := cl.DialTunnel(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("ping"))
	got, _ := io.ReadAll(conn)
	conn.Close()
	if string(got) != "echo:ping" {
		t.Fatalf("%q", got)
	}
	if seen := fs.tcpSeen; len(seen) != 1 || seen[0] != "127.0.0.1:7790" {
		t.Fatalf("the server was asked to reach %v", seen)
	}

	for _, bad := range []string{"127.0.0.1", "127.0.0.1:0", "127.0.0.1:99999", "a b:80", "host';x:80", ""} {
		c.Tunnel = bad
		cl2, err := Connect(ctx(t), c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cl2.DialTunnel(ctx(t)); err == nil {
			t.Errorf("Tunnel %q was dialled", bad)
		}
		cl2.Close()
	}
	if len(fs.tcpSeen) != 1 {
		t.Fatalf("a refused address still reached the server: %v", fs.tcpSeen)
	}
}

func TestDialTunnelRefusedByTheServer(t *testing.T) {
	fs := startServer(t, &fakeServer{password: "pw"}) // direct-tcpip prohibited
	c := fs.cfg()
	c.Tunnel = "127.0.0.1:7790"
	cl, err := Connect(ctx(t), c)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if _, err := cl.DialTunnel(ctx(t)); !errors.Is(err, ErrTunnelRefused) {
		t.Fatalf("%v", err)
	}
}

func TestKeyboardInteractivePromptShapes(t *testing.T) {
	for prompt, ok := range map[string]bool{
		"Password: ":                      true,
		"svc@host's password: ":           true,
		"Password for svc: ":              true,
		"One-time password: ":             false,
		"Enter your password and token: ": false,
	} {
		fs := startServer(t, &fakeServer{password: "pw", kiPrompt: prompt, handler: func(string) ([]byte, string, int) { return []byte("x\n1\n"), "", 0 }})
		cl, err := Connect(ctx(t), fs.cfg())
		if ok != (err == nil) {
			t.Fatalf("prompt %q: signed in=%v", prompt, err == nil)
		}
		if err == nil {
			cl.Close()
		} else if fs.secretSeen.Load() != 0 {
			t.Fatalf("prompt %q got the stored password", prompt)
		}
	}
}

// A server that accepts the TCP connection and never speaks cannot hold Connect or FetchHostKey open.
func TestASilentServerTimesOutAndCancels(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var held []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		for _, c := range held {
			c.Close()
		}
		mu.Unlock()
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	cfg := Config{Host: "127.0.0.1", Port: n, User: "svc", OS: "linux", Auth: "password", Secret: "pw",
		HostKey: "SHA256:" + strings.Repeat("A", 43), Allowed: []string{"/var/log"}, DialTimeout: 300 * time.Millisecond}

	start := time.Now()
	if _, err := Connect(ctx(t), cfg); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("Connect to a silent server: %v after %v", err, time.Since(start))
	}
	start = time.Now()
	if _, err := FetchHostKey(ctx(t), cfg); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("FetchHostKey from a silent server: %v after %v", err, time.Since(start))
	}
	cfg.DialTimeout = time.Minute
	c, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start = time.Now()
	if _, err := Connect(c, cfg); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("cancelled Connect: %v after %v", err, time.Since(start))
	}
}

// A junction inside an allowed folder that points out of it must not be followed (Windows, real PowerShell).
func TestWindowsJunctionOutOfTheAllowedFolderIsRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs Windows")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("no powershell.exe")
	}
	base := t.TempDir()
	allowed, outside := filepath.Join(base, "logs"), filepath.Join(base, "secret")
	os.MkdirAll(allowed, 0o700)
	os.MkdirAll(outside, 0o700)
	os.WriteFile(filepath.Join(outside, "key.txt"), []byte("private"), 0o600)
	os.WriteFile(filepath.Join(allowed, "ok.log"), []byte("fine"), 0o600)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(allowed, "jn"), outside).CombinedOutput(); err != nil {
		t.Skipf("cannot make a junction here: %v %s", err, out)
	}
	fs := startServer(t, &fakeServer{password: "pw"})
	fs.handler = func(cmd string) ([]byte, string, int) {
		args := strings.Fields(cmd)
		c := exec.Command(ps, args[1:]...)
		var out, er bytes.Buffer
		c.Stdout, c.Stderr = &out, &er
		code := 0
		if c.Run() != nil {
			code = 1
		}
		return out.Bytes(), er.String(), code
	}
	cfg := fs.cfg()
	cfg.OS, cfg.Allowed = "windows", []string{allowed}
	cl, err := Connect(ctx(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if rc, err := cl.Open(ctx(t), filepath.Join(allowed, "jn", "key.txt"), 0); err == nil {
		rc.Close()
		t.Fatal("read a file through a junction that leaves the allowed folder")
	}
	if _, err := cl.List(ctx(t), filepath.Join(allowed, "jn")); err == nil {
		t.Fatal("listed a folder through a junction")
	}
	rc, err := cl.Open(ctx(t), filepath.Join(allowed, "ok.log"), 0)
	if err != nil {
		t.Fatalf("a normal file in the allowed folder: %v", err)
	}
	rc.Close()
}
