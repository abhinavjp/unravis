// Package keeperwire is the small, dependency-free part of the key keeper's protocol: the frame format, the request and answer shapes,
// the pipe dialer and the checks it makes on what answers. The keeper (internal/keeper) and its clients use it, and so does the
// Blackbox setup helper (cmd/unravis-blackbox), which must not link the keeper's stores, drivers and checks: it runs elevated and is
// hash-pinned, so what it imports is part of what an administrator trusts.
//
// The wire format is described in the internal/keeper package comment. Nothing here knows any saved secret.
package keeperwire

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ProtocolVersion is the request version the keeper speaks.
const ProtocolVersion = 1

// OpSQLHandoverTake is the one operation the elevated Blackbox helper sends (RV-SEC-2): it fetches the saved SQL login the person's
// Unravis asked the keeper to hold for it. The rest of the keeper's operations live in the keeper package.
const OpSQLHandoverTake = "sql.handover.take"

// MaxFrame bounds one frame (request, response or chunk).
const MaxFrame = 1 << 20

// Request is what a client sends.
type Request struct {
	V    int             `json:"v"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Response is what the keeper answers.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *WireError      `json:"error,omitempty"`
	Stream bool            `json:"stream,omitempty"`
}

// WireError is a refusal or a failure. Code is one of the keeper's Code* constants; Message is safe to show; Detail carries the
// structured part (a driver's connect classification, the existing entry of a duplicate, a Blackbox read kind).
type WireError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  json.RawMessage `json:"detail,omitempty"`
}

func (e *WireError) Error() string { return e.Message }

// Caller is who sent a request, as Windows reports it for the pipe client. It is filled by the transport, never by the request.
type Caller struct {
	SID     string `json:"sid"`
	Name    string `json:"name"`
	PID     uint32 `json:"pid"`
	Session uint32 `json:"session"`
}

// ClientConn is the client's side of one connection.
type ClientConn interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
}

// Dialer opens one connection to the keeper. A dialer whose keeper is not there returns an error that matches ErrNotRunning.
type Dialer interface {
	Dial(ctx context.Context) (ClientConn, error)
}

// ErrNotRunning means nothing is listening on the keeper's pipe: the service is stopped or not installed.
var ErrNotRunning = errors.New("the Unravis Key Keeper service is not running")

// ErrImpostor means something answered on the keeper's pipe name that is not a Windows service (its process is not in session 0).
var ErrImpostor = errors.New("the program answering on the key keeper's pipe is not the Unravis Key Keeper service")

// ErrFrameTooLarge is a frame over MaxFrame.
var ErrFrameTooLarge = errors.New("keeper frame too large")

// WriteFrame writes one length-prefixed frame.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(buf, uint32(len(payload)))
	copy(buf[4:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one length-prefixed frame.
func ReadFrame(r io.Reader) ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n > MaxFrame {
		return nil, ErrFrameTooLarge
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return buf, nil
}

// WriteJSONFrame encodes v and writes it as one frame.
func WriteJSONFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("keeper: encode frame: %w", err)
	}
	return WriteFrame(w, b)
}
