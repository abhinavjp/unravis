package mcp

import "testing"

// BL-450: random bytes into the stdio loop. It must not panic, must stop when the input ends, everything it writes must be one
// JSON-RPC object per line, each under the 1 MiB cap (stdout carries nothing else: a stray line would break the host), and the session
// must live through the input: a ping sent after it is answered (checkStdout, BL-342 review 1). No backend listens. A call made after a good start is cancelled when the input ends (the host closed the pipe), so the data path is not
// reached here; what is fuzzed is the framing, the lifecycle and the argument checks.
func FuzzBridgeServe(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"))
	// Calls after a good start (without it the loop only answers "initialize first").
	const start = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	f.Add([]byte(start + `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}` + "\n"))
	f.Add([]byte(start + `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"audit_list_sources","arguments":{"x":1,"x":2}}}` + "\n"))
	f.Add([]byte(start + `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"audit_compare","arguments":{"start":"x","limit":-1}}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5}}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}` + "\n"))
	f.Add([]byte("not-json\n\n\x00\xff\n"))
	f.Add([]byte(`[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]` + "\n"))
	f.Fuzz(func(t *testing.T, input []byte) { checkStdout(t, sdkImpl, input) })
}
