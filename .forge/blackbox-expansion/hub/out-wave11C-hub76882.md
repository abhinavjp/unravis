internal/bbsql/sched.go
```go
package bbsql

import "time"

// schedulersSampler reports the summed visible-online scheduler counters.
// Not safe for concurrent use; the runner serialises Decode calls.
type schedulersSampler struct{}

func (s *schedulersSampler) ID() string   { return "bb:schedulers" }
func (s *schedulersSampler) Kind() string { return "schedulers" }
func (s *schedulersSampler) Interval() time.Duration {
	return BaseInterval
}

// Decode returns one Schedulers value rather than a slice because the statement
// already sums every VISIBLE ONLINE scheduler into a single row; an empty result
// stays nil so the field is omitted instead of serialising zeros.
func (s *schedulersSampler) Decode(rows []Row) (any, bool, error) {
	if len(rows) == 0 {
		return nil, false, nil
	}
	var out []Schedulers
	truncated, err := decodeRows(rows, &out, CapSchedulers)
	if err != nil {
		return nil, truncated, err
	}
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out[0], truncated, nil
}

// semaphoreCumulative is the previous reading for one resource semaphore id.
type semaphoreCumulative struct {
	grantees, waiters, forced, timeouts int64
}

// semaphoresSampler reports per-resource semaphore pressure. Grantees and
// waiters are instantaneous counts and are emitted as read; forced and timeouts
// are cumulative counters and are emitted as Go-computed deltas. The first cycle
// only establishes baselines and emits nil. A cumulative counter that goes
// backwards (SQL restart or rollover) re-baselines that id and skips it for the
// cycle. prev is rebuilt from the current cycle's rows, so it stays bounded by
// CapSemaphores and ids not seen this cycle are dropped. Deltas of zero are
// still emitted: a steady grant is evidence.
// Not safe for concurrent use; the runner serialises Decode calls.
type semaphoresSampler struct {
	prev map[int32]semaphoreCumulative
}

func (s *semaphoresSampler) ID() string   { return "bb:semaphores" }
func (s *semaphoresSampler) Kind() string { return "semaphores" }
func (s *semaphoresSampler) Interval() time.Duration {
	return BaseInterval
}

func (s *semaphoresSampler) Decode(rows []Row) (any, bool, error) {
	if len(rows) == 0 {
		// Nothing was seen this cycle, so no old baseline survives it.
		s.prev = nil
		return nil, false, nil
	}

	var current []Semaphores
	truncated, err := decodeRows(rows, &current, CapSemaphores)
	if err != nil {
		return nil, truncated, err
	}

	next := make(map[int32]semaphoreCumulative, len(current))
	payload := make([]Semaphores, 0, len(current))
	for _, row := range current {
		id := row.ResourceSemaphoreID
		reading := semaphoreCumulative{
			grantees: int64(row.Grantees),
			waiters:  int64(row.Waiters),
			forced:   int64(row.Forced),
			timeouts: int64(row.Timeouts),
		}
		prev, seen := s.prev[id]
		if seen {
			if reading.forced < prev.forced || reading.timeouts < prev.timeouts {
				// Counter went backwards: re-baseline and skip this cycle.
				next[id] = reading
				continue
			}
			payload = append(payload, Semaphores{
				ResourceSemaphoreID: id,
				Grantees:            row.Grantees,
				Waiters:             row.Waiters,
				Forced:              int32(reading.forced - prev.forced),
				Timeouts:            int32(reading.timeouts - prev.timeouts),
			})
		}
		next[id] = reading
	}
	s.prev = next

	if len(payload) == 0 {
		return nil, truncated, nil
	}
	return payload, truncated, nil
}

// tempdbSampler reports tempdb page totals.
// Not safe for concurrent use; the runner serialises Decode calls.
type tempdbSampler struct{}

func (s *tempdbSampler) ID() string   { return "bb:tempdb" }
func (s *tempdbSampler) Kind() string { return "tempdb" }
func (s *tempdbSampler) Interval() time.Duration {
	return DemotedInterval
}

// Decode returns one Tempdb value rather than a slice because the statement
// sums tempdb pages into a single row; an empty result stays nil.
func (s *tempdbSampler) Decode(rows []Row) (any, bool, error) {
	if len(rows) == 0 {
		return nil, false, nil
	}
	var out []Tempdb
	truncated, err := decodeRows(rows, &out, CapTempdb)
	if err != nil {
		return nil, truncated, err
	}
	if len(out) == 0 {
		return nil, truncated, nil
	}
	return out[0], truncated, nil
}

func init() {
	RegisterSampler(&schedulersSampler{})
	RegisterSampler(&semaphoresSampler{})
	RegisterSampler(&tempdbSampler{})
}
```

