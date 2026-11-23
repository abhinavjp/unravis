package clicore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/pipesec"
)

// Limits of the wire (spec 05 3.5): the owner refuses more than MaxRequestBytes; an answer above MaxResponseBytes is refused here.
const (
	MaxRequestBytes  = 1 << 20
	MaxResponseBytes = 16 << 20
	maxMessageBytes  = 600
)

// Attacher finds the running owner and talks to it. The fields are the seams of the platform: production sets them in DefaultAttacher.
type Attacher struct {
	// RunDir is the folder of the locator file.
	RunDir func() (string, error)
	// Prober answers the process and pipe questions of locator.Alive.
	Prober locator.Prober
	// Dial opens a verified connection to the owner's ag pipe: the server end must be the process the locator names and the app program in
	// the install folder (the checks of spec 05 3.5 without a token). It returns a pipesec error for a failed check.
	Dial func(ctx context.Context, rec locator.Record) (net.Conn, error)
	// UserAgent is sent on every request ("unravis-cli/<version>").
	UserAgent string
}

// Session is an attached command line: the owner answered hello, and the checks passed.
type Session struct {
	a     Attacher
	rec   locator.Record
	Hello Hello
}

// Hello is what the owner says about itself (spec 05 3.5).
type Hello struct {
	V          int    `json:"v"`
	PID        int    `json:"pid"`
	Gen        string `json:"gen"`
	AppVersion string `json:"appVersion"`
	Owner      string `json:"owner"`
	State      string `json:"state"`
	Proto      struct {
		Major int `json:"major"`
		Minor int `json:"minor"`
	} `json:"proto"`
}

// Attach finds the owner through the locator, opens a verified connection and checks hello. Nothing but hello is sent before the checks
// pass. Every failure is a command line error with the code of spec 05 3.8.
func (a Attacher) Attach(ctx context.Context) (*Session, *Error) {
	if a.RunDir == nil || a.Prober == nil || a.Dial == nil {
		return nil, NewError(CodeEndpointUnavailable, "The command line cannot reach Unravis on this system.", "")
	}
	dir, err := a.RunDir()
	if err != nil {
		return nil, NewError(CodeAppNotRunning, "Unravis is not running. Start the app and try again.", "")
	}
	rec, err := locator.Read(locator.FilePath(dir, false))
	switch {
	case errors.Is(err, locator.ErrNoLocator):
		return nil, NewError(CodeAppNotRunning, "Unravis is not running. Start the app and try again.", "")
	case errors.Is(err, locator.ErrUnknownSchema):
		return nil, NewError(CodeVersionIncompatible, "Update Unravis: this command line is older than the running app.", "")
	case err != nil:
		return nil, NewError(CodeEndpointUnavailable, "The file that says where Unravis runs could not be read.", "Restart Unravis.")
	}
	if !locator.Alive(rec, a.Prober) {
		return nil, NewError(CodeStaleLocator, "Unravis is not running (the last session ended). Start the app and try again.", "")
	}
	s := &Session{a: a, rec: rec}
	body, _, derr := s.get(ctx, "/cli/v1/hello")
	if derr != nil {
		return nil, derr
	}
	if e := s.checkHello(body); e != nil {
		return nil, e
	}
	return s, nil
}

// dialError maps what a failed dial says to a code.
func dialError(err error) *Error {
	switch {
	case errors.Is(err, pipesec.ErrServerMismatch):
		return NewError(CodeEndpointUntrusted, "Unravis did not prove who it is. Restart Unravis.", "")
	case errors.Is(err, pipesec.ErrPeerDenied):
		return NewError(CodePermissionDenied, "Unravis refused this command line.", "Run it as the same Windows user, from the Unravis folder.")
	case errors.Is(err, pipesec.ErrPipeBusy):
		return NewError(CodeOwnerBusy, "Unravis is busy. Try again in a moment.", "")
	case errors.Is(err, context.DeadlineExceeded):
		return NewError(CodeTimeout, "Unravis did not answer in time.", "")
	case errors.Is(err, context.Canceled):
		return NewError(CodeCanceled, "Canceled.", "")
	}
	return NewError(CodeEndpointUnavailable, "Unravis could not be reached.", "Restart Unravis.")
}

// get sends a GET and reads the body (at most MaxResponseBytes).
func (s *Session) get(ctx context.Context, path string) ([]byte, int, *Error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	return s.send(req, false)
}

// send does one request on its own verified connection (one request per connection: there is nothing to reuse and nothing to
// poison). It writes the request and reads one answer by hand, so the program does not link an HTTP client. afterWork is true once the
// request body may have reached the owner (a failure then is never retryable).
func (s *Session) send(req *http.Request, afterWork bool) ([]byte, int, *Error) {
	ctx := req.Context()
	conn, err := s.a.Dial(ctx, s.rec)
	if err != nil {
		return nil, 0, dialError(unwrapAll(err))
	}
	defer func() { _ = conn.Close() }()
	// A ctx that ends closes the connection, which ends the read or write at once.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	req.Header.Set("User-Agent", s.a.UserAgent)
	req.Header.Set("Connection", "close")
	req.Host = "localhost"
	failed := func() *Error {
		if ctx.Err() != nil {
			return dialError(ctx.Err())
		}
		if afterWork {
			// the owner went away while it was working: the work may have run, so no repeat is safe
			return NewErrorAfterWork(CodeUpstreamUnavailable, "Unravis stopped answering during the command.", "Check that Unravis is running, then run the command again.")
		}
		return NewError(CodeEndpointUnavailable, "Unravis could not be reached.", "Restart Unravis.")
	}
	if err := req.Write(conn); err != nil {
		return nil, 0, failed()
	}
	res, err := http.ReadResponse(bufio.NewReaderSize(conn, 32<<10), req)
	if err != nil {
		return nil, 0, failed()
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, 0, failed()
	}
	if len(b) > MaxResponseBytes {
		return nil, 0, NewErrorAfterWork(CodeResultTooLarge, "The answer is larger than 16 MiB. Narrow the request.", "")
	}
	return b, res.StatusCode, nil
}

