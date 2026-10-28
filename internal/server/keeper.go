package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/keeper"
)

// The key keeper (P14). In the shared layout the saved stores are the keeper's (keeper.SQLStore, keeper.MongoStore, keeper.BBStore): a
// stored password or read key never reaches this process, so the work that needs it runs in the keeper. The handlers find that out by
// asking the store for these optional methods; the per-user stores and the demo stores do not have them and keep the old path.

// keeperSQL is the keeper side of the saved SQL Server list.
type keeperSQL interface {
	Held(id string) (bool, error)
	TestSaved(ctx context.Context, ref keeper.SQLRef) (dbhealth.TestResult, string, error)
	HealthSaved(ctx context.Context, ref keeper.SQLRef, label string) (dbhealth.Report, error)
	LaneSaved(ctx context.Context, ref keeper.SQLRef, label string, req dbhealth.LaneRequest) (dbhealth.Report, error)
}

// keeperMongo is the keeper side of the saved MongoDB list.
type keeperMongo interface {
	Held(audit.ConnectionInput) (bool, error)
	TestSaved(ctx context.Context, in audit.ConnectionInput) (audit.ConnectionResult, error)
	HealthSaved(ctx context.Context, in audit.ConnectionInput, database, collection string) (dbhealth.Report, error)
	LaneSaved(ctx context.Context, in audit.ConnectionInput, label string, req dbhealth.LaneRequest) (dbhealth.Report, error)
	RememberChecked(ctx context.Context, label string, in audit.ConnectionInput, collection string, remember bool) (history.Recent, error)
}

// keeperAudit is the audit repository of the shared layout (keeper.AuditRouter): a source from a saved link whose password the keeper
// holds is opened in the keeper and read there (P14b), so the password never reaches this process.
type keeperAudit interface {
	AddHeldSource(ctx context.Context, in audit.SourceInput) (audit.Source, error)
	SourcesWithKeeper(ctx context.Context) ([]audit.Source, map[string]keeper.HeldState, error)
}

// keeperBB is the keeper side of the saved Blackbox list: reads that need a read key go through the keeper (Key never answers).
type keeperBB interface {
	KeyedSource(id string, loc blackbox.Location) blackbox.Source
}

// KeeperHub is the keeper wiring for the status and migration routes.
type KeeperHub struct {
	Client *keeper.Client
	// Migration moves the person's old per-user saved files into the keeper (nil when there is nothing to look at).
	Migration *keeper.Migration
	// OldCases counts the person's cases from before the PC was set up for everyone, which stay in their own folder, sealed for them
	// (owner ruling for P14a: not moved, said on the start screen). nil counts none.
	OldCases func() int
}

// WithKeeper turns on the shared layout's keeper routes.
func WithKeeper(k *KeeperHub) Option { return func(h *handler) { h.keeper = k } }

const (
	keeperStoppedText  = "The Unravis Key Keeper service is not running, so saved passwords and read keys cannot be used. Start the service, or ask the person who looks after this PC."
	keeperImpostorText = "Something other than the Unravis Key Keeper service answered on its pipe, so nothing was sent to it. Ask the person who looks after this PC."
	keeperHeldSource   = "The key keeper holds this link's password, and this Unravis cannot ask it to open the source. Restart Unravis, or ask the person who looks after this PC."
	keeperSeatsText    = "The key keeper has too many audit sources open for this PC. Remove a source you no longer need, or close another Unravis window, then try again."
	keeperGoneText     = "The key keeper closed this source: it was restarted, or the source was not used for a long time. Add it again from its saved link."
	keeperMixedText    = "Sources opened by the key keeper cannot be compared in one view with sources whose password you typed. Choose sources of one kind."
	keeperBusyText     = "A check with this saved connection is already running. Wait for it to finish."
	keeperRateText     = "The key keeper is answering too many requests. Wait a minute, then try again."
	keeperRefusedText  = "The key keeper refused this request."
)

