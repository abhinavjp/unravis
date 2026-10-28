package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/sqlconn"
)

// ErrKeeperHeld answers any request for a saved secret on the Unravis side: the key keeper holds it and has no call that gives it out.
var ErrKeeperHeld = errors.New("the key keeper holds this secret and never gives it out")

// defaultCallTimeout bounds a call whose context has no deadline.
const defaultCallTimeout = 3 * time.Minute

// Client talks to the key keeper, one connection per call.
type Client struct {
	Dialer Dialer
}

// NewClient makes a client over d.
func NewClient(d Dialer) *Client { return &Client{Dialer: d} }

func (c *Client) start(ctx context.Context, op string, args any) (ClientConn, Response, func(), error) {
	if c == nil || c.Dialer == nil {
		return nil, Response{}, nil, ErrNotRunning
	}
	conn, err := c.Dialer.Dial(ctx)
	if err != nil {
		return nil, Response{}, nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultCallTimeout)
	}
	_ = conn.SetDeadline(deadline)
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(done)
			_ = conn.Close()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	raw := json.RawMessage("{}")
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			stop()
			return nil, Response{}, nil, err
		}
		raw = b
	}
	if err := writeJSONFrame(conn, Request{V: ProtocolVersion, Op: op, Args: raw}); err != nil {
		stop()
		return nil, Response{}, nil, transportErr(ctx, err)
	}
	frame, err := ReadFrame(conn)
	if err != nil {
		stop()
		return nil, Response{}, nil, transportErr(ctx, err)
	}
	var resp Response
	if err := json.Unmarshal(frame, &resp); err != nil {
		stop()
		return nil, Response{}, nil, &WireError{Code: CodeInternal, Message: "The key keeper's answer was not understood."}
	}
	if !resp.OK {
		stop()
		if resp.Error == nil {
			resp.Error = &WireError{Code: CodeInternal, Message: "The key keeper failed on that request."}
		}
		return nil, resp, nil, resp.Error
	}
	return conn, resp, stop, nil
}

// errTransport is a keeper that was reached but dropped the call.
var errTransport = errors.New("the connection to the key keeper dropped")

func transportErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(errTransport, err)
}

// Call runs op and decodes its result into out (which may be nil).
func (c *Client) Call(ctx context.Context, op string, args, out any) error {
	_, resp, stop, err := c.start(ctx, op, args)
	if err != nil {
		return err
	}
	defer stop()
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return &WireError{Code: CodeInternal, Message: "The key keeper's answer was not understood."}
		}
	}
	return nil
}

// Open runs a streaming op: out receives the head, and the returned reader yields the body. A nil reader means the answer had no body.
func (c *Client) Open(ctx context.Context, op string, args, out any) (io.ReadCloser, error) {
	conn, resp, stop, err := c.start(ctx, op, args)
	if err != nil {
		return nil, err
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			stop()
			return nil, &WireError{Code: CodeInternal, Message: "The key keeper's answer was not understood."}
		}
	}
	if !resp.Stream {
		stop()
		return nil, nil
	}
	return &streamReader{conn: conn, stop: stop, ctx: ctx}, nil
}

// Ping reports whether the keeper answers.
func (c *Client) Ping(ctx context.Context) error {
	return c.Call(ctx, OpPing, nil, nil)
}

type streamReader struct {
	conn ClientConn
	stop func()
	ctx  context.Context
	buf  []byte
	err  error
}

func (s *streamReader) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		frame, err := ReadFrame(s.conn)
		if err != nil {
			s.err = transportErr(s.ctx, err)
			if errors.Is(err, io.EOF) && s.ctx.Err() == nil {
				s.err = errors.Join(errTransport, io.ErrUnexpectedEOF)
			}
			return 0, s.err
		}
		if len(frame) == 0 {
			s.err = errors.Join(errTransport, io.ErrUnexpectedEOF)
			return 0, s.err
		}
		switch frame[0] {
		case chunkData:
			s.buf = frame[1:]
		case chunkEnd:
			s.err = io.EOF
		case chunkErr:
			var we WireError
			if json.Unmarshal(frame[1:], &we) != nil || we.Code == "" {
				we = WireError{Code: CodeInternal, Message: "The key keeper failed on that request."}
			}
			s.err = &we
		default:
			s.err = errors.Join(errTransport, io.ErrUnexpectedEOF)
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *streamReader) Close() error {
	s.stop()
	return nil
}

/* --------------------------------------------- errors back to native form */

type storeKind int

const (
	kindSQL storeKind = iota
	kindMongo
	kindBB
)

// nativeError carries a keeper answer as the error the local store would have returned, so the existing HTTP handlers word it the same
// way. errors.As still finds the *WireError.
type nativeError struct {
	native error
	wire   *WireError
}

func (e *nativeError) Error() string   { return e.native.Error() }
func (e *nativeError) Unwrap() []error { return []error{e.native, e.wire} }

func native(err error, kind storeKind) error {
	var we *WireError
	if !errors.As(err, &we) {
		return err
	}
	pick := func(sql, mongo, bb error) error {
		switch kind {
		case kindSQL:
			return sql
		case kindMongo:
			return mongo
		}
		return bb
	}
	var n error
	switch we.Code {
	case CodeNotFound:
		n = pick(sqlconn.ErrNotFound, history.ErrNotFound, bbsaved.ErrNotFound)
	case CodeFull:
		n = pick(sqlconn.ErrFull, history.ErrFull, bbsaved.ErrTooMany)
	case CodeInvalid:
		n = pick(sqlconn.ErrInvalid, audit.ErrInvalidInput, bbsaved.ErrInvalid)
	case CodeDuplicate:
		n = bbsaved.ErrDuplicate
	case CodeCorrupt:
		n = pick(sqlconn.ErrCorrupt, history.ErrCorrupt, bbsaved.ErrCorrupt)
	case CodeUnavailable:
		n = pick(sqlconn.ErrUnavailable, history.ErrUnavailable, bbsaved.ErrUnavailable)
	case CodeMismatch:
		n = audit.ErrInvalidInput
	case CodeConnect:
		var d struct{ Code, Message string }
		_ = json.Unmarshal(we.Detail, &d)
		if d.Code == "" {
			d.Code, d.Message = "unreachable", we.Message
		}
		n = &dbhealth.ConnectError{Code: d.Code, Message: d.Message}
	case CodeMongoConnect:
		var d audit.ConnectionError
		_ = json.Unmarshal(we.Detail, &d)
		if d.Message == "" {
			d.Message = we.Message
		}
		n = &d
	case CodeBBRead:
		var d struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(we.Detail, &d)
		if d.Kind == "" {
			d.Kind = string(blackbox.ReadUnreachable)
		}
		if d.Message == "" {
			d.Message = we.Message
		}
		n = &blackbox.ReadError{Kind: blackbox.ReadKind(d.Kind), Message: d.Message}
	default:
		return we
	}
	return &nativeError{native: n, wire: we}
}
