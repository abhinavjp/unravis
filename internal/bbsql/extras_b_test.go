package bbsql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// extras_b_test.go covers the batch B extras: bb:perfctr, bb:mem,
// bb:memclerks, bb:conns, bb:trans, bb:waiting, bb:agdetail, bb:locks,
// bb:facts, bb:factsdb and bb:factscfg. Rows are keyed by the SQL column names
// the statements SELECT (conns_total, pc_counter, ...), never by the JSON keys.
// Nothing here connects to anything; every row is a fake.

var xbBatch = []string{
	"perfctr", "mem", "memclerks", "conns", "trans", "waiting", "agdetail", "locks", "facts", "factsdb", "factscfg",
}

// xbRow builds a row from alternating column names and values.
func xbRow(kv ...any) Row {
	r := Row{}
	for i := 0; i < len(kv); i += 2 {
		r[kv[i].(string)] = kv[i+1]
	}
	return r
}

func xbMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func xbSampler(t *testing.T, kind string) Sampler {
	t.Helper()
	ss := Extras([]string{kind})
	if len(ss) != 1 {
		t.Fatalf("extra %q is not registered", kind)
	}
	return ss[0]
}

// xbObserve feeds a stateful sampler the continuity metadata the runner would.
func xbObserve(s Sampler, epoch uint64) {
	if o, ok := s.(ObservingSampler); ok {
		o.Observe(Observation{Time: time.Date(2026, 5, 1, 9, 0, int(epoch), 0, time.UTC), Epoch: epoch}) //nolint:gosec // tiny test epochs
	}
}

// ---------------------------------------------------------- registration

func TestBatchBRegisteredInTheEverythingProfileOnly(t *testing.T) {
	std := ExtrasForProfile(ProfileStandard)
	if len(std) != 0 {
		t.Fatalf("standard profile enables %v", std)
	}
	all := map[string]bool{}
	for _, k := range ExtrasForProfile(ProfileEverything) {
		all[k] = true
	}
	for _, k := range xbBatch {
		if !all[k] {
			t.Errorf("%s missing from the everything profile", k)
		}
		s := xbSampler(t, k)
		if s.Kind() != k || !strings.HasPrefix(s.ID(), "bb:") || s.ID() != "bb:"+k {
			t.Errorf("%s: ID/Kind = %q/%q", k, s.ID(), s.Kind())
		}
		if _, ok := extraStatements[s.ID()]; !ok {
			t.Errorf("%s: statement not registered as an extra", k)
		}
		if _, ok := Statements[s.ID()]; ok {
			t.Errorf("%s: statement leaked into the exported view", k)
		}
	}
}

func TestBatchBCadences(t *testing.T) {
	want := map[string]time.Duration{
		"perfctr": ExtraEvery30s, "mem": DemotedInterval, "memclerks": DemotedInterval, "conns": DemotedInterval,
		"trans": DemotedInterval, "waiting": BaseInterval, "agdetail": DemotedInterval, "locks": DemotedInterval,
		"facts": ExtraEveryDay, "factsdb": ExtraEveryDay, "factscfg": ExtraEveryDay,
	}
	for k, d := range want {
		if got := xbSampler(t, k).Interval(); got != d {
			t.Errorf("%s cadence = %v, want %v", k, got, d)
		}
	}
}

// TestBatchBRowCapMatchesTheStatement: TOP n+1 in SQL, cap n in Go, and a
// single-row statement has neither TOP nor GROUP BY.
func TestBatchBRowCapMatchesTheStatement(t *testing.T) {
	re := regexp.MustCompile(`(?i)\bTOP ([0-9]+)\b`)
	for _, k := range xbBatch {
		id := "bb:" + k
		text, _ := Statement(id)
		cap := statementCap(text)
		if m := re.FindStringSubmatch(text); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n != cap+1 {
				t.Errorf("%s: TOP %d but cap %d", id, n, cap)
			}
		} else if cap != CapSingleRow || strings.Contains(text, "GROUP BY") {
			t.Errorf("%s: no TOP but cap %d", id, cap)
		}
	}
}

// TestBatchBStatementsNeverSelectNames: the statements may not project a
// name, text or handle column. perfctr reads object_name, counter_name and
// instance_name only inside the trimming and classification expressions, which
// TestPerfctrOnlyProjectsFixedAliases pins.
func TestBatchBStatementsNeverSelectNames(t *testing.T) {
	banned := []string{"database_name", "login", "host", "program", "sql_text", "query_plan", "_handle", "physical_name",
		"resource_description", "transaction_name", "pool_name", "group_name", "client_net_address", "collation"}
	for _, k := range xbBatch {
		text, _ := Statement("bb:" + k)
		lower := strings.ToLower(text)
		for _, b := range banned {
			if strings.Contains(lower, b) {
				t.Errorf("bb:%s mentions %q", k, b)
			}
		}
	}
}

// ---------------------------------------------------------- gauges

