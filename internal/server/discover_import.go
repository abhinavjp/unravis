package server

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/evtx"
	"api-audit-explorer/internal/discover/localread"
	"api-audit-explorer/internal/importer"
)

// Importing what a scan found (spec 4, 6). The request names findings by their
// opaque ids, never by path: the ids resolve against the in-memory scan, and the
// files behind them are opened only through localread, which refuses a path that
// is not the file the scan identified. The import runs as one background job per
// finding on the existing job machinery (GET .../imports/jobs/{job}); the jobs of
// one request run one after the other under one case gate and one limiter slot.

const (
	// maxDiscoverWindow is the longest window one request may ask for.
	maxDiscoverWindow = 31 * 24 * time.Hour
	// maxDiscoverFindings is how many findings one request may name.
	maxDiscoverFindings = 64
	// defaultChannelCap bounds the spool of one event channel when neither the
	// importer nor the store gives a smaller cap.
	defaultChannelCap = 32 << 20
	// capMargin is left under the importer's byte cap for replayed headers.
	capMargin = 64 << 10
)

type discoverImportReq struct {
	ScanID     string   `json:"scanId"`
	FindingIDs []string `json:"findingIds"`
	Window     struct {
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	} `json:"window"`
	AckGating bool   `json:"ackGating"`
	AckText   string `json:"ackText"`
	AckCustom bool   `json:"ackCustom"`
}

type discoverJobRef struct {
	FindingID string `json:"findingId"`
	JobID     string `json:"jobId"`
}

type discoverImportAnswer struct {
	Jobs []discoverJobRef `json:"jobs"`
}

// discoverItem is one finding of a request, with its own copy of what the scan
// knew (the scan may expire while the job runs).
type discoverItem struct {
	job *importJob
	f   discover.Finding
	fs  []scanFile
	imp importer.Importer
}

// discoverResult is the 201 body of one finding's job: the shape of an import
// answer, plus how many files and which gaps the pull had.
type discoverResult struct {
	Importer string           `json:"importer"`
	Score    int              `json:"score"`
	Tied     []string         `json:"tied"`
	Report   *reportDTO       `json:"report"`
	Record   *importRecordDTO `json:"record"`
	Files    int              `json:"files"`
	Gaps     []string         `json:"gaps"`
}

