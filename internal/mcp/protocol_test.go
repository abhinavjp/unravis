package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProtocolLifecycleAndReadOnlyDiscovery(t *testing.T) {
	input, clientInput := io.Pipe()
	clientOutput, output := io.Pipe()
	b, _ := New(18765, WithToken(testSessionToken))
	done := make(chan error, 1)
	go func() { done <- b.Serve(context.Background(), input, output); output.Close() }()
	scanner := bufio.NewScanner(clientOutput)
	send := func(value string) map[string]any {
		t.Helper()
		if _, err := io.WriteString(clientInput, value+"\n"); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() {
			t.Fatal("no MCP response")
		}
		var response map[string]any
		if json.Unmarshal(scanner.Bytes(), &response) != nil {
			t.Fatal("non-protocol stdout")
		}
		return response
	}
	init := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if init["result"].(map[string]any)["protocolVersion"] != "2025-11-25" {
		t.Fatal("bad negotiation")
	}
	io.WriteString(clientInput, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	listed := send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	definitions := listed["result"].(map[string]any)["tools"].([]any)
	if len(definitions) != 10 {
		t.Fatalf("tools=%d", len(definitions))
	}
	for _, item := range definitions {
		tool := item.(map[string]any)
		if tool["annotations"].(map[string]any)["readOnlyHint"] != true {
			t.Fatal("non-read-only tool")
		}
	}
	guide := send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`)
	if guide["result"].(map[string]any)["isError"] != false {
		t.Fatal("guide failed")
	}
	malformed := send(`not-json`)
	if malformed["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatal("invalid parse response")
	}
	unknown := send(`{"jsonrpc":"2.0","id":"unknown","method":"shell/exec"}`)
	if unknown["error"] == nil {
		t.Fatal("unknown method accepted")
	}
	clientInput.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	clientOutput.Close()
}

func TestServerInfoNameIsUnravis(t *testing.T) {
	input, clientInput := io.Pipe()
	clientOutput, output := io.Pipe()
	b, _ := New(18765, WithToken(testSessionToken))
	done := make(chan error, 1)
	go func() { done <- b.Serve(context.Background(), input, output); output.Close() }()
	scanner := bufio.NewScanner(clientOutput)
	if _, err := io.WriteString(clientInput, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() {
		t.Fatal("no MCP response")
	}
	var response struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.ServerInfo.Name != "unravis" || !strings.HasPrefix(response.Result.Instructions, "Unravis:") {
		t.Fatalf("serverInfo=%q instructions=%q", response.Result.ServerInfo.Name, response.Result.Instructions)
	}
	clientInput.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	clientOutput.Close()
}

func TestCacheExpiresAndPagingDoesNotMutateResults(t *testing.T) {
	b := newBridge(fixed("http://127.0.0.1:1"), nil)
	now := time.Now()
	b.now = func() time.Time { return now }
	b.resultID = "old"
	b.result = &auditAnalysisForTest
	b.created = now.Add(-resultTTL - time.Second)
	if _, err := b.cached("old"); err == nil {
		t.Fatal("expired cache accepted")
	}
	page, err := groupPage(auditAnalysisForTest.APIs, groupArgs{Limit: 1})
	if err != nil || page["totalGroups"].(int) != len(auditAnalysisForTest.APIs) {
		t.Fatal("invalid page")
	}
	if _, err := groupPage(nil, groupArgs{Limit: 101}); err == nil {
		t.Fatal("unbounded page accepted")
	}
	if _, err := strictArgsForTest(` {"unknown":1}`); err == nil {
		t.Fatal("unknown fields accepted")
	}
}
func strictArgsForTest(raw string) (any, error) {
	var v struct{}
	err := strictArgs(json.RawMessage(raw), &v)
	return v, err
}

func TestProtocolErrorNeverEchoesMalformedInput(t *testing.T) {
	b, _ := New(18765, WithToken(testSessionToken))
	var output strings.Builder
	if err := b.Serve(context.Background(), strings.NewReader("private-password-not-json\n"), &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private-password") {
		t.Fatal("input echoed")
	}
}

func TestCancelledToolStopsUpstreamQuery(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	b := newBridge(fixed(upstream.URL), upstream.Client())
	input, clientInput := io.Pipe()
	clientOutput, output := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- b.Serve(context.Background(), input, output); output.Close() }()
	scanner := bufio.NewScanner(clientOutput)
	io.WriteString(clientInput, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`+"\n")
	if !scanner.Scan() {
		t.Fatal("init failed")
	}
	io.WriteString(clientInput, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	io.WriteString(clientInput, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"audit_list_sources","arguments":{}}}`+"\n")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("call did not start")
	}
	io.WriteString(clientInput, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`+"\n")
	if !scanner.Scan() || !strings.Contains(scanner.Text(), `"isError":true`) {
		t.Fatal("cancelled call not reported")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not cancel")
	}
	clientInput.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	clientOutput.Close()
}

func TestParentCancellationStopsIdleProtocol(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	b, _ := New(18765, WithToken(testSessionToken))
	done := make(chan error, 1)
	go func() { done <- b.Serve(ctx, input, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		input.Close()
		<-done
		t.Fatal("parent cancellation left idle stdio blocked")
	}
}

func TestRequestIDsUseDecodedIdentity(t *testing.T) {
	if idKey(json.RawMessage(`"abc"`)) != idKey(json.RawMessage(`"\u0061bc"`)) {
		t.Fatal("escaped string ID not canonicalized")
	}
	if idKey(json.RawMessage(`1`)) == idKey(json.RawMessage(`"1"`)) {
		t.Fatal("string and integer IDs collide")
	}
	if !validID(json.RawMessage(`9223372036854775807`)) || validID(json.RawMessage(`1.5`)) {
		t.Fatal("integer ID bounds incorrect")
	}
}
