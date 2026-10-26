package dbhealth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeMongo struct {
	calls []string
	by    map[string]map[string]any // first key of the command -> reply
	err   map[string]error
}

func (f *fakeMongo) Command(_ context.Context, _ string, cmd string) (map[string]any, error) {
	f.calls = append(f.calls, cmd)
	for k, e := range f.err {
		if strings.HasPrefix(cmd, `{"`+k+`"`) {
			return nil, e
		}
	}
	for k, r := range f.by {
		if strings.HasPrefix(cmd, `{"`+k+`"`) {
			return r, nil
		}
	}
	return map[string]any{}, nil
}

func TestReadOnlyCommandAllowsOnlyDiagnostics(t *testing.T) {
	ok := []string{
		`{"serverStatus":1}`, `{"currentOp":1,"active":true}`, `{"replSetGetStatus":1}`, `{"dbStats":1}`,
		`{"collStats":"c"}`, `{"listIndexes":"c"}`, `{"profile":-1}`, `{"find":"system.profile","filter":{}}`,
	}
	for _, c := range ok {
		if err := ReadOnlyCommand(c); err != nil {
			t.Errorf("%s should be allowed: %v", c, err)
		}
	}
	bad := []string{
		`{"profile":2}`, `{"profile":0}`, `{"find":"orders"}`, `{"drop":"c"}`, `{"delete":"c","deletes":[]}`,
		`{"insert":"c","documents":[]}`, `{"update":"c"}`, `{"createIndexes":"c"}`, `{"killOp":1,"op":5}`,
		`{"aggregate":"c","pipeline":[{"$out":"x"}]}`, `{"find":"system.profile","filter":{"$where":"sleep(9999)"}}`,
		`{"dropDatabase":1}`, `not json`, ``, `[]`, `{}`,
	}
	for _, c := range bad {
		if err := ReadOnlyCommand(c); err == nil {
			t.Errorf("%s must be refused", c)
		}
	}
}

func TestRunMongoNeverSendsAWrite(t *testing.T) {
	f := &fakeMongo{by: map[string]map[string]any{"profile": {"was": float64(1)}}}
	RunMongo(context.Background(), f, "M", "AuditDb", "Logs", fixedNow)
	for _, c := range f.calls {
		if err := ReadOnlyCommand(c); err != nil {
			t.Errorf("a check sent %s", c)
		}
	}
	if len(f.calls) < 5 {
		t.Fatalf("expected the checks to run, got %d calls", len(f.calls))
	}
}

func TestRunMongoSurvivesPermissionDenied(t *testing.T) {
	f := &fakeMongo{err: map[string]error{"serverStatus": errors.New("not authorized on admin to execute command on host PRODMONGO01")}}
	rep := RunMongo(context.Background(), f, "M", "AuditDb", "Logs", fixedNow)
	if len(rep.Findings) != 6 {
		t.Fatalf("want 6 findings, got %d", len(rep.Findings))
	}
	s := rep.Findings[0]
	if s.Status != Unavailable || s.Summary != reasonPermission || strings.Contains(s.Summary, "PRODMONGO01") {
		t.Fatalf("server: %+v", s)
	}
}

func TestEvalServerStatus(t *testing.T) {
	r := map[string]any{
		"version": "6.0.5", "uptime": float64(86400 * 3),
		"connections": map[string]any{"current": float64(950), "available": float64(50)},
		"globalLock":  map[string]any{"currentQueue": map[string]any{"total": float64(0)}},
	}
	env := &Env{}
	f := evalServerStatus(r, env)
	if f.Status != Bad || env.Version != "6.0.5" {
		t.Fatalf("95%% connections should be bad: %+v", f)
	}
	r["connections"] = map[string]any{"current": float64(100), "available": float64(900)}
	if got := evalServerStatus(r, &Env{}).Status; got != OK {
		t.Fatalf("healthy: %s", got)
	}
	r["uptime"] = float64(600)
	if got := evalServerStatus(r, &Env{}).Status; got != Warn {
		t.Fatalf("recent restart: %s", got)
	}
}

func TestEvalServerStatusReadsNumberLong(t *testing.T) {
	r := map[string]any{
		"version": "7", "uptime": map[string]any{"$numberLong": "999999"},
		"connections": map[string]any{"current": map[string]any{"$numberInt": "10"}, "available": map[string]any{"$numberInt": "990"}},
	}
	if f := evalServerStatus(r, &Env{}); f.Status != OK {
		t.Fatalf("%+v", f)
	}
}

