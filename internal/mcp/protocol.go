package mcp

import (
	"api-audit-explorer/internal/audit"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
)

const maxMessage = 1 << 20

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

// Serve implements newline-delimited MCP stdio. Logs must never go to output.
func (b *Bridge) Serve(parent context.Context, input io.Reader, output io.Writer) error {
	ctx, cancelAll := context.WithCancel(parent)
	defer cancelAll()
	var writes sync.Mutex
	var writeErr error
	send := func(id json.RawMessage, result any, code int, message string) {
		response := map[string]any{"jsonrpc": "2.0", "id": id}
		if code != 0 {
			response["error"] = map[string]any{"code": code, "message": message}
		} else {
			response["result"] = result
		}
		data, err := json.Marshal(response)
		if err != nil || len(data) > maxMessage {
			data, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "Tool output exceeds 1 MiB. Reduce the requested page limit."}}}})
		}
		writes.Lock()
		defer writes.Unlock()
		if writeErr != nil {
			return
		}
		_, writeErr = output.Write(append(data, '\n'))
		if writeErr != nil {
			cancelAll()
		}
	}
	var pendingMu sync.Mutex
	pending := map[string]context.CancelFunc{}
	var workers sync.WaitGroup
	initialized, ready := false, false
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxMessage)
	lines := make(chan []byte)
	scanErrors := make(chan error, 1)
	var inputErr error
	go func() {
		defer close(lines)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		scanErrors <- scanner.Err()
	}()
	defer func() {
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	reading := true
	for reading {
		var line []byte
		select {
		case <-ctx.Done():
			reading = false
			continue
		case data, ok := <-lines:
			if !ok {
				select {
				case inputErr = <-scanErrors:
				default:
				}
				reading = false
				continue
			}
			line = data
		}
		var request rpcRequest
		if !json.Valid(line) {
			send(nil, nil, -32700, "Parse error")
			continue
		}
		if json.Unmarshal(line, &request) != nil || request.JSONRPC != "2.0" || request.Method == "" {
			send(nil, nil, -32600, "Invalid request")
			continue
		}
		notification := len(request.ID) == 0
		if notification {
			if request.Method == "notifications/initialized" && initialized {
				ready = true
			}
			if request.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(request.Params, &p) == nil {
					pendingMu.Lock()
					if cancel := pending[idKey(p.RequestID)]; cancel != nil {
						cancel()
					}
					pendingMu.Unlock()
				}
			}
			continue
		}
		if !validID(request.ID) {
			send(nil, nil, -32600, "Request id must be a string or integer")
			continue
		}
		if request.Method == "initialize" {
			if initialized {
				send(request.ID, nil, -32600, "Already initialized")
				continue
			}
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if json.Unmarshal(request.Params, &params) != nil || params.ProtocolVersion == "" {
				send(request.ID, nil, -32602, "protocolVersion is required")
				continue
			}
			version := params.ProtocolVersion
			switch version {
			case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
			default:
				version = "2025-11-25"
			}
			initialized = true
			send(request.ID, map[string]any{"protocolVersion": version, "serverInfo": map[string]any{"name": "api-audit-explorer", "version": "1.0.0"}, "capabilities": map[string]any{"tools": map[string]any{}}, "instructions": "Local read-only Brain Payroll audit investigations. Start with audit_get_guide and audit_list_sources. Do not infer browser filters. Use explicit source IDs and incident/reference windows. No credentials, payloads, connection writes or code execution are exposed."}, 0, "")
			continue
		}
		if request.Method == "ping" {
			send(request.ID, map[string]any{}, 0, "")
			continue
		}
		if !ready {
			send(request.ID, nil, -32000, "Initialize the MCP session first")
			continue
		}
		if request.Method == "tools/list" {
			send(request.ID, map[string]any{"tools": tools()}, 0, "")
			continue
		}
		if request.Method != "tools/call" {
			send(request.ID, nil, -32601, "Method not found")
			continue
		}
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta"`
		}
		if strictArgs(request.Params, &params) != nil || params.Name == "" {
			send(request.ID, nil, -32602, "Invalid tools/call parameters")
			continue
		}
		pendingMu.Lock()
		if _, duplicate := pending[idKey(request.ID)]; duplicate {
			pendingMu.Unlock()
			send(request.ID, nil, -32600, "Request id is already in flight")
			continue
		}
		if len(pending) >= 4 {
			pendingMu.Unlock()
			send(request.ID, nil, -32000, "Too many in-flight requests; wait or cancel before retrying")
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, audit.OperationTimeout)
		pending[idKey(request.ID)] = cancel
		pendingMu.Unlock()
		workers.Add(1)
		go func(request rpcRequest, name string, args json.RawMessage) {
			defer workers.Done()
			defer cancel()
			defer func() { pendingMu.Lock(); delete(pending, idKey(request.ID)); pendingMu.Unlock() }()
			result, err := b.call(callCtx, name, args)
			var text string
			if err != nil {
				text = err.Error()
			} else {
				data, marshalErr := json.Marshal(result)
				if marshalErr != nil {
					text = "Could not encode audit results"
					err = marshalErr
				} else {
					text = string(data)
				}
			}
			send(request.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": err != nil}, 0, "")
		}(request, params.Name, params.Arguments)
	}
	cancelAll()
	workers.Wait()
	if inputErr != nil && parent.Err() == nil {
		return errors.New("MCP input could not be read or exceeded 1 MiB")
	}
	writes.Lock()
	defer writes.Unlock()
	if writeErr != nil {
		return errors.New("MCP output could not be written")
	}
	return nil
}
