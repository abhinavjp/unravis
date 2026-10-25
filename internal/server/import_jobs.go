package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

// Background imports. POST /api/cases/{id}/imports?async=1 runs every check the
// synchronous call runs (importer, host, supplied settings, acknowledgement, size
// cap, closed case, the per-case gate and the import limiter), takes in the whole
// upload, and then parses and stores it in a goroutine. The request answers 202
// with a job; GET /api/cases/{id}/imports/jobs/{job} reports the job, and DELETE
// cancels it.
//
// The parse runs through the same importFile function as the synchronous call,
// writing to a buffer instead of the connection, so the finished job carries
// exactly the status and body the synchronous call would have sent: nothing is
// translated twice and the error codes cannot drift.
//
// A request body cannot outlive its request, so the upload waits in a spool file
// until the job has read it. The storage decision is that raw bytes never reach
// the disk unprotected, so the spool is encrypted with a random key that lives
// only in this process's memory, and it is deleted when the job ends. A spool
// left behind by a crash can never be read: its key is gone.
//
// Job state holds counts, a state word, and the same body the synchronous call
// answers (reasons and numbers, never file content). Nothing is logged.

// Job states.
const (
	jobQueued  = "queued"
	jobRunning = "running"
	jobDone    = "done"
	jobFailed  = "failed"
)

const (
	// importJobTTL is how long a finished job stays readable.
	importJobTTL = time.Hour
	// maxImportJobs caps the jobs kept, finished ones included. At most the
	// import limiter's few can be running, so the rest are old results.
	maxImportJobs = 64
	// jobIDBytes is the random size of a job id.
	jobIDBytes = 16
	// spoolPattern names a spool file; it holds only ciphertext.
	spoolPattern = "unravis-import-*.tmp"
)

var jobIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// importJobs is the in-memory registry. The zero value works.
type importJobs struct {
	mu     sync.Mutex
	m      map[string]*importJob
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	wg     sync.WaitGroup

	// Test hooks: zero means the defaults above.
	max      int
	ttl      time.Duration
	clock    func() time.Time
	spoolDir string
}

type importJob struct {
	id     string
	caseID casestore.CaseID
	ctx    context.Context
	cancel context.CancelFunc
	read   atomic.Int64
	total  atomic.Int64

	mu       sync.Mutex
	state    string
	canceled bool
	status   int
	result   json.RawMessage
	failure  *caseFailure
	finished time.Time
}