func TestConnsTransDecode(t *testing.T) {
	conns := xbRow("conns_total", int64(40), "conns_user", int64(30), "conns_running", int64(3),
		"conns_sleeping", int64(25), "conns_in_tran", int64(2), "conns_idle_tran_sec", int64(95))
	got, trunc, err := xbSampler(t, "conns").Decode([]Row{conns})
	if err != nil || trunc {
		t.Fatalf("conns: %v trunc=%v", err, trunc)
	}
	if want := `{"connsTotal":40,"connsUser":30,"connsRunning":3,"connsSleeping":25,"connsInTran":2,"connsIdleTranSec":95}`; xbMarshal(t, got) != want {
		t.Errorf("conns payload = %s", xbMarshal(t, got))
	}
	tr := xbRow("trans_open", int64(5), "trans_oldest_sec", int64(1200), "trans_rolling_back", int64(1))
	got, _, err = xbSampler(t, "trans").Decode([]Row{tr})
	if err != nil {
		t.Fatalf("trans: %v", err)
	}
	if want := `{"transOpen":5,"transOldestSec":1200,"transRollingBack":1}`; xbMarshal(t, got) != want {
		t.Errorf("trans payload = %s", xbMarshal(t, got))
	}
	// An idle server is a reading of zeros, not a missing sample.
	zero := xbRow("trans_open", int64(0), "trans_oldest_sec", int64(0), "trans_rolling_back", int64(0))
	got, _, err = xbSampler(t, "trans").Decode([]Row{zero})
	if err != nil || got == nil {
		t.Errorf("zero trans = %v %v, want a payload", got, err)
	}
}

func TestSingleRowExtrasRejectBadReplies(t *testing.T) {
	good := map[string]Row{
		"conns": xbRow("conns_total", int64(1), "conns_user", int64(1), "conns_running", int64(1),
			"conns_sleeping", int64(0), "conns_in_tran", int64(0), "conns_idle_tran_sec", int64(0)),
		"trans": xbRow("trans_open", int64(1), "trans_oldest_sec", int64(1), "trans_rolling_back", int64(0)),
	}
	for kind, row := range good {
		s := xbSampler(t, kind)
		if _, _, err := s.Decode([]Row{row}); err != nil {
			t.Fatalf("%s good row: %v", kind, err)
		}
		for col := range row {
			miss := Row{}
			for c, v := range row {
				if c != col {
					miss[c] = v
				}
			}
			if _, _, err := s.Decode([]Row{miss}); err == nil {
				t.Errorf("%s: missing %s accepted", kind, col)
			}
			null := Row{}
			for c, v := range row {
				null[c] = v
			}
			null[col] = nil
			if _, _, err := s.Decode([]Row{null}); err == nil {
				t.Errorf("%s: NULL %s accepted", kind, col)
			}
		}
		extra := Row{"database_name": "secret"}
		for c, v := range row {
			extra[c] = v
		}
		if _, _, err := s.Decode([]Row{extra}); err == nil {
			t.Errorf("%s: an unknown column was accepted", kind)
		} else if strings.Contains(err.Error(), "database_name") || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: error leaks the column or value: %v", kind, err)
		}
	}
}

func TestNoRowsIsNoSample(t *testing.T) {
	for _, k := range xbBatch {
		s := xbSampler(t, k)
		xbObserve(s, 1)
		payload, _, err := s.Decode(nil)
		if err != nil || payload != nil {
			t.Errorf("%s: empty reply = %v, %v; want nil, nil", k, payload, err)
		}
	}
}

// ---------------------------------------------------------- waiting, locks, clerks

func TestWaitingDecode(t *testing.T) {
	rows := []Row{
		xbRow("wait_type", "LCK_M_X", "wt_tasks", int64(4), "wt_max_ms", int64(9000), "wt_blocked", int64(4), "wt_blockers", int64(1), "wt_max_block_ms", int64(9000)),
		xbRow("wait_type", "PAGEIOLATCH_SH", "wt_tasks", int64(2), "wt_max_ms", int64(15), "wt_blocked", int64(0), "wt_blockers", int64(0), "wt_max_block_ms", int64(0)),
	}
	got, trunc, err := xbSampler(t, "waiting").Decode(rows)
	if err != nil || trunc {
		t.Fatalf("waiting: %v %v", err, trunc)
	}
	want := `[{"waitType":"LCK_M_X","wtTasks":4,"wtMaxMs":9000,"wtBlocked":4,"wtBlockers":1,"wtMaxBlockMs":9000},` +
		`{"waitType":"PAGEIOLATCH_SH","wtTasks":2,"wtMaxMs":15,"wtBlocked":0,"wtBlockers":0,"wtMaxBlockMs":0}]`
	if xbMarshal(t, got) != want {
		t.Errorf("waiting = %s", xbMarshal(t, got))
	}
	bad := []Row{xbRow("wait_type", "lck m x; DROP", "wt_tasks", int64(1), "wt_max_ms", int64(1), "wt_blocked", int64(0), "wt_blockers", int64(0), "wt_max_block_ms", int64(0))}
	if _, _, err := xbSampler(t, "waiting").Decode(bad); err == nil || strings.Contains(err.Error(), "DROP") {
		t.Errorf("a malformed wait type must be refused with a fixed error, got %v", err)
	}
	over := make([]Row, CapWaiting+1)
	for i := range over {
		over[i] = rows[1]
	}
	got, trunc, err = xbSampler(t, "waiting").Decode(over)
	if err != nil || !trunc || len(got.([]Waiting)) != CapWaiting {
		t.Errorf("over cap: err=%v trunc=%v", err, trunc)
	}
}

