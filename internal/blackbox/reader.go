package blackbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ReadKind says why a read of a Blackbox failed. The words the person sees are in
// ReadError.Message; the kind is what the state and the page branch on.
type ReadKind string

// Read failures.
const (
	// ReadNotFound: the place is reachable but no Blackbox is there (no folder, no
	// manifest, an address that answers but is not a Blackbox).
	ReadNotFound ReadKind = "not-found"
	// ReadUnreachable: the place did not answer (a share that dropped, a server
	// that is off, a port nothing listens on).
	ReadUnreachable ReadKind = "unreachable"
	// ReadKeyRefused: the read key is missing or wrong.
	ReadKeyRefused ReadKind = "key-refused"
	// ReadThrottled: too many wrong keys from this PC; wait a minute.
	ReadThrottled ReadKind = "throttled"
	// ReadBusy: the recorder is serving as many readers as it allows.
	ReadBusy ReadKind = "busy"
	// ReadNewerFormat: the recorder is newer than this Unravis.
	ReadNewerFormat ReadKind = "newer-format"
	// ReadBadData: the place answered with something that is not Blackbox data.
	ReadBadData ReadKind = "bad-data"
)

// ReadError is a read that failed. The message never holds the read key.
type ReadError struct {
	Kind    ReadKind
	Message string
	Cause   error
}

func (e *ReadError) Error() string { return e.Message }
func (e *ReadError) Unwrap() error { return e.Cause }

func readErr(kind ReadKind, msg string, cause error) *ReadError {
	return &ReadError{Kind: kind, Message: msg, Cause: cause}
}

// KindOf returns the kind of a read error, or ReadUnreachable for an error that
// is not one (so a surprise is never shown as "fine").
func KindOf(err error) ReadKind {
	var re *ReadError
	if errors.As(err, &re) {
		return re.Kind
	}
	return ReadUnreachable
}

// Limits on what a read takes in.
const (
	maxManifestBytes = 8 << 20
	maxStatusBytes   = 1 << 20
	maxLineBytes     = 4 << 20
	// MaxReadFiles bounds how many hourly files one read opens (7 days is 168).
	MaxReadFiles = 24 * 8
	// RequestTimeout bounds one request to a recorder.
	RequestTimeout = 15 * time.Second
)

// Cumulative read budgets (W12-04). The per-line cap alone does not bound a
// read: one validly named hourly file can hold many individually legal lines.
// A read stops as soon as it has taken in one byte (or decoded one sample)
// past the budget, keeps what it has, and reports Limited — an incomplete read,
// never a clean EOF. Variables so a test can tighten them.
var (
	// maxReadTotalBytes bounds the input bytes of ONE ReadSamples call.
	maxReadTotalBytes int64 = 128 << 20
	// maxReadTotalSamples bounds the decoded sample objects of ONE ReadSamples
	// call (every decoded line counts, even one outside the window: decoding is
	// the cost being bounded).
	maxReadTotalSamples = 100000
)

// Read admission (W12-04). A folder read runs its file-system calls on helper
// goroutines because a dead share cannot be cancelled; without a bound, repeated
// permitted reads of a dead share pile up outstanding OS calls. Two bounded
// pools cap that: one read slot per concurrent ReadSamples (global, across all
// sources), and a bounded number of outstanding file-system calls — globally
// and per source — INCLUDING calls whose caller already gave up. A helper holds
// its slot until the OS call returns, so the number of blocked helpers can never
// exceed the pool. Acquisition honours ctx, so a caller is never held past its
// deadline waiting for a slot.
const (
	maxConcurrentReads   = 8
	maxOutstandingOS     = 16
	maxSourceOutstanding = 4
)

var (
	readAdmit = make(chan struct{}, maxConcurrentReads)
	osAdmit   = make(chan struct{}, maxOutstandingOS)
)

// acquireSlot takes one slot of ch, or reports false when ctx ends first.
func acquireSlot(ctx context.Context, ch chan struct{}) (release func(), ok bool) {
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	case <-ctx.Done():
		return nil, false
	}
}

// acquireOS takes a global outstanding-call slot and then a per-source one
// (local may be nil). Both are released by the returned func, which the helper
// goroutine calls when the OS call finishes — even after the caller gave up.
func acquireOS(ctx context.Context, local chan struct{}) (release func(), ok bool) {
	relG, ok := acquireSlot(ctx, osAdmit)
	if !ok {
		return nil, false
	}
	if local == nil {
		return relG, true
	}
	relL, ok := acquireSlot(ctx, local)
	if !ok {
		relG()
		return nil, false
	}
	return func() { relL(); relG() }, true
}

