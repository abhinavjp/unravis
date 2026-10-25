package rules_test

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
	"api-audit-explorer/internal/resource/rules"
)

// All fixtures are synthetic: 2001 dates and invented host names.
var day = time.Date(2001, 10, 25, 0, 0, 0, 0, time.UTC)

func hms(h, m, s int) time.Time {
	return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second)
}

// stall is the shared synthetic stall on web-a: 28 s ending 01:02:31.
var stall = join.Interval{ID: "ep-0", Start: hms(1, 2, 3), End: hms(1, 2, 31)}

// meta is what one import says about its source.
type meta struct {
	host    string
	quality evidence.SourceQuality
	gaps    []string
}

type harness struct {
	t       *testing.T
	ctx     context.Context
	c       casestore.Case
	id      string
	pending map[string][]evidence.Event
	order   []string
	meta    map[string]meta
	files   int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "rules")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &harness{t: t, ctx: ctx, c: c, id: string(info.ID), pending: map[string][]evidence.Event{}, meta: map[string]meta{}}
}

func (h *harness) add(evs ...evidence.Event) {
	for _, e := range evs {
		if _, ok := h.pending[e.Source]; !ok {
			h.order = append(h.order, e.Source)
		}
		h.pending[e.Source] = append(h.pending[e.Source], e)
	}
}

// commit imports the pending events, one file per source.
func (h *harness) commit() {
	h.t.Helper()
	for _, src := range h.order {
		evs := h.pending[src]
		m := h.meta[src]
		h.files++
		nonce := "n" + strconv.Itoa(h.files)
		for i := range evs {
			evs[i].EvidenceID = evidence.EventID(h.id, src, nonce, int64(i+1), 0)
			evs[i].Line = int64(i + 1)
		}
		run := func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			rep := importer.Report{Importer: src, Quality: m.quality, KnownGaps: m.gaps}
			if _, err := io.Copy(io.Discard, r); err != nil {
				return rep, err
			}
			for _, e := range evs {
				if err := emit(e); err != nil {
					return rep, err
				}
				rep.Emitted++
			}
			return rep, nil
		}
		_, err := h.c.Import(h.ctx, casestore.ImportMeta{Importer: src, FileName: "f.txt", HostLabel: m.host, AckGating: true, AckText: "ack"},
			strings.NewReader("file "+nonce), run)
		if err != nil {
			h.t.Fatalf("import %s: %v", src, err)
		}
	}
	h.pending = map[string][]evidence.Event{}
	h.order = nil
}

func (h *harness) eval(from, to time.Time, stalls ...join.Interval) rules.Result {
	h.t.Helper()
	h.commit()
	res, err := rules.Evaluate(h.ctx, h.c, importer.Window{From: from, To: to}, stalls, rules.DefaultThresholds())
	if err != nil {
		h.t.Fatal(err)
	}
	checkResult(h.t, res)
	return res
}

func fp(v float64) *float64 { return &v }

