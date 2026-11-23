package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

// One reading of every line (BL-342 review 1, M1/M2). The SDK's decoder is case-sensitive, keeps its own depth limit and rejects trailing
// bytes; encoding/json is case-insensitive and keeps the last duplicate. A line checked by one and handed to the other is two different
// messages. So the line is parsed here once, with strict rules, and the SDK is sent only a message built from the fields that passed: the
// raw bytes of a client line never reach it.

// maxNesting is the deepest array/object nesting a line may have. The SDK's decoder stops at 1000; real requests are a handful deep.
const maxNesting = 256

// lineRefusal is the answer to a line the loop will not pass on. id is nil when the line gave no id that could be trusted.
type lineRefusal struct {
	id      json.RawMessage
	code    int
	message string
	silent  bool // a notification: nothing to answer
}

// sdkLine is a line that passed: the message to send to the SDK, and what the loop needs to track it.
type sdkLine struct {
	message []byte          // canonical JSON-RPC message, no trailing newline
	id      json.RawMessage // canonical id; empty for a notification
	method  string
	params  json.RawMessage // object, or empty
}

// foldKey maps a key to one representative of its case-folding orbit, so keys that encoding/json would treat as the same field compare equal.
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

// strictShape checks, in one pass over already-valid JSON, that no object (at any depth) has two keys that are equal or differ only in case,
// and that nesting stays within maxNesting.
func strictShape(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()            // numbers out of float64 range are valid JSON; only their syntax matters here
	var keys []map[string]struct{} // one set per open object; nil entry for an array
	var expectKey []bool           // per open container: the next string token is a key
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		switch v := token.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				if len(keys) >= maxNesting {
					return false
				}
				if v == '{' {
					keys = append(keys, map[string]struct{}{})
					expectKey = append(expectKey, true)
				} else {
					keys = append(keys, nil)
					expectKey = append(expectKey, false)
				}
			default:
				keys, expectKey = keys[:len(keys)-1], expectKey[:len(expectKey)-1]
				afterValue(expectKey)
			}
		case string:
			if n := len(keys); n > 0 && keys[n-1] != nil && expectKey[n-1] {
				folded := foldKey(v)
				if _, seen := keys[n-1][folded]; seen {
					return false
				}
				keys[n-1][folded] = struct{}{}
				expectKey[n-1] = false
			} else {
				afterValue(expectKey)
			}
		default:
			afterValue(expectKey)
		}
	}
}

// afterValue marks that the value of the innermost object's key is done, so the next string there is a key again.
func afterValue(expectKey []bool) {
	if n := len(expectKey); n > 0 {
		expectKey[n-1] = true
	}
}

// readLine applies the loop's rules to one input line.
func readLine(line []byte) (*sdkLine, *lineRefusal) {
	if !json.Valid(line) {
		return nil, &lineRefusal{code: -32700, message: "Parse error"}
	}
	if !strictShape(line) {
		return nil, &lineRefusal{code: -32600, message: "Invalid request"}
	}
	var request rpcRequest
	if json.Unmarshal(line, &request) != nil || request.JSONRPC != "2.0" || request.Method == "" {
		return nil, &lineRefusal{code: -32600, message: "Invalid request"}
	}
	var id json.RawMessage
	if len(request.ID) != 0 {
		key := idKey(request.ID)
		if key == "" {
			return nil, &lineRefusal{code: -32600, message: "Request id must be a string or integer"}
		}
		id = json.RawMessage(key)
	}
	isRequest := len(id) != 0
	if !servedMethods[request.Method] || (isRequest && strings.HasPrefix(request.Method, "notifications/")) {
		// Only tools are offered. Anything else is refused here, so no other SDK feature is reachable and the method name is not echoed.
		return nil, &lineRefusal{id: id, code: -32601, message: "Method not found", silent: !isRequest}
	}
	if !isRequest && !strings.HasPrefix(request.Method, "notifications/") {
		return nil, &lineRefusal{silent: true} // a request method sent as a notification has nobody to answer to
	}
	params := bytes.TrimSpace(request.Params)
	if bytes.Equal(params, []byte("null")) {
		params = nil
	}
	if len(params) != 0 && params[0] != '{' {
		return nil, &lineRefusal{id: id, code: -32602, message: "Invalid params", silent: !isRequest}
	}
	if request.Method == "tools/call" {
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta"`
		}
		if strictArgs(params, &call) != nil || call.Name == "" {
			return nil, &lineRefusal{id: id, code: -32602, message: "Invalid tools/call parameters"}
		}
	}
	message := map[string]any{"jsonrpc": "2.0", "method": request.Method}
	if isRequest {
		message["id"] = id
	}
	if len(params) != 0 {
		message["params"] = json.RawMessage(params)
	}
	data, err := json.Marshal(message)
	if err != nil {
		return nil, &lineRefusal{id: id, code: -32600, message: "Invalid request", silent: !isRequest}
	}
	return &sdkLine{message: data, id: id, method: request.Method, params: params}, nil
}