// errReadLimited is the internal sentinel of a read that reached a cumulative
// budget. It never leaves the package: the read reports Limited instead.
var errReadLimited = errors.New("the read reached its byte or sample budget")

// readBudget is what is left of one read's cumulative budgets.
type readBudget struct {
	bytesLeft   int64
	samplesLeft int
}

// budgetReader counts input bytes against the budget and stops one byte past
// it (the +1 proves the overflow without reading the rest of the file).
type budgetReader struct {
	r   io.Reader
	bud *readBudget
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.bud.bytesLeft < 0 {
		return 0, errReadLimited
	}
	if int64(len(p)) > b.bud.bytesLeft+1 {
		p = p[:int(b.bud.bytesLeft+1)]
	}
	n, err := b.r.Read(p)
	b.bud.bytesLeft -= int64(n)
	if b.bud.bytesLeft < 0 {
		return n, errReadLimited
	}
	return n, err
}

// ManifestRead is the manifest as read, with the recorder's clock when it said.
type ManifestRead struct {
	Manifest   Manifest
	ServerTime time.Time // zero for a folder: no second clock to compare
}

// Source is read-only access to one Blackbox, whichever way it is reached.
// Nothing on it writes, deletes or renames.
type Source interface {
	Manifest(ctx context.Context) (ManifestRead, error)
	// Status is the recorder's own status. Only a URL has one; a folder answers
	// (nil, nil).
	Status(ctx context.Context) (*StatusDoc, error)
	// OpenFile opens one hourly recording file by its name.
	OpenFile(ctx context.Context, name string) (io.ReadCloser, error)
}

/* ------------------------------------------------------ folder or share */

// FileAccess is the file system a folder or share is read through. The real one
// is DirAccess; tests give one that fails the way a dropped share does.
type FileAccess interface {
	ReadFile(name string) ([]byte, error)
	Open(name string) (io.ReadCloser, error)
}

// DirAccess reads files of a directory (a local folder or a UNC path).
//
// Containment (W12-05): a valid basename is not a containment proof — the entry
// itself, or any ancestor, can be a symlink, a junction or another reparse
// alias. Opens therefore validate every ancestor and the final object with
// Lstat (which does not follow the final link), refuse anything that is not a
// plain directory/regular file (on Windows a junction Lstats as ModeIrregular,
// a symlink as ModeSymlink — both are refused), and then verify the OPEN HANDLE
// against a fresh Lstat of the path (os.SameFile): a handle opened through a
// link points at the target, whose identity differs from the link's own, so the
// mismatch is caught even if the entry was swapped after the first check.
// Ancestors are re-checked after the open the same way. This is race-resistant,
// not race-free: without openat-style relative opens a swap between the last
// check and the open cannot be excluded on every platform; the handle identity
// check closes the final-component hole, and the ancestor re-check narrows the
// rest.
//
// Trusted is the explicit trusted-root policy: when the owner vouches for the
// whole root, links and reparse points under it are allowed (the legacy
// behaviour). It is off by default; nothing in the app sets it silently.
type DirAccess struct {
	Dir     string
	Trusted bool
}

// errLinkRefused is the internal sentinel of a refused link/reparse/non-regular
// object. It carries no path; the public message is fixed.
var errLinkRefused = errors.New("the entry is not a plain file inside the recordings folder")

// ReadFile reads one file of the folder, at most max bytes.
func (d DirAccess) ReadFile(name string) ([]byte, error) {
	f, err := d.openContained(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxManifestBytes {
		return nil, errors.New("file too large")
	}
	return b, nil
}

// Open opens one file of the folder under the containment policy.
func (d DirAccess) Open(name string) (io.ReadCloser, error) {
	return d.openContained(name)
}

// openContained opens dir/name as a plain regular file without following links.
func (d DirAccess) openContained(name string) (io.ReadCloser, error) {
	// One path element only: no separators, no traversal, no alternate syntax.
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return nil, errLinkRefused
	}
	if d.Trusted {
		return os.Open(filepath.Join(d.Dir, name))
	}
	dir := filepath.Clean(d.Dir)
	// Validate every ancestor: a plain directory, no link or reparse alias.
	anc, err := statAncestors(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name)
	li, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !li.Mode().IsRegular() {
		return nil, errLinkRefused // a link, junction, pipe, device or directory
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// The handle must BE the directory entry: a link opened by os.Open yields
	// the target's identity, which differs from the entry's own Lstat identity.
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	li2, err := os.Lstat(path)
	if err != nil || !os.SameFile(fi, li2) || !li2.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, errLinkRefused
	}
	// An ancestor swapped while the open ran is caught by re-checking identities.
	for _, a := range anc {
		st, serr := os.Lstat(a.path)
		if serr != nil || !os.SameFile(st, a.info) || !st.IsDir() || st.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			f.Close()
			if serr != nil {
				return nil, serr
			}
			return nil, errLinkRefused
		}
	}
	return f, nil
}

