// Package sshdirect reads a server over SSH with golang.org/x/crypto/ssh, in this process: no ssh.exe, no password in an environment variable or an argument,
// and a host key that is checked against the fingerprint the person pinned. There is no trust on first use: FetchHostKey only shows what the server presents,
// and sends no secret. It runs file readers through exec channels and builds every command from values the grammar has already accepted.
package sshdirect

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/ssh"

	"api-audit-explorer/internal/remote/grammar"
	"api-audit-explorer/internal/remote/transport"
)

// Errors shown to people.
var (
	ErrNoHostKey     = errors.New("This server's host key has not been pinned yet. Compare its fingerprint and pin it first.")                                            //nolint:staticcheck // shown to the person as a sentence
	ErrSignIn        = errors.New("The server did not accept the sign-in.")                                                                                               //nolint:staticcheck // shown to the person as a sentence
	ErrNoTunnel      = errors.New("This server has no Blackbox address set.")                                                                                             //nolint:staticcheck // shown to the person as a sentence
	ErrTunnelRefused = errors.New("The server did not let this PC reach the Blackbox address. Check that the recorder is running there and which address it listens on.") //nolint:staticcheck // shown to the person as a sentence
	ErrUnreachable   = errors.New("Could not reach the server.")                                                                                                          //nolint:staticcheck // shown to the person as a sentence
	ErrCommand       = errors.New("The server could not complete that request.")                                                                                          //nolint:staticcheck // shown to the person as a sentence
	ErrListTooLarge  = errors.New("That folder holds too many files to list at once.")                                                                                    //nolint:staticcheck // shown to the person as a sentence
)

// HostKeyChanged means the server presented a key other than the pinned one. It is a hard stop: nothing was sent.
type HostKeyChanged struct{ Pinned, Presented string }

func (e *HostKeyChanged) Error() string {
	return "The server's host key is not the one you pinned. Nothing was sent. Pinned " + e.Pinned + ", presented " + e.Presented + "."
}

// HostKey is one key a server offered.
type HostKey struct {
	Algorithm   string `json:"algorithm"`
	Fingerprint string `json:"fingerprint"`
}

// Config is one server. Secret and Passphrase come from the sealed registry and are used only to sign in.
type Config struct {
	Host, User, OS     string
	Port               int
	Auth               string // "key" or "password"
	Secret, Passphrase string
	HostKey            string   // pinned SHA256 fingerprint
	Allowed            []string // folders that may be read
	Tunnel             string   // host:port, as the server sees it, that DialTunnel may reach (the Blackbox status address); empty means no tunnel
	DialTimeout        time.Duration
	Dial               func(ctx context.Context, network, addr string) (net.Conn, error) // tests and tunnels
}

const (
	maxListBytes = 4 << 20
	maxListFiles = 20000
	maxStderr    = 4 << 10
)

var hostKeyAlgorithms = []string{
	ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

func (c Config) check() error {
	for _, r := range []string{grammar.Host(c.Host), grammar.Port(c.Port), grammar.User(c.User)} {
		if r != "" {
			return errors.New(r)
		}
	}
	if c.OS != "windows" && c.OS != "linux" {
		return errors.New("Say whether the server runs Windows or Linux.") //nolint:staticcheck // shown to the person as a sentence
	}
	return nil
}

func (c Config) addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

func (c Config) dial(ctx context.Context) (net.Conn, error) {
	d := c.DialTimeout
	if d <= 0 {
		d = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	if c.Dial != nil {
		return c.Dial(ctx, "tcp", c.addr())
	}
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", c.addr())
}

// FetchHostKey shows what keys the server presents. It signs in with nothing: the handshake is stopped at the host key check, so no secret leaves this PC.
func FetchHostKey(ctx context.Context, c Config) ([]HostKey, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	var out []HostKey
	var firstErr error
	for _, algo := range hostKeyAlgorithms {
		conn, err := c.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			firstErr = ErrUnreachable
			break
		}
		fp, err := presented(ctx, c, conn, algo)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		dup := false
		for _, k := range out {
			if k.Fingerprint == fp {
				dup = true
			}
		}
		if !dup {
			out = append(out, HostKey{Algorithm: algo, Fingerprint: fp})
		}
	}
	if len(out) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, ErrUnreachable
	}
	return out, nil
}

type stopHandshake struct{}

func (stopHandshake) Error() string { return "stopped at the host key" }

