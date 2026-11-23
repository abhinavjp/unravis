package casestore_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
)

// BL-369: the audit record is inside case.db, so an archive file carries it and a
// restore brings it back whole, with the chain intact and every kept result readable.
func TestCodeRunsSurviveArchiveRestore(t *testing.T) {
	ctx := context.Background()
	e := newCFEnv(t)
	info := cfSeed(t, e, "Audit archive", 3)
	c, err := e.st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := c.(casestore.CodeRunStore)
	var ids []string
	for i := 0; i < 3; i++ {
		run, err := s.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "return await unravis.audit_compare({});", KeepResults: true, Reason: "archive me"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordCall(ctx, run.RunID, casestore.CallRecord{Seq: 1, DoneSeq: 1, Tool: "audit_compare", Args: []byte(`{"n":1}`), Result: []byte(`{"kept":true}`), Status: "ok"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.EndRun(ctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK, Result: []byte(`{"done":1}`)}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, run.RunID)
	}
	// One left running: it must come back running, then be closable.
	open, err := s.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "still going"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.VerifyCodeChain(ctx, nil)
	if err != nil || !before.OK || before.Rows != 10 || before.OpenRuns != 1 {
		t.Fatalf("before: %+v %v", before, err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	fk := arKey(t)
	file := filepath.Join(t.TempDir(), "audit.unravis-case")
	if _, err := casestore.WriteArchivePart(ctx, file, arSource(t, e, info), fk, nil); err != nil {
		t.Fatal(err)
	}
	got, err := casestore.RestoreCaseFileWith(ctx, e.st, e.lookup, file, casestore.FixedKey(fk.Key))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	rc, err := e.st.Open(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	rs := rc.(casestore.CodeRunStore)
	after, err := rs.VerifyCodeChain(ctx, &casestore.ChainAnchor{N: before.HeadN, Hash: before.Head})
	if err != nil || !after.OK || after.Head != before.Head || after.Rows != 10 || after.OpenRuns != 1 || !after.AnchorChecked {
		t.Fatalf("after restore: %+v %v", after, err)
	}
	for _, id := range ids {
		if res, err := rs.CallResult(ctx, id, 1); err != nil || string(res) != `{"kept":true}` {
			t.Errorf("kept result of %s after restore = %q %v", id, res, err)
		}
	}
	// The open run is young: left alone. A store that opens the case later closes it.
	if n, err := rs.CloseOrphanRuns(ctx); err != nil || n != 0 {
		t.Fatalf("CloseOrphanRuns on a young run = %d %v", n, err)
	}
	_ = rc.Close()
	later := time.Now().Add(casestore.OrphanGrace + time.Hour)
	st2, err := sqlite.New(sqlite.Options{Root: e.root, Protector: storetest.RandomProtector(), Protectors: e.lookup,
		NewCaseKind: func() string { return casestore.KindMasterKey }, MinFreeBytes: -1, Now: func() time.Time { return later }})
	if err != nil {
		t.Fatal(err)
	}
	rc2, err := st2.Open(ctx, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc2.Close() }()
	rs = rc2.(casestore.CodeRunStore)
	if n, err := rs.CloseOrphanRuns(ctx); err != nil || n != 1 {
		t.Fatalf("CloseOrphanRuns on the restored case = %d %v", n, err)
	}
	if r, _ := rs.GetRun(ctx, open.RunID); r.Status != casestore.RunCrashed {
		t.Errorf("restored open run = %+v", r)
	}
	if rep, _ := rs.VerifyCodeChain(ctx, nil); !rep.OK || rep.Rows != 11 {
		t.Errorf("restored case after closing the orphan: %+v", rep)
	}
}
