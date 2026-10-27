package dbhealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func jd(t time.Time) map[string]any { return map[string]any{"$date": t.UTC().Format(time.RFC3339)} }

func profDoc(t time.Time, ns, plan string) map[string]any {
	return map[string]any{"ts": jd(t), "ns": ns, "op": "query", "millis": float64(900), "planSummary": plan}
}

func batch(docs ...map[string]any) map[string]any {
	arr := make([]any, len(docs))
	for i, d := range docs {
		arr[i] = d
	}
	return map[string]any{"cursor": map[string]any{"firstBatch": arr}}
}

// mongoOK is a standalone server whose profiler has run since before the reference window.
func mongoOK(docs ...map[string]any) *fakeMongo {
	return &fakeMongo{by: map[string]map[string]any{
		"serverStatus": {"version": "7.0.5", "uptime": float64(86400 * 9)},
		"profile":      {"was": float64(1), "slowms": float64(100)},
		"find":         batch(docs...),
	}}
}

// The profiler's oldest record and the window read are both "find" commands, so this responder tells them apart.
type profMongo struct {
	*fakeMongo
	oldest map[string]any
	window map[string]any
}

func (p *profMongo) Command(ctx context.Context, db, cmd string) (map[string]any, error) {
	p.calls = append(p.calls, cmd)
	if cmd == profileOldest {
		if p.oldest != nil {
			return p.oldest, nil
		}
		return batch(), nil
	}
	return p.fakeMongo.Command(ctx, db, cmd)
}

func newProf(oldest time.Time, docs ...map[string]any) *profMongo {
	f := mongoOK(docs...)
	p := &profMongo{fakeMongo: f}
	if !oldest.IsZero() {
		p.oldest = batch(map[string]any{"ts": jd(oldest)})
	}
	return p
}

func mreq() LaneRequest {
	r := laneReq
	r.Database, r.Collection = "AuditDb", "Logs"
	return r
}

func TestMongoLaneCollscanInIncidentIsM6Yes(t *testing.T) {
	m := newProf(at(1, 0, 0),
		profDoc(at(19, 10, 10), "AuditDb.Logs", "IXSCAN { ts: 1 }"),
		profDoc(at(20, 10, 12), "AuditDb.Logs", "COLLSCAN"),
		profDoc(at(20, 10, 12), "Other.Logs", "COLLSCAN"),
	)
	r := RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	if r.Kind != "mongo" || r.Lane == nil || r.Lane.Answers["M6"] != AnswerYes {
		t.Fatalf("report = %+v", r.Lane)
	}
	if got := r.Lane.State(); got != StateReady {
		t.Errorf("state = %s", got)
	}
	if r.Version != "7.0.5" || r.Lane.Guide != nil {
		t.Errorf("version %q guide %v", r.Version, r.Lane.Guide)
	}
	for _, c := range m.calls {
		if err := ReadOnlyCommand(c); err != nil {
			t.Errorf("sent %s: %v", c, err)
		}
	}
}

func TestMongoLaneOtherDatabaseScanDoesNotCount(t *testing.T) {
	m := newProf(at(1, 0, 0),
		profDoc(at(20, 10, 12), "Other.Logs", "COLLSCAN"),
		profDoc(at(20, 10, 13), "AuditDb.Logs", "IXSCAN { ts: 1 }"),
	)
	r := RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	if r.Lane.Answers["M6"] != AnswerNo {
		t.Fatalf("answers = %v", r.Lane.Answers)
	}
}

func TestMongoLaneIndexedOnlyIsNoOnlyWhenProfilerCoversIncident(t *testing.T) {
	docs := []map[string]any{profDoc(at(20, 10, 40), "AuditDb.Logs", "IXSCAN { ts: 1 }")}
	// The profiler began half way through the incident: indexed operations after that prove nothing about the first half.
	r := RunMongoLane(context.Background(), newProf(at(20, 10, 30), docs...), "Mongo", mreq(), fixedNow)
	if _, ok := r.Lane.Answers["M6"]; ok {
		t.Errorf("partial coverage must not give a no: %v", r.Lane.Answers)
	}
	if !strings.Contains(checkByID(t, r, CheckMongoProfiler).Summary, "part of the incident") {
		t.Errorf("summary = %s", checkByID(t, r, CheckMongoProfiler).Summary)
	}
	r = RunMongoLane(context.Background(), newProf(at(2, 0, 0), docs...), "Mongo", mreq(), fixedNow)
	if r.Lane.Answers["M6"] != AnswerNo {
		t.Errorf("full coverage, all indexed = no: %v", r.Lane.Answers)
	}
}