// POST /api/cases/{id}/discover/imports
func (h *handler) postDiscoverImports(w http.ResponseWriter, r *http.Request, raw string) {
	id, c, ok := h.openCaseForWrite(w, r, raw)
	if !ok {
		return
	}
	closed := c.Info().Closed != nil
	_ = c.Close()
	if h.registry == nil {
		writeCaseFailure(w, http.StatusServiceUnavailable, "cases-unavailable", "Cases are unavailable.")
		return
	}
	cfg, ok := h.discoverSupported(w)
	if !ok {
		return
	}
	if closed {
		writeCaseFailure(w, http.StatusConflict, "case-closed", "The case is closed. Nothing can be imported into it.")
		return
	}
	var req discoverImportReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !req.AckGating || req.AckText == "" {
		writeCaseFailure(w, http.StatusBadRequest, "ack-required", "Send ackGating and ackText with the request.")
		return
	}
	if len(req.AckText) > casestore.MaxAckText {
		writeCaseFailure(w, http.StatusBadRequest, "ack-too-long", ackTooLongText)
		return
	}
	ackText, ok := boundAck(req.AckText, req.AckCustom)
	if !ok {
		writeCaseFailure(w, http.StatusBadRequest, "ack-mismatch", ackMismatchText)
		return
	}
	win, ok := cleanWindow(req.Window.From, req.Window.To)
	if !ok {
		writeCaseFailure(w, http.StatusBadRequest, "bad-window", "The window must start before it ends and span at most 31 days.")
		return
	}
	if n := len(req.FindingIDs); n < 1 || n > maxDiscoverFindings {
		writeCaseFailure(w, http.StatusBadRequest, "bad-findings", "Name between 1 and 64 findings.")
		return
	}
	seen := map[string]bool{}
	for _, fid := range req.FindingIDs {
		if !findingIDPattern.MatchString(fid) || seen[fid] {
			writeCaseFailure(w, http.StatusBadRequest, "bad-findings", "A finding id is malformed or repeated.")
			return
		}
		seen[fid] = true
	}
	rec, how := h.disc.lookup(req.ScanID)
	switch how {
	case scanExpired:
		writeCaseFailure(w, http.StatusNotFound, "scan-expired", "That scan has expired. Scan again.")
		return
	case scanUnknown:
		writeCaseFailure(w, http.StatusNotFound, "scan-unknown", "That scan was not found.")
		return
	}
	rec.mu.Lock()
	state := rec.state
	picked := make([]*scanFinding, 0, len(req.FindingIDs))
	missing := false
	for _, fid := range req.FindingIDs {
		sf := rec.byID[fid]
		if sf == nil {
			missing = true
			break
		}
		picked = append(picked, &scanFinding{f: sf.f, files: append([]scanFile(nil), sf.files...)})
	}
	rec.mu.Unlock()
	if state != scanDone {
		writeCaseFailure(w, http.StatusConflict, "scan-not-ready", "That scan has not finished.")
		return
	}
	if missing {
		writeCaseFailure(w, http.StatusNotFound, "finding-unknown", "A finding was not found in that scan.")
		return
	}
	items := make([]discoverItem, 0, len(picked))
	for _, sf := range picked {
		switch sf.f.Kind {
		case discover.KindLiveSeries:
			writeCaseFailure(w, http.StatusUnprocessableEntity, "live-series-use-sampler", "Live counters are recorded with the sampler, not imported.")
			return
		case discover.KindLogFile, discover.KindLogSet, discover.KindEventChannel:
		default:
			writeCaseFailure(w, http.StatusUnprocessableEntity, "not-importable", "A finding needs an export or another computer and cannot be imported from here.")
			return
		}
		imp, found := h.registry.ByName(sf.f.Importer)
		if !found || imp.Name() == "win-live" {
			writeCaseFailure(w, http.StatusUnprocessableEntity, "not-importable", "No importer is available for a finding.")
			return
		}
		if sf.f.Kind == discover.KindEventChannel && cfg.Query == nil {
			writeCaseFailure(w, http.StatusNotImplemented, string(discover.ReasonNotSupportedOS), "Event logs cannot be read on this computer.")
			return
		}
		items = append(items, discoverItem{f: sf.f, fs: sf.files, imp: imp})
	}
	env := cfg.Env
	env.Importers = h.registry
	host := h.localHost(env)
	if host.proposed {
		writeCaseFailure(w, http.StatusUnprocessableEntity, string(importer.ReasonUnknownHost), importer.ReasonUnknownHost.Text())
		return
	}

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
	refs := make([]discoverJobRef, 0, len(items))
	for i := range items {
		job := h.jobs.create(id)
		if job == nil {
			for j := 0; j < i; j++ {
				h.jobs.discard(items[j].job)
			}
			slot()
			release()
			w.Header().Set("Retry-After", "2")
			writeCaseFailure(w, http.StatusTooManyRequests, "import-busy", "Other imports are running. Try again in a moment.")
			return
		}
		items[i].job = job
		refs = append(refs, discoverJobRef{FindingID: items[i].f.ID, JobID: job.id})
	}
	hint := host.hint
	h.jobs.wg.Add(1)
	go h.runDiscoverBatch(cfg, id, items, &hint, ackText, win, slot, release)
	writeJSON(w, http.StatusAccepted, discoverImportAnswer{Jobs: refs}, 1<<16)
}

// cleanWindow checks a request window: both bounds, UTC, from before to, at most
// 31 days.
func cleanWindow(from, to time.Time) (importer.Window, bool) {
	if from.IsZero() || to.IsZero() {
		return importer.Window{}, false
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) || to.Sub(from) > maxDiscoverWindow {
		return importer.Window{}, false
	}
	return importer.Window{From: from, To: to}, true
}

// runDiscoverBatch runs the findings of one request in order. It holds the case
// gate and the limiter slot until the last one ends, and lets the OS know the
// reads are background work.
func (h *handler) runDiscoverBatch(cfg *DiscoverConfig, id casestore.CaseID, items []discoverItem, hint *importer.HostHint, ackText string, win importer.Window, slot, release func()) {
	defer h.jobs.wg.Done()
	defer release()
	defer slot()
	leave := localread.EnterBackground()
	defer leave()
	for i := range items {
		h.runDiscoverItem(cfg, id, items[i], hint, ackText, win)
	}
}