func TestLocksDecode(t *testing.T) {
	row := func(typ, mode, status string, n int64) Row {
		return xbRow("lock_db", int64(5), "lock_type", typ, "lock_mode", mode, "lock_status", status, "lock_count", n)
	}
	got, _, err := xbSampler(t, "locks").Decode([]Row{row("KEY", "X", "GRANT", 120), row("OBJECT", "Sch-S", "WAIT", 3), row("KEY", "RangeS_U", "GRANT", 1)})
	if err != nil {
		t.Fatalf("locks: %v", err)
	}
	if want := `[{"lockDb":5,"lockType":"KEY","lockMode":"X","lockStatus":"GRANT","lockCount":120},` +
		`{"lockDb":5,"lockType":"OBJECT","lockMode":"Sch-S","lockStatus":"WAIT","lockCount":3},` +
		`{"lockDb":5,"lockType":"KEY","lockMode":"RangeS_U","lockStatus":"GRANT","lockCount":1}]`; xbMarshal(t, got) != want {
		t.Errorf("locks = %s", xbMarshal(t, got))
	}
	for _, bad := range []Row{row("key; drop", "X", "GRANT", 1), row("KEY", "X Y", "GRANT", 1), row("KEY", "X", "grant", 1), row("KEY", "", "GRANT", 1)} {
		if _, _, err := xbSampler(t, "locks").Decode([]Row{bad}); err == nil {
			t.Errorf("locks accepted %v", bad)
		}
	}
}

func TestMemClerksDecode(t *testing.T) {
	rows := []Row{
		xbRow("clerk_type", "MEMORYCLERK_SQLBUFFERPOOL", "clerk_pages_kb", int64(900000), "clerk_vm_kb", int64(1000)),
		xbRow("clerk_type", "CACHESTORE_SQLCP", "clerk_pages_kb", int64(5000), "clerk_vm_kb", int64(0)),
	}
	got, _, err := xbSampler(t, "memclerks").Decode(rows)
	if err != nil {
		t.Fatalf("memclerks: %v", err)
	}
	if want := `[{"clerkType":"MEMORYCLERK_SQLBUFFERPOOL","clerkPagesKb":900000,"clerkVmKb":1000},{"clerkType":"CACHESTORE_SQLCP","clerkPagesKb":5000,"clerkVmKb":0}]`; xbMarshal(t, got) != want {
		t.Errorf("memclerks = %s", xbMarshal(t, got))
	}
	bad := xbRow("clerk_type", "a b", "clerk_pages_kb", int64(1), "clerk_vm_kb", int64(1))
	if _, _, err := xbSampler(t, "memclerks").Decode([]Row{bad}); err == nil {
		t.Errorf("a clerk type with a space was accepted")
	}
	over := make([]Row, CapMemClerks+1)
	for i := range over {
		over[i] = rows[0]
	}
	if got, trunc, err := xbSampler(t, "memclerks").Decode(over); err != nil || !trunc || len(got.([]MemClerk)) != CapMemClerks {
		t.Errorf("over cap: err=%v trunc=%v", err, trunc)
	}
}

// ---------------------------------------------------------- mem

func xbMemRow(faults int64) Row {
	return xbRow("mem_proc_kb", int64(8000000), "mem_util_pct", int64(97), "mem_locked_kb", int64(0), "mem_large_kb", int64(0),
		"mem_commit_avail_kb", int64(4000000), "mem_faults_cum", faults, "mem_proc_low", int64(0), "mem_virt_low", int64(0),
		"mem_sys_total_kb", int64(16000000), "mem_sys_avail_kb", int64(2000000), "mem_pf_total_kb", int64(20000000),
		"mem_pf_avail_kb", int64(9000000), "mem_cache_kb", int64(300000), "mem_paged_kb", int64(100000),
		"mem_nonpaged_kb", int64(80000), "mem_sys_high", int64(1), "mem_sys_low", int64(0))
}

func TestMemFaultDelta(t *testing.T) {
	s := xbSampler(t, "mem")
	faults := func(epoch uint64, cum int64) *uint64 {
		xbObserve(s, epoch)
		p, _, err := s.Decode([]Row{xbMemRow(cum)})
		if err != nil {
			t.Fatalf("mem: %v", err)
		}
		return p.(Mem).PageFaults
	}
	if faults(1, 1000) != nil {
		t.Errorf("first reading must not carry a delta")
	}
	if p := faults(1, 1500); p == nil || *p != 500 {
		t.Errorf("delta = %v, want 500", p)
	}
	if p := faults(1, 1500); p == nil || *p != 0 {
		t.Errorf("a measured zero must stay present, got %v", p)
	}
	if faults(1, 10) != nil {
		t.Errorf("a counter that went backwards (restart) must not produce a delta")
	}
	if p := faults(1, 40); p == nil || *p != 30 {
		t.Errorf("delta after a reset = %v, want 30", p)
	}
	if faults(2, 100) != nil {
		t.Errorf("a new epoch (a missed interval) must rebaseline")
	}
}

