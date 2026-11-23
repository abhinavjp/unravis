// Package clisrv is the owner side of the command line (spec 05-cli, 3.5): the routes /cli/v1/* that the dashboard process serves on the
// ag pipe. A call names an operation; the capability matrix (internal/clicap) decides, by deny by default, whether the command line of a
// person may run it; the operation runs through a Backend (today the same tool code as the MCP bridge, in this process).
//
// Who is calling is decided by the connection and nothing else. The pipe admits only the same Windows user in the same session with an
// Unravis image (internal/pipesec), and internal/pipesrv lets the thin command line image through to this handler. There is no token
// and no proof exchange (BL-318, option B: the locator carries none). The principal of every call here is the human command line;
// nothing in a request can name another (a "principal" field is an unknown field and is refused).
package clisrv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
)

// Limits of the wire (spec 3.5).
const (
	MaxRequestBytes  = 1 << 20
	MaxResponseBytes = 16 << 20
	MaxInFlight      = 4
	DefaultDeadline  = 30 * time.Second
	MaxDeadline      = time.Hour
)

// Backend runs one MCP tool for the human command line and returns its structured result. An error is already a command line error.
type Backend interface {
	Call(ctx context.Context, tool string, args json.RawMessage) (any, *clicore.Error)
}

// Info is what the owner says about itself in hello.
type Info struct {
	PID        int
	Gen        string // creation time of the process as the locator spells it (FILETIME, decimal)
	AppVersion string
	Owner      string // "app" until a headless owner exists
}

// Deps are the owner's parts. Backend, Info and Now are needed; State may be nil (always "ready").
type Deps struct {
	Backend Backend
	Info    func() Info
	State   func() string // "ready", "locked" or "updating"
	Now     func() time.Time
	Logf    func(format string, args ...any)
}

