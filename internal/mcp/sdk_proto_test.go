package mcp

import (
	"api-audit-explorer/internal/audit"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file is the BL-341 prototype: the stdio loop on the official Go SDK, the way BL-342 would ship it. It lives in a test file so that the
// spike changes no shipped code (verdict: docs/desktop/spikes/BL-341-go-sdk.md). The SDK speaks both wire lifecycles: the handshake of
// 2024-11-05 to 2025-11-25 and the per-request lifecycle of 2026-07-28. The loop keeps in front of it the bounds the hand-rolled loop had,
// which the SDK does not give: one JSON-RPC object per line of at most 1 MiB, a plain answer to a bad line instead of closing the session,
// four calls in flight and no duplicate ids, only the methods of the tools capability, no echo of the client's input in errors, answers
// capped at 1 MiB, and calls cancelled and awaited when the host goes away.

// inflight tracks the tools/call requests that have no answer yet, by decoded request id (the loop's 4-in-flight and duplicate-id rules).
type inflight struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

const maxInFlight = 4

// servedMethods are the only methods that reach the SDK: the two lifecycles, the tools, and the notifications the loop acts on.
var servedMethods = map[string]bool{
	"initialize": true, "notifications/initialized": true, "ping": true, // handshake 2024-11-05 to 2025-11-25
	"server/discover": true, // 2026-07-28: no handshake, every request carries its own _meta
	"tools/list":      true, "tools/call": true, "notifications/cancelled": true,
}

// admit records id, or says why it cannot be: a protocol error code and message.
func (f *inflight) admit(id json.RawMessage) (int, string) {
	key := idKey(id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, duplicate := f.ids[key]; duplicate {
		return -32600, "Request id is already in flight"
	}
	if len(f.ids) >= maxInFlight {
		return -32000, "Too many in-flight requests; wait or cancel before retrying"
	}
	if f.ids == nil {
		f.ids = map[string]struct{}{}
	}
	f.ids[key] = struct{}{}
	return 0, ""
}

func (f *inflight) release(id json.RawMessage) {
	f.mu.Lock()
	delete(f.ids, idKey(id))
	f.mu.Unlock()
}

// frameWriter is the SDK's output: one JSON-RPC message per Write, serialized with the loop's own protocol errors, each capped at
// maxMessage (a larger one is replaced by the same tool error the hand-rolled loop sent), and each response releasing its in-flight slot.
type frameWriter struct {
	mu       sync.Mutex
	w        io.Writer
	err      error
	onFail   func()
	released func(id json.RawMessage)
}

func (f *frameWriter) Close() error { return nil }

func (f *frameWriter) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// Write sends one message the SDK produced.
func (f *frameWriter) Write(p []byte) (int, error) {
	var head struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	isResponse := json.Unmarshal(p, &head) == nil && head.Method == "" && len(head.ID) > 0
	if isResponse {
		// Free the slot before the answer is out, so a host that sends its next call on receiving this answer never meets the old one.
		f.released(head.ID)
	}
	if len(p)-1 > maxMessage {
		if !isResponse {
			return len(p), nil
		}
		p = oversized(head.ID)
	}
	return len(p), f.send(p)
}

// reject sends a protocol error the loop answers itself.
func (f *frameWriter) reject(id json.RawMessage, code int, message string) {
	_ = f.send(rpcError(id, code, message))
}

func (f *frameWriter) send(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if _, err := f.w.Write(p); err != nil {
		f.err = err
		f.onFail()
		return err
	}
	return nil
}

func rpcError(id json.RawMessage, code int, message string) []byte {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	return append(data, '\n')
}

func oversized(id json.RawMessage) []byte {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "Tool output exceeds 1 MiB. Reduce the requested page limit."}}}})
	return append(data, '\n')
}

