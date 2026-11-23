package sqlite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

// BL-369: the audit record on a real, sealed SQLite case file. The damage tests
// act as someone who holds the case key (they open the same database and write
// SQL), because that is the attacker the chain has to be judged against.

var cctx = context.Background()

func mustRun(t *testing.T, c *Case, in casestore.RunStart, calls []casestore.CallRecord, end casestore.RunEnd) casestore.CodeRun {
	t.Helper()
	if in.Surface == "" {
		in.Surface = "cli"
	}
	run, err := c.BeginRun(cctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range calls {
		if err := c.RecordCall(cctx, run.RunID, rec); err != nil {
			t.Fatal(err)
		}
	}
	if end.Status == "" {
		end.Status = casestore.RunOK
	}
	fin, err := c.EndRun(cctx, run.RunID, end)
	if err != nil {
		t.Fatal(err)
	}
	return fin
}

func call(seq int, status, args, result string) casestore.CallRecord {
	return casestore.CallRecord{Seq: seq, DoneSeq: seq, Tool: "audit_compare", Args: []byte(args), Result: []byte(result), Status: status, DurationMS: int64(10 + seq)}
}

// seedRuns writes three sealed runs, six chain rows, four calls.
func seedRuns(t *testing.T, c *Case) []casestore.CodeRun {
	t.Helper()
	keep := casestore.RunStart{KeepResults: true, CallerID: "unravis-cli", Reason: "check the slow routes", Seed: "c0ffee", ClockMS: 1700000000000,
		ToolSchemaHash: strings.Repeat("e", 64), Source: "const a = await unravis.audit_compare({});\nreturn a;"}
	end := casestore.RunEnd{Status: casestore.RunOK, Result: []byte(`{"slow":[1,2,3]}`), SandboxMode: "lpac", Runtime: "wazero-interp",
		WorkerSHA256: strings.Repeat("d", 64), StrippedJSHash: strings.Repeat("f", 64), ConsoleLogs: "log line", DurationMS: 55, ScriptMS: 7}
	r1 := mustRun(t, c, keep, []casestore.CallRecord{call(1, "ok", `{"a":1}`, `{"r":"one"}`), call(2, "ok", `{"a":2}`, `{"r":"two"}`)}, end)
	keep2 := keep
	keep2.Source = "return await unravis.audit_get_slowest({});"
	r2 := mustRun(t, c, keep2, []casestore.CallRecord{call(1, "error", `{}`, `{"error":"nope"}`)}, casestore.RunEnd{Status: casestore.RunError, ErrorCode: "script_error", ErrorText: "boom"})
	r3 := mustRun(t, c, casestore.RunStart{Surface: "mcp", CallerID: "claude", CallerDeclared: true, Source: "return 3"}, []casestore.CallRecord{call(1, "ok", `{"z":0}`, `{"r":3}`)}, end)
	return []casestore.CodeRun{r1, r2, r3}
}

func newRunCase(t *testing.T) (*Store, *Case, casestore.CaseInfo) {
	t.Helper()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Audit")
	return s, mustOpen(t, s, info.ID), info
}

func dropGuards(t *testing.T, c *Case) {
	t.Helper()
	for _, g := range codeGuards {
		if _, err := c.db.ExecContext(cctx, "DROP TRIGGER IF EXISTS "+g.name); err != nil {
			t.Fatal(err)
		}
	}
}

func verify(t *testing.T, c *Case, anchor *casestore.ChainAnchor) casestore.ChainReport {
	t.Helper()
	rep, err := c.VerifyCodeChain(cctx, anchor)
	if err != nil {
		t.Fatalf("VerifyCodeChain: %v", err)
	}
	return rep
}

func rawSQL(t *testing.T, c *Case, q string, args ...any) {
	t.Helper()
	if _, err := c.db.ExecContext(cctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func problemChecks(rep casestore.ChainReport) []string {
	var out []string
	for _, p := range rep.Problems {
		out = append(out, strconv.FormatInt(p.N, 10)+":"+p.Check)
	}
	return out
}

func hasCheck(rep casestore.ChainReport, check string) bool {
	for _, p := range rep.Problems {
		if p.Check == check {
			return true
		}
	}
	return false
}

func mustBroken(t *testing.T, rep casestore.ChainReport, wantFirstN int64, checks ...string) {
	t.Helper()
	if rep.OK || len(rep.Problems) == 0 {
		t.Fatalf("damage not noticed: %+v", rep)
	}
	if wantFirstN >= 0 && rep.Problems[0].N != wantFirstN {
		t.Errorf("first broken row = %d, want %d (%v)", rep.Problems[0].N, wantFirstN, problemChecks(rep))
	}
	for _, c := range checks {
		if !hasCheck(rep, c) {
			t.Errorf("check %q not raised: %v", c, problemChecks(rep))
		}
	}
}

func TestChainVerifies(t *testing.T) {
	_, c, _ := newRunCase(t)
	runs := seedRuns(t, c)
	rep := verify(t, c, nil)
	if !rep.OK || rep.Rows != 10 || rep.Runs != 3 || rep.OpenRuns != 0 || rep.HeadN != 10 || len(rep.Head) != 64 || len(rep.Warnings) != 0 {
		t.Fatalf("verify = %+v", rep)
	}
	rows, err := c.ChainRows(cctx, 0, 0)
	if err != nil || len(rows) != 10 || rows[9].RecordHash != rep.Head {
		t.Fatalf("ChainRows: %v %v", len(rows), err)
	}
	for i, r := range rows {
		prev := casestore.ChainZeroHash
		if i > 0 {
			prev = rows[i-1].RecordHash
		}
		if r.N != int64(i+1) || r.PrevHash != prev || r.RecordHash != casestore.SHA256Hex([]byte(r.RecordJSON)) {
			t.Errorf("row %d = %+v", i, r)
		}
	}
	// The record carries no free text of the run beyond what the spec lists: the
	// source is in the record only as a hash.
	if strings.Contains(rows[0].RecordJSON, "audit_compare") || !strings.Contains(rows[0].RecordJSON, casestore.SHA256Hex([]byte(runs[0].Source))) {
		t.Errorf("begin record: %s", rows[0].RecordJSON)
	}
	// The head is kept beside the chain too.
	var v string
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k = ?", codeHeadKey).Scan(&v); err != nil || v != "10:"+rep.Head {
		t.Errorf("meta head = %q %v", v, err)
	}
}

// A case without the audit tables reads as empty; the first run makes them, with no
// schema bump. Everything this build does to a case apart from the audit calls is
// code this change does not touch (the diff holds no edit of the import, read,
// integrity, close, export or erase paths), and that is the code a previous build
// runs on the case: so a case holding audit rows, triggers and a chain must still
// import, read, check, close and reopen through it, without disturbing the audit rows.
// What no unit test can show is a real previous binary; that stays a manual check
// (see the BL-369 handoff).
func TestCaseWithAuditTablesStillWorksForTheExistingCode(t *testing.T) {
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Compat")
	c := mustOpen(t, s, info.ID)
	ctx := cctx
	if runs, err := c.ListRuns(ctx, casestore.RunQuery{}); err != nil || runs != nil {
		t.Fatalf("ListRuns on a case without the tables = %v %v", runs, err)
	}
	if rep := verify(t, c, nil); !rep.OK || rep.Rows != 0 || len(rep.Warnings) != 0 {
		t.Fatalf("verify = %+v", rep)
	}
	if n, err := c.CloseOrphanRuns(ctx); err != nil || n != 0 {
		t.Fatalf("CloseOrphanRuns = %d %v", n, err)
	}
	if st, err := c.CodeRunStats(ctx); err != nil || st != (casestore.CodeStats{}) {
		t.Fatalf("stats = %+v %v", st, err)
	}
	var before string
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k='schema_version'").Scan(&before); err != nil || before != strconv.Itoa(schemaVersion) {
		t.Fatalf("schema_version = %q %v", before, err)
	}
	seedRuns(t, c)
	var after, marker string
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k='schema_version'").Scan(&after); err != nil || after != before {
		t.Fatalf("schema version %q -> %q: the audit tables must not bump it", before, after)
	}
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k='code.schema'").Scan(&marker); err != nil || marker != "1" {
		t.Fatalf("the audit tables carry no version marker: %q %v", marker, err)
	}
	head := verify(t, c, nil)

	// What the existing code does with such a case.
	mustImport(t, c, "after-audit", 4)
	if imports, events, _ := counts(t, c); imports != 1 || events != 4 {
		t.Fatalf("import into a case with audit tables: %d imports %d events", imports, events)
	}
	if got, err := c.Events(ctx, casestore.EventQuery{Limit: 10}); err != nil || len(got) != 4 {
		t.Fatalf("Events = %d %v", len(got), err)
	}
	if err := c.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck: %v", err)
	}
	if err := c.SetWorkspaceBlob(ctx, []byte(`{"a":1}`)); err != nil {
		t.Fatalf("a neighbouring write: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseCase(ctx, info.ID); err != nil {
		t.Fatalf("CloseCase: %v", err)
	}
	c2 := mustOpen(t, newStoreAt(t, root, s.opt.Protector), info.ID)
	if err := c2.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck after reopen: %v", err)
	}
	if rep := verify(t, c2, nil); !rep.OK || rep.Head != head.Head || rep.Rows != head.Rows {
		t.Fatalf("the audit record changed under the existing code: %+v (was %+v)", rep, head)
	}
	if refusesSchema(schemaVersion, schemaVersion) || !refusesSchema(schemaVersion+1, schemaVersion) {
		t.Fatal("the schema refusal rule changed")
	}
	rows, err := c2.db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'code\\_%' ESCAPE '\\' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "code_chain,code_run_calls,code_runs" {
		t.Errorf("tables = %v", names)
	}
}

