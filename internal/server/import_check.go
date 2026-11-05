package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// POST /api/cases/{id}/imports/check reads every uploaded file once, the way a
// preview does, and stores nothing. The page uses the answer to tell the person,
// before anything is imported, which files are ready and which need a fix (a
// computer to add, a time zone, another format).
//
// The call runs on the background-import machinery: the same case gate, import
// limiter slot, job registry and encrypted spool as postImport. The job's
// result is a checkReportDTO with one verdict per file. Each file goes through
// importFileWith with preview=true and allowUnlistedHost=false and no importer
// or computer, so the kind is detected and a file naming a computer the host
// list does not know is refused on purpose: that refusal is how the page learns
// the server name to offer.
//
// Optional text fields named "key" before a file part carry the client's id for
// that file (echoed back as Key); without one the key is "name|size".

const (
	// maxCheckFiles bounds the files of one check.
	maxCheckFiles = 200
	// checkBodyFactor times the single-file cap bounds the whole request, so the
	// spool cannot grow without limit.
	checkBodyFactor = 8
	// maxCheckKeyBytes bounds a client key.
	maxCheckKeyBytes = 512
)

// Verdicts of a checked file.
const (
	verdictReady         = "ready"
	verdictNeedsComputer = "needs-computer"
	verdictNeedsZone     = "needs-zone"
	verdictNotSupported  = "not-supported"
	verdictNoComputer    = "no-computer"
)

// checkFileVerdict is the answer for one file. Tied holds two or more kinds when
// the content could not choose between them; the page then asks which one.
type checkFileVerdict struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Size       int64    `json:"size"`
	Verdict    string   `json:"verdict"`
	Importer   string   `json:"importer"`
	Tied       []string `json:"tied"`
	ServerName string   `json:"serverName"`
	NeedsZone  bool     `json:"needsZone"`
	Reason     string   `json:"reason"`
	Rows       int      `json:"rows"`
	Emitted    int      `json:"emitted"`
	Rejected   int      `json:"rejected"`
}

// checkReportDTO is the result of a finished check job.
type checkReportDTO struct {
	Files     []checkFileVerdict `json:"files"`
	CheckedAt time.Time          `json:"checkedAt"`
}

// checkItem is one uploaded file waiting in its spool.
type checkItem struct {
	key, name string
	size      int64
	spool     *spoolFile
}

// postImportCheck answers POST /api/cases/{id}/imports/check with a 202 job.
func (h *handler) postImportCheck(w http.ResponseWriter, r *http.Request, raw string) {
	id, c, ok := h.openCaseForWrite(w, r, raw)
	if !ok {
		return
	}
	// The early handle serves the checks before the gate and is dropped before
	// the wait for it, as postImport does.
	defer func() {
		if c != nil {
			_ = c.Close()
		}
	}()
	if h.registry == nil {
		writeCaseFailure(w, http.StatusServiceUnavailable, "cases-unavailable", "Cases are unavailable.")
		return
	}
	if c.Info().Closed != nil {
		writeCaseFailure(w, http.StatusConflict, "case-closed", "The case is closed. Nothing can be imported into it.")
		return
	}
	h.event(r, "import_check_started")
	limit := h.uploadCap(nil)
	total := limit * checkBodyFactor
	if r.ContentLength > total {
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "request-too-large", "The upload is larger than the cap.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeCaseFailure(w, http.StatusUnsupportedMediaType, "not-multipart", "Content-Type must be multipart/form-data.")
		return
	}
	_ = c.Close()
	c = nil
	release, err := h.gates.acquire(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	slot, ok := h.imports.tryAcquire()
	if !ok {
		release()
		w.Header().Set("Retry-After", "2")
		writeCaseFailure(w, http.StatusTooManyRequests, "import-busy", "Other imports are running. Try again in a moment.")
		return
	}
	// The job takes the gate, the slot and the spools; every other exit gives
	// them back here.
	var (
		job       *importJob
		items     []checkItem
		handedOff bool
	)
	defer func() {
		if handedOff {
			return
		}
		for i := range items {
			items[i].spool.close()
		}
		if job != nil {
			h.jobs.discard(job)
		}
		slot()
		release()
	}()
	if job = h.jobs.create(id); job == nil {
		w.Header().Set("Retry-After", "2")
		writeCaseFailure(w, http.StatusTooManyRequests, "import-busy", "Other imports are running. Try again in a moment.")
		return
	}
	job.mu.Lock()
	job.kind = jobKindCheck
	job.mu.Unlock()

	r.Body = http.MaxBytesReader(w, newRollingBody(w, r.Body), total)
	form, err := r.MultipartReader()
	if err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "bad-form", "The upload is not a valid multipart form.")
		return
	}
	var key string
	var sum int64
	for {
		part, err := form.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeCheckFormError(w, err)
			return
		}
		switch part.FormName() {
		case "key":
			value, err := io.ReadAll(io.LimitReader(part, maxCheckKeyBytes+1))
			if err != nil || len(value) > maxCheckKeyBytes {
				writeCaseFailure(w, http.StatusBadRequest, "bad-form", "A form field is invalid.")
				return
			}
			key = string(value)
		case "file":
			if len(items) >= maxCheckFiles {
				writeCaseFailure(w, http.StatusRequestEntityTooLarge, "too-many-files", "Too many files to check at once.")
				return
			}
			item, ok := h.spoolCheckFile(w, part, key, limit)
			if !ok {
				return
			}
			items = append(items, item)
			sum += item.size
			key = ""
		default:
			writeCaseFailure(w, http.StatusBadRequest, "bad-form", "The upload has a field the server does not know.")
			return
		}
	}
	if len(items) == 0 {
		writeCaseFailure(w, http.StatusBadRequest, "file-missing", "The upload has no file.")
		return
	}
	job.total.Store(sum)
	handedOff = true
	h.jobs.wg.Add(1)
	go h.runCheckJob(job, items, id, slot, release)
	writeJSON(w, http.StatusAccepted, job.snapshot(), 4096)
}