// newSDKServer builds the SDK server over bridge b. life is the loop's own context: a tool call ends with it (host closed the pipe, parent
// cancelled, output failed) even if the SDK's per-request context were to outlive the session, and workers lets the loop wait for the
// calls still running before it returns, as the hand-rolled loop did.
func newSDKServer(b *Bridge, life context.Context, workers *sync.WaitGroup) *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "unravis", Version: "1.0.0"}, &sdk.ServerOptions{
		Instructions: serverInstructions,
		// Tools only: no logging (the SDK's default), and no list-changed notifications.
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		// Nothing here is shared: the catalog and the results depend on who is connected. Never cacheable, never public.
		SetCacheable: func(_ context.Context, _ sdk.Request, c *sdk.Cacheable) { c.TTLMs, c.CacheScope = 0, "private" },
	})
	no := false
	order := map[string]int{}
	for i, def := range tools() {
		name, _ := def["name"].(string)
		description, _ := def["description"].(string)
		order[name] = i
		srv.AddTool(&sdk.Tool{
			Name: name, Description: description, InputSchema: def["inputSchema"],
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no},
		}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			workers.Add(1)
			defer workers.Done()
			callCtx, cancel := context.WithTimeout(life, audit.OperationTimeout)
			defer cancel()
			defer context.AfterFunc(ctx, cancel)() // a cancel notification still ends the call
			result, err := b.call(callCtx, req.Params.Name, req.Params.Arguments)
			var text string
			if err != nil {
				text = err.Error()
			} else if data, marshalErr := json.Marshal(result); marshalErr != nil {
				text, err = "Could not encode audit results", marshalErr
			} else {
				text = string(data)
			}
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}, IsError: err != nil}, nil
		})
	}
	srv.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if call, ok := req.(*sdk.CallToolRequest); ok {
				if _, known := order[call.Params.Name]; !known {
					// Same answer as before, and the name the client sent is not echoed.
					return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "unknown tool; only the advertised read-only audit tools are available"}}, IsError: true}, nil
				}
			}
			result, err := next(ctx, method, req)
			if listed, ok := result.(*sdk.ListToolsResult); ok && err == nil {
				// The SDK lists tools by name; the catalog has a fixed order of its own.
				for i := 1; i < len(listed.Tools); i++ {
					for j := i; j > 0 && order[listed.Tools[j].Name] < order[listed.Tools[j-1].Name]; j-- {
						listed.Tools[j], listed.Tools[j-1] = listed.Tools[j-1], listed.Tools[j]
					}
				}
			}
			return result, err
		}
	})
	return srv
}

// serveSDK runs newline-delimited MCP over input and output until the input ends or parent is cancelled. Logs must never go to output.
func (b *Bridge) serveSDK(parent context.Context, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var pending inflight
	out := &frameWriter{w: output, onFail: cancel, released: pending.release}
	// The SDK reads from this pipe. Only lines that are well-formed requests or notifications reach it.
	pr, pw := io.Pipe()
	scanErrors := make(chan error, 1)
	go func() {
		defer func() { _ = pw.Close() }()
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), maxMessage)
		for scanner.Scan() {
			line := scanner.Bytes()
			if !json.Valid(line) {
				out.reject(nil, -32700, "Parse error")
				continue
			}
			var request rpcRequest
			if json.Unmarshal(line, &request) != nil || request.JSONRPC != "2.0" || request.Method == "" {
				out.reject(nil, -32600, "Invalid request")
				continue
			}
			if len(request.ID) != 0 && !validID(request.ID) {
				out.reject(nil, -32600, "Request id must be a string or integer")
				continue
			}
			if !servedMethods[request.Method] {
				// Only tools are offered. Anything else is refused here, so no other SDK feature is reachable and the method name is not echoed.
				if len(request.ID) != 0 {
					out.reject(request.ID, -32601, "Method not found")
				}
				continue
			}
			if len(request.ID) != 0 && oversizeVersion(request.Params) {
				// The SDK answers an unknown 2026-07-28 version with the string it was sent; a version is a date, so nothing longer is echoed.
				out.reject(request.ID, -32602, "Invalid protocol version")
				continue
			}
			if request.Method == "tools/call" && len(request.ID) != 0 {
				var params struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
					Meta      json.RawMessage `json:"_meta"`
				}
				if strictArgs(request.Params, &params) != nil || params.Name == "" {
					out.reject(request.ID, -32602, "Invalid tools/call parameters")
					continue
				}
				if code, message := pending.admit(request.ID); code != 0 {
					out.reject(request.ID, code, message)
					continue
				}
			}
			if _, err := pw.Write(append(append([]byte(nil), line...), '\n')); err != nil {
				return
			}
		}
		scanErrors <- scanner.Err()
	}()
	var workers sync.WaitGroup
	// The line cap is enforced above, on the line itself. The SDK counts bytes its decoder reads ahead too, so its own cap sits higher and
	// only backs the first one up.
	_ = newSDKServer(b, ctx, &workers).Run(ctx, &sdk.IOTransport{Reader: pr, Writer: out, MaxLineLength: 2 * maxMessage})
	cancel()
	_ = pr.Close()
	workers.Wait()
	if closer, ok := input.(io.Closer); ok {
		_ = closer.Close()
	}
	var inputErr error
	select {
	case inputErr = <-scanErrors:
	default:
	}
	if inputErr != nil && parent.Err() == nil {
		return errors.New("MCP input could not be read or exceeded 1 MiB")
	}
	if out.failure() != nil {
		return errors.New("MCP output could not be written")
	}
	return nil
}

// oversizeVersion reports whether the per-request protocol version (_meta, 2026-07-28 lifecycle) is longer than any real version.
func oversizeVersion(params json.RawMessage) bool {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false
	}
	return len(p.Meta[sdk.MetaKeyProtocolVersion]) > 32
}
