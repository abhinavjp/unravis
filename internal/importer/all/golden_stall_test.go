package all_test

// Golden stall test (wave 2 plan section 6, INT2). One synthetic stall,
// 2001-10-25T01:02:31Z (14:02:31 local on web-a, Pacific/Auckland, +13:00), is
// written into seven fixtures in seven different time forms:
//
//	log4net-text     host-local, no zone in the file
//	serilog-clef     UTC ("Z")
//	redis-slowlog    Unix epoch
//	win-counters     PDH header bias -780
//	dotnet-counters  host-local, culture-formatted
//	win-procsnap     UTC (SampleUtc)
//	sql-errorlog     host-local on sql-a
//
// Each file is parsed by its real importer. A source is aligned when an event
// that marks the stall (or, for a sampled source, any sample) lies within the
// source's own tolerance of the stall, with the host label the case expects.
// The sources that state a duration also go through the case timeline against
// an audit interval around the stall, and each must give an overlap row.
//
// Four mutations must each make that check fail: a +13 h shift on one source,
// a dropped host, a swapped time zone rule, and a truncated file. Each runs as
// a sub-test that asserts the mutated variant is reported as not aligned.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
	"api-audit-explorer/internal/join"
	"api-audit-explorer/internal/timeline"
)

// stall is the shared stall instant.
var stall = time.Date(2001, 10, 25, 1, 2, 31, 0, time.UTC)

// nzdt is Pacific/Auckland in October 2001 (daylight time, +13:00).
var nzdt = time.FixedZone("NZDT", 13*3600)

var (
	hostWeb   = &importer.HostHint{Label: "web-a", Role: "app", TZ: nzdt, LogicalCores: 4, Runtime: "iis-10"}
	hostSQL   = &importer.HostHint{Label: "sql-a", Role: "sql", TZ: nzdt, Runtime: "sql-2019"}
	hostRedis = &importer.HostHint{Label: "redis-a", Role: "app", TZ: time.UTC}
	utcHost   = func(h *importer.HostHint) *importer.HostHint { c := *h; c.TZ = time.UTC; return &c }
)

// gsrc is one fixture, the importer that reads it and how to tell it aligned.
type gsrc struct {
	name     string
	file     string
	body     func(shift time.Duration) []byte
	shift    time.Duration // added to every time the file states
	host     *importer.HostHint
	supplied map[string]string
	mark     func(evidence.Event) bool // the event that marks the stall; nil: any event
	tol      time.Duration             // distance from the stall the marker may lie
	overlap  bool                      // states a duration, so the timeline lists it
	trim     func([]byte) []byte       // overrides the default truncation
}

func (s gsrc) data() []byte { return s.body(s.shift) }

// ---- fixture builders ------------------------------------------------------------

// ticks calls f for each step-spaced time from stall-before to stall+after.
func ticks(before, after, step time.Duration, f func(t time.Time)) {
	for d := -before; d <= after; d += step {
		f(stall.Truncate(step).Add(d))
	}
}

func log4netBody(shift time.Duration) []byte {
	var b strings.Builder
	const l = "2006-01-02 15:04:05,000"
	ticks(180*time.Second, 60*time.Second, 10*time.Second, func(t time.Time) {
		if t.After(stall) && !t.After(stall.Add(5*time.Second)) {
			return
		}
		if !t.After(stall) && t.After(stall.Add(-10*time.Second)) {
			// the stall line goes here, in time order
			fmt.Fprintf(&b, "%s [17] WARN  StackExchange.Redis.ConnectionMultiplexer - Timeout performing GET (5000ms), next: GET key-1, inst: 0, qu: 0, qs: 41, in: 8192, serverEndpoint: redis-a.example.test:6379, v: 2.6.122.38350\n",
				stall.Add(123*time.Millisecond).Add(shift).In(nzdt).Format(l))
		}
		fmt.Fprintf(&b, "%s [12] INFO  Synthetic.Api.OrdersController - Request handled\n", t.Add(shift).In(nzdt).Format(l))
	})
	return []byte(b.String())
}

