package bbsql

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"
)

// extras_test.go pins the P1b seam: an extra is optional evidence. It is off by
// default, it can never degrade the Standard samples, and a bad registration
// stops the build's tests rather than reaching production.

// extraTestRunner builds a runner with one Standard sampler (waits) and the
// given extras. The extras ride on allowlisted statement ids of the Standard
// set, so no test registers anything global.
func extraTestRunner(t *testing.T, q *recQuery, now time.Time, extras ...Sampler) *Runner {
	t.Helper()
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	r.extras = extras
	r.extrasReady = true
	return r
}

func extraSampler(id, kind string) *fakeSampler {
	return &fakeSampler{id: id, kind: kind, interval: BaseInterval}
}

func TestExtrasDefaultSendsNothingExtra(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(2)}}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	if len(r.EnabledExtras) != 0 {
		t.Fatalf("EnabledExtras default = %v, want none", r.EnabledExtras)
	}
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(r.extras) != 0 {
		t.Fatalf("extras built with nothing enabled: %d", len(r.extras))
	}
	want := 3 // two ceiling statements plus waits
	if got := len(q.texts()); got != want {
		t.Errorf("statements sent = %d, want %d (ceiling x2 + waits)", got, want)
	}
}

func TestExtraRunsAfterStandardAndIsWritten(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:      fakeWaitRows(1),
		Statements["bb:semaphores"]: {{}},
	}}
	r := extraTestRunner(t, q, now, extraSampler("bb:semaphores", "xa"))
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	seen := q.texts()
	if len(seen) < 2 || seen[len(seen)-1] != Statements["bb:semaphores"] {
		t.Fatalf("the extra must be sent last, after Standard: %v", seen)
	}
	if got := sampleCount(t, r.Dir, "xa"); got != 1 {
		t.Errorf("extra samples written = %d, want 1", got)
	}
	if got := sampleCount(t, r.Dir, "waits"); got != 1 {
		t.Errorf("standard samples written = %d, want 1", got)
	}
}

func TestExtraFailureNeverDegradesStandard(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	q.errFor = func(text string) error {
		if text == Statements["bb:semaphores"] {
			return errors.New("boom")
		}
		return nil
	}
	r := extraTestRunner(t, q, now, extraSampler("bb:semaphores", "xa"))
	before := r.CurrentState()
	if err := r.Tick(now); err != nil {
		t.Fatalf("an extra failure must not fail the tick: %v", err)
	}
	after := r.CurrentState()
	if after != before {
		t.Errorf("State changed by an extra failure: %+v -> %+v", before, after)
	}
	if got := sampleCount(t, r.Dir, "waits"); got != 1 {
		t.Errorf("standard sample missing after an extra failure (%d)", got)
	}
	if got := sampleCount(t, r.Dir, "xa"); got != 0 {
		t.Errorf("failed extra wrote %d samples", got)
	}
	// Back-off: the very next tick must not hit the failing extra again.
	next := now.Add(BaseInterval)
	r.Now = func() time.Time { return next }
	if err := r.Tick(next); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if got := q.count(Statements["bb:semaphores"]); got != 1 {
		t.Errorf("failing extra sent %d times, want 1 (backing off)", got)
	}
}

func TestExtraPermissionDeniedSwitchesOnlyThatExtraOff(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:  fakeWaitRows(1),
		Statements["bb:tempdb"]: {{}},
	}}
	q.errFor = func(text string) error {
		if text == Statements["bb:semaphores"] {
			return errors.New("The SELECT permission was denied")
		}
		return nil
	}
	r := extraTestRunner(t, q, now,
		extraSampler("bb:semaphores", "xa"), extraSampler("bb:tempdb", "xb"))
	before := r.CurrentState()
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := r.ExtrasOff(); !reflect.DeepEqual(got, []string{"xa"}) {
		t.Fatalf("ExtrasOff = %v, want [xa]", got)
	}
	if r.CurrentState() != before {
		t.Errorf("a denied extra changed State: %+v", r.CurrentState())
	}
	if got := sampleCount(t, r.Dir, "xb"); got != 1 {
		t.Errorf("the healthy extra wrote %d samples, want 1", got)
	}
	next := now.Add(time.Hour)
	r.Now = func() time.Time { return next }
	if err := r.Tick(next); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if got := q.count(Statements["bb:semaphores"]); got != 1 {
		t.Errorf("a switched-off extra was sent again (%d)", got)
	}
}