func (h *handler) runDiscoverItem(cfg *DiscoverConfig, id casestore.CaseID, it discoverItem, hint *importer.HostHint, ackText string, win importer.Window) {
	job := it.job
	capture := &captureWriter{}
	defer job.cancel()
	defer func() {
		if rec := recover(); rec != nil {
			capture = &captureWriter{}
			writeCaseFailure(capture, http.StatusInternalServerError, "import-failed", "The import could not be completed. Nothing was stored.")
		}
		job.finish(h.jobs.now(), capture.code(), capture.buf.Bytes())
	}()
	ctx := job.ctx
	if ctx.Err() != nil {
		writeCaseFailure(capture, http.StatusBadRequest, "canceled", "The request was canceled.")
		return
	}
	job.setRunning()
	c, err := h.cases.Open(ctx, id)
	if err != nil {
		writeStoreError(capture, err)
		return
	}
	defer c.Close()
	if c.Info().Closed != nil {
		writeCaseFailure(capture, http.StatusConflict, "case-closed", "The case is closed. Nothing can be imported into it.")
		return
	}
	disc, ok := c.(casestore.Discoverer)
	if !ok {
		writeCaseFailure(capture, http.StatusNotImplemented, "discover-unavailable", "This case store cannot keep discovery marks.")
		return
	}
	dkey := disc.DiscoverKey()
	if len(dkey) == 0 {
		writeCaseFailure(capture, http.StatusConflict, "case-closed", "The case is closed. Nothing can be imported into it.")
		return
	}
	p := &discoverPull{h: h, cfg: cfg, id: id, c: c, disc: disc, dkey: dkey, it: it, hint: hint, ackText: ackText, win: win, ctx: ctx, w: capture}
	if it.f.Kind == discover.KindEventChannel {
		p.channel()
	} else {
		p.files()
	}
}

// discoverPull is the state of one finding's job.
type discoverPull struct {
	h       *handler
	cfg     *DiscoverConfig
	id      casestore.CaseID
	c       casestore.Case
	disc    casestore.Discoverer
	dkey    []byte
	it      discoverItem
	hint    *importer.HostHint
	ackText string
	win     importer.Window
	ctx     context.Context
	w       *captureWriter

	agg      importRecordDTO
	last     *reportDTO
	imported int
	gaps     []string
}

// caseMarks adapts the case's watermark table to localread.WatermarkStore.
type caseMarks struct {
	ctx context.Context
	d   casestore.Discoverer
}

func toDiscoverKey(k localread.FileKey) casestore.DiscoverFileKey {
	return casestore.DiscoverFileKey{VolumeSerial: k.VolumeSerial, FileIndex: k.FileIndex, PathHMAC: k.PathHMAC}
}

func (m caseMarks) Get(k localread.FileKey) (localread.Mark, bool, error) {
	v, ok, err := m.d.DiscoverMark(m.ctx, toDiscoverKey(k))
	if err != nil || !ok {
		return localread.Mark{}, false, err
	}
	return localread.Mark{Offset: v.Offset, LastLine: v.LastLine, HeadLen: v.HeadLen, HeadSHA: v.HeadSHA}, true, nil
}

func (m caseMarks) Put(k localread.FileKey, v localread.Mark) error {
	return m.d.PutDiscoverMark(m.ctx, toDiscoverKey(k), casestore.DiscoverMark{Offset: v.Offset, LastLine: v.LastLine, HeadLen: v.HeadLen, HeadSHA: v.HeadSHA})
}

func (m caseMarks) Delete(k localread.FileKey) error {
	return m.d.DeleteDiscoverMark(m.ctx, toDiscoverKey(k))
}

// headerKindFor says which header a range of that importer's file must replay.
func headerKindFor(name string) localread.HeaderKind {
	switch name {
	case "iis-w3c", "iis-httperr":
		return localread.HeaderW3C
	case "win-counters":
		return localread.HeaderCSV
	}
	return localread.HeaderNone
}

// byteCap is the most bytes one pull may hand to the importer.
func byteCap(imp importer.Importer) int64 {
	n := imp.DefaultLimits().MaxBytes
	if n > 2*capMargin {
		return n - capMargin
	}
	return n
}