func clefBody(shift time.Duration) []byte {
	var b strings.Builder
	const l = "2006-01-02T15:04:05.0000000Z"
	line := func(t time.Time, elapsed string) {
		fmt.Fprintf(&b, `{"@t":"%s","@mt":"HTTP {RequestMethod} {RequestPath} responded {StatusCode} in {Elapsed:0.0000} ms","RequestMethod":"GET","RequestPath":"/orders/o-1","StatusCode":200,"Elapsed":%s,"SourceContext":"Serilog.AspNetCore.RequestLoggingMiddleware"}`+"\n",
			t.Add(shift).UTC().Format(l), elapsed)
	}
	ticks(180*time.Second, 60*time.Second, 10*time.Second, func(t time.Time) {
		if t.After(stall.Add(-10*time.Second)) && !t.After(stall) {
			line(stall.Add(-50*time.Millisecond), "1834.2")
		}
		line(t, "20.0")
	})
	return []byte(b.String())
}

func slowlogBody(shift time.Duration) []byte {
	var b strings.Builder
	entry := func(id int, t time.Time, micros int) {
		fmt.Fprintf(&b, "%d) 1) (integer) %d\n   2) (integer) %d\n   3) (integer) %d\n   4) 1) \"GET\"\n      2) \"key-1\"\n   5) \"192.0.2.10:52144\"\n   6) \"\"\n",
			18-id, id, t.Add(shift).Unix(), micros)
	}
	entry(17, stall.Add(40*time.Second), 20000)
	entry(16, stall.Add(20*time.Second), 12000)
	entry(15, stall, 1500000)
	return []byte(b.String())
}

func pdhBody(shift time.Duration) []byte {
	var b strings.Builder
	b.WriteString(`"(PDH-CSV 4.0) (New Zealand Daylight Time)(-780)","\\WEB-A\Processor(_Total)\% Processor Time","\\WEB-A\Memory\Available MBytes"` + "\n")
	ticks(180*time.Second, 60*time.Second, 15*time.Second, func(t time.Time) {
		cpu := 12.5
		if d := t.Sub(stall); d >= -time.Second && d <= 30*time.Second {
			cpu = 96.5
		}
		fmt.Fprintf(&b, "\"%s\",\"%.2f\",\"2048\"\n", t.Add(shift).In(nzdt).Format("01/02/2006 15:04:05.000"), cpu)
	})
	return []byte(b.String())
}

func dotnetBody(shift time.Duration) []byte {
	var b strings.Builder
	b.WriteString("Timestamp,Provider,Counter Name,Counter Type,Mean/Increment\n")
	ticks(180*time.Second, 60*time.Second, 10*time.Second, func(t time.Time) {
		ts := t.Add(shift).In(nzdt).Format("01/02/2006 3:04:05 PM")
		fmt.Fprintf(&b, "%s,System.Runtime,CPU Usage (%%),Metric,12.5\n", ts)
		fmt.Fprintf(&b, "%s,System.Runtime,ThreadPool Queue Length,Metric,3\n", ts)
	})
	return []byte(b.String())
}

func procsnapBody(shift time.Duration) []byte {
	var b strings.Builder
	b.WriteString("#TYPE Selected.System.Diagnostics.Process\n")
	b.WriteString(`"SampleUtc","ProcessName","Id","CPU","WorkingSet64","PrivateMemorySize64","HandleCount","Threads","StartTimeUtc"` + "\n")
	ticks(180*time.Second, 60*time.Second, 15*time.Second, func(t time.Time) {
		ts := t.Add(shift).UTC().Format("2006-01-02T15:04:05.0000000Z")
		fmt.Fprintf(&b, "\"%s\",\"w3wp\",\"1240\",\"120.5\",\"1932735283\",\"1500000000\",\"950\",\"42\",\"2001-10-25T00:00:00.0000000Z\"\n", ts)
		fmt.Fprintf(&b, "\"%s\",\"sqlservr\",\"4242\",\"5000.25\",\"8589934592\",\"8000000000\",\"3200\",\"210\",\"2001-10-24T06:30:00.0000000Z\"\n", ts)
	})
	return []byte(b.String())
}

