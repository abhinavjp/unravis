package keeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Handler runs one operation. It returns the result to encode, or an error (a *WireError, or a store/driver error that classify turns
// into one). A streaming handler calls call.Stream before returning; its result is then the stream's header.
type Handler func(ctx context.Context, call *Call) (any, error)

// StreamFunc writes the body of a streaming answer.
type StreamFunc func(ctx context.Context, w io.Writer) error

// Call is one request as a handler sees it.
type Call struct {
	Caller Caller
	Op     string
	Args   json.RawMessage
	// Record is the saved record the call touched, for the audit log. Handlers set it.
	Record string
	stream StreamFunc
}

// Decode reads the arguments strictly: unknown fields are refused.
func (c *Call) Decode(v any) error {
	if len(c.Args) == 0 {
		c.Args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(c.Args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &WireError{Code: CodeInvalid, Message: "The request to the key keeper was not understood."}
	}
	return nil
}

// Stream makes the answer a stream written by f.
func (c *Call) Stream(f StreamFunc) { c.stream = f }

type opEntry struct {
	class   string
	timeout time.Duration
	h       Handler
}

// ServerOptions configures a Server.
type ServerOptions struct {
	Log         *AuditLog
	Limiter     *RateLimiter
	MaxConns    int           // 0 means 16
	ReadTimeout time.Duration // time allowed for the request frame; 0 means 10 s
	Now         func() time.Time
}

// Server answers keeper requests.
type Server struct {
	mu      sync.RWMutex
	ops     map[string]opEntry
	log     *AuditLog
	limit   *RateLimiter
	sem     chan struct{}
	readTO  time.Duration
	now     func() time.Time
	started time.Time
}

// NewServer makes a server with ping and status registered.
func NewServer(o ServerOptions) *Server {
	if o.MaxConns <= 0 {
		o.MaxConns = 16
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = 10 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Server{ops: map[string]opEntry{}, log: o.Log, limit: o.Limiter, sem: make(chan struct{}, o.MaxConns), readTO: o.ReadTimeout, now: o.Now}
	s.started = o.Now()
	s.Handle(OpPing, classRead, 5*time.Second, func(context.Context, *Call) (any, error) {
		return map[string]any{"v": ProtocolVersion}, nil
	})
	s.Handle(OpStatus, classRead, 5*time.Second, func(context.Context, *Call) (any, error) {
		return map[string]any{"v": ProtocolVersion, "since": s.started.UTC()}, nil
	})
	return s
}

// Handle registers an operation. This is the extension point: P14b registers the audit.* operations here (the keeper hosting
// internal/store behind the pipe), with class "reach" for opening a source and a streaming handler for query results. An operation
// registered here must never return a saved secret.
func (s *Server) Handle(op, class string, timeout time.Duration, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops[op] = opEntry{class: class, timeout: timeout, h: h}
}

// Serve accepts connections until ctx ends or the listener fails.
func (s *Server) Serve(ctx context.Context, l Listener) error {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrListenerClosed) {
				return nil
			}
			return err
		}
		select {
		case s.sem <- struct{}{}:
		default:
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.refuseBusy(c)
			}()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.sem }()
			s.serveConn(ctx, c)
		}()
	}
}

func (s *Server) refuseBusy(c Conn) {
	defer c.Close()
	_ = c.SetDeadline(s.now().Add(s.readTO))
	if _, err := ReadFrame(c); err != nil {
		return
	}
	_ = writeJSONFrame(c, Response{Error: &WireError{Code: CodeBusy, Message: "The key keeper is busy. Try again in a few seconds."}})
	drain(c, s.now)
}