func TestMongoLaneNoSlowOperationsIsNotAPass(t *testing.T) {
	r := RunMongoLane(context.Background(), newProf(at(2, 0, 0)), "Mongo", mreq(), fixedNow)
	if _, ok := r.Lane.Answers["M6"]; ok {
		t.Errorf("no records is not a no: %v", r.Lane.Answers)
	}
	// An empty profile collection says nothing about how long the profiler has been on.
	r = RunMongoLane(context.Background(), newProf(time.Time{}), "Mongo", mreq(), fixedNow)
	if _, ok := r.Lane.Answers["M6"]; ok || !strings.Contains(checkByID(t, r, CheckMongoProfiler).Summary, "empty") {
		t.Errorf("empty profile: %v / %s", r.Lane.Answers, checkByID(t, r, CheckMongoProfiler).Summary)
	}
}

func TestMongoLaneProfilerOffGivesGuide(t *testing.T) {
	m := mongoOK()
	m.by["profile"] = map[string]any{"was": float64(0), "slowms": float64(100)}
	r := RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	c := checkByID(t, r, CheckMongoProfiler)
	if c.State != StateNotYet || c.Fix == nil || !strings.Contains(c.Fix.Script, "setProfilingLevel(1") {
		t.Fatalf("profiler off = %+v", c)
	}
	if r.Lane.Guide == nil || r.Lane.Guide.SwitchOn != c.Fix.Script || len(r.Lane.Guide.Steps) == 0 {
		t.Errorf("guide = %+v", r.Lane.Guide)
	}
	if _, ok := r.Lane.Answers["M6"]; ok {
		t.Error("M6 answered with the profiler off")
	}
	if r.Lane.State() != "Partly ready" {
		t.Errorf("state = %s", r.Lane.State())
	}
}

func TestMongoLaneProfilerTooYoung(t *testing.T) {
	r := RunMongoLane(context.Background(), newProf(at(22, 8, 0), profDoc(at(22, 9, 0), "AuditDb.Logs", "COLLSCAN")), "Mongo", mreq(), fixedNow)
	c := checkByID(t, r, CheckMongoProfiler)
	if c.State != StateNotYet || !strings.Contains(c.Summary, "after the incident ended") {
		t.Fatalf("check = %+v", c)
	}
	if _, ok := r.Lane.Answers["M6"]; ok {
		t.Error("M6 answered from a profiler that started after the incident")
	}
}

func TestMongoLaneOnset(t *testing.T) {
	var docs []map[string]any
	docs = append(docs, profDoc(at(19, 10, 5), "AuditDb.Logs", "COLLSCAN")) // a quiet reference window: one slow op in an hour
	for i := 0; i < 4; i++ {
		docs = append(docs, profDoc(at(20, 10, 20).Add(time.Duration(i)*time.Second), "AuditDb.Logs", "COLLSCAN"))
	}
	docs = append(docs, profDoc(at(20, 10, 5), "AuditDb.Logs", "IXSCAN { a: 1 }")) // a lone slow op is not an onset
	r := RunMongoLane(context.Background(), newProf(at(1, 0, 0), docs...), "Mongo", mreq(), fixedNow)
	if r.Lane.Onset == nil || !r.Lane.Onset.Equal(at(20, 10, 20)) {
		t.Fatalf("onset = %v", r.Lane.Onset)
	}
	// Profiler younger than the reference window: no onset, because the rate before the incident is unknown.
	r = RunMongoLane(context.Background(), newProf(at(20, 0, 0), docs...), "Mongo", mreq(), fixedNow)
	if r.Lane.Onset != nil {
		t.Errorf("onset without a reference rate: %v", r.Lane.Onset)
	}
}