func TestExtraNetworkErrorStaysWithTheExtra(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	q.errFor = func(text string) error {
		if text == Statements["bb:semaphores"] {
			return errors.New("dial tcp: connection refused")
		}
		return nil
	}
	r := extraTestRunner(t, q, now, extraSampler("bb:semaphores", "xa"))
	before := r.CurrentState()
	if err := r.Tick(now); err != nil {
		t.Fatalf("an extra's network-looking error must not fail the tick: %v", err)
	}
	if r.CurrentState() != before {
		t.Errorf("State changed by an extra's error text: %+v", r.CurrentState())
	}
	if r.extraRuns["xa"].fails != 1 {
		t.Errorf("extra fails = %d, want 1", r.extraRuns["xa"].fails)
	}
}

func TestExtraLoginFailureGoesThroughFail(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	q.errFor = func(text string) error {
		if text == Statements["bb:semaphores"] {
			return errors.New("Login failed for user")
		}
		return nil
	}
	r := extraTestRunner(t, q, now, extraSampler("bb:semaphores", "xa"))
	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeLoginFailed {
		t.Fatalf("tick error = %v, want the fixed login error", err)
	}
}

// TestExtraClampedTimeoutsEventuallyStrike: a slow-cadence extra always runs
// with less than its own limit, so its timeouts are forgiven only a few times in
// a row; after that it backs off like any failing extra and keeps its cadence.
func TestExtraClampedTimeoutsEventuallyStrike(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{}}
	r := extraTestRunner(t, q, now, extraSampler("bb:semaphores", "xa"))
	run := r.extraState("xa")
	s := r.extras[0]
	to := &Error{CodeTimeout, "The query did not finish within its time limit."}
	for i := 0; i < extraMaxClamps-1; i++ {
		if err := r.extraFailed(context.Background(), s, run, now, to, true); err != nil {
			t.Fatal(err)
		}
		if run.fails != 0 {
			t.Fatalf("clamped timeout %d struck early", i+1)
		}
		if got := r.lastRun["xa"]; !got.Equal(now) {
			t.Fatalf("a clamped timeout must keep the extra's own cadence (lastRun %v)", got)
		}
	}
	if err := r.extraFailed(context.Background(), s, run, now, to, true); err != nil {
		t.Fatal(err)
	}
	if run.fails != 1 || run.skipUntil.IsZero() {
		t.Errorf("after %d clamped timeouts the extra must strike and back off (fails %d)", extraMaxClamps, run.fails)
	}
}

// TestExtraSourcesArePinned: registering an extra approves its FROM source for
// the whole lint, so the set of sources extras read is pinned here. Adding one
// is a deliberate edit to this list, reviewed as a security change (a source
// can expose client addresses, names or text that no column rule catches).
func TestExtraSourcesArePinned(t *testing.T) {
	want := map[string]bool{
		"sys.configurations":                                       true,
		"sys.databases":                                            true,
		"sys.dm_exec_sessions":                                     true,
		"sys.dm_hadr_database_replica_states":                      true,
		"sys.dm_os_memory_clerks":                                  true,
		"sys.dm_os_performance_counters":                           true,
		"sys.dm_os_process_memory CROSS JOIN sys.dm_os_sys_memory": true,
		"sys.dm_os_sys_info":                                       true,
		"sys.dm_os_waiting_tasks":                                  true,
		"sys.dm_tran_active_transactions":                          true,
		"sys.dm_tran_locks":                                        true,
	}
	if !maps.Equal(extraSources, want) {
		t.Errorf("extraSources = %v, want exactly %v", extraSources, want)
	}
}