func attrs(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func pdh(host string, t time.Time, object, inst, counter string, v float64, unit string) evidence.Event {
	a := attrs("object", object, "counter", counter)
	if inst != "" {
		a["instance"] = inst
	}
	return evidence.Event{Source: "win-counters", Kind: "counter", Host: host, Start: t, End: t, Value: fp(v), Unit: unit,
		TZRule: clock.RuleHeaderBias, Attrs: a, Line: 1}
}

func metric(src, host string, t time.Time, v float64, unit string, kv ...string) evidence.Event {
	return evidence.Event{Source: src, Kind: "metric", Host: host, Start: t, End: t, Value: fp(v), Unit: unit,
		TZRule: clock.RuleUTCField, Attrs: attrs(kv...), Line: 1}
}

func point(src, kind, host string, t time.Time, kv ...string) evidence.Event {
	return evidence.Event{Source: src, Kind: kind, Host: host, Start: t, End: t, TZRule: clock.RuleUTCField, Attrs: attrs(kv...), Line: 1}
}

func span(src, kind, host string, start time.Time, ms float64, kv ...string) evidence.Event {
	return evidence.Event{Source: src, Kind: kind, Host: host, Start: start, End: start.Add(time.Duration(ms * float64(time.Millisecond))),
		DurationMs: fp(ms), TZRule: clock.RuleUTCField, Attrs: attrs(kv...), Line: 1}
}

func tp(t time.Time) *time.Time { return &t }

// every calls fn for each step from from to to inclusive.
func every(from, to time.Time, step time.Duration, fn func(time.Time)) {
	for t := from; !t.After(to); t = t.Add(step) {
		fn(t)
	}
}

// checkResult holds for every result: no causal wording anywhere, no label
// above Correlation, and no nil slice.
func checkResult(t *testing.T, res rules.Result) {
	t.Helper()
	if res.Hosts == nil || res.Rules == nil || res.Gaps == nil || res.Markers == nil || res.Episodes == nil {
		t.Fatalf("nil slice in result")
	}
	lint := func(what, s string) {
		if err := evidence.LintCausal(s); err != nil {
			t.Errorf("%s %q: %v", what, s, err)
		}
	}
	for _, r := range res.Rules {
		lint("wording", r.Wording)
		lint("title", r.Title)
		lint("thresholds", r.Thresholds)
		if r.Label != audit.EvidenceSymptom && r.Label != audit.EvidenceCorrelation {
			t.Errorf("label %q above Correlation in %q", r.Label, r.Wording)
		}
		if r.Thresholds == "" {
			t.Errorf("row %s %q states no thresholds", r.ID, r.Wording)
		}
		if r.Evidence == nil {
			t.Errorf("row %s has nil evidence", r.ID)
		}
	}
	for _, g := range res.Gaps {
		lint("gap", g)
	}
	for _, h := range res.Hosts {
		for _, s := range h.Summary {
			lint("summary", s)
		}
		for _, c := range h.Coverage {
			lint("coverage", c.Metric)
		}
	}
	for _, m := range res.Markers {
		lint("marker", m.Text)
	}
}

func wordings(res rules.Result, id string) []string {
	var out []string
	for _, r := range res.Rules {
		if r.ID == id {
			out = append(out, r.Wording)
		}
	}
	return out
}

func row(t *testing.T, res rules.Result, id, wording string) rules.Row {
	t.Helper()
	for _, r := range res.Rules {
		if r.ID == id && r.Wording == wording {
			return r
		}
	}
	t.Fatalf("no %s row %q; %s rows: %q", id, wording, id, wordings(res, id))
	return rules.Row{}
}

func hasGap(res rules.Result, g string) bool {
	for _, x := range res.Gaps {
		if x == g {
			return true
		}
	}
	return false
}

/* ------------------------------------------------------------------ tests */

func TestEpisodes(t *testing.T) {
	ivs := []join.Interval{
		{ID: "c", Start: hms(1, 30, 0), End: hms(1, 30, 5)},
		{ID: "a", Start: hms(1, 0, 0), End: hms(1, 0, 10)},
		{ID: "b", Start: hms(1, 9, 0), End: hms(1, 8, 0)},   // reversed: read as 01:08-01:09
		{ID: "d", Start: hms(1, 40, 5), End: hms(1, 40, 6)}, // exactly 10 min after c ends
	}
	eps := rules.Episodes(ivs, 10*time.Minute)
	if len(eps) != 3 {
		t.Fatalf("episodes = %+v", eps)
	}
	if strings.Join(eps[0].IDs, ",") != "a,b" || !eps[0].Start.Equal(hms(1, 0, 0)) || !eps[0].End.Equal(hms(1, 9, 0)) {
		t.Errorf("episode 0 = %+v", eps[0])
	}
	if strings.Join(eps[1].IDs, ",") != "c" || strings.Join(eps[2].IDs, ",") != "d" {
		t.Errorf("episodes = %+v", eps)
	}
	if ivs[2].Start != hms(1, 9, 0) {
		t.Error("input modified")
	}
}

// stallFixture is the shared synthetic stall: host pressure, GC, a thread-pool
// queue, a Redis timeout entry, a late heartbeat, a slow Redis command and a
// recycle shortly before.
func stallFixture(h *harness) {
	every(hms(0, 40, 0), hms(1, 20, 0), 15*time.Second, func(t time.Time) {
		cpu, mem, gc, q := 20.0, 2048.0, 2.0, 0.0
		switch t {
		case hms(1, 2, 30):
			cpu, mem, gc, q = 97.8, 420, 34, 57
		case hms(1, 2, 15):
			q = 12
		case hms(0, 42, 0):
			cpu = 95
		}
		h.add(pdh("web-a", t, "Processor", "_Total", "% Processor Time", cpu, "percent-host"),
			pdh("web-a", t, "Memory", "", "Available MBytes", mem, "count"),
			metric("dotnet-counters", "web-a", t, gc, "percent", "counter", "% Time in GC since last GC", "pid", "1240", "process", "w3wp"),
			metric("dotnet-counters", "web-a", t, q, "count", "counter", "ThreadPool Queue Length", "pid", "1240", "process", "w3wp"))
	})
	h.add(point("log4net-text", "log-entry", "web-a", hms(1, 2, 29), "family", "redis-timeout", "worker-busy", "38", "worker-min", "8"))
	h.add(span("mel-console", "heartbeat-late", "web-a", hms(1, 2, 29).Add(623*time.Millisecond), 1500))
	h.add(span("redis-slowlog", "slow-command", "redis-a", hms(0, 45, 0).Add(-15*time.Millisecond), 15, "id", "11", "command", "GET"),
		span("redis-slowlog", "slow-command", "redis-a", hms(1, 2, 20).Add(-1012*time.Millisecond), 1012, "id", "12", "command", "KEYS"))
	h.add(point("win-events", "apppool-recycle", "web-a", hms(0, 59, 32), "reason", "Time"))
	h.meta["win-counters"] = meta{host: "web-a"}
	h.meta["dotnet-counters"] = meta{host: "web-a"}
	h.meta["log4net-text"] = meta{host: "web-a"}
	h.meta["mel-console"] = meta{host: "web-a"}
	h.meta["win-events"] = meta{host: "web-a"}
	h.meta["redis-slowlog"] = meta{host: "redis-a", quality: evidence.SourceQuality{
		Threshold:     "commands slower than 10000 microseconds",
		CoverageFirst: tp(hms(0, 45, 0).Add(-15 * time.Millisecond)), CoverageLast: tp(hms(1, 2, 20)),
		TimezoneRule: clock.RuleUTCField}}
}

func TestStallPositive(t *testing.T) {
	h := newHarness(t)
	stallFixture(h)
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)

	if len(res.Episodes) != 1 || !res.Episodes[0].Start.Equal(stall.Start) {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	corr := audit.EvidenceCorrelation
	sym := audit.EvidenceSymptom
	for _, c := range []struct{ id, wording, label string }{
		{"RC3", "A Windows performance counter sample showed host CPU at 97.8 % during this interval (Correlation).", corr},
		{"RC3", "A Windows performance counter sample showed available memory at 420 MiB during this interval (Correlation).", corr},
		{"RC3", "Host CPU at or above 90 % on web-a in 1 of 1 stall episodes, and in 1 of 3 comparison windows outside stalls.", sym},
		{"RC3", "Available memory below 500 MiB on web-a in 1 of 1 stall episodes, and in 0 of 3 comparison windows outside stalls.", sym},
		{"RC4", "A .NET runtime counter sample showed time in GC for w3wp:1240 at 34 % during this interval (Correlation).", corr},
		{"RC4", "Time in GC at or above 10 % in a process on web-a in 1 of 1 stall episodes, and in 0 of 3 comparison windows outside stalls.", sym},
		{"RC5", "A .NET runtime counter sample showed thread-pool queue length for w3wp:1240 at 57 during this interval (Correlation).", corr},
		{"RC5", "Thread-pool queue length above 0 in 2 or more samples in a process on web-a in 1 of 1 stall episodes, and in 0 of 3 comparison windows outside stalls.", sym},
		{"RC5", "Redis timeout entries with WORKER busy above min on web-a in 1 of 1 stall episodes, and in 0 of 3 comparison windows outside stalls.", sym},
		{"RC5", "An ASP.NET Core console log session had a late heartbeat of 1.5 s overlapping this interval (Correlation).", corr},
		{"RC6", "The Redis SLOWLOG buffer held a slow command of 1.012 s overlapping this interval (Correlation).", corr},
		{"RC8", "This interval starts 2 min 31 s after a recycle event of the application pool (reason: Time) on web-a (Correlation).", corr},
	} {
		r := row(t, res, c.id, c.wording)
		if r.Label != c.label {
			t.Errorf("%s %q: label %q, want %q", c.id, c.wording, r.Label, c.label)
		}
		if c.label == corr && (len(r.Evidence) == 0 || r.Episode == nil) {
			t.Errorf("%s %q: correlation row without evidence or episode: %+v", c.id, c.wording, r)
		}
	}
	// The slow command row names the KEYS entry, not the earlier GET.
	slow := row(t, res, "RC6", "The Redis SLOWLOG buffer held a slow command of 1.012 s overlapping this interval (Correlation).")
	if len(slow.Evidence) != 1 {
		t.Errorf("slow command evidence = %v", slow.Evidence)
	}
	// The slow command overlapped the episode: no absence row for redis-a.
	for _, w := range wordings(res, "RC6") {
		if strings.HasPrefix(w, "Not observed") {
			t.Errorf("unexpected absence row %q", w)
		}
	}
	if !hasGap(res, "Application pool recycle events are logged by default only for the Time, Memory and PrivateMemory triggers before IIS 8.5, and logEventOnRecycle may have been changed; other recycle triggers may have left no event.") {
		t.Errorf("recycle gap missing: %q", res.Gaps)
	}
	if !hasGap(res, "Not observed (gap or sampling: no per-process counters for web-a).") {
		t.Errorf("per-process gap missing: %q", res.Gaps)
	}
	var web *rules.HostSummary
	for i := range res.Hosts {
		if res.Hosts[i].Host == "web-a" {
			web = &res.Hosts[i]
		}
	}
	if web == nil {
		t.Fatalf("no web-a summary: %+v", res.Hosts)
	}
	want := map[string]bool{"Highest host CPU in this window: 97.8 %.": false, "Lowest available memory in this window: 420 MiB.": false, "Redis timeout entries in this window: 1.": false}
	for _, s := range web.Summary {
		if _, ok := want[s]; ok {
			want[s] = true
		}
	}
	for s, ok := range want {
		if !ok {
			t.Errorf("summary %q missing: %q", s, web.Summary)
		}
	}
	if len(web.Coverage) != 4 {
		t.Errorf("coverage = %+v", web.Coverage)
	}
	for _, c := range web.Coverage {
		if c.Fraction != 1 {
			t.Errorf("coverage %s = %v", c.Metric, c.Fraction)
		}
	}
	if len(res.Markers) != 1 || res.Markers[0].Kind != "apppool-recycle" {
		t.Errorf("markers = %+v", res.Markers)
	}
}

// slowlogCase is a redis-a SLOWLOG import with the given quality, plus a
// web-a counter so the window has samples.
func slowlogCase(t *testing.T, q evidence.SourceQuality, evs ...evidence.Event) rules.Result {
	h := newHarness(t)
	h.add(evs...)
	h.meta["redis-slowlog"] = meta{host: "redis-a", quality: q,
		gaps: []string{"SLOWLOG is a ring buffer of fixed length; entries older than the oldest one in this file may have been overwritten"}}
	return h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)
}