type ancestorStat struct {
	path string
	info fs.FileInfo
}

// statAncestors Lstats every ancestor of dir, deepest last. A volume or share
// root must be a plain directory too; the UNC server namespace root (\\server\)
// is not a file-system object and is skipped. A junction or symlink anywhere in
// the chain refuses the whole open.
func statAncestors(dir string) ([]ancestorStat, error) {
	var chain []string
	p := dir
	for {
		chain = append(chain, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	var out []ancestorStat
	for i := len(chain) - 1; i >= 0; i-- {
		a := chain[i]
		if isUNCServerRoot(a) {
			continue
		}
		st, err := os.Lstat(a)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() || st.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return nil, errLinkRefused
		}
		out = append(out, ancestorStat{path: a, info: st})
	}
	return out, nil
}

// isUNCServerRoot reports whether p is the \\server\ namespace root (which has
// no Lstat-able identity of its own).
func isUNCServerRoot(p string) bool {
	if !strings.HasPrefix(p, `\\`) {
		return false
	}
	rest := strings.TrimPrefix(p, `\\`)
	rest = strings.TrimSuffix(rest, `\`)
	return rest != "" && !strings.Contains(rest, `\`)
}

// FolderSource reads a recordings folder or a share.
type FolderSource struct {
	Loc    Location
	Access FileAccess

	// osSlots bounds this source's outstanding file-system calls (W12-04). It
	// is made on first use so a zero-value FolderSource works; a FolderSource
	// must not be copied after first use.
	mu      sync.Mutex
	osSlots chan struct{}
}

// NewFolderSource reads loc through the file system.
func NewFolderSource(loc Location) *FolderSource {
	return &FolderSource{Loc: loc, Access: DirAccess{Dir: loc.Path}}
}

// sourceSlots is this source's bounded pool of outstanding OS calls.
func (s *FolderSource) sourceSlots() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.osSlots == nil {
		s.osSlots = make(chan struct{}, maxSourceOutstanding)
	}
	return s.osSlots
}

func (s *FolderSource) place() string {
	if s.Loc.Kind == LocShare {
		return "share"
	}
	return "folder"
}

// classifyFile turns a file system error into a read error. A name that is not
// there is Not found; a refused link or non-regular object is Bad data (a fixed
// sentence, never the path); every other error (a share that dropped, access
// refused, a network path that went away) is Unreachable.
func (s *FolderSource) classifyFile(err error, what string) *ReadError {
	if errors.Is(err, errLinkRefused) {
		return readErr(ReadBadData, "That "+s.place()+" holds an entry that is not a plain recording file, so it was not opened.", err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return readErr(ReadNotFound, "No Blackbox is in that "+s.place()+": "+what+" is not there.", err)
	}
	if errors.Is(err, fs.ErrPermission) {
		return readErr(ReadUnreachable, "This account may not read that "+s.place()+". Ask for read access to it.", err)
	}
	return readErr(ReadUnreachable, "The "+s.place()+" did not answer. Check the connection and try again.", err)
}

// Manifest reads manifest.json.
func (s *FolderSource) Manifest(ctx context.Context) (ManifestRead, error) {
	if err := ctx.Err(); err != nil {
		return ManifestRead{}, readErr(ReadUnreachable, "The read was cancelled.", err)
	}
	b, err := readFileCtx(ctx, s.Access, ManifestName, s.sourceSlots())
	if err != nil {
		return ManifestRead{}, s.classifyFile(err, ManifestName)
	}
	m, rerr := parseManifest(b)
	if rerr != nil {
		return ManifestRead{}, rerr
	}
	return ManifestRead{Manifest: m}, nil
}

// Status: a folder has no status page.
func (s *FolderSource) Status(ctx context.Context) (*StatusDoc, error) { return nil, nil }

// OpenFile opens a recording file. Only names the recorder writes are opened,
// and only as plain files inside the folder (no links, no reparse aliases).
func (s *FolderSource) OpenFile(ctx context.Context, name string) (io.ReadCloser, error) {
	if !ValidFileName(name) {
		return nil, readErr(ReadBadData, "That is not a recording file name.", nil)
	}
	f, err := openFileCtx(ctx, s.Access, name, s.sourceSlots())
	if err != nil {
		return nil, s.classifyFile(err, name)
	}
	return f, nil
}

// errReadCancelled is what a folder or share read returns when its time ran out
// while the file system had not answered. It is not "not found": a dead share is
// Unreachable.
var errReadCancelled = errors.New("the read ran out of time before the share answered")

// readFileCtx reads one file through acc but stops waiting when ctx ends. A dead
// or dropped share can hold a file system call for a long time; the call is left
// to finish on its own goroutine and its answer is thrown away. The helper holds
// an admission slot until the OS call returns, so abandoned calls stay bounded
// (W12-04); when no slot is free the read ends with the context instead of
// spawning another helper.
func readFileCtx(ctx context.Context, acc FileAccess, name string, local chan struct{}) ([]byte, error) {
	release, ok := acquireOS(ctx, local)
	if !ok {
		return nil, errReadCancelled
	}
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		defer release()
		b, err := acc.ReadFile(name)
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-ctx.Done():
		return nil, errReadCancelled
	}
}

// openFileCtx opens one file through acc, stopping the wait when ctx ends. A
// file that opens after the caller gave up is closed by the helper itself (no
// second goroutine), and the helper's admission slot is released only when the
// OS call returns. The handoff is mutex-guarded so a late handle is always
// closed exactly once, whichever side wins the race. The returned stream wraps
// each read the same way.
func openFileCtx(ctx context.Context, acc FileAccess, name string, local chan struct{}) (io.ReadCloser, error) {
	release, ok := acquireOS(ctx, local)
	if !ok {
		return nil, errReadCancelled
	}
	type res struct {
		rc  io.ReadCloser
		err error
	}
	ch := make(chan res, 1)
	var mu sync.Mutex
	abandoned := false
	go func() {
		defer release()
		rc, err := acc.Open(name)
		mu.Lock()
		if abandoned && err == nil {
			rc.Close() // the caller is gone; never hand out a late handle
			rc = nil
		}
		ch <- res{rc, err} // buffered: never blocks under the mutex
		mu.Unlock()
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		if r.rc == nil {
			return nil, errReadCancelled
		}
		return &ctxReadCloser{ctx: ctx, rc: r.rc, local: local}, nil
	case <-ctx.Done():
		mu.Lock()
		abandoned = true
		select {
		case r := <-ch: // the helper finished first: close its handle here
			if r.rc != nil {
				r.rc.Close()
			}
		default: // the helper will see abandoned and close the late handle
		}
		mu.Unlock()
		return nil, errReadCancelled
	}
}

// ctxReadCloser reads from rc on a helper goroutine so that a read that never
// comes back (a share that dropped mid-file) ends with the context. Each helper
// holds an admission slot until its OS call returns. After a cancelled read the
// stream is closed exactly once — by the helper when its blocked call returns,
// or by the cancel path when the call had already finished — and not used again.
type ctxReadCloser struct {
	ctx       context.Context
	rc        io.ReadCloser
	local     chan struct{}
	mu        sync.Mutex
	abandoned bool
	failed    error
	closeOnce sync.Once
}

func (c *ctxReadCloser) closeRC() { c.closeOnce.Do(func() { c.rc.Close() }) }

func (c *ctxReadCloser) Read(p []byte) (int, error) {
	if c.failed != nil {
		return 0, c.failed
	}
	release, ok := acquireOS(c.ctx, c.local)
	if !ok {
		// No helper was spawned, so no call is outstanding: the stream is still
		// closable by Close. Do not mark it failed.
		return 0, errReadCancelled
	}
	type res struct {
		buf []byte
		err error
	}
	ch := make(chan res, 1)
	buf := make([]byte, len(p)) // the helper owns buf; p is touched only here
	go func() {
		defer release()
		n, err := c.rc.Read(buf)
		c.mu.Lock()
		if c.abandoned {
			c.closeRC() // a late answer on a dead share: close once, here
		}
		ch <- res{buf[:n], err} // buffered: never blocks under the mutex
		c.mu.Unlock()
	}()
	select {
	case r := <-ch:
		return copy(p, r.buf), r.err
	case <-c.ctx.Done():
		c.mu.Lock()
		c.abandoned = true
		select {
		case <-ch: // the read had already finished: close here, it cannot block
			c.closeRC()
		default: // the helper closes when its blocked call returns
		}
		c.mu.Unlock()
		c.failed = errReadCancelled
		return 0, errReadCancelled
	}
}

func (c *ctxReadCloser) Close() error {
	if c.failed != nil {
		// The helper owns the close once its blocked call returns; closing here
		// could block the caller on the same dead share.
		return nil
	}
	c.closeRC()
	return nil
}

func parseManifest(b []byte) (Manifest, *ReadError) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, readErr(ReadBadData, "manifest.json could not be read. It may be half written; try again in a moment.", err)
	}
	if m.Format > FormatVersion {
		return Manifest{}, readErr(ReadNewerFormat, "This Blackbox was written by a newer version than this Unravis understands. Update Unravis to read it.", ErrNewerFormat)
	}
	if m.Format < 1 {
		return Manifest{}, readErr(ReadBadData, "That folder has a manifest.json that is not a Blackbox manifest.", nil)
	}
	return m, nil
}

/* ------------------------------------------------------------------ URL */

// HTTPSource reads a Blackbox through its status server with the read key in the
// X-Blackbox-Key header. The key is never put in the address, in a message or in
// a log line, and a redirect is never followed (a redirect could carry the key to
// another address).
type HTTPSource struct {
	Loc    Location
	Key    string
	Client *http.Client
}

// NewHTTPSource builds a source for a URL location. client may be nil.
func NewHTTPSource(loc Location, key string, client *http.Client) *HTTPSource {
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	// The key travels in a header, so a Blackbox read never goes through a proxy
	// taken from the environment: the proxy would see the key over plain http.
	switch t := c.Transport.(type) {
	case nil:
		if dt, ok := http.DefaultTransport.(*http.Transport); ok {
			nt := dt.Clone()
			nt.Proxy = nil
			c.Transport = nt
		}
	case *http.Transport:
		nt := t.Clone()
		nt.Proxy = nil
		c.Transport = nt
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.Timeout == 0 {
		c.Timeout = RequestTimeout
	}
	return &HTTPSource{Loc: loc, Key: key, Client: &c}
}

func (s *HTTPSource) get(ctx context.Context, path string, limit int64) ([]byte, http.Header, *ReadError) {
	rc, h, rerr := s.open(ctx, path)
	if rerr != nil {
		return nil, nil, rerr
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, nil, readErr(ReadUnreachable, "The connection dropped while reading. Try again.", err)
	}
	if int64(len(b)) > limit {
		return nil, nil, readErr(ReadBadData, "The address answered with more data than a Blackbox sends.", nil)
	}
	return b, h, nil
}

func (s *HTTPSource) open(ctx context.Context, path string) (io.ReadCloser, http.Header, *ReadError) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Loc.BaseURL()+path, nil)
	if err != nil {
		return nil, nil, readErr(ReadBadData, "That address cannot be used.", nil)
	}
	req.Header.Set(KeyHeader, s.Key)
	req.Header.Set("Accept", "application/json, application/x-ndjson")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, nil, s.netErr(err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return resp.Body, resp.Header, nil
	case resp.StatusCode == http.StatusUnauthorized:
		resp.Body.Close()
		return nil, nil, readErr(ReadKeyRefused, "The read key was refused. Check the key you typed against the one printed when the Blackbox was installed.", nil)
	case resp.StatusCode == http.StatusTooManyRequests:
		resp.Body.Close()
		return nil, nil, readErr(ReadThrottled, "Too many wrong keys were sent from this PC. Wait a minute, then try again.", nil)
	case resp.StatusCode == http.StatusServiceUnavailable:
		resp.Body.Close()
		return nil, nil, readErr(ReadBusy, "The Blackbox is serving as many readers as it allows. Try again in a few seconds.", nil)
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, nil, readErr(ReadNotFound, "That address answered, but no Blackbox is there (or the file is gone).", nil)
	default:
		resp.Body.Close()
		return nil, nil, readErr(ReadNotFound, fmt.Sprintf("That address answered with %d, which a Blackbox does not send.", resp.StatusCode), nil)
	}
}

// netErr words a transport error without echoing the request (which holds the
// key in its header) or the URL.
func (s *HTTPSource) netErr(err error) *ReadError {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return readErr(ReadUnreachable, "The Blackbox did not answer in time. Check that the server is on and the port is open to this PC.", err)
	}
	return readErr(ReadUnreachable, "Nothing answered at that address. Check the name and port, that the Blackbox is running, and that its status page is open to this PC.", err)
}

func serverTime(h http.Header) time.Time {
	t, err := time.Parse(time.RFC3339Nano, h.Get(TimeHeader))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// Manifest reads /manifest.
func (s *HTTPSource) Manifest(ctx context.Context) (ManifestRead, error) {
	b, h, rerr := s.get(ctx, "/manifest", maxManifestBytes)
	if rerr != nil {
		return ManifestRead{}, rerr
	}
	var doc struct {
		Format   int             `json:"format"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Manifest) == 0 {
		return ManifestRead{}, readErr(ReadNotFound, "That address answered, but not like a Blackbox.", err)
	}
	if doc.Format > FormatVersion {
		return ManifestRead{}, readErr(ReadNewerFormat, "This Blackbox was written by a newer version than this Unravis understands. Update Unravis to read it.", ErrNewerFormat)
	}
	m, perr := parseManifest(doc.Manifest)
	if perr != nil {
		return ManifestRead{}, perr
	}
	return ManifestRead{Manifest: m, ServerTime: serverTime(h)}, nil
}

// Status reads /status.
func (s *HTTPSource) Status(ctx context.Context) (*StatusDoc, error) {
	b, h, rerr := s.get(ctx, "/status", maxStatusBytes)
	if rerr != nil {
		return nil, rerr
	}
	var d StatusDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, readErr(ReadNotFound, "That address answered, but not like a Blackbox.", err)
	}
	if d.Format > FormatVersion {
		return nil, readErr(ReadNewerFormat, "This Blackbox was written by a newer version than this Unravis understands. Update Unravis to read it.", ErrNewerFormat)
	}
	if t := serverTime(h); !t.IsZero() {
		d.Time = t
	}
	return &d, nil
}

