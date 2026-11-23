package mcp

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxMessage = 1 << 20
	maxSafeID  = 1<<53 - 1
)

// serverInstructions is the text the server returns to every client on connect (initialize reply or server/discover).
const serverInstructions = "Unravis: local read-only Brain Payroll audit investigations. Start with audit_get_guide and audit_list_sources. Do not infer browser filters. Use explicit source IDs and incident/reference windows. No credentials, payloads, connection writes or code execution are exposed."

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func validID(id json.RawMessage) bool {
	return idKey(id) != ""
}

// idKey is the canonical form of an id a client sent, or "" for an id the loop refuses (over 256 bytes as sent, not a string or integer, ...).
func idKey(id json.RawMessage) string {
	if len(id) == 0 || len(id) > 256 {
		return ""
	}
	return canonicalID(id)
}

// canonicalID is the form an id is tracked under, whatever its length. An answer carries the id as the SDK encoded it, which can be longer
// than the client's (U+2028 and U+2029 are always escaped, 3 bytes to 6), so a slot is freed by this, not by idKey (review 3, S1).
func canonicalID(id json.RawMessage) string {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		// A string with invalid UTF-8 or a lone surrogate would come back changed (U+FFFD), and two such ids would collide: refused.
		if strings.ContainsRune(v, utf8.RuneError) {
			return ""
		}
		data, _ := json.Marshal(v)
		return string(data)
	case json.Number:
		// Beyond 2^53 the SDK's float64 ids would answer a different number than the client sent, so such ids are refused.
		if n, err := v.Int64(); err == nil && n >= -maxSafeID && n <= maxSafeID {
			return strconv.FormatInt(n, 10)
		}
	}
	return ""
}