func errorlogBody(shift time.Duration) []byte {
	var b strings.Builder
	const l = "2006-01-02 15:04:05.00"
	at := func(t time.Time) string { return t.Add(shift).In(nzdt).Format(l) }
	start := stall.Add(-3 * time.Hour)
	fmt.Fprintf(&b, "%s Server      Microsoft SQL Server 2019 (RTM) - 15.0.2000.5 (X64)\n", at(start))
	b.WriteString("\tSynthetic build line for tests\n")
	fmt.Fprintf(&b, "%s Server      UTC adjustment: 13:00\n", at(start.Add(20*time.Millisecond)))
	fmt.Fprintf(&b, "%s Server      SQL Server is starting at normal priority base (=7). This is an informational message only.\n", at(start.Add(50*time.Millisecond)))
	ticks(180*time.Second, 60*time.Second, 30*time.Second, func(t time.Time) {
		if t.After(stall.Add(-30*time.Second)) && !t.After(stall) {
			fmt.Fprintf(&b, "%s spid8s      SQL Server has encountered 3 occurrence(s) of I/O requests taking longer than 15 seconds to complete on file [D:\\synthetic\\db-alpha.mdf] in database id 5.  The duration of the long I/O is: 16021 ms.\n", at(stall))
		}
		fmt.Fprintf(&b, "%s spid9s      Starting up database 'db-alpha'.\n", at(t))
	})
	return []byte(b.String())
}

func isKind(kind string) func(evidence.Event) bool {
	return func(e evidence.Event) bool { return e.Kind == kind }
}

func goldenSources() []gsrc {
	return []gsrc{
		{name: "log4net-text", file: "app.log", body: log4netBody, host: hostWeb, tol: time.Second,
			mark: func(e evidence.Event) bool { return e.Kind == "log-entry" && e.Attrs["family"] == "redis-timeout" }},
		{name: "serilog-clef", file: "app.clef", body: clefBody, host: hostWeb, tol: time.Second,
			mark: isKind("app-request"), overlap: true},
		{name: "redis-slowlog", file: "slowlog.txt", body: slowlogBody, host: hostRedis, tol: time.Second,
			mark: isKind("slow-command"), overlap: true},
		{name: "win-counters", file: "web-a-perf.csv", body: pdhBody, host: hostWeb, tol: 15 * time.Second},
		{name: "dotnet-counters", file: "web-a-counters.csv", body: dotnetBody, host: hostWeb, tol: 10 * time.Second,
			supplied: map[string]string{"pid": "1240"}},
		{name: "win-procsnap", file: "web-a-procs.csv", body: procsnapBody, host: hostWeb, tol: 15 * time.Second},
		{name: "sql-errorlog", file: "ERRORLOG", body: errorlogBody, host: hostSQL, tol: time.Second,
			mark: isKind("io-stall")},
	}
}

// ---- alignment ---------------------------------------------------------------------

// auditWindow is the stalled audit interval the timeline is asked about.
var auditWindow = join.Interval{ID: "audit-stall", Start: stall.Add(-2 * time.Second), End: stall.Add(2 * time.Second)}

