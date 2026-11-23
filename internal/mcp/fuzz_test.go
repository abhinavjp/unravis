package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// BL-450: random bytes into the stdio loop. It must not panic, must stop when the input ends, and everything it writes must be one
// JSON-RPC object per line, each under the 1 MiB cap (stdout carries nothing else: a stray line would break the host).
// No backend listens, so a tool call that needs data answers with an error, which is also valid output.
func FuzzBridgeServe(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"audit_list_sources","arguments":{"x":1,"x":2}}}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}` + "\n"))
	f.Add([]byte("not-json\n\n\x00\xff\n"))
	f.Add([]byte(`[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]` + "\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		b, err := New(1, WithToken(testSessionToken))
		if err != nil {
			t.Skip()
		}
		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = b.Serve(ctx, bytes.NewReader(input), &out)
		}()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("Serve did not stop after its input ended")
		}
		for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
			if line == "" {
				continue
			}
			if len(line) > maxMessage {
				t.Fatalf("a response line of %d bytes is over the %d cap", len(line), maxMessage)
			}
			var object map[string]any
			if json.Unmarshal([]byte(line), &object) != nil || object["jsonrpc"] != "2.0" {
				t.Fatalf("stdout carries something that is not a JSON-RPC object: %.200q", line)
			}
		}
	})
}