// presented offers only algo, records the host key and stops before any authentication.
func presented(ctx context.Context, c Config, conn net.Conn, algo string) (string, error) {
	defer conn.Close() //nolint:errcheck // best-effort cleanup, nothing to do on failure
	var fp string
	cfg := &ssh.ClientConfig{
		User:              c.User,
		HostKeyAlgorithms: []string{algo},
		Timeout:           handshakeTimeout(c),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp = ssh.FingerprintSHA256(key)
			return stopHandshake{}
		},
	}
	sc, _, _, herr := handshake(ctx, conn, c, cfg)
	if sc != nil {
		sc.Close() //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
	}
	if fp != "" {
		return fp, nil
	}
	if herr != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", ErrUnreachable
}

// handshake runs the SSH handshake with a deadline and with cancellation: a server that accepts the connection and then says nothing cannot hold it open.
func handshake(ctx context.Context, conn net.Conn, c Config, cfg *ssh.ClientConfig) (ssh.Conn, <-chan ssh.NewChannel, <-chan *ssh.Request, error) {
	conn.SetDeadline(time.Now().Add(handshakeTimeout(c)))   //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
	stop := context.AfterFunc(ctx, func() { conn.Close() }) //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
	sc, chans, reqs, err := ssh.NewClientConn(conn, c.addr(), cfg)
	if !stop() { // cancelled while the handshake ran
		if sc != nil {
			sc.Close() //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
		}
		return nil, nil, nil, ctx.Err()
	}
	if err == nil {
		conn.SetDeadline(time.Time{}) //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
	}
	return sc, chans, reqs, err
}

func handshakeTimeout(c Config) time.Duration {
	if c.DialTimeout > 0 {
		return c.DialTimeout
	}
	return 15 * time.Second
}

func (c Config) authMethods() ([]ssh.AuthMethod, error) {
	switch c.Auth {
	case "key":
		var signer ssh.Signer
		var err error
		if c.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(c.Secret), []byte(c.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(c.Secret))
		}
		if err != nil {
			return nil, errors.New("The private key could not be read (wrong passphrase, or not an OpenSSH or PEM key).") //nolint:staticcheck // shown to the person as a sentence
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case "password":
		// Many Linux servers take a password only through keyboard-interactive. Answer a single prompt that asks for a password; refuse anything
		// else, so a one-time-code or other prompt is never answered with the stored password.
		ki := ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			if len(questions) == 0 {
				return nil, nil
			}
			if len(questions) == 1 && rePasswordPrompt.MatchString(questions[0]) {
				return []string{c.Secret}, nil
			}
			return nil, errors.New("The server asked for something other than a password.") //nolint:staticcheck // shown to the person as a sentence
		})
		return []ssh.AuthMethod{ssh.Password(c.Secret), ki}, nil
	}
	return nil, errors.New("Sign-in must be a key or a password.") //nolint:staticcheck // shown to the person as a sentence
}

// Client is a connected server. Safe for concurrent use; each request opens its own channel.
type Client struct {
	cfg  Config
	conn *ssh.Client
}

// Connect signs in, but only to a server whose host key is the pinned one. When the server offers several key types the connection tries each until
// one matches the pin; if none does, it stops with HostKeyChanged.
func Connect(ctx context.Context, c Config) (*Client, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	if grammar.Fingerprint(c.HostKey) != "" {
		return nil, ErrNoHostKey
	}
	methods, err := c.authMethods()
	if err != nil {
		return nil, err
	}
	var seen string
	for _, algo := range hostKeyAlgorithms {
		raw, err := c.dial(ctx)
		if err != nil {
			return nil, ErrUnreachable
		}
		var got string
		cfg := &ssh.ClientConfig{
			User:              c.User,
			Auth:              methods,
			HostKeyAlgorithms: []string{algo},
			Timeout:           handshakeTimeout(c),
			HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
				got = ssh.FingerprintSHA256(key)
				if got != c.HostKey {
					return stopHandshake{}
				}
				return nil
			},
		}
		sc, chans, reqs, herr := handshake(ctx, raw, c, cfg)
		if herr == nil {
			return &Client{cfg: c, conn: ssh.NewClient(sc, chans, reqs)}, nil
		}
		raw.Close() //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
		if got != "" && got != c.HostKey {
			if seen == "" {
				seen = got
			}
			continue // another key type may be the pinned one
		}
		if got == "" {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // this server does not offer that algorithm
		}
		return nil, ErrSignIn // key matched; sign-in failed
	}
	if seen != "" {
		return nil, &HostKeyChanged{Pinned: c.HostKey, Presented: seen}
	}
	return nil, ErrUnreachable
}