func TestChainTriggersBlockUpdateDelete(t *testing.T) {
	_, c, _ := newRunCase(t)
	runs := seedRuns(t, c)
	id := runs[0].RunID
	blocked := map[string]string{
		"update chain":         "UPDATE code_chain SET record_json = 'x' WHERE n = 1",
		"delete chain":         "DELETE FROM code_chain WHERE n = 6",
		"delete all chain":     "DELETE FROM code_chain",
		"update sealed run":    "UPDATE code_runs SET status = 'ok', error_text = 'edited' WHERE run_id = '" + id + "'",
		"delete run":           "DELETE FROM code_runs WHERE run_id = '" + id + "'",
		"update call":          "UPDATE code_run_calls SET result_hash = '" + strings.Repeat("0", 64) + "' WHERE run_id = '" + id + "'",
		"delete call":          "DELETE FROM code_run_calls WHERE run_id = '" + id + "' AND seq = 2",
		"call into sealed run": "INSERT INTO code_run_calls(run_id, seq, done_seq, tool_name, args_hash, result_hash, blob_state, status) VALUES('" + id + "', 9, 9, 't', 'a', 'b', 'off', 'ok')",
		"chain wrong n":        "INSERT INTO code_chain(n, run_id, phase, record_json, prev_hash, record_hash) VALUES(99, 'x', 'end', '{}', '" + runsHead(t, c) + "', 'h99')",
		"chain wrong prev":     "INSERT INTO code_chain(n, run_id, phase, record_json, prev_hash, record_hash) VALUES(7, 'x', 'end', '{}', 'bad', 'h7')",
		"chain reuses a run":   "INSERT INTO code_chain(n, run_id, phase, record_json, prev_hash, record_hash) VALUES(7, '" + id + "', 'end', '{}', '" + runsHead(t, c) + "', 'h7')",
	}
	for name, q := range blocked {
		if _, err := c.db.ExecContext(cctx, q); err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
	if rep := verify(t, c, nil); !rep.OK || rep.Rows != 10 {
		t.Fatalf("a refused edit changed the record: %+v", rep)
	}
	// A running run may change, but never its identity or source.
	open, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"source":     "UPDATE code_runs SET source_ts = 'y' WHERE run_id = '" + open.RunID + "'",
		"identifier": "UPDATE code_runs SET allow_identifiers = 1 WHERE run_id = '" + open.RunID + "'",
		"created":    "UPDATE code_runs SET created_at_utc = 1 WHERE run_id = '" + open.RunID + "'",
		"surface":    "UPDATE code_runs SET surface = 'ui' WHERE run_id = '" + open.RunID + "'",
	} {
		if _, err := c.db.ExecContext(cctx, q); err == nil {
			t.Errorf("a running run's %s could be changed", name)
		}
	}
	if _, err := c.db.ExecContext(cctx, "UPDATE code_runs SET status = 'bogus' WHERE run_id = ?", open.RunID); err == nil {
		t.Error("a status outside the list was accepted")
	}
}

func runsHead(t *testing.T, c *Case) string {
	t.Helper()
	return verify(t, c, nil).Head
}

// Every column of a run row is covered: change one byte of any and verify names it.
func TestOneByteEditBreaksVerifyRunRow(t *testing.T) {
	// begin-covered columns break row 1 (the run's begin record), the others row 4 (its end record).
	cols := []struct {
		name  string
		text  bool
		first int64
	}{
		{"created_at_utc", false, 1}, {"surface", true, 1}, {"caller_id", true, 1}, {"caller_declared", false, 1},
		{"allow_identifiers", false, 1}, {"keep_results", false, 1}, {"tool_schema_hash", true, 1}, {"reason", true, 1},
		{"source_ts", true, 1}, {"seed", true, 1}, {"clock_ms", false, 1},
		{"ended_at_utc", false, 4}, {"sandbox_mode", true, 4}, {"runtime", true, 4}, {"worker_sha256", true, 4},
		{"stripped_js_hash", true, 4}, {"error_code", true, 4}, {"error_text", true, 4}, {"result_hash", true, 4},
		{"result_bytes", false, 4}, {"result_preview", true, 4}, {"console_logs", true, 4}, {"call_count", false, 4},
		{"duration_ms", false, 4}, {"script_ms", false, 4},
	}
	for _, col := range cols {
		t.Run(col.name, func(t *testing.T) {
			_, c, _ := newRunCase(t)
			runs := seedRuns(t, c)
			dropGuards(t, c)
			if col.text {
				rawSQL(t, c, "UPDATE code_runs SET "+col.name+" = "+col.name+" || 'x' WHERE run_id = ?", runs[0].RunID)
			} else {
				rawSQL(t, c, "UPDATE code_runs SET "+col.name+" = "+col.name+" + 1 WHERE run_id = ?", runs[0].RunID)
			}
			rep := verify(t, c, nil)
			mustBroken(t, rep, col.first)
			if rep.Problems[0].RunID != runs[0].RunID {
				t.Errorf("problem names run %q", rep.Problems[0].RunID)
			}
			if !hasCheck(rep, casestore.CheckBeginMismatch) && !hasCheck(rep, casestore.CheckEndMismatch) && !hasCheck(rep, casestore.CheckUnreadable) {
				t.Errorf("checks = %v", problemChecks(rep))
			}
		})
	}
	t.Run("status running again", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "UPDATE code_runs SET status = 'running', ended_at_utc = 0 WHERE run_id = ?", runs[0].RunID)
		mustBroken(t, verify(t, c, nil), 4, casestore.CheckRunStatus)
	})
	t.Run("sealed status flipped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "UPDATE code_runs SET status = 'error' WHERE run_id = ?", runs[0].RunID)
		mustBroken(t, verify(t, c, nil), 4, casestore.CheckEndMismatch)
	})
}