internal/bbsql/sched_test.go
```go
package bbsql

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func schedTestRow(current, runnable int64) Row {
	return Row{
		"current_tasks":  current,
		"runnable_tasks": runnable,
	}
}

func semaphoreTestRow(id, grantees, waiters, forced, timeouts int64) Row {
	return Row{
		"resource_semaphore_id": id,
		"grantees":              grantees,
		"waiters":               waiters,
		"forced":                forced,
		// timeout_count is the cumulative source column for Timeouts.
		"timeout_count": timeouts,
	}
}

func tempdbTestRow(internal, user uint64) Row {
	return Row{
		"internal_pages": internal,
		"user_pages":     user,
	}
}

func findSemaphoreSample(rows []Semaphores, id int32) (Semaphores, bool) {
	for _, row := range rows {
		if row.ResourceSemaphoreID == id {
			return row, true
		}
	}
	return Semaphores{}, false
}

func TestSchedulersSummed(t *testing.T) {
	s := &schedulersSampler{}

	got, truncated, err := s.Decode([]Row{schedTestRow(3, 2)})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if truncated {
		t.Fatalf("Decode() truncated = true, want false")
	}
	value, ok := got.(Schedulers)
	if !ok {
		t.Fatalf("payload type = %T, want Schedulers", got)
	}
	if value.CurrentTasks != 3 || value.RunnableTasks != 2 {
		t.Fatalf("payload = %+v, want currentTasks=3 runnableTasks=2", value)
	}

	got, truncated, err = s.Decode(nil)
	if err != nil || truncated || got != nil {
		t.Fatalf("Decode(nil) = (%#v, %v, %v), want (nil, false, nil)", got, truncated, err)
	}
	got, truncated, err = s.Decode([]Row{})
	if err != nil || truncated || got != nil {
		t.Fatalf("Decode(empty) = (%#v, %v, %v), want (nil, false, nil)", got, truncated, err)
	}
}

func TestSchedulersCapOne(t *testing.T) {
	s := &schedulersSampler{}

	got, truncated, err := s.Decode([]Row{schedTestRow(3, 2), schedTestRow(9, 8)})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !truncated {
		t.Fatalf("Decode() truncated = false, want true for 2 rows with CapSchedulers=1")
	}
	value, ok := got.(Schedulers)
	if !ok {
		t.Fatalf("payload type = %T, want Schedulers", got)
	}
	if value.CurrentTasks != 3 || value.RunnableTasks != 2 {
		t.Fatalf("payload = %+v, want the first row (3,2)", value)
	}
}

func TestTempdbTotals(t *testing.T) {
	s := &tempdbSampler{}

	got, truncated, err := s.Decode([]Row{tempdbTestRow(1024, 2048)})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if truncated {
		t.Fatalf("Decode() truncated = true, want false")
	}
	value, ok := got.(Tempdb)
	if !ok {
		t.Fatalf("payload type = %T, want Tempdb", got)
	}
	if value.InternalPages != 1024 || value.UserPages != 2048 {
		t.Fatalf("payload = %+v, want internalPages=1024 userPages=2048", value)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}
	encoded = bytes.TrimSpace(encoded)
	if len(encoded) == 0 || encoded[0] != '{' {
		t.Fatalf("payload JSON = %q, want a single object", encoded)
	}

	got, truncated, err = s.Decode(nil)
	if err != nil || truncated || got != nil {
		t.Fatalf("Decode(nil) = (%#v, %v, %v), want (nil, false, nil)", got, truncated, err)
	}
}

func TestSemaphoresDelta(t *testing.T) {
	s := &semaphoresSampler{}

	first, truncated, err := s.Decode([]Row{
		semaphoreTestRow(1, 10, 3, 100, 50),
		semaphoreTestRow(2, 7, 1, 40, 9),
	})
	if err != nil {
		t.Fatalf("cycle 1 Decode() error = %v", err)
	}
	if truncated || first != nil {
		t.Fatalf("cycle 1 = (%#v, %v), want (nil, false)", first, truncated)
	}

	got, truncated, err := s.Decode([]Row{
		semaphoreTestRow(1, 14, 5, 105, 52),
		semaphoreTestRow(2, 7, 1, 40, 9),
	})
	if err != nil {
		t.Fatalf("cycle 2 Decode() error = %v", err)
	}
	if truncated {
		t.Fatalf("cycle 2 truncated = true, want false")
	}
	rows, ok := got.([]Semaphores)
	if !ok {
		t.Fatalf("payload type = %T, want []Semaphores", got)
	}
	if len(rows) != 2 {
		t.Fatalf("len(payload) = %d, want 2", len(rows))
	}

	row, ok := findSemaphoreSample(rows, 1)
	if !ok {
		t.Fatalf("id 1 missing from payload %+v", rows)
	}
	if row.Grantees != 14 || row.Waiters != 5 {
		t.Fatalf("id 1 instantaneous values = (%d,%d), want current (14,5)", row.Grantees, row.Waiters)
	}
	if row.Forced != 5 || row.Timeouts != 2 {
		t.Fatalf("id 1 deltas = (%d,%d), want (5,2)", row.Forced, row.Timeouts)
	}

	steady, ok := findSemaphoreSample(rows, 2)
	if !ok {
		t.Fatalf("steady id 2 missing from payload %+v", rows)
	}
	if steady.Forced != 0 || steady.Timeouts != 0 {
		t.Fatalf("id 2 zero deltas = (%d,%d), want (0,0)", steady.Forced, steady.Timeouts)
	}
	if steady.Grantees != 7 || steady.Waiters != 1 {
		t.Fatalf("id 2 instantaneous values = (%d,%d), want current (7,1)", steady.Grantees, steady.Waiters)
	}
}

func TestSemaphoresReset(t *testing.T) {
	s := &semaphoresSampler{}

	if _, _, err := s.Decode([]Row{semaphoreTestRow(1, 10, 3, 100, 50)}); err != nil {
		t.Fatalf("cycle 1 Decode() error = %v", err)
	}

	got, _, err := s.Decode([]Row{semaphoreTestRow(1, 12, 2, 3, 57)})
	if err != nil {
		t.Fatalf("cycle 2 Decode() error = %v", err)
	}
	if got != nil {
		t.Fatalf("cycle 2 payload = %#v, want nil after the counter went backwards", got)
	}

	got, _, err = s.Decode([]Row{semaphoreTestRow(1, 13, 4, 7, 60)})
	if err != nil {
		t.Fatalf("cycle 3 Decode() error = %v", err)
	}
	rows, ok := got.([]Semaphores)
	if !ok || len(rows) != 1 {
		t.Fatalf("cycle 3 payload = %#v, want one Semaphores row", got)
	}
	if rows[0].Forced != 4 || rows[0].Timeouts != 3 {
		t.Fatalf("cycle 3 deltas = (%d,%d), want deltas from the re-baselined (3,57): (4,3)",
			rows[0].Forced, rows[0].Timeouts)
	}
}

func TestSemaphoresBoundedPrev(t *testing.T) {
	s := &semaphoresSampler{}

	for cycle := int32(0); cycle < 2; cycle++ {
		rows := make([]Row, 0, 20)
		for i := int32(0); i < 20; i++ {
			id := cycle*20 + i + 1
			rows = append(rows, semaphoreTestRow(int64(id), 1, 0, int64(id), 0))
		}
		if _, _, err := s.Decode(rows); err != nil {
			t.Fatalf("cycle %d Decode() error = %v", cycle+1, err)
		}
		if len(s.prev) > CapSemaphores {
			t.Fatalf("cycle %d len(prev) = %d, want <= %d", cycle+1, len(s.prev), CapSemaphores)
		}
	}
}

func TestSemaphoresStaleIDDropped(t *testing.T) {
	s := &semaphoresSampler{}

	if _, _, err := s.Decode([]Row{
		semaphoreTestRow(1, 1, 0, 10, 5),
		semaphoreTestRow(2, 2, 0, 20, 10),
	}); err != nil {
		t.Fatalf("cycle 1 Decode() error = %v", err)
	}
	if len(s.prev) != 2 {
		t.Fatalf("cycle 1 len(prev) = %d, want 2", len(s.prev))
	}

	got, _, err := s.Decode([]Row{semaphoreTestRow(1, 1, 0, 10, 5)})
	if err != nil {
		t.Fatalf("cycle 2 Decode() error = %v", err)
	}
	if len(s.prev) != 1 {
		t.Fatalf("cycle 2 len(prev) = %d, want 1", len(s.prev))
	}
	if _, ok := s.prev[2]; ok {
		t.Fatalf("stale id 2 still present in prev %+v", s.prev)
	}
	rows, ok := got.([]Semaphores)
	if !ok || len(rows) != 1 || rows[0].ResourceSemaphoreID != 1 {
		t.Fatalf("cycle 2 payload = %#v, want only id 1", got)
	}
}

func TestSchedRegistered(t *testing.T) {
	cases := []struct {
		sampler  Sampler
		id       string
		kind     string
		interval time.Duration
	}{
		{&schedulersSampler{}, "bb:schedulers", "schedulers", BaseInterval},
		{&semaphoresSampler{}, "bb:semaphores", "semaphores", BaseInterval},
		{&tempdbSampler{}, "bb:tempdb", "tempdb", DemotedInterval},
	}

	for _, tc := range cases {
		if got := tc.sampler.ID(); got != tc.id {
			t.Errorf("%T.ID() = %q, want %q", tc.sampler, got, tc.id)
		}
		if got := tc.sampler.Kind(); got != tc.kind {
			t.Errorf("%T.Kind() = %q, want %q", tc.sampler, got, tc.kind)
		}
		if got := tc.sampler.Interval(); got != tc.interval {
			t.Errorf("%T.Interval() = %s, want %s", tc.sampler, got, tc.interval)
		}
		if _, ok := Statement(tc.id); !ok {
			t.Errorf("Statement(%q) not allowlisted", tc.id)
		}
		// init() already registered these kinds; duplicate entries are ignored,
		// so this only proves registration of a fresh value is accepted.
		RegisterSampler(tc.sampler)
	}
}

func TestSchedNoForbiddenFields(t *testing.T) {
	forbidden := []struct {
		name  string
		value any
	}{
		{"scheduler_id", int64(1)},
		{"parent_node_id", int64(0)},
		{"login_name", "sa"},
		{"host_name", "box"},
		{"program_name", "app"},
		{"description", "description"},
		{"text", "select 1"},
	}

	samplers := []struct {
		name    string
		sampler Sampler
		base    Row
	}{
		{
			name:    "schedulers",
			sampler: &schedulersSampler{},
			base: Row{
				"current_tasks":  int64(1),
				"runnable_tasks": int64(0),
			},
		},
		{
			name:    "semaphores",
			sampler: &semaphoresSampler{},
			base: Row{
				"resource_semaphore_id": int64(1),
				"grantees":              int64(1),
				"waiters":               int64(0),
				"forced":                int64(0),
				"timeout_count":         int64(0),
			},
		},
		{
			name:    "tempdb",
			sampler: &tempdbSampler{},
			base: Row{
				"internal_pages": uint64(1),
				"user_pages":     uint64(1),
			},
		},
	}

	for _, sm := range samplers {
		for _, field := range forbidden {
			row := make(Row, len(sm.base)+1)
			for key, value := range sm.base {
				row[key] = value
			}
			row[field.name] = field.value

			got, _, err := sm.sampler.Decode([]Row{row})
			if !errors.Is(err, errUnknownColumn) {
				t.Errorf("%s with %q: error = %v, want errUnknownColumn", sm.name, field.name, err)
			}
			if got != nil {
				t.Errorf("%s with %q: payload = %#v, want nil", sm.name, field.name, got)
			}
		}
	}
}
```

- Test rows use snake_case aliases: `current_tasks`/`runnable_tasks`, `resource_semaphore_id`/`grantees`/`waiters`/`forced`, `internal_pages`/`user_pages`.
- `timeout_count` is assumed to be the statement alias that `decodeRows` maps to `Semaphores.Timeouts`; if the hidden alias is `timeouts`, change that one test key.
- Semaphore payload is `[]Semaphores` with current `Grantees`/`Waiters` and delta `Forced`/`Timeouts`; first cycle and all-new-id cycles return nil.
- Any backwards `forced` or `timeouts` re-baselines that id and suppresses it for the cycle; the next cycle measures from the new baseline.
- `prev` is rebuilt each cycle and cleared on empty rows, so it never exceeds `CapSemaphores` and stale ids disappear.
- `TestSchedRegistered` verifies IDs/Kind/Interval/allowlist and calls `RegisterSampler` on fresh values; `Samplers()` is not in the stated surface, so registry enumeration is not asserted.
- Reviewer should confirm `decodeRows` keeps the first `cap` rows (the cap-one test assumes first-row retention) and that unknown-column errors satisfy `errors.Is(err, errUnknownColumn)`.
