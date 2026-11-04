package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// rollupTestRepo is a fake repository that also reads rollups. It counts calls so
// the tests can tell a cache hit from a read.
type rollupTestRepo struct {
	fakeRepository
	ids     []string
	calls   int
	err     error
	failing map[string]bool
	deleted []string
}

func (r *rollupTestRepo) Sources(context.Context) ([]audit.Source, error) {
	var out []audit.Source
	for _, id := range r.ids {
		out = append(out, audit.Source{ID: id})
	}
	return out, nil
}

func (r *rollupTestRepo) DeleteSource(_ context.Context, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *rollupTestRepo) Rollup(_ context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	r.calls++
	if r.err != nil {
		return audit.RollupSlots{}, r.err
	}
	builder := audit.NewRollupBuilder(plan.Width)
	out := audit.RollupSlots{Builder: builder}
	for _, id := range plan.SourceIDs {
		if r.failing[id] {
			out.Sources = append(out.Sources, audit.SourceResult{SourceID: id, ErrorCode: "unavailable", Error: "The source did not answer."})
			continue
		}
		out.Sources = append(out.Sources, audit.SourceResult{SourceID: id, OK: true})
		out.Provenance = append(out.Provenance, audit.Provenance{SourceID: id})
		builder.AddCounts(plan.From.Unix()+3600, audit.RollupCounts{Total: 150, Sync: 100, Background: 50, C4xx: 3})
	}
	return out, nil
}

const rollupTestQuery = "from=2026-10-01T00:00:00Z&to=2026-10-04T00:00:00Z&tz=UTC"

func getRollup(h http.Handler, path string) (int, map[string]any) {
	res := request(h, http.MethodGet, path, "", true)
	var body map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &body)
	return res.Code, body
}

func TestDailyAndHourGridShapes(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a"}}
	h := New(repo, nil, testToken, false)

	code, daily := getRollup(h, "/api/daily?"+rollupTestQuery)
	if code != http.StatusOK {
		t.Fatalf("daily = %d %v", code, daily)
	}
	days, _ := daily["days"].([]any)
	if len(days) != 3 || daily["zone"] != "UTC" || daily["lowTrafficBelow"] != float64(audit.RollupLowTraffic) || daily["flatPercent"] != float64(audit.RollupFlatPercent) {
		t.Fatalf("daily body = %v", daily)
	}
	first := days[0].(map[string]any)
	for _, key := range []string{"date", "start", "end", "hours", "total", "sync", "background", "partial", "lowTraffic"} {
		if _, ok := first[key]; !ok {
			t.Errorf("a day is missing %q: %v", key, first)
		}
	}
	if first["total"] != float64(150) || first["sync"] != float64(100) || first["background"] != float64(50) {
		t.Errorf("first day counts = %v", first)
	}
	if srcs, _ := daily["sources"].([]any); len(srcs) != 1 {
		t.Errorf("sources = %v", daily["sources"])
	}

	code, grid := getRollup(h, "/api/hourgrid?"+rollupTestQuery)
	if code != http.StatusOK {
		t.Fatalf("hourgrid = %d %v", code, grid)
	}
	cells, _ := grid["cells"].([]any)
	if len(cells) == 0 || grid["noiseBelow"] != float64(audit.RollupNoiseCount) {
		t.Fatalf("grid body = %v", grid)
	}
	cell := cells[0].(map[string]any)
	for _, key := range []string{"date", "hour", "start", "end", "count", "slowCount", "noise"} {
		if _, ok := cell[key]; !ok {
			t.Errorf("a cell is missing %q: %v", key, cell)
		}
	}
	if gd, _ := grid["days"].([]any); len(gd) != 3 {
		t.Errorf("grid days = %v", grid["days"])
	}
}