// writeKeeperError words the keeper's own refusals (stopped, busy, too many requests). Store answers (not found, invalid, connect
// failures) are left to the handler, which already words them. It reports whether it wrote the answer.
func writeKeeperError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	type failure struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	switch {
	case errors.Is(err, keeper.ErrNotRunning):
		writeJSON(w, http.StatusServiceUnavailable, failure{keeperStoppedText, "keeper-stopped"}, 4096)
		return true
	case errors.Is(err, keeper.ErrImpostor):
		writeJSON(w, http.StatusServiceUnavailable, failure{keeperImpostorText, "keeper-impostor"}, 4096)
		return true
	case errors.Is(err, keeper.ErrKeeperHeld):
		writeJSON(w, http.StatusBadRequest, failure{keeperHeldSource, "keeper-held"}, 4096)
		return true
	case errors.Is(err, keeper.ErrHeldSourceGone):
		writeJSON(w, http.StatusNotFound, failure{keeperGoneText, "keeper-source-gone"}, 4096)
		return true
	case errors.Is(err, keeper.ErrMixedSources):
		writeJSON(w, http.StatusBadRequest, failure{keeperMixedText, "keeper-mixed"}, 4096)
		return true
	}
	var we *keeper.WireError
	if !errors.As(err, &we) {
		return false
	}
	switch we.Code {
	case keeper.CodeBusy:
		writeJSON(w, http.StatusConflict, failure{keeperBusyText, "keeper-busy"}, 4096)
	case keeper.CodeRateLimited:
		writeJSON(w, http.StatusTooManyRequests, failure{keeperRateText, "keeper-rate"}, 4096)
	case keeper.CodeRefused:
		msg := we.Message
		if msg == "" {
			msg = keeperRefusedText
		}
		writeJSON(w, http.StatusForbidden, failure{msg, "keeper-refused"}, 4096)
	case keeper.CodeUnavailable, keeper.CodeInternal, keeper.CodeVersion, keeper.CodeUnknownOp:
		writeJSON(w, http.StatusServiceUnavailable, failure{we.Message, "keeper-error"}, 4096)
	default:
		return false
	}
	return true
}

// heldSQL reports whether this SQL request must run in the keeper: a saved SQL login whose password the keeper stores and nothing typed
// now. A typed password still runs here, as before (owner ruling B19: typed passwords stay allowed).
func (h *handler) heldSQL(w http.ResponseWriter, req sqlRequest) (keeperSQL, *keeper.SQLRef, bool) {
	k, isKeeper := h.sqlConns.(keeperSQL)
	if !isKeeper || req.SavedID == "" || req.Password != "" {
		return nil, nil, true
	}
	held, err := k.Held(req.SavedID)
	if err != nil {
		if !writeKeeperError(w, err) {
			writeError(w, http.StatusServiceUnavailable, "Saved SQL Server connections could not be read.")
		}
		return nil, nil, false
	}
	if !held {
		return nil, nil, true
	}
	return k, &keeper.SQLRef{ID: req.SavedID, Database: req.Database}, true
}

// heldMongo reports whether this MongoDB request must run in the keeper (a saved link with a stored password and nothing typed).
func (h *handler) heldMongo(w http.ResponseWriter, in audit.ConnectionInput) (keeperMongo, bool) {
	k, isKeeper := h.history.(keeperMongo)
	if !isKeeper || in.SavedConnectionID == "" || in.Password != "" {
		return nil, true
	}
	held, err := k.Held(in)
	if err != nil {
		if !writeKeeperError(w, err) {
			writeSavedMongoError(w, err)
		}
		return nil, false
	}
	if !held {
		return nil, true
	}
	return k, true
}

func writeSavedMongoError(w http.ResponseWriter, err error) {
	if errors.Is(err, history.ErrCorrupt) || errors.Is(err, history.ErrUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "Saved connections could not be read. The saved file was left unchanged.")
		return
	}
	writeError(w, http.StatusBadRequest, "Saved connection no longer matches these settings. Select it again or enter the password.")
}

// testHeldMongo tests a saved MongoDB link inside the keeper.
func (h *handler) testHeldMongo(w http.ResponseWriter, r *http.Request, k keeperMongo, input audit.ConnectionInput) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	result, err := k.TestSaved(ctx, input)
	if err != nil {
		switch {
		case writeKeeperError(w, err), writeConnectionError(w, err):
		case errors.Is(err, history.ErrNotFound), errors.Is(err, history.ErrCorrupt), errors.Is(err, history.ErrUnavailable):
			writeSavedMongoError(w, err)
		case errors.Is(err, audit.ErrInvalidInput):
			writeError(w, http.StatusBadRequest, "Could not connect or list databases. Check the URI, credentials, authentication database, and permissions.")
		default:
			writeRepoError(w, err)
		}
		return
	}
	if result.Databases == nil {
		result.Databases = []string{}
	}
	h.mongoTests.record(result.Databases)
	writeJSON(w, http.StatusOK, result, maxResponseBytes)
}

