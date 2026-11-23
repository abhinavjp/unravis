package sqlite

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/sealedfile"
)

// BL-369 hardening (review 2 of the audit store): every test here names the hole
// it closes and fails when the fix is taken out.

// S1: UPDATE OR REPLACE with a rowid gets past the delete guard of code_runs
// (REPLACE fires no delete trigger) and used to delete a sealed run.
func TestUpdateOrReplaceCannotDeleteOrReorderARun(t *testing.T) {
	_, c, _ := newRunCase(t)
	runs := seedRuns(t, c)
	open, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "open"})
	if err != nil {
		t.Fatal(err)
	}
	var sealedRowid int64
	if err := c.db.QueryRowContext(cctx, "SELECT rowid FROM code_runs WHERE run_id=?", runs[0].RunID).Scan(&sealedRowid); err != nil {
		t.Fatal(err)
	}
	before := verify(t, c, nil)
	for name, q := range map[string]string{
		"update or replace onto a sealed run": "UPDATE OR REPLACE code_runs SET rowid=" + strconvI(sealedRowid) + " WHERE run_id='" + open.RunID + "'",
		"update or replace, other spelling":   "UPDATE OR REPLACE code_runs SET rowid=" + strconvI(sealedRowid) + ", status='running' WHERE run_id='" + open.RunID + "'",
		"plain update of the rowid":           "UPDATE code_runs SET rowid=999999 WHERE run_id='" + open.RunID + "'",
		"update or ignore of the rowid":       "UPDATE OR IGNORE code_runs SET rowid=999999 WHERE run_id='" + open.RunID + "'",
	} {
		if _, err := c.db.ExecContext(cctx, q); err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
	var n int
	_ = c.db.QueryRowContext(cctx, "SELECT COUNT(*) FROM code_runs WHERE run_id=?", runs[0].RunID).Scan(&n)
	if n != 1 {
		t.Fatalf("the sealed run row is gone (%d rows)", n)
	}
	got, err := c.ListRuns(cctx, casestore.RunQuery{})
	if err != nil || len(got) != 4 || got[0].RunID != open.RunID {
		t.Fatalf("ListRuns order changed: %v %v", got, err)
	}
	if after := verify(t, c, nil); !after.OK || after.Head != before.Head || len(after.Warnings) != 0 {
		t.Fatalf("a refused update changed the record: %+v", after)
	}
}

// A case written by the build that had the shorter code_runs_sealed text gets the
// new text at its next BeginRun; a trigger somebody else rewrote is left alone
// (and stays reported).
func TestEarlierRowidGuardTextIsUpgraded(t *testing.T) {
	_, c, _ := newRunCase(t)
	seedRuns(t, c)
	legacy := legacyGuards["code_runs_sealed"][0]
	rawSQL(t, c, "DROP TRIGGER code_runs_sealed")
	rawSQL(t, c, legacy)
	if rep := verify(t, c, nil); !rep.OK || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "code_runs_sealed") {
		t.Fatalf("the earlier text must be reported before the upgrade: %+v", rep)
	}
	open, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if rep := verify(t, c, nil); !rep.OK || len(rep.Warnings) != 0 {
		t.Fatalf("after the upgrade: %+v", rep)
	}
	if _, err := c.db.ExecContext(cctx, "UPDATE code_runs SET rowid=999999 WHERE run_id=?", open.RunID); err == nil {
		t.Error("the upgraded guard still lets the rowid change")
	}

	// Somebody's own trigger of that name is not ours to replace.
	rawSQL(t, c, "DROP TRIGGER code_runs_sealed")
	rawSQL(t, c, "CREATE TRIGGER code_runs_sealed BEFORE UPDATE ON code_runs WHEN 0 BEGIN SELECT 1; END")
	if _, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "y"}); err != nil {
		t.Fatal(err)
	}
	if rep := verify(t, c, nil); len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "code_runs_sealed") {
		t.Fatalf("a rewritten guard was repaired or hidden: %+v", rep)
	}
}