func TestMongoLaneMissingGrants(t *testing.T) {
	denied := errors.New("not authorized on admin to execute command { serverStatus: 1 } for user unravis_ro@PRODMONGO")
	m := mongoOK()
	m.err = map[string]error{"serverStatus": denied}
	r := RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	c := checkByID(t, r, CheckMongoServer)
	if c.State != StateNotYet || c.Fix == nil || !strings.Contains(c.Fix.Permission, "clusterMonitor") || !strings.Contains(c.Fix.Script, `"clusterMonitor"`) {
		t.Fatalf("server = %+v", c)
	}
	if strings.Contains(c.Summary, "PRODMONGO") {
		t.Errorf("driver text leaked: %s", c.Summary)
	}
	if checkByID(t, r, CheckMongoProfiler).State != StateReady {
		t.Errorf("profiler should still run without clusterMonitor")
	}

	m = mongoOK()
	m.err = map[string]error{"profile": errors.New("not authorized on AuditDb to execute command { profile: -1 }")}
	r = RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	c = checkByID(t, r, CheckMongoProfiler)
	if c.State != StateNotYet || c.Fix == nil || !strings.Contains(c.Fix.Permission, "read on the AuditDb") || !strings.Contains(c.Fix.Script, `role: "read", db: "AuditDb"`) {
		t.Fatalf("profiler = %+v", c)
	}
	if len(r.Lane.Answers) != 0 {
		t.Errorf("answers = %v", r.Lane.Answers)
	}
}

func TestMongoLaneDownMidReadKeepsEarlierCheck(t *testing.T) {
	m := mongoOK()
	m.err = map[string]error{"profile": errors.New("connection(PRODMONGO:27017) socket was unexpectedly closed: EOF")}
	r := RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	if checkByID(t, r, CheckMongoServer).State != StateReady {
		t.Error("the check that finished must keep its answer")
	}
	c := checkByID(t, r, CheckMongoProfiler)
	if c.State != StateFailed || c.Fix != nil || strings.Contains(c.Summary, "PRODMONGO") {
		t.Errorf("profiler = %+v", c)
	}
	if r.Lane.State() != "Partly ready" || r.Overall != OK {
		t.Errorf("state %s overall %s", r.Lane.State(), r.Overall)
	}

	// Down while reading the records themselves (the third profiler command).
	p := newProf(at(1, 0, 0))
	p.fakeMongo.err = map[string]error{"find": errors.New("i/o timeout")}
	p.oldest = batch(map[string]any{"ts": jd(at(1, 0, 0))})
	// profileOldest is answered by the wrapper before the error table, so only the window read fails.
	r = RunMongoLane(context.Background(), p, "Mongo", mreq(), fixedNow)
	if checkByID(t, r, CheckMongoProfiler).State != StateFailed {
		t.Errorf("window read failure = %+v", checkByID(t, r, CheckMongoProfiler))
	}
}

func TestMongoLaneNeedsADatabaseAndHonoursCancel(t *testing.T) {
	req := mreq()
	req.Database = ""
	r := RunMongoLane(context.Background(), mongoOK(), "Mongo", req, fixedNow)
	if checkByID(t, r, CheckMongoProfiler).State != StateNotYet {
		t.Error("no database should be cannot test yet")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = RunMongoLane(ctx, mongoOK(), "Mongo", mreq(), fixedNow)
	for _, c := range r.Lane.Checks {
		if c.State != StateFailed {
			t.Errorf("%s = %s after cancel", c.ID, c.State)
		}
	}
}

func TestMongoLaneNeverSendsAWrite(t *testing.T) {
	m := newProf(at(1, 0, 0), profDoc(at(20, 10, 12), "AuditDb.Logs", "COLLSCAN"))
	RunMongoLane(context.Background(), m, "Mongo", mreq(), fixedNow)
	if len(m.calls) < 4 {
		t.Fatalf("only %d commands sent: %v", len(m.calls), m.calls)
	}
	for _, c := range m.calls {
		if err := ReadOnlyCommand(c); err != nil {
			t.Errorf("sent %s", c)
		}
	}
	// Even if the profiler guide were wired to the client by mistake, the guard refuses the switch-on commands.
	for _, c := range []string{`{"profile":1,"slowms":100}`, `{"profile":0}`, `{"drop":"system.profile"}`} {
		if err := ReadOnlyCommand(c); err == nil {
			t.Errorf("%s must be refused", c)
		}
	}
}