func TestOneByteEditBreaksVerifyCallRowAndBlob(t *testing.T) {
	type edit struct {
		name  string
		sql   string
		check string
		first int64 // the chain row where the damage shows (the call's own record, or the next one when the call is gone)
	}
	edits := []edit{
		{"args_json", "UPDATE code_run_calls SET args_json = args_json || ' ' WHERE seq = 1", casestore.CheckCallRow, 2},
		{"args_json same hash", `UPDATE code_run_calls SET args_json = '{"a":9}' WHERE seq = 1`, casestore.CheckCallRow, 2},
		{"args_hash", "UPDATE code_run_calls SET args_hash = '" + strings.Repeat("1", 64) + "' WHERE seq = 1", casestore.CheckCallRow, 2},
		{"args_state", "UPDATE code_run_calls SET args_state = 'withheld' WHERE seq = 1", casestore.CheckCallRow, 2},
		{"tool_name", "UPDATE code_run_calls SET tool_name = 'audit_get_guide' WHERE seq = 1", casestore.CheckCallMismatch, 2},
		{"result_hash", "UPDATE code_run_calls SET result_hash = '" + strings.Repeat("2", 64) + "' WHERE seq = 1", casestore.CheckCallBlob, 2},
		{"result_bytes", "UPDATE code_run_calls SET result_bytes = result_bytes + 1 WHERE seq = 1", casestore.CheckCallBlob, 2},
		{"blob_state", "UPDATE code_run_calls SET blob_state = 'off' WHERE seq = 1", casestore.CheckCallBlob, 2},
		{"blob dropped", "UPDATE code_run_calls SET result_blob = NULL WHERE seq = 1", casestore.CheckCallBlob, 2},
		{"status", "UPDATE code_run_calls SET status = 'denied' WHERE seq = 1", casestore.CheckCallMismatch, 2},
		{"duration_ms", "UPDATE code_run_calls SET duration_ms = duration_ms + 1 WHERE seq = 1", casestore.CheckCallMismatch, 2},
		{"done_seq", "UPDATE code_run_calls SET done_seq = 7 WHERE seq = 1", casestore.CheckCallMismatch, 2},
		{"seq", "UPDATE code_run_calls SET seq = 5 WHERE seq = 1", casestore.CheckCallMissing, 2},
		{"delete call", "DELETE FROM code_run_calls WHERE seq = 2", casestore.CheckCallMissing, 3},
		{"move call to another run", "UPDATE code_run_calls SET run_id = (SELECT run_id FROM code_runs ORDER BY rowid DESC LIMIT 1), seq = 8, done_seq = 8 WHERE seq = 2", casestore.CheckCallMissing, 3},
	}
	for _, e := range edits {
		t.Run(e.name, func(t *testing.T) {
			_, c, _ := newRunCase(t)
			runs := seedRuns(t, c)
			dropGuards(t, c)
			rawSQL(t, c, strings.Replace(e.sql, "WHERE seq", "WHERE run_id = '"+runs[0].RunID+"' AND seq", 1))
			rep := verify(t, c, nil)
			mustBroken(t, rep, e.first, e.check)
		})
	}
	t.Run("calls swapped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "UPDATE code_run_calls SET seq = 99 WHERE run_id = ? AND seq = 1", runs[0].RunID)
		rawSQL(t, c, "UPDATE code_run_calls SET seq = 1 WHERE run_id = ? AND seq = 2", runs[0].RunID)
		rawSQL(t, c, "UPDATE code_run_calls SET seq = 2 WHERE run_id = ? AND seq = 99", runs[0].RunID)
		mustBroken(t, verify(t, c, nil), 2, casestore.CheckCallMismatch)
	})
	t.Run("one byte of the stored copy", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		var blob []byte
		if err := c.db.QueryRow("SELECT result_blob FROM code_run_calls WHERE run_id = ? AND seq = 1", runs[0].RunID).Scan(&blob); err != nil {
			t.Fatal(err)
		}
		blob[len(blob)/2] ^= 0x01
		rawSQL(t, c, "UPDATE code_run_calls SET result_blob = ? WHERE run_id = ? AND seq = 1", blob, runs[0].RunID)
		mustBroken(t, verify(t, c, nil), 2, casestore.CheckCallBlob)
		if _, err := c.CallResult(cctx, runs[0].RunID, 1); !errors.Is(err, casestore.ErrCodeTampered) {
			t.Errorf("CallResult of an edited copy = %v", err)
		}
	})
	t.Run("a copy that expands without limit", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		enc, _ := zstd.NewWriter(nil)
		bomb := enc.EncodeAll(make([]byte, 96<<20), nil)
		if len(bomb) > 1<<20 {
			t.Fatalf("bomb is %d bytes", len(bomb))
		}
		rawSQL(t, c, "UPDATE code_run_calls SET result_blob = ? WHERE run_id = ? AND seq = 1", bomb, runs[0].RunID)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		rep := verify(t, c, nil)
		runtime.ReadMemStats(&m1)
		mustBroken(t, rep, 2, casestore.CheckCallBlob)
		if grown := m1.TotalAlloc - m0.TotalAlloc; grown > 48<<20 {
			t.Errorf("verifying a decompression bomb allocated %d MiB", grown>>20)
		}
		if _, err := c.CallResult(cctx, runs[0].RunID, 1); !errors.Is(err, casestore.ErrCodeTampered) {
			t.Errorf("CallResult of a bomb = %v", err)
		}
	})
}

