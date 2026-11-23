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
		if _, err := c.db.ExecContext(cctx, "DROP TRIGGER IF EXISTS "+g); err != nil {
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
	if !rep.OK || rep.Rows != 6 || rep.Runs != 3 || rep.OpenRuns != 0 || rep.HeadN != 6 || len(rep.Head) != 64 || len(rep.Warnings) != 0 {
		t.Fatalf("verify = %+v", rep)
	}
	rows, err := c.ChainRows(cctx, 0, 0)
	if err != nil || len(rows) != 6 || rows[5].RecordHash != rep.Head {
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
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k = ?", codeHeadKey).Scan(&v); err != nil || v != "6:"+rep.Head {
		t.Errorf("meta head = %q %v", v, err)
	}
}

// Appending to a case without the tables makes them; reading one without them is empty.
func TestOldCaseWithoutTablesReadsEmptyAndKeepsItsSchemaVersion(t *testing.T) {
	_, c, _ := newRunCase(t)
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
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k='schema_version'").Scan(&before); err != nil {
		t.Skipf("no schema_version row to compare: %v", err)
	}
	seedRuns(t, c)
	var after string
	if err := c.db.QueryRow("SELECT v FROM meta WHERE k='schema_version'").Scan(&after); err != nil || after != before || after != strconv.Itoa(schemaVersion) {
		t.Fatalf("schema version %q -> %q (build knows %d): the audit tables must not bump it", before, after, schemaVersion)
	}
	// A build that knows the same schema version opens the case; the build before
	// this one knows the same version and has no code for the tables, which the
	// rest of the case does not reference.
	if refusesSchema(schemaVersion, schemaVersion) {
		t.Fatal("a case at this build's own version is refused")
	}
	if err := c.IntegrityCheck(ctx); err != nil {
		t.Fatalf("IntegrityCheck with the audit tables: %v", err)
	}
	rows, err := c.db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'code\\_%' ESCAPE '\\' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
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
	if rep := verify(t, c, nil); !rep.OK || rep.Rows != 6 {
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
	// begin-covered columns break row 1 (the run's begin record), the others row 2.
	cols := []struct {
		name  string
		text  bool
		first int64
	}{
		{"created_at_utc", false, 1}, {"surface", true, 1}, {"caller_id", true, 1}, {"caller_declared", false, 1},
		{"allow_identifiers", false, 1}, {"keep_results", false, 1}, {"tool_schema_hash", true, 1}, {"reason", true, 1},
		{"source_ts", true, 1}, {"seed", true, 1}, {"clock_ms", false, 1},
		{"ended_at_utc", false, 2}, {"sandbox_mode", true, 2}, {"runtime", true, 2}, {"worker_sha256", true, 2},
		{"stripped_js_hash", true, 2}, {"error_code", true, 2}, {"error_text", true, 2}, {"result_hash", true, 2},
		{"result_bytes", false, 2}, {"result_preview", true, 2}, {"console_logs", true, 2}, {"call_count", false, 2},
		{"duration_ms", false, 2}, {"script_ms", false, 2},
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
		mustBroken(t, verify(t, c, nil), 2, casestore.CheckRunStatus)
	})
	t.Run("sealed status flipped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "UPDATE code_runs SET status = 'error' WHERE run_id = ?", runs[0].RunID)
		mustBroken(t, verify(t, c, nil), 2, casestore.CheckEndMismatch)
	})
}

