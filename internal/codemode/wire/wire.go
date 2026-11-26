// Package wire is the frame protocol between the code-mode broker (in unravis.exe) and the sandbox worker (spec 04, section 3.3).
// It is stdlib only, so the worker can import it without pulling in anything else.
//
// A frame is a 4-byte little-endian length and then that many bytes of UTF-8 JSON. The reader of a frame never trusts the other end:
// a length over the maximum is refused before anything is allocated, a frame that is not exactly one JSON object of the expected shape
// (unknown field, repeated field, text after the object) is refused, and the caller ends the run on any such error.
package wire

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Version is the protocol version both ends say in hello.
const Version = 1

// MaxFrame is the largest payload a frame may carry: 1 MiB of data and 4 KiB of envelope.
const MaxFrame = 1<<20 + 4<<10

// Frame kinds (field "t").
const (
	TypeHello = "hello"
	TypeRun   = "run"
	TypeCall  = "call"
	TypeRet   = "ret"
	TypeDone  = "done"
)

// Errors of the reader. All of them end the run (code bad_frame).
var (
	ErrFrameTooLarge = errors.New("wire: frame too large")
	ErrEmptyFrame    = errors.New("wire: empty frame")
	ErrBadFrame      = errors.New("wire: malformed frame")
)

// WriteFrame writes one frame with a single Write call, so two writers that share a mutex cannot interleave bytes.
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) == 0 {
		return ErrEmptyFrame
	}
	if len(payload) > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(buf, uint32(len(payload))) //nolint:gosec // len(payload) <= MaxFrame, checked above
	copy(buf[4:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame. It returns io.EOF only when the stream ended before the first byte of a frame; a stream that ends inside
// a frame gives io.ErrUnexpectedEOF. A declared length over max (or MaxFrame when max is 0 or larger) is refused without reading it.
func ReadFrame(r io.Reader, max int) ([]byte, error) {
	if max <= 0 || max > MaxFrame {
		max = MaxFrame
	}
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(head[:])
	if n == 0 {
		return nil, ErrEmptyFrame
	}
	if uint64(n) > uint64(max) {
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

// Limits are the numbers the worker enforces for itself (the broker enforces its own copy of the ones it can see).
type Limits struct {
	MemPages   int `json:"memPages"`
	HeapBytes  int `json:"heapBytes"`
	StackBytes int `json:"stackBytes"`
	Ticks      int `json:"ticks"`
	CallBytes  int `json:"callBytes"`
	LogBytes   int `json:"logBytes"`
	MaxCalls   int `json:"maxCalls"`
	InFlight   int `json:"inFlight"`
	// EngineMs is the most time the worker lets the engine run for the whole script (not counting waits for host calls). Zero means the
	// worker's own default. It is the worker's own backstop for built-ins that poll no tick; the broker keeps its wall timer too.
	EngineMs int `json:"engineMs,omitempty"`
}

// Clock is the script clock the worker implements (privacy.ScriptClock).
type Clock struct {
	Mode   string `json:"mode"`
	BaseMs int64  `json:"baseMs"`
}

// Hello is the first frame, worker to broker, before any script code ran.
type Hello struct {
	T           string          `json:"t"`
	V           int             `json:"v"`
	Runtime     string          `json:"runtime"`
	Mitigations map[string]bool `json:"mitigations"`
	WasmSHA256  string          `json:"wasmSha256,omitempty"`
}

// Run is the one frame broker to worker that starts the script.
type Run struct {
	T      string   `json:"t"`
	JS     string   `json:"js"`
	Tools  []string `json:"tools"`
	Limits Limits   `json:"limits"`
	Clock  Clock    `json:"clock"`
	Seed   string   `json:"seed"`
}

// Call is a host call, worker to broker.
type Call struct {
	T    string          `json:"t"`
	ID   int64           `json:"id"`
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// RetError is the error of a ret frame.
type RetError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Ret answers one call, broker to worker. OK true carries Result (the tool's answer, which itself may be an error envelope).
type Ret struct {
	T      string          `json:"t"`
	ID     int64           `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RetError       `json:"error,omitempty"`
}

// DoneError is the failure of a done frame.
type DoneError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

// Done is the last frame, worker to broker.
type Done struct {
	T             string          `json:"t"`
	OK            bool            `json:"ok"`
	Result        json.RawMessage `json:"result,omitempty"`
	Logs          []string        `json:"logs,omitempty"`
	LogsTruncated bool            `json:"logsTruncated,omitempty"`
	Ticks         int64           `json:"ticks,omitempty"`
	HeapPeak      int64           `json:"heapPeak,omitempty"`
	Error         *DoneError      `json:"error,omitempty"`
}

// PeekType returns the "t" of a frame after checking that it is one JSON object, valid UTF-8, with no repeated top-level name.
func PeekType(frame []byte) (string, error) {
	if !utf8.Valid(frame) {
		return "", ErrBadFrame
	}
	if err := checkObject(frame); err != nil {
		return "", err
	}
	var k struct {
		T string `json:"t"`
	}
	if err := json.Unmarshal(frame, &k); err != nil || k.T == "" {
		return "", ErrBadFrame
	}
	return k.T, nil
}

// Decode reads a frame into v (a pointer to one of the frame types). An unknown field, a repeated field, a second JSON value after
// the object, or a value of the wrong type is ErrBadFrame. The error never carries the frame.
func Decode(frame []byte, v any) error {
	if !utf8.Valid(frame) {
		return ErrBadFrame
	}
	if err := checkObject(frame); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(frame))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ErrBadFrame
	}
	if dec.More() {
		return ErrBadFrame
	}
	return nil
}

// checkObject says the frame is exactly one JSON object whose top-level names are all different (compared exactly, and without
// regard to case, because the decoder matches field names without regard to case and the last one would win).
func checkObject(frame []byte) error {
	dec := json.NewDecoder(bytes.NewReader(frame))
	tok, err := dec.Token()
	if d, ok := tok.(json.Delim); err != nil || !ok || d != '{' {
		return ErrBadFrame
	}
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		key, ok := keyTok.(string)
		if err != nil || !ok {
			return ErrBadFrame
		}
		folded := foldKey(key)
		if seen[folded] {
			return ErrBadFrame
		}
		seen[folded] = true
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return ErrBadFrame
		}
	}
	if end, err := dec.Token(); err != nil {
		return ErrBadFrame
	} else if d, ok := end.(json.Delim); !ok || d != '}' {
		return ErrBadFrame
	}
	if _, err := dec.Token(); err != io.EOF {
		return ErrBadFrame
	}
	return nil
}

// foldKey maps a name to one representative of its case-folding orbit, as encoding/json matches names ("K" and the Kelvin sign are one
// name; so are "s" and the long s).
func foldKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < low {
				low = f
			}
		}
		b.WriteRune(low)
	}
	return b.String()
}

// Marshal encodes a frame and refuses one over MaxFrame. Text is not HTML-escaped, so a result reaches the worker with the same bytes
// the audit record holds.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("wire: %w", err)
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if len(b) > MaxFrame {
		return nil, ErrFrameTooLarge
	}
	return b, nil
}