func TestOneByteEditBreaksVerifyChainRow(t *testing.T) {
	edits := map[string]string{
		"record_json":      "UPDATE code_chain SET record_json = record_json || ' ' WHERE n = 3",
		"record_json byte": "UPDATE code_chain SET record_json = replace(record_json, '\"v\":1', '\"v\":2') WHERE n = 3",
		"record_hash":      "UPDATE code_chain SET record_hash = '" + strings.Repeat("3", 64) + "' WHERE n = 3",
		"prev_hash":        "UPDATE code_chain SET prev_hash = '" + strings.Repeat("4", 64) + "' WHERE n = 3",
		"run_id column":    "UPDATE code_chain SET run_id = '00000000-0000-7000-8000-00000000f00d' WHERE n = 3",
		"first prev_hash":  "UPDATE code_chain SET prev_hash = '" + strings.Repeat("5", 64) + "' WHERE n = 1",
	}
	for name, q := range edits {
		t.Run(name, func(t *testing.T) {
			_, c, _ := newRunCase(t)
			seedRuns(t, c)
			dropGuards(t, c)
			rawSQL(t, c, q)
			rep := verify(t, c, nil)
			first := int64(3)
			if strings.HasPrefix(name, "first") {
				first = 1
			}
			mustBroken(t, rep, first)
		})
	}
	t.Run("record rewritten and rehashed but not relinked", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		dropGuards(t, c)
		var rec string
		_ = c.db.QueryRow("SELECT record_json FROM code_chain WHERE n = 1").Scan(&rec)
		forged := strings.Replace(rec, `"surface":"cli"`, `"surface":"ui"`, 1)
		rawSQL(t, c, "UPDATE code_chain SET record_json = ?, record_hash = ? WHERE n = 1", forged, casestore.SHA256Hex([]byte(forged)))
		// The row now hashes, but the run row disagrees and the next row's link is stale.
		mustBroken(t, verify(t, c, nil), 1, casestore.CheckBeginMismatch)
		if rep := verify(t, c, nil); !hasCheck(rep, casestore.CheckPrevHash) {
			t.Errorf("checks = %v", problemChecks(rep))
		}
	})
}

func TestDeletedMiddleRowIsFound(t *testing.T) {
	t.Run("chain row", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE n = 5")
		rep := verify(t, c, nil)
		mustBroken(t, rep, 6, casestore.CheckChainGap, casestore.CheckPrevHash)
		if !hasCheck(rep, casestore.CheckRunUnchained) {
			t.Errorf("a run lost its begin record unnoticed: %v", problemChecks(rep))
		}
	})
	t.Run("the whole second run", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE run_id = ?", runs[1].RunID)
		rawSQL(t, c, "DELETE FROM code_run_calls WHERE run_id = ?", runs[1].RunID)
		rawSQL(t, c, "DELETE FROM code_runs WHERE run_id = ?", runs[1].RunID)
		mustBroken(t, verify(t, c, nil), 8, casestore.CheckChainGap, casestore.CheckPrevHash)
	})
	t.Run("renumbered to hide the gap", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE run_id = ?", runs[1].RunID)
		rawSQL(t, c, "UPDATE code_chain SET n = n - 3 WHERE n > 7")
		rep := verify(t, c, nil)
		mustBroken(t, rep, 5, casestore.CheckPrevHash, casestore.CheckBeginMismatch)
	})
}

func TestReorderedRowsAreFound(t *testing.T) {
	t.Run("chain rows swapped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "UPDATE code_chain SET n = 100 WHERE n = 3")
		rawSQL(t, c, "UPDATE code_chain SET n = 3 WHERE n = 4")
		rawSQL(t, c, "UPDATE code_chain SET n = 4 WHERE n = 100")
		mustBroken(t, verify(t, c, nil), 3, casestore.CheckPrevHash)
	})
	t.Run("two whole runs swapped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "UPDATE code_chain SET n = n + 100 WHERE n IN (5, 6, 7)")
		rawSQL(t, c, "UPDATE code_chain SET n = n - 3 WHERE n IN (8, 9, 10)")
		rawSQL(t, c, "UPDATE code_chain SET n = n - 97 WHERE n > 100")
		rep := verify(t, c, nil)
		mustBroken(t, rep, 5)
		if !hasCheck(rep, casestore.CheckPrevHash) {
			t.Errorf("checks = %v", problemChecks(rep))
		}
	})
}

func TestTruncatedTailIsFound(t *testing.T) {
	t.Run("last chain row only", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE n = 10")
		rep := verify(t, c, nil)
		mustBroken(t, rep, -1, casestore.CheckRunStatus, casestore.CheckHeadMismatch)
	})
	t.Run("the last run removed from every table", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE run_id = ?", runs[2].RunID)
		rawSQL(t, c, "DELETE FROM code_run_calls WHERE run_id = ?", runs[2].RunID)
		rawSQL(t, c, "DELETE FROM code_runs WHERE run_id = ?", runs[2].RunID)
		// A chain that looks whole is cut: only the head kept beside it, or one the user noted, can tell.
		mustBroken(t, verify(t, c, nil), 7, casestore.CheckHeadMismatch)
	})
	t.Run("and the head beside the chain rewritten too", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		noted := verify(t, c, nil) // the user wrote down n=10 and its hash
		anchor := &casestore.ChainAnchor{N: noted.HeadN, Hash: noted.Head}
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE run_id = ?", runs[2].RunID)
		rawSQL(t, c, "DELETE FROM code_run_calls WHERE run_id = ?", runs[2].RunID)
		rawSQL(t, c, "DELETE FROM code_runs WHERE run_id = ?", runs[2].RunID)
		var last casestore.ChainRow
		_ = c.db.QueryRow("SELECT n, record_hash FROM code_chain ORDER BY n DESC LIMIT 1").Scan(&last.N, &last.RecordHash)
		rawSQL(t, c, "UPDATE meta SET v = ? WHERE k = ?", fmt.Sprintf("%d:%s", last.N, last.RecordHash), codeHeadKey)
		// Honest limit: without a noted head this is a valid, shorter chain.
		if rep := verify(t, c, nil); !rep.OK || rep.HeadN != 7 {
			t.Fatalf("a cleanly cut chain is a valid chain, got %+v", rep)
		}
		mustBroken(t, verify(t, c, anchor), 10, casestore.CheckAnchorAhead)
	})
}

// Putting an older, valid copy of the case back is a valid chain, and is only
// found against a head noted after the copy was made.
func TestReplayedOldChainIsFoundByANotedHead(t *testing.T) {
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Replay")
	c := mustOpen(t, s, info.ID)
	mustRun(t, c, casestore.RunStart{Source: "return 1"}, nil, casestore.RunEnd{})
	mustRun(t, c, casestore.RunStart{Source: "return 2"}, nil, casestore.RunEnd{})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, string(info.ID))
	snap := t.TempDir()
	copyDir(t, dir, snap)

	c = mustOpen(t, s, info.ID)
	mustRun(t, c, casestore.RunStart{Source: "return 3"}, nil, casestore.RunEnd{})
	mustRun(t, c, casestore.RunStart{Source: "return 4"}, nil, casestore.RunEnd{})
	noted := verify(t, c, nil)
	if !noted.OK || noted.HeadN != 8 {
		t.Fatalf("setup: %+v", noted)
	}
	anchor := &casestore.ChainAnchor{N: noted.HeadN, Hash: noted.Head}
	early := &casestore.ChainAnchor{N: 4, Hash: mustRows(t, c)[3].RecordHash}
	if rep := verify(t, c, anchor); !rep.OK || !rep.AnchorChecked {
		t.Fatalf("the noted head against the same history: %+v", rep)
	}
	if rep := verify(t, c, early); !rep.OK {
		t.Fatalf("an earlier noted head against a later history: %+v", rep)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Roll the case back to the copy.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	copyDir(t, snap, dir)
	c = mustOpen(t, s, info.ID)
	rep := verify(t, c, nil)
	if !rep.OK || rep.HeadN != 4 {
		t.Fatalf("an older copy is a valid chain: %+v", rep)
	}
	mustBroken(t, verify(t, c, anchor), 8, casestore.CheckAnchorAhead)
	if rep := verify(t, c, early); !rep.OK {
		t.Errorf("the older head is still in the older copy: %+v", rep)
	}

	// A fork: the old copy goes on with other runs; a head noted on the real history no longer matches at its number.
	mustRun(t, c, casestore.RunStart{Source: "a different run"}, nil, casestore.RunEnd{})
	mustRun(t, c, casestore.RunStart{Source: "another different run"}, nil, casestore.RunEnd{})
	forked := verify(t, c, anchor)
	mustBroken(t, forked, 8, casestore.CheckAnchorMismatch)
}