func (s *Server) serveConn(ctx context.Context, c Conn) {
	defer c.Close()
	_ = c.SetDeadline(s.now().Add(s.readTO))
	frame, err := ReadFrame(c)
	if err != nil {
		return
	}
	caller, err := c.Identify()
	if err != nil || caller.SID == "" {
		s.log.Write(AuditEntry{Op: "?", Outcome: CodeRefused, PID: caller.PID, Session: caller.Session})
		_ = writeJSONFrame(c, Response{Error: &WireError{Code: CodeRefused, Message: "The key keeper could not tell who is asking."}})
		drain(c, s.now)
		return
	}
	var req Request
	if err := json.Unmarshal(frame, &req); err != nil || req.Op == "" {
		s.finish(c, caller, "?", "", nil, &WireError{Code: CodeInvalid, Message: "The request to the key keeper was not understood."})
		return
	}
	if req.V != ProtocolVersion {
		s.finish(c, caller, req.Op, "", nil, &WireError{Code: CodeVersion, Message: "This Unravis and the key keeper are different versions. Install the same version of both."})
		return
	}
	s.mu.RLock()
	op, ok := s.ops[req.Op]
	s.mu.RUnlock()
	if !ok {
		s.finish(c, caller, req.Op, "", nil, &WireError{Code: CodeUnknownOp, Message: "The key keeper does not know that request."})
		return
	}
	if !s.limit.Allow(caller.SID, op.class) {
		s.finish(c, caller, req.Op, "", nil, &WireError{Code: CodeRateLimited, Message: "Too many requests to the key keeper in the last minute. Wait a minute, then try again."})
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, op.timeout)
	defer cancel()
	_ = c.SetDeadline(s.now().Add(op.timeout + 10*time.Second))
	call := &Call{Caller: caller, Op: req.Op, Args: req.Args}
	result, herr := runHandler(opCtx, op.h, call)
	s.finishCall(opCtx, c, caller, call, result, herr)
}

func runHandler(ctx context.Context, h Handler, call *Call) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, &WireError{Code: CodeInternal, Message: "The key keeper failed on that request."}
		}
	}()
	return h(ctx, call)
}

func (s *Server) finish(c Conn, caller Caller, op, record string, result any, werr *WireError) {
	call := &Call{Caller: caller, Op: op, Record: record}
	var err error
	if werr != nil {
		err = werr
	}
	s.finishCall(context.Background(), c, caller, call, result, err)
}

func (s *Server) finishCall(ctx context.Context, c Conn, caller Caller, call *Call, result any, err error) {
	resp := Response{OK: err == nil}
	outcome := "ok"
	if err != nil {
		resp.Error = classify(err)
		outcome = resp.Error.Code
	} else {
		raw, merr := json.Marshal(result)
		if merr != nil {
			resp = Response{Error: &WireError{Code: CodeInternal, Message: "The key keeper could not prepare the answer."}}
			outcome = CodeInternal
		} else {
			resp.Result = raw
			resp.Stream = call.stream != nil
		}
	}
	werr := writeJSONFrame(c, resp)
	if werr == nil && resp.OK && resp.Stream {
		if serr := writeStream(ctx, c, call.stream); serr != nil {
			outcome = "stream-failed"
		}
	}
	s.log.Write(AuditEntry{SID: caller.SID, Name: caller.Name, PID: caller.PID, Session: caller.Session, Op: call.Op, Record: call.Record, Outcome: outcome})
	if werr == nil {
		drain(c, s.now)
	}
}

// drain waits (briefly) for the client to close after reading the answer, so nothing written is lost when the pipe is torn down.
func drain(c Conn, now func() time.Time) {
	_ = c.SetDeadline(now().Add(5 * time.Second))
	_, _ = io.Copy(io.Discard, io.LimitReader(c, 64<<10))
}

type chunkWriter struct {
	w io.Writer
}

func (cw chunkWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > MaxFrame-1 {
			n = MaxFrame - 1
		}
		buf := make([]byte, 1+n)
		buf[0] = chunkData
		copy(buf[1:], p[:n])
		if err := WriteFrame(cw.w, buf); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func writeStream(ctx context.Context, w io.Writer, f StreamFunc) error {
	if err := f(ctx, chunkWriter{w: w}); err != nil {
		b, _ := json.Marshal(classify(err))
		_ = WriteFrame(w, append([]byte{chunkErr}, b...))
		return err
	}
	return WriteFrame(w, []byte{chunkEnd})
}
