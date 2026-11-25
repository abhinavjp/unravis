package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// The error codes of a tool call, the vocabulary of spec 03 that the command line maps (clicore.FromMCP). The standard-input bridge
// only shows the message; the owner side of the command line (internal/clisrv) reads the code.
const (
	CodeInvalidArguments   = "invalid_arguments"
	CodeResultExpired      = "result_expired"
	CodeBudgetExceeded     = "query_budget_exceeded"
	CodeCancelled          = "cancelled"
	CodeDeadlineExceeded   = "deadline_exceeded"
	CodeResultTooLarge     = "result_too_large"
	CodeBackendUnavailable = "backend_unavailable"
	CodeNotFound           = "not_found"
	CodeLocked             = "locked"
	CodeBusy               = "busy"
	CodeInternal           = "internal"
)

// CodedError is an error with a stable code next to its message.
type CodedError struct {
	Code string
	Msg  string
	// Wire, when set, is the code a client of the MCP server sees instead of the one mapped from Code (see wireCode). Code stays what the
	// command line reads, so a refusal can be permission_required on the wire and invalid_arguments there.
	Wire string
}

func (e *CodedError) Error() string { return e.Msg }

func coded(code, msg string) error { return &CodedError{Code: code, Msg: msg} }

// codedWire is coded with a different code on the MCP wire.
func codedWire(code, wire, msg string) error { return &CodedError{Code: code, Msg: msg, Wire: wire} }

// Code returns the code of a tool-call error. An error without one is an argument problem (every other failure of a call is coded where
// it is made), and a context error is a cancel or a deadline.
func Code(err error) string {
	var ce *CodedError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &ce):
		return ce.Code
	case errors.Is(err, context.DeadlineExceeded):
		return CodeDeadlineExceeded
	case errors.Is(err, context.Canceled):
		return CodeCancelled
	}
	return CodeInvalidArguments
}

// Call runs one tool by name with its JSON arguments and returns the structured result, as `tools/call` does, but with the error kept
// as an error (see Code). It is the entry of the command line's owner side.
func (b *Bridge) Call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	return b.call(ctx, name, args)
}

// NewLocal builds a bridge that calls the dashboard handler in this process, with no network and no session file: for the owner side of
// the command line, which runs inside the dashboard process. token is the dashboard's own session token. Identifiers (user ids, user
// agents, IP addresses) follow the option; the command line of a person passes WithIdentifiers(true), the operator policy of the app.
func NewLocal(handler http.Handler, token string, opts ...Option) *Bridge {
	client := &http.Client{Transport: localTransport{handler}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	return newBridge(func() (string, string, error) { return "http://127.0.0.1", token, nil }, client, opts...)
}

// localTransport sends a request straight to a handler.
type localTransport struct{ h http.Handler }

func (t localTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	// A handler that panics must not take the owner down through this call: the panic becomes an internal error.
	defer func() {
		if p := recover(); p != nil {
			resp, err = nil, coded(CodeInternal, "the dashboard handler failed")
		}
	}()
	r := req.Clone(req.Context())
	r.RemoteAddr = "127.0.0.1:0"
	r.RequestURI = r.URL.RequestURI()
	r.Host = r.URL.Host
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	t.h.ServeHTTP(rec, r)
	if err := req.Context().Err(); err != nil {
		return nil, err // the call was cancelled while the handler worked; the answer is not wanted
	}
	hdr := rec.header.Clone()
	return &http.Response{
		Status:        http.StatusText(rec.status),
		StatusCode:    rec.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        hdr,
		Body:          readCloser{bytes.NewReader(rec.body.Bytes())},
		ContentLength: int64(rec.body.Len()),
		Request:       req,
	}, nil
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

// recorder is the smallest http.ResponseWriter that keeps what a handler wrote. The body is capped a little above maxBody, so a
// handler that writes without end cannot fill memory: boundedBody then refuses it.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
}
func (r *recorder) Write(p []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	if room := maxBody + 1 - r.body.Len(); room > 0 {
		if len(p) > room {
			r.body.Write(p[:room])
		} else {
			r.body.Write(p)
		}
	}
	return len(p), nil
}