func mustRows(t *testing.T, c *Case) []casestore.ChainRow {
	t.Helper()
	rows, err := c.ChainRows(cctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(to, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			copyDir(t, filepath.Join(from, e.Name()), filepath.Join(to, e.Name()))
			continue
		}
		b, err := os.ReadFile(filepath.Join(from, e.Name())) //nolint:gosec // a test copying its own temp folder
		if err != nil {
			t.Fatal(err)
		}
		werr := os.WriteFile(filepath.Join(to, e.Name()), b, 0o600) //nolint:gosec // a test copying its own temp folder
		if werr != nil {
			t.Fatal(werr)
		}
	}
}

// What a key holder can do, stated as a test so the limit is not a surprise:
// rebuilding every record and hash gives a chain that verifies, and only a head
// noted before the rebuild tells it apart.
func TestKeyHolderRebuildingTheWholeChainIsOnlyFoundByANotedHead(t *testing.T) {
	_, c, _ := newRunCase(t)
	runs := seedRuns(t, c)
	before := verify(t, c, nil)
	anchor := &casestore.ChainAnchor{N: before.HeadN, Hash: before.Head}
	dropGuards(t, c)

	// Rewrite run 1's source and rebuild the whole chain from the table rows.
	rawSQL(t, c, "UPDATE code_runs SET source_ts = 'return 666' WHERE run_id = ?", runs[0].RunID)
	rawSQL(t, c, "DELETE FROM code_chain")
	prev, n := casestore.ChainZeroHash, int64(0)
	for _, r := range runs {
		run, err := c.GetRun(cctx, r.RunID)
		if err != nil {
			t.Fatal(err)
		}
		calls, err := c.loadCalls(cctx, c.db, r.RunID, false)
		if err != nil {
			t.Fatal(err)
		}
		beginRow, err := casestore.BeginChainRow(run, n+1, prev)
		if err != nil {
			t.Fatal(err)
		}
		built := []casestore.ChainRow{beginRow}
		last := beginRow.RecordHash
		for i, call := range calls {
			row, err := casestore.CallChainRow(call, n+2+int64(i), last)
			if err != nil {
				t.Fatal(err)
			}
			built, last = append(built, row), row.RecordHash
		}
		// The end record covers the call rows and the (unchanged) run row.
		endRow, err := casestore.EndChainRow(run, calls, n+2+int64(len(calls)), last)
		if err != nil {
			t.Fatal(err)
		}
		built = append(built, endRow)
		for _, row := range built {
			rawSQL(t, c, "INSERT INTO code_chain(n, run_id, phase, seq, record_json, prev_hash, record_hash) VALUES(?,?,?,?,?,?,?)",
				row.N, row.RunID, row.Phase, row.Seq, row.RecordJSON, row.PrevHash, row.RecordHash)
			n, prev = row.N, row.RecordHash
		}
	}
	rawSQL(t, c, "UPDATE meta SET v = ? WHERE k = ?", fmt.Sprintf("%d:%s", n, prev), codeHeadKey)
	if rep := verify(t, c, nil); !rep.OK {
		t.Fatalf("a fully rebuilt chain verifies by design: %+v", rep)
	}
	mustBroken(t, verify(t, c, anchor), 10, casestore.CheckAnchorMismatch)
}

func TestDroppedGuardsAreReportedAsWarnings(t *testing.T) {
	_, c, _ := newRunCase(t)
	seedRuns(t, c)
	rawSQL(t, c, "DROP TRIGGER code_chain_no_delete")
	rep := verify(t, c, nil)
	if !rep.OK || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "code_chain_no_delete") {
		t.Fatalf("verify = %+v", rep)
	}
}

func TestPastedRowsAreFound(t *testing.T) {
	t.Run("a run row the chain never saw", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "INSERT INTO code_runs(run_id, created_at_utc, surface, source_ts, status) VALUES('00000000-0000-7000-8000-00000000beef', 1, 'cli', 'evil', 'ok')")
		mustBroken(t, verify(t, c, nil), 0, casestore.CheckRunUnchained)
	})
	t.Run("a call row on a sealed run", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "INSERT INTO code_run_calls(run_id, seq, done_seq, tool_name, args_hash, result_hash, blob_state, status) VALUES(?, 9, 9, 'audit_compare', ?, ?, 'off', 'ok')",
			runs[0].RunID, strings.Repeat("a", 64), strings.Repeat("b", 64))
		mustBroken(t, verify(t, c, nil), -1, casestore.CheckCallRow, casestore.CheckCallUnchained)
	})
	t.Run("a call row for no run", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "INSERT INTO code_run_calls(run_id, seq, done_seq, tool_name, args_hash, result_hash, blob_state, status) VALUES('00000000-0000-7000-8000-00000000cafe', 1, 1, 'audit_compare', ?, ?, 'off', 'ok')",
			strings.Repeat("a", 64), strings.Repeat("b", 64))
		mustBroken(t, verify(t, c, nil), 0, casestore.CheckCallsOrphan)
	})
	t.Run("a second end record for a run", func(t *testing.T) {
		// The schema refuses it (UNIQUE run and phase), even without the triggers.
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rows := mustRows(t, c)
		old := rows[3] // the end record of run 1
		forged := casestore.NewChainRow(11, runs[0].RunID, "end", 0, old.RecordJSON, rows[len(rows)-1].RecordHash)
		if _, err := c.db.ExecContext(cctx, "INSERT INTO code_chain(n, run_id, phase, seq, record_json, prev_hash, record_hash) VALUES(?,?,?,?,?,?,?)",
			forged.N, forged.RunID, forged.Phase, forged.Seq, forged.RecordJSON, forged.PrevHash, forged.RecordHash); err == nil {
			t.Fatal("a second end record was accepted")
		}
	})
}

func TestUnreadableValuesFailClosed(t *testing.T) {
	for name, q := range map[string]string{
		"text in an integer column": "UPDATE code_runs SET created_at_utc = 'soon' WHERE rowid = 1",
		"blob in a text column":     "UPDATE code_runs SET reason = x'00ff' WHERE rowid = 1",
		"null in a chain column":    "UPDATE code_chain SET record_hash = NULL WHERE n = 2",
		"a flag that is not 0 or 1": "UPDATE code_runs SET caller_declared = 7 WHERE rowid = 1",
	} {
		t.Run(name, func(t *testing.T) {
			_, c, _ := newRunCase(t)
			seedRuns(t, c)
			dropGuards(t, c)
			if _, err := c.db.ExecContext(cctx, q); err != nil {
				t.Skipf("the schema refuses it already: %v", err)
			}
			rep, err := c.VerifyCodeChain(cctx, nil)
			if err == nil && rep.OK {
				t.Fatalf("a damaged value verified: %+v", rep)
			}
			if err == nil && !hasCheck(rep, casestore.CheckUnreadable) && !hasCheck(rep, casestore.CheckBeginMismatch) && !hasCheck(rep, casestore.CheckEndMismatch) && !hasCheck(rep, casestore.CheckTextUTF8) {
				t.Errorf("checks = %v", problemChecks(rep))
			}
		})
	}
}

