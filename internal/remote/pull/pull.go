package pull

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/remote/grammar"
	"api-audit-explorer/internal/remote/transport"
)

const (
	checkpointEvery = 4 << 20 // raw bytes between saved resume points
	diskMargin      = 256 << 20
	// RecordName is the provenance file of a job. It is not manifest.json, so a pulled recorder folder keeps the recorder's own manifest.
	RecordName      = "pull-record.json"
	statusDone      = "done"
	statusFailed    = "failed"
	statusInterrupt = "interrupted"
)

// Options configure one pull job.
type Options struct {
	StagingDir string // the job folder is StagingDir/JobID
	JobID      string // letters, digits, . _ - ; the same id resumes an earlier run
	Server     string // label written to the provenance record
	Limits     Limits
	// Free reports free bytes on the staging drive. Nil means the OS check; if the check cannot be made the job runs anyway.
	Free func(dir string) (int64, error)
	// Progress is called from several goroutines, one call at a time.
	Progress func(Progress)
}

// Progress is a snapshot for the screen or the CLI.
type Progress struct {
	JobID      string
	File       string
	FileBytes  int64 // raw bytes of File copied so far
	JobBytes   int64 // raw bytes of the whole job copied in this run
	FilesTotal int
	FilesDone  int
}

// FileResult is the outcome for one file.
type FileResult struct {
	Name         string `json:"name"`
	OutName      string `json:"outName"`
	RawBytes     int64  `json:"rawBytes"`
	OutBytes     int64  `json:"outBytes"`
	SHA256       string `json:"sha256"` // of the file as the server sent it
	ServerDigest string `json:"serverDigest,omitempty"`
	Verified     bool   `json:"verified"`
	Changed      bool   `json:"changedWhileCopying,omitempty"`
	Scrubbed     int    `json:"linesScrubbed"`
	Dropped      int    `json:"linesDropped"`
	Status       string `json:"status"` // done, failed, interrupted
	Note         string `json:"note,omitempty"`
}

// Result is the whole job. It is also written as pull-record.json next to the files, so a pulled recorder folder keeps its own manifest.json.
type Result struct {
	JobID       string       `json:"jobId"`
	Server      string       `json:"server"`
	Kind        string       `json:"kind"`
	Dir         string       `json:"serverFolder"`
	StagedAt    string       `json:"stagedAt"`
	Started     time.Time    `json:"started"`
	Finished    time.Time    `json:"finished"`
	Files       []FileResult `json:"files"`
	Skipped     []Skipped    `json:"skipped"`
	Interrupted bool         `json:"interrupted"`
	Bytes       int64        `json:"bytesCopied"`
}

// OK reports whether every planned file finished (done, with or without a changed-while-copying note).
func (r Result) OK() bool {
	if r.Interrupted {
		return false
	}
	for _, f := range r.Files {
		if f.Status != statusDone {
			return false
		}
	}
	return true
}

type fileState struct {
	Size     int64       `json:"size"`
	MTime    int64       `json:"mtime"`
	RawOff   int64       `json:"rawOff"`
	OutBytes int64       `json:"outBytes"`
	Hash     string      `json:"hash"` // base64 of the SHA-256 state at RawOff
	Scrubbed int         `json:"scrubbed"`
	Dropped  int         `json:"dropped"`
	Result   *FileResult `json:"result,omitempty"`
}

var errJobLimit = errors.New("the job reached its total size limit")