func absenceRow(t *testing.T, res rules.Result) rules.Row {
	t.Helper()
	var out []rules.Row
	for _, r := range res.Rules {
		if r.ID == "RC6" && strings.HasPrefix(r.Wording, "Not observed") {
			out = append(out, r)
		}
	}
	if len(out) != 1 {
		t.Fatalf("absence rows = %+v", out)
	}
	if out[0].Label != audit.EvidenceSymptom || out[0].Episode == nil || out[0].Host != "redis-a" {
		t.Errorf("absence row = %+v", out[0])
	}
	return out[0]
}

func TestSlowlogCoverageStartsAfterStall(t *testing.T) {
	q := evidence.SourceQuality{Threshold: "commands slower than 10000 microseconds",
		CoverageFirst: tp(hms(1, 2, 59)), CoverageLast: tp(hms(1, 5, 0)), TimezoneRule: clock.RuleUTCField}
	res := slowlogCase(t, q,
		span("redis-slowlog", "slow-command", "redis-a", hms(1, 2, 59), 1000, "id", "20"),
		span("redis-slowlog", "slow-command", "redis-a", hms(1, 4, 59).Add(500*time.Millisecond), 500, "id", "21"))
	got := absenceRow(t, res).Wording
	want := "Not observed (gap or sampling: the Redis slow-log window on redis-a starts at 2001-10-25T01:02:59Z, after this interval starts)."
	if got != want {
		t.Errorf("absence = %q\nwant      %q", got, want)
	}
	for _, w := range wordings(res, "RC6") {
		if strings.Contains(w, "Correlation") {
			t.Errorf("unexpected overlap row %q", w)
		}
	}
	if !hasGap(res, "SLOWLOG is a ring buffer of fixed length; entries older than the oldest one in this file may have been overwritten") {
		t.Errorf("ring buffer gap missing: %q", res.Gaps)
	}
}

