package mcp

import (
	"bytes"
	"encoding/json"
	"strconv"
)

const maxMessage = 1 << 20

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
func idKey(id json.RawMessage) string {
	if len(id) == 0 || len(id) > 256 {
		return ""
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		data, _ := json.Marshal(v)
		return string(data)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return strconv.FormatInt(n, 10)
		}
	}
	return ""
}