// aligned parses every source with its own importer, loads the events into one
// case, and returns nil only when every source places the stall where the
// others do. The error names the first source that does not.
func aligned(srcs []gsrc) error {
	ctx := context.Background()
	reg := all.Registry()
	st := casestore.NewMemory()
	info, err := st.Create(ctx, "golden stall")
	if err != nil {
		return err
	}
	c, err := st.Open(ctx, info.ID)
	if err != nil {
		return err
	}
	defer c.Close()
	cid := string(c.Info().ID)

	for i, s := range srcs {
		imp, ok := reg.ByName(s.name)
		if !ok {
			return fmt.Errorf("%s: not registered", s.name)
		}
		opts := importer.Options{
			CaseID: cid, ImportNonce: fmt.Sprintf("golden-%d", i), FileName: s.file,
			Host: s.host, Supplied: s.supplied,
			Pseudonymize: func(kind, v string) string { return kind + "-0000" },
		}
		if s.host != nil {
			h := *s.host
			opts.Hosts = func(string) (importer.HostHint, bool) { return h, true }
		}
		events, rep, err := importer.Collect(ctx, imp, strings.NewReader(string(s.data())), opts)
		if err != nil {
			return fmt.Errorf("%s: import refused: %s", s.name, errKind(err))
		}
		if !markerNear(events, s, opts.Host) {
			return fmt.Errorf("%s: no event within %s of the stall on the expected host", s.name, s.tol)
		}
		run := func(_ io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
			for _, e := range events {
				if err := emit(e); err != nil {
					return rep, err
				}
			}
			return rep, nil
		}
		label := ""
		if s.host != nil {
			label = s.host.Label
		}
		meta := casestore.ImportMeta{
			Importer: s.name, FileName: s.file, HostLabel: label, AckGating: true, AckText: "golden test",
			AllowedAttrs: imp.AllowedAttrs(),
		}
		if _, err := c.Import(ctx, meta, strings.NewReader(string(s.data())), run); err != nil {
			return fmt.Errorf("%s: case import: %s", s.name, errKind(err))
		}
	}

	v, err := timeline.Build(ctx, c, importer.Window{}, []join.Interval{auditWindow})
	if err != nil {
		return err
	}
	if !v.Enabled {
		return fmt.Errorf("timeline disabled: %s", v.ReasonCode)
	}
	for _, s := range srcs {
		if !s.overlap {
			continue
		}
		found := false
		for _, r := range v.Rows {
			if r.Kind == timeline.KindOverlap && r.Source == s.name {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: the timeline lists no event overlapping the stalled interval", s.name)
		}
	}
	return nil
}

// markerNear reports whether a marking event (any event when mark is nil) lies
// within s.tol of the stall and carries the expected host label.
func markerNear(events []evidence.Event, s gsrc, host *importer.HostHint) bool {
	for _, e := range events {
		if s.mark != nil && !s.mark(e) {
			continue
		}
		if host != nil && e.Host != host.Label {
			continue
		}
		if host == nil && e.Host != "" {
			continue
		}
		if !e.End.Before(stall.Add(-s.tol)) && !e.Start.After(stall.Add(s.tol)) {
			return true
		}
	}
	return false
}

// errKind names an error without echoing anything from a file.
func errKind(err error) string {
	var q *importer.QuarantineError
	if errors.As(err, &q) {
		return "quarantine:" + string(q.Reason)
	}
	var l *importer.LimitError
	if errors.As(err, &l) {
		return "limit"
	}
	return err.Error()
}

// ---- mutations ---------------------------------------------------------------------

func withSource(srcs []gsrc, i int, f func(*gsrc)) []gsrc {
	out := append([]gsrc(nil), srcs...)
	f(&out[i])
	return out
}

// truncate keeps the first half of the lines, as a copy cut short would.
func truncate(b []byte) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	return []byte(strings.Join(lines[:len(lines)/2], ""))
}