func TestSlowlogThresholdWording(t *testing.T) {
	q := evidence.SourceQuality{Threshold: "commands slower than 10000 microseconds",
		CoverageFirst: tp(hms(0, 30, 0)), CoverageLast: tp(hms(1, 5, 0)), TimezoneRule: clock.RuleUTCField}
	res := slowlogCase(t, q,
		span("redis-slowlog", "slow-command", "redis-a", hms(0, 30, 0), 20, "id", "1"),
		span("redis-slowlog", "slow-command", "redis-a", hms(1, 5, 0).Add(-20*time.Millisecond), 20, "id", "2"))
	want := "Not observed in Redis SLOWLOG. This source only records commands slower than 10000 µs (execution time only); shorter waits would not appear."
	if got := absenceRow(t, res).Wording; got != want {
		t.Errorf("absence = %q\nwant      %q", got, want)
	}
}

func TestSlowlogUnknownThresholdAndIDGap(t *testing.T) {
	q := evidence.SourceQuality{Threshold: "unknown", CoverageFirst: tp(hms(0, 30, 0)), CoverageLast: tp(hms(1, 5, 0))}
	res := slowlogCase(t, q, span("redis-slowlog", "slow-command", "redis-a", hms(0, 30, 0), 20, "id", "1"))
	if got, want := absenceRow(t, res).Wording, "Not observed (gap or sampling: slowlog-log-slower-than not stated for redis-a)."; got != want {
		t.Errorf("absence = %q, want %q", got, want)
	}

	q.Threshold = "commands slower than 10000 microseconds"
	res = slowlogCase(t, q,
		span("redis-slowlog", "slow-command", "redis-a", hms(0, 30, 0), 20, "id", "1"),
		span("redis-slowlog", "slow-command", "redis-a", hms(1, 4, 0), 20, "id", "5"))
	if got, want := absenceRow(t, res).Wording, "Not observed (gap or sampling: the Redis slow-log buffer on redis-a has a gap in its entry ids, so entries may be missing)."; got != want {
		t.Errorf("absence = %q, want %q", got, want)
	}
}