// OpenFile opens /files/<name>.
func (s *HTTPSource) OpenFile(ctx context.Context, name string) (io.ReadCloser, error) {
	if !ValidFileName(name) {
		return nil, readErr(ReadBadData, "That is not a recording file name.", nil)
	}
	rc, _, rerr := s.open(ctx, "/files/"+name)
	if rerr != nil {
		return nil, rerr
	}
	return rc, nil
}

/* -------------------------------------------------------------- reading */

// Reading is one look at a Blackbox: the manifest, the status when there is one,
// and when it was looked at. It is the input of DeriveState.
type Reading struct {
	Manifest   Manifest
	Status     *StatusDoc
	ServerTime time.Time // the recorder's clock at the moment of the read, zero if unknown
	ReadAt     time.Time // this PC's clock at the moment of the read
}

// ProbeSource reads the manifest and, for a URL, the status. now is this PC's clock.
// On a folder the recorder's clock is not available, so the clock check does not
// apply (it says so rather than passing).
func ProbeSource(ctx context.Context, src Source, now func() time.Time) (Reading, error) {
	if now == nil {
		now = time.Now
	}
	before := now().UTC()
	mr, err := src.Manifest(ctx)
	if err != nil {
		return Reading{}, err
	}
	r := Reading{Manifest: mr.Manifest, ServerTime: mr.ServerTime, ReadAt: before}
	st, err := src.Status(ctx)
	if err != nil {
		return Reading{}, err
	}
	r.Status = st
	if r.ServerTime.IsZero() && st != nil {
		r.ServerTime = st.Time
	}
	// The clock is compared at the middle of the request, so the round trip does
	// not read as skew.
	after := now().UTC()
	r.ReadAt = before.Add(after.Sub(before) / 2)
	return r, nil
}