func TestMemRejectsOutOfDomain(t *testing.T) {
	s := xbSampler(t, "mem")
	for col, v := range map[string]int64{"mem_util_pct": 101, "mem_proc_low": 2, "mem_sys_high": 5} {
		r := xbMemRow(1)
		r[col] = v
		if _, _, err := s.Decode([]Row{r}); err == nil {
			t.Errorf("%s=%d accepted", col, v)
		}
	}
	r := xbMemRow(1)
	delete(r, "mem_cache_kb")
	if _, _, err := s.Decode([]Row{r}); err == nil {
		t.Errorf("a missing column was accepted")
	}
}

// ---------------------------------------------------------- agdetail

func xbAGRow(db, local int64, extra ...any) Row {
	r := xbRow("database_id", db, "ag_local", local, "ag_health", int64(2), "ag_suspended", int64(0),
		"ag_send_q_kb", nil, "ag_send_rate_kbs", nil, "ag_redo_q_kb", nil, "ag_redo_rate_kbs", nil)
	for i := 0; i < len(extra); i += 2 {
		r[extra[i].(string)] = extra[i+1]
	}
	return r
}

func TestAGDetailNullsAreAbsentNotZero(t *testing.T) {
	got, _, err := xbSampler(t, "agdetail").Decode([]Row{
		xbAGRow(5, 1),
		xbAGRow(5, 0, "ag_send_q_kb", int64(2048), "ag_send_rate_kbs", int64(900), "ag_redo_q_kb", int64(0), "ag_redo_rate_kbs", int64(1200)),
	})
	if err != nil {
		t.Fatalf("agdetail: %v", err)
	}
	want := `[{"databaseId":5,"agLocal":1,"agHealth":2,"agSuspended":0},` +
		`{"databaseId":5,"agLocal":0,"agHealth":2,"agSuspended":0,"agSendQKb":2048,"agSendRateKbs":900,"agRedoQKb":0,"agRedoRateKbs":1200}]`
	if xbMarshal(t, got) != want {
		t.Errorf("agdetail = %s", xbMarshal(t, got))
	}
	for _, r := range []Row{xbAGRow(5, 2), xbAGRow(5, 1, "ag_health", int64(3)), xbAGRow(5, 1, "ag_suspended", int64(2))} {
		if _, _, err := xbSampler(t, "agdetail").Decode([]Row{r}); err == nil {
			t.Errorf("out of domain row accepted: %v", r)
		}
	}
	r := xbAGRow(5, 1)
	delete(r, "ag_redo_q_kb")
	if _, _, err := xbSampler(t, "agdetail").Decode([]Row{r}); err == nil {
		t.Errorf("a missing nullable column must still be required to be present")
	}
}

func TestAGDetailIsGatedLikeAG(t *testing.T) {
	g, ok := xbSampler(t, "agdetail").(Gate)
	if !ok || g.GateID() != "bb:hadr" {
		t.Fatalf("agdetail gate = %v %v, want bb:hadr", ok, g)
	}
	if !g.GateOpen([]Row{{"hadr": int64(1)}}) {
		t.Errorf("hadr=1 must open the gate")
	}
	for _, rows := range [][]Row{nil, {{"hadr": int64(0)}}, {{"hadr": nil}}, {{}}, {{"hadr": int64(1)}, {"hadr": int64(1)}}} {
		if g.GateOpen(rows) {
			t.Errorf("gate opened for %v", rows)
		}
	}
}

// ---------------------------------------------------------- facts

func TestFactsDecode(t *testing.T) {
	row := xbRow("f_version", "15.0.4123.1", "f_engine_edition", int64(3), "f_clustered", int64(0), "f_hadr", int64(1),
		"f_cpus", int64(16), "f_ht_ratio", int64(2), "f_schedulers", int64(16), "f_max_workers", int64(960),
		"f_phys_kb", int64(67108864), "f_target_kb", int64(50331648), "f_vm_type", int64(1), "f_uptime_sec", int64(86400))
	got, _, err := xbSampler(t, "facts").Decode([]Row{row})
	if err != nil {
		t.Fatalf("facts: %v", err)
	}
	if want := `{"fVersion":"15.0.4123.1","fEngineEdition":3,"fClustered":0,"fHadr":1,"fCpus":16,"fHtRatio":2,"fSchedulers":16,"fMaxWorkers":960,"fPhysKb":67108864,"fTargetKb":50331648,"fVmType":1,"fUptimeSec":86400}`; xbMarshal(t, got) != want {
		t.Errorf("facts = %s", xbMarshal(t, got))
	}
	// The version must be digits and dots: a name or any other text is refused.
	for _, v := range []string{"MYSERVER", "15.0.x", "", "15", "15.0.4123.1.1", "15.0\n"} {
		r := Row{}
		for c, x := range row {
			r[c] = x
		}
		r["f_version"] = v
		if _, _, err := xbSampler(t, "facts").Decode([]Row{r}); err == nil {
			t.Errorf("version %q accepted", v)
		}
	}
	// ProductVersion arrives from a sql_variant cast as bytes on some drivers.
	r := Row{}
	for c, x := range row {
		r[c] = x
	}
	r["f_version"] = []byte("16.0.1000.6")
	if _, _, err := xbSampler(t, "facts").Decode([]Row{r}); err != nil {
		t.Errorf("a byte version was refused: %v", err)
	}
}