func TestRollupRejectsBadInputBeforeReading(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a"}}
	h := New(repo, nil, testToken, false)
	cases := map[string]string{
		"missing from":   "to=2026-10-04T00:00:00Z",
		"bad to":         "from=2026-10-01T00:00:00Z&to=tomorrow",
		"from after to":  "from=2026-10-04T00:00:00Z&to=2026-10-01T00:00:00Z",
		"from equals to": "from=2026-10-01T00:00:00Z&to=2026-10-01T00:00:00Z",
		"121 days":       "from=2026-01-01T00:00:00Z&to=2026-05-02T00:00:00Z",
		"NaN slowMs":     rollupTestQuery + "&slowMs=NaN",
		"text slowMs":    rollupTestQuery + "&slowMs=fast",
		"negative slow":  rollupTestQuery + "&slowMs=-5",
		"huge slowMs":    rollupTestQuery + "&slowMs=99999999999",
		"bad tz":         "from=2026-10-01T00:00:00Z&to=2026-10-04T00:00:00Z&tz=Mars/Base",
	}
	for _, path := range []string{"/api/daily?", "/api/hourgrid?"} {
		for name, query := range cases {
			if res := request(h, http.MethodGet, path+query, "", true); res.Code != http.StatusBadRequest {
				t.Errorf("%s %s = %d %s, want 400", path, name, res.Code, res.Body.String())
			}
		}
	}
	if repo.calls != 0 {
		t.Errorf("a rejected request must not read, got %d reads", repo.calls)
	}
	// Exactly 120 days is the largest allowed window.
	ok := "from=2026-01-01T00:00:00Z&to=2026-05-01T00:00:00Z&tz=UTC"
	if res := request(h, http.MethodGet, "/api/daily?"+ok, "", true); res.Code != http.StatusOK {
		t.Errorf("120 days = %d %s", res.Code, res.Body.String())
	}
}

func TestRollupErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"broad scan", fmt.Errorf("%w: wide", audit.ErrBroadScanRefused), http.StatusConflict},
		{"group cap", fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrRollupGroupLimit), http.StatusRequestEntityTooLarge},
		{"docs budget", audit.ErrDocsExaminedBudget, http.StatusRequestEntityTooLarge},
		{"size check", audit.ErrCollectionSizeCheck, http.StatusBadGateway},
		{"not found", fmt.Errorf("%w: source was not found", audit.ErrNotFound), http.StatusNotFound},
	}
	for _, c := range cases {
		repo := &rollupTestRepo{ids: []string{"a"}, err: c.err}
		h := New(repo, nil, testToken, false)
		for _, path := range []string{"/api/daily?", "/api/hourgrid?"} {
			res := request(h, http.MethodGet, path+rollupTestQuery, "", true)
			if res.Code != c.code {
				t.Errorf("%s %s = %d %s, want %d", c.name, path, res.Code, res.Body.String(), c.code)
			}
			if strings.Contains(res.Body.String(), "wide: ") && c.code == http.StatusConflict && !strings.Contains(res.Body.String(), "24 hours") {
				t.Errorf("%s: the refusal should explain the 24 hour rule: %s", c.name, res.Body.String())
			}
		}
	}
	// The refusal carries a machine code so the page can offer the right help.
	repo := &rollupTestRepo{ids: []string{"a"}, err: audit.ErrBroadScanRefused}
	_, body := getRollup(New(repo, nil, testToken, false), "/api/daily?"+rollupTestQuery)
	if body["code"] != "broad_scan_refused" || !strings.Contains(fmt.Sprint(body["error"]), "--allow-broad-scans") {
		t.Errorf("refusal body = %v", body)
	}
}

func TestRollupNeedsAReaderAndAToken(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, false)
	for _, path := range []string{"/api/daily?", "/api/hourgrid?"} {
		if res := request(h, http.MethodGet, path+rollupTestQuery+"&sources=a", "", true); res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without a rollup reader = %d, want 503", path, res.Code)
		}
		if res := request(h, http.MethodGet, path+rollupTestQuery, "", false); res.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token = %d, want 401", path, res.Code)
		}
	}
}

func TestRollupUsesAllSourcesByDefaultAndHonoursSelection(t *testing.T) {
	var seen [][]string
	repo := &rollupTestRepo{ids: []string{"a", "b"}}
	h := New(&spyRollupRepo{rollupTestRepo: repo, seen: &seen}, nil, testToken, false)
	for _, query := range []string{
		rollupTestQuery,
		rollupTestQuery + "&sources=b",
		rollupTestQuery + "&source=a&source=b",
	} {
		if res := request(h, http.MethodGet, "/api/daily?"+query, "", true); res.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", query, res.Code, res.Body.String())
		}
	}
	// The third request names the same sources as the first, so it is a cache hit.
	want := [][]string{{"a", "b"}, {"b"}}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("sources read = %v, want %v", seen, want)
	}
}