// rememberInKeeper saves a MongoDB link in the keeper, which resolves a saved id and checks the settings itself.
func (h *handler) rememberInKeeper(w http.ResponseWriter, r *http.Request, k keeperMongo, label string, in audit.ConnectionInput, collection string, remember bool) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	item, err := k.RememberChecked(ctx, label, in, collection, remember)
	if err != nil {
		writeRememberError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item, maxResponseBytes)
}

// heldSourceDTO is a source in the list, with how the key keeper holds it: "held" (opened by the keeper from a saved link whose
// password it stores) or "stopped" (the same, but the keeper cannot be reached, so the source cannot be read until it runs again).
type heldSourceDTO struct {
	audit.Source
	Keeper keeper.HeldState `json:"keeper,omitempty"`
}

func (h *handler) sourcesWithKeeper(ctx context.Context, w http.ResponseWriter, ka keeperAudit) {
	items, states, err := ka.SourcesWithKeeper(ctx)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	out := make([]heldSourceDTO, 0, len(items))
	for _, s := range items {
		out = append(out, heldSourceDTO{Source: s, Keeper: states[s.ID]})
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}

// addHeldSource opens an audit source in the key keeper from a saved link whose password it stores. Nothing typed is sent: the keeper
// resolves the saved link itself and checks that it still matches the settings the person saw.
func (h *handler) addHeldSource(w http.ResponseWriter, r *http.Request, ka keeperAudit, input audit.SourceInput) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	input.Password = ""
	item, err := ka.AddHeldSource(ctx, input)
	if err != nil {
		writeHeldSourceError(w, err)
		return
	}
	h.rollups.clear()
	writeJSON(w, http.StatusCreated, heldSourceDTO{Source: item, Keeper: keeper.HeldByKeeper}, maxResponseBytes)
}

func writeHeldSourceError(w http.ResponseWriter, err error) {
	var we *keeper.WireError
	if errors.As(err, &we) {
		switch we.Code {
		case keeper.CodeBusy:
			writeJSON(w, http.StatusServiceUnavailable, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{keeperSeatsText, "keeper-full"}, 4096)
			return
		case keeper.CodeMismatch, keeper.CodeNotFound, keeper.CodeCorrupt, keeper.CodeUnavailable:
			writeSavedMongoError(w, err)
			return
		}
	}
	switch {
	case writeKeeperError(w, err), writeConnectionError(w, err):
	default:
		writeRepoError(w, err)
	}
}

// writeHeldMongoFailure words a failed keeper MongoDB check the way the local path does.
func writeHeldMongoFailure(w http.ResponseWriter, err error) {
	switch {
	case writeKeeperError(w, err), writeConnectionError(w, err):
	case errors.Is(err, history.ErrNotFound), errors.Is(err, history.ErrCorrupt), errors.Is(err, history.ErrUnavailable), errors.Is(err, audit.ErrInvalidInput):
		writeSavedMongoError(w, err)
	default:
		writeError(w, http.StatusBadRequest, "Could not connect to MongoDB. Check the connection details and permissions.")
	}
}

/* ------------------------------------------------------------ status, move */

type keeperStatusDTO struct {
	// Mode is "user" (per-user files, no keeper) or "shared" (the keeper holds saved secrets).
	Mode    string `json:"mode"`
	Running bool   `json:"running"`
	// Problem is "stopped" or "impostor" when the keeper cannot be used.
	Problem         string         `json:"problem,omitempty"`
	Pending         keeper.Pending `json:"pending"`
	OldFilesPresent bool           `json:"oldFilesPresent"`
}

// demoKeeperStatus is the canned answer for the screenshot previews (?preview=saved|stopped|migrate, demo only).
func demoKeeperStatus(preview string) (keeperStatusDTO, bool) {
	switch preview {
	case "saved":
		return keeperStatusDTO{Mode: "shared", Running: true}, true
	case "stopped":
		return keeperStatusDTO{Mode: "shared", Problem: "stopped"}, true
	case "migrate":
		return keeperStatusDTO{Mode: "shared", Running: true, Pending: keeper.Pending{SQL: 2, Mongo: 1, Blackbox: 1}, OldFilesPresent: true}, true
	case "moved":
		return keeperStatusDTO{Mode: "shared", Running: true, OldFilesPresent: true}, true
	}
	return keeperStatusDTO{}, false
}