// unwrapAll returns the innermost error that dialError can classify (net/http wraps a dial error in url.Error and net.OpError).
func unwrapAll(err error) error {
	for _, target := range []error{pipesec.ErrServerMismatch, pipesec.ErrPeerDenied, pipesec.ErrPipeBusy, context.DeadlineExceeded, context.Canceled} {
		if errors.Is(err, target) {
			return target
		}
	}
	return err
}

// checkHello is the whole verification of the owner's hello: the process, the generation and the protocol.
func (s *Session) checkHello(body []byte) *Error {
	untrusted := NewError(CodeEndpointUntrusted, "Unravis did not prove who it is. Restart Unravis.", "")
	var h Hello
	dec := json.NewDecoder(bytes.NewReader(body))
	if dec.Decode(&h) != nil || h.V != 1 {
		return untrusted
	}
	if h.PID != s.rec.PID {
		return untrusted
	}
	gen, err := time.Parse(time.RFC3339Nano, h.Gen)
	if err != nil {
		return untrusted
	}
	if d := gen.Sub(s.rec.StartTime); d > time.Second || d < -time.Second {
		return untrusted
	}
	if h.Proto.Major != ProtoMajor || h.Proto.Minor < ProtoMinor {
		return NewError(CodeVersionIncompatible, "Update Unravis: this command line does not match the running app.", "")
	}
	switch h.State {
	case "ready", "locked", "updating":
	default:
		return untrusted
	}
	s.Hello = h
	return nil
}

// Call is one operation.
type Call struct {
	Op        string
	Args      json.RawMessage // a JSON object; nil means {}
	RequestID string
	CallID    string // random, for cancel
	Deadline  time.Duration
}

// Result is a success of an operation.
type Result struct {
	Data json.RawMessage
	Meta Meta
}

type wireRequest struct {
	Op         string          `json:"op"`
	Args       json.RawMessage `json:"args"`
	RequestID  string          `json:"requestId"`
	CallID     string          `json:"callId,omitempty"`
	DeadlineMs int64           `json:"deadlineMs"`
}

type wireAnswer struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Meta  Meta            `json:"meta"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Action  string `json:"action"`
	} `json:"error"`
}

// Do runs an operation. The state of the owner decides first: an owner in an update or locked refuses everything but app.status. When
// ctx ends while the call runs, the owner is told to cancel (best effort) and the error is CANCELED or TIMEOUT.
func (s *Session) Do(ctx context.Context, c Call) (*Result, *Error) {
	if c.Op != "app.status" {
		switch s.Hello.State {
		case "updating":
			return nil, NewError(CodeUpdateInProgress, "Unravis is being updated. Try again in a minute.", "")
		case "locked":
			return nil, NewError(CodeCaseLocked, "Unravis is locked. Unlock it in the app.", "")
		}
	}
	args := c.Args
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(wireRequest{Op: c.Op, Args: args, RequestID: c.RequestID, CallID: c.CallID, DeadlineMs: c.Deadline.Milliseconds()})
	if err != nil {
		return nil, NewError(CodeInternal, "The request could not be built.", "")
	}
	if len(body) > MaxRequestBytes {
		return nil, NewError(CodeRequestTooLarge, "The request is larger than 1 MiB.", "")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/cli/v1/op", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	b, status, e := s.send(req, true)
	if e != nil {
		if ctx.Err() != nil && c.CallID != "" {
			s.cancel(c.CallID)
		}
		return nil, e
	}
	if status != http.StatusOK {
		return nil, NewErrorAfterWork(CodeInternal, "Unravis answered in a way this command line does not understand (HTTP "+strconv.Itoa(status)+").", "Update Unravis.")
	}
	var a wireAnswer
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, NewErrorAfterWork(CodeInternal, "Unravis answered in a way this command line does not understand.", "Update Unravis.")
	}
	if !a.OK {
		if a.Error == nil {
			return nil, NewErrorAfterWork(CodeInternal, "Unravis failed without saying why.", "")
		}
		return nil, NewErrorAfterWork(ParseCode(a.Error.Code), clip(a.Error.Message), clip(a.Error.Action))
	}
	if a.Meta.Coverage != "complete" && a.Meta.Coverage != "partial" {
		a.Meta.Coverage = "complete"
	}
	if a.Meta.Identifiers != "hidden" && a.Meta.Identifiers != "shown" {
		a.Meta.Identifiers = "hidden"
	}
	if a.Meta.Warnings == nil {
		a.Meta.Warnings = []string{}
	}
	for i, w := range a.Meta.Warnings {
		a.Meta.Warnings[i] = clip(w)
	}
	return &Result{Data: a.Data, Meta: a.Meta}, nil
}

// cancel tells the owner to stop a call. It is best effort with its own short deadline, since the caller's context has ended.
func (s *Session) cancel(callID string) {
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	body, _ := json.Marshal(map[string]string{"callId": callID})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/cli/v1/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	_, _, _ = s.send(req, false)
}

// ParseCode reads a code name of the owner. A name that is not in the table is INTERNAL (the output contract does the same).
func ParseCode(name string) Code {
	for _, c := range Codes() {
		if string(c) == name {
			return c
		}
	}
	return CodeInternal
}

// clip bounds text from the owner, which is untrusted.
func clip(s string) string {
	if len(s) > maxMessageBytes {
		s = s[:maxMessageBytes]
	}
	return strings.TrimSpace(s)
}
