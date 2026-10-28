// Package keeper is the Unravis key keeper (P14, owner ruling B19).
//
// The key keeper is a small Windows service that alone holds the saved secrets of a shared install: saved SQL Server passwords, saved
// MongoDB link passwords and Blackbox read keys. They are sealed in the keeper's own folder for the keeper's service account. Unravis
// processes (one per person signed in to the PC) talk to the keeper over a local named pipe and ask it to DO things with a saved secret
// ("test saved SQL connection 7f3a", "run the fixed health checks on it", "GET /manifest from Blackbox 12be"); the keeper answers with
// the result. There is no operation that returns a password or a read key, in any form. A secret travels the other way only: from the
// person who types it, through their Unravis, into the keeper, once.
//
// Wire format (protocol version 1):
//
//   - One request per connection. Every frame is a 4-byte big-endian length followed by that many bytes, at most MaxFrame.
//   - The client sends one frame holding a JSON Request {v, op, args}.
//   - The keeper answers one frame holding a JSON Response {ok, result | error, stream}.
//   - When stream is true, chunk frames follow. The first byte of a chunk says what it is: 'D' data (the rest is body bytes), 'E' a
//     failure part-way (the rest is a JSON WireError) or 'Z' the clean end. A connection that closes before 'Z' is a broken read.
//   - The caller is identified by Windows itself (the pipe client token), per connection, so there is nothing to replay: a request
//     carries no session token, and a copied frame sent by another account is that other account's request.
//
// Operations are grouped in namespaces: ping/status, sql.*, mongo.*, bb.*. The namespace audit.* is reserved for P14b, which will
// host the MongoDB audit repository (internal/store) behind this same pipe, using the stream frames for query results. See
// Server.Handle for the extension point.
package keeper

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is the request version this keeper speaks.
const ProtocolVersion = 1

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

// WireError is a refusal or a failure. Code is one of the Code* constants; Message is safe to show; Detail carries the structured part
// (a driver's connect classification, the existing entry of a duplicate, a Blackbox read kind).
type WireError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  json.RawMessage `json:"detail,omitempty"`
}

func (e *WireError) Error() string { return e.Message }

// Error codes.
const (
	CodeNotFound     = "not-found"
	CodeFull         = "full"
	CodeInvalid      = "invalid"
	CodeDuplicate    = "duplicate"
	CodeCorrupt      = "corrupt"
	CodeUnavailable  = "unavailable"
	CodeConnect      = "connect"       // SQL Server: Detail is {code, message} from dbhealth.ClassifyConnect
	CodeMongoConnect = "mongo-connect" // MongoDB: Detail is {stage, code, message} (audit.ConnectionError)
	CodeMismatch     = "mismatch"      // a saved MongoDB link no longer matches the settings sent with it
	CodeRateLimited  = "rate-limited"
	CodeRefused      = "refused"
	CodeBusy         = "busy"
	CodeBBRead       = "bb-read" // Blackbox read: Detail is {kind, message}
	CodeUnknownOp    = "unknown-op"
	CodeVersion      = "version"
	CodeInternal     = "internal"
)

// Operations.
const (
	OpPing   = "ping"
	OpStatus = "status"

	OpSQLList     = "sql.list"
	OpSQLSave     = "sql.save"
	OpSQLDelete   = "sql.delete"
	OpSQLDescribe = "sql.describe"
	OpSQLTest     = "sql.test"
	OpSQLHealth   = "sql.health"
	OpSQLLane     = "sql.lane"
	OpSQLImport   = "sql.import"

	OpMongoList     = "mongo.list"
	OpMongoDescribe = "mongo.describe"
	OpMongoRemember = "mongo.remember"
	OpMongoDelete   = "mongo.delete"
	OpMongoTest     = "mongo.test"
	OpMongoHealth   = "mongo.health"
	OpMongoLane     = "mongo.lane"
	OpMongoImport   = "mongo.import"

	OpBBList        = "bb.list"
	OpBBGet         = "bb.get"
	OpBBFind        = "bb.find"
	OpBBAdd         = "bb.add"
	OpBBSetThisPC   = "bb.setThisPC"
	OpBBSetKey      = "bb.setKey"
	OpBBSetRecorder = "bb.setRecorder"
	OpBBRemove      = "bb.remove"
	OpBBHTTP        = "bb.http"
	OpBBImport      = "bb.import"
)

// ReservedPrefixP14b is the namespace kept free for the MongoDB audit data plane (P14b).
const ReservedPrefixP14b = "audit."

// Chunk kinds of a stream.
const (
	chunkData = 'D'
	chunkErr  = 'E'
	chunkEnd  = 'Z'
)

var errFrameTooLarge = errors.New("keeper frame too large")

// WriteFrame writes one length-prefixed frame.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrame {
		return errFrameTooLarge
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
		return nil, errFrameTooLarge
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

func writeJSONFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("keeper: encode frame: %w", err)
	}
	return WriteFrame(w, b)
}