// importJobDTO is the body of the 202 answer and of GET .../jobs/{job}.
// BytesRead counts the upload bytes the parser has taken in; it reaches
// BytesTotal while the file is being committed. Status and Result (done) or
// Failure (failed) are the status and body the synchronous call would answer.
type importJobDTO struct {
	ID         string          `json:"id"`
	State      string          `json:"state"`
	BytesRead  int64           `json:"bytesRead"`
	BytesTotal int64           `json:"bytesTotal"`
	Status     int             `json:"status,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Failure    *caseFailure    `json:"failure,omitempty"`
}

func (j *importJobs) now() time.Time {
	if j.clock != nil {
		return j.clock()
	}
	return time.Now()
}

func (j *importJobs) limits() (int, time.Duration) {
	max, ttl := j.max, j.ttl
	if max <= 0 {
		max = maxImportJobs
	}
	if ttl <= 0 {
		ttl = importJobTTL
	}
	return max, ttl
}

// pruneLocked drops finished jobs older than the TTL. The caller holds j.mu.
func (j *importJobs) pruneLocked() {
	_, ttl := j.limits()
	now := j.now()
	for id, job := range j.m {
		job.mu.Lock()
		old := !job.finished.IsZero() && now.Sub(job.finished) > ttl
		job.mu.Unlock()
		if old {
			delete(j.m, id)
		}
	}
}

// create registers a queued job, or returns nil when the registry is shut down
// or full of jobs that are still running (the oldest finished job is dropped to
// make room first).
func (j *importJobs) create(caseID casestore.CaseID) *importJob {
	var raw [jobIDBytes]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	if j.ctx == nil {
		j.ctx, j.cancel = context.WithCancel(context.Background())
	}
	j.pruneLocked()
	max, _ := j.limits()
	for len(j.m) >= max {
		var oldest *importJob
		var oldestAt time.Time
		for _, job := range j.m {
			job.mu.Lock()
			at := job.finished
			job.mu.Unlock()
			if !at.IsZero() && (oldest == nil || at.Before(oldestAt)) {
				oldest, oldestAt = job, at
			}
		}
		if oldest == nil {
			return nil
		}
		delete(j.m, oldest.id)
	}
	if j.m == nil {
		j.m = map[string]*importJob{}
	}
	ctx, cancel := context.WithCancel(j.ctx)
	job := &importJob{id: hex.EncodeToString(raw[:]), caseID: caseID, cancel: cancel, state: jobQueued}
	job.ctx = ctx
	j.m[job.id] = job
	return job
}

// discard removes a job that never started (a refusal after it was created).
func (j *importJobs) discard(job *importJob) {
	job.cancel()
	j.mu.Lock()
	delete(j.m, job.id)
	j.mu.Unlock()
}

// get returns the job of that case, or nil. A job of another case is not found.
func (j *importJobs) get(caseID casestore.CaseID, id string) *importJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.pruneLocked()
	job := j.m[id]
	if job == nil || job.caseID != caseID {
		return nil
	}
	return job
}

// remove drops a finished job from the registry.
func (j *importJobs) remove(job *importJob) {
	j.mu.Lock()
	delete(j.m, job.id)
	j.mu.Unlock()
}

// cancelCase cancels every job of the case that has not finished.
func (j *importJobs) cancelCase(caseID casestore.CaseID) {
	j.mu.Lock()
	var hit []*importJob
	for _, job := range j.m {
		if job.caseID == caseID {
			hit = append(hit, job)
		}
	}
	j.mu.Unlock()
	for _, job := range hit {
		job.requestCancel()
	}
}

// cancelCaseUntilStopped cancels the case's jobs now and again every 50 ms until
// the returned function is called. An erase uses it while it waits for the case
// gate, so a job that took the gate just before the erase asked for it is
// cancelled too.
func (j *importJobs) cancelCaseUntilStopped(caseID casestore.CaseID) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			j.cancelCase(caseID)
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

// shutdown cancels every job, refuses new ones, and waits up to wait for the
// running ones to stop (they roll their imports back).
func (j *importJobs) shutdown(wait time.Duration) {
	j.mu.Lock()
	j.closed = true
	cancel := j.cancel
	j.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	stopped := make(chan struct{})
	go func() {
		j.wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(wait):
	}
}

// CancelImports cancels the background imports of a handler returned by New and
// waits briefly for them to stop. The dashboard calls it as it shuts down, before
// the case store closes.
func CancelImports(served http.Handler) {
	if h, ok := served.(*handler); ok && h != nil {
		h.disc.shutdown(3 * time.Second)
		h.stopSamplerAll()
		h.jobs.shutdown(3 * time.Second)
	}
}

func (jb *importJob) requestCancel() {
	jb.mu.Lock()
	if jb.finished.IsZero() {
		jb.canceled = true
	}
	jb.mu.Unlock()
	jb.cancel()
}

func (jb *importJob) active() bool {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.finished.IsZero()
}

func (jb *importJob) setRunning() {
	jb.mu.Lock()
	if jb.finished.IsZero() {
		jb.state = jobRunning
	}
	jb.mu.Unlock()
}

// finish records the answer importFile wrote. A job that was cancelled and did
// not commit reports "canceled", whatever the parser made of the cancellation.
func (jb *importJob) finish(at time.Time, status int, body []byte) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	if !jb.finished.IsZero() {
		return
	}
	jb.finished = at
	jb.status = status
	if status > 0 && status < 400 {
		jb.state = jobDone
		jb.result = append(json.RawMessage(nil), body...)
		return
	}
	jb.state = jobFailed
	var f caseFailure
	if err := json.Unmarshal(body, &f); err != nil || f.Error == "" {
		f = caseFailure{Error: "The import could not be completed. Nothing was stored.", Reason: "import-failed"}
		jb.status = http.StatusInternalServerError
	}
	if jb.canceled {
		f = caseFailure{Error: "The import was canceled.", Reason: "canceled"}
		jb.status = http.StatusBadRequest
	}
	jb.failure = &f
}

func (jb *importJob) snapshot() importJobDTO {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return importJobDTO{
		ID: jb.id, State: jb.state, BytesRead: jb.read.Load(), BytesTotal: jb.total.Load(),
		Status: jb.status, Result: jb.result, Failure: jb.failure,
	}
}

/* ----------------------------------------------------------------- start */

// importAsync takes in the file part, then starts the job over it. The gate and
// the limiter slot pass to the job (handOff is called before the goroutine
// starts); on any refusal here they stay with postImport, which releases them.
func (h *handler) importAsync(w http.ResponseWriter, id casestore.CaseID, job *importJob, imp importer.Importer, hint *importer.HostHint, supplied map[string]string, part io.Reader, preview, ackGating bool, ackText, fileName string, handOff func(), slot, release func()) {
	if !preview && (!ackGating || ackText == "") {
		writeCaseFailure(w, http.StatusBadRequest, "ack-required", "Send ackGating and ackText before the file.")
		return
	}
	tracked := &trackedReader{r: part}
	spool, err := newSpool(h.jobs.spoolDir)
	if err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "import-failed", "The import could not start.")
		return
	}
	size, err := spool.fill(tracked)
	if err != nil {
		spool.close()
		h.writeImportError(w, err, tracked, casestore.ImportResult{})
		return
	}
	job.total.Store(size)
	handOff()
	h.jobs.wg.Add(1)
	go h.runImportJob(job, spool, id, imp, hint, supplied, preview, ackGating, ackText, fileName, slot, release)
	writeJSON(w, http.StatusAccepted, job.snapshot(), 4096)
}

// runImportJob is the background half of an async import.
func (h *handler) runImportJob(job *importJob, spool *spoolFile, id casestore.CaseID, imp importer.Importer, hint *importer.HostHint, supplied map[string]string, preview, ackGating bool, ackText, fileName string, slot, release func()) {
	defer h.jobs.wg.Done()
	defer release()
	defer slot()
	defer spool.close()
	defer job.cancel()
	capture := &captureWriter{}
	defer func() {
		// A panic must not take the process down or leave the job running.
		if rec := recover(); rec != nil {
			capture = &captureWriter{}
			writeCaseFailure(capture, http.StatusInternalServerError, "import-failed", "The import could not be completed. Nothing was stored.")
		}
		job.finish(h.jobs.now(), capture.code(), capture.buf.Bytes())
	}()
	job.setRunning()
	ctx := job.ctx
	c, err := h.cases.Open(ctx, id)
	if err != nil {
		writeStoreError(capture, err)
		return
	}
	defer c.Close()
	body, err := spool.reader()
	if err != nil {
		writeCaseFailure(capture, http.StatusInternalServerError, "import-failed", "The import could not be completed. Nothing was stored.")
		return
	}
	counted := &countingReader{ctx: ctx, r: body, n: &job.read}
	h.importFile(ctx, capture, id, c, imp, hint, supplied, counted, preview, ackGating, ackText, fileName)
}

/* -------------------------------------------------------------- handlers */

// caseJobPath matches /api/cases/{id}/imports/jobs/{job} and returns "id/job".
func caseJobPath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/cases/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] != "imports" || parts[2] != "jobs" || parts[3] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[3], true
}

func (h *handler) jobFromPath(w http.ResponseWriter, arg string) (*importJob, bool) {
	rawCase, rawJob, _ := strings.Cut(arg, "/")
	id, ok := caseIDOf(w, rawCase)
	if !ok || !h.casesAvailable(w) {
		return nil, false
	}
	var job *importJob
	if jobIDPattern.MatchString(rawJob) {
		job = h.jobs.get(id, rawJob)
	}
	if job == nil {
		writeCaseFailure(w, http.StatusNotFound, "job-not-found", "That import job was not found. It may have expired.")
		return nil, false
	}
	return job, true
}

// getImportJob answers GET /api/cases/{id}/imports/jobs/{job}.
func (h *handler) getImportJob(w http.ResponseWriter, _ *http.Request, arg string) {
	job, ok := h.jobFromPath(w, arg)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, job.snapshot(), maxResponseBytes)
}

// deleteImportJob answers DELETE /api/cases/{id}/imports/jobs/{job}. A job that
// is still running is cancelled (it reports "canceled" when it stops and then
// stays readable until it expires); a finished job is forgotten.
func (h *handler) deleteImportJob(w http.ResponseWriter, _ *http.Request, arg string) {
	job, ok := h.jobFromPath(w, arg)
	if !ok {
		return
	}
	if job.active() {
		job.requestCancel()
	} else {
		h.jobs.remove(job)
	}
	w.WriteHeader(http.StatusNoContent)
}

/* --------------------------------------------------------------- helpers */

// captureWriter is the ResponseWriter importFile writes to in a job.
type captureWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}

func (c *captureWriter) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.buf.Write(p)
}

func (c *captureWriter) code() int {
	if c.status == 0 {
		return http.StatusInternalServerError
	}
	return c.status
}

// countingReader counts the bytes read and stops reading once ctx is done, so a
// cancelled job ends even when the importer never looks at its context. It has
// no Size method on purpose: the store then streams the file exactly as it does
// for a synchronous import.
type countingReader struct {
	ctx context.Context
	r   io.Reader
	n   *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// spoolFile holds one upload encrypted (AES-256 in counter mode, random key and
// IV kept only here) in a temp file.
type spoolFile struct {
	f   *os.File
	key [32]byte
	iv  [aes.BlockSize]byte
}

func newSpool(dir string) (*spoolFile, error) {
	s := &spoolFile{}
	if _, err := io.ReadFull(rand.Reader, s.key[:]); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rand.Reader, s.iv[:]); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, spoolPattern) // opened 0600
	if err != nil {
		return nil, err
	}
	s.f = f
	return s, nil
}

func (s *spoolFile) stream() (cipher.Stream, error) {
	block, err := aes.NewCipher(s.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewCTR(block, s.iv[:]), nil
}

// fill writes all of r into the spool and returns the byte count.
func (s *spoolFile) fill(r io.Reader) (int64, error) {
	st, err := s.stream()
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(cipher.StreamWriter{S: st, W: s.f}, r)
	if err != nil {
		return n, err
	}
	return n, s.f.Sync()
}

// reader returns the plain bytes from the start.
func (s *spoolFile) reader() (io.Reader, error) {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	st, err := s.stream()
	if err != nil {
		return nil, err
	}
	return cipher.StreamReader{S: st, R: s.f}, nil
}

// close deletes the file and forgets the key. It is safe to call twice.
func (s *spoolFile) close() {
	if s.f == nil {
		return
	}
	name := s.f.Name()
	_ = s.f.Close()
	// A file that cannot be removed holds only ciphertext whose key is gone.
	_ = os.Remove(name)
	s.f = nil
	s.key = [32]byte{}
}