// memoryFixture is private bytes of w3wp on web-a rising from 1 GiB to 2 GiB
// over an hour, one sample every 5 min.
func memoryFixture(h *harness, recycle bool) {
	i := 0
	every(hms(0, 0, 0), hms(1, 0, 0), 5*time.Minute, func(t time.Time) {
		v := float64(1<<30) + float64(i)*float64(1<<30)/12
		h.add(metric("win-procsnap", "web-a", t, v, "bytes", "metric", "private-bytes", "process", "w3wp", "pid", "1240"))
		i++
	})
	h.meta["win-procsnap"] = meta{host: "web-a"}
	if recycle {
		h.add(point("win-events", "apppool-recycle", "web-a", hms(0, 32, 0), "reason", "Memory"))
		h.meta["win-events"] = meta{host: "web-a"}
	}
}

func TestMemoryRiseSplitByRecycle(t *testing.T) {
	h := newHarness(t)
	memoryFixture(h, true)
	res := h.eval(hms(0, 0, 0), hms(1, 0, 0))
	if w := wordings(res, "RC2"); len(w) != 0 {
		t.Errorf("RC2 rows across a recycle: %q", w)
	}
	for _, g := range []string{
		"Not observed (gap or sampling: web-a w3wp:1240 private bytes: the segment from 2001-10-25T00:00:00Z to 2001-10-25T00:30:00Z has 7 samples over 30 min; a rise is tested on 12 or more samples over 30 min or more, with 80 % of the samples expected).",
		"Not observed (gap or sampling: web-a w3wp:1240 private bytes: the segment from 2001-10-25T00:35:00Z to 2001-10-25T01:00:00Z has 6 samples over 25 min; a rise is tested on 12 or more samples over 30 min or more, with 80 % of the samples expected).",
	} {
		if !hasGap(res, g) {
			t.Errorf("gap %q missing: %q", g, res.Gaps)
		}
	}
	for _, s := range res.Hosts {
		for _, line := range s.Summary {
			if strings.Contains(line, "rose by") {
				t.Errorf("summary claims a rise: %q", line)
			}
		}
	}
}

