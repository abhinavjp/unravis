package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"api-audit-explorer/internal/casestore"
)

// A case keeps a small opaque "workspace" note inside its encrypted case
// database: the web UI uses it to remember a non-secret reference to the case's
// Mongo connection and databases, and the last investigation filters. The server
// stores the JSON as sent (after checking it) and never interprets it.
//
//	GET /api/cases/{id}/workspace   200 the stored JSON object ({} when none)
//	PUT /api/cases/{id}/workspace   200 the stored JSON object; replaces the whole note
//
// PUT accepts one JSON object of at most casestore.MaxWorkspaceBlob bytes, nested
// at most workspaceMaxDepth levels, with strings of at most workspaceMaxString
// characters and no key (at any depth, any letter case or separators) that names
// a credential or an address (see workspaceForbiddenKeys). No object repeats a
// key, and no string value holds "://". An empty object clears the note.
//
// Answers: 400 "workspace-invalid" for a body that is not one JSON object or
// breaks a rule above (the message never repeats the input); 404 for an invalid
// or unknown case id; 409 "case-closed" for a write to a closed case (GET still
// works); 413 "workspace-too-large" for a body over the cap; 415 for a body
// that is not JSON; 423 while the master key is locked (before any handler
// runs); 503 in the demo, when cases are unavailable or when the case store
// cannot keep a workspace; 500 "workspace-damaged" when the stored note is not
// a JSON object (it is only replaced by a PUT).

const (
	workspaceMaxDepth  = 8
	workspaceMaxString = 512

	workspaceDemoText    = "A workspace is kept in a case, and cases are off in the demo."
	workspaceInvalidText = "The workspace must be one JSON object that holds no credentials or addresses."
	workspaceDepthText   = "The workspace is nested more deeply than allowed."
	workspaceStringText  = "A workspace value is longer than allowed."
	workspaceKeyText     = "The workspace holds a field that is not allowed. Only non-secret references are kept."
	workspaceTooBigText  = "The workspace is larger than a case can keep."
	workspaceClosedText  = "This case is closed. Its workspace can still be read but not changed."
)

// workspaceForbiddenKeys are compared after lower-casing a key and keeping only
// its ASCII letters and digits, so separators of any kind do not hide a name.
var workspaceForbiddenKeys = map[string]bool{
	"password": true, "passwd": true, "pwd": true, "pass": true, "secret": true,
	"token": true, "uri": true, "url": true, "host": true, "hostname": true,
	"address": true, "username": true, "user": true, "connectionstring": true,
	"connstr": true, "cacertificate": true, "cert": true, "certificate": true,
	"privatekey": true, "apikey": true, "credential": true, "credentials": true,
	"auth": true, "authsource": true,
}

// normaliseWorkspaceKey lower-cases k and keeps only [a-z0-9].
func normaliseWorkspaceKey(k string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(k) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type workspaceProblem struct{ text string }

func (p *workspaceProblem) Error() string { return p.text }

/* ---------------------------------------------------------------- plumbing */

// caseWorkspaceOf opens the case for a workspace request (same 404 rule as
// caseLedgerOf).
func (h *handler) caseWorkspaceOf(w http.ResponseWriter, r *http.Request, raw string) (casestore.Case, casestore.WorkspaceBlobStore, casestore.CaseID, bool) {
	if h.demo {
		writeError(w, http.StatusServiceUnavailable, workspaceDemoText)
		return nil, nil, "", false
	}
	if _, err := casestore.ParseID(raw); err != nil {
		if h.casesAvailable(w) {
			writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		}
		return nil, nil, "", false
	}
	id, c, ok := h.openCase(w, r, raw)
	if !ok {
		return nil, nil, "", false
	}
	var inner casestore.Case = c
	if sc, wrapped := c.(*sharedCase); wrapped {
		inner = sc.Case
	}
	wb, ok := inner.(casestore.WorkspaceBlobStore)
	if !ok {
		c.Close()
		writeCaseFailure(w, http.StatusServiceUnavailable, "workspace-unavailable", "This case store cannot keep a workspace.")
		return nil, nil, "", false
	}
	return c, wb, id, true
}

func writeWorkspaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, casestore.ErrCaseClosed):
		writeCaseFailure(w, http.StatusConflict, "case-closed", workspaceClosedText)
	case errors.Is(err, casestore.ErrCapExceeded):
		writeCaseFailure(w, http.StatusRequestEntityTooLarge, "workspace-too-large", workspaceTooBigText)
	case errors.Is(err, casestore.ErrHandleClosed), errors.Is(err, casestore.ErrNotFound):
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	default:
		writeStoreError(w, err)
	}
}

/* ---------------------------------------------------------------- validation */