// S2: a trigger the store did not create can turn an insert into a silent no-op
// (RAISE(IGNORE)). The write used to return nil, the broker answered the script,
// and verify said OK. Now the write fails with ErrCodeTampered, nothing is stored,
// and verify names the trigger.
func TestPlantedIgnoreTriggersAreRefusedAndReported(t *testing.T) {
	plants := map[string][]string{
		"call insert":       {"CREATE TRIGGER zz_a BEFORE INSERT ON code_run_calls BEGIN SELECT RAISE(IGNORE); END"},
		"call chain record": {"CREATE TRIGGER zz_b BEFORE INSERT ON code_chain WHEN NEW.phase='call' BEGIN SELECT RAISE(IGNORE); END"},
		"head update":       {`CREATE TRIGGER zz_c BEFORE UPDATE ON meta WHEN NEW.k='code.chain.head' BEGIN SELECT RAISE(IGNORE); END`},
	}
	for name, stmts := range plants {
		t.Run(name, func(t *testing.T) {
			_, c, _ := newRunCase(t)
			seedRuns(t, c)
			before := verify(t, c, nil)
			anchor := &casestore.ChainAnchor{N: before.HeadN, Hash: before.Head}
			run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x", KeepResults: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range stmts {
				rawSQL(t, c, q)
			}
			err = c.RecordCall(cctx, run.RunID, call(1, "ok", `{"host":"db1"}`, `{"secret":"rows"}`))
			if !errors.Is(err, casestore.ErrCodeTampered) {
				t.Fatalf("RecordCall with a planted trigger = %v, want ErrCodeTampered", err)
			}
			calls, _ := c.RunCalls(cctx, run.RunID)
			if len(calls) != 0 {
				t.Fatalf("a refused call was stored: %v", calls)
			}
			rep := verify(t, c, anchor)
			found := false
			for _, w := range rep.Warnings {
				found = found || strings.Contains(w, "unexpected trigger zz_")
			}
			if !found {
				t.Fatalf("the planted trigger is not reported: %+v", rep)
			}
		})
	}
	t.Run("run insert", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		rawSQL(t, c, "CREATE TRIGGER zz_d BEFORE INSERT ON code_runs BEGIN SELECT RAISE(IGNORE); END")
		if _, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x"}); !errors.Is(err, casestore.ErrCodeTampered) {
			t.Fatalf("BeginRun with a planted trigger = %v", err)
		}
		if runs, _ := c.ListRuns(cctx, casestore.RunQuery{}); len(runs) != 3 {
			t.Fatalf("a refused run was stored: %d", len(runs))
		}
	})
}

// N1: tables that are gone while the case says it made them, and an open run row
// that holds the outcome of a run that has not ended.
func TestGoneTablesAndOpenRowOutcomeAreReported(t *testing.T) {
	t.Run("tables dropped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		for _, q := range []string{"DROP TABLE code_chain", "DROP TABLE code_run_calls", "DROP TABLE code_runs", "DELETE FROM meta WHERE k = 'code.chain.head'"} {
			rawSQL(t, c, q)
		}
		rep := verify(t, c, nil)
		if len(rep.Warnings) == 0 || !strings.Contains(strings.Join(rep.Warnings, " "), "gone") {
			t.Fatalf("dropped tables were not reported: %+v", rep)
		}
	})
	t.Run("one table dropped", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		seedRuns(t, c)
		rawSQL(t, c, "DROP TABLE code_run_calls")
		rep := verify(t, c, nil)
		if len(rep.Warnings) == 0 && rep.OK {
			t.Fatalf("a dropped table was not noticed: %+v", rep)
		}
	})
	t.Run("a case that never held a run is silent", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		if rep := verify(t, c, nil); !rep.OK || len(rep.Warnings) != 0 {
			t.Fatalf("verify = %+v", rep)
		}
	})
	t.Run("open row with an outcome", func(t *testing.T) {
		_, c, _ := newRunCase(t)
		run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x"})
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"console_logs='planted'", "error_text='planted'", "duration_ms=7", "result_preview='p'", "sandbox_mode='lpac'", "result_hash='h'"} {
			rawSQL(t, c, "UPDATE code_runs SET "+q+" WHERE run_id=?", run.RunID)
			mustBroken(t, verify(t, c, nil), 0, casestore.CheckRunStatus)
			rawSQL(t, c, "UPDATE code_runs SET console_logs='', error_text='', duration_ms=0, result_preview='', sandbox_mode='', result_hash='' WHERE run_id=?", run.RunID)
			if rep := verify(t, c, nil); !rep.OK {
				t.Fatalf("an honest open row is flagged after %s: %+v", q, rep)
			}
		}
	})
}

func strconvI(n int64) string { return strconv.FormatInt(n, 10) }