// Handler serves /cli/v1/hello, /cli/v1/op and /cli/v1/cancel.
func Handler(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logf == nil {
		d.Logf = log.Printf
	}
	s := &server{d: d, inflight: map[string]context.CancelFunc{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/cli/v1/hello", s.hello)
	mux.HandleFunc("/cli/v1/op", s.op)
	mux.HandleFunc("/cli/v1/cancel", s.cancel)
	return mux
}

type server struct {
	d        Deps
	mu       sync.Mutex
	inflight map[string]context.CancelFunc // callId -> cancel
	running  int
}

// helloBody is the answer of hello.
type helloBody struct {
	V          int       `json:"v"`
	PID        int       `json:"pid"`
	Gen        string    `json:"gen"`
	Proto      protoBody `json:"proto"`
	AppVersion string    `json:"appVersion"`
	Owner      string    `json:"owner"`
	State      string    `json:"state"`
}

type protoBody struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

func (s *server) hello(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	state := "ready"
	if s.d.State != nil {
		state = s.d.State()
	}
	switch state {
	case "ready", "locked", "updating":
	default:
		state = "ready"
	}
	i := s.d.Info()
	writeJSON(w, http.StatusOK, helloBody{V: 1, PID: i.PID, Gen: i.Gen, Proto: protoBody{clicore.ProtoMajor, clicore.ProtoMinor}, AppVersion: i.AppVersion, Owner: i.Owner, State: state})
}

// request is the body of POST /cli/v1/op.
type request struct {
	Op         string          `json:"op"`
	Args       json.RawMessage `json:"args"`
	RequestID  string          `json:"requestId"`
	CallID     string          `json:"callId"`
	DeadlineMs int64           `json:"deadlineMs"`
}

var (
	requestIDRE = regexp.MustCompile(`^req_[0-9a-f]{16}$`)
	callIDRE    = regexp.MustCompile(`^[0-9a-f-]{8,64}$`)
	opRE        = regexp.MustCompile(`^[a-z][a-z0-9.]{0,63}$`)
)

// Answer is the body of an op answer.
type answer struct {
	OK    bool           `json:"ok"`
	Data  any            `json:"data,omitempty"`
	Meta  *clicore.Meta  `json:"meta,omitempty"`
	Error *clicore.Error `json:"error,omitempty"`
}

func fail(e *clicore.Error) answer { return answer{OK: false, Error: e} }

func (s *server) op(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	// A request that came from a page in a browser is never ours.
	if r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		writeJSON(w, http.StatusOK, fail(clicore.NewError(clicore.CodeRequestTooLarge, "The request is larger than 1 MiB.", "")))
		return
	}
	var req request
	if e := decodeStrict(body, &req); e != nil {
		writeJSON(w, http.StatusOK, fail(e))
		return
	}
	if !requestIDRE.MatchString(req.RequestID) {
		writeJSON(w, http.StatusOK, fail(clicore.NewError(clicore.CodeInvalidArgument, "The request id is not valid.", "")))
		return
	}
	if req.CallID != "" && !callIDRE.MatchString(req.CallID) {
		writeJSON(w, http.StatusOK, fail(clicore.NewError(clicore.CodeInvalidArgument, "The call id is not valid.", "")))
		return
	}
	if !opRE.MatchString(req.Op) {
		writeJSON(w, http.StatusOK, fail(clicore.NewError(clicore.CodeUnknownOperation, "That is not an operation.", "")))
		return
	}
	a, status := s.run(r.Context(), req)
	if !a.OK && a.Error != nil {
		s.d.Logf("cli op=%s request=%s code=%s", req.Op, req.RequestID, a.Error.Code)
	}
	if status == 0 {
		status = http.StatusOK
	}
	writeJSON(w, status, a)
}

// run is the gate and the call. The gate comes first: an operation the matrix does not allow never reaches a handler.
func (s *server) run(parent context.Context, req request) (answer, int) {
	row, ok := clicap.Lookup(req.Op)
	if !ok {
		return fail(clicore.NewError(clicore.CodeUnknownOperation, "That is not an operation.", "Run 'unravis schema' to list the operations.")), 0
	}
	if allowed, _ := clicap.Gate(req.Op, clicap.CLIHuman); !allowed {
		return fail(clicore.NewError(clicore.CodePermissionDenied, "That operation is not available to the command line.", "")), 0
	}
	if len(req.Args) > 0 {
		if t := bytes.TrimSpace(req.Args); len(t) == 0 || t[0] != '{' {
			return fail(clicore.NewError(clicore.CodeInvalidArgument, "The arguments must be an object.", "")), 0
		}
		if dup, bad := duplicateKeys(req.Args); bad || dup {
			return fail(clicore.NewError(clicore.CodeInvalidArgument, "The arguments are not valid JSON, or repeat a name.", "")), 0
		}
	}
	args := req.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	deadline := time.Duration(req.DeadlineMs) * time.Millisecond
	if req.DeadlineMs <= 0 {
		deadline = DefaultDeadline
	}
	if deadline > MaxDeadline {
		deadline = MaxDeadline
	}
	ctx, cancel := context.WithTimeout(parent, deadline)
	defer cancel()
	if !s.begin(req.CallID, cancel) {
		return fail(clicore.NewError(clicore.CodeOwnerBusy, "Too many commands are running. Wait for one to finish.", "")), 0
	}
	defer s.end(req.CallID)

	var data any
	var cerr *clicore.Error
	switch {
	case row.Tool != "":
		data, cerr = s.d.Backend.Call(ctx, row.Tool, args)
	case req.Op == "app.status":
		data, cerr = s.appStatus(args)
	default:
		return fail(clicore.NewError(clicore.CodeUnknownOperation, "This operation is not built in this version of Unravis.", "Update Unravis.")), 0
	}
	if cerr != nil {
		return fail(cerr), 0
	}
	meta := metaOf(data)
	out := answer{OK: true, Data: data, Meta: &meta}
	// The size is checked here, so an answer the CLI would refuse is not sent at all.
	if b, err := json.Marshal(out); err != nil {
		return fail(clicore.NewError(clicore.CodeInternal, "The result could not be encoded.", "")), 0
	} else if len(b) > MaxResponseBytes {
		return fail(clicore.NewError(clicore.CodeResultTooLarge, "The result is larger than 16 MiB. Narrow the request.", "")), 0
	}
	return out, 0
}

func (s *server) begin(callID string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running >= MaxInFlight {
		return false
	}
	s.running++
	if callID != "" {
		s.inflight[callID] = cancel
	}
	return true
}

func (s *server) end(callID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running--
	if callID != "" {
		delete(s.inflight, callID)
	}
}

// cancel stops a running call by its id. Another process of the same user could cancel it too: the id is random and only the
// caller knows it, and the call is read-only.
func (s *server) cancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	var req struct {
		CallID string `json:"callId"`
	}
	if err != nil || decodeStrict(body, &req) != nil || !callIDRE.MatchString(req.CallID) {
		writeJSON(w, http.StatusBadRequest, fail(clicore.NewError(clicore.CodeInvalidArgument, "The call id is not valid.", "")))
		return
	}
	s.mu.Lock()
	c := s.inflight[req.CallID]
	s.mu.Unlock()
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]string{"state": "none"})
		return
	}
	c()
	writeJSON(w, http.StatusOK, map[string]string{"state": "cancelling"})
}