// fail writes a failure and ends the job.
func (p *discoverPull) fail(status int, reason, msg string) {
	p.w.buf.Reset()
	p.w.status = 0
	writeCaseFailure(p.w, status, reason, msg)
}

// failErr maps an error of the local reader (never its text) to a failure.
func (p *discoverPull) failErr(err error) {
	switch {
	case errors.Is(err, context.Canceled):
		p.fail(http.StatusBadRequest, "canceled", "The request was canceled.")
	case errors.Is(err, context.DeadlineExceeded):
		p.fail(http.StatusGatewayTimeout, "timeout", "The request timed out.")
	case errors.Is(err, localread.ErrAccessDenied), errors.Is(err, evtx.ErrAccessDenied), errors.Is(err, fs.ErrPermission):
		p.fail(http.StatusForbidden, "needs-elevation", "Windows refused access. Run the dashboard as an administrator to read this.")
	case errors.Is(err, localread.ErrUnsafePath):
		p.fail(http.StatusUnprocessableEntity, "unsafe-path", "The file is not the one the scan found.")
	case errors.Is(err, localread.ErrNoHeader):
		p.fail(http.StatusUnprocessableEntity, "no-header", "The file has no header line to read its columns from.")
	case errors.Is(err, localread.ErrNoTimestamps):
		p.fail(http.StatusUnprocessableEntity, "no-timestamps", "The file has no readable timestamps, so the window cannot be applied.")
	case errors.Is(err, localread.ErrLineTooLong):
		p.fail(http.StatusUnprocessableEntity, "line-too-long", "The file has a line longer than the reader allows.")
	case errors.Is(err, localread.ErrFileShrank), errors.Is(err, localread.ErrMisaligned), errors.Is(err, fs.ErrNotExist):
		p.fail(http.StatusConflict, "file-changed", "The file changed while it was read. Scan again.")
	case errors.Is(err, localread.ErrNotSupportedOS), errors.Is(err, evtx.ErrNotSupportedOS):
		p.fail(http.StatusNotImplemented, string(discover.ReasonNotSupportedOS), "Host discovery is not available on this computer.")
	case errors.Is(err, evtx.ErrChannelRefused):
		p.fail(http.StatusUnprocessableEntity, "channel-refused", "That event log cannot be read.")
	case errors.Is(err, casestore.ErrCapExceeded):
		p.fail(http.StatusRequestEntityTooLarge, "cap-exceeded", "The file or the case is over its size or row cap.")
	default:
		p.fail(http.StatusInternalServerError, "import-failed", "The import could not be completed. Nothing was stored.")
	}
}

// done writes the aggregate answer, or the "nothing new" answer.
func (p *discoverPull) done() {
	p.w.buf.Reset()
	p.w.status = 0
	if p.imported == 0 {
		writeCaseFailure(p.w, http.StatusConflict, "duplicate", "Nothing new since the last pull of this source.")
		return
	}
	rec := p.agg
	rec.FileName = p.it.f.Display
	rec.KnownGaps = unionStrings(rec.KnownGaps, p.gaps)
	p.agg = rec
	out := discoverResult{Importer: p.it.imp.Name(), Tied: []string{}, Report: p.last, Record: &rec, Files: p.imported, Gaps: unionStrings(nil, p.gaps)}
	if out.Report != nil {
		out.Report.KnownGaps = unionStrings(out.Report.KnownGaps, p.gaps)
	}
	writeJSON(p.w, http.StatusCreated, out, maxResponseBytes)
}

