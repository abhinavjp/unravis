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
type DirAccess struct{ Dir string }

// ReadFile reads one file of the folder, at most max bytes.
func (d DirAccess) ReadFile(name string) ([]byte, error) {
	f, err := os.Open(filepath.Join(d.Dir, name))
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

// Open opens one file of the folder.
func (d DirAccess) Open(name string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(d.Dir, name))
}

// FolderSource reads a recordings folder or a share.
type FolderSource struct {
	Loc    Location
	Access FileAccess
}

// NewFolderSource reads loc through the file system.
func NewFolderSource(loc Location) *FolderSource {
	return &FolderSource{Loc: loc, Access: DirAccess{Dir: loc.Path}}
}

func (s *FolderSource) place() string {
	if s.Loc.Kind == LocShare {
		return "share"
	}
	return "folder"
}

// classifyFile turns a file system error into a read error. A name that is not
// there is Not found; every other error (a share that dropped, access refused,
// a network path that went away) is Unreachable.
func (s *FolderSource) classifyFile(err error, what string) *ReadError {
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
	b, err := readFileCtx(ctx, s.Access, ManifestName)
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

// OpenFile opens a recording file. Only names the recorder writes are opened.
func (s *FolderSource) OpenFile(ctx context.Context, name string) (io.ReadCloser, error) {
	if !ValidFileName(name) {
		return nil, readErr(ReadBadData, "That is not a recording file name.", nil)
	}
	f, err := openFileCtx(ctx, s.Access, name)
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
// or dropped share can hold a file system call for a long time; the call is left to
// finish on its own goroutine and its answer is thrown away.
func readFileCtx(ctx context.Context, acc FileAccess, name string) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
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

// openFileCtx opens one file through acc, stopping the wait when ctx ends (a file
// that opens after that is closed again), and wraps the stream so that each read
// also stops waiting when ctx ends.
func openFileCtx(ctx context.Context, acc FileAccess, name string) (io.ReadCloser, error) {
	type res struct {
		rc  io.ReadCloser
		err error
	}
	ch := make(chan res, 1)
	go func() {
		rc, err := acc.Open(name)
		ch <- res{rc, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return &ctxReadCloser{ctx: ctx, rc: r.rc}, nil
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.rc != nil {
				r.rc.Close()
			}
		}()
		return nil, errReadCancelled
	}
}

// ctxReadCloser reads from rc on a helper goroutine so that a read that never
// comes back (a share that dropped mid-file) ends with the context. After a
// cancelled read the stream is closed and not used again.
type ctxReadCloser struct {
	ctx    context.Context
	rc     io.ReadCloser
	failed error
}

func (c *ctxReadCloser) Read(p []byte) (int, error) {
	if c.failed != nil {
		return 0, c.failed
	}
	type res struct {
		buf []byte
		err error
	}
	ch := make(chan res, 1)
	buf := make([]byte, len(p)) // the helper owns buf; p is touched only here
	go func() {
		n, err := c.rc.Read(buf)
		ch <- res{buf[:n], err}
	}()
	select {
	case r := <-ch:
		return copy(p, r.buf), r.err
	case <-c.ctx.Done():
		c.failed = errReadCancelled
		go c.rc.Close()
		return 0, errReadCancelled
	}
}

func (c *ctxReadCloser) Close() error {
	if c.failed != nil {
		return nil // already being closed
	}
	return c.rc.Close()
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
}

// Incomplete is true when part of what the manifest holds was not read.
func (r ReadReport) Incomplete() bool { return len(r.Dropped) > 0 || len(r.Missing) > 0 }

// ReadSamples reads the samples between from and to, in time order. It opens only
// the hourly files that can hold them. A file that fails part way keeps the
// samples read before the failure and is listed in Dropped; a later file is still
// tried, so one bad hour does not hide the rest. Samples are never invented: an
// hour that was not read is a gap in the result, and the gap rule decides what it
// means.
func ReadSamples(ctx context.Context, src Source, m Manifest, from, to time.Time) ([]Sample, ReadReport, error) {
	var rep ReadReport
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
		names = names[len(names)-MaxReadFiles:]
	}
	for _, f := range names {
		if err := ctx.Err(); err != nil {
			return out, rep, readErr(ReadUnreachable, "The read was cancelled.", err)
		}
		samples, torn, newer, err := readFile(ctx, src, f.Name, from, to)
		rep.Files++
		out = append(out, samples...)
		rep.Newer = rep.Newer || newer
		if torn {
			rep.Torn = append(rep.Torn, f.Name)
		}
		if err != nil {
			if KindOf(err) == ReadNotFound {
				rep.Missing = append(rep.Missing, f.Name)
			} else {
				rep.Dropped = append(rep.Dropped, f.Name)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	out = dedupeSamples(out)
	rep.Samples = len(out)
	return out, rep, nil
}

func dedupeSamples(in []Sample) []Sample {
	out := in[:0]
	for i, s := range in {
		if i > 0 && s.T.Equal(in[i-1].T) && s.Run == in[i-1].Run {
			continue
		}
		out = append(out, s)
	}
	return out
}

// readFile reads one hourly file. torn is true when the last line was not whole.
// On a failure after some lines were read the samples so far are returned with
// the error.
func readFile(ctx context.Context, src Source, name string, from, to time.Time) (samples []Sample, torn, newer bool, err error) {
	rc, err := src.OpenFile(ctx, name)
	if err != nil {
		return nil, false, false, err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(rc, 64<<10)
	lastBad := false
	for {
		line, rerr := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 {
			var s Sample
			lastBad = false
			if jerr := json.Unmarshal(line, &s); jerr != nil {
				// A line that does not parse is the cut end of a file being written
				// or killed (torn when it is the last one); a bad line in the middle
				// is skipped the same way.
				lastBad = true
			} else if s.V > FormatVersion {
				newer = true
			} else if !s.T.Before(from) && s.T.Before(to) {
				s.T = s.T.UTC()
				samples = append(samples, s)
			}
		}
		if rerr == io.EOF {
			return samples, lastBad, newer, nil
		}
		if rerr != nil {
			var re *ReadError
			if errors.As(rerr, &re) {
				return samples, torn, newer, rerr
			}
			return samples, torn, newer, readErr(ReadUnreachable, "The connection dropped while reading "+name+".", rerr)
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