func TestExtraTenFailuresSwitchItOffNotTheService(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	q.errFor = func(text string) error {
		if text == Statements["bb:semaphores"] {
			return errors.New("boom")
		}
		return nil
	}
	r := extraTestRunner(t, q, now, extraSampler("bb:semaphores", "xa"))
	at := now
	for i := 0; i < extraMaxFailures+2; i++ {
		cur := at
		r.Now = func() time.Time { return cur }
		if err := r.Tick(cur); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		at = at.Add(2 * time.Hour) // past any back-off
	}
	if got := r.ExtrasOff(); !reflect.DeepEqual(got, []string{"xa"}) {
		t.Fatalf("ExtrasOff = %v, want [xa]", got)
	}
	if got := q.count(Statements["bb:semaphores"]); got != extraMaxFailures {
		t.Errorf("failing extra sent %d times, want exactly %d", got, extraMaxFailures)
	}
	if st := r.CurrentState(); st.AutoOff || st.Failures != 0 {
		t.Errorf("Standard State touched by an extra: %+v", st)
	}
}

func TestExtraClosedGateIsNotAnError(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]: fakeWaitRows(1),
		Statements["bb:hadr"]:  {{}},
	}}
	g := &fakeGatedSampler{
		fakeSampler: fakeSampler{id: "bb:semaphores", kind: "xg", interval: BaseInterval},
		gateID:      "bb:hadr", closed: true,
	}
	r := extraTestRunner(t, q, now, g)
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := q.count(Statements["bb:semaphores"]); got != 0 {
		t.Errorf("a closed-gate extra sent its statement %d times", got)
	}
	if len(r.ExtrasOff()) != 0 {
		t.Errorf("a closed gate switched the extra off")
	}
}

func TestExtraSkippedWhenTheTickBudgetIsSpent(t *testing.T) {
	if testing.Short() {
		t.Skip("burns the real 5 s tick budget")
	}
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:  fakeWaitRows(1),
		Statements["bb:tempdb"]: {{}},
	}}
	q.onQuery = func(text string) {
		if text == Statements["bb:semaphores"] {
			time.Sleep(tickBudget + 50*time.Millisecond)
		}
	}
	r := extraTestRunner(t, q, now,
		extraSampler("bb:semaphores", "xa"), extraSampler("bb:tempdb", "xb"))
	before := r.CurrentState()
	_ = r.Tick(now)
	if got := q.count(Statements["bb:tempdb"]); got != 0 {
		t.Errorf("an extra ran after the tick budget was spent (%d)", got)
	}
	if r.extraRuns["xb"] == nil || r.extraRuns["xb"].epoch == 0 {
		t.Errorf("a skipped extra must rebaseline (epoch bump)")
	}
	if r.extraRuns["xb"].fails != 0 {
		t.Errorf("a budget skip is not a strike")
	}
	if r.CurrentState() != before {
		t.Errorf("State changed: %+v", r.CurrentState())
	}
}

func TestExtraObserveSeesAdvancingEpochAfterAFailure(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]:      fakeWaitRows(1),
		Statements["bb:semaphores"]: {{}},
	}}
	fail := true
	q.errFor = func(text string) error {
		if text == Statements["bb:semaphores"] && fail {
			return errors.New("boom")
		}
		return nil
	}
	obs := &observingExtra{fakeSampler: *extraSampler("bb:semaphores", "xo")}
	r := extraTestRunner(t, q, now, obs)
	if err := r.Tick(now); err != nil {
		t.Fatal(err)
	}
	fail = false
	next := now.Add(time.Hour)
	r.Now = func() time.Time { return next }
	if err := r.Tick(next); err != nil {
		t.Fatal(err)
	}
	if len(obs.seen) != 1 {
		t.Fatalf("observations = %d, want 1", len(obs.seen))
	}
	if obs.seen[0].Epoch == r.gen {
		t.Errorf("epoch after a failure equals the base generation; the delta would span the gap")
	}
}

type observingExtra struct {
	fakeSampler
	seen []Observation
}

func (o *observingExtra) Observe(ob Observation) { o.seen = append(o.seen, ob) }

// ---- registry ---------------------------------------------------------------

