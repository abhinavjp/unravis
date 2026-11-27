// Package saltmaster reads a machine through a Salt master, with one sign-in to a jump host (the server where the `salt` command runs).
// It is the main route of the Servers screen: nobody needs a login to the machines themselves.
//
// Every command is built here from values the grammar package has accepted, in the same shape as tools/remote (PR 348):
//
//	[sudo -n] salt|salt-key|salt-run --out=json --no-color [--static -t N] 'word' 'word' ...
//
// Only exact minion ids are ever targeted (no wildcards, lists or compounds). Nothing here writes to a machine, accepts or removes a key, or
// applies a state. Free-form commands are never accepted: the few `cmd.run` forms below (list, size, hash, one-chunk read, link check) are
// fixed templates whose only inputs are a checked path and two numbers. They go beyond the read verbs the PowerShell tool allows
// (it has no binary read or hash), so they need the same security review as that tool.
package saltmaster

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/remote/grammar"
	"api-audit-explorer/internal/remote/transport"
)

// Errors shown to people.
var (
	ErrNoMinion   = errors.New("The Salt master did not give an answer for that machine.")
	ErrOffline    = errors.New("That machine did not answer. It may be off, or not connected to the Salt master.")
	ErrBadReply   = errors.New("The Salt master gave an answer Unravis could not read.")
	ErrMasterCmd  = errors.New("The Salt master could not run that request.")
	ErrTooLarge   = errors.New("That folder holds too many files to list at once.")
	ErrShortChunk = errors.New("The machine sent a piece of the file that was cut short. Nothing was kept from it.")
)

// Runner runs one command on the jump host. sshdirect.Client satisfies it.
type Runner interface {
	Exec(ctx context.Context, cmd string, limit int) ([]byte, error)
}

// Master is the jump host where `salt` runs.
type Master struct {
	R          Runner
	OS         string // the jump host: "linux" or "windows" (Windows assumes the SSH default shell, cmd.exe: not verified)
	Sudo       bool   // prefix `sudo -n`; Linux only
	TimeoutSec int    // `salt -t` for quick calls (default 30)
	// HashTimeoutSec is `salt -t` for hashing a whole file (default 600).
	HashTimeoutSec int
	// ChunkBytes is the raw size of one pulled piece (default 48 KiB, about 64 KiB on the wire). Not verified against a real master's limits.
	ChunkBytes int
}

const (
	maxListBytes = 4 << 20
	maxListFiles = 20000
	maxMachines  = 2000
	defChunk     = 48 << 10
	maxChunk     = 192 << 10
)

func (m Master) timeout() int {
	if m.TimeoutSec > 0 {
		return m.TimeoutSec
	}
	return 30
}

func (m Master) hashTimeout() int {
	if m.HashTimeoutSec > 0 {
		return m.HashTimeoutSec
	}
	return 600
}

func (m Master) chunk() int {
	switch {
	case m.ChunkBytes <= 0:
		return defChunk
	case m.ChunkBytes > maxChunk:
		return maxChunk
	}
	return m.ChunkBytes
}

// qc is the quote a script uses around its own path. A single quote is safe inside the jump host shell word in both cases (escaped for sh, inside double quotes for cmd).
func (m Master) qc() string { return "'" }