// Close ends the connection.
func (cl *Client) Close() error { return cl.conn.Close() }

// TunnelTarget splits and checks the one address a tunnel may reach.
func TunnelTarget(addr string) (host string, port int, err error) {
	h, p, serr := net.SplitHostPort(addr)
	if serr != nil {
		return "", 0, errors.New("The Blackbox address must look like 127.0.0.1:7790.") //nolint:staticcheck // shown to the person as a sentence
	}
	n, aerr := strconv.Atoi(p)
	if aerr != nil {
		return "", 0, errors.New("The Blackbox address must look like 127.0.0.1:7790.") //nolint:staticcheck // shown to the person as a sentence
	}
	for _, r := range []string{grammar.Host(h), grammar.Port(n)} {
		if r != "" {
			return "", 0, errors.New(r)
		}
	}
	return h, n, nil
}

// DialTunnel opens a connection from the server to the one address named in Config.Tunnel, through the signed-in connection.
// It takes no address argument on purpose: a caller cannot use it to reach anything else on the server's network.
func (cl *Client) DialTunnel(ctx context.Context) (net.Conn, error) {
	if cl.cfg.Tunnel == "" {
		return nil, ErrNoTunnel
	}
	if _, _, err := TunnelTarget(cl.cfg.Tunnel); err != nil {
		return nil, err
	}
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := cl.conn.Dial("tcp", cl.cfg.Tunnel)
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, ErrTunnelRefused
		}
		return r.c, nil
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.c != nil {
				r.c.Close() //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
			}
		}()
		return nil, ctx.Err()
	}
}

// ---- commands ----

func q(s string) string { return "'" + s + "'" } // grammar has refused quotes already; see literal

// literal returns s as a quoted shell/PowerShell literal, or an error if it holds a character that could escape the quotes.
func literal(s string) (string, error) {
	if strings.ContainsAny(s, "'\"`$%\r\n\x00") || grammar.PathCharset(s) != "" {
		return "", transport.ErrNotAllowed
	}
	return q(s), nil
}

func psCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, v := range u {
		b = append(b, byte(v), byte(v>>8)) //nolint:gosec // value is range-checked or small by construction
	}
	return "powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand " + base64.StdEncoding.EncodeToString(b)
}

// rePasswordPrompt matches the usual password prompts ("Password:", "user@host's password:", "Password for user:") and nothing like "One-time password:".
var rePasswordPrompt = regexp.MustCompile(`(?i)^\s*(\S+'s\s+)?password(\s+for\s+\S+)?\s*:?\s*$`)

const psPrelude = "$ErrorActionPreference='Stop';[Console]::OutputEncoding=[Text.Encoding]::UTF8;"

type cmdSet struct {
	resolve func(p, root string) string
	list    func(dir string) string
	open    func(p string, off int64) string
	digest  func(p string) string
	probe   string
}

func commands(os string) cmdSet {
	if os == "windows" {
		return cmdSet{
			// Resolve-Path does not follow links, so every item from the file up to the allowed folder is checked: none may be a link or junction.
			resolve: func(p, root string) string {
				return psCommand(psPrelude + "$root=" + root + ";$r=(Resolve-Path -LiteralPath " + p + ").ProviderPath;$c=Get-Item -LiteralPath $r -Force;" +
					"while($c -and ($c.FullName.TrimEnd('\\') -ne $root.TrimEnd('\\'))){if($c.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'link'};if($c.PSIsContainer){$c=$c.Parent}else{$c=$c.Directory}};" +
					"if(-not $c){throw 'outside'};$r")
			},
			list: func(d string) string {
				return psCommand(psPrelude + "Get-ChildItem -LiteralPath " + d + " -File | ForEach-Object { \"{0}`t{1}`t{2}\" -f $_.Length,([DateTimeOffset]$_.LastWriteTimeUtc).ToUnixTimeSeconds(),$_.Name }")
			},
			open: func(p string, off int64) string {
				return psCommand(psPrelude + "$f=[IO.File]::Open(" + p + ",'Open','Read','ReadWrite');try{$f.Seek(" + strconv.FormatInt(off, 10) +
					",'Begin')|Out-Null;$o=[Console]::OpenStandardOutput();$f.CopyTo($o,65536);$o.Flush()}finally{$f.Dispose()}")
			},
			digest: func(p string) string {
				return psCommand(psPrelude + "(Get-FileHash -Algorithm SHA256 -LiteralPath " + p + ").Hash.ToLower()")
			},
			probe: psCommand(psPrelude + "(Get-CimInstance Win32_OperatingSystem).Caption;[DateTimeOffset]::UtcNow.ToUnixTimeSeconds()"),
		}
	}
	return cmdSet{
		resolve: func(p, _ string) string { return "realpath -e -- " + p },
		list: func(d string) string {
			return "find " + d + " -maxdepth 1 -type f -printf '%s\\t%T@\\t%f\\0'"
		},
		open:   func(p string, off int64) string { return "tail -c +" + strconv.FormatInt(off+1, 10) + " -- " + p },
		digest: func(p string) string { return "sha256sum -- " + p },
		probe:  "uname -sr; date +%s",
	}
}