// demoHeldSources is the canned source list for the screenshot previews (?preview=saved|stopped, demo only): the demo's sources shown
// as opened by the key keeper, which runs (saved) or cannot be reached (stopped).
func demoHeldSources(items []audit.Source, preview string) ([]heldSourceDTO, bool) {
	state := keeper.HeldByKeeper
	switch preview {
	case "saved":
	case "stopped":
		state = keeper.HeldButDown
	default:
		return nil, false
	}
	out := make([]heldSourceDTO, 0, len(items))
	for _, s := range items {
		out = append(out, heldSourceDTO{Source: s, Keeper: state})
	}
	return out, true
}

func keeperProblem(err error) string {
	if errors.Is(err, keeper.ErrImpostor) {
		return "impostor"
	}
	return "stopped"
}

func (h *handler) keeperStatus(w http.ResponseWriter, r *http.Request) {
	if h.demo {
		if st, ok := demoKeeperStatus(r.URL.Query().Get("preview")); ok {
			writeJSON(w, http.StatusOK, st, 4096)
			return
		}
	}
	if h.keeper == nil {
		writeJSON(w, http.StatusOK, keeperStatusDTO{Mode: "user"}, 4096)
		return
	}
	st := keeperStatusDTO{Mode: "shared"}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.keeper.Client.Ping(ctx); err != nil {
		st.Problem = keeperProblem(err)
	} else {
		st.Running = true
	}
	if m := h.keeper.Migration; m != nil {
		st.OldFilesPresent = m.OldFilesPresent()
		if st.Running && st.OldFilesPresent {
			// A count that cannot be made leaves the move offered: the person's old files are still there.
			if p, err := m.Pending(); err == nil {
				st.Pending = p
			}
		}
	}
	writeJSON(w, http.StatusOK, st, 4096)
}

func (h *handler) keeperMigrate(w http.ResponseWriter, r *http.Request) {
	if h.demo {
		writeJSON(w, http.StatusOK, keeperStatusDTO{Mode: "shared", Running: true, OldFilesPresent: true}, 4096)
		return
	}
	if h.keeper == nil || h.keeper.Migration == nil {
		writeError(w, http.StatusNotFound, "There is nothing to move: this PC keeps saved connections per person.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	p, err := h.keeper.Migration.Migrate(ctx)
	if err != nil {
		if !writeKeeperError(w, err) {
			writeError(w, http.StatusServiceUnavailable, "The saved connections could not be moved. Your old files were left unchanged.")
		}
		return
	}
	writeJSON(w, http.StatusOK, keeperStatusDTO{Mode: "shared", Running: true, Pending: p, OldFilesPresent: h.keeper.Migration.OldFilesPresent()}, 4096)
}

// keeperRemoveOld deletes the person's old per-user saved files, only after they confirm and only once every record in them is in the
// keeper with its secret.
func (h *handler) keeperRemoveOld(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm bool `json:"confirm"`
		// ConfirmDuplicates says the person also saw that some of their old records are in the keeper only as someone else's copy.
		ConfirmDuplicates bool `json:"confirmDuplicates"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, "Confirm that the old copies should be removed.")
		return
	}
	if h.demo {
		writeJSON(w, http.StatusOK, keeperStatusDTO{Mode: "shared", Running: true}, 4096)
		return
	}
	if h.keeper == nil || h.keeper.Migration == nil {
		writeError(w, http.StatusNotFound, "There is nothing to remove: this PC keeps saved connections per person.")
		return
	}
	if err := h.keeper.Migration.RemoveOld(body.ConfirmDuplicates); err != nil {
		type refusal struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if errors.Is(err, keeper.ErrNotVerified) {
			writeJSON(w, http.StatusConflict, refusal{"Not every old saved connection is in the key keeper yet, so the old copies were kept. Move them first.", "not-verified"}, 4096)
			return
		}
		if errors.Is(err, keeper.ErrDuplicatesUnconfirmed) {
			writeJSON(w, http.StatusConflict, refusal{"Some of your old saved connections were already saved by someone else on this PC, so the old copies were kept. Check the list and confirm again.", "duplicates-unconfirmed"}, 4096)
			return
		}
		if !writeKeeperError(w, err) {
			writeError(w, http.StatusServiceUnavailable, "The old copies could not be removed. Nothing in the key keeper was changed.")
		}
		return
	}
	writeJSON(w, http.StatusOK, keeperStatusDTO{Mode: "shared", Running: true}, 4096)
}
