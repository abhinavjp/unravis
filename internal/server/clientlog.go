package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/applog"
)

// The UI's half of the journey log. The page sends small, allow-listed
// entries — a screen it showed, an action the user took, an error it displayed
// — and the server writes them into the same log file with src=ui, so one file
// tells the whole story of a session. Entries never carry field contents, ids
// or query text: the fields are fixed, the strings are capped, and the message
// is sanitized and truncated here whatever the page sent.

const (
	// clientLogPerMinute caps the entries one tab may send; a healthy page
	// sends a handful per screen change, so 60 is far above honest use.
	clientLogPerMinute = 60
	clientLogMaxBytes  = 8 << 10
	clientLogMaxBatch  = 20
	clientLogMaxField  = 64
	clientLogMaxMsg    = 300
)

// clientLogEntry is the allow-listed shape of one UI entry. Unknown fields
// are refused by the decoder, so a page (or anything else with the token)
// cannot smuggle extra data into the log through this route.
type clientLogEntry struct {
	Kind    string `json:"kind"`
	Screen  string `json:"screen"`
	Action  string `json:"action"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Stage   string `json:"stage"`
	ReqID   string `json:"reqId"`
}

type clientLogBatch struct {
	Entries []clientLogEntry `json:"entries"`
}

func validClientKind(kind string) bool {
	switch kind {
	case "nav", "action", "error":
		return true
	}
	return false
}

func capField(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// clientLog answers POST /api/client-log (session token required, like every
// /api/ route). Accepted entries are written at info, errors at warn.
func (h *handler) clientLog(w http.ResponseWriter, r *http.Request) {
	if !h.clientLogs.allow() {
		writeError(w, http.StatusTooManyRequests, "Too many log entries in the last minute.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, clientLogMaxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "Log entry exceeds 8 KiB.")
			return
		}
		writeError(w, http.StatusBadRequest, "Log entry could not be read.")
		return
	}
	// A batch is {"entries":[...]}; a single entry is the object itself.
	var entries []clientLogEntry
	var batch clientLogBatch
	if err := json.Unmarshal(raw, &batch); err == nil && batch.Entries != nil {
		entries = batch.Entries
	} else {
		var one clientLogEntry
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&one); err != nil {
			writeError(w, http.StatusBadRequest, "Log entry is invalid.")
			return
		}
		entries = []clientLogEntry{one}
	}
	if len(entries) == 0 || len(entries) > clientLogMaxBatch {
		writeError(w, http.StatusBadRequest, "Log entry batch is empty or too large.")
		return
	}
	for _, e := range entries {
		if !validClientKind(e.Kind) {
			writeError(w, http.StatusBadRequest, "Log entry kind is not recognised.")
			return
		}
	}
	for _, e := range entries {
		msg := applog.Sanitize(e.Message)
		if utf8.RuneCountInString(msg) > clientLogMaxMsg {
			msg = string([]rune(msg)[:clientLogMaxMsg])
		}
		attrs := []any{
			"src", "ui",
			"kind", e.Kind,
			"screen", capField(e.Screen, clientLogMaxField),
			"action", capField(e.Action, clientLogMaxField),
			"code", capField(e.Code, clientLogMaxField),
			"stage", capField(e.Stage, clientLogMaxField),
			"message", msg,
		}
		if e.ReqID != "" {
			attrs = append(attrs, "ui_req_id", capField(e.ReqID, 16))
		}
		if e.Kind == "error" {
			h.log().Warn("client log", attrs...)
		} else {
			h.log().Info("client log", attrs...)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// logsInfo answers GET /api/logs/info with where the log file is, its size
// and when its newest entry was written, for the Support logs section.
func (h *handler) logsInfo(w http.ResponseWriter, _ *http.Request) {
	var newest string
	info := applog.Stat(h.logDir)
	if !info.Newest.IsZero() {
		newest = info.Newest.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, struct {
		Path   string `json:"path"`
		Dir    string `json:"dir"`
		Size   int64  `json:"size"`
		Newest string `json:"newest"`
	}{info.Path, info.Dir, info.Size, newest}, 4096)
}