func writeCheckFormError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "request-too-large", "The upload is larger than the cap.")
		return
	}
	writeCaseFailure(w, http.StatusBadRequest, "bad-form", "The upload is not a valid multipart form.")
}

// spoolCheckFile takes one file part into an encrypted spool. On a refusal it
// has answered and holds nothing.
func (h *handler) spoolCheckFile(w http.ResponseWriter, part *multipart.Part, key string, limit int64) (checkItem, bool) {
	name := baseName(part.FileName())
	spool, err := newSpool(h.jobs.spoolDir)
	if err != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "import-failed", "The check could not start.")
		return checkItem{}, false
	}
	tracked := &trackedReader{r: io.LimitReader(part, limit+1)}
	size, err := spool.fill(tracked)
	if err != nil {
		spool.close()
		h.writeImportError(w, err, tracked, casestore.ImportResult{})
		return checkItem{}, false
	}
	if size > limit {
		spool.close()
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "request-too-large", "The upload is larger than the cap.")
		return checkItem{}, false
	}
	if key == "" {
		key = fmt.Sprintf("%s|%d", name, size)
	}
	return checkItem{key: key, name: name, size: size, spool: spool}, true
}

// runCheckJob is the background half of a check. It holds the gate and the slot
// until it ends and deletes every spool.
func (h *handler) runCheckJob(job *importJob, items []checkItem, id casestore.CaseID, slot, release func()) {
	defer h.jobs.wg.Done()
	defer release()
	defer slot()
	defer func() {
		for i := range items {
			items[i].spool.close()
		}
	}()
	defer job.cancel()
	capture := &captureWriter{}
	defer func() {
		// A panic must not take the process down or leave the job running.
		if rec := recover(); rec != nil {
			capture = &captureWriter{}
			writeCaseFailure(capture, http.StatusInternalServerError, "import-failed", "The check could not be completed.")
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
	report := checkReportDTO{Files: make([]checkFileVerdict, 0, len(items))}
	for i := range items {
		report.Files = append(report.Files, h.checkOne(ctx, c, id, job, &items[i]))
		if ctx.Err() != nil {
			writeCaseFailure(capture, http.StatusBadRequest, "canceled", "The check was canceled.")
			return
		}
	}
	report.CheckedAt = h.jobs.now().UTC()
	writeJSON(capture, http.StatusOK, report, maxResponseBytes)
}

// checkOne reads one file as a preview and turns the answer into a verdict.
func (h *handler) checkOne(ctx context.Context, c casestore.Case, id casestore.CaseID, job *importJob, it *checkItem) checkFileVerdict {
	v := checkFileVerdict{Key: it.key, Name: it.name, Size: it.size, Tied: []string{}}
	notSupported := func(reason string) checkFileVerdict {
		v.Verdict, v.Reason = verdictNotSupported, reason
		return v
	}
	body, err := it.spool.reader()
	if err != nil {
		return notSupported("import-failed")
	}
	capture := &captureWriter{}
	counted := &countingReader{ctx: ctx, r: body, n: &job.read}
	h.importFileWith(ctx, capture, id, c, nil, nil, nil, counted, true, false, "", it.name, importer.Window{}, false)

	if capture.code() == http.StatusOK {
		var ans importAnswer
		if err := json.Unmarshal(capture.buf.Bytes(), &ans); err != nil {
			return notSupported("import-failed")
		}
		if len(ans.Tied) > 0 {
			v.Tied = ans.Tied
			return notSupported("")
		}
		if ans.Importer == "" || ans.Report == nil {
			return notSupported(string(importer.ReasonUnsupportedFormat))
		}
		v.Importer = ans.Importer
		v.Rows, v.Emitted, v.Rejected = int(ans.Report.Rows), int(ans.Report.Emitted), int(ans.Report.Rejected)
		v.Verdict = verdictReady
		if h.needsZone(ans.Report) {
			v.Verdict, v.NeedsZone = verdictNeedsZone, true
		}
		return v
	}

	var f caseFailure
	if err := json.Unmarshal(capture.buf.Bytes(), &f); err != nil || f.Reason == "" {
		return notSupported("import-failed")
	}
	reason := importer.Reason(f.Reason)
	byContent := reason == importer.ReasonUnsupportedFormat || reason == importer.ReasonArchive || strings.HasPrefix(f.Reason, "binary-")
	var imp importer.Importer
	if !byContent {
		imp = h.detectImporter(it)
		if imp != nil {
			v.Importer = imp.Name()
		}
	}
	switch {
	case reason == importer.ReasonUnknownHost:
		var observed []string
		if imp != nil {
			observed = h.observedHosts(ctx, id, imp, it)
		}
		if len(observed) > 0 {
			v.Verdict, v.ServerName = verdictNeedsComputer, observed[0]
		} else {
			v.Verdict = verdictNoComputer
		}
		return v
	case reason == importer.ReasonTZUnlabelled:
		v.Verdict, v.NeedsZone = verdictNeedsZone, true
		return v
	}
	return notSupported(f.Reason)
}

// needsZone says a file written in a host's local clock names a host the list
// holds without a time zone.
func (h *handler) needsZone(rep *reportDTO) bool {
	if rep.Quality.TimezoneRule != clock.RuleHostLocal {
		return false
	}
	opts := importer.Options{Hosts: h.hostsNow().Lookup}
	for _, name := range rep.ObservedHosts {
		if host, ok := opts.LookupHost(name); ok && host.TZ == nil {
			return true
		}
	}
	return false
}

// detectImporter picks the importer the head of the file points to, or nil when
// there is none or the content ties.
func (h *handler) detectImporter(it *checkItem) importer.Importer {
	body, err := it.spool.reader()
	if err != nil {
		return nil
	}
	head := make([]byte, importer.HeadSize)
	n, err := io.ReadFull(body, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil
	}
	best, _, tied := h.registry.Detect(head[:n], it.name)
	if tied != nil {
		return nil
	}
	return best
}

// observedHosts re-reads a file the host list refused and returns the computer
// names it met before the refusal. importFileWith keeps no report for a refused
// file, and the names are what the page needs to offer adding the computer.
func (h *handler) observedHosts(ctx context.Context, id casestore.CaseID, imp importer.Importer, it *checkItem) []string {
	body, err := it.spool.reader()
	if err != nil {
		return nil
	}
	nonce, err := randomNonce()
	if err != nil {
		return nil
	}
	opts := importer.Options{CaseID: string(id), ImportNonce: nonce, FileName: it.name, Hosts: h.hostsNow().Lookup}
	prepared, opts, err := importer.Prepare(imp, body, opts)
	if err != nil {
		return nil
	}
	rep, _ := imp.Parse(ctx, prepared, opts, func(evidence.Event) error { return nil })
	return rep.ObservedHosts
}