// checkWorkspace reports why body is not an acceptable workspace, or nil. The
// returned text is fixed: it never repeats the input.
func checkWorkspace(body []byte) *workspaceProblem {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return &workspaceProblem{workspaceInvalidText}
	}
	if p := walkWorkspaceObject(dec, 1); p != nil {
		return p
	}
	if _, err := dec.Token(); err != io.EOF {
		return &workspaceProblem{workspaceInvalidText}
	}
	return nil
}

// walkWorkspaceObject reads the members of an object whose '{' was just read and
// its closing '}'. It uses the token reader so that a repeated key at any depth
// is seen (decoding into a map would keep only the last one).
func walkWorkspaceObject(dec *json.Decoder, depth int) *workspaceProblem {
	if depth > workspaceMaxDepth {
		return &workspaceProblem{workspaceDepthText}
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			return &workspaceProblem{workspaceInvalidText}
		}
		if utf8.RuneCountInString(key) > workspaceMaxString {
			return &workspaceProblem{workspaceStringText}
		}
		if seen[key] || workspaceForbiddenKeys[normaliseWorkspaceKey(key)] {
			return &workspaceProblem{workspaceKeyText}
		}
		seen[key] = true
		if p := walkWorkspaceValue(dec, depth+1); p != nil {
			return p
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return &workspaceProblem{workspaceInvalidText}
	}
	return nil
}

func walkWorkspaceArray(dec *json.Decoder, depth int) *workspaceProblem {
	if depth > workspaceMaxDepth {
		return &workspaceProblem{workspaceDepthText}
	}
	for dec.More() {
		if p := walkWorkspaceValue(dec, depth+1); p != nil {
			return p
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return &workspaceProblem{workspaceInvalidText}
	}
	return nil
}

// walkWorkspaceValue reads one value that sits at the given depth.
func walkWorkspaceValue(dec *json.Decoder, depth int) *workspaceProblem {
	tok, err := dec.Token()
	if err != nil {
		return &workspaceProblem{workspaceInvalidText}
	}
	if depth > workspaceMaxDepth {
		return &workspaceProblem{workspaceDepthText}
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			return walkWorkspaceObject(dec, depth)
		}
		if t == '[' {
			return walkWorkspaceArray(dec, depth)
		}
		return &workspaceProblem{workspaceInvalidText}
	case string:
		if utf8.RuneCountInString(t) > workspaceMaxString {
			return &workspaceProblem{workspaceStringText}
		}
		if strings.Contains(t, "://") {
			// A pasted address or connection string.
			return &workspaceProblem{workspaceInvalidText}
		}
	}
	return nil
}

/* ---------------------------------------------------------------- handlers */

func (h *handler) caseWorkspaceGet(w http.ResponseWriter, r *http.Request, raw string) {
	c, wb, _, ok := h.caseWorkspaceOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	b, err := wb.WorkspaceBlob(ctx)
	if err != nil {
		writeWorkspaceError(w, err)
		return
	}
	h.writeWorkspace(w, http.StatusOK, b)
}

func (h *handler) caseWorkspacePut(w http.ResponseWriter, r *http.Request, raw string) {
	c, wb, id, ok := h.caseWorkspaceOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, casestore.MaxWorkspaceBlob)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeCaseFailure(w, http.StatusRequestEntityTooLarge, "workspace-too-large", workspaceTooBigText)
		} else {
			writeCaseFailure(w, http.StatusBadRequest, "workspace-invalid", workspaceInvalidText)
		}
		return
	}
	if p := checkWorkspace(body); p != nil {
		writeCaseFailure(w, http.StatusBadRequest, "workspace-invalid", p.text)
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		writeCaseFailure(w, http.StatusBadRequest, "workspace-invalid", workspaceInvalidText)
		return
	}
	stored := compact.Bytes()
	if bytes.Equal(stored, []byte("{}")) {
		stored = nil // an empty object clears the note
	}

	mu := h.wsLocks.of(id)
	mu.Lock()
	defer mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := wb.SetWorkspaceBlob(ctx, stored); err != nil {
		writeWorkspaceError(w, err)
		return
	}
	h.event(r, "settings_changed", "setting", "case_workspace")
	h.writeWorkspace(w, http.StatusOK, stored)
}

// writeWorkspace answers with the stored object, or {} when there is none. A
// stored note that is not a JSON object is reported, never passed on.
func (h *handler) writeWorkspace(w http.ResponseWriter, status int, b []byte) {
	if len(b) == 0 {
		b = []byte("{}")
	} else if checkWorkspaceShape(b) != nil {
		writeCaseFailure(w, http.StatusInternalServerError, "workspace-damaged", "This case's workspace could not be read. It was left as it is.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(append([]byte(nil), b...), '\n'))
}

// checkWorkspaceShape only checks that the stored bytes are one JSON object.
func checkWorkspaceShape(b []byte) *workspaceProblem {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		return &workspaceProblem{workspaceInvalidText}
	}
	return nil
}