func TestFactsDBAndCfgDecode(t *testing.T) {
	db := xbRow("database_id", int64(7), "fdb_compat", int64(150), "fdb_recovery", int64(1), "fdb_state", int64(0), "fdb_access", int64(0),
		"fdb_page_verify", int64(2), "fdb_snapshot", int64(1), "fdb_read_only", int64(0), "fdb_auto_close", int64(0), "fdb_auto_shrink", int64(1),
		"fdb_auto_create_stats", int64(1), "fdb_auto_update_stats", int64(1), "fdb_rcsi", int64(1), "fdb_query_store", int64(1), "fdb_encrypted", int64(0))
	got, _, err := xbSampler(t, "factsdb").Decode([]Row{db})
	if err != nil {
		t.Fatalf("factsdb: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(xbMarshal(t, got.([]FactsDB)[0])), &m); err != nil || len(m) != 15 || m["databaseId"] != float64(7) {
		t.Errorf("factsdb payload = %v (%v)", m, err)
	}
	db["fdb_auto_shrink"] = int64(2)
	if _, _, err := xbSampler(t, "factsdb").Decode([]Row{db}); err == nil {
		t.Errorf("a flag of 2 was accepted")
	}
	cfg := []Row{xbRow("cfg_id", int64(1544), "cfg_value", int64(2147483647)), xbRow("cfg_id", int64(1539), "cfg_value", int64(0))}
	got, _, err = xbSampler(t, "factscfg").Decode(cfg)
	if err != nil {
		t.Fatalf("factscfg: %v", err)
	}
	if want := `[{"cfgId":1544,"cfgValue":2147483647},{"cfgId":1539,"cfgValue":0}]`; xbMarshal(t, got) != want {
		t.Errorf("factscfg = %s", xbMarshal(t, got))
	}
	over := make([]Row, CapFactsCfg+1)
	for i := range over {
		over[i] = cfg[1]
	}
	if got, trunc, err := xbSampler(t, "factscfg").Decode(over); err != nil || !trunc || len(got.([]FactsCfg)) != CapFactsCfg {
		t.Errorf("over cap: err=%v trunc=%v", err, trunc)
	}
}

// ---------------------------------------------------------- perfctr

// TestPerfctrStatementMatchesTable: every name in the table is in the SQL IN
// list and every name in the IN list is in the table, so the send policy and
// the keep policy cannot drift apart.
func TestPerfctrStatementMatchesTable(t *testing.T) {
	_, list, ok := strings.Cut(stmtPerfctr, "WHERE counter_name IN (")
	if !ok {
		t.Fatal("no IN list")
	}
	list, _, _ = strings.Cut(list, ")\nORDER BY")
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`N'([^']+)'`).FindAllStringSubmatch(list, -1) {
		names[m[1]] = true
	}
	tableNames := map[string]bool{}
	keys := map[string]bool{}
	for _, d := range perfDefs {
		tableNames[d.counter] = true
		if d.kind != pcBase {
			if d.key == "" || keys[d.key] {
				t.Errorf("counter %q has an empty or duplicate key %q", d.counter, d.key)
			}
			keys[d.key] = true
		}
		if d.kind == pcRatio {
			if _, ok := perfIndex[strings.ToLower(d.object+"\x00"+d.base)]; !ok {
				t.Errorf("ratio %q has no base counter in the table", d.counter)
			}
		}
	}
	for n := range names {
		if !tableNames[n] {
			t.Errorf("SQL asks for %q, which the table does not keep", n)
		}
	}
	for n := range tableNames {
		if !names[n] {
			t.Errorf("table keeps %q but the SQL never asks for it", n)
		}
	}
	// A counter name can occur in more than one object; the lookup is
	// (object, counter), so each pair must be unique.
	seen := map[string]bool{}
	for _, d := range perfDefs {
		k := strings.ToLower(d.object + "\x00" + d.counter)
		if seen[k] {
			t.Errorf("duplicate counter %s/%s", d.object, d.counter)
		}
		seen[k] = true
	}
}

