package server

import (
	"net/http"
	"sync"
	"time"
)

// "Check for updates" and the background check (docs/auto-updater/SPEC.md section 6).
//
//	POST /api/update-check     runs a check now (the button); never by a page load
//	GET  /api/update/status    the last known answer and the settings; makes no network call
//	PUT  /api/update/settings  {auto, intervalMinutes}; saved on this PC
//
// Every check answer is HTTP 200: the status field says latest, available, unreachable, unverified, dev or none. Neither the
// error text nor any response body leaves the process. The check itself lives in internal/update; the timer in updater.go.

const updateManualMinGap = 5 * time.Second

// updateCheckReply is the JSON body of the three endpoints.
type updateCheckReply = UpdateStatus

// updateCheckState holds one Updater per server, kept here because the handler struct lives in server.go.
type updateCheckState struct {
	mu      sync.Mutex
	updater *Updater
	demo    bool      // demo build: ?preview= answers a canned reply (screenshots only)
	lastRun time.Time // a double click costs one check
}

var updateCheckStates sync.Map // *handler -> *updateCheckState

func updateCheckStateFor(h *handler) *updateCheckState {
	state, _ := updateCheckStates.LoadOrStore(h, &updateCheckState{})
	return state.(*updateCheckState)
}

// WithUpdater attaches the update checker. Without it the update endpoints answer "dev" and check nothing.
func WithUpdater(u *Updater) Option {
	return func(h *handler) {
		state := updateCheckStateFor(h)
		state.mu.Lock()
		state.updater = u
		state.mu.Unlock()
	}
}

// WithUpdateCheckDemo lets a demo build answer POST /api/update-check?preview=
// latest|available|offline with a canned reply and no outgoing call, so
// screenshots can show every state. A real build never sets it.
func WithUpdateCheckDemo() Option {
	return func(h *handler) {
		state := updateCheckStateFor(h)
		state.mu.Lock()
		state.demo = true
		state.mu.Unlock()
	}
}

// updateCheckDemoReply returns the canned reply for a preview value.
func updateCheckDemoReply(preview string) (updateCheckReply, bool) {
	base := updateCheckReply{Current: "1.2.0", Channel: "stable", Auto: true, IntervalMinutes: 30, CheckedAt: "2026-11-20T08:00:00Z"}
	switch preview {
	case "latest":
		base.Status = "latest"
		return base, true
	case "available":
		base.Status, base.Latest = "available", "1.3.0"
		base.URL = "https://github.com/abhinavjp/unravis/releases/tag/v1.3.0"
		return base, true
	case "offline":
		base.Status = "unreachable"
		return base, true
	case "unverified":
		base.Status = "unverified"
		return base, true
	}
	return updateCheckReply{}, false
}

func (h *handler) updaterAndDemo() (*Updater, bool) {
	state := updateCheckStateFor(h)
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.updater, state.demo
}

func noUpdater() updateCheckReply {
	return updateCheckReply{Status: "dev", IntervalMinutes: updateDefaultMinutes}
}

// updateCheck answers POST /api/update-check. It always answers 200; a failed check is status "unreachable" or "unverified".
func (h *handler) updateCheck(w http.ResponseWriter, r *http.Request) {
	u, demo := h.updaterAndDemo()
	if demo {
		if reply, ok := updateCheckDemoReply(r.URL.Query().Get("preview")); ok {
			h.event(r, "update_check", "status", reply.Status)
			writeJSON(w, http.StatusOK, reply, 4096)
			return
		}
	}
	if u == nil {
		writeJSON(w, http.StatusOK, noUpdater(), 4096)
		return
	}
	state := updateCheckStateFor(h)
	state.mu.Lock()
	recent := time.Since(state.lastRun) < updateManualMinGap
	if !recent {
		state.lastRun = time.Now()
	}
	state.mu.Unlock()
	var reply updateCheckReply
	if recent {
		reply = u.Status()
	} else {
		reply = u.CheckNow(r.Context())
	}
	h.event(r, "update_check", "status", reply.Status)
	writeJSON(w, http.StatusOK, reply, 4096)
}

// updateStatus answers GET /api/update/status.
func (h *handler) updateStatus(w http.ResponseWriter, r *http.Request) {
	u, demo := h.updaterAndDemo()
	if demo {
		if reply, ok := updateCheckDemoReply(r.URL.Query().Get("preview")); ok {
			writeJSON(w, http.StatusOK, reply, 4096)
			return
		}
	}
	if u == nil {
		writeJSON(w, http.StatusOK, noUpdater(), 4096)
		return
	}
	writeJSON(w, http.StatusOK, u.Status(), 4096)
}

// updateSettingsPut answers PUT /api/update/settings.
func (h *handler) updateSettingsPut(w http.ResponseWriter, r *http.Request) {
	u, _ := h.updaterAndDemo()
	if u == nil {
		writeError(w, http.StatusConflict, "Update checks are not available in this build.")
		return
	}
	in := UpdaterSettings{Auto: true, IntervalMinutes: updateDefaultMinutes}
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, err := cleanUpdaterSettings(in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := u.SetSettings(in)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.event(r, "update_settings", "auto", saved.Auto, "interval_minutes", saved.IntervalMinutes)
	writeJSON(w, http.StatusOK, u.Status(), 4096)
}
