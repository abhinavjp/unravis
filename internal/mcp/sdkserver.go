package mcp

import (
	"api-audit-explorer/internal/audit"
	_ "api-audit-explorer/internal/mcp/envguard" // before the SDK: see the package comment
	"api-audit-explorer/internal/version"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The stdio loop runs on the official Go MCP SDK (BL-342; spike and verdict in docs/desktop/spikes/BL-341-go-sdk.md). The SDK speaks both
// wire lifecycles: the handshake of 2024-11-05 to 2025-11-25 and the per-request lifecycle of 2026-07-28. The loop keeps in front of it
// the bounds the hand-rolled loop had, which the SDK does not give: one JSON-RPC object per line of at most 1 MiB, a plain answer to a bad
// line instead of closing the session, four calls in flight and no duplicate ids, only the methods of the tools capability, no echo of the
// client's input in errors, answers capped at 1 MiB, and calls cancelled and awaited when the host goes away.

// inflight tracks the requests that have no answer yet, by canonical request id: an id in use is never admitted again (an answer to a
// second request with the same id would free the first one's slot), and at most maxInFlight of them are tools/call.
type inflight struct {
	mu    sync.Mutex
	ids   map[string]bool // id -> is a tools/call
	calls int
}

const (
	maxInFlight = 4  // tools/call
	maxTracked  = 64 // requests of any method; the SDK answers every request, so this only bounds a loop that never reads the answers
)

// servedMethods are the only methods that reach the SDK: the two lifecycles, the tools, and the notifications the loop acts on.
var servedMethods = map[string]bool{
	"initialize": true, "notifications/initialized": true, "ping": true, // handshake 2024-11-05 to 2025-11-25
	"server/discover": true, // 2026-07-28: no handshake, every request carries its own _meta
	"tools/list":      true, "tools/call": true, "notifications/cancelled": true,
}

// admit records id (canonical, from readLine), or says why it cannot be: a protocol error code and message.
func (f *inflight) admit(id json.RawMessage, call bool) (int, string) {
	key := string(id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, duplicate := f.ids[key]; duplicate {
		return -32600, "Request id is already in flight"
	}
	if call && f.calls >= maxInFlight {
		return -32000, "Too many in-flight requests; wait or cancel before retrying"
	}
	if len(f.ids) >= maxTracked {
		return -32000, "Too many in-flight requests; wait or cancel before retrying"
	}
	if f.ids == nil {
		f.ids = map[string]bool{}
	}
	f.ids[key] = call
	if call {
		f.calls++
	}
	return 0, ""
}

// waitEmpty returns when every tracked request has been answered, the session has ended, or d has passed.
func (f *inflight) waitEmpty(ctx context.Context, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		f.mu.Lock()
		empty := len(f.ids) == 0
		f.mu.Unlock()
		if empty {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// release frees the slot of the request the SDK just answered.
func (f *inflight) release(id json.RawMessage) {
	key := canonicalID(id)
	f.mu.Lock()
	if call, held := f.ids[key]; held {
		delete(f.ids, key)
		if call {
			f.calls--
		}
	}
	f.mu.Unlock()
}

// frameWriter is the SDK's output: one JSON-RPC message per Write (the SDK's ioConn writes the message and its newline in a single call; a
// test pins that), serialized with the loop's own protocol errors, each capped at maxMessage (a larger one is replaced by the same tool
// error the hand-rolled loop sent), each response releasing its in-flight slot, and each error answer carrying a fixed text.
type frameWriter struct {
	mu       sync.Mutex
	w        io.Writer
	err      error
	done     context.Context // the session; nothing is written once it has ended
	onFail   func()
	released func(id json.RawMessage)
}

func (f *frameWriter) Close() error { return nil }

func (f *frameWriter) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// Write sends one message the SDK produced. A server notification over the cap is dropped (the server sends none: no list-changed, no
// logging, no progress).
func (f *frameWriter) Write(p []byte) (int, error) {
	var head struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Error  *struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		} `json:"error"`
	}
	isResponse := json.Unmarshal(p, &head) == nil && head.Method == "" && len(head.ID) > 0
	if isResponse {
		// Free the slot before the answer is out, so a host that sends its next call on receiving this answer never meets the old one.
		f.released(head.ID)
		if head.Error != nil {
			// SDK error texts quote what the client sent (params, type names, versions). The code stays, the text is ours.
			p = scrubbedError(head.ID, head.Error.Code, head.Error.Data)
		}
	}
	if len(p)-1 > maxMessage {
		if !isResponse {
			return len(p), nil
		}
		p = oversized(head.ID)
	}
	return len(p), f.send(p)
}

// errorText is the only text an error answer carries, by code.
func errorText(code int) string {
	switch code {
	case -32700:
		return "Parse error"
	case -32600:
		return "Invalid request"
	case -32601:
		return "Method not found"
	case -32602:
		return "Invalid params"
	case -32603:
		return "Internal error"
	case -32022:
		return "Unsupported protocol version"
	case -32000:
		return "Request refused"
	}
	return "Request failed"
}

// scrubbedError is the error answer for id with the SDK's code, a fixed text and no data, except the list of versions the server speaks
// (SEP-2575), which a client needs to pick one. The version the client asked for is not sent back.
func scrubbedError(id json.RawMessage, code int, data json.RawMessage) []byte {
	if code == 0 {
		code = -32600 // the SDK uses 0 for "not initialized" and "already initialized"; 0 is not a JSON-RPC code
	}
	answer := map[string]any{"code": code, "message": errorText(code)}
	if code == -32022 {
		var versions struct {
			Supported []string `json:"supported"`
		}
		if json.Unmarshal(data, &versions) == nil && len(versions.Supported) > 0 {
			answer["data"] = map[string]any{"supported": versions.Supported}
		}
	}
	out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": answer})
	return append(out, '\n')
}

// reject sends a protocol error the loop answers itself.
func (f *frameWriter) reject(id json.RawMessage, code int, message string) {
	if f.done.Err() != nil {
		return
	}
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
	srv := sdk.NewServer(&sdk.Implementation{Name: "unravis", Version: serverVersion()}, &sdk.ServerOptions{
		Instructions: serverInstructions,
		// Tools only: no logging (the SDK's default), and no list-changed notifications.
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		// Nothing here is shared: the catalog and the results depend on who is connected. Never cacheable, never public.
		SetCacheable: func(_ context.Context, _ sdk.Request, c *sdk.Cacheable) { c.TTLMs, c.CacheScope = 0, "private" },
	})
	order := map[string]int{}
	for i, def := range tools() {
		name, _ := def["name"].(string)
		description, _ := def["description"].(string)
		order[name] = i
		hints, _ := def["annotations"].(map[string]any)
		destructive, openWorld := hints["destructiveHint"] == true, hints["openWorldHint"] == true
		srv.AddTool(&sdk.Tool{
			Name: name, Description: description, InputSchema: def["inputSchema"],
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: hints["readOnlyHint"] == true, DestructiveHint: &destructive, IdempotentHint: hints["idempotentHint"] == true, OpenWorldHint: &openWorld},
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

// Serve runs newline-delimited MCP over input and output until the input ends or parent is cancelled. Logs must never go to output.
func (b *Bridge) Serve(parent context.Context, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var pending inflight
	out := &frameWriter{w: output, done: ctx, onFail: cancel, released: pending.release}
	// The SDK reads from this pipe. It is sent only the canonical messages readLine builds, never a line as the client wrote it.
	pr, pw := io.Pipe()
	scanErrors := make(chan error, 1)
	callsCtx, cancelCalls := context.WithCancel(ctx)
	defer cancelCalls()
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), maxMessage)
		defer func() {
			// The input has ended. Calls still running are abandoned, as before (the host went away), and every other request already
			// read is still answered before the SDK is told the input is over; closing at once would drop those answers.
			cancelCalls()
			pending.waitEmpty(ctx, 5*time.Second)
			_ = pw.Close()
		}()
		for scanner.Scan() {
			line, refusal := readLine(scanner.Bytes())
			if refusal != nil {
				if !refusal.silent {
					out.reject(refusal.id, refusal.code, refusal.message)
				}
				continue
			}
			if len(line.id) != 0 {
				if code, message := pending.admit(line.id, line.method == "tools/call"); code != 0 {
					out.reject(line.id, code, message)
					continue
				}
			}
			if _, err := pw.Write(append(line.message, '\n')); err != nil {
				return
			}
		}
		scanErrors <- scanner.Err()
	}()
	var workers sync.WaitGroup
	// The line cap is enforced above, on the line itself. The SDK counts bytes its decoder reads ahead too, so its own cap sits higher and
	// only backs the first one up.
	_ = newSDKServer(b, callsCtx, &workers).Run(ctx, &sdk.IOTransport{Reader: pr, Writer: out, MaxLineLength: 2 * maxMessage})
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

// serverVersion is the version the server reports: the build's, as stamped, or "dev".
func serverVersion() string {
	if v := strings.TrimSpace(version.Version); v != "" {
		return v
	}
	return "dev"
}