// laterStore opens the store at root again with a clock past the orphan grace.
func laterStore(t *testing.T, root string, s *Store) *Store {
	t.Helper()
	later := time.Now().Add(casestore.OrphanGrace + time.Hour)
	return newStoreAt(t, root, s.opt.Protector, func(o *Options) { o.Now = func() time.Time { return later } })
}

func TestCrashLeavesCrashedRow(t *testing.T) {
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Crash")
	c := mustOpen(t, s, info.ID)
	mustRun(t, c, casestore.RunStart{Source: "return 1"}, nil, casestore.RunEnd{})
	dead, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "kill -9 here", KeepResults: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RecordCall(cctx, dead.RunID, call(1, "ok", `{}`, `{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	// The process dies: the handle goes away with the run still "running".
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	soon := newStoreAt(t, root, s.opt.Protector)
	if n, err := mustOpen(t, soon, info.ID).CloseOrphanRuns(cctx); err != nil || n != 0 {
		t.Fatalf("a run younger than the grace was closed: %d %v", n, err)
	}
	s2 := laterStore(t, root, s)
	c2 := mustOpen(t, s2, info.ID)
	if rep := verify(t, c2, nil); !rep.OK || rep.OpenRuns != 1 {
		t.Fatalf("an unfinished run is open, not broken: %+v", rep)
	}
	if n, err := c2.CloseOrphanRuns(cctx); err != nil || n != 1 {
		t.Fatalf("CloseOrphanRuns = %d %v", n, err)
	}
	got, err := c2.GetRun(cctx, dead.RunID)
	if err != nil || got.Status != casestore.RunCrashed || got.CallCount != 1 || got.EndedMS == 0 || got.ErrorText == "" {
		t.Fatalf("closed run = %+v %v", got, err)
	}
	if rep := verify(t, c2, nil); !rep.OK || rep.OpenRuns != 0 || rep.Rows != 5 {
		t.Fatalf("verify after = %+v", rep)
	}
	if res, err := c2.CallResult(cctx, dead.RunID, 1); err != nil || string(res) != `{"x":1}` {
		t.Errorf("the call made before the crash is kept: %q %v", res, err)
	}
}

// Rows and chain survive a close and reopen of the sealed file with the same key,
// and a case file written by this build is opened again by the same key only.
func TestRowsSurviveReopen(t *testing.T) {
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Reopen")
	c := mustOpen(t, s, info.ID)
	runs := seedRuns(t, c)
	before := verify(t, c, nil)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := newStoreAt(t, root, s.opt.Protector)
	c2 := mustOpen(t, s2, info.ID)
	after := verify(t, c2, nil)
	if !after.OK || after.Head != before.Head || after.Rows != 10 {
		t.Fatalf("after reopen: %+v (before %+v)", after, before)
	}
	got, err := c2.GetRun(cctx, runs[0].RunID)
	if err != nil || got.Source != runs[0].Source || got.ResultHash != runs[0].ResultHash {
		t.Fatalf("run after reopen = %+v %v", got, err)
	}
	// And the file on disk holds none of it in the clear.
	raw, err := os.ReadFile(filepath.Join(root, string(info.ID), dbName)) //nolint:gosec // the test reads its own case file
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"unravis.audit_compare", "check the slow routes", "code_run_calls"} {
		if bytes.Contains(raw, []byte(needle)) {
			t.Errorf("the sealed file holds %q in the clear", needle)
		}
	}
	// A run goes on from the head it found.
	mustRun(t, c2, casestore.RunStart{Source: "return 4"}, nil, casestore.RunEnd{})
	if rep := verify(t, c2, nil); !rep.OK || rep.Rows != 12 {
		t.Fatalf("a run after the reopen: %+v", rep)
	}
}

func TestCodeRunsSurviveRewrap(t *testing.T) {
	kr := rewrapKeyring(t, "correct horse battery")
	s, root, _, info := dpapiCase(t)
	s.opt.Protectors = keyringLookup(kr)
	c := mustOpen(t, s, info.ID)
	seedRuns(t, c)
	before := verify(t, c, nil)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Rewrap(cctx, info.ID); err != nil {
		t.Fatal(err)
	}
	s2 := masterStore(t, root, kr)
	c2 := mustOpen(t, s2, info.ID)
	after := verify(t, c2, nil)
	if !after.OK || after.Head != before.Head || after.Rows != 10 {
		t.Fatalf("after Rewrap: %+v (before %+v)", after, before)
	}
	if res, err := c2.CallResult(cctx, mustRows2(t, c2)[0].RunID, 1); err != nil || len(res) == 0 {
		t.Errorf("a kept result after Rewrap: %q %v", res, err)
	}
}

func mustRows2(t *testing.T, c *Case) []casestore.CodeRun {
	t.Helper()
	runs, err := c.ListRuns(cctx, casestore.RunQuery{})
	if err != nil || len(runs) == 0 {
		t.Fatalf("ListRuns = %v %v", runs, err)
	}
	// oldest first
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	return runs
}

// Parallel writers (two windows, the broker's concurrent runs) never fork or gap the chain.
func TestConcurrentRunsKeepOneChain(t *testing.T) {
	_, c, _ := newRunCase(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	const workers, each = 6, 5
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: fmt.Sprintf("return %d", w*100+i), KeepResults: true})
				if err != nil {
					errs <- err
					return
				}
				for k := 1; k <= 2; k++ {
					if err := c.RecordCall(cctx, run.RunID, call(k, "ok", `{}`, fmt.Sprintf(`{"w":%d}`, w))); err != nil {
						errs <- err
						return
					}
				}
				if _, err := c.EndRun(cctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK}); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	rep := verify(t, c, nil)
	if !rep.OK || rep.Rows != workers*each*4 || rep.Runs != workers*each {
		t.Fatalf("verify = %+v", rep)
	}
}

// Result copies and the verifier start no goroutine that stays.
func TestNoGoroutineLeftAfterRuns(t *testing.T) {
	_, c, _ := newRunCase(t)
	seedRuns(t, c) // warm up the shared encoder and decoder
	before := runtime.NumGoroutine()
	for i := 0; i < 40; i++ {
		mustRun(t, c, casestore.RunStart{Source: "return 1", KeepResults: true}, []casestore.CallRecord{call(1, "ok", `{}`, `{"x":1}`)}, casestore.RunEnd{})
		verify(t, c, nil)
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines %d -> %d", before, after)
	}
}

// An import in progress refuses a run's first write instead of waiting for it.
func TestRunWriteDuringImportDoesNotWait(t *testing.T) {
	_, c, _ := newRunCase(t)
	c.importRun.Store(true)
	defer c.importRun.Store(false)
	done := make(chan error, 1)
	go func() { _, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x"}); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, casestore.ErrImportRunning) {
			t.Fatalf("BeginRun during an import = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("BeginRun waited for the import")
	}
}

func calls3(t *testing.T, c *Case) (casestore.CodeRun, []byte) {
	t.Helper()
	run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x", KeepResults: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RecordCall(cctx, run.RunID, call(1, "ok", `{"a":1}`, `{"real":"result"}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordCall(cctx, run.RunID, call(2, "ok", `{"a":2}`, `{"second":"call"}`)); err != nil {
		t.Fatal(err)
	}
	forged := []byte(`{"forged":"result"}`)
	return run, forged
}

const (
	forgeReplaceSQL = `INSERT OR REPLACE INTO code_run_calls(run_id, seq, done_seq, tool_name, args_json, args_hash, args_state, result_hash, result_bytes, result_blob, blob_state, status, duration_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`
	forgeInsertSQL = `INSERT INTO code_run_calls(run_id, seq, done_seq, tool_name, args_json, args_hash, args_state, result_hash, result_bytes, result_blob, blob_state, status, duration_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`
)

// forgeCallArgs are the bound values of a call row holding the forged result; the
// caller sets the run id (args[0]) and may move seq and done_seq.
func forgeCallArgs(forged []byte) []any {
	return []any{nil, 1, 1, "audit_compare", `{"a":1}`, casestore.SHA256Hex([]byte(`{"a":1}`)), "kept",
		casestore.SHA256Hex(forged), len(forged), zstdBytes(forged), "kept", "ok", 11}
}

func zstdBytes(b []byte) []byte {
	enc, _ := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	defer func() { _ = enc.Close() }()
	return enc.EncodeAll(b, nil)
}

// M1 of the first review: the calls of a run that is still open were bound to
// nothing, and INSERT OR REPLACE swapped one with every trigger in place. Now every
// call is chained when it is written and the triggers refuse a replaced key.
func TestReplaceOfAnOpenRunsCallIsRefusedAndCannotBeLaundered(t *testing.T) {
	t.Run("the guards refuse the replace", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		run, forged := calls3(t, c)
		args := forgeCallArgs(forged)
		args[0] = run.RunID
		if _, err := c.db.ExecContext(cctx, forgeReplaceSQL, args...); err == nil {
			t.Fatal("INSERT OR REPLACE over a call row of an open run was allowed")
		}
		// A row with seq 2 and done_seq 1 would have removed both old rows.
		args[1], args[2] = 2, 1
		if _, err := c.db.ExecContext(cctx, forgeReplaceSQL, args...); err == nil {
			t.Fatal("INSERT OR REPLACE over two call rows was allowed")
		}
		if res, err := c.CallResult(cctx, run.RunID, 1); err != nil || string(res) != `{"real":"result"}` {
			t.Fatalf("the call changed: %q %v", res, err)
		}
		if rep := verify(t, c, nil); !rep.OK || rep.OpenRuns != 1 || len(rep.Warnings) != 0 {
			t.Fatalf("verify = %+v", rep)
		}
	})
	t.Run("with the guards dropped, the forged call of an open run is found while open and after the close", func(t *testing.T) {
		s, root := newTestStore(t)
		info := mustCreate(t, s, "Open run")
		c := mustOpen(t, s, info.ID)
		seedRuns(t, c)
		run, forged := calls3(t, c)
		noted := verify(t, c, nil) // a head noted while the run is open
		anchor := &casestore.ChainAnchor{N: noted.HeadN, Hash: noted.Head}
		dropGuards(t, c)
		args := forgeCallArgs(forged)
		args[0] = run.RunID
		rawSQL(t, c, forgeReplaceSQL, args...)
		rep := verify(t, c, nil)
		mustBroken(t, rep, 12, casestore.CheckCallMismatch) // the call record of seq 1 is row 12
		// The app's own crash close must not seal it.
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		c2 := mustOpen(t, laterStore(t, root, s), info.ID)
		if n, err := c2.CloseOrphanRuns(cctx); err != nil || n != 1 {
			t.Fatalf("CloseOrphanRuns = %d %v", n, err)
		}
		rep = verify(t, c2, nil)
		mustBroken(t, rep, 12, casestore.CheckCallMismatch)
		if res, _ := c2.CallResult(cctx, run.RunID, 1); !bytes.Equal(res, forged) {
			t.Fatalf("setup: the forged row is not in place: %q", res)
		}
		// A head noted while the run was open still has to match after the close.
		if rep := verify(t, c2, anchor); hasCheck(rep, casestore.CheckAnchorMismatch) || !rep.AnchorChecked {
			t.Errorf("a head noted while the run was open is still a prefix of the history: %+v", rep)
		}
	})
	t.Run("an honest close keeps a head noted while the run was open", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		run, _ := calls3(t, c)
		noted := verify(t, c, nil)
		anchor := &casestore.ChainAnchor{N: noted.HeadN, Hash: noted.Head}
		if noted.OpenRuns != 1 || noted.Rows != 3 {
			t.Fatalf("setup: %+v", noted)
		}
		if _, err := c.EndRun(cctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK}); err != nil {
			t.Fatal(err)
		}
		if rep := verify(t, c, anchor); !rep.OK || !rep.AnchorChecked || rep.Rows != 4 {
			t.Fatalf("verify against the head noted while open = %+v", rep)
		}
	})
	t.Run("a call row nobody chained", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		run, forged := calls3(t, c)
		dropGuards(t, c)
		args := forgeCallArgs(forged)
		args[0], args[1], args[2] = run.RunID, 7, 7
		rawSQL(t, c, forgeInsertSQL, args...)
		mustBroken(t, verify(t, c, nil), -1, casestore.CheckCallUnchained)
	})
	t.Run("a call record whose row was deleted", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		run, _ := calls3(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_run_calls WHERE run_id = ? AND seq = 2", run.RunID)
		mustBroken(t, verify(t, c, nil), 3, casestore.CheckCallMissing)
	})
}