func TestMemoryRiseWithoutRecycle(t *testing.T) {
	h := newHarness(t)
	memoryFixture(h, false)
	res := h.eval(hms(0, 0, 0), hms(1, 0, 0))
	r := row(t, res, "RC2", "web-a w3wp:1240: private bytes rose by 1 GiB over 1 h (from 1 GiB to 2 GiB).")
	if r.Label != audit.EvidenceSymptom || !strings.Contains(r.Title, "Theil-Sen slope 1 GiB/h") {
		t.Errorf("row = %+v", r)
	}
	// RC1 lists the process by peak private bytes.
	row(t, res, "RC1", "Highest peak private bytes on web-a in this window: w3wp:1240 (2 GiB).")
}

func TestProcessesRankedWithSeriesSelectors(t *testing.T) {
	h := newHarness(t)
	memoryFixture(h, false)
	every(hms(0, 0, 0), hms(1, 0, 0), 5*time.Minute, func(tm time.Time) {
		h.add(pdh("web-a", tm, "Process V2", "w3wp:1240", "% Processor Time", 30, "percent-host"))
		h.add(pdh("web-a", tm, "Process V2", "w3wp:1240", "Working Set", 1e9, ""))
		h.add(pdh("web-a", tm, "Process V2", "dotnet:3000", "% Processor Time", 50, "percent-host"))
		// A legacy instance: its pid comes from ID Process, so no selector names it.
		h.add(pdh("web-a", tm, "Process", "job-alpha#1", "ID Process", 2000, ""))
		h.add(pdh("web-a", tm, "Process", "job-alpha#1", "% Processor Time", 70, "percent-host"))
	})
	h.meta["win-counters"] = meta{host: "web-a"}
	res := h.eval(hms(0, 0, 0), hms(1, 0, 0))
	var got []rules.Process
	for _, hs := range res.Hosts {
		if hs.Host == "web-a" {
			got = hs.Processes
		}
	}
	want := []rules.Process{
		{Name: "dotnet:3000", Sel: map[string]string{
			"proc.cpu": `kind=counter;source=win-counters;host=web-a;attr.object=Process V2;attr.instance=dotnet:3000;attr.counter=% Processor Time;field=value`,
		}},
		{Name: "w3wp:1240", Sel: map[string]string{
			"proc.cpu":     `kind=counter;source=win-counters;host=web-a;attr.object=Process V2;attr.instance=w3wp:1240;attr.counter=% Processor Time;field=value`,
			"proc.ws":      `kind=counter;source=win-counters;host=web-a;attr.object=Process V2;attr.instance=w3wp:1240;attr.counter=Working Set;field=value`,
			"proc.private": "kind=metric;source=win-procsnap;host=web-a;attr.metric=private-bytes;attr.pid=1240;field=value",
		}},
	}
	if len(got) != len(want) {
		t.Fatalf("processes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Name != want[i].Name || len(got[i].Sel) != len(want[i].Sel) {
			t.Errorf("process %d = %+v, want %+v", i, got[i], want[i])
			continue
		}
		for k, v := range want[i].Sel {
			if got[i].Sel[k] != v {
				t.Errorf("process %s sel %s = %q, want %q", want[i].Name, k, got[i].Sel[k], v)
			}
		}
	}
}

