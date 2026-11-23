package clicore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SchemaVersion is the version of the output contract. It is a string on purpose (spec D9).
const SchemaVersion = "1"

// Error is a failure the command line reports. Message and Action are plain words for a person and carry no secret, path of a
// secret, token or connection text: the caller builds them from fixed text.
type Error struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Action    string `json:"action,omitempty"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// NewError makes an error with its default retry rule (the body was not sent).
func NewError(c Code, message, action string) *Error {
	return &Error{Code: c, Message: message, Retryable: Retryable(c, false), Action: action}
}

// Meta is what travels next to the data of a success.
type Meta struct {
	Coverage    string   `json:"coverage"`    // complete or partial
	Identifiers string   `json:"identifiers"` // hidden or shown
	Truncated   any      `json:"truncated"`   // null, or what was cut
	Warnings    []string `json:"warnings"`    // never null
}

type successEnvelope struct {
	SchemaVersion string `json:"schemaVersion"`
	Operation     string `json:"operation"`
	RequestID     string `json:"requestId"`
	Data          any    `json:"data"`
	Meta          Meta   `json:"meta"`
}

type errorEnvelope struct {
	SchemaVersion string `json:"schemaVersion"`
	Operation     string `json:"operation"`
	RequestID     string `json:"requestId"`
	Error         *Error `json:"error"`
}

// NewRequestID returns "req_" and 16 hex characters from the system random source. It is also the correlation text of an internal
// error, so a person can quote it.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("clicore: no random source: " + err.Error()) // a program that cannot get random bytes must not go on
	}
	return "req_" + hex.EncodeToString(b[:])
}

// MarshalSuccess is the one line (with its newline) a successful command writes to stdout in JSON mode.
func MarshalSuccess(operation, requestID string, data any, meta Meta) ([]byte, error) {
	if meta.Warnings == nil {
		meta.Warnings = []string{}
	}
	return ASCIIJSON(successEnvelope{SchemaVersion, operation, requestID, data, meta})
}

// MarshalError is the one line (with its newline) a failed command writes to stderr in JSON mode.
func MarshalError(operation, requestID string, e *Error) ([]byte, error) {
	if e == nil {
		return nil, errors.New("clicore: no error to write")
	}
	return ASCIIJSON(errorEnvelope{SchemaVersion, operation, requestID, e})
}

// ASCIIJSON encodes v as one JSON line made of 7-bit characters only: every character above U+007F is written as \uXXXX (a surrogate
// pair above U+FFFF), so the text survives any console code page, a UTF-16 redirect of Windows PowerShell 5.1 and a copy through a
// tool that guesses the encoding (spec D9). There is no byte order mark. "<", ">" and "&" are not escaped.
func ASCIIJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	in := buf.Bytes() // ends with "\n"
	out := make([]byte, 0, len(in)+16)
	for _, r := range string(bytes.TrimSuffix(in, []byte("\n"))) {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r < 0x10000:
			out = append(out, fmt.Sprintf(`\u%04x`, r)...)
		default:
			r -= 0x10000
			out = append(out, fmt.Sprintf(`\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))...)
		}
	}
	return append(out, '\n'), nil
}

// WriteError writes a failure the way the output mode asks: one JSON object on stderr in JSON and JSONL mode, or three plain lines
// ("unravis: message", "Action: ...", "Code: ...") in table mode. It returns the exit code of the error. stdout is not touched.
func WriteError(stderr io.Writer, mode Mode, operation, requestID string, e *Error) int {
	if e == nil {
		e = NewError(CodeInternal, "An unknown error happened.", "")
	}
	if mode == ModeJSON || mode == ModeJSONL {
		if b, err := MarshalError(operation, requestID, e); err == nil {
			_, _ = stderr.Write(b)
			return ExitCode(e.Code)
		}
	}
	var sb strings.Builder
	sb.WriteString("unravis: " + Printable(e.Message) + "\n")
	if e.Action != "" {
		sb.WriteString("Action: " + Printable(e.Action) + "\n")
	}
	sb.WriteString("Code: " + string(e.Code) + "\n")
	if e.Code == CodeInternal {
		sb.WriteString("Request: " + requestID + "\n") // the support correlation id
	}
	_, _ = io.WriteString(stderr, sb.String())
	return ExitCode(e.Code)
}