// TestPerfctrOnlyProjectsFixedAliases: the select list is the six fixed aliases
// and the three name columns appear only wrapped (trimmed or classified).
func TestPerfctrOnlyProjectsFixedAliases(t *testing.T) {
	body, _, _ := strings.Cut(stmtPerfctr, "FROM sys.dm_os_performance_counters")
	var aliases []string
	for _, m := range regexp.MustCompile(`AS (pc_[a-z]+)`).FindAllStringSubmatch(body, -1) {
		aliases = append(aliases, m[1])
	}
	want := []string{"pc_object", "pc_counter", "pc_inst", "pc_db", "pc_value", "pc_type"}
	if !reflect.DeepEqual(aliases, want) {
		t.Errorf("aliases = %v, want %v", aliases, want)
	}
	for _, name := range []string{"instance_name", "object_name", "counter_name"} {
		for _, m := range regexp.MustCompile(`.{0,6}`+name).FindAllString(body, -1) {
			if !strings.Contains(m, "RTRIM(") {
				t.Errorf("%s appears bare in the select list: %q", name, m)
			}
		}
	}
}

func xbPC(object, counter, inst string, db any, value, typ int64) Row {
	return xbRow("pc_object", object, "pc_counter", counter, "pc_inst", inst, "pc_db", db, "pc_value", value, "pc_type", typ)
}

func xbPerf(t *testing.T, s Sampler, epoch uint64, rows []Row) map[string]int64 {
	t.Helper()
	xbObserve(s, epoch)
	p, _, err := s.Decode(rows)
	if err != nil {
		t.Fatalf("perfctr: %v", err)
	}
	out := map[string]int64{}
	if p == nil {
		return out
	}
	for _, c := range p.([]PerfCounter) {
		k := c.K
		if c.DB != nil {
			k += "#" + strconv.Itoa(int(*c.DB))
		}
		if _, dup := out[k]; dup {
			t.Fatalf("duplicate key %s", k)
		}
		out[k] = c.V
	}
	return out
}

func TestPerfctrGaugeRateAndRatio(t *testing.T) {
	s := xbSampler(t, "perfctr")
	rows := func(batches, ple, hit, base int64) []Row {
		return []Row{
			xbPC("SQL Statistics", "Batch Requests/sec", "", nil, batches, pcTypeRate),
			xbPC("Buffer Manager", "Page life expectancy", "", nil, ple, pcTypeValue),
			xbPC("Buffer Manager", "Buffer cache hit ratio", "", nil, hit, pcTypeRatio),
			xbPC("Buffer Manager", "Buffer cache hit ratio base", "", nil, base, pcTypeBase),
		}
	}
	got := xbPerf(t, s, 1, rows(1000, 300, 99, 100))
	if !reflect.DeepEqual(got, map[string]int64{"bm.ple": 300, "bm.hit": 9900}) {
		t.Errorf("first reading = %v (rate must wait for a baseline, base is never written)", got)
	}
	got = xbPerf(t, s, 1, rows(1750, 310, 1, 4))
	if !reflect.DeepEqual(got, map[string]int64{"ss.batches": 750, "bm.ple": 310, "bm.hit": 2500}) {
		t.Errorf("second reading = %v", got)
	}
	// A restart: the counter goes backwards, the rate is dropped, gauges stay.
	got = xbPerf(t, s, 1, rows(20, 5, 0, 0))
	if !reflect.DeepEqual(got, map[string]int64{"bm.ple": 5}) {
		t.Errorf("after a reset = %v", got)
	}
	// A new epoch (a missed interval) rebaselines.
	got = xbPerf(t, s, 2, rows(90, 6, 5, 5))
	if _, ok := got["ss.batches"]; ok {
		t.Errorf("a rate was written across an epoch change: %v", got)
	}
	if got["bm.hit"] != 10000 {
		t.Errorf("ratio = %d, want 10000", got["bm.hit"])
	}
}

func TestPerfctrInstanceRules(t *testing.T) {
	s := xbSampler(t, "perfctr")
	rows := []Row{
		// instDB: _Total and per-database id rows are kept.
		xbPC("Databases", "Active Transactions", "_Total", nil, 9, pcTypeValue),
		xbPC("Databases", "Active Transactions", "db", int64(5), 4, pcTypeValue),
		xbPC("Databases", "Active Transactions", "db", int64(2), 3, pcTypeValue),
		// A database row whose name did not resolve to an id is dropped, not guessed.
		xbPC("Databases", "Active Transactions", "db", nil, 99, pcTypeValue),
		xbPC("Databases", "Active Transactions", "db", int64(0), 98, pcTypeValue),
		xbPC("Databases", "Active Transactions", "db", int64(1<<40), 97, pcTypeValue),
		// instTotal: only _Total (lock types and per-type rows are dropped).
		xbPC("Locks", "Lock Waits/sec", "_Total", nil, 1, pcTypeRate),
		xbPC("Locks", "Lock Waits/sec", "db", nil, 1, pcTypeRate),
		// instNone: a per-instance row of a server counter is dropped (Buffer Node).
		xbPC("Buffer Node", "Page life expectancy", "db", nil, 7, pcTypeValue),
		xbPC("Buffer Manager", "Page life expectancy", "db", nil, 8, pcTypeValue),
		xbPC("Buffer Manager", "Page life expectancy", "", nil, 123, pcTypeValue),
		// Unknown counters and a wrong type are dropped.
		xbPC("Plan Cache", "Cache Pages", "", nil, 1, pcTypeValue),
		xbPC("Memory Manager", "Total Server Memory (KB)", "", nil, 5, pcTypeRate),
	}
	got := xbPerf(t, s, 1, rows)
	want := map[string]int64{"db.active": 9, "db.active#5": 4, "db.active#2": 3, "bm.ple": 123}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kept = %v, want %v", got, want)
	}
}