func unionStrings(a, b []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range append(append([]string(nil), a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// add folds one committed import into the aggregate.
func (p *discoverPull) add(ans importAnswer) {
	if ans.Record == nil {
		return
	}
	if p.imported == 0 {
		p.agg = *ans.Record
	} else {
		p.agg.ID, p.agg.Imported, p.agg.SHA256 = ans.Record.ID, ans.Record.Imported, ans.Record.SHA256
		p.agg.Rows += ans.Record.Rows
		p.agg.Bytes += ans.Record.Bytes
		p.agg.Rejected += ans.Record.Rejected
		p.agg.KnownGaps = unionStrings(p.agg.KnownGaps, ans.Record.KnownGaps)
	}
	p.last = ans.Report
	p.imported++
}

// audit records the read. A failure to record it is not an import failure: the
// import is stored, and the row is bounded by a cap the user can see.
func (p *discoverPull) audit(row casestore.DiscoverRead) {
	row.At = p.h.jobs.now().UTC()
	row.Detector, row.Importer, row.Kind = p.it.f.Detector, p.it.imp.Name(), string(p.it.f.Kind)
	row.Display = truncateField(p.it.f.Display)
	if m := p.cfg.Env.Machine; m != nil {
		row.Elevated = m.Elevated()
	}
	row.WindowFrom, row.WindowTo = p.win.From, p.win.To
	_ = p.disc.RecordDiscoverRead(p.ctx, row)
}

func truncateField(s string) string {
	if len(s) > casestore.MaxDiscoverField {
		return s[:casestore.MaxDiscoverField]
	}
	return s
}

// importPiece runs the shared import over one piece (a file range or a channel
// spool) and parses the answer.
func (p *discoverPull) importPiece(body io.Reader, name string) (importAnswer, int, caseFailure, bool) {
	sub := &captureWriter{}
	p.h.importFileWith(p.ctx, sub, p.id, p.c, p.it.imp, p.hint, nil, body, false, true, p.ackText, name, p.win)
	status := sub.code()
	if status == http.StatusCreated {
		var ans importAnswer
		if err := json.Unmarshal(sub.buf.Bytes(), &ans); err == nil && ans.Record != nil {
			return ans, status, caseFailure{}, true
		}
		return importAnswer{}, http.StatusInternalServerError, caseFailure{Error: "The import could not be completed. Nothing was stored.", Reason: "import-failed"}, false
	}
	var f caseFailure
	if err := json.Unmarshal(sub.buf.Bytes(), &f); err != nil || f.Error == "" {
		f = caseFailure{Error: "The import could not be completed. Nothing was stored.", Reason: "import-failed"}
		status = http.StatusInternalServerError
	}
	return importAnswer{}, status, f, false
}

// isNothingNew says a piece failed only because it holds nothing to add: its
// bytes are already in the case, or every event lies outside the window. Both
// count as "nothing new", not as errors.
func isNothingNew(status int, f caseFailure) bool {
	return status == http.StatusConflict && (f.Reason == "duplicate" || isOutOfWindow(status, f))
}

// isOutOfWindow says a piece failed because no event of it lies in the window.
// Unlike a duplicate its bytes are not in the case, so its mark must not move.
func isOutOfWindow(status int, f caseFailure) bool {
	return status == http.StatusConflict && f.Reason == "out-of-window"
}

// forward ends the job with a failure the shared import wrote.
func (p *discoverPull) forward(status int, f caseFailure) {
	p.w.buf.Reset()
	p.w.status = 0
	writeJSON(p.w, status, f, 4096)
}

// sha tee --------------------------------------------------------------

type hashingReader struct {
	r io.Reader
	h interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
}

func (r *hashingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		_, _ = r.h.Write(p[:n])
	}
	return n, err
}

func (r *hashingReader) sum() string { return hex.EncodeToString(r.h.Sum(nil)) }

// limitedReader paces a reader through the limiter.
type pacedReader struct {
	ctx context.Context
	r   io.Reader
	lim localread.Limiter
}

func (r *pacedReader) Read(p []byte) (int, error) {
	if len(p) > 64<<10 {
		p = p[:64<<10]
	}
	n, err := r.r.Read(p)
	if n > 0 {
		if werr := r.lim.Wait(r.ctx, n); werr != nil {
			return n, werr
		}
	}
	return n, err
}

/* ----------------------------------------------------------------- files */

func (p *discoverPull) limiter() localread.Limiter {
	if p.cfg.Limiter != nil {
		return p.cfg.Limiter
	}
	return localread.DefaultLimiter()
}

// files pulls every file of the finding, oldest first. A file whose watermark
// shows nothing new is skipped; the first failure ends the job (earlier files
// stay imported and keep their marks).
func (p *discoverPull) files() {
	if len(p.it.fs) == 0 {
		p.fail(http.StatusUnprocessableEntity, "not-importable", "The finding names no file.")
		return
	}
	store := caseMarks{ctx: p.ctx, d: p.disc}
	popts := importer.Options{Host: p.hint}
	probe, hasProbe := importer.ProbeFunc(p.it.imp, popts)
	spec := localread.WindowSpec{From: p.win.From, To: p.win.To, Cap: byteCap(p.it.imp)}
	if hasProbe {
		spec.Probe = probe
	}
	whole := p.it.imp.Name() == "log4net-xml" // records may span lines: read whole, unmarked
	for _, f := range p.it.fs {
		if p.ctx.Err() != nil {
			p.failErr(p.ctx.Err())
			return
		}
		key := localread.KeyFor(f.id, p.dkey, f.path)
		if whole {
			if !p.wholeFile(f, key) {
				return
			}
			continue
		}
		rr, err := localread.NextWindow(p.ctx, p.cfg.Opener, store, key, f.path, f.id, headerKindFor(p.it.imp.Name()), spec, localread.RangeOptions{Limiter: p.cfg.Limiter})
		if err != nil {
			p.failErr(err)
			return
		}
		meta := rr.Meta
		row := casestore.DiscoverRead{
			PathHMAC: hex.EncodeToString(key.PathHMAC[:]), RangeStart: meta.Start, RangeEnd: meta.End,
			FileSize: meta.File.Size, FileModified: meta.File.ModTime,
		}
		if meta.Empty {
			// Nothing new (or the whole file lies outside the window). A restart
			// still moves the mark.
			_ = rr.Commit(store, time.Time{})
			_ = rr.Close()
			row.Result = "empty"
			p.audit(row)
			continue
		}
		hr := &hashingReader{r: &countingReader{ctx: p.ctx, r: rr, n: &p.it.job.read}, h: sha256.New()}
		ans, status, fl, ok := p.importPiece(hr, meta.File.Base)
		dup := !ok && isNothingNew(status, fl)
		row.BytesSHA256 = hr.sum()
		if !ok && isOutOfWindow(status, fl) {
			// No row of the piece lies inside the window and none was stored: the
			// mark stays put, so a later pull with a wider window still reads them.
			_ = rr.Close()
			row.Result = truncateField(fl.Reason)
			p.audit(row)
			continue
		}
		if !ok && !dup {
			_ = rr.Close()
			row.Result = truncateField(fl.Reason)
			p.audit(row)
			p.forward(status, fl)
			return
		}
		// The bytes are in the case (or were already): move the mark past them.
		var lastLine time.Time
		if ok && ans.Report != nil && ans.Report.Last != nil {
			lastLine = *ans.Report.Last
		}
		cerr := rr.Commit(store, lastLine)
		_ = rr.Close()
		switch {
		case dup:
			row.Result = truncateField(fl.Reason)
		case cerr != nil:
			row.Result = "mark-failed"
		default:
			row.Result = "ok"
		}
		p.audit(row)
		if ok {
			p.add(ans)
			if meta.Gap != "" {
				p.gaps = append(p.gaps, meta.Gap)
			}
		}
	}
	p.done()
}

// wholeFile imports one file in full through the frozen reader, with no mark: a
// second pull of the same bytes is answered "duplicate" by the store.
func (p *discoverPull) wholeFile(f scanFile, key localread.FileKey) bool {
	rc, info, err := localread.OpenWith(p.cfg.Opener, f.path, f.id)
	if err != nil {
		p.failErr(err)
		return false
	}
	defer rc.Close()
	hr := &hashingReader{r: &countingReader{ctx: p.ctx, r: &pacedReader{ctx: p.ctx, r: rc, lim: p.limiter()}, n: &p.it.job.read}, h: sha256.New()}
	ans, status, fl, ok := p.importPiece(hr, info.Base)
	dup := !ok && isNothingNew(status, fl)
	row := casestore.DiscoverRead{
		PathHMAC: hex.EncodeToString(key.PathHMAC[:]), RangeEnd: info.Size, FileSize: info.Size, FileModified: info.ModTime,
		BytesSHA256: hr.sum(),
	}
	switch {
	case ok:
		row.Result = "ok"
	case dup:
		row.Result = truncateField(fl.Reason)
	default:
		row.Result = truncateField(fl.Reason)
	}
	p.audit(row)
	if !ok && !dup {
		p.forward(status, fl)
		return false
	}
	if ok {
		p.add(ans)
	}
	return true
}

/* -------------------------------------------------------------- channels */

// errSpoolCap ends a channel read that outgrew its cap.
var errSpoolCap = errors.New("server: event log spool over its cap")

// cappedSpool is an io.Writer into an encrypted spool file with a size cap.
type cappedSpool struct {
	w    io.Writer
	max  int64
	n    int64
	over bool
}

func (c *cappedSpool) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.max {
		c.over = true
		return 0, errSpoolCap
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func channelFileName(channel string) string {
	var b strings.Builder
	for _, r := range channel {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := b.String()
	if len(name) > 100 {
		name = name[:100]
	}
	return name + ".csv"
}

// channel pulls an event log: the records after the stored record id, inside
// the window, spooled (encrypted, capped) and imported by win-events.
func (p *discoverPull) channel() {
	name := p.it.f.Display
	key := localread.FileKey{PathHMAC: localread.PathHMAC(p.dkey, "event-channel:"+name)}
	store := caseMarks{ctx: p.ctx, d: p.disc}
	var after uint64
	if m, ok, err := store.Get(key); err != nil {
		p.failErr(err)
		return
	} else if ok && m.Offset > 0 {
		after = uint64(m.Offset)
	}
	limit := p.it.imp.DefaultLimits().MaxBytes
	if fileCap := casestore.DefaultCaps.MaxFileBytes; fileCap > 0 && (limit <= 0 || fileCap < limit) {
		limit = fileCap
	}
	if limit <= 0 || limit > defaultChannelCap {
		limit = defaultChannelCap
	}
	spool, err := newSpool(p.h.jobs.spoolDir)
	if err != nil {
		p.fail(http.StatusInternalServerError, "import-failed", "The import could not start.")
		return
	}
	defer spool.close()
	st, err := spool.stream()
	if err != nil {
		p.fail(http.StatusInternalServerError, "import-failed", "The import could not start.")
		return
	}
	cw := &cappedSpool{w: cipher.StreamWriter{S: st, W: spool.f}, max: limit}
	res, err := p.cfg.Query(p.ctx, cw, name, p.win.From, p.win.To, after)
	if cw.over {
		p.fail(http.StatusRequestEntityTooLarge, "cap-exceeded", "The event log slice is over the size cap. Choose a shorter window.")
		return
	}
	if err != nil {
		p.failErr(err)
		return
	}
	row := casestore.DiscoverRead{
		PathHMAC: hex.EncodeToString(key.PathHMAC[:]), RangeStart: int64(after), RangeEnd: int64(res.LastRecordID),
		FileSize: cw.n,
	}
	if res.Rows == 0 {
		row.Result = "empty"
		p.audit(row)
		p.done()
		return
	}
	body, err := spool.reader()
	if err != nil {
		p.fail(http.StatusInternalServerError, "import-failed", "The import could not be completed. Nothing was stored.")
		return
	}
	hr := &hashingReader{r: &countingReader{ctx: p.ctx, r: io.LimitReader(body, cw.n), n: &p.it.job.read}, h: sha256.New()}
	ans, status, fl, ok := p.importPiece(hr, channelFileName(name))
	dup := !ok && isNothingNew(status, fl)
	row.BytesSHA256 = hr.sum()
	switch {
	case ok:
		row.Result = "ok"
	case dup:
		row.Result = truncateField(fl.Reason)
	default:
		row.Result = truncateField(fl.Reason)
	}
	p.audit(row)
	if !ok && !dup {
		p.forward(status, fl)
		return
	}
	if !ok && isOutOfWindow(status, fl) {
		p.done() // nothing stored: the record mark stays, a wider window can read them
		return
	}
	var lastLine time.Time
	if ok && ans.Report != nil && ans.Report.Last != nil {
		lastLine = *ans.Report.Last
	}
	_ = store.Put(key, localread.Mark{Offset: int64(res.LastRecordID), LastLine: lastLine})
	if ok {
		p.add(ans)
		if res.Truncated {
			p.gaps = append(p.gaps, "channel-truncated")
		}
	}
	p.done()
}

var _ = bytes.MinRead