// appStatus answers app.status: what hello says, as data.
func (s *server) appStatus(args json.RawMessage) (any, *clicore.Error) {
	var none struct{}
	if e := decodeStrict(args, &none); e != nil {
		return nil, e
	}
	i := s.d.Info()
	state := "ready"
	if s.d.State != nil {
		state = s.d.State()
	}
	return map[string]any{"running": true, "owner": i.Owner, "pid": i.PID, "version": i.AppVersion, "state": state,
		"proto": protoBody{clicore.ProtoMajor, clicore.ProtoMinor}}, nil
}

// metaOf builds the meta of a success from the data. A source that did not answer makes the coverage partial. The identifiers of the
// human command line are shown (the operator policy of the app).
func metaOf(data any) clicore.Meta {
	m := clicore.Meta{Coverage: "complete", Identifiers: "shown"}
	b, err := json.Marshal(data)
	if err != nil {
		return m
	}
	var probe struct {
		Sources []struct {
			OK *bool `json:"ok"`
		} `json:"sources"`
	}
	if json.Unmarshal(b, &probe) == nil {
		for _, s := range probe.Sources {
			if s.OK != nil && !*s.OK {
				m.Coverage = "partial"
				m.Warnings = []string{"At least one source did not answer, so the result is partial."}
				break
			}
		}
	}
	return m
}

// decodeStrict decodes one JSON object and refuses unknown fields and trailing data.
func decodeStrict(b []byte, v any) *clicore.Error {
	bad := func() *clicore.Error {
		return clicore.NewError(clicore.CodeInvalidArgument, "The request is not valid JSON of the expected shape.", "")
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return bad()
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil {
		return bad()
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return bad()
	}
	return nil
}

// duplicateKeys reports whether any object in raw repeats a key (a request that says one thing twice is refused, spec 3.2). bad is true
// when raw is not valid JSON.
func duplicateKeys(raw []byte) (dup, bad bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	type frame struct {
		keys map[string]bool
		obj  bool
		key  bool // the next string of an object is a key
	}
	var stack []frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return dup, len(stack) != 0
		}
		if err != nil {
			return false, true
		}
		top := func() *frame {
			if len(stack) == 0 {
				return nil
			}
			return &stack[len(stack)-1]
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				stack = append(stack, frame{keys: map[string]bool{}, obj: true, key: true})
			case '[':
				stack = append(stack, frame{})
			default:
				stack = stack[:len(stack)-1]
				if t := top(); t != nil && t.obj {
					t.key = true
				}
			}
		case string:
			if t := top(); t != nil && t.obj && t.key {
				if t.keys[v] {
					dup = true
				}
				t.keys[v] = true
				t.key = false
				continue
			}
			if t := top(); t != nil && t.obj {
				t.key = true
			}
		default:
			if t := top(); t != nil && t.obj {
				t.key = true
			}
		}
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