func TestPerfctrPerDatabaseRateKeepsEachDatabaseSeparate(t *testing.T) {
	s := xbSampler(t, "perfctr")
	mk := func(a, b int64) []Row {
		return []Row{
			xbPC("Databases", "Log Growths", "db", int64(5), a, pcTypeValue),
			xbPC("Databases", "Log Growths", "db", int64(6), b, pcTypeValue),
		}
	}
	xbPerf(t, s, 1, mk(10, 100))
	got := xbPerf(t, s, 1, mk(12, 100))
	if !reflect.DeepEqual(got, map[string]int64{"db.loggrow#5": 2, "db.loggrow#6": 0}) {
		t.Errorf("deltas = %v", got)
	}
	// Database 6 disappears and comes back: no delta across the gap.
	xbPerf(t, s, 1, mk(13, 100)[:1])
	got = xbPerf(t, s, 1, mk(14, 105))
	if got["db.loggrow#5"] != 1 {
		t.Errorf("db 5 delta = %d, want 1", got["db.loggrow#5"])
	}
	if _, ok := got["db.loggrow#6"]; ok {
		t.Errorf("db 6 has a delta across a gap: %v", got)
	}
}

func TestPerfctrOutputHasOnlyFixedKeysAndNumbers(t *testing.T) {
	s := xbSampler(t, "perfctr")
	rows := []Row{
		xbPC("Databases", "Percent Log Used", "db", int64(5), 17, pcTypeValue),
		xbPC("Databases", "Percent Log Used", "_Total", nil, 18, pcTypeValue),
		xbPC("Buffer Manager", "Page life expectancy", "", nil, 5000, pcTypeValue),
	}
	xbObserve(s, 1)
	p, _, err := s.Decode(rows)
	if err != nil {
		t.Fatal(err)
	}
	var generic []map[string]any
	if err := json.Unmarshal([]byte(xbMarshal(t, p)), &generic); err != nil {
		t.Fatal(err)
	}
	valid := map[string]bool{}
	for _, d := range perfDefs {
		valid[d.key] = true
	}
	for _, g := range generic {
		for k, v := range g {
			switch k {
			case "k":
				if !valid[v.(string)] {
					t.Errorf("key %v is not in the table", v)
				}
			case "v", "db":
				if _, ok := v.(float64); !ok {
					t.Errorf("%s is not a number: %v", k, v)
				}
			default:
				t.Errorf("unexpected JSON key %q", k)
			}
		}
	}
	if got := xbMarshal(t, p); got != `[{"k":"bm.ple","v":5000},{"k":"db.logpct","v":18},{"k":"db.logpct","v":17,"db":5}]` {
		t.Errorf("payload order or shape = %s", got)
	}
}

func TestPerfctrRowContract(t *testing.T) {
	s := xbSampler(t, "perfctr")
	ok := xbPC("Buffer Manager", "Page life expectancy", "", nil, 1, pcTypeValue)
	if _, _, err := s.Decode([]Row{ok}); err != nil {
		t.Fatalf("good row: %v", err)
	}
	for col := range ok {
		if col == "pc_db" {
			continue // nullable: DB_ID is NULL for a non-database instance
		}
		r := Row{}
		for c, v := range ok {
			if c != col {
				r[c] = v
			}
		}
		if _, _, err := s.Decode([]Row{r}); err == nil {
			t.Errorf("missing %s accepted", col)
		}
		r2 := Row{}
		for c, v := range ok {
			r2[c] = v
		}
		r2[col] = nil
		if _, _, err := s.Decode([]Row{r2}); err == nil {
			t.Errorf("NULL %s accepted", col)
		}
	}
	// pc_db must be present (even if NULL).
	r := Row{}
	for c, v := range ok {
		if c != "pc_db" {
			r[c] = v
		}
	}
	if _, _, err := s.Decode([]Row{r}); err == nil {
		t.Errorf("a row without pc_db was accepted")
	}
	// An unknown column is refused with a fixed error.
	r = Row{"instance_name": "mydb"}
	for c, v := range ok {
		r[c] = v
	}
	if _, _, err := s.Decode([]Row{r}); err == nil || strings.Contains(err.Error(), "mydb") {
		t.Errorf("unknown column: %v", err)
	}
	// Over the cap: truncated, and the extra row is not read.
	over := make([]Row, CapPerfctr+1)
	for i := range over {
		over[i] = ok
	}
	if _, trunc, err := s.Decode(over); err != nil || !trunc {
		t.Errorf("over cap: err=%v trunc=%v", err, trunc)
	}
}

