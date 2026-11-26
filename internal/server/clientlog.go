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
// or query text: the fields are fixed, the strings are capped, and every text
// field is sanitized and truncated here whatever the page sent.

const (
	// clientLogPerMinute caps the entries one tab may send; a healthy page
	// sends a handful per screen change, so 60 is far above honest use.
	clientLogPerMinute = 60
	clientLogMaxBytes  = 8 << 10
	clientLogMaxBatch  = 20
	clientLogMaxField  = 64
	clientLogMaxMsg    = 300
	// clientLogPreSanitize bounds the text handed to applog.Sanitize so its
	// regexp work never runs on an attacker-sized string. It is a multiple of
	// clientLogMaxMsg, not equal to it, so a secret that straddles the final cut
	// is still redacted whole before the cut.
	clientLogPreSanitize = clientLogMaxMsg * 4
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

// truncRunes returns s cut to at most n runes, unchanged when it already fits.
func truncRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}

// scrubField cuts s to a bounded length, passes it through applog.Sanitize and then cuts it to max runes. The first cut is a multiple
// of max, not max itself, so a secret that straddles the final cut is redacted whole before the cut, as for the message.
func scrubField(s string, max int) string {
	s = applog.Sanitize(truncRunes(s, max*4))
	return truncRunes(s, max)
}

// decodeStrict decodes one JSON value into v. A field v does not name is refused, and so is any text after the value.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
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
	// Both forms are strict. A batch with an unknown field (in the envelope or in an entry) fails the batch decode and then the single
	// decode, which does not know "entries", so it is refused.
	var batch clientLogBatch
	if err := decodeStrict(raw, &batch); err == nil && batch.Entries != nil {
		entries = batch.Entries
	} else {
		var one clientLogEntry
		if err := decodeStrict(raw, &one); err != nil {
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
		msg := applog.Sanitize(truncRunes(e.Message, clientLogPreSanitize))
		if utf8.RuneCountInString(msg) > clientLogMaxMsg {
			msg = string([]rune(msg)[:clientLogMaxMsg])
		}
		attrs := []any{
			"src", "ui",
			"kind", e.Kind,
			"screen", scrubField(e.Screen, clientLogMaxField),
			"action", scrubField(e.Action, clientLogMaxField),
			"code", scrubField(e.Code, clientLogMaxField),
			"stage", scrubField(e.Stage, clientLogMaxField),
			"message", msg,
		}
		if e.ReqID != "" {
			attrs = append(attrs, "ui_req_id", scrubField(e.ReqID, 16))
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