// ---- running ----

type result struct {
	out    []byte
	stderr string
}

func (cl *Client) run(ctx context.Context, cmd string, limit int) (result, error) {
	sess, err := cl.conn.NewSession()
	if err != nil {
		return result{}, ErrUnreachable
	}
	defer sess.Close() //nolint:errcheck // best-effort cleanup, nothing to do on failure
	var stdout, stderr limited
	stdout.max, stderr.max = limit, maxStderr
	sess.Stdout, sess.Stderr = &stdout, &stderr
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case <-ctx.Done():
		sess.Close() //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
		return result{}, ctx.Err()
	case err := <-done:
		if stdout.over {
			return result{}, ErrListTooLarge
		}
		if err != nil {
			return result{stderr: stderr.String()}, ErrCommand
		}
		return result{out: stdout.b, stderr: stderr.String()}, nil
	}
}

// Exec runs one command string that the caller has built from grammar-checked values (the Salt jump-host route builds the `salt` command
// this way) and returns its output. A command that exits non-zero, or prints more than limit bytes, is an error.
func (cl *Client) Exec(ctx context.Context, cmd string, limit int) ([]byte, error) {
	res, err := cl.run(ctx, cmd, limit)
	if err != nil {
		return nil, err
	}
	return res.out, nil
}

type limited struct {
	b    []byte
	max  int
	over bool
}

func (l *limited) Write(p []byte) (int, error) {
	if len(l.b)+len(p) > l.max {
		l.over = true
		return len(p), nil
	}
	l.b = append(l.b, p...)
	return len(p), nil
}
func (l *limited) String() string { return string(l.b) }

// ---- transport.Transport ----

var _ transport.Transport = (*Client)(nil)

// plain checks that p is a literal path inside the allowed folders.
func (cl *Client) plain(p string) error {
	if strings.ContainsAny(p, "*?") || grammar.StrictPath(p) != "" || grammar.PathAllowed(p, cl.cfg.Allowed) != "" {
		return transport.ErrNotAllowed
	}
	return nil
}