// ReadReport says what a read of samples got and what it could not.
type ReadReport struct {
	Files   int      // recording files opened
	Samples int      // samples kept
	Torn    []string // files whose last line was cut (a recorder writing, or killed)
	// Dropped names the files that could not be read at all, or stopped part way
	// (a share that dropped, a connection that was cut). The samples read before
	// the drop are kept.
	Dropped []string
	// Missing names files the manifest lists that were not there any more.
	Missing []string
	Newer   bool // a sample line said it was written by a newer version
	// Corrupt names files that held a line which did not parse in the MIDDLE of
	// the file (not just a cut tail). The lines around it are kept, but the file
	// cannot speak for the time the bad line stood for (W12-01).
	Corrupt []string
	// Capped is true when the manifest listed more overlapping hourly files than
	// MaxReadFiles and the oldest were left unread. OmittedFiles says how many
	// (W12-01: a cap is lost coverage, never a clean read).
	Capped       bool
	OmittedFiles int
	// Limited is true when a cumulative byte or decoded-sample budget stopped the
	// read before the window was covered. It is an incomplete read, not EOF
	// (W12-04).
	Limited bool
}

// Incomplete is true when part of what the manifest holds was not read at all:
// a file dropped or missing. It drives the page state (DeriveState), whose
// contract treats a torn tail of a file being written as normal, so Torn and
// Newer alone do not make a read Incomplete in that sense.
func (r ReadReport) Incomplete() bool { return len(r.Dropped) > 0 || len(r.Missing) > 0 }