// ---------------------------------------------------------- through the runner

// TestBatchBRunsThroughTheRunner sends every batch B statement through the real
// tick path with fake replies and checks each kind is written once, the gate of
// agdetail is honoured, and a failing extra leaves the others alone.
func TestBatchBRunsThroughTheRunner(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	replies := map[string][]Row{
		Statements["bb:waits"]: fakeWaitRows(1),
		stmtOf("bb:perfctr"):   {xbPC("Buffer Manager", "Page life expectancy", "", nil, 300, pcTypeValue)},
		stmtOf("bb:mem"):       {xbMemRow(10)},
		stmtOf("bb:memclerks"): {xbRow("clerk_type", "MEMORYCLERK_SQLBUFFERPOOL", "clerk_pages_kb", int64(1), "clerk_vm_kb", int64(1))},
		stmtOf("bb:conns"): {xbRow("conns_total", int64(1), "conns_user", int64(1), "conns_running", int64(1),
			"conns_sleeping", int64(0), "conns_in_tran", int64(0), "conns_idle_tran_sec", int64(0))},
		stmtOf("bb:trans"): {xbRow("trans_open", int64(0), "trans_oldest_sec", int64(0), "trans_rolling_back", int64(0))},
		stmtOf("bb:waiting"): {xbRow("wait_type", "LCK_M_S", "wt_tasks", int64(1), "wt_max_ms", int64(1), "wt_blocked", int64(1),
			"wt_blockers", int64(1), "wt_max_block_ms", int64(1))},
		stmtOf("bb:hadr"):     {{"hadr": int64(1)}},
		stmtOf("bb:agdetail"): {xbAGRow(5, 1)},
		stmtOf("bb:locks"):    {xbRow("lock_db", int64(5), "lock_type", "KEY", "lock_mode", "X", "lock_status", "GRANT", "lock_count", int64(2))},
		stmtOf("bb:facts"): {xbRow("f_version", "15.0.1.1", "f_engine_edition", int64(2), "f_clustered", int64(0), "f_hadr", int64(0),
			"f_cpus", int64(4), "f_ht_ratio", int64(1), "f_schedulers", int64(4), "f_max_workers", int64(512),
			"f_phys_kb", int64(1), "f_target_kb", int64(1), "f_vm_type", int64(0), "f_uptime_sec", int64(1))},
		stmtOf("bb:factsdb"):  {xbRow("database_id", int64(1), "fdb_compat", int64(150), "fdb_recovery", int64(3), "fdb_state", int64(0), "fdb_access", int64(0), "fdb_page_verify", int64(2), "fdb_snapshot", int64(0), "fdb_read_only", int64(0), "fdb_auto_close", int64(0), "fdb_auto_shrink", int64(0), "fdb_auto_create_stats", int64(1), "fdb_auto_update_stats", int64(1), "fdb_rcsi", int64(0), "fdb_query_store", int64(0), "fdb_encrypted", int64(0))},
		stmtOf("bb:factscfg"): {xbRow("cfg_id", int64(1544), "cfg_value", int64(1000))},
	}
	q := &recQuery{replies: replies}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	r.EnabledExtras = xbBatch
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, k := range xbBatch {
		if got := sampleCount(t, r.Dir, k); got != 1 {
			t.Errorf("%s samples written = %d, want 1", k, got)
		}
	}
	if got := q.count(stmtOf("bb:hadr")); got != 1 {
		t.Errorf("hadr gate sent %d times, want 1", got)
	}
	if off := r.ExtrasOff(); len(off) != 0 {
		t.Errorf("extras switched off: %v", off)
	}
}

func stmtOf(id string) string {
	s, ok := Statement(id)
	if !ok {
		panic("no statement " + id)
	}
	return s
}

func TestBatchBClosedHadrGateSkipsAGDetailOnly(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]: fakeWaitRows(1),
		stmtOf("bb:hadr"):      {{"hadr": int64(0)}},
		stmtOf("bb:trans"):     {xbRow("trans_open", int64(0), "trans_oldest_sec", int64(0), "trans_rolling_back", int64(0))},
	}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	r.EnabledExtras = []string{"agdetail", "trans"}
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := q.count(stmtOf("bb:agdetail")); got != 0 {
		t.Errorf("agdetail sent %d times on an instance without availability groups", got)
	}
	if sampleCount(t, r.Dir, "trans") != 1 || sampleCount(t, r.Dir, "agdetail") != 0 {
		t.Errorf("trans/agdetail samples = %d/%d, want 1/0", sampleCount(t, r.Dir, "trans"), sampleCount(t, r.Dir, "agdetail"))
	}
}

func TestBatchBExtrasAreNeverSentUnderStandard(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
	r.EnabledExtras = ExtrasForProfile(ProfileStandard)
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	sent := q.texts()
	sort.Strings(sent)
	for _, k := range xbBatch {
		if q.count(stmtOf("bb:"+k)) != 0 {
			t.Errorf("bb:%s sent under the standard profile", k)
		}
	}
	_ = fmt.Sprint(sent)
}