// Run copies the planned files into StagingDir/JobID. Raw server text never touches the disk: each line is scrubbed first.
func Run(ctx context.Context, t transport.Transport, req Request, plan Plan, opt Options) (Result, error) {
	lim := opt.Limits.withDefaults()
	if plan.Problem != "" {
		return Result{}, ErrPlanTooLarge{plan.Problem}
	}
	if r := grammar.SafeWord(opt.JobID); r != "" {
		return Result{}, errors.New("The job name is not valid.")
	}
	dir := filepath.Join(opt.StagingDir, opt.JobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, fmt.Errorf("Could not create the staging folder: %w", err)
	}
	if err := checkDisk(opt, dir, plan.Bytes); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, lim.MaxDuration)
	defer cancel()

	j := &job{t: t, req: req, opt: opt, lim: lim, dir: dir, th: newThrottle(lim.BytesPerSec), total: len(plan.Files)}
	res := Result{JobID: opt.JobID, Server: opt.Server, Kind: req.Kind, Dir: req.Dir, StagedAt: dir, Started: time.Now().UTC(), Skipped: plan.Skipped, Files: make([]FileResult, len(plan.Files))}

	used := map[string]bool{}
	outs := make([]string, len(plan.Files))
	for i, pf := range plan.Files {
		outs[i] = safeName(pf.Name, used)
	}
	sem := make(chan struct{}, lim.Parallel)
	var wg sync.WaitGroup
	for i := range plan.Files {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			res.Files[i] = j.copyFile(ctx, plan.Files[i], outs[i])
			j.fileDone(plan.Files[i].Name)
		}(i)
	}
	wg.Wait()
	for _, f := range res.Files {
		if f.Status == statusInterrupt {
			res.Interrupted = true
		}
	}
	res.Bytes = j.bytes()
	res.Finished = time.Now().UTC()
	if err := writeJSON(filepath.Join(dir, RecordName), res); err != nil {
		return res, fmt.Errorf("Could not write the provenance record: %w", err)
	}
	return res, nil
}

func checkDisk(opt Options, dir string, need int64) error {
	free := opt.Free
	if free == nil {
		free = func(d string) (int64, error) {
			di, err := blackbox.OSDisk{}.Free(d)
			return int64(di.Free), err
		}
	}
	f, err := free(dir)
	if err != nil {
		return nil // cannot tell: do not block
	}
	if f < need+diskMargin {
		return fmt.Errorf("There is not enough free disk space for this pull: it needs about %s and %s is free. Free some space or narrow the pull.", human(need+diskMargin), human(f))
	}
	return nil
}

type job struct {
	t     transport.Transport
	req   Request
	opt   Options
	lim   Limits
	dir   string
	th    *throttle
	total int

	mu        sync.Mutex
	jobBytes  int64
	filesDone int
}

func (j *job) bytes() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.jobBytes
}

func (j *job) add(n int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jobBytes += n
	if j.jobBytes > j.lim.PerJob {
		return errJobLimit
	}
	return nil
}

func (j *job) progress(file string, fileBytes int64) {
	if j.opt.Progress == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.opt.Progress(Progress{JobID: j.opt.JobID, File: file, FileBytes: fileBytes, JobBytes: j.jobBytes, FilesTotal: j.total, FilesDone: j.filesDone})
}

func (j *job) fileDone(name string) {
	j.mu.Lock()
	j.filesDone++
	j.mu.Unlock()
	j.progress(name, 0)
}