// CoverageIncomplete is true when ANY reason keeps this read from speaking for
// the whole window: dropped or missing files, a newer format, a torn tail, a
// corrupt interior line, the file cap, or a byte/sample budget. A lane that
// draws buckets from a read must use this, not Incomplete: every one of these
// reasons leaves time unobserved, and unobserved time is never zero activity
// (W12-01).
func (r ReadReport) CoverageIncomplete() bool {
	return r.Incomplete() || r.Newer || len(r.Torn) > 0 || len(r.Corrupt) > 0 || r.Capped || r.Limited
}

// ReadSamples reads the samples between from and to, in time order. It opens only
// the hourly files that can hold them. A file that fails part way keeps the
// samples read before the failure and is listed in Dropped; a later file is still
// tried, so one bad hour does not hide the rest. Samples are never invented: an
// hour that was not read is a gap in the result, and the gap rule decides what it
// means.
//
// Bounds (W12-01/W12-04): at most MaxReadFiles of the newest overlapping files
// are opened and the omission is reported (Capped), the cumulative input bytes
// and decoded-sample count are budgeted (Limited), cancellation is checked in
// the decode loop, and each file's samples are merged into the running result
// as they are read instead of accumulating every file first. Every one of these
// limits makes CoverageIncomplete true: a limit is lost coverage, never EOF.
func ReadSamples(ctx context.Context, src Source, m Manifest, from, to time.Time) ([]Sample, ReadReport, error) {
	var rep ReadReport
	release, ok := acquireSlot(ctx, readAdmit)
	if !ok {
		return nil, rep, readErr(ReadBusy, "Too many Blackbox reads are running at once. Try again in a moment.", ctx.Err())
	}
	defer release()
	var out []Sample
	var names []FileEntry
	for _, f := range m.Files {
		end := f.Hour.Add(time.Hour)
		if !f.Hour.Before(to) || !end.After(from) {
			continue
		}
		names = append(names, f)
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Hour.Before(names[j].Hour) })
	if len(names) > MaxReadFiles {
		rep.Capped = true
		rep.OmittedFiles = len(names) - MaxReadFiles
		names = names[len(names)-MaxReadFiles:]
	}
	bud := &readBudget{bytesLeft: maxReadTotalBytes, samplesLeft: maxReadTotalSamples}
	for _, f := range names {
		if err := ctx.Err(); err != nil {
			return out, rep, readErr(ReadUnreachable, "The read was cancelled.", err)
		}
		samples, st, err := readFile(ctx, src, f.Name, from, to, bud)
		rep.Files++
		out = mergeSamples(out, samples)
		rep.Newer = rep.Newer || st.newer
		if st.torn {
			rep.Torn = append(rep.Torn, f.Name)
		}
		if st.corrupt {
			rep.Corrupt = append(rep.Corrupt, f.Name)
		}
		if st.limited {
			rep.Limited = true
		}
		if err != nil {
			if ctx.Err() != nil {
				return out, rep, readErr(ReadUnreachable, "The read was cancelled.", ctx.Err())
			}
			if KindOf(err) == ReadNotFound {
				rep.Missing = append(rep.Missing, f.Name)
			} else {
				rep.Dropped = append(rep.Dropped, f.Name)
			}
		}
		if rep.Limited {
			break // the budget is spent: later files would only be refused too
		}
	}
	rep.Samples = len(out)
	return out, rep, nil
}