// rootOf is the allowed folder that holds p (the first that matches, as PathAllowed matches), written with the server's separators.
func (cl *Client) rootOf(p string) string {
	norm := strings.ReplaceAll(p, `\`, "/")
	for _, a := range cl.cfg.Allowed {
		an := strings.TrimRight(strings.ReplaceAll(a, `\`, "/"), "/")
		if an == "" {
			continue
		}
		hit := norm == an || strings.HasPrefix(norm, an+"/")
		if cl.cfg.OS == "windows" {
			hit = strings.EqualFold(norm, an) || (len(norm) > len(an) && strings.EqualFold(norm[:len(an)+1], an+"/"))
			if hit {
				return strings.ReplaceAll(an, "/", `\`)
			}
		} else if hit {
			return an
		}
	}
	return ""
}

// resolve asks the server where p really points (a link out of the allowed folders is caught here) and checks the answer again.
func (cl *Client) resolve(ctx context.Context, p string) (string, error) {
	if err := cl.plain(p); err != nil {
		return "", err
	}
	lit, err := literal(p)
	if err != nil {
		return "", err
	}
	root, err := literal(cl.rootOf(p))
	if err != nil {
		return "", transport.ErrNotAllowed
	}
	res, err := cl.run(ctx, commands(cl.cfg.OS).resolve(lit, root), 4096)
	if err != nil {
		return "", err
	}
	real := strings.TrimRight(string(res.out), "\r\n")
	if real == "" || strings.ContainsAny(real, "\r\n") {
		return "", ErrCommand
	}
	if err := cl.plain(real); err != nil {
		return "", err
	}
	return real, nil
}

// Probe tests the connection and reports what the server is.
func (cl *Client) Probe(ctx context.Context) (transport.HostInfo, error) {
	before := time.Now()
	res, err := cl.run(ctx, commands(cl.cfg.OS).probe, 4096)
	if err != nil {
		return transport.HostInfo{}, err
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(res.out), "\r", "")), "\n")
	info := transport.HostInfo{OS: cl.cfg.OS}
	if len(lines) >= 1 {
		info.Description = strings.TrimSpace(lines[0])
	}
	if len(lines) >= 2 {
		if sec, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
			mid := before.Add(time.Since(before) / 2)
			info.ClockOffset = time.Unix(sec, 0).Sub(mid).Round(time.Second)
		}
	}
	return info, nil
}

// List lists the files directly inside dir (not subfolders).
func (cl *Client) List(ctx context.Context, dir string) ([]transport.FileInfo, error) {
	real, err := cl.resolve(ctx, dir)
	if err != nil {
		return nil, err
	}
	lit, err := literal(real)
	if err != nil {
		return nil, err
	}
	res, err := cl.run(ctx, commands(cl.cfg.OS).list(lit), maxListBytes)
	if err != nil {
		return nil, err
	}
	var out []transport.FileInfo
	sep, text := "\n", strings.ReplaceAll(string(res.out), "\r", "") // Windows names cannot hold a newline
	if cl.cfg.OS != "windows" {
		sep, text = "\x00", string(res.out) // Linux names can, so find ends each entry with a NUL
	}
	for _, line := range strings.Split(text, sep) {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || parts[2] == "" || strings.ContainsAny(parts[2], "\x00/\\") || len(parts[2]) > 255 {
			continue // a name we would not accept later is not offered
		}
		size, e1 := strconv.ParseInt(parts[0], 10, 64)
		secs, e2 := strconv.ParseFloat(parts[1], 64)
		if e1 != nil || e2 != nil || size < 0 {
			continue
		}
		out = append(out, transport.FileInfo{Name: parts[2], Size: size, MTime: time.Unix(int64(secs), 0).UTC()})
		if len(out) > maxListFiles {
			return nil, ErrListTooLarge
		}
	}
	return out, nil
}

// Digest returns the SHA-256 the server computes for the whole file.
func (cl *Client) Digest(ctx context.Context, path string) (string, error) {
	real, err := cl.resolve(ctx, path)
	if err != nil {
		return "", err
	}
	lit, err := literal(real)
	if err != nil {
		return "", err
	}
	res, err := cl.run(ctx, commands(cl.cfg.OS).digest(lit), 4096)
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(res.out))
	if len(f) == 0 || len(f[0]) != 64 {
		return "", ErrCommand
	}
	if _, err := hex.DecodeString(f[0]); err != nil {
		return "", ErrCommand
	}
	return strings.ToLower(f[0]), nil
}

// Open streams the file from byte offset off. Reading to the end without an error means the server finished the read.
func (cl *Client) Open(ctx context.Context, path string, off int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, transport.ErrNotAllowed
	}
	real, err := cl.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	lit, err := literal(real)
	if err != nil {
		return nil, err
	}
	sess, err := cl.conn.NewSession()
	if err != nil {
		return nil, ErrUnreachable
	}
	pipe, err := sess.StdoutPipe()
	if err != nil {
		sess.Close() //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
		return nil, ErrUnreachable
	}
	var stderr limited
	stderr.max = maxStderr
	sess.Stderr = &stderr
	if err := sess.Start(commands(cl.cfg.OS).open(lit, off)); err != nil {
		sess.Close() //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
		return nil, ErrCommand
	}
	r := &stream{sess: sess, pipe: pipe}
	stop := context.AfterFunc(ctx, func() { r.Close() }) //nolint:errcheck,gosec // best-effort cleanup, nothing to do on failure
	r.stop = stop
	return r, nil
}

type stream struct {
	sess *ssh.Session
	pipe io.Reader
	stop func() bool
	once sync.Once
}

func (s *stream) Read(p []byte) (int, error) {
	n, err := s.pipe.Read(p)
	if err == io.EOF {
		// the data is complete only if the command ended cleanly
		if werr := s.sess.Wait(); werr != nil {
			return n, ErrCommand
		}
	}
	return n, err
}

func (s *stream) Close() error {
	var err error
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
		}
		err = s.sess.Close()
	})
	return err
}

// Sum is a helper for callers that hash while they copy.
func Sum(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil)), n, err
}