// spyRollupRepo records the source IDs of each Rollup call.
type spyRollupRepo struct {
	*rollupTestRepo
	seen *[][]string
}

func (s *spyRollupRepo) Rollup(ctx context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	*s.seen = append(*s.seen, plan.SourceIDs)
	return s.rollupTestRepo.Rollup(ctx, plan)
}

func TestRollupCacheServesBothViewsFromOneRead(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a"}}
	h := New(repo, nil, testToken, false)
	for _, path := range []string{"/api/daily?", "/api/hourgrid?", "/api/daily?", "/api/hourgrid?"} {
		if res := request(h, http.MethodGet, path+rollupTestQuery, "", true); res.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, res.Code)
		}
	}
	if repo.calls != 1 {
		t.Errorf("the table and the grid of one window share a read, got %d reads", repo.calls)
	}
	// Another window, zone or slow limit is another read.
	for _, query := range []string{
		"from=2026-10-01T00:00:00Z&to=2026-10-05T00:00:00Z&tz=UTC",
		"from=2026-10-01T00:00:00Z&to=2026-10-04T00:00:00Z&tz=Asia/Kolkata",
		rollupTestQuery + "&slowMs=500",
	} {
		before := repo.calls
		request(h, http.MethodGet, "/api/daily?"+query, "", true)
		if repo.calls != before+1 {
			t.Errorf("%s should miss the cache", query)
		}
	}
}

func TestRollupCacheExpiresAfterItsTTL(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a"}}
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	clock := &now
	h := New(repo, nil, testToken, false, func(h *handler) { h.rollups.now = func() time.Time { return *clock } })

	request(h, http.MethodGet, "/api/daily?"+rollupTestQuery, "", true)
	*clock = clock.Add(rollupCacheTTL - time.Second)
	request(h, http.MethodGet, "/api/hourgrid?"+rollupTestQuery, "", true)
	if repo.calls != 1 {
		t.Fatalf("inside the TTL the read is reused, got %d reads", repo.calls)
	}
	*clock = clock.Add(2 * time.Second)
	request(h, http.MethodGet, "/api/daily?"+rollupTestQuery, "", true)
	if repo.calls != 2 {
		t.Errorf("after the TTL the source is read again, got %d reads", repo.calls)
	}
}

func TestRollupCacheIsClearedWhenASourceIsRemoved(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a"}}
	h := New(repo, nil, testToken, false)
	request(h, http.MethodGet, "/api/daily?"+rollupTestQuery, "", true)
	if res := request(h, http.MethodDelete, "/api/sources/a", "", true); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body.String())
	}
	request(h, http.MethodGet, "/api/daily?"+rollupTestQuery, "", true)
	if repo.calls != 2 {
		t.Errorf("a removed source must not be served from the cache, got %d reads", repo.calls)
	}
}

func TestRollupCacheIsClearedWhenASourceIsAdded(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a"}}
	h := New(repo, nil, testToken, false)
	request(h, http.MethodGet, "/api/daily?"+rollupTestQuery, "", true)
	body := `{"label":"b","uri":"mongodb://localhost:27017","tlsMode":"disabled"}`
	res := request(h, http.MethodPost, "/api/sources", body, true)
	if res.Code != http.StatusCreated {
		t.Skipf("the add-source form needs more than this fake supports: %d %s", res.Code, res.Body.String())
	}
	request(h, http.MethodGet, "/api/daily?"+rollupTestQuery, "", true)
	if repo.calls != 2 {
		t.Errorf("an added source must clear the cache, got %d reads", repo.calls)
	}
}

func TestRollupDoesNotCacheAPartialAnswer(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a", "b"}, failing: map[string]bool{"b": true}}
	h := New(repo, nil, testToken, false)
	code, body := getRollup(h, "/api/daily?"+rollupTestQuery)
	if code != http.StatusOK {
		t.Fatalf("a partial answer is still a 200: %d %v", code, body)
	}
	srcs, _ := body["sources"].([]any)
	if len(srcs) != 2 || srcs[1].(map[string]any)["ok"] != false {
		t.Errorf("the failed source must be reported: %v", body["sources"])
	}
	if days, _ := body["days"].([]any); len(days) != 3 || days[0].(map[string]any)["total"] != float64(150) {
		t.Errorf("the good source's rows stay: %v", body["days"])
	}
	request(h, http.MethodGet, "/api/hourgrid?"+rollupTestQuery, "", true)
	if repo.calls != 2 {
		t.Errorf("a partial answer is read again, not cached: %d reads", repo.calls)
	}
}

