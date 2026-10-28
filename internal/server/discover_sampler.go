package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sampler"
)

// The live sampler (spec 3, 6). One sampler runs per process, recording CPU and
// memory of the host and its processes into one case. Its batches go through the
// same import path as a file (acknowledgement, ledger, caps, the case gate and
// the import limiter), so the case holds the same evidence either way.

const (
	// stopCaseClosed is the stop reason shown when the case ended the recording.
	stopCaseClosed = "case-closed"
	// samplerGateWait bounds how long a batch waits for the case gate.
	samplerGateWait = 2 * time.Minute
	// samplerSlotWait bounds how long a batch waits for an import slot.
	samplerSlotWait = 2 * time.Minute
	// samplerCloseGrace is how long a case close waits for the last batch.
	samplerCloseGrace = 30 * time.Second
)

var (
	errSamplerBusy      = errors.New("server: no import slot for a sampler batch")
	errSamplerCaseShut  = errors.New("server: the case is closed")
	errSamplerImportBad = errors.New("server: a sampler batch was refused")
)

// samplerRun is one recording, running or stopped. The last run stays until a
// new one replaces it so GET can report why it stopped.
type samplerRun struct {
	caseID casestore.CaseID
	s      *sampler.Sampler
	cancel context.CancelFunc
	closed chan struct{} // closed when the run has ended and released its case
	params samplerParams // what it was started with

	mu       sync.Mutex
	why      string // set before a stop the server asked for
	caseShut bool   // a batch found the case closed or erased
}

func (r *samplerRun) setWhy(why string) {
	r.mu.Lock()
	if r.why == "" {
		r.why = why
	}
	r.mu.Unlock()
}

// stop ends the run and waits until its case handle is released. discard
// cancels the run first, so a batch waiting for the case gate gives up at once
// (an erase must not wait for it); otherwise the last batch is saved, within
// grace.
func (r *samplerRun) stop(discard bool, grace time.Duration) {
	if discard {
		r.cancel()
	}
	done := make(chan struct{})
	go func() { r.s.Stop(); close(done) }()
	if grace > 0 && !discard {
		select {
		case <-done:
		case <-time.After(grace):
			r.cancel()
			<-done
		}
	} else {
		<-done
	}
	<-r.closed
}

func (r *samplerRun) stopReason(st sampler.Status) string {
	r.mu.Lock()
	why, shut := r.why, r.caseShut
	r.mu.Unlock()
	switch st.Stop {
	case sampler.StopLimit:
		return "cap"
	case sampler.StopWriteError:
		if shut {
			return stopCaseClosed
		}
		return "error"
	case sampler.StopSourceError:
		return "error"
	case sampler.StopUser:
		if why == stopCaseClosed {
			return stopCaseClosed
		}
	}
	return ""
}

type samplerReq struct {
	IntervalSec int    `json:"intervalSec"`
	AckGating   bool   `json:"ackGating"`
	AckText     string `json:"ackText"`
	AckCustom   bool   `json:"ackCustom"`
}

type samplerDTO struct {
	State      string     `json:"state"`
	Since      *time.Time `json:"since,omitempty"`
	Samples    int        `json:"samples"`
	StopReason string     `json:"stopReason,omitempty"`
}