// mergeSamples merges one file's sorted samples into the running sorted result,
// dropping duplicates (same time and run) as it goes, so neither duplicates nor
// whole files accumulate before the final order is known.
func mergeSamples(out, file []Sample) []Sample {
	if len(file) == 0 {
		return out
	}
	sort.SliceStable(file, func(i, j int) bool { return file[i].T.Before(file[j].T) })
	// Fast path (PERF-1): when the file starts strictly AFTER everything read
	// so far (or nothing was read yet), no interleaving is possible, so append
	// instead of copying the whole accumulated slice into a new merge buffer.
	// Duplicates are still dropped exactly as the merge below drops them:
	// same T AND same Run against the last kept sample, which covers both
	// within-file duplicates and (for an empty out) nothing else. A file whose
	// first T EQUALS out's last T does NOT take this path — the merge below
	// keeps out's payload first and drops the file's duplicate.
	if len(out) == 0 || out[len(out)-1].T.Before(file[0].T) {
		for _, s := range file {
			if n := len(out); n > 0 && s.T.Equal(out[n-1].T) && s.Run == out[n-1].Run {
				continue
			}
			out = append(out, s)
		}
		return out
	}
	merged := make([]Sample, 0, len(out)+len(file))
	i, j := 0, 0
	emit := func(s Sample) {
		if n := len(merged); n > 0 && s.T.Equal(merged[n-1].T) && s.Run == merged[n-1].Run {
			return
		}
		merged = append(merged, s)
	}
	for i < len(out) && j < len(file) {
		if file[j].T.Before(out[i].T) {
			emit(file[j])
			j++
		} else {
			emit(out[i])
			i++
		}
	}
	for ; i < len(out); i++ {
		emit(out[i])
	}
	for ; j < len(file); j++ {
		emit(file[j])
	}
	return merged
}