// S3: RecordCall and EndRun wait for a REAL import (a slow importer holds the
// folder lock for as long as it runs), however long that takes, and give up when
// their own context ends.
func TestRealImportMakesAuditWritesWaitNotFail(t *testing.T) {
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Import wait")
	c := mustOpen(t, s, info.ID)
	run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x", KeepResults: true})
	if err != nil {
		t.Fatal(err)
	}
	release, importDone := blockingImport(t, c, "wait")
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	// A context that ends first ends the wait, and nothing is stored.
	short, cancel := context.WithTimeout(cctx, 150*time.Millisecond)
	defer cancel()
	timed := make(chan error, 1)
	go func() { timed <- c.RecordCall(short, run.RunID, call(1, "ok", `{}`, `{"x":1}`)) }()
	select {
	case err := <-timed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("RecordCall with a short context during the import = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RecordCall ignored its context while the import held the folder")
	}

	done := make(chan error, 2)
	go func() { done <- c.RecordCall(cctx, run.RunID, call(1, "ok", `{}`, `{"x":1}`)) }()
	select {
	case err := <-done:
		t.Fatalf("RecordCall did not wait for the import: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	// On Windows sealedfile.Lock gives up after 5 s: keep the import going past it.
	if runtime.GOOS == "windows" {
		time.Sleep(5500 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("RecordCall gave up while the import was still running: %v", err)
	default:
	}
	released = true
	close(release)
	if err := <-importDone; err != nil {
		t.Fatalf("import: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RecordCall after the import: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RecordCall never finished after the import")
	}
	fin, err := c.EndRun(cctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK})
	if err != nil || fin.CallCount != 1 {
		t.Fatalf("EndRun = %+v, %v", fin, err)
	}
	if rep := verify(t, c, nil); !rep.OK || len(rep.Warnings) != 0 {
		t.Fatalf("verify = %+v", rep)
	}
}

// The wait helper itself: it honours ctx, and it is the folder lock that is waited on.
func TestLockWaitHonoursContext(t *testing.T) {
	_, c, _ := newRunCase(t)
	hold, err := sealedfile.Lock(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(cctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := sealedfile.LockWait(ctx, c.dir); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("LockWait = %v after %v", err, time.Since(start))
	}
	hold()
	rel, err := sealedfile.LockWait(cctx, c.dir)
	if err != nil {
		t.Fatal(err)
	}
	rel()
}

// S4: an audit write commits with synchronous=FULL on a connection of its own and
// hands the connection back as NORMAL; other connections stay NORMAL.
func TestAuditWritesRunSynchronousFull(t *testing.T) {
	_, c, _ := newRunCase(t)
	db, err := c.handle()
	if err != nil {
		t.Fatal(err)
	}
	tx, done, err := c.beginDurable(cctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var mode int
	if err := tx.QueryRowContext(cctx, "PRAGMA synchronous").Scan(&mode); err != nil || mode != 2 {
		t.Fatalf("synchronous inside an audit write = %d, %v (want 2, FULL)", mode, err)
	}
	done()
	seedRuns(t, c)
	for i := 0; i < 8; i++ { // however the pool hands them out, no connection is left on FULL
		if err := c.db.QueryRowContext(cctx, "PRAGMA synchronous").Scan(&mode); err != nil || mode != 1 {
			t.Fatalf("synchronous after the writes = %d, %v (want 1, NORMAL)", mode, err)
		}
	}
}

// S5b: a withheld value keeps a hash keyed by a per-case secret. The same value
// hashes alike inside one case (so equal values still show), never to the plain
// SHA-256, and differently in another case.
func TestWithheldHashesAreKeyedPerCase(t *testing.T) {
	s, _ := newTestStore(t)
	mk := func(name string) (*Case, casestore.CodeRun) {
		c := mustOpen(t, s, mustCreate(t, s, name).ID)
		run := mustRun(t, c, casestore.RunStart{Source: "x", KeepResults: true},
			[]casestore.CallRecord{
				call(1, "ok", `{"host":"10.1.2.3"}`, `{"from":"10.9.9.9"}`),
				call(2, "ok", `{"host":"10.1.2.3"}`, `{"n":1}`),
			}, casestore.RunEnd{Status: casestore.RunOK, Result: []byte(`{"ip":"10.1.2.3"}`)})
		return c, run
	}
	c1, r1 := mk("one")
	c2, r2 := mk("two")
	calls1, _ := c1.RunCalls(cctx, r1.RunID)
	calls2, _ := c2.RunCalls(cctx, r2.RunID)
	plainArgs := casestore.SHA256Hex([]byte(`{"host":"10.1.2.3"}`))
	plainResult := casestore.SHA256Hex([]byte(`{"from":"10.9.9.9"}`))
	if calls1[0].ArgsState != casestore.ArgsWithheld || calls1[0].ArgsHash == plainArgs || calls1[0].ResultHash == plainResult {
		t.Fatalf("a withheld value keeps its plain hash: %+v", calls1[0])
	}
	if calls1[0].ArgsHash != calls1[1].ArgsHash {
		t.Error("equal withheld values must still hash alike inside one case")
	}
	if calls1[0].ArgsHash == calls2[0].ArgsHash || calls1[0].ResultHash == calls2[0].ResultHash {
		t.Error("two cases share one secret")
	}
	got, _ := c1.GetRun(cctx, r1.RunID)
	if got.ResultHash == casestore.SHA256Hex([]byte(`{"ip":"10.1.2.3"}`)) || len(got.ResultHash) != 64 {
		t.Errorf("run result hash = %q", got.ResultHash)
	}
	// The plain hash is nowhere in the chain.
	rows, err := c1.ChainRows(cctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		for _, plain := range []string{plainArgs, plainResult, casestore.SHA256Hex([]byte(`{"ip":"10.1.2.3"}`))} {
			if strings.Contains(r.RecordJSON, plain) {
				t.Errorf("chain row %d holds a plain hash of a withheld value", r.N)
			}
		}
	}
	if rep := verify(t, c1, nil); !rep.OK || len(rep.Warnings) != 0 {
		t.Fatalf("verify = %+v", rep)
	}
	// An allowed run keeps plain hashes (nothing is withheld, nothing needs hiding).
	allow := mustRun(t, c1, casestore.RunStart{Source: "x", AllowIdentifiers: true},
		[]casestore.CallRecord{call(1, "ok", `{"host":"10.1.2.3"}`, `{"from":"10.9.9.9"}`)}, casestore.RunEnd{Status: casestore.RunOK})
	ac, _ := c1.RunCalls(cctx, allow.RunID)
	if ac[0].ArgsHash != plainArgs || ac[0].ArgsState != casestore.ArgsKept {
		t.Errorf("allow mode call = %+v", ac[0])
	}
}

// S5a on the real store: an address glued to a word, an underscore or a letter is
// withheld, and one that straddles the cap of a text is not left as a partial.
func TestHiddenModeCatchesGluedAddressesAndCutEdges(t *testing.T) {
	_, c, _ := newRunCase(t)
	run, err := c.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x", KeepResults: true})
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range []string{"host_10.0.0.1", "srv10.1.2.3", "10.0.0.1x"} {
		if err := c.RecordCall(cctx, run.RunID, call(i+1, "ok", `{"h":"`+a+`"}`, `{}`)); err != nil {
			t.Fatal(err)
		}
	}
	calls, _ := c.RunCalls(cctx, run.RunID)
	for i, cl := range calls {
		if cl.ArgsState != casestore.ArgsWithheld || cl.ArgsJSON != "" {
			t.Errorf("call %d kept an address: %+v", i+1, cl)
		}
	}
	// "192.168.10.77" starts 6 bytes before the preview cap: the cut must not leave "192.168".
	pad := strings.Repeat("a", casestore.MaxRunPreview-6)
	logs := strings.Repeat("b", casestore.MaxRunErrorText-6) + "192.168.10.77 tail"
	fin, err := c.EndRun(cctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK, Result: []byte(pad + "192.168.10.77"), ErrorText: logs})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fin.ResultPreview, fin.ErrorText} {
		if strings.Contains(text, "192.168") || len(text) > casestore.MaxRunPreview {
			t.Errorf("a cut left part of an address: ...%q", text[max(0, len(text)-20):])
		}
	}
}

// R6 of the review: forging a whole new run at the tail with SQL only, no trigger
// dropped, passes verify unless the head kept beside the chain is checked or the
// chain was noted: this is the documented key-holder limit, pinned here so a
// change to it is a decision.
func TestForgedTailRunIsOnlyCaughtByTheHead(t *testing.T) {
	_, c, _ := newRunCase(t)
	seedRuns(t, c)
	tail := verify(t, c, nil)
	run, err := casestore.NewCodeRun(casestore.RunStart{RunID: "01900000-0000-7000-8000-000000000001", Surface: "ui", Source: "forged"}, time.UnixMilli(1700000000000))
	if err != nil {
		t.Fatal(err)
	}
	b, err := casestore.BeginChainRow(run, tail.HeadN+1, tail.Head)
	if err != nil {
		t.Fatal(err)
	}
	rawSQL(t, c, "INSERT INTO code_runs(run_id, created_at_utc, surface, source_ts, status) VALUES(?,?,?,?,'running')", run.RunID, run.CreatedMS, run.Surface, run.Source)
	rawSQL(t, c, "INSERT INTO code_chain(n,run_id,phase,seq,record_json,prev_hash,record_hash) VALUES(?,?,?,?,?,?,?)", b.N, b.RunID, b.Phase, b.Seq, b.RecordJSON, b.PrevHash, b.RecordHash)
	mustBroken(t, verify(t, c, nil), -1, casestore.CheckHeadMismatch)
	rawSQL(t, c, "UPDATE meta SET v=? WHERE k='code.chain.head'", strconvI(b.N)+":"+b.RecordHash)
	if rep := verify(t, c, nil); !rep.OK {
		t.Fatalf("a forgery that also moves the head is the documented limit and verifies: %+v", rep)
	}
}