// POST /api/cases/{id}/sampler
func (h *handler) postSampler(w http.ResponseWriter, r *http.Request, raw string) {
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
		writeCaseFailure(w, http.StatusConflict, "case-closed", "The case is closed. Nothing can be recorded into it.")
		return
	}
	var req samplerReq
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
	switch req.IntervalSec {
	case 5, 15, 30, 60:
	default:
		writeCaseFailure(w, http.StatusBadRequest, "bad-interval", "The interval must be 5, 15, 30 or 60 seconds.")
		return
	}
	imp, found := h.registry.ByName("win-live")
	if !found {
		writeCaseFailure(w, http.StatusUnprocessableEntity, "not-importable", "No importer is available for live samples.")
		return
	}
	env := cfg.Env
	env.Importers = h.registry
	host := h.localHost(env)
	if host.proposed {
		writeCaseFailure(w, http.StatusUnprocessableEntity, string(importer.ReasonUnknownHost), importer.ReasonUnknownHost.Text())
		return
	}

	p := samplerParams{interval: req.IntervalSec, ackText: ackText, hint: host.hint, imp: imp, flush: cfg.SamplerFlush, source: cfg.SamplerSource}
	h.disc.smu.Lock()
	defer h.disc.smu.Unlock()
	if cur := h.disc.sampler; cur != nil && cur.s.Status().Running {
		writeCaseFailure(w, http.StatusConflict, "sampler-busy", "A recording is already running. Stop it first.")
		return
	}
	if f := h.launchSamplerLocked(id, p); f != nil {
		if f.err != nil {
			writeStoreError(w, f.err)
			return
		}
		writeCaseFailure(w, f.status, f.reason, f.msg)
		return
	}
	writeJSON(w, http.StatusAccepted, samplerDTO{State: "recording"}, 1<<12)
}

// samplerParams is what a recording was started with, kept so an archive that is
// stopped or cancelled can start the same recording again.
type samplerParams struct {
	interval int
	ackText  string
	hint     importer.HostHint
	imp      importer.Importer
	flush    time.Duration
	source   sampler.Source
}

// launchFailure is why a recording could not start: a store error (err), or a
// status, reason and message of its own.
type launchFailure struct {
	err    error
	status int
	reason string
	msg    string
}

// launchSamplerLocked starts the recording into case id. The caller holds
// h.disc.smu and has checked that no recording is running.
func (h *handler) launchSamplerLocked(id casestore.CaseID, p samplerParams) *launchFailure {
	// The run keeps its own handle on the case: it names processes through it
	// and stores batches through it.
	pc, err := h.cases.Open(context.Background(), id)
	if err != nil {
		return &launchFailure{err: err}
	}
	h.disc.mu.Lock()
	runCtx, shut := h.disc.context(), h.disc.closed
	h.disc.mu.Unlock()
	if shut {
		_ = pc.Close()
		return &launchFailure{status: http.StatusServiceUnavailable, reason: "shutting-down", msg: "The dashboard is shutting down."}
	}
	ctx, cancel := context.WithCancel(runCtx)
	run := &samplerRun{caseID: id, cancel: cancel, closed: make(chan struct{}), params: p}
	hint := p.hint
	s, err := sampler.Start(sampler.Config{
		IntervalSeconds: p.interval,
		Pseudonymize:    pc.Pseudonymize,
		Source:          p.source,
		FlushEvery:      p.flush,
		Import: func(name string, body []byte) error {
			return h.sampleBatch(ctx, run, pc, p.imp, &hint, p.ackText, name, body)
		},
	})
	if err != nil {
		cancel()
		_ = pc.Close()
		if errors.Is(err, sampler.ErrNotSupportedOS) {
			return &launchFailure{status: http.StatusNotImplemented, reason: string(discover.ReasonNotSupportedOS), msg: "Live recording is not available on this computer."}
		}
		return &launchFailure{status: http.StatusInternalServerError, reason: "sampler-failed", msg: "The recording could not be started."}
	}
	run.s = s
	h.disc.sampler = run
	go func() {
		<-s.Done()
		cancel()
		_ = pc.Close()
		close(run.closed)
	}()
	return nil
}

// samplerRunningFor reports the parameters of a recording that is running into
// case id.
func (h *handler) samplerRunningFor(id casestore.CaseID) (samplerParams, bool) {
	h.disc.smu.Lock()
	defer h.disc.smu.Unlock()
	run := h.disc.sampler
	if run == nil || run.caseID != id || run.s == nil || !run.s.Status().Running {
		return samplerParams{}, false
	}
	return run.params, true
}