// serverPath joins the folder and a file name with the folder's own separator.
func serverPath(dir, name string) string {
	sep := "/"
	if len(dir) >= 2 && dir[1] == ':' {
		sep = `\`
	}
	return strings.TrimRight(dir, `\/`) + sep + name
}

func (j *job) copyFile(ctx context.Context, pf PlanFile, out string) FileResult {
	fr := FileResult{Name: pf.Name, OutName: out}
	fail := func(note string) FileResult {
		fr.Status, fr.Note = statusFailed, note
		return fr
	}
	statePath := filepath.Join(j.dir, out+".state")
	partPath := filepath.Join(j.dir, out+".part")
	finalPath := filepath.Join(j.dir, out)

	var st fileState
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Size != pf.Size || st.MTime != pf.MTime.Unix() { // new file, or the server file changed since: start over
		st = fileState{Size: pf.Size, MTime: pf.MTime.Unix()}
		_ = os.Remove(partPath)
	}
	if st.Result != nil && st.Result.Status == statusDone {
		if fi, err := os.Stat(finalPath); err == nil && fi.Size() == st.Result.OutBytes {
			return *st.Result
		}
		st = fileState{Size: pf.Size, MTime: pf.MTime.Unix()}
	}

	part, err := os.OpenFile(partPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fail("Could not create the staging file.")
	}
	defer part.Close()
	if fi, err := part.Stat(); err != nil || fi.Size() < st.OutBytes {
		st = fileState{Size: pf.Size, MTime: pf.MTime.Unix()} // the staged part is gone or short: restart
	}
	if err := part.Truncate(st.OutBytes); err != nil {
		return fail("Could not prepare the staging file.")
	}
	if _, err := part.Seek(st.OutBytes, io.SeekStart); err != nil {
		return fail("Could not prepare the staging file.")
	}
	h := sha256.New()
	if st.Hash != "" {
		raw, err := base64.StdEncoding.DecodeString(st.Hash)
		if err == nil {
			err = h.(encoding.BinaryUnmarshaler).UnmarshalBinary(raw)
		}
		if err != nil {
			st = fileState{Size: pf.Size, MTime: pf.MTime.Unix()}
			_ = part.Truncate(0)
			_, _ = part.Seek(0, io.SeekStart)
			h = sha256.New()
		}
	}

	rc, err := j.t.Open(ctx, serverPath(j.req.Dir, pf.Name), st.RawOff)
	if err != nil {
		return j.stopped(ctx, fr, "Could not open the file on the server: "+err.Error())
	}
	defer rc.Close()
	br := bufio.NewReaderSize(&meter{r: rc, ctx: ctx, j: j}, 64<<10)
	bw := bufio.NewWriterSize(part, 64<<10)

	off, outBytes := st.RawOff, st.OutBytes
	scrubbed, dropped := st.Scrubbed, st.Dropped
	sinceCP := int64(0)
	checkpoint := func() error {
		if err := bw.Flush(); err != nil {
			return err
		}
		if err := part.Sync(); err != nil {
			return err
		}
		state, err := h.(encoding.BinaryMarshaler).MarshalBinary()
		if err != nil {
			return err
		}
		st.RawOff, st.OutBytes, st.Scrubbed, st.Dropped = off, outBytes, scrubbed, dropped
		st.Hash = base64.StdEncoding.EncodeToString(state)
		sinceCP = 0
		j.progress(pf.Name, off)
		return writeJSON(statePath, st)
	}

	for {
		line, n, partial, rerr := readLine(br, h, j.lim.MaxLine)
		if errors.Is(rerr, errJobLimit) {
			return fail(fmt.Sprintf("The job reached its %s total limit. Narrow the time range or the pattern.", human(j.lim.PerJob)))
		}
		if rerr != nil && rerr != io.EOF {
			if partial == 0 { // stopped on a line boundary: keep everything so far
				_ = checkpoint()
			}
			return j.stopped(ctx, fr, "The connection stopped before the file finished. Run the pull again to continue.")
		}
		if n > 0 || line != nil {
			off += int64(n)
			sinceCP += int64(n)
			if off > j.lim.PerFile {
				_ = os.Remove(partPath)
				_ = os.Remove(statePath)
				return fail(fmt.Sprintf("The file grew past the %s limit while it was being copied.", human(j.lim.PerFile)))
			}
			switch {
			case line == nil: // a line longer than MaxLine, or the scrubber failed: dropped
				dropped++
			case bytes.IndexByte(line, 0) >= 0:
				_ = os.Remove(partPath)
				_ = os.Remove(statePath)
				return fail("This is not a plain text file (it may be UTF-16 or binary), so it was not copied.")
			default:
				s := strings.TrimRight(string(line), "\r\n")
				clean, changed, ok := scrubLine(j.req.Kind, s)
				if !ok {
					dropped++
					break
				}
				if changed {
					scrubbed++
				}
				m, werr := bw.WriteString(clean + "\n")
				outBytes += int64(m)
				if werr != nil {
					return fail("Could not write to the staging folder: " + werr.Error())
				}
			}
			if sinceCP >= checkpointEvery {
				if err := checkpoint(); err != nil {
					return fail("Could not save progress: " + err.Error())
				}
			}
		}
		if rerr == io.EOF {
			break
		}
	}
	if err := bw.Flush(); err != nil {
		return fail("Could not write to the staging folder: " + err.Error())
	}
	if err := part.Sync(); err != nil {
		return fail("Could not write to the staging folder: " + err.Error())
	}
	part.Close()

	fr.RawBytes, fr.OutBytes, fr.Scrubbed, fr.Dropped = off, outBytes, scrubbed, dropped
	fr.SHA256 = hex.EncodeToString(h.Sum(nil))
	if d, err := j.t.Digest(ctx, serverPath(j.req.Dir, pf.Name)); err != nil {
		if ctx.Err() != nil {
			return j.stopped(ctx, fr, "")
		}
		fr.Note = "The copy could not be checked against the server: " + err.Error()
	} else {
		fr.ServerDigest = d
		fr.Verified = strings.EqualFold(d, fr.SHA256)
	}
	if !fr.Verified {
		if j.stable(ctx, pf, off) {
			if fr.ServerDigest != "" {
				_ = os.Remove(partPath)
				_ = os.Remove(statePath)
				return fail("The copy does not match the file on the server, so it was thrown away. Try again.")
			}
		} else {
			fr.Changed = true
			fr.Note = "The file changed on the server while it was being copied, so this copy is a snapshot that could not be checked."
		}
	}
	_ = os.Remove(finalPath)
	if err := os.Rename(partPath, finalPath); err != nil {
		return fail("Could not finish the staged file: " + err.Error())
	}
	fr.Status = statusDone
	st.Result = &fr
	_ = writeJSON(statePath, st)
	return fr
}

// stable reports whether the server file still has the planned time and the size we read.
func (j *job) stable(ctx context.Context, pf PlanFile, read int64) bool {
	infos, err := j.t.List(ctx, j.req.Dir)
	if err != nil {
		return false
	}
	for _, fi := range infos {
		if fi.Name == pf.Name {
			return fi.Size == read && fi.MTime.Unix() == pf.MTime.Unix()
		}
	}
	return false
}

func (j *job) stopped(ctx context.Context, fr FileResult, note string) FileResult {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		fr.Status, fr.Note = statusInterrupt, "Stopped. Run the pull again to continue."
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fr.Status, fr.Note = statusInterrupt, "The job reached its time limit. Run the pull again to continue."
	default:
		fr.Status, fr.Note = statusInterrupt, note
	}
	return fr
}

// readLine reads one line, hashing every raw byte it consumes. n is the raw bytes consumed. line is nil when the line was dropped (longer than max).
// partial is the number of bytes of an unfinished line when an error cut it short. io.EOF comes with the last line, if any.
func readLine(br *bufio.Reader, h io.Writer, max int) (line []byte, n int, partial int, err error) {
	var buf []byte
	tooLong := false
	for {
		chunk, e := br.ReadSlice('\n')
		if len(chunk) > 0 {
			h.Write(chunk)
			n += len(chunk)
			if !tooLong {
				if len(buf)+len(chunk) > max {
					tooLong, buf = true, nil
				} else {
					buf = append(buf, chunk...)
				}
			}
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		if e != nil && e != io.EOF {
			return nil, n, n, e
		}
		if tooLong {
			return nil, n, 0, e
		}
		if len(buf) == 0 && e == io.EOF {
			return nil, 0, 0, io.EOF
		}
		return buf, n, 0, e
	}
}

// meter counts bytes against the job limit and the shared speed ceiling.
type meter struct {
	r   io.Reader
	ctx context.Context
	j   *job
}

func (m *meter) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	if n > 0 {
		if e := m.j.add(int64(n)); e != nil {
			return n, e
		}
		if e := m.j.th.wait(m.ctx, int64(n)); e != nil {
			return n, e
		}
	}
	return n, err
}

// throttle keeps the whole job under a bytes-per-second ceiling.
type throttle struct {
	mu    sync.Mutex
	rate  int64
	start time.Time
	sent  int64
}

func newThrottle(rate int64) *throttle { return &throttle{rate: rate, start: time.Now()} }

func (t *throttle) wait(ctx context.Context, n int64) error {
	t.mu.Lock()
	t.sent += n
	due := t.start.Add(time.Duration(float64(t.sent) / float64(t.rate) * float64(time.Second)))
	t.mu.Unlock()
	d := time.Until(due)
	if d <= 0 {
		return nil
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var reservedWin = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)
var reUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// safeName makes a name safe to create on a Windows disk and unique within the job.
func safeName(name string, used map[string]bool) string {
	n := path.Base(strings.ReplaceAll(name, `\`, "/"))
	n = reUnsafe.ReplaceAllString(n, "_")
	n = strings.Trim(n, ". ")
	if n == "" {
		n = "file"
	}
	if reservedWin.MatchString(n) {
		n = "_" + n
	}
	if len(n) > 100 {
		n = n[:100]
	}
	low := strings.ToLower(n)
	if strings.HasSuffix(low, ".part") || strings.HasSuffix(low, ".state") || low == RecordName {
		n += "_"
	}
	base, ext := n, ""
	if i := strings.LastIndex(n, "."); i > 0 {
		base, ext = n[:i], n[i:]
	}
	cand := n
	for k := 2; used[strings.ToLower(cand)]; k++ {
		cand = fmt.Sprintf("%s-%d%s", base, k, ext)
	}
	used[strings.ToLower(cand)] = true
	return cand
}

func writeJSON(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	_ = os.Remove(p)
	return os.Rename(tmp, p)
}