// fileReadStatus is what one hourly file's read proved about its own coverage.
type fileReadStatus struct {
	torn    bool // the last line was cut
	newer   bool // a line said it was written by a newer version
	corrupt bool // a line in the MIDDLE did not parse
	limited bool // a cumulative budget stopped this file
}

// readFile reads one hourly file under the read's cumulative budgets. On a
// failure after some lines were read the samples so far are returned with the
// error; a budget stop is not an error but a limited status.
func readFile(ctx context.Context, src Source, name string, from, to time.Time, bud *readBudget) (samples []Sample, st fileReadStatus, err error) {
	rc, err := src.OpenFile(ctx, name)
	if err != nil {
		return nil, st, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(&budgetReader{r: rc, bud: bud}, 64<<10)
	pendingBad := false
	lines := 0
	for {
		// Cancellation is checked in the decode loop, not only between files: a
		// large file must not hold a cancelled read (W12-04).
		if lines%32 == 1 {
			if cerr := ctx.Err(); cerr != nil {
				return samples, st, readErr(ReadUnreachable, "The read was cancelled.", cerr)
			}
		}
		lines++
		line, rerr := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 {
			var s Sample
			if jerr := json.Unmarshal(line, &s); jerr != nil {
				// A line that does not parse is the cut end of a file being
				// written or killed (torn when it is the last one). A bad line
				// with good lines after it is corruption in the middle: the
				// time it stood for is unobserved, so the file is Corrupt.
				if pendingBad {
					st.corrupt = true
				}
				pendingBad = true
			} else {
				if pendingBad {
					st.corrupt = true // the bad line was interior, not the tail
					pendingBad = false
				}
				// Every decoded object spends the sample budget, even one
				// outside the window or of a newer version: decoding is the
				// cost being bounded.
				bud.samplesLeft--
				if bud.samplesLeft < 0 {
					st.limited = true
					return samples, st, nil
				}
				if s.V > FormatVersion {
					st.newer = true
				} else if !s.T.Before(from) && s.T.Before(to) {
					s.T = s.T.UTC()
					samples = append(samples, s)
				}
			}
		}
		if rerr == io.EOF {
			st.torn = pendingBad
			return samples, st, nil
		}
		if rerr != nil {
			if errors.Is(rerr, errReadLimited) {
				st.limited = true
				st.torn = pendingBad
				return samples, st, nil
			}
			if cerr := ctx.Err(); cerr != nil {
				return samples, st, readErr(ReadUnreachable, "The read was cancelled.", cerr)
			}
			var re *ReadError
			if errors.As(rerr, &re) {
				return samples, st, rerr
			}
			return samples, st, readErr(ReadUnreachable, "The connection dropped while reading "+name+".", rerr)
		}
	}
}

// readLine reads one line of any length up to maxLineBytes.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		part, isPrefix, err := br.ReadLine()
		if err != nil {
			return buf, err
		}
		buf = append(buf, part...)
		if len(buf) > maxLineBytes {
			return nil, readErr(ReadBadData, "A recording line is larger than a Blackbox writes.", nil)
		}
		if !isPrefix {
			return buf, nil
		}
	}
}