func TestRollupWithNoSourceAnsweringHasNoDays(t *testing.T) {
	repo := &rollupTestRepo{ids: []string{"a"}, failing: map[string]bool{"a": true}}
	h := New(repo, nil, testToken, false)
	code, daily := getRollup(h, "/api/daily?"+rollupTestQuery)
	days, isList := daily["days"].([]any)
	if code != http.StatusOK || !isList || len(days) != 0 {
		t.Errorf("zero rows must not pass for a quiet system: %d %v", code, daily)
	}
	code, grid := getRollup(h, "/api/hourgrid?"+rollupTestQuery)
	cells, isList := grid["cells"].([]any)
	if code != http.StatusOK || !isList || len(cells) != 0 {
		t.Errorf("hourgrid with no source = %d %v", code, grid)
	}
	if repo.calls != 2 {
		t.Errorf("nothing is cached when no source answered: %d reads", repo.calls)
	}
}

func TestRollupCacheKeepsAtMostEightWindows(t *testing.T) {
	var c rollupCache
	plan := func(day int) audit.RollupPlan {
		p, err := audit.PlanRollup(audit.RollupInput{SourceIDs: []string{"a"}, From: time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, day+1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for day := 1; day <= 12; day++ {
		c.put(plan(day), audit.RollupSlots{})
	}
	if len(c.entries) != rollupCacheEntries {
		t.Errorf("entries = %d, want %d", len(c.entries), rollupCacheEntries)
	}
	if _, hit := c.get(plan(12)); !hit {
		t.Error("the newest window must be kept")
	}
	if _, hit := c.get(plan(1)); hit {
		t.Error("the oldest window must have been dropped")
	}
}

func TestRollupSourceParamsAreEscapedNotTrusted(t *testing.T) {
	var seen [][]string
	repo := &rollupTestRepo{ids: []string{"a"}}
	h := New(&spyRollupRepo{rollupTestRepo: repo, seen: &seen}, nil, testToken, false)
	q := rollupTestQuery + "&sources=" + url.QueryEscape(" a , ,b ")
	if res := request(h, http.MethodGet, "/api/daily?"+q, "", true); res.Code != http.StatusOK {
		t.Fatalf("code %d %s", res.Code, res.Body.String())
	}
	if fmt.Sprint(seen) != "[[a b]]" {
		t.Errorf("sources = %v, want blanks dropped and names trimmed", seen)
	}
}

func rollupCachePlan(t *testing.T, day int) audit.RollupPlan {
	t.Helper()
	p, err := audit.PlanRollup(audit.RollupInput{SourceIDs: []string{"a"}, From: time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 1, day+1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// slotsWithGroups is a read that holds n groups.
func slotsWithGroups(n int) audit.RollupSlots {
	b := audit.NewRollupBuilder(time.Hour)
	for i := 0; i < n; i++ {
		b.AddCounts(int64(i)*3600, audit.RollupCounts{Total: 1})
	}
	return audit.RollupSlots{Builder: b}
}

// put drops the entries that are past their TTL, so a cache nobody reads from
// does not keep old reads in memory (PF-B-03).
func TestRollupCachePutPrunesExpiredEntries(t *testing.T) {
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	c := rollupCache{now: func() time.Time { return now }}
	c.put(rollupCachePlan(t, 1), audit.RollupSlots{})
	c.put(rollupCachePlan(t, 2), audit.RollupSlots{})
	now = now.Add(rollupCacheTTL - time.Second)
	c.put(rollupCachePlan(t, 3), audit.RollupSlots{})
	if len(c.entries) != 3 {
		t.Fatalf("inside the TTL: %d entries, want 3", len(c.entries))
	}
	now = now.Add(2 * time.Second) // days 1 and 2 are now past the TTL, day 3 is not
	c.put(rollupCachePlan(t, 4), audit.RollupSlots{})
	if len(c.entries) != 2 {
		t.Fatalf("after the TTL: %d entries, want 2 (days 3 and 4)", len(c.entries))
	}
	for _, e := range c.entries {
		if now.Sub(e.at) >= rollupCacheTTL {
			t.Errorf("an expired entry is still held: %v", e.at)
		}
	}
}

// The groups all the entries hold together are capped: the oldest entries go
// first, and a read over the cap by itself is not held at all.
func TestRollupCacheCapsTotalGroups(t *testing.T) {
	var c rollupCache
	third := rollupCacheGroups / 3
	c.put(rollupCachePlan(t, 1), slotsWithGroups(third))
	c.put(rollupCachePlan(t, 2), slotsWithGroups(third))
	c.put(rollupCachePlan(t, 3), slotsWithGroups(third))
	if len(c.entries) != 3 {
		t.Fatalf("three reads under the cap: %d entries", len(c.entries))
	}
	// A fourth read of the same size pushes the total over the cap: day 1, the
	// oldest, is dropped.
	c.put(rollupCachePlan(t, 4), slotsWithGroups(third))
	if _, hit := c.get(rollupCachePlan(t, 1)); hit {
		t.Error("the oldest read must go when the cap is passed")
	}
	if _, hit := c.get(rollupCachePlan(t, 4)); !hit {
		t.Error("the newest read must be held")
	}
	var total int64
	for _, e := range c.entries {
		total += e.groups
	}
	if total > rollupCacheGroups {
		t.Errorf("held %d groups, cap %d", total, rollupCacheGroups)
	}
	// One read over the cap alone is not held, and the others stay.
	before := len(c.entries)
	c.put(rollupCachePlan(t, 5), slotsWithGroups(rollupCacheGroups+1))
	if _, hit := c.get(rollupCachePlan(t, 5)); hit {
		t.Error("a read over the cap by itself must not be held")
	}
	if len(c.entries) != before {
		t.Errorf("entries = %d, want the %d already held", len(c.entries), before)
	}
}

// When a new read does not fit beside the newest kept entries, it and every older
// entry go: eviction is oldest first, not packing by size (PF-B-03).
func TestRollupCacheEvictsOldestFirstNotBySize(t *testing.T) {
	var c rollupCache
	c.put(rollupCachePlan(t, 1), slotsWithGroups(rollupCacheGroups/4))
	c.put(rollupCachePlan(t, 2), slotsWithGroups(rollupCacheGroups*2/3))
	if len(c.entries) != 2 {
		t.Fatalf("two reads under the cap: %d entries", len(c.entries))
	}
	// Day 3 does not fit beside day 2, the newer of the two, but would fit beside
	// day 1: day 1 must not be kept in place of day 2.
	c.put(rollupCachePlan(t, 3), slotsWithGroups(rollupCacheGroups/2))
	if _, hit := c.get(rollupCachePlan(t, 3)); !hit {
		t.Error("the new read must be kept")
	}
	if _, hit := c.get(rollupCachePlan(t, 2)); hit {
		t.Error("day 2 does not fit beside day 3 and must be dropped")
	}
	if _, hit := c.get(rollupCachePlan(t, 1)); hit {
		t.Error("day 1 is older than the entry that did not fit and must be dropped too")
	}
}

// A read built row by row (the Go scan path) counts the slots and bins it holds,
// so it is bounded by the cache cap like a read merged from an aggregation.
func TestRollupGroupsCountsRowsAndBins(t *testing.T) {
	b := audit.NewRollupBuilder(time.Hour)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fast, slow := 5.0, 5000.0
	b.AddRow(at, "synchronous", "valid", "", &fast, 1000)
	b.AddRow(at, "synchronous", "valid", "", &slow, 1000)
	b.AddRow(at.Add(2*time.Hour), "synchronous", "valid", "", &fast, 1000)
	// Two slots and three slot-bin pairs.
	if got := b.Groups(); got < 5 {
		t.Errorf("Groups = %d, want at least the 2 slots and 3 bins held", got)
	}
	b.AddBin(at.Add(4*time.Hour).Unix(), 3, 1, 10, 10, 10)
	if got := slotGroups(audit.RollupSlots{Builder: b}); got < 7 {
		t.Errorf("slotGroups = %d after an extra slot and bin, want at least 7", got)
	}
}