// restartSampler starts a recording again with the parameters it had. It is best
// effort: a recording that cannot start leaves the case as it is, and the page
// shows it as stopped.
func (h *handler) restartSampler(id casestore.CaseID, p samplerParams) {
	h.disc.smu.Lock()
	defer h.disc.smu.Unlock()
	if cur := h.disc.sampler; cur != nil && cur.s != nil && cur.s.Status().Running {
		return
	}
	_ = h.launchSamplerLocked(id, p)
}

// sampleBatch stores one batch of samples through the shared import path.
func (h *handler) sampleBatch(ctx context.Context, run *samplerRun, c casestore.Case, imp importer.Importer, hint *importer.HostHint, ackText, name string, body []byte) error {
	gctx, cancel := context.WithTimeout(ctx, samplerGateWait)
	release, err := h.gates.acquire(gctx, run.caseID)
	cancel()
	if err != nil {
		return err
	}
	defer release()
	var slot func()
	deadline := time.Now().Add(samplerSlotWait)
	for {
		s, ok := h.imports.tryAcquire()
		if ok {
			slot = s
			break
		}
		if time.Now().After(deadline) {
			return errSamplerBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	defer slot()
	if c.Info().Closed != nil {
		run.mu.Lock()
		run.caseShut = true
		run.mu.Unlock()
		return errSamplerCaseShut
	}
	cw := &captureWriter{}
	h.importFileWith(ctx, cw, run.caseID, c, imp, hint, nil, bytes.NewReader(body), false, true, ackText, name, importer.Window{})
	switch st := cw.code(); {
	case st == http.StatusCreated:
		return nil
	case st == http.StatusConflict:
		var f caseFailure
		if json.Unmarshal(cw.buf.Bytes(), &f) == nil && f.Reason == "duplicate" {
			return nil
		}
		if f.Reason == "case-closed" {
			run.mu.Lock()
			run.caseShut = true
			run.mu.Unlock()
		}
	}
	return errSamplerImportBad
}

// GET /api/cases/{id}/sampler
func (h *handler) getSampler(w http.ResponseWriter, r *http.Request, raw string) {
	id, c, ok := h.openCaseForWrite(w, r, raw)
	if !ok {
		return
	}
	_ = c.Close()
	h.disc.smu.Lock()
	run := h.disc.sampler
	h.disc.smu.Unlock()
	if run == nil || run.caseID != id {
		writeJSON(w, http.StatusOK, samplerDTO{State: "idle"}, 1<<12)
		return
	}
	st := run.s.Status()
	since := st.Since.UTC()
	out := samplerDTO{State: "recording", Since: &since, Samples: st.Samples}
	if !st.Running {
		out.State = "stopped"
		out.StopReason = run.stopReason(st)
	}
	writeJSON(w, http.StatusOK, out, 1<<12)
}

// DELETE /api/cases/{id}/sampler
func (h *handler) deleteSampler(w http.ResponseWriter, r *http.Request, raw string) {
	id, c, ok := h.openCaseForWrite(w, r, raw)
	if !ok {
		return
	}
	_ = c.Close()
	h.stopSamplerFor(id, false, "")
	w.WriteHeader(http.StatusNoContent)
}

// stopSamplerFor stops the recording into case id, if there is one, and waits
// for it to save its last batch (discard: for it to give up, because the case is
// being erased).
func (h *handler) stopSamplerFor(id casestore.CaseID, discard bool, why string) {
	h.disc.smu.Lock()
	run := h.disc.sampler
	h.disc.smu.Unlock()
	if run == nil || run.caseID != id || run.s == nil {
		return
	}
	if run.s.Status().Running && why != "" {
		run.setWhy(why)
	}
	run.stop(discard, samplerCloseGrace)
	if discard {
		h.disc.smu.Lock()
		if h.disc.sampler == run {
			h.disc.sampler = nil
		}
		h.disc.smu.Unlock()
	}
}

// stopSamplerAll stops the recording as the dashboard exits.
func (h *handler) stopSamplerAll() {
	h.disc.smu.Lock()
	run := h.disc.sampler
	h.disc.smu.Unlock()
	if run == nil || run.s == nil {
		return
	}
	run.stop(false, 3*time.Second)
}