// S1: INSERT OR REPLACE and an upsert get past the delete triggers of every table.
func TestReplaceCannotOverwriteAnyAuditTable(t *testing.T) {
	_, c, _ := newRunCase(t)
	runs := seedRuns(t, c)
	before := verify(t, c, nil)
	head := before.Head
	for name, q := range map[string]string{
		"chain row (replace)": "INSERT OR REPLACE INTO code_chain(n, run_id, phase, seq, record_json, prev_hash, record_hash) VALUES(11, '" + runs[1].RunID + "', 'begin', 0, '{}', '" + head + "', 'zz')",
		"chain row same n":    "INSERT OR REPLACE INTO code_chain(n, run_id, phase, seq, record_json, prev_hash, record_hash) VALUES(3, 'x', 'end', 0, '{}', 'p', 'h3')",
		"chain row upsert":    "INSERT INTO code_chain(n, run_id, phase, seq, record_json, prev_hash, record_hash) VALUES(3, 'x', 'end', 0, '{}', 'p', 'h3') ON CONFLICT(n) DO UPDATE SET record_json = 'evil'",
		"sealed run":          "INSERT OR REPLACE INTO code_runs(run_id, created_at_utc, surface, source_ts, status) VALUES('" + runs[0].RunID + "', 1, 'cli', 'evil', 'ok')",
		"run upsert":          "INSERT INTO code_runs(run_id, created_at_utc, surface, source_ts, status) VALUES('" + runs[0].RunID + "', 1, 'cli', 'evil', 'ok') ON CONFLICT(run_id) DO UPDATE SET source_ts = 'evil'",
		"call row":            "INSERT OR REPLACE INTO code_run_calls(run_id, seq, done_seq, tool_name, args_hash, result_hash, blob_state, status) VALUES('" + runs[0].RunID + "', 1, 1, 'x', 'a', 'b', 'off', 'ok')",
	} {
		if _, err := c.db.ExecContext(cctx, q); err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
	if after := verify(t, c, nil); !after.OK || after.Head != head || after.Rows != before.Rows || len(after.Warnings) != 0 {
		t.Fatalf("a refused replace changed the record: %+v", after)
	}
	if got, _ := c.GetRun(cctx, runs[0].RunID); got.Source != runs[0].Source {
		t.Errorf("the sealed run changed: %q", got.Source)
	}
}

// N1: a trigger replaced by one of the same name that does nothing is reported.
func TestNeuteredGuardIsReported(t *testing.T) {
	_, c, _ := newRunCase(t)
	seedRuns(t, c)
	rawSQL(t, c, "DROP TRIGGER code_chain_no_delete")
	rawSQL(t, c, "CREATE TRIGGER code_chain_no_delete BEFORE DELETE ON code_chain WHEN 0 BEGIN SELECT 1; END")
	rep := verify(t, c, nil)
	if !rep.OK || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "code_chain_no_delete") {
		t.Fatalf("verify = %+v", rep)
	}
}

