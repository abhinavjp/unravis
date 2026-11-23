package storetest

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

func coder(t *testing.T, c casestore.Case) casestore.CodeRunStore {
	t.Helper()
	s, ok := c.(casestore.CodeRunStore)
	if !ok {
		t.Fatalf("%T does not implement casestore.CodeRunStore", c)
	}
	return s
}

func okCall(seq int) casestore.CallRecord {
	return casestore.CallRecord{Seq: seq, DoneSeq: seq, Tool: "audit_compare", Args: []byte(`{"b":2,"a":[1,2.50]}`),
		Result: []byte(`{"groups":[1,2,3]}`), Status: casestore.CallOK, DurationMS: 12}
}

func mustVerify(t *testing.T, ctx context.Context, s casestore.CodeRunStore, anchor *casestore.ChainAnchor) casestore.ChainReport {
	t.Helper()
	rep, err := s.VerifyCodeChain(ctx, anchor)
	if err != nil {
		t.Fatalf("VerifyCodeChain: %v", err)
	}
	return rep
}

// codeRunsContract is the contract of the code-mode audit record: nothing until
// the first run, a run is begun, takes calls and is sealed, the chain verifies,
// every input is bounded, a sealed run takes no more writes, an unfinished run is
// closed as crashed, results are kept or not as the run says, and in hidden mode
// an address never reaches the store.
func codeRunsContract(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a, b := e.newCase("a"), e.newCase("b")
	sa, sb := coder(t, a), coder(t, b)
	ctx := e.ctx

	// Nothing yet: empty reads, an intact empty record.
	if runs, err := sa.ListRuns(ctx, casestore.RunQuery{}); err != nil || len(runs) != 0 {
		t.Fatalf("fresh ListRuns = %v %v", runs, err)
	}
	if rows, err := sa.ChainRows(ctx, 0, 0); err != nil || len(rows) != 0 {
		t.Fatalf("fresh ChainRows = %v %v", rows, err)
	}
	if rep := mustVerify(t, ctx, sa, nil); !rep.OK || rep.Rows != 0 || rep.Head != "" {
		t.Fatalf("fresh verify = %+v", rep)
	}
	if n, err := sa.CloseOrphanRuns(ctx); err != nil || n != 0 {
		t.Fatalf("fresh CloseOrphanRuns = %d %v", n, err)
	}
	if _, err := sa.GetRun(ctx, "00000000-0000-7000-8000-000000000000"); !errors.Is(err, casestore.ErrRunNotFound) {
		t.Fatalf("GetRun on none: %v", err)
	}
	if st, err := sa.CodeRunStats(ctx); err != nil || st.Runs != 0 {
		t.Fatalf("fresh stats = %+v %v", st, err)
	}

	// A whole run.
	run, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", CallerID: "unravis-cli", KeepResults: true, Reason: "why not",
		Source: "const r = await unravis.audit_compare({});\nreturn r;", Seed: "ab12", ClockMS: 1700000000000,
		ToolSchemaHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if !casestore.ValidRunID(run.RunID) || run.Status != casestore.RunRunning || run.CreatedMS == 0 {
		t.Fatalf("BeginRun = %+v", run)
	}
	if got, err := sa.GetRun(ctx, run.RunID); err != nil || got.Source != run.Source || got.Status != casestore.RunRunning {
		t.Fatalf("GetRun = %+v %v", got, err)
	}
	if rep := mustVerify(t, ctx, sa, nil); !rep.OK || rep.Rows != 1 || rep.OpenRuns != 1 {
		t.Fatalf("verify with an open run = %+v", rep)
	}
	if err := sa.RecordCall(ctx, run.RunID, okCall(1)); err != nil {
		t.Fatal(err)
	}
	two := okCall(2)
	two.Status, two.Result = casestore.CallError, []byte(`{"error":"no"}`)
	if err := sa.RecordCall(ctx, run.RunID, two); err != nil {
		t.Fatal(err)
	}
	if err := sa.RecordCall(ctx, run.RunID, okCall(1)); !errors.Is(err, casestore.ErrCallExists) {
		t.Errorf("a seq used twice: %v", err)
	}
	dup := okCall(3)
	dup.DoneSeq = 2
	if err := sa.RecordCall(ctx, run.RunID, dup); !errors.Is(err, casestore.ErrCallExists) {
		t.Errorf("a done_seq used twice: %v", err)
	}
	if err := sa.RecordCall(ctx, "00000000-0000-7000-8000-000000000000", okCall(1)); !errors.Is(err, casestore.ErrRunNotFound) {
		t.Errorf("a call for no run: %v", err)
	}
	if err := sb.RecordCall(ctx, run.RunID, okCall(1)); !errors.Is(err, casestore.ErrRunNotFound) {
		t.Errorf("a call into another case's run: %v", err)
	}
	if _, err := sa.BeginRun(ctx, casestore.RunStart{RunID: run.RunID, Surface: "cli", Source: "x"}); !errors.Is(err, casestore.ErrRunExists) {
		t.Errorf("run id in use: %v", err)
	}
	fin, err := sa.EndRun(ctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK, Result: []byte(`{"v":1}`), SandboxMode: "lpac",
		Runtime: "wazero-interp", ConsoleLogs: "hello", DurationMS: 40, ScriptMS: 9, StrippedJSHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if fin.Status != casestore.RunOK || fin.CallCount != 2 || fin.ResultBytes != 7 || fin.EndedMS < fin.CreatedMS || fin.ResultHash != casestore.SHA256Hex([]byte(`{"v":1}`)) {
		t.Fatalf("EndRun = %+v", fin)
	}
	if _, err := sa.EndRun(ctx, run.RunID, casestore.RunEnd{Status: casestore.RunOK}); !errors.Is(err, casestore.ErrRunFinished) {
		t.Errorf("second EndRun: %v", err)
	}
	if err := sa.RecordCall(ctx, run.RunID, okCall(3)); !errors.Is(err, casestore.ErrRunFinished) {
		t.Errorf("a call after the end: %v", err)
	}
	rep := mustVerify(t, ctx, sa, nil)
	if !rep.OK || rep.Rows != 2 || rep.Runs != 1 || rep.OpenRuns != 0 || rep.HeadN != 2 || len(rep.Head) != 64 || len(rep.Warnings) != 0 {
		t.Fatalf("verify = %+v", rep)
	}
	if rep := mustVerify(t, ctx, sa, &casestore.ChainAnchor{N: 1, Hash: mustChain(t, ctx, sa)[0].RecordHash}); !rep.OK || !rep.AnchorChecked {
		t.Errorf("verify against an earlier head = %+v", rep)
	}
	if rep := mustVerify(t, ctx, sb, nil); !rep.OK || rep.Rows != 0 {
		t.Errorf("another case saw the chain: %+v", rep)
	}

	calls, err := sa.RunCalls(ctx, run.RunID)
	if err != nil || len(calls) != 2 || calls[0].Seq != 1 || calls[1].Seq != 2 {
		t.Fatalf("RunCalls = %+v %v", calls, err)
	}
	if calls[0].ArgsJSON != `{"a":[1,2.5],"b":2}` || calls[0].BlobState != casestore.BlobKept || calls[0].Blob != nil || calls[0].BlobBytes == 0 || calls[0].ArgsHash != casestore.SHA256Hex([]byte(calls[0].ArgsJSON)) {
		t.Errorf("call 1 = %+v", calls[0])
	}
	if got, err := sa.CallResult(ctx, run.RunID, 1); err != nil || !bytes.Equal(got, okCall(1).Result) {
		t.Errorf("CallResult = %q %v", got, err)
	}
	if _, err := sa.CallResult(ctx, run.RunID, 9); !errors.Is(err, casestore.ErrRunNotFound) {
		t.Errorf("CallResult of no call: %v", err)
	}
	rows, err := sa.ChainRows(ctx, 0, 0)
	if err != nil || len(rows) != 2 || rows[0].Phase != "begin" || rows[1].Phase != "end" || rows[1].PrevHash != rows[0].RecordHash || rows[0].PrevHash != casestore.ChainZeroHash {
		t.Fatalf("ChainRows = %+v %v", rows, err)
	}
	if page, _ := sa.ChainRows(ctx, 1, 1); len(page) != 1 || page[0].N != 2 {
		t.Errorf("ChainRows after 1 = %+v", page)
	}
	list, err := sa.ListRuns(ctx, casestore.RunQuery{})
	if err != nil || len(list) != 1 || list[0].Source != "" || list[0].ConsoleLogs != "" || list[0].SourceBytes != len(run.Source) || list[0].ResultPreview != `{"v":1}` {
		t.Fatalf("ListRuns = %+v %v", list, err)
	}
	if got, _ := sa.GetRun(ctx, run.RunID); got.ConsoleLogs != "hello" || got.Source != run.Source {
		t.Errorf("GetRun after the end = %+v", got)
	}

	// Keep results off: hashes only, never a copy.
	off, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "mcp", CallerID: "claude", CallerDeclared: true, Source: "return 1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sa.RecordCall(ctx, off.RunID, okCall(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.EndRun(ctx, off.RunID, casestore.RunEnd{Status: casestore.RunOK}); err != nil {
		t.Fatal(err)
	}
	oc, _ := sa.RunCalls(ctx, off.RunID)
	if len(oc) != 1 || oc[0].BlobState != casestore.BlobOff || oc[0].BlobBytes != 0 || oc[0].ResultHash != casestore.SHA256Hex(okCall(1).Result) || oc[0].ResultBytes != int64(len(okCall(1).Result)) {
		t.Errorf("keep results off: %+v", oc)
	}
	if _, err := sa.CallResult(ctx, off.RunID, 1); !errors.Is(err, casestore.ErrBlobNotKept) {
		t.Errorf("CallResult with no copy: %v", err)
	}

	// Bounds and refusals. Nothing refused changes the record.
	rowsBefore := len(mustChain(t, ctx, sa))
	bad := map[string]casestore.RunStart{
		"surface":     {Surface: "tv", Source: "x"},
		"run id":      {RunID: "NOT-A-UUID", Surface: "cli", Source: "x"},
		"caller":      {Surface: "cli", Source: "x", CallerID: strings.Repeat("c", casestore.MaxRunCallerID+1)},
		"caller ctrl": {Surface: "cli", Source: "x", CallerID: "a\nb"},
		"reason":      {Surface: "cli", Source: "x", Reason: strings.Repeat("é", casestore.MaxRunReason+1)},
		"seed":        {Surface: "cli", Source: "x", Seed: "not a seed!"},
		"schema hash": {Surface: "cli", Source: "x", ToolSchemaHash: "abc"},
		"utf-8":       {Surface: "cli", Source: "a\xffb"},
	}
	for name, in := range bad {
		if _, err := sa.BeginRun(ctx, in); !errors.Is(err, casestore.ErrBadCodeRun) {
			t.Errorf("BeginRun(%s) = %v, want ErrBadCodeRun", name, err)
		}
	}
	if _, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: strings.Repeat("x", casestore.MaxRunSource+1)}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("source over the cap: %v", err)
	}
	if r, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: strings.Repeat("x", casestore.MaxRunSource)}); err != nil {
		t.Errorf("source at the cap: %v", err)
	} else if _, err := sa.EndRun(ctx, r.RunID, casestore.RunEnd{Status: casestore.RunError}); err != nil {
		t.Error(err)
	}
	cr, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "ui", Source: "x", KeepResults: true})
	if err != nil {
		t.Fatal(err)
	}
	badCalls := map[string]casestore.CallRecord{
		"seq 0":        {Seq: 0, DoneSeq: 1, Tool: "t", Status: "ok"},
		"seq big":      {Seq: casestore.MaxCallsPerRun + 1, DoneSeq: 1, Tool: "t", Status: "ok"},
		"tool":         {Seq: 1, DoneSeq: 1, Tool: "no spaces", Status: "ok"},
		"status":       {Seq: 1, DoneSeq: 1, Tool: "t", Status: "maybe"},
		"args json":    {Seq: 1, DoneSeq: 1, Tool: "t", Status: "ok", Args: []byte(`{"a":`)},
		"args dup":     {Seq: 1, DoneSeq: 1, Tool: "t", Status: "ok", Args: []byte(`{"a":1,"a":2}`)},
		"duration":     {Seq: 1, DoneSeq: 1, Tool: "t", Status: "ok", DurationMS: -1},
		"args too big": {Seq: 1, DoneSeq: 1, Tool: "t", Status: "ok", Args: []byte(`"` + strings.Repeat("a", casestore.MaxCallArgs) + `"`)},
	}
	for name, rec := range badCalls {
		err := sa.RecordCall(ctx, cr.RunID, rec)
		if name == "args too big" {
			if !errors.Is(err, casestore.ErrCapExceeded) {
				t.Errorf("RecordCall(%s) = %v, want ErrCapExceeded", name, err)
			}
		} else if !errors.Is(err, casestore.ErrBadCodeRun) {
			t.Errorf("RecordCall(%s) = %v, want ErrBadCodeRun", name, err)
		}
	}
	// A result over MaxCallBlob is hashed and not copied; at the cap it is copied.
	big := bytes.Repeat([]byte("a"), casestore.MaxCallBlob+1)
	if err := sa.RecordCall(ctx, cr.RunID, casestore.CallRecord{Seq: 1, DoneSeq: 1, Tool: "t", Status: "ok", Result: big}); err != nil {
		t.Fatal(err)
	}
	if err := sa.RecordCall(ctx, cr.RunID, casestore.CallRecord{Seq: 2, DoneSeq: 2, Tool: "t", Status: "ok", Result: big[:casestore.MaxCallBlob]}); err != nil {
		t.Fatal(err)
	}
	if err := sa.RecordCall(ctx, cr.RunID, casestore.CallRecord{Seq: 3, DoneSeq: 3, Tool: "t", Status: "ok", Result: make([]byte, casestore.MaxCallResult+1)}); !errors.Is(err, casestore.ErrCapExceeded) {
		t.Errorf("result over the hard cap: %v", err)
	}
	// Texts are cut, not refused.
	long := strings.Repeat("é", casestore.MaxRunLogs)
	fin2, err := sa.EndRun(ctx, cr.RunID, casestore.RunEnd{Status: casestore.RunError, ErrorText: long, ConsoleLogs: long, Result: []byte(long)})
	if err != nil {
		t.Fatal(err)
	}
	if len(fin2.ErrorText) > casestore.MaxRunErrorText || len(fin2.ConsoleLogs) > casestore.MaxRunLogs || len(fin2.ResultPreview) > casestore.MaxRunPreview {
		t.Errorf("texts not cut: %d %d %d", len(fin2.ErrorText), len(fin2.ConsoleLogs), len(fin2.ResultPreview))
	}
	cc, _ := sa.RunCalls(ctx, cr.RunID)
	if len(cc) != 2 || cc[0].BlobState != casestore.BlobOversize || cc[1].BlobState != casestore.BlobKept {
		t.Errorf("blob states = %+v", cc)
	}
	if rep := mustVerify(t, ctx, sa, nil); !rep.OK {
		t.Errorf("verify after refusals: %+v", rep)
	}
	if _, err := sa.EndRun(ctx, run.RunID, casestore.RunEnd{Status: "running"}); !errors.Is(err, casestore.ErrRunFinished) && !errors.Is(err, casestore.ErrBadCodeRun) {
		t.Errorf("EndRun with status running: %v", err)
	}
	if n := len(mustChain(t, ctx, sa)); n <= rowsBefore {
		t.Errorf("chain did not grow: %d", n)
	}
	bad2, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sa.EndRun(ctx, bad2.RunID, casestore.RunEnd{Status: "running"}); !errors.Is(err, casestore.ErrBadCodeRun) {
		t.Errorf("EndRun with status running: %v", err)
	}
	if _, err := sa.EndRun(ctx, bad2.RunID, casestore.RunEnd{Status: casestore.RunOK, SandboxMode: "yolo"}); !errors.Is(err, casestore.ErrBadCodeRun) {
		t.Errorf("EndRun with a made-up sandbox mode: %v", err)
	}
	if _, err := sa.EndRun(ctx, bad2.RunID, casestore.RunEnd{Status: casestore.RunCancelled}); err != nil {
		t.Fatalf("a refused EndRun left the run open for a good one: %v", err)
	}

	// Hidden mode: an address in free text is replaced; one in arguments or a
	// result keeps only the hash. Allow mode keeps everything.
	hid, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "return 1", KeepResults: true, Reason: "look at 10.1.2.3 and fe80::1ff:fe23:4567:890a"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hid.Reason, "10.1.2.3") || strings.Contains(hid.Reason, "fe80") || !strings.Contains(hid.Reason, "[ip]") {
		t.Errorf("reason kept an address: %q", hid.Reason)
	}
	rec := casestore.CallRecord{Seq: 1, DoneSeq: 1, Tool: "audit_get_requests", Status: "ok", Args: []byte(`{"ip":"203.0.113.9"}`), Result: []byte(`{"from":"198.51.100.7","time":"12:30:45","v":"1.2.3"}`)}
	if err := sa.RecordCall(ctx, hid.RunID, rec); err != nil {
		t.Fatal(err)
	}
	clean := casestore.CallRecord{Seq: 2, DoneSeq: 2, Tool: "audit_get_requests", Status: "ok", Args: []byte(`{"v":"1.2.3","t":"12:30:45","n":"std::bad"}`), Result: []byte(`{"n":"Foo::Bar"}`)}
	if err := sa.RecordCall(ctx, hid.RunID, clean); err != nil {
		t.Fatal(err)
	}
	hf, err := sa.EndRun(ctx, hid.RunID, casestore.RunEnd{Status: casestore.RunError, ErrorText: "connect 192.0.2.1:443 failed", ConsoleLogs: "peer 2001:db8::1 said hi", Result: []byte(`{"ip":"192.0.2.55"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{hf.ErrorText, hf.ConsoleLogs, hf.ResultPreview} {
		if strings.Contains(s, "192.0.2") || strings.Contains(s, "2001:db8") || !strings.Contains(s, "[ip]") {
			t.Errorf("hidden-mode text kept an address: %q", s)
		}
	}
	hc, _ := sa.RunCalls(ctx, hid.RunID)
	if hc[0].ArgsState != casestore.ArgsWithheld || hc[0].ArgsJSON != "" || hc[0].BlobState != casestore.BlobWithheld || hc[0].BlobBytes != 0 ||
		hc[0].ArgsHash == "" || hc[0].ResultHash != casestore.SHA256Hex(rec.Result) {
		t.Errorf("hidden call with addresses: %+v", hc[0])
	}
	if hc[1].ArgsState != casestore.ArgsKept || hc[1].BlobState != casestore.BlobKept {
		t.Errorf("times, versions and C++ names were taken for addresses: %+v", hc[1])
	}
	if _, err := sa.CallResult(ctx, hid.RunID, 1); !errors.Is(err, casestore.ErrBlobNotKept) {
		t.Errorf("withheld result: %v", err)
	}
	allow, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "return 1", KeepResults: true, AllowIdentifiers: true, Reason: "look at 10.1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if !allow.AllowIdentifiers || allow.Reason != "look at 10.1.2.3" {
		t.Errorf("allow mode changed the reason: %+v", allow)
	}
	if err := sa.RecordCall(ctx, allow.RunID, rec); err != nil {
		t.Fatal(err)
	}
	ac, _ := sa.RunCalls(ctx, allow.RunID)
	if ac[0].ArgsState != casestore.ArgsKept || ac[0].ArgsJSON != `{"ip":"203.0.113.9"}` || ac[0].BlobState != casestore.BlobKept {
		t.Errorf("allow mode call: %+v", ac[0])
	}
	if _, err := sa.EndRun(ctx, allow.RunID, casestore.RunEnd{Status: casestore.RunOK}); err != nil {
		t.Fatal(err)
	}

	// An unfinished run is closed as crashed, once.
	orphan, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "while(true){}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sa.RecordCall(ctx, orphan.RunID, okCall(1)); err != nil {
		t.Fatal(err)
	}
	if n, err := sa.CloseOrphanRuns(ctx); err != nil || n != 1 {
		t.Fatalf("CloseOrphanRuns = %d %v", n, err)
	}
	if got, _ := sa.GetRun(ctx, orphan.RunID); got.Status != casestore.RunCrashed || got.CallCount != 1 || got.EndedMS == 0 {
		t.Errorf("orphan = %+v", got)
	}
	if _, err := sa.EndRun(ctx, orphan.RunID, casestore.RunEnd{Status: casestore.RunOK}); !errors.Is(err, casestore.ErrRunFinished) {
		t.Errorf("late EndRun of a crashed run: %v", err)
	}
	if n, _ := sa.CloseOrphanRuns(ctx); n != 0 {
		t.Errorf("second CloseOrphanRuns = %d", n)
	}
	if rep := mustVerify(t, ctx, sa, nil); !rep.OK || rep.OpenRuns != 0 {
		t.Fatalf("verify after the crash close: %+v", rep)
	}

	// Nothing is ever deleted by itself: every run is still there, newest first.
	var ids []string
	for i := 0; i < 30; i++ {
		r, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "return 1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sa.EndRun(ctx, r.RunID, casestore.RunEnd{Status: casestore.RunOK}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.RunID)
	}
	all, err := sa.ListRuns(ctx, casestore.RunQuery{Limit: casestore.MaxRunsPage})
	if err != nil || len(all) != 38 || all[0].RunID != ids[len(ids)-1] {
		t.Fatalf("ListRuns = %d runs %v (first %v)", len(all), err, all[0].RunID)
	}
	if page, _ := sa.ListRuns(ctx, casestore.RunQuery{Limit: 5, Offset: 2}); len(page) != 5 || page[0].RunID != ids[len(ids)-3] {
		t.Errorf("paged ListRuns = %d", len(page))
	}
	if st, _ := sa.CodeRunStats(ctx); st.Runs != 38 || st.OpenRuns != 0 || st.Calls < 8 || st.BlobBytes == 0 {
		t.Errorf("stats = %+v", st)
	}

	// Handles, closed and erased cases, cancelled contexts.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := sa.BeginRun(cctx, casestore.RunStart{Surface: "cli", Source: "x"}); err == nil {
		t.Error("BeginRun ran on a cancelled context")
	}
	last, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "in flight when the case closes"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.store.Open(ctx, a.Info().ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := coder(t, again).GetRun(ctx, last.RunID); got.Source != last.Source {
		t.Errorf("a second handle reads %+v", got)
	}
	_ = again.Close()
	if _, err := coder(t, again).GetRun(ctx, last.RunID); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle read: %v", err)
	}
	if _, err := coder(t, again).BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "x"}); !errors.Is(err, casestore.ErrHandleClosed) {
		t.Errorf("closed handle write: %v", err)
	}
	if err := e.store.CloseCase(ctx, a.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "x"}); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("BeginRun on a closed case: %v", err)
	}
	if err := sa.RecordCall(ctx, last.RunID, okCall(1)); !errors.Is(err, casestore.ErrCaseClosed) {
		t.Errorf("RecordCall on a closed case: %v", err)
	}
	if _, err := sa.GetRun(ctx, last.RunID); err != nil {
		t.Errorf("a closed case reads: %v", err)
	}
	if _, err := sa.EndRun(ctx, last.RunID, casestore.RunEnd{Status: casestore.RunCancelled}); err != nil {
		t.Errorf("a run in flight is sealed on a closed case: %v", err)
	}
	if rep := mustVerify(t, ctx, sa, nil); !rep.OK {
		t.Errorf("verify on a closed case: %+v", rep)
	}

	if err := e.store.Erase(ctx, b.Info().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.ListRuns(ctx, casestore.RunQuery{}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased ListRuns: %v", err)
	}
	if _, err := sb.BeginRun(ctx, casestore.RunStart{Surface: "cli", Source: "x"}); !errors.Is(err, casestore.ErrNotFound) {
		t.Errorf("erased BeginRun: %v", err)
	}
}

func mustChain(t *testing.T, ctx context.Context, s casestore.CodeRunStore) []casestore.ChainRow {
	t.Helper()
	var out []casestore.ChainRow
	var after int64
	for {
		page, err := s.ChainRows(ctx, after, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			return out
		}
		out = append(out, page...)
		after = page[len(page)-1].N
	}
}