// snapshotExtras restores every extras-related global when the test ends, so a
// test that registers something cannot leak it into the pinned Standard tests.
func snapshotExtras(t *testing.T) {
	t.Helper()
	extraMu.Lock()
	reg := append([]extraEntry(nil), extraRegistry...)
	extraMu.Unlock()
	stm := maps.Clone(statements)
	exs := maps.Clone(extraStatements)
	caps := maps.Clone(extraCaps)
	srcs := maps.Clone(extraSources)
	cols := maps.Clone(extraColumnKeys)
	from := maps.Clone(lintApprovedFrom)
	specs := maps.Clone(rowSpecs)
	t.Cleanup(func() {
		extraMu.Lock()
		extraRegistry = reg
		extraMu.Unlock()
		statements, extraStatements, extraCaps = stm, exs, caps
		extraSources, extraColumnKeys = srcs, cols
		lintApprovedFrom, rowSpecs = from, specs
	})
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: want a panic, got none", name)
		}
	}()
	f()
}

func TestRegisterExtraFactoryRejectsBadInput(t *testing.T) {
	snapshotExtras(t)
	mustPanic(t, "nil factory", func() { RegisterExtraFactory(TierEverything, nil) })
	mustPanic(t, "nil sampler", func() { RegisterExtraFactory(TierEverything, func() Sampler { return nil }) })
	mustPanic(t, "bad cadence", func() {
		RegisterExtraFactory(TierEverything, func() Sampler {
			return &fakeSampler{id: "bb:waits", kind: "xbad", interval: 7 * time.Second}
		})
	})
	mustPanic(t, "no tier", func() {
		RegisterExtraFactory(0, func() Sampler { return extraSampler("bb:waits", "xnotier") })
	})
	RegisterExtraFactory(TierEverything, func() Sampler { return extraSampler("bb:waits", "xdup") })
	mustPanic(t, "duplicate kind", func() {
		RegisterExtraFactory(TierDeep, func() Sampler { return extraSampler("bb:waits", "xdup") })
	})
}

const extraOKStmt = setPrefix + `SELECT TOP 6 counter_name, cntr_value
FROM sys.dm_os_performance_counters
ORDER BY counter_name /* unravis:bb:xtest */`

func TestRegisterExtraStatementContract(t *testing.T) {
	snapshotExtras(t)
	const src = "sys.dm_os_performance_counters"
	mustPanic(t, "id without bb:", func() { registerExtraStatement("xtest", extraOKStmt, 5, src) })
	mustPanic(t, "duplicate id", func() { registerExtraStatement("bb:waits", extraOKStmt, 5, src) })
	mustPanic(t, "no cap", func() { registerExtraStatement("bb:xtest", extraOKStmt, 0, src) })
	mustPanic(t, "TOP is not cap+1", func() { registerExtraStatement("bb:xtest", extraOKStmt, 9, src) })
	mustPanic(t, "no source", func() { registerExtraStatement("bb:xtest", extraOKStmt, 5, "") })
	noTop := strings.Replace(extraOKStmt, "TOP 6 ", "", 1)
	mustPanic(t, "no TOP, not single-row", func() { registerExtraStatement("bb:xtest", noTop, 5, src) })
	banned := strings.Replace(extraOKStmt, "counter_name, cntr_value", "counter_name, host_name", 1)
	mustPanic(t, "banned column", func() { registerExtraStatement("bb:xtest", banned, 5, src) })

	registerExtraStatement("bb:xtest", extraOKStmt, 5, src)
	if text, ok := Statement("bb:xtest"); !ok || text != extraOKStmt {
		t.Fatalf("a registered extra statement must be sendable")
	}
	if !IsAllowed(extraOKStmt) {
		t.Errorf("IsAllowed is false for a registered extra statement")
	}
	if _, ok := Statements["bb:xtest"]; ok {
		t.Errorf("extras must never appear in the exported Statements view")
	}
	if got := statementCap(extraOKStmt); got != 5 {
		t.Errorf("statementCap = %d, want 5", got)
	}
	mustPanic(t, "duplicate text", func() { registerExtraStatement("bb:xtest2", extraOKStmt, 5, src) })
}

