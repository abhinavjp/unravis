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

	h.disc.smu.Lock()
	defer h.disc.smu.Unlock()
	if cur := h.disc.sampler; cur != nil && cur.s.Status().Running {
		writeCaseFailure(w, http.StatusConflict, "sampler-busy", "A recording is already running. Stop it first.")
		return
	}
	// The run keeps its own handle on the case: it names processes through it
	// and stores batches through it.
	pc, err := h.cases.Open(context.Background(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	h.disc.mu.Lock()
	runCtx, shut := h.disc.context(), h.disc.closed
	h.disc.mu.Unlock()
	if shut {
		_ = pc.Close()
		writeCaseFailure(w, http.StatusServiceUnavailable, "shutting-down", "The dashboard is shutting down.")
		return
	}
	ctx, cancel := context.WithCancel(runCtx)
	run := &samplerRun{caseID: id, cancel: cancel, closed: make(chan struct{})}
	hint := host.hint
	flush := cfg.SamplerFlush
	s, err := sampler.Start(sampler.Config{
		IntervalSeconds: req.IntervalSec,
		Pseudonymize:    pc.Pseudonymize,
		Source:          cfg.SamplerSource,
		FlushEvery:      flush,
		Import: func(name string, body []byte) error {
			return h.sampleBatch(ctx, run, pc, imp, &hint, ackText, name, body)
		},
	})
	if err != nil {
		cancel()
		_ = pc.Close()
		if errors.Is(err, sampler.ErrNotSupportedOS) {
			writeCaseFailure(w, http.StatusNotImplemented, string(discover.ReasonNotSupportedOS), "Live recording is not available on this computer.")
			return
		}
		writeCaseFailure(w, http.StatusInternalServerError, "sampler-failed", "The recording could not be started.")
		return
	}
	run.s = s
	h.disc.sampler = run
	go func() {
		<-s.Done()
		cancel()
		_ = pc.Close()
		close(run.closed)
	}()
	writeJSON(w, http.StatusAccepted, samplerDTO{State: "recording"}, 1<<12)
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
