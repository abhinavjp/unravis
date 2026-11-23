package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"unicode/utf8"

	"api-audit-explorer/internal/audit"
)

// BL-344: the entry points the backend dispatcher (internal/mcpsrv) uses. The dispatcher runs a tool for an authenticated profile in the
// owner process, so it needs the same answer the stdio loop gives, without the loop: CallEnvelope is the body of the SDK handler, and
// ErrorEnvelope builds the refusals the backend makes itself (a profile that may not call) in the same shape.

// unknownToolText is the answer to a tool name the catalog does not have. The name the client sent is never echoed.
const unknownToolText = "unknown tool; only the advertised read-only audit tools are available"

// knownTool says whether name is one of the ten tools of the catalog.
func knownTool(name string) bool {
	for _, def := range tools() {
		if n, _ := def["name"].(string); n == name {
			return true
		}
	}
	return false
}

// KnownTool says whether name is a tool of the catalog.
func KnownTool(name string) bool { return knownTool(name) }

// CallEnvelope runs one tool as tools/call does and returns the envelope (the bytes that are both the structured and the text content)
// and whether it is an error answer. The call ends with ctx and, at the latest, after the five-minute operation deadline. The answer
// never repeats the arguments, a path or upstream text.
func (b *Bridge) CallEnvelope(ctx context.Context, name string, args json.RawMessage) (body []byte, isError bool) {
	if !knownTool(name) {
		return encodeError("unknown", coded(CodeInvalidArguments, unknownToolText)), true
	}
	callCtx, cancel := context.WithTimeout(ctx, audit.OperationTimeout)
	defer cancel()
	result, err := b.call(callCtx, name, args)
	if err == nil {
		body, err = encodeResult(name, result)
	}
	if err != nil {
		return encodeError(name, err), true
	}
	return body, false
}

// ErrorEnvelope builds the error answer of a refusal made outside a tool: code is one of the Wire* codes (anything else becomes
// internal), message and action are fixed text of the caller (cut at 400 bytes), tool is a catalog name ("unknown" for any other
// string, so a client's own text never comes back).
func ErrorEnvelope(tool, code, message, action string) []byte {
	if !knownTool(tool) {
		tool = "unknown"
	}
	if !slices.Contains(wireCodes, code) {
		code = WireInternal
	}
	if len(message) > maxErrorMessage {
		message = message[:maxErrorMessage]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return errorEnvelope(tool, code, message, action)
}

// StrictJSON says whether data is one valid JSON value in which no object, at any depth, has two keys that are equal or differ only in
// case, and whose nesting is within the bound of the stdio loop. A request that says one thing twice is refused, because the decoders
// that read it later match names without regard to case and the last one wins.
func StrictJSON(data []byte) bool { return json.Valid(data) && strictShape(data) }
