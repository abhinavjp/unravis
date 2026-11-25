package codemode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/mcp"
)

// everything the case holds about a run, as one text, for the sweeps below: the run row, every call row and kept copy, every chain row.
func (e *env) everything(runID string) string {
	e.t.Helper()
	var b strings.Builder
	run := e.getRun(runID)
	j, _ := json.Marshal(run)
	b.Write(j)
	for _, c := range e.calls(runID) {
		j, _ := json.Marshal(c)
		b.Write(j)
		if c.BlobState == casestore.BlobKept {
			blob, err := e.cas.CallResult(context.Background(), runID, c.Seq)
			if err != nil {
				e.t.Fatal(err)
			}
			b.Write(blob)
		}
	}
	rows, err := e.cas.ChainRows(context.Background(), 0, 1000)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range rows {
		b.WriteString(r.RecordJSON)
	}
	return b.String()
}

// TestHiddenModeNoRawIdentifiers: a run that calls the breakdown by user id, user agent and (refused) address on the real tools. The
// tools hide ids and agents as hidden-<rank>; nothing the script sees, returns, prints or leaves in the case holds a raw address or an
// id.
func TestHiddenModeNoRawIdentifiers(t *testing.T) {
	var answers [3]json.RawMessage
	e := newEnv(t, func(w *fakeWorker) {
		cmp, err := w.Call("audit_compare", map[string]any{"query": demoQuery(), "limit": 3})
		if err != nil {
			t.Error(err)
			return
		}
		var c struct {
			ResultID string `json:"resultId"`
		}
		_ = json.Unmarshal(cmp, &c)
		for i, dim := range []string{"userId", "userAgent", "userIp"} {
			// includeIp is the script asking for addresses: ignored, the tool refuses.
			answers[i], _ = w.Call("audit_get_breakdown", map[string]any{"resultId": c.ResultID, "dim": dim, "includeIp": true})
		}
		w.doneLogs(map[string]any{"users": json.RawMessage(answers[0])}, "users: "+string(answers[0][:200]))
	})
	res := e.run(`return 1`)
	e.wantStatus(res, casestore.RunOK, "")
	if !strings.Contains(string(answers[0]), `"hidden-1"`) || !strings.Contains(string(answers[1]), `"hidden-1"`) {
		t.Errorf("user ids and agents are not hidden: %.300s", answers[0])
	}
	var refused struct {
		Error *struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(answers[2], &refused)
	if refused.Error == nil || refused.Error.Code != mcp.WirePermissionRequired {
		t.Errorf("the address dimension was not refused: %.300s", answers[2])
	}
	all := e.everything(res.RunID) + string(res.Result) + strings.Join(res.Logs, "\n") + string(answers[0]) + string(answers[1]) + string(answers[2])
	if m := ipPattern.FindString(all); m != "" {
		t.Errorf("an address %q is in what the run saw or left behind", m)
	}
	if res.Identifiers != "hidden" || e.getRun(res.RunID).AllowIdentifiers {
		t.Error("the run does not say it was hidden")
	}
	calls := e.calls(res.RunID)
	if len(calls) != 4 || calls[3].Status != casestore.CallError {
		t.Errorf("calls: %+v", calls)
	}
	e.verify()
}

// TestAllowModeShowsIdentifiers: with the operator's permission the same run sees real addresses, and says so in its row.
func TestAllowModeShowsIdentifiers(t *testing.T) {
	var ips json.RawMessage
	e := newEnv(t, func(w *fakeWorker) {
		cmp, _ := w.Call("audit_compare", map[string]any{"query": demoQuery(), "limit": 3})
		var c struct {
			ResultID string `json:"resultId"`
		}
		_ = json.Unmarshal(cmp, &c)
		ips, _ = w.Call("audit_get_breakdown", map[string]any{"resultId": c.ResultID, "dim": "userIp", "includeIp": true})
		w.done(json.RawMessage(ips))
	}, withOperatorIdentifiers)
	res, err := e.runReq(RunRequest{Code: "return 1", AllowIdentifiers: true})
	if err != nil {
		t.Fatal(err)
	}
	e.wantStatus(res, casestore.RunOK, "")
	if ipPattern.FindString(string(ips)) == "" {
		t.Fatalf("allow mode did not show addresses: %.300s", ips)
	}
	row := e.getRun(res.RunID)
	if !row.AllowIdentifiers || res.Identifiers != "shown" {
		t.Errorf("the run does not say identifiers were shown: %+v", row)
	}
	e.verify()
}

// Hostile host text: addresses, a connection string, a token look-alike. What reaches the script, the result, the logs and the case holds
// none of the raw values, and an address has one token for the whole run.
func TestHostTextIsSweptScrubbedAndEscaped(t *testing.T) {
	const addr = "10.20.30.40"
	var seen, errAnswer json.RawMessage
	e := newEnv(t, func(w *fakeWorker) {
		seen, _ = w.Call("audit_list_sources", map[string]any{})
		errAnswer, _ = w.Call("audit_get_guide", map[string]any{})
		// The script writes the same address itself, and one it made up.
		w.doneLogs(map[string]any{"mine": "192.168.7.8", "again": addr, "tok": "[IP_1]"}, "saw "+addr+" and 192.168.7.8", "Server=db1;Password=hunter2")
	}, withHost(func(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
		if tool == "audit_get_guide" {
			return mcp.ErrorEnvelope(tool, mcp.WireBackendUnavailable, "login failed for sa at Server=db1;Password=hunter2 from "+addr, "retry from "+addr), true
		}
		return mustJSON(map[string]any{"schemaVersion": 1, "tool": tool, "sources": []string{"host " + addr, "[IP_1]", "again " + addr}, "note": "ok"}), false
	}))
	res := e.run(`return 1`)
	e.wantStatus(res, casestore.RunOK, "")

	if strings.Contains(string(seen), addr) || !strings.Contains(string(seen), `host [IP_1]`) || !strings.Contains(string(seen), `again [IP_1]`) {
		t.Errorf("the script was told: %s", seen)
	}
	if !strings.Contains(string(seen), `"[_IP_1]"`) {
		t.Errorf("a token look-alike in host data was not escaped: %s", seen)
	}
	for _, leak := range []string{addr, "hunter2", "Server=db1"} {
		if strings.Contains(string(errAnswer), leak) {
			t.Errorf("the error answer leaks %q: %s", leak, errAnswer)
		}
	}
	var envErr struct {
		Error struct{ Code string } `json:"error"`
	}
	if json.Unmarshal(errAnswer, &envErr) != nil || envErr.Error.Code != mcp.WireBackendUnavailable {
		t.Errorf("the error answer lost its code: %s", errAnswer)
	}
	out := string(res.Result)
	if strings.Contains(out, "192.168.7.8") || strings.Contains(out, addr) {
		t.Errorf("the result leaks: %s", out)
	}
	if !strings.Contains(out, "[IP_OUT]") || !strings.Contains(out, `"tok":"[IP_1]"`) {
		t.Errorf("result %s", out)
	}
	logs := strings.Join(res.Logs, "\n")
	if strings.Contains(logs, "192.168.7.8") || strings.Contains(logs, addr) || strings.Contains(logs, "hunter2") {
		t.Errorf("the logs leak: %s", logs)
	}
	all := e.everything(res.RunID)
	for _, leak := range []string{addr, "192.168.7.8", "hunter2"} {
		if strings.Contains(all, leak) {
			t.Errorf("the case holds %q", leak)
		}
	}
	// A call row in hidden mode keeps what the script saw (swept), not what the host said.
	rows := e.calls(res.RunID)
	if len(rows) != 2 || rows[1].Status != casestore.CallError {
		t.Fatalf("rows %+v", rows)
	}
	e.verify()
}

func TestScriptErrorTextIsScrubbed(t *testing.T) {
	e := newEnv(t, func(w *fakeWorker) {
		w.doneErr(CodeScriptError, `TypeError: boom at C:\Users\bob\secret\x.ts with Password=hunter2 from 172.16.4.5`)
	})
	res := e.run(`throw 1`)
	e.wantStatus(res, casestore.RunError, CodeScriptError)
	for _, leak := range []string{"hunter2", `C:\Users\bob`, "172.16.4.5"} {
		if strings.Contains(res.Error.Message, leak) || strings.Contains(e.everything(res.RunID), leak) {
			t.Errorf("the error text leaks %q: %s", leak, res.Error.Message)
		}
	}
	if !strings.Contains(res.Error.Message, "boom") {
		t.Errorf("the message is gone: %s", res.Error.Message)
	}
}

// The script's own words about itself never carry a code the worker made up.
func TestWorkerCannotInventErrorCodes(t *testing.T) {
	for _, code := range []string{"denied", "no_case", "cancelled", "worker_crashed", "anything", ""} {
		e := newEnv(t, func(w *fakeWorker) { w.doneErr(code, "x") })
		res := e.run(`1`)
		e.wantStatus(res, casestore.RunError, CodeBadFrame)
	}
	// The five codes the worker may use map to their statuses.
	for code, status := range map[string]string{CodeTicks: casestore.RunLimitExceeded, CodeOOM: casestore.RunLimitExceeded, CodeStack: casestore.RunLimitExceeded,
		CodeOutputTooLarge: casestore.RunLimitExceeded, CodeScriptError: casestore.RunError} {
		e := newEnv(t, func(w *fakeWorker) { w.doneErr(code, "x") })
		res := e.run(`1`)
		e.wantStatus(res, status, code)
	}
}