// S2: a second window opening the case must not kill the first window's live run.
func TestCloseOrphanRunsSparesALiveRun(t *testing.T) {
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Two windows")
	c1 := mustOpen(t, s, info.ID)
	live, err := c1.BeginRun(cctx, casestore.RunStart{Surface: "ui", Source: "live"})
	if err != nil {
		t.Fatal(err)
	}
	c2 := mustOpen(t, s, info.ID)
	if n, err := c2.CloseOrphanRuns(cctx); err != nil || n != 0 {
		t.Fatalf("a second handle closed %d live run(s): %v", n, err)
	}
	if err := c1.RecordCall(cctx, live.RunID, call(1, "ok", `{}`, `{}`)); err != nil {
		t.Fatalf("the live run lost its place: %v", err)
	}
	// Another process, hours later, finds it abandoned.
	if err := c1.Close(); err != nil {
		t.Fatal(err)
	}
	c3 := mustOpen(t, laterStore(t, root, s), info.ID)
	if n, err := c3.CloseOrphanRuns(cctx); err != nil || n != 1 {
		t.Fatalf("an abandoned run was not closed: %d %v", n, err)
	}
	if got, _ := c3.GetRun(cctx, live.RunID); got.Status != casestore.RunCrashed || got.CallCount != 1 {
		t.Fatalf("closed run = %+v", got)
	}
}

// S3: a run that finishes while an import holds the case is sealed when the import
// lets go; it is not marked crashed. A new run is refused during the import.
func TestRunEndsDuringAnImportAndIsSealedAfterIt(t *testing.T) {
	_, c, _ := newRunCase(t)
	run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x", KeepResults: true})
	if err != nil {
		t.Fatal(err)
	}
	c.importRun.Store(true)
	defer c.importRun.Store(false)
	release, err := sealedfile.Lock(c.dir) // what an import holds
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "new"}); !errors.Is(err, casestore.ErrImportRunning) {
		t.Fatalf("a new run during an import = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		if err := c.RecordCall(cctx, run.RunID, call(1, "ok", `{}`, `{"x":1}`)); err != nil {
			done <- err
			return
		}
		_, err := c.EndRun(cctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the write did not wait for the import (%v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("after the import: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the write never finished")
	}
	got, _ := c.GetRun(cctx, run.RunID)
	if got.Status != casestore.RunOK || got.CallCount != 1 {
		t.Fatalf("run = %+v", got)
	}
	if rep := verify(t, c, nil); !rep.OK {
		t.Fatalf("verify = %+v", rep)
	}
}

// S5: a text that is not valid UTF-8 cannot be stored, and an edit that puts one
// in is found (encoding/json would fold it into U+FFFD and hide the change).
func TestInvalidUTF8IsRefusedAndFound(t *testing.T) {
	_, c, _ := newRunCase(t)
	run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for name, end := range map[string]casestore.RunEnd{
		"error code": {Status: casestore.RunError, ErrorCode: "e\xff"},
		"runtime":    {Status: casestore.RunError, Runtime: "r\xfe"},
	} {
		if _, err := c.EndRun(cctx, run.RunID, end); !errors.Is(err, casestore.ErrBadCodeRun) {
			t.Errorf("%s: EndRun = %v, want ErrBadCodeRun", name, err)
		}
	}
	if _, err := c.EndRun(cctx, run.RunID, casestore.RunEnd{Status: casestore.RunError, ErrorCode: "e\ufffd", Runtime: "r\ufffd"}); err != nil {
		t.Fatal(err)
	}
	if rep := verify(t, c, nil); !rep.OK {
		t.Fatalf("the literal U+FFFD is a valid text: %+v", rep)
	}
	dropGuards(t, c)
	for _, edit := range []string{
		"UPDATE code_runs SET error_code = CAST(X'6580' AS TEXT) WHERE run_id = ?",
		"UPDATE code_runs SET runtime = CAST(X'72FF' AS TEXT) WHERE run_id = ?",
		"UPDATE code_runs SET reason = CAST(X'80' AS TEXT) WHERE run_id = ?",
	} {
		rawSQL(t, c, edit, run.RunID)
		mustBroken(t, verify(t, c, nil), -1, casestore.CheckTextUTF8)
		rawSQL(t, c, "UPDATE code_runs SET error_code = ?, runtime = ?, reason = '' WHERE run_id = ?", "e\ufffd", "r\ufffd", run.RunID)
	}
}

// Nothing deletes a run by itself, however many there are (spec 04 D10).
func TestNoAutoPrune(t *testing.T) {
	_, c, _ := newRunCase(t)
	var first casestore.CodeRun
	for i := 0; i < 120; i++ {
		r := mustRun(t, c, casestore.RunStart{Source: fmt.Sprintf("return %d", i), KeepResults: true}, []casestore.CallRecord{call(1, "ok", `{}`, `{"x":1}`)}, casestore.RunEnd{})
		if i == 0 {
			first = r
		}
	}
	runs, err := c.ListRuns(cctx, casestore.RunQuery{Limit: casestore.MaxRunsPage})
	if err != nil || len(runs) != 120 {
		t.Fatalf("ListRuns = %d %v", len(runs), err)
	}
	if res, err := c.CallResult(cctx, first.RunID, 1); err != nil || string(res) != `{"x":1}` {
		t.Fatalf("the oldest kept result: %q %v", res, err)
	}
	if rep := verify(t, c, nil); !rep.OK || rep.Rows != 120*3 {
		t.Fatalf("verify = %+v", rep)
	}
}
