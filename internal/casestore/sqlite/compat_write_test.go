package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// TestCompatWriteCase runs in the branch under test (BL-573, see compat_test.go): it makes a case with audit
// runs, calls, a chain and the code.hash_key through the code-run API of THIS build, imports a few events,
// closes it, and records what the previous build must find. It is not copied to the previous tree: it needs the
// code-run API, which only this build is sure to have.
func TestCompatWriteCase(t *testing.T) {
	dir := os.Getenv(compatWriteEnv)
	if dir == "" {
		t.Skip(compatWriteEnv + " is not set: this test runs from tools/ci/compat-upgrade.sh")
	}
	ctx := context.Background()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := New(compatOptions(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Create(ctx, "Compat")
	if err != nil {
		t.Fatal(err)
	}
	oc, err := s.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := oc.(*Case)
	t.Cleanup(func() { _ = c.Close() })

	compatImport(t, c, "before-upgrade", compatWriteEvts)

	run := func(in casestore.RunStart, calls []casestore.CallRecord, end casestore.RunEnd) {
		t.Helper()
		if in.Surface == "" {
			in.Surface = "cli"
		}
		r, err := c.BeginRun(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range calls {
			if err := c.RecordCall(ctx, r.RunID, rec); err != nil {
				t.Fatal(err)
			}
		}
		if end.Status == "" {
			end.Status = casestore.RunOK
		}
		if _, err := c.EndRun(ctx, r.RunID, end); err != nil {
			t.Fatal(err)
		}
	}
	rec := func(seq int, status, args, result string) casestore.CallRecord {
		return casestore.CallRecord{Seq: seq, DoneSeq: seq, Tool: "audit_compare", Args: []byte(args), Result: []byte(result), Status: status, DurationMS: int64(10 + seq)}
	}
	keep := casestore.RunStart{KeepResults: true, CallerID: "unravis-cli", Reason: "check the slow routes", Seed: "c0ffee", ClockMS: 1700000000000,
		ToolSchemaHash: strings.Repeat("e", 64), Source: "const a = await unravis.audit_compare({});\nreturn a;"}
	ok := casestore.RunEnd{Status: casestore.RunOK, Result: []byte(`{"slow":[1,2,3]}`), SandboxMode: "lpac", Runtime: "wazero-interp",
		WorkerSHA256: strings.Repeat("d", 64), StrippedJSHash: strings.Repeat("f", 64), ConsoleLogs: "log line", DurationMS: 55, ScriptMS: 7}
	run(keep, []casestore.CallRecord{rec(1, "ok", `{"a":1}`, `{"r":"one"}`), rec(2, "ok", `{"a":2}`, `{"r":"two"}`)}, ok)
	keep2 := keep
	keep2.Source = "return await unravis.audit_get_slowest({});"
	run(keep2, []casestore.CallRecord{rec(1, "error", `{}`, `{"error":"nope"}`)}, casestore.RunEnd{Status: casestore.RunError, ErrorCode: "script_error", ErrorText: "boom"})
	run(casestore.RunStart{Surface: "mcp", CallerID: "claude", CallerDeclared: true, Source: "return 3"}, []casestore.CallRecord{rec(1, "ok", `{"z":0}`, `{"r":3}`)}, ok)

	if rep, err := c.VerifyCodeChain(ctx, nil); err != nil || !rep.OK {
		t.Fatalf("the branch's own chain does not verify: %+v %v", rep, err)
	}
	facts := compatQuery(t, c)
	facts["case_id"] = string(info.ID)
	evs, err := c.Events(ctx, casestore.EventQuery{Limit: 100})
	if err != nil || len(evs) != compatWriteEvts {
		t.Fatalf("Events = %d %v", len(evs), err)
	}
	facts["events"] = "5"
	if facts["code_runs"] != "3" || facts["code_run_calls"] != "4" || facts["code_chain"] != "10" || facts["hash_key_rows"] != "1" {
		t.Fatalf("the writer did not make the audit data it meant to: %v", facts)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// No CloseCase here: that starts the retention clock and the case then refuses imports, and the second build
	// has to import into it. The reader calls CloseCase itself.
	if err := os.WriteFile(filepath.Join(dir, compatExpect), compatEncode(facts), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, compatIDFile), []byte(info.ID), 0o600); err != nil {
		t.Fatal(err)
	}
}