func TestOneByteEditBreaksVerifyCallRowAndBlob(t *testing.T) {
	type edit struct {
		name  string
		sql   string
		check string
	}
	edits := []edit{
		{"args_json", "UPDATE code_run_calls SET args_json = args_json || ' ' WHERE seq = 1", casestore.CheckCallRow},
		{"args_json same hash", `UPDATE code_run_calls SET args_json = '{"a":9}' WHERE seq = 1`, casestore.CheckCallRow},
		{"args_hash", "UPDATE code_run_calls SET args_hash = '" + strings.Repeat("1", 64) + "' WHERE seq = 1", casestore.CheckCallRow},
		{"args_state", "UPDATE code_run_calls SET args_state = 'withheld' WHERE seq = 1", casestore.CheckCallRow},
		{"tool_name", "UPDATE code_run_calls SET tool_name = 'audit_get_guide' WHERE seq = 1", casestore.CheckEndMismatch},
		{"result_hash", "UPDATE code_run_calls SET result_hash = '" + strings.Repeat("2", 64) + "' WHERE seq = 1", casestore.CheckCallBlob},
		{"result_bytes", "UPDATE code_run_calls SET result_bytes = result_bytes + 1 WHERE seq = 1", casestore.CheckCallBlob},
		{"blob_state", "UPDATE code_run_calls SET blob_state = 'off' WHERE seq = 1", casestore.CheckCallBlob},
		{"blob dropped", "UPDATE code_run_calls SET result_blob = NULL WHERE seq = 1", casestore.CheckCallBlob},
		{"status", "UPDATE code_run_calls SET status = 'denied' WHERE seq = 1", casestore.CheckEndMismatch},
		{"duration_ms", "UPDATE code_run_calls SET duration_ms = duration_ms + 1 WHERE seq = 1", casestore.CheckEndMismatch},
		{"done_seq", "UPDATE code_run_calls SET done_seq = 7 WHERE seq = 1", casestore.CheckEndMismatch},
		{"seq", "UPDATE code_run_calls SET seq = 5 WHERE seq = 1", casestore.CheckEndMismatch},
		{"delete call", "DELETE FROM code_run_calls WHERE seq = 2", casestore.CheckEndMismatch},
		{"move call to another run", "UPDATE code_run_calls SET run_id = (SELECT run_id FROM code_runs ORDER BY rowid DESC LIMIT 1), seq = 8, done_seq = 8 WHERE seq = 2", casestore.CheckEndMismatch},
	}
	for _, e := range edits {
		t.Run(e.name, func(t *testing.T) {
			_, c, _ := newRunCase(t)
			runs := seedRuns(t, c)
			dropGuards(t, c)
			rawSQL(t, c, strings.Replace(e.sql, "WHERE seq", "WHERE run_id = '"+runs[0].RunID+"' AND seq", 1))
			rep := verify(t, c, nil)
			mustBroken(t, rep, 2, e.check)
		})
	}
	t.Run("calls swapped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "UPDATE code_run_calls SET seq = 99 WHERE run_id = ? AND seq = 1", runs[0].RunID)
		rawSQL(t, c, "UPDATE code_run_calls SET seq = 1 WHERE run_id = ? AND seq = 2", runs[0].RunID)
		rawSQL(t, c, "UPDATE code_run_calls SET seq = 2 WHERE run_id = ? AND seq = 99", runs[0].RunID)
		mustBroken(t, verify(t, c, nil), 2, casestore.CheckEndMismatch)
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
		_ = c.db.QueryRow("SELECT record_json FROM code_chain WHERE n = 3").Scan(&rec)
		forged := strings.Replace(rec, `"surface":"cli"`, `"surface":"ui"`, 1)
		rawSQL(t, c, "UPDATE code_chain SET record_json = ?, record_hash = ? WHERE n = 3", forged, casestore.SHA256Hex([]byte(forged)))
		// The row now hashes, but the run row disagrees and the next row's link is stale.
		mustBroken(t, verify(t, c, nil), 3, casestore.CheckBeginMismatch)
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
		rawSQL(t, c, "DELETE FROM code_chain WHERE n = 3")
		rep := verify(t, c, nil)
		mustBroken(t, rep, 4, casestore.CheckChainGap, casestore.CheckPrevHash)
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
		mustBroken(t, verify(t, c, nil), 5, casestore.CheckChainGap, casestore.CheckPrevHash)
	})
	t.Run("renumbered to hide the gap", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE run_id = ?", runs[1].RunID)
		rawSQL(t, c, "UPDATE code_chain SET n = n - 2 WHERE n > 2")
		rep := verify(t, c, nil)
		mustBroken(t, rep, 3, casestore.CheckPrevHash, casestore.CheckBeginMismatch)
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
		rawSQL(t, c, "UPDATE code_chain SET n = n + 100 WHERE n IN (3, 4)")
		rawSQL(t, c, "UPDATE code_chain SET n = n - 2 WHERE n IN (5, 6)")
		rawSQL(t, c, "UPDATE code_chain SET n = n - 96 WHERE n > 100")
		rep := verify(t, c, nil)
		mustBroken(t, rep, 3)
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
		rawSQL(t, c, "DELETE FROM code_chain WHERE n = 6")
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
		mustBroken(t, verify(t, c, nil), 4, casestore.CheckHeadMismatch)
	})
	t.Run("and the head beside the chain rewritten too", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		runs := seedRuns(t, c)
		noted := verify(t, c, nil) // the user wrote down n=6 and its hash
		anchor := &casestore.ChainAnchor{N: noted.HeadN, Hash: noted.Head}
		dropGuards(t, c)
		rawSQL(t, c, "DELETE FROM code_chain WHERE run_id = ?", runs[2].RunID)
		rawSQL(t, c, "DELETE FROM code_run_calls WHERE run_id = ?", runs[2].RunID)
		rawSQL(t, c, "DELETE FROM code_runs WHERE run_id = ?", runs[2].RunID)
		var last casestore.ChainRow
		_ = c.db.QueryRow("SELECT n, record_hash FROM code_chain ORDER BY n DESC LIMIT 1").Scan(&last.N, &last.RecordHash)
		rawSQL(t, c, "UPDATE meta SET v = ? WHERE k = ?", fmt.Sprintf("%d:%s", last.N, last.RecordHash), codeHeadKey)
		// Honest limit: without a noted head this is a valid, shorter chain.
		if rep := verify(t, c, nil); !rep.OK || rep.HeadN != 4 {
			t.Fatalf("a cleanly cut chain is a valid chain, got %+v", rep)
		}
		mustBroken(t, verify(t, c, anchor), 6, casestore.CheckAnchorAhead)
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
		b, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
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
		for _, phase := range []string{"begin", "end"} {
			n++
			var row casestore.ChainRow
			if phase == "begin" {
				row, _ = casestore.BeginChainRow(run, n, prev)
			} else {
				// The end record covers the call rows and the (unchanged) run row.
				row, _ = casestore.EndChainRow(run, calls, n, prev)
			}
			rawSQL(t, c, "INSERT INTO code_chain(n, run_id, phase, record_json, prev_hash, record_hash) VALUES(?,?,?,?,?,?)",
				row.N, row.RunID, row.Phase, row.RecordJSON, row.PrevHash, row.RecordHash)
			prev = row.RecordHash
		}
	}
	rawSQL(t, c, "UPDATE meta SET v = ? WHERE k = ?", fmt.Sprintf("%d:%s", n, prev), codeHeadKey)
	if rep := verify(t, c, nil); !rep.OK {
		t.Fatalf("a fully rebuilt chain verifies by design: %+v", rep)
	}
	mustBroken(t, verify(t, c, anchor), 6, casestore.CheckAnchorMismatch)
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
		mustBroken(t, verify(t, c, nil), 2, casestore.CheckCallRow)
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
		old := rows[1] // the end record of run 1
		forged := casestore.NewChainRow(7, runs[0].RunID, "end", old.RecordJSON, rows[5].RecordHash)
		if _, err := c.db.ExecContext(cctx, "INSERT INTO code_chain(n, run_id, phase, record_json, prev_hash, record_hash) VALUES(?,?,?,?,?,?)",
			forged.N, forged.RunID, forged.Phase, forged.RecordJSON, forged.PrevHash, forged.RecordHash); err == nil {
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
			if err == nil && !hasCheck(rep, casestore.CheckUnreadable) && !hasCheck(rep, casestore.CheckBeginMismatch) && !hasCheck(rep, casestore.CheckEndMismatch) {
				t.Errorf("checks = %v", problemChecks(rep))
			}
		})
	}
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
	s2 := newStoreAt(t, root, s.opt.Protector)
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
	if rep := verify(t, c2, nil); !rep.OK || rep.OpenRuns != 0 || rep.Rows != 4 {
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
	if !after.OK || after.Head != before.Head || after.Rows != 6 {
		t.Fatalf("after reopen: %+v (before %+v)", after, before)
	}
	got, err := c2.GetRun(cctx, runs[0].RunID)
	if err != nil || got.Source != runs[0].Source || got.ResultHash != runs[0].ResultHash {
		t.Fatalf("run after reopen = %+v %v", got, err)
	}
	// And the file on disk holds none of it in the clear.
	raw, err := os.ReadFile(filepath.Join(root, string(info.ID), dbName))
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
	if rep := verify(t, c2, nil); !rep.OK || rep.Rows != 8 {
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
	if !after.OK || after.Head != before.Head || after.Rows != 6 {
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
	if !rep.OK || rep.Rows != workers*each*2 || rep.Runs != workers*each {
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
