package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// The caller the harness's client is made as: raw connections in these tests use the same one, so they land on the same seat.
var harnessCaller = Caller{SID: testSID, Name: `PC\analyst`, PID: 4242, Session: 1}

// rawAnalyze starts one audit.analyze read without the client's own limits and returns the connection and a channel for the
// keeper's first answer frame.
func (h *harness) rawAnalyze(r *AuditRouter, sourceID string, timeoutMs int) (ClientConn, <-chan Response) {
	h.t.Helper()
	conn, err := h.l.Dialer(harnessCaller).Dial(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	args, _ := json.Marshal(AuditArgs[audit.Query]{Instance: r.instance, Input: audit.Query{SourceIDs: []string{sourceID}}, TimeoutMs: timeoutMs})
	if err := writeJSONFrame(conn, Request{V: ProtocolVersion, Op: OpAuditAnalyze, Args: args}); err != nil {
		h.t.Fatal(err)
	}
	out := make(chan Response, 1)
	go func() {
		var resp Response
		if frame, err := ReadFrame(conn); err == nil {
			_ = json.Unmarshal(frame, &resp)
		}
		out <- resp
	}()
	return conn, out
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) heldSource(r *AuditRouter) string {
	h.t.Helper()
	src, err := r.AddHeldSource(context.Background(), h.rememberMongo(mongoSecret))
	if err != nil {
		h.t.Fatal(err)
	}
	return src.ID
}

// A burst of slow audit reads (one dashboard load sends several at once, and a PC may have several people on it) must leave room in
// the keeper's pool for the short sql, mongo and bb operations: the reads beyond the audit budget are answered "busy, try again" at once,
// and an sql or Blackbox operation still gets through while the others run.
func TestAFloodOfSlowAuditReadsLeavesRoomForOtherOperations(t *testing.T) {
	h := newHarness(t, nil)
	r := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	id := h.heldSource(r)
	h.hosted.gate = make(chan struct{})

	const reads = 8 // one session may hold 8 connections: all of them used for audit reads
	var conns []ClientConn
	var answers []<-chan Response
	for i := 0; i < reads; i++ {
		// The reads already started are held open; every earlier connection (the one that stored the source) must be released first,
		// or the eighth read would meet the per-session cap for a reason this test is not about.
		h.server.waitOpenAtMost(t, harnessCaller.Session, i)
		c, a := h.rawAnalyze(r, id, 0)
		conns = append(conns, c)
		answers = append(answers, a)
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	waitFor(t, "the audit run slots to fill", func() bool { return h.hosted.started.Load() == int32(cap(h.server.auditRuns)) })

	// Exactly the reads beyond the audit budget are answered at once, as busy with the audit scope. Wait for that many answers (no
	// fixed delay: a slow machine only makes it take longer); the reads inside the budget are held by the gate and cannot answer.
	type answered struct {
		i    int
		resp Response
	}
	arrived := make(chan answered, reads)
	for i, a := range answers {
		go func() { arrived <- answered{i, <-a} }()
	}
	want := reads - cap(h.server.auditConns)
	refused := map[int]bool{}
	for len(refused) < want {
		select {
		case got := <-arrived:
			if got.resp.OK || got.resp.Error == nil || got.resp.Error.Code != CodeBusy || !strings.Contains(string(got.resp.Error.Detail), `"audit"`) {
				t.Fatalf("read %d was answered %+v", got.i, got.resp)
			}
			refused[got.i] = true
			conns[got.i].Close()
		case <-time.After(10 * time.Second):
			t.Fatalf("%d reads refused busy, want %d", len(refused), want)
		}
	}
	select {
	case got := <-arrived:
		t.Fatalf("a read inside the audit budget was answered while the gate was shut: read %d %+v", got.i, got.resp)
	default:
	}
	if len(refused) != want {
		t.Fatalf("%d reads refused busy, want %d", len(refused), want)
	}

	// Other operations are not starved while the audit reads hold their share.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.client.Call(ctx, OpSQLList, nil, nil); err != nil {
		t.Fatalf("sql.list during the flood: %v", err)
	}
	if err := h.client.Call(ctx, OpBBList, nil, nil); err != nil {
		t.Fatalf("bb.list during the flood: %v", err)
	}
	if err := h.client.Call(ctx, OpPing, nil, nil); err != nil {
		t.Fatalf("ping during the flood: %v", err)
	}

	// When the slow reads finish, the ones that waited for a run slot are answered too.
	close(h.hosted.gate)
	for n := 0; n < cap(h.server.auditConns); n++ {
		select {
		case got := <-arrived:
			if !got.resp.OK {
				t.Fatalf("waiting read %d failed: %+v", got.i, got.resp)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d waiting reads were answered", n, cap(h.server.auditConns))
		}
	}
}

// A client that goes away frees the connection and the run slot at once, not when the read would have ended.
func TestAnAuditReadStopsWhenItsClientGoesAway(t *testing.T) {
	h := newHarness(t, nil)
	r := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	id := h.heldSource(r)
	h.hosted.gate = make(chan struct{})
	defer close(h.hosted.gate)

	conn, _ := h.rawAnalyze(r, id, 0)
	waitFor(t, "the read to start", func() bool { return h.hosted.started.Load() == 1 })
	if len(h.server.auditConns) != 1 || len(h.server.auditRuns) != 1 {
		t.Fatalf("budget in use: %d connections, %d runs", len(h.server.auditConns), len(h.server.auditRuns))
	}
	conn.Close()
	waitFor(t, "the read to be canceled", func() bool { return h.hosted.canceled.Load() == 1 })
	waitFor(t, "the audit budget to be given back", func() bool { return len(h.server.auditConns) == 0 && len(h.server.auditRuns) == 0 })
	waitFor(t, "the connection to be given back", func() bool { return len(h.server.sem) == 0 })
}

// A read is stopped after the time its caller will wait, and never runs past the keeper's own cap.
func TestAnAuditReadIsBoundedByItsCallerAndByTheCap(t *testing.T) {
	if auditQueryTimeout > 2*time.Minute || auditQueryTimeout >= audit.OperationTimeout {
		t.Fatalf("the keeper's cap on one audit read is %v", auditQueryTimeout)
	}
	h := newHarness(t, nil)
	r := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	id := h.heldSource(r)
	h.hosted.gate = make(chan struct{})
	defer close(h.hosted.gate)

	start := time.Now()
	conn, answer := h.rawAnalyze(r, id, 100)
	defer conn.Close()
	select {
	case resp := <-answer:
		if resp.OK || resp.Error == nil || resp.Error.Code != CodeAudit || !strings.Contains(string(resp.Error.Detail), "timeout") {
			t.Fatalf("answer = %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a read with a 100 ms limit was still running after 5 s")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}

	// Through the router the caller's own deadline travels to the keeper.
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, err := r.Analyze(ctx, audit.Query{SourceIDs: []string{id}}); err == nil {
		t.Fatal("a stuck read returned an answer")
	}
	waitFor(t, "the audit budget to be given back", func() bool { return len(h.server.auditConns) == 0 })
}

// The keeper does not build or send an answer past its cap.
func TestAuditAnswerPastTheCapIsRefusedByTheKeeper(t *testing.T) {
	h := newHarness(t, nil)
	r := NewAuditRouter(&fakeHosted{prefix: "local"}, h.client)
	id := h.heldSource(r)
	old := auditAnswerCap
	auditAnswerCap = 4096
	t.Cleanup(func() { auditAnswerCap = old })

	h.hosted.set("", 20000)
	_, err := r.Analyze(context.Background(), audit.Query{SourceIDs: []string{id}})
	if !errors.Is(err, audit.ErrLimit) {
		t.Fatalf("an oversized answer: %v", err)
	}
	if strings.Contains(h.wire.String(), strings.Repeat("w", 200)) {
		t.Fatal("the keeper streamed an answer that is past its cap")
	}
	// An answer under the cap still crosses.
	h.hosted.set("", 0)
	if _, err := r.Analyze(context.Background(), audit.Query{SourceIDs: []string{id}}); err != nil {
		t.Fatalf("a small answer: %v", err)
	}
}