func TestEvalCurrentOpHidesCommandTextAndFlagsScans(t *testing.T) {
	ops := []map[string]any{
		{"op": "query", "ns": "audit.Logs", "secs_running": float64(42), "planSummary": "COLLSCAN", "command": map[string]any{"find": "Logs", "filter": map[string]any{"secret": "x"}}},
		{"op": "getmore", "ns": "local.oplog.rs", "secs_running": float64(900)},
	}
	f := evalCurrentOp(ops)
	if f.Status != Warn || !strings.Contains(f.Summary, "COLLSCAN") {
		t.Fatalf("%+v", f)
	}
	for _, row := range f.Rows {
		for _, c := range row {
			if strings.Contains(c, "secret") {
				t.Fatal("command text leaked")
			}
		}
	}
	if len(f.Rows) != 1 {
		t.Fatalf("oplog tail should be ignored: %+v", f.Rows)
	}
	if evalCurrentOp(nil).Status != OK {
		t.Error("nothing running is ok")
	}
}

func TestEvalReplication(t *testing.T) {
	d := func(ms int64) map[string]any { return map[string]any{"$date": map[string]any{"$numberLong": itoa(ms)}} }
	mk := func(secLagMs int64, health float64) map[string]any {
		return map[string]any{"members": []any{
			map[string]any{"name": "a", "stateStr": "PRIMARY", "health": float64(1), "optimeDate": d(1_000_000_000)},
			map[string]any{"name": "b", "stateStr": "SECONDARY", "health": health, "optimeDate": d(1_000_000_000 - secLagMs)},
		}}
	}
	if got := evalReplication(mk(2000, 1)).Status; got != OK {
		t.Errorf("2s lag: %s", got)
	}
	if got := evalReplication(mk(30_000, 1)).Status; got != Warn {
		t.Errorf("30s lag: %s", got)
	}
	if got := evalReplication(mk(120_000, 1)).Status; got != Bad {
		t.Errorf("120s lag: %s", got)
	}
	if got := evalReplication(mk(0, 0)).Status; got != Bad {
		t.Errorf("unhealthy: %s", got)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestReplicationStandaloneIsANote(t *testing.T) {
	f := &fakeMongo{err: map[string]error{"replSetGetStatus": errors.New("not running with --replSet")}}
	got := runReplication(context.Background(), f, &Env{})
	if got.Status != Note {
		t.Fatalf("%+v", got)
	}
}

func TestProfilerOffAndOn(t *testing.T) {
	off := &fakeMongo{by: map[string]map[string]any{"profile": {"was": float64(0)}}}
	if got := runProfiler(context.Background(), off, &Env{Database: "d", Now: fixedNow()}); got.Status != Note || !strings.Contains(got.Summary, "off") {
		t.Fatalf("off: %+v", got)
	}
	if got := runProfiler(context.Background(), off, &Env{}); got.Status != Note {
		t.Fatalf("no database: %+v", got)
	}
	docs := []map[string]any{
		{"op": "query", "ns": "d.Logs", "millis": float64(900), "planSummary": "COLLSCAN", "docsExamined": float64(10000), "nreturned": float64(10)},
		{"op": "query", "ns": "d.Logs", "millis": float64(300), "planSummary": "COLLSCAN", "docsExamined": float64(10000), "nreturned": float64(0)},
	}
	f := evalProfiler(docs)
	if f.Status != Warn || len(f.Rows) != 1 || f.Rows[0][2] != "2" {
		t.Fatalf("%+v", f)
	}
	if evalProfiler(nil).Status != OK {
		t.Error("none slow is ok")
	}
}

func TestEvalDBStatsDiskThresholds(t *testing.T) {
	mk := func(used float64) map[string]any {
		return map[string]any{"dataSize": float64(100), "indexSize": float64(10), "fsUsedSize": used, "fsTotalSize": float64(100)}
	}
	for used, want := range map[float64]Status{50: OK, 88: Warn, 97: Bad} {
		if got := evalDBStats(mk(used)).Status; got != want {
			t.Errorf("%v%%: got %s want %s", used, got, want)
		}
	}
}

func TestEvalTimingIndex(t *testing.T) {
	has := []map[string]any{{"key": map[string]any{"_id": float64(1)}}, {"key": map[string]any{"RequestInitiatedAt": float64(-1)}}}
	if evalTimingIndex(has).Status != OK {
		t.Error("index present is ok")
	}
	if evalTimingIndex(has[:1]).Status != Warn {
		t.Error("missing index warns")
	}
}