// shWord quotes one word for the jump host's shell.
func (m Master) shWord(s string) (string, error) {
	if strings.ContainsAny(s, "\x00\r\n") {
		return "", transport.ErrNotAllowed
	}
	if m.OS == "windows" {
		if strings.ContainsAny(s, `"%`) {
			return "", transport.ErrNotAllowed
		}
		return `"` + s + `"`, nil
	}
	return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`, nil
}

// command builds one remote command string.
func (m Master) command(tool string, timeoutSec int, words ...string) (string, error) {
	var parts []string
	if m.Sudo && m.OS != "windows" {
		parts = append(parts, "sudo", "-n")
	}
	parts = append(parts, tool, "--out=json", "--no-color")
	if tool == "salt" {
		parts = append(parts, "--static", "-t", strconv.Itoa(timeoutSec))
	}
	for _, w := range words {
		q, err := m.shWord(w)
		if err != nil {
			return "", err
		}
		parts = append(parts, q)
	}
	return strings.Join(parts, " "), nil
}

func (m Master) exec(ctx context.Context, cmd string, limit int) ([]byte, error) {
	if m.R == nil {
		return nil, ErrMasterCmd
	}
	return m.R.Exec(ctx, cmd, limit)
}

// firstJSON returns the JSON document in out, ignoring anything the master printed before it (warnings).
func firstJSON(out []byte) ([]byte, error) {
	// Salt may print warnings such as "[WARNING] ..." before the JSON, so try each possible start until one parses.
	s := string(out)
	for i := 0; i < len(s); i++ {
		if s[i] != '{' && s[i] != '[' {
			continue
		}
		if i > 0 && s[i-1] != '\n' {
			continue // the document starts a line, so a brace inside a warning is never taken for it
		}
		var raw json.RawMessage
		dec := json.NewDecoder(strings.NewReader(s[i:]))
		if dec.Decode(&raw) != nil {
			continue
		}
		if len(strings.TrimSpace(s[i+int(dec.InputOffset()):])) != 0 {
			continue // text after the document means this was not the reply
		}
		return raw, nil
	}
	return nil, ErrBadReply
}

// ---- the master itself ----

// Machine is one machine the master knows.
type Machine struct {
	ID     string `json:"id"`
	State  string `json:"state"`            // accepted, pending, denied or rejected
	Online *bool  `json:"online,omitempty"` // nil when the master could not say
}

// Machines lists the machines the master has keys for, and which accepted ones answer (`salt-key -L`, then `salt-run manage.status`).
// It never accepts, rejects or deletes a key.
func (m Master) Machines(ctx context.Context) ([]Machine, error) {
	cmd, err := m.command("salt-key", 0, "-L")
	if err != nil {
		return nil, err
	}
	out, err := m.exec(ctx, cmd, 1<<20)
	if err != nil {
		return nil, ErrMasterCmd
	}
	doc, err := firstJSON(out)
	if err != nil {
		return nil, err
	}
	var keys map[string][]string
	if err := json.Unmarshal(doc, &keys); err != nil {
		return nil, ErrBadReply
	}
	var list []Machine
	for field, state := range map[string]string{"minions": "accepted", "minions_pre": "pending", "minions_denied": "denied", "minions_rejected": "rejected"} {
		for _, id := range keys[field] {
			if grammar.MinionID(id) != "" {
				continue // an id we would never target is not offered
			}
			list = append(list, Machine{ID: id, State: state})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	if len(list) > maxMachines {
		list = list[:maxMachines]
	}
	// Which accepted machines answer. A failure here only leaves "online" unknown.
	if up, ok := m.online(ctx); ok {
		for i := range list {
			if list[i].State == "accepted" {
				v := up[list[i].ID]
				list[i].Online = &v
			}
		}
	}
	return list, nil
}

func (m Master) online(ctx context.Context) (map[string]bool, bool) {
	cmd, err := m.command("salt-run", 0, "manage.status")
	if err != nil {
		return nil, false
	}
	out, err := m.exec(ctx, cmd, 1<<20)
	if err != nil {
		return nil, false
	}
	doc, err := firstJSON(out)
	if err != nil {
		return nil, false
	}
	var st struct {
		Up   []string `json:"up"`
		Down []string `json:"down"`
	}
	if json.Unmarshal(doc, &st) != nil || (st.Up == nil && st.Down == nil) {
		return nil, false
	}
	up := map[string]bool{}
	for _, id := range st.Up {
		up[id] = true
	}
	return up, true
}

// call runs one function on one exact minion and returns that minion's value.
func (m Master) call(ctx context.Context, id string, timeoutSec int, fn string, args ...string) (json.RawMessage, error) {
	if r := grammar.MinionID(id); r != "" {
		return nil, errors.New(r)
	}
	for _, a := range args {
		if r := grammar.YamlSafe(a); r != "" {
			return nil, errors.New(r)
		}
	}
	words := append([]string{id, fn}, args...)
	cmd, err := m.command("salt", timeoutSec, words...)
	if err != nil {
		return nil, err
	}
	out, err := m.exec(ctx, cmd, 1<<20)
	if err != nil {
		return nil, ErrMasterCmd
	}
	doc, err := firstJSON(out)
	if err != nil {
		return nil, ErrBadReply
	}
	var reply map[string]json.RawMessage
	if json.Unmarshal(doc, &reply) != nil {
		return nil, ErrBadReply
	}
	v, ok := reply[id]
	if !ok {
		return nil, ErrNoMinion
	}
	return v, nil
}

// Ping asks one machine to answer through the master (`test.ping`).
func (m Master) Ping(ctx context.Context, id string) error {
	v, err := m.call(ctx, id, m.timeout(), "test.ping")
	if err != nil {
		return err
	}
	var ok bool
	if json.Unmarshal(v, &ok) != nil || !ok {
		return ErrOffline
	}
	return nil
}

// Identity is what a machine says it is.
type Identity struct {
	OS          string // windows or linux
	Description string
}

// Identify reads the machine's operating system (`grains.item`).
func (m Master) Identify(ctx context.Context, id string) (Identity, error) {
	v, err := m.call(ctx, id, m.timeout(), "grains.item", "kernel", "osfullname", "osrelease")
	if err != nil {
		return Identity{}, err
	}
	var g struct {
		Kernel, Osfullname, Osrelease string
	}
	raw := map[string]any{}
	if json.Unmarshal(v, &raw) != nil {
		return Identity{}, ErrOffline
	}
	str := func(k string) string { s, _ := raw[k].(string); return strings.TrimSpace(s) }
	g.Kernel, g.Osfullname, g.Osrelease = str("kernel"), str("osfullname"), str("osrelease")
	if g.Kernel == "" {
		return Identity{}, ErrOffline
	}
	id2 := Identity{Description: strings.TrimSpace(g.Osfullname + " " + g.Osrelease)}
	if strings.EqualFold(g.Kernel, "windows") {
		id2.OS = "windows"
	} else {
		id2.OS = "linux"
	}
	if id2.Description == "" {
		id2.Description = g.Kernel
	}
	return id2, nil
}

// ---- one machine ----

// Transport reads one machine through the master. It satisfies transport.Transport.
type Transport struct {
	m       Master
	id      string
	os      string
	allowed []string
}

var _ transport.Transport = (*Transport)(nil)

// For returns the transport for one machine. os is the machine's operating system ("linux" or "windows", from Identify); allowed are the
// folders that may be read.
func (m Master) For(id, os string, allowed []string) (*Transport, error) {
	if r := grammar.MinionID(id); r != "" {
		return nil, errors.New(r)
	}
	if os != "windows" && os != "linux" {
		return nil, errors.New("Say whether the machine runs Windows or Linux.")
	}
	return &Transport{m: m, id: id, os: os, allowed: allowed}, nil
}

// Close does nothing: the jump-host connection belongs to whoever opened it.
func (t *Transport) Close() error { return nil }

func (t *Transport) win() bool { return t.os == "windows" }

// shellKwarg is how the command string is run on the machine.
func (t *Transport) kwargs(needShell bool) string {
	if t.win() {
		return "shell=powershell"
	}
	if needShell {
		return "python_shell=True" // only the chunk read, whose inputs are a checked path and two numbers
	}
	return "python_shell=False"
}

func (t *Transport) plain(p string) error {
	if strings.ContainsAny(p, "*?") || grammar.StrictPath(p) != "" || grammar.PathAllowed(p, t.allowed) != "" {
		return transport.ErrNotAllowed
	}
	if !t.win() && strings.Contains(p, `\`) {
		return transport.ErrNotAllowed
	}
	return nil
}

func (t *Transport) lit(p string) string { q := t.m.qc(); return q + p + q }

// rootOf is the allowed folder that holds p, written with the machine's separators.
func (t *Transport) rootOf(p string) string {
	norm := strings.ReplaceAll(p, `\`, "/")
	for _, a := range t.allowed {
		an := strings.TrimRight(strings.ReplaceAll(a, `\`, "/"), "/")
		if an == "" {
			continue
		}
		if t.win() {
			if strings.EqualFold(norm, an) || (len(norm) > len(an) && strings.EqualFold(norm[:len(an)+1], an+"/")) {
				return strings.ReplaceAll(an, "/", `\`)
			}
		} else if norm == an || strings.HasPrefix(norm, an+"/") {
			return an
		}
	}
	return ""
}

// run executes one cmd.run on the machine and returns its text.
func (t *Transport) run(ctx context.Context, timeoutSec int, cmdline string, needShell bool) (string, error) {
	v, err := t.m.call(ctx, t.id, timeoutSec, "cmd.run", cmdline, t.kwargs(needShell))
	if err != nil {
		return "", err
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", ErrOffline // false, or an object: the machine did not return
	}
	return strings.ReplaceAll(s, "\r", ""), nil
}

var reMinionDown = regexp.MustCompile(`(?i)^(minion did not return|\[not connected\]|no response)`)

func (t *Transport) text(ctx context.Context, timeoutSec int, cmdline string, needShell bool) (string, error) {
	s, err := t.run(ctx, timeoutSec, cmdline, needShell)
	if err != nil {
		return "", err
	}
	if reMinionDown.MatchString(strings.TrimSpace(s)) {
		return "", ErrOffline
	}
	return strings.TrimRight(s, "\n"), nil
}

// resolve asks the machine where p really points (a link or junction out of the allowed folders is caught here) and checks the answer again.
func (t *Transport) resolve(ctx context.Context, p string) (string, error) {
	if err := t.plain(p); err != nil {
		return "", err
	}
	var cmdline string
	if t.win() {
		root := t.rootOf(p)
		if root == "" {
			return "", transport.ErrNotAllowed
		}
		// No Resolve-Path through links: every item from the file up to the allowed folder is checked; none may be a link or junction.
		cmdline = "$root=" + t.lit(root) + ";$r=(Resolve-Path -LiteralPath " + t.lit(p) + ").ProviderPath;$c=Get-Item -LiteralPath $r -Force;" +
			"while($c -and ($c.FullName.TrimEnd([char]92) -ne $root.TrimEnd([char]92))){if($c.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 1};if($c.PSIsContainer){$c=$c.Parent}else{$c=$c.Directory}};" +
			"if(-not $c){throw 2};$r"
	} else {
		cmdline = "realpath -e -- " + t.lit(p)
	}
	s, err := t.text(ctx, t.m.timeout(), cmdline, false)
	if err != nil {
		return "", err
	}
	real := strings.TrimSpace(s)
	if real == "" || strings.ContainsAny(real, "\r\n") {
		return "", transport.ErrNotAllowed
	}
	if err := t.plain(real); err != nil {
		return "", err
	}
	return real, nil
}

// Probe tests that the machine answers and reports what it is.
func (t *Transport) Probe(ctx context.Context) (transport.HostInfo, error) {
	if err := t.m.Ping(ctx, t.id); err != nil {
		return transport.HostInfo{}, err
	}
	idn, err := t.m.Identify(ctx, t.id)
	if err != nil {
		return transport.HostInfo{}, err
	}
	info := transport.HostInfo{OS: t.os, Description: idn.Description}
	clock := "date +%s"
	if t.win() {
		clock = "([DateTimeOffset]::UtcNow).ToUnixTimeSeconds()"
	}
	before := time.Now()
	if s, err := t.text(ctx, t.m.timeout(), clock, false); err == nil {
		if sec, perr := strconv.ParseInt(strings.TrimSpace(s), 10, 64); perr == nil {
			mid := before.Add(time.Since(before) / 2)
			info.ClockOffset = time.Unix(sec, 0).Sub(mid).Round(time.Second)
		}
	}
	return info, nil
}

// List lists the files directly inside dir (not subfolders).
func (t *Transport) List(ctx context.Context, dir string) ([]transport.FileInfo, error) {
	real, err := t.resolve(ctx, dir)
	if err != nil {
		return nil, err
	}
	var cmdline string
	if t.win() {
		cmdline = "Get-ChildItem -LiteralPath " + t.lit(real) + " -File | ForEach-Object {\"{0}|{1}|{2}\" -f $_.Length,([DateTimeOffset]$_.LastWriteTimeUtc).ToUnixTimeSeconds(),$_.Name}"
	} else {
		cmdline = "ls -l --time-style=+%s -- " + t.lit(real)
	}
	s, err := t.text(ctx, t.m.timeout(), cmdline, false)
	if err != nil {
		return nil, err
	}
	if len(s) > maxListBytes {
		return nil, ErrTooLarge
	}
	var out []transport.FileInfo
	for _, line := range strings.Split(s, "\n") {
		fi, ok := t.parseListLine(line)
		if !ok {
			continue
		}
		out = append(out, fi)
		if len(out) > maxListFiles {
			return nil, ErrTooLarge
		}
	}
	return out, nil
}

func (t *Transport) parseListLine(line string) (transport.FileInfo, bool) {
	var size, secs, name string
	if t.win() {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			return transport.FileInfo{}, false
		}
		size, secs, name = parts[0], parts[1], parts[2]
	} else {
		// -rw-r--r-- 1 owner group SIZE EPOCH name with spaces
		if !strings.HasPrefix(line, "-") {
			return transport.FileInfo{}, false // a folder, a link or the "total" line
		}
		rest := line
		var f []string
		for len(f) < 6 {
			rest = strings.TrimLeft(rest, " ")
			i := strings.IndexByte(rest, ' ')
			if i < 0 {
				return transport.FileInfo{}, false
			}
			f = append(f, rest[:i])
			rest = rest[i:]
		}
		size, secs, name = f[4], f[5], strings.TrimPrefix(rest, " ")
	}
	if name == "" || strings.ContainsAny(name, "\x00/\\") || len(name) > 255 {
		return transport.FileInfo{}, false
	}
	n, e1 := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	sec, e2 := strconv.ParseInt(strings.TrimSpace(secs), 10, 64)
	if e1 != nil || e2 != nil || n < 0 {
		return transport.FileInfo{}, false
	}
	return transport.FileInfo{Name: name, Size: n, MTime: time.Unix(sec, 0).UTC()}, true
}

func (t *Transport) size(ctx context.Context, real string) (int64, error) {
	var cmdline string
	if t.win() {
		cmdline = "(Get-Item -LiteralPath " + t.lit(real) + ").Length"
	} else {
		cmdline = "stat -c %s -- " + t.lit(real)
	}
	s, err := t.text(ctx, t.m.timeout(), cmdline, false)
	if err != nil {
		return 0, err
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if perr != nil || n < 0 {
		return 0, ErrBadReply
	}
	return n, nil
}

// Digest returns the SHA-256 the machine computes for the whole file.
func (t *Transport) Digest(ctx context.Context, path string) (string, error) {
	real, err := t.resolve(ctx, path)
	if err != nil {
		return "", err
	}
	var cmdline string
	if t.win() {
		cmdline = "(Get-FileHash -Algorithm SHA256 -LiteralPath " + t.lit(real) + ").Hash.ToLower()"
	} else {
		// dd with nofollow refuses a symlink swapped in after the path was resolved. A failed read hashes nothing, which never matches
		// the copy, so the pull discards it.
		cmdline = "dd if=" + t.lit(real) + " iflag=nofollow status=none | sha256sum"
	}
	s, err := t.text(ctx, t.m.hashTimeout(), cmdline, false)
	if err != nil {
		return "", err
	}
	f := strings.Fields(s)
	if len(f) == 0 || len(f[0]) != 64 {
		return "", ErrBadReply
	}
	h := strings.ToLower(f[0])
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", ErrBadReply
		}
	}
	return h, nil
}

var reB64 = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

// readChunk reads exactly n bytes at off (base64 on the wire).
func (t *Transport) readChunk(ctx context.Context, real string, off int64, n int) ([]byte, error) {
	var cmdline string
	if t.win() {
		cmdline = "$f=[IO.File]::Open(" + t.lit(real) + ",3,1,3);try{[void]$f.Seek(" + strconv.FormatInt(off, 10) + ",0);$b=New-Object byte[] " + strconv.Itoa(n) +
			";$n=$f.Read($b,0,$b.Length);[Convert]::ToBase64String($b,0,$n)}finally{$f.Dispose()}"
	} else {
		// nofollow: a file swapped for a symlink after the path was resolved is refused, not followed.
		cmdline = "dd if=" + t.lit(real) + " iflag=nofollow,skip_bytes,count_bytes skip=" + strconv.FormatInt(off, 10) + " count=" + strconv.Itoa(n) + " status=none | base64 -w0"
	}
	s, err := t.text(ctx, t.m.timeout(), cmdline, true)
	if err != nil {
		return nil, err
	}
	s = strings.TrimSpace(s)
	if !reB64.MatchString(s) {
		return nil, ErrShortChunk // an error message, not data
	}
	b, derr := base64.StdEncoding.DecodeString(s)
	if derr != nil || len(b) != n {
		return nil, ErrShortChunk
	}
	return b, nil
}

// Open streams the file from byte offset off, one chunk at a time. The size is read first, so a chunk that comes back short is an error and
// never looks like the end of the file. A file that grows while it is read is read up to the size it had when the read started.
func (t *Transport) Open(ctx context.Context, path string, off int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, transport.ErrNotAllowed
	}
	real, err := t.resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	size, err := t.size(ctx, real)
	if err != nil {
		return nil, err
	}
	if off > size {
		return nil, transport.ErrNotAllowed
	}
	return &chunkReader{t: t, ctx: ctx, real: real, pos: off, size: size, step: t.m.chunk()}, nil
}

type chunkReader struct {
	t      *Transport
	ctx    context.Context
	real   string
	pos    int64
	size   int64
	step   int
	buf    []byte
	err    error
	closed bool
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		if r.closed {
			return 0, io.ErrClosedPipe
		}
		if r.err != nil {
			return 0, r.err
		}
		if r.pos >= r.size {
			return 0, io.EOF
		}
		if err := r.ctx.Err(); err != nil {
			r.err = err
			continue
		}
		n := int64(r.step)
		if left := r.size - r.pos; left < n {
			n = left
		}
		b, err := r.t.readChunk(r.ctx, r.real, r.pos, int(n))
		if err != nil {
			r.err = err
			continue
		}
		r.buf = b
		r.pos += int64(len(b))
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *chunkReader) Close() error { r.closed = true; return nil }