func TestGoldenStall(t *testing.T) {
	srcs := goldenSources()

	t.Run("aligned", func(t *testing.T) {
		if err := aligned(srcs); err != nil {
			t.Fatalf("the unmodified fixtures must align: %v", err)
		}
	})

	t.Run("shift-13h", func(t *testing.T) {
		for i, s := range srcs {
			t.Run(s.name, func(t *testing.T) {
				m := withSource(srcs, i, func(g *gsrc) { g.shift = 13 * time.Hour })
				if err := aligned(m); err != nil {
					t.Logf("rejected: %v", err)
				} else {
					t.Fatal("a source shifted by +13 h was still reported as aligned")
				}
			})
		}
	})

	t.Run("dropped-host", func(t *testing.T) {
		for i, s := range srcs {
			t.Run(s.name, func(t *testing.T) {
				m := withSource(srcs, i, func(g *gsrc) { g.host = nil })
				if err := aligned(m); err != nil {
					t.Logf("rejected: %v", err)
				} else {
					t.Fatal("a source with no host was still reported as aligned")
				}
			})
		}
	})

	t.Run("swapped-zone-rule", func(t *testing.T) {
		swaps := map[string]func(*gsrc){
			// The owner states the zone-less times are UTC, but they are local.
			"log4net-text": func(g *gsrc) { g.supplied = map[string]string{"log-time": "utc"} },
			// The host zone is taken as UTC instead of Pacific/Auckland.
			"dotnet-counters": func(g *gsrc) { g.host = utcHost(hostWeb) },
			"sql-errorlog":    func(g *gsrc) { g.host = utcHost(hostSQL) },
			// The PDH header bias is read as zero.
			"win-counters": func(g *gsrc) {
				body := g.body
				g.body = func(shift time.Duration) []byte {
					return []byte(strings.Replace(string(body(shift)), "(-780)", "(0)", 1))
				}
			},
		}
		for i, s := range srcs {
			swap, ok := swaps[s.name]
			if !ok {
				continue // the rule of a UTC or epoch source cannot be swapped
			}
			t.Run(s.name, func(t *testing.T) {
				if err := aligned(withSource(srcs, i, swap)); err != nil {
					t.Logf("rejected: %v", err)
				} else {
					t.Fatal("a source read under the wrong time zone rule was still reported as aligned")
				}
			})
		}
	})

	t.Run("truncated-file", func(t *testing.T) {
		for i, s := range srcs {
			t.Run(s.name, func(t *testing.T) {
				m := withSource(srcs, i, func(g *gsrc) {
					body := g.body
					g.body = func(shift time.Duration) []byte { return truncate(body(shift)) }
				})
				if err := aligned(m); err != nil {
					t.Logf("rejected: %v", err)
				} else {
					t.Fatal("a truncated file was still reported as aligned")
				}
			})
		}
	})
}

// A source that is aligned reports the stall at the same UTC instant whatever
// its time form. This pins the rule each fixture is read under.
func TestGoldenStallRules(t *testing.T) {
	want := map[string]string{
		"log4net-text": clock.RuleHostLocal, "serilog-clef": clock.RuleUTCField,
		"redis-slowlog": clock.RuleUTCField, "win-counters": clock.RuleHeaderBias,
		"dotnet-counters": clock.RuleHostLocal, "win-procsnap": clock.RuleUTCField, "sql-errorlog": clock.RuleHostLocal,
	}
	reg := all.Registry()
	for _, s := range goldenSources() {
		t.Run(s.name, func(t *testing.T) {
			imp, _ := reg.ByName(s.name)
			opts := importer.Options{CaseID: "goldencase0000000000000a", ImportNonce: "n", FileName: s.file, Host: s.host, Supplied: s.supplied,
				Pseudonymize: func(kind, v string) string { return kind + "-0000" }}
			events, _, err := importer.Collect(context.Background(), imp, strings.NewReader(string(s.data())), opts)
			if err != nil {
				t.Fatal(errKind(err))
			}
			got := map[string]bool{}
			for _, e := range events {
				got[e.TZRule] = true
			}
			if !got[want[s.name]] {
				t.Errorf("events carry rules %v, want %s", got, want[s.name])
			}
		})
	}
}