func TestRegisterExtraColumnsRejectsConflicts(t *testing.T) {
	snapshotExtras(t)
	registerExtraColumns(map[string]string{"xcol_a": "xa"})
	registerExtraColumns(map[string]string{"xcol_a": "xa"}) // same mapping again is fine
	mustPanic(t, "mapped two ways", func() { registerExtraColumns(map[string]string{"xcol_a": "other"}) })
	for col, key := range columnKeys {
		mustPanic(t, "standard conflict", func() { registerExtraColumns(map[string]string{col: key + "_x"}) })
		break
	}
}

func TestExtrasForProfile(t *testing.T) {
	snapshotExtras(t)
	extraMu.Lock()
	extraRegistry = nil
	extraMu.Unlock()
	RegisterExtraFactory(TierEverything, func() Sampler { return extraSampler("bb:waits", "xe") })
	RegisterExtraFactory(TierDeep, func() Sampler { return extraSampler("bb:waits", "xd") })

	cases := []struct {
		profile string
		want    []string
	}{
		{ProfileStandard, nil},
		{"", nil},
		{"EVERYTHING ", nil}, // exact names only: a typo never widens collection
		{"nonsense", nil},
		{ProfileEverything, []string{"xe"}},
		{ProfileDeep, []string{"xd", "xe"}},
	}
	for _, c := range cases {
		if got := ExtrasForProfile(c.profile); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ExtrasForProfile(%q) = %v, want %v", c.profile, got, c.want)
		}
	}
}

func TestExtrasIgnoresUnknownAndStandardKinds(t *testing.T) {
	snapshotExtras(t)
	extraMu.Lock()
	extraRegistry = nil
	extraMu.Unlock()
	RegisterExtraFactory(TierEverything, func() Sampler { return extraSampler("bb:waits", "xe") })

	if got := Extras(nil); got != nil {
		t.Errorf("Extras(nil) = %v, want nil", got)
	}
	got := Extras([]string{"xe", "nope", "waits", "requests"})
	if len(got) != 1 || got[0].Kind() != "xe" {
		t.Fatalf("Extras returned %d samplers, want only xe", len(got))
	}
	a, b := Extras([]string{"xe"}), Extras([]string{"xe"})
	if a[0] == b[0] {
		t.Errorf("Extras must return fresh instances (a sampler holds delta state)")
	}
}

// TestNoExtraKindCollidesWithAStandardKind is the production-registry check:
// whatever the extras files register, a kind is owned by exactly one registry.
func TestNoExtraKindCollidesWithAStandardKind(t *testing.T) {
	std := map[string]bool{}
	for _, s := range Samplers() {
		std[s.Kind()] = true
	}
	for _, k := range ExtraKinds() {
		if std[k] {
			t.Errorf("extra kind %q collides with a Standard kind", k)
		}
	}
}

// TestProductionExtrasHonourTheStatementContract checks every REGISTERED extra
// against the same rules the Standard set meets: its statement exists, was
// registered as an extra, is linted, and its cadence is allowed.
func TestProductionExtrasHonourTheStatementContract(t *testing.T) {
	for _, k := range ExtraKinds() {
		ss := Extras([]string{k})
		if len(ss) != 1 {
			t.Fatalf("extra %q does not build", k)
		}
		s := ss[0]
		if !extraCadenceOK[s.Interval()] {
			t.Errorf("extra %q cadence %v is outside the allowed set", k, s.Interval())
		}
		text, ok := Statement(s.ID())
		if !ok {
			t.Errorf("extra %q uses non-allowlisted id %q", k, s.ID())
			continue
		}
		if _, ok := extraStatements[s.ID()]; !ok {
			t.Errorf("extra %q uses a Standard statement id %q", k, s.ID())
		}
		if err := lintStatement(s.ID(), text); err != nil {
			t.Errorf("extra %q statement fails the grammar: %v", k, err)
		}
	}
}

func TestExtraStatementsStayOutOfTheExportedView(t *testing.T) {
	for id := range extraStatements {
		if _, ok := Statements[id]; ok {
			t.Errorf("%s leaked into the exported Statements view", id)
		}
	}
}