func TestLowCoverageIsAGapNotANegative(t *testing.T) {
	h := newHarness(t)
	// Host CPU on web-a only from 01:00 to 01:05 in a 40 min window.
	every(hms(1, 0, 0), hms(1, 5, 0), 15*time.Second, func(t time.Time) {
		h.add(pdh("web-a", t, "Processor", "_Total", "% Processor Time", 20, "percent-host"))
	})
	h.meta["win-counters"] = meta{host: "web-a"}
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)
	if !hasGap(res, "Not observed (gap or sampling: 13 % of samples present for host.cpu on web-a).") {
		t.Errorf("coverage gap missing: %q", res.Gaps)
	}
	for _, w := range wordings(res, "RC3") {
		if strings.Contains(w, "stall episodes") {
			t.Errorf("episode share from a low-coverage series: %q", w)
		}
	}
}

func TestMongoAbsenceAndQueue(t *testing.T) {
	h := newHarness(t)
	every(hms(0, 40, 0), hms(1, 20, 0), 15*time.Second, func(t time.Time) {
		v := 0.0
		if t == hms(1, 2, 15) {
			v = 9
		}
		h.add(metric("mongo-serverstatus", "mongo-a", t, v, "count", "metric", "globalLock.currentQueue.total"))
	})
	h.add(span("mongod-json", "slow-op", "mongo-a", hms(0, 50, 0), 300))
	h.meta["mongo-serverstatus"] = meta{host: "mongo-a"}
	h.meta["mongod-json"] = meta{host: "mongo-a", quality: evidence.SourceQuality{SourceVersion: "7.0.2",
		Threshold: "operations slower than 100 ms", TimezoneRule: clock.RuleOffsetInField,
		CoverageFirst: tp(hms(0, 30, 0)), CoverageLast: tp(hms(1, 30, 0))}}
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0), stall)
	row(t, res, "RC9", "A MongoDB serverStatus sample showed MongoDB queue length at 9 during this interval (Correlation).")
	row(t, res, "RC9", "Not observed in mongod.log. This source only records operations slower than 100 ms; shorter waits would not appear.")
}

func TestNoStallsStillSummarises(t *testing.T) {
	h := newHarness(t)
	stallFixture(h)
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0))
	if len(res.Episodes) != 0 {
		t.Errorf("episodes = %+v", res.Episodes)
	}
	for _, r := range res.Rules {
		if r.Episode != nil || strings.Contains(r.Wording, "stall episodes") {
			t.Errorf("row tied to a stall without stalls: %+v", r)
		}
	}
	if len(res.Hosts) == 0 {
		t.Error("no host summaries")
	}
}

func TestStallOutsideWindowIsDropped(t *testing.T) {
	h := newHarness(t)
	stallFixture(h)
	late := join.Interval{ID: "late", Start: hms(3, 0, 0), End: hms(3, 0, 10)}
	res := h.eval(hms(0, 40, 0), hms(1, 20, 0), stall, late)
	if len(res.Episodes) != 1 || res.Episodes[0].IDs[0] != "ep-0" {
		t.Errorf("episodes = %+v", res.Episodes)
	}
}

func TestErasedCase(t *testing.T) {
	ctx := context.Background()
	store := casestore.NewMemory()
	info, err := store.Create(ctx, "gone")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := store.Erase(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Evaluate(ctx, c, importer.Window{From: hms(0, 0, 0), To: hms(1, 0, 0)}, nil, rules.DefaultThresholds()); err == nil {
		t.Error("evaluate on an erased case gave no error")
	}
}
