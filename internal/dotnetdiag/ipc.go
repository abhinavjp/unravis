// Package dotnetdiag is the read-only .NET diagnostic IPC probe (BL-110 step 1).
//
// It knows exactly two messages, a fixed counters start and a stop, and one
// reply format. Nothing in it takes a provider name, keyword, command id or
// payload from a caller. Bytes from the runtime are untrusted: every length is
// checked against a fixed size before it is used and nothing is allocated from
// a number the peer sent.
package dotnetdiag

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"unicode/utf16"
)

// Reason is a fixed word for why a probe stopped. It never carries OS or
// runtime text.
type Reason string

const (
	ReasonNotRunning   Reason = "not-running"
	ReasonDenied       Reason = "diag-denied"
	ReasonDisabled     Reason = "diag-disabled" // no port open in a live process
	ReasonBusy         Reason = "busy"
	ReasonPeerMismatch Reason = "peer-mismatch"
	ReasonUnsupported  Reason = "unsupported"
	ReasonRejected     Reason = "rejected" // runtime sent an error reply
	ReasonTimeout      Reason = "timeout"
	ReasonBadReply     Reason = "bad-reply"
	ReasonBadStream    Reason = "bad-stream"
	ReasonStopFailed   Reason = "stop-failed"
	ReasonCancelled    Reason = "cancelled"
	ReasonOther        Reason = "other"
)

// Error carries a Reason and a number (Win32 error or HRESULT, 0 if none).
// Its text is built from those two only, never from OS or runtime text.
type Error struct {
	Reason Reason
	Code   uint32
}

func (e *Error) Error() string {
	if e.Code == 0 {
		return "dotnetdiag: " + string(e.Reason)
	}
	return fmt.Sprintf("dotnetdiag: %s (code 0x%08x)", e.Reason, e.Code)
}

func newErr(r Reason, code uint32) *Error { return &Error{Reason: r, Code: code} }

// Wire constants of the diagnostic IPC protocol (dotnet/diagnostics,
// documentation/design-docs/ipc-protocol.md). All numbers are little-endian.
const (
	ipcMagic     = "DOTNET_IPC_V1\x00" // 14 bytes, includes the NUL
	ipcMagicLen  = 14
	ipcHeaderLen = 20

	setEvtPipe byte = 0x02
	setServer  byte = 0xFF

	idCollectTracing3 byte = 0x04 // CollectTracing3
	idStopTracing     byte = 0x01
	idReplyOK         byte = 0x00
	idReplyError      byte = 0xFF

	// Largest reply the client accepts: header plus one uint64 or one int32.
	maxReplyLen = 28
	okReplyLen  = 28
	errReplyLen = 24
)

// startProvider and startArgs are the only provider text the probe ever sends.
const (
	startProvider = "System.Runtime"
	startArgs     = "EventCounterIntervalSec=5"
)

// appendUTF16 appends len-in-chars (including the NUL) and the UTF-16LE text
// with a final NUL. It is only ever called with the two constants above.
func appendUTF16(b []byte, s string) []byte {
	u := utf16.Encode([]rune(s))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(u)+1))
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return binary.LittleEndian.AppendUint16(b, 0)
}

func appendHeader(b []byte, size uint16, set, id byte) []byte {
	b = append(b, ipcMagic...)
	b = binary.LittleEndian.AppendUint16(b, size)
	return append(b, set, id, 0, 0)
}

// encodeStart builds the one fixed CollectTracing3 message: a 1 MiB buffer,
// NetTrace format, no rundown, no stack walk, one provider (System.Runtime,
// all keywords, verbose) with a 5 second counter interval. 136 bytes.
func encodeStart() []byte {
	b := make([]byte, 0, 136)
	b = appendHeader(b, 0, setEvtPipe, idCollectTracing3) // size patched below
	b = binary.LittleEndian.AppendUint32(b, 1)            // circularBufferMB
	b = binary.LittleEndian.AppendUint32(b, 1)            // format: NetTrace
	b = append(b, 0, 0)                                   // requestRundown, requestStackwalk
	b = binary.LittleEndian.AppendUint32(b, 1)            // provider count
	b = binary.LittleEndian.AppendUint64(b, 0xFFFFFFFF)   // keywords
	b = binary.LittleEndian.AppendUint32(b, 5)            // log level: verbose
	b = appendUTF16(b, startProvider)
	b = appendUTF16(b, startArgs)
	binary.LittleEndian.PutUint16(b[ipcMagicLen:], uint16(len(b)))
	return b
}

// encodeStop builds the one fixed StopTracing message for a session id. 28 bytes.
func encodeStop(id uint64) []byte {
	b := make([]byte, 0, 28)
	b = appendHeader(b, 28, setEvtPipe, idStopTracing)
	return binary.LittleEndian.AppendUint64(b, id)
}

// reply is a decoded server reply. Exactly one of the two shapes is set:
// IsError false with SessionID, or IsError true with HRESULT.
type reply struct {
	IsError   bool
	SessionID uint64
	HRESULT   uint32
}

// decodeReply reads exactly one reply and nothing more: 20 header bytes, then
// size-20 payload bytes into a fixed array. It never reads or allocates by the
// size field beyond the two accepted sizes. A read error from a deadline is
// returned as ReasonTimeout, anything else wrong as ReasonBadReply.
func decodeReply(r io.Reader) (reply, error) {
	var hdr [ipcHeaderLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return reply{}, readErr(err)
	}
	if string(hdr[:ipcMagicLen]) != ipcMagic {
		return reply{}, newErr(ReasonBadReply, 0)
	}
	size := binary.LittleEndian.Uint16(hdr[14:16])
	set, id := hdr[16], hdr[17]
	if binary.LittleEndian.Uint16(hdr[18:20]) != 0 || set != setServer {
		return reply{}, newErr(ReasonBadReply, 0)
	}
	switch {
	case size == okReplyLen && id == idReplyOK:
	case size == errReplyLen && id == idReplyError:
	default:
		return reply{}, newErr(ReasonBadReply, 0)
	}
	var payload [maxReplyLen - ipcHeaderLen]byte
	n := int(size) - ipcHeaderLen
	if _, err := io.ReadFull(r, payload[:n]); err != nil {
		return reply{}, readErr(err)
	}
	if id == idReplyError {
		return reply{IsError: true, HRESULT: binary.LittleEndian.Uint32(payload[:4])}, nil
	}
	return reply{SessionID: binary.LittleEndian.Uint64(payload[:8])}, nil
}

// readErr maps a failed read to a Reason. A deadline is a timeout; a short or
// closed read is a bad reply. The text of err is dropped.
func readErr(err error) error {
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return newErr(ReasonTimeout, 0)
	}
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	return newErr(ReasonBadReply, 0)
}

// HRESULTs the runtime is known to send for the start and stop commands.
const (
	hrNotSupported         uint32 = 0x80131515
	hrUnknownCommand       uint32 = 0x80131385
	hrNotYetAvailable      uint32 = 0x8013135b
	hrRuntimeUninitialized uint32 = 0x80131371
)

// reasonForHRESULT maps an error reply to a Reason. The number itself is kept
// by the caller in Error.Code.
func reasonForHRESULT(hr uint32) Reason {
	switch hr {
	case hrNotSupported, hrUnknownCommand:
		return ReasonUnsupported
	case hrNotYetAvailable, hrRuntimeUninitialized:
		return ReasonBusy
	}
	return ReasonRejected
}

// err returns nil for an OK reply, or the Error for an error reply.
func (r reply) err() error {
	if !r.IsError {
		return nil
	}
	return newErr(reasonForHRESULT(r.HRESULT), r.HRESULT)
}
