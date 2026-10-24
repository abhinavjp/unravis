package redis

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// logGood is a synthetic redis.log: a save, a slow fsync, a closed client, a stop and a
// start, then one line in the year-less form.
const logGood = `1234:M 25 Oct 2001 14:02:30.120 * Background saving started by pid 1240
1240:C 25 Oct 2001 14:02:31.900 * DB saved on disk
1234:M 25 Oct 2001 14:02:32.004 * Background saving terminated with success
1234:M 25 Oct 2001 14:02:33.500 * Asynchronous AOF fsync is taking too long (disk is busy?). Writing the AOF buffer without waiting for fsync to complete, this may slow down Redis.
1234:M 25 Oct 2001 14:02:40.000 # Client id=91 addr=192.0.2.10:52144 name=web-a-conn closed for overcoming of output buffer limits.
1234:M 25 Oct 2001 14:05:00.000 # User requested shutdown...
1300:M 25 Oct 2001 14:05:03.100 * Ready to accept connections tcp
[1234] 25 Oct 14:02:30.120 * Background saving started by pid 1240
`

// logBad has the sentinel in a client name on a line with an impossible day (rejected), on a
// valid line (dropped by the parser) and on a line that is not a log line.
const logBad = `1234:M 25 Oct 2001 14:02:30.120 * Background saving started by pid 1240
1234:M 99 Oct 2001 14:02:40.000 # Client id=91 addr=192.0.2.10:52144 name=SENTINEL-7f3a closed for overcoming of output buffer limits.
1234:M 25 Oct 2001 14:02:41.000 # Client id=92 addr=198.51.100.7:52150 name=SENTINEL-7f3a closed for overcoming of output buffer limits.
SENTINEL-7f3a is not a log line
`

// logNew is logGood without the year-less line.
var logNew = logGood[:strings.LastIndex(strings.TrimSuffix(logGood, "\n"), "\n")+1]

func logLarge() []byte {
	var b strings.Builder
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&b, "%d:M 01 Jun 2001 %02d:%02d:%02d.%03d * Asynchronous AOF fsync is taking too long (disk is busy?).\n",
			2000+i, i/3600, i/60%60, i%60, i%1000)
	}
	return []byte(b.String())
}

func logZone(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func logOpts(t testing.TB) importer.Options {
	o := hostOpts()
	o.Host.TZ = logZone(t)
	o.Supplied = map[string]string{logYearKey: "2001"}
	return o
}

func logFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: []byte(logGood), Bad: []byte(logBad), Large: logLarge(),
		NoFreeText: true, Options: logOpts(t),
	}
}

func TestConformanceLog(t *testing.T) { importertest.Run(t, NewLog(), logFixtures(t)) }

func FuzzLog(f *testing.F) { importertest.FuzzParse(f, NewLog(), logFixtures(f)) }

func TestLogGoodEvents(t *testing.T) {
	evs, rep := mustCollect(t, NewLog(), logGood, logOpts(t))
	type row struct{ kind, role, family, sev string }
	want := []row{
		{KindBgsaveStart, "master", "rdb", "info"},
		{KindBgsaveDone, "child", "rdb", "info"}, // the parent's line for the same save is merged
		{KindAOFFsyncSlow, "master", "aof", "info"},
		{KindClientBufferClosed, "master", "output-buffer-limit", "warning"},
		{KindRedisStop, "master", "other", "warning"},
		{KindRedisStart, "master", "other", "info"},
		{KindBgsaveStart, "", "rdb", "info"}, // year-less line: no role
	}
	if len(evs) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(evs), len(want), rep)
	}
	for i, w := range want {
		e := evs[i]
		got := row{e.Kind, e.Attrs[attrRole], e.Attrs[attrFamily], e.Severity}
		if got != w {
			t.Errorf("event %d: %+v want %+v", i, got, w)
		}
		if e.Source != NameLog || e.Host != "redis-a" || e.TZRule != clock.RuleHostLocal || !e.Start.Equal(e.End) {
			t.Errorf("event %d shape: %+v", i, e)
		}
	}
	// 14:02:30.120 on 25 Oct 2001 is EDT (UTC-4).
	if got := evs[0].Start.Format(time.RFC3339Nano); got != "2001-10-25T18:02:30.12Z" {
		t.Errorf("first time %s", got)
	}
	if got := evs[6].Start.Format(time.RFC3339Nano); got != "2001-10-25T18:02:30.12Z" {
		t.Errorf("year-less time %s", got)
	}
	if rep.Rows != 8 || rep.Emitted != 7 || rep.Rejected != 0 {
		t.Errorf("report %+v", rep)
	}
	if rep.FieldRules["timestamp"] != clock.RuleHostLocal || rep.Quality.TimezoneRule != clock.RuleHostLocal {
		t.Errorf("rules %v %q", rep.FieldRules, rep.Quality.TimezoneRule)
	}
	gaps := strings.Join(rep.KnownGaps, "|")
	for _, g := range []string{gapLogLocal, gapLogLevel, gapLogNoYear, gapLogMerged} {
		if !strings.Contains(gaps, g) {
			t.Errorf("missing gap %q", g)
		}
	}
	if strings.Contains(gaps, gapLogUnmatched) || strings.Contains(gaps, gapLogRolled) {
		t.Errorf("unexpected gap in %q", gaps)
	}
}

func TestLogGapsPassLint(t *testing.T) {
	for _, g := range []string{gapLogLocal, gapLogLevel, gapLogKinds, gapLogUnmatched, gapLogNoYear, gapLogRolled, gapLogMerged} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

func TestLogRefusals(t *testing.T) {
	// No time zone on the host.
	o := logOpts(t)
	o.Host.TZ = nil
	_, _, err := collect(t, NewLog(), logGood, o)
	var q *importer.QuarantineError
	if !errors.Is(err, importer.ErrTimezoneUnlabelled) || !errors.As(err, &q) || q.Reason != importer.ReasonTZUnlabelled {
		t.Errorf("no TZ: %v", err)
	}
	// No host at all.
	o = logOpts(t)
	o.Host = nil
	if _, _, err = collect(t, NewLog(), logGood, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("no host: %v", err)
	}
	// A year-less line with no owner year.
	o = logOpts(t)
	o.Supplied = nil
	_, _, err = collect(t, NewLog(), logGood, o)
	if !errors.Is(err, ErrYearNotStated) || !errors.Is(err, importer.ErrQuarantine) {
		t.Errorf("no year: %v", err)
	}
	// The same file without the year-less line needs no year.
	if _, rep, err := collect(t, NewLog(), strings.Join(strings.Split(logGood, "\n")[:7], "\n")+"\n", o); err != nil || rep.Emitted != 6 {
		t.Errorf("no year, new form only: %v %+v", err, rep)
	}
	// An out-of-range year setting is the same as none.
	o = logOpts(t)
	o.Supplied = map[string]string{logYearKey: "1850"}
	if _, _, err = collect(t, NewLog(), logGood, o); !errors.Is(err, ErrYearNotStated) {
		t.Errorf("bad year: %v", err)
	}
	// Text with no log line at all is not a redis log.
	_, _, err = collect(t, NewLog(), "hello\nworld\n", logOpts(t))
	if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat {
		t.Errorf("not a log: %v", err)
	}
}

func TestLogUnmatchedCounted(t *testing.T) {
	in := `1234:M 25 Oct 2001 14:02:30.120 * Background saving started by pid 1240
                _._
           _.-` + "``" + `__ ''-._
1234:M 25 Oct 2001 14:02:31.000 * 100 changes in 300 seconds. Saving...
1234:M 25 Oct 2001 14:02:32.000 * Some message we do not know
`
	evs, rep := mustCollect(t, NewLog(), in, logOpts(t))
	if len(evs) != 1 || rep.Rows != 5 || rep.Emitted != 1 || rep.Rejected != 0 {
		t.Fatalf("%d events %+v", len(evs), rep)
	}
	if !strings.Contains(strings.Join(rep.KnownGaps, "|"), gapLogUnmatched) {
		t.Errorf("no unmatched gap: %v", rep.KnownGaps)
	}
	// A file of routine lines only is still a log.
	evs, rep = mustCollect(t, NewLog(), "1234:M 25 Oct 2001 14:02:32.000 * Some message we do not know\n", logOpts(t))
	if len(evs) != 0 || rep.Rows != 1 {
		t.Errorf("%d events %+v", len(evs), rep)
	}
}

func TestLogRoles(t *testing.T) {
	var b strings.Builder
	for i, r := range "MSCXQ" {
		fmt.Fprintf(&b, "%d:%c 25 Oct 2001 14:02:%02d.000 * Background saving started by pid 9\n", 100+i, r, i)
	}
	evs, _ := mustCollect(t, NewLog(), b.String(), logOpts(t))
	var got []string
	for _, e := range evs {
		got = append(got, e.Attrs[attrRole])
	}
	if strings.Join(got, ",") != "master,replica,child,sentinel," {
		t.Errorf("roles %q", got)
	}
}

func TestLogYearlessRollover(t *testing.T) {
	in := `[10] 30 Dec 23:59:58.000 * Background saving started by pid 11
[10] 31 Dec 23:59:59.500 * Background saving started by pid 12
[10] 01 Jan 00:00:01.250 * Background saving started by pid 13
[10] 31 Dec 23:59:59.000 * Background saving started by pid 14
`
	o := logOpts(t)
	o.Host.TZ = time.UTC
	o.Supplied = map[string]string{logYearKey: "2001"}
	evs, rep := mustCollect(t, NewLog(), in, o)
	if len(evs) != 4 {
		t.Fatalf("%d events %+v", len(evs), rep)
	}
	want := []string{"2001-12-30T23:59:58Z", "2001-12-31T23:59:59.5Z", "2002-01-01T00:00:01.25Z", "2002-12-31T23:59:59Z"}
	for i, w := range want {
		if got := evs[i].Start.Format(time.RFC3339Nano); got != w {
			t.Errorf("event %d: %s want %s", i, got, w)
		}
	}
	if !strings.Contains(strings.Join(rep.KnownGaps, "|"), gapLogRolled) {
		t.Errorf("no rollover gap: %v", rep.KnownGaps)
	}
	// A line a few minutes back inside the same month does not move the year.
	in = "[10] 05 Mar 10:00:00.000 * Background saving started by pid 1\n[10] 05 Mar 09:58:00.000 * Background saving started by pid 2\n"
	evs, rep = mustCollect(t, NewLog(), in, o)
	if len(evs) != 2 || evs[1].Start.Year() != 2001 || strings.Contains(strings.Join(rep.KnownGaps, "|"), gapLogRolled) {
		t.Errorf("small step moved the year: %+v", rep)
	}
	// The old form may omit the milliseconds.
	evs, _ = mustCollect(t, NewLog(), "[10] 5 Mar 10:00:00 * Background saving started by pid 1\n", o)
	if len(evs) != 1 || evs[0].Start.Format(time.RFC3339Nano) != "2001-03-05T10:00:00Z" {
		t.Errorf("no-ms form: %+v", evs)
	}
}

func TestLogDST(t *testing.T) {
	line := func(day, mon, hms string) string {
		return fmt.Sprintf("1:M %s %s 2001 %s * Background saving started by pid 2\n", day, mon, hms)
	}
	in := line("28", "Oct", "01:30:00.000") + // fold: 01:30 happens twice
		line("1", "Apr", "02:30:00.000") + // gap: 02:00 to 03:00 does not exist
		line("1", "Apr", "03:30:00.000") // fine
	o := logOpts(t)
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := collect(t, NewLog(), in, o)
	if err != nil || len(evs) != 1 || rep.Rejected != 2 {
		t.Fatalf("%v %d events %+v", err, len(evs), rep)
	}
	if rep.Errors[0].Reason != importer.ReasonDSTAmbiguous || rep.Errors[1].Reason != importer.ReasonDSTNonexistent {
		t.Errorf("reasons %+v", rep.Errors)
	}
}

func TestLogBadTimes(t *testing.T) {
	for name, line := range map[string]string{
		"day":   "1:M 32 Oct 2001 14:02:30.120 * Background saving started by pid 2",
		"month": "1:M 25 Foo 2001 14:02:30.120 * Background saving started by pid 2",
		"hour":  "1:M 25 Oct 2001 25:02:30.120 * Background saving started by pid 2",
		"year":  "1:M 25 Oct 1999 14:02:30.120 * Background saving started by pid 2",
		"leap":  "1:M 29 Feb 2001 14:02:30.120 * Background saving started by pid 2",
	} {
		o := logOpts(t)
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := collect(t, NewLog(), line+"\n1:M 25 Oct 2001 14:02:30.120 * Background saving started by pid 2\n", o)
		if name == "year" {
			// 1999 is outside the accepted years, so the shape is a log line and the time is bad.
			if err != nil || len(evs) != 1 || rep.Rejected != 1 {
				t.Errorf("%s: %v %d %+v", name, err, len(evs), rep)
			}
			continue
		}
		if err != nil || len(evs) != 1 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime {
			t.Errorf("%s: %v %d %+v", name, err, len(evs), rep)
		}
	}
}

// One start, stop, save or rewrite writes several lines; each is one event.
func TestLogMerging(t *testing.T) {
	kinds := func(in string) string {
		evs, _ := mustCollect(t, NewLog(), in, logOpts(t))
		var k []string
		for _, e := range evs {
			k = append(k, e.Kind)
		}
		return strings.Join(k, ",")
	}
	h := func(pid, sec int, role, msg string) string {
		return fmt.Sprintf("%d:%s 25 Oct 2001 14:00:%02d.000 * %s\n", pid, role, sec, msg)
	}
	// Banner and Ready of one start.
	if got := kinds(h(7, 1, "M", "oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo") + h(7, 3, "M", "Ready to accept connections tcp")); got != "redis-start" {
		t.Errorf("start: %s", got)
	}
	// A Ready line from another pid is a different start.
	if got := kinds(h(7, 1, "M", "oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo") + h(8, 3, "M", "Ready to accept connections tcp")); got != "redis-start,redis-start" {
		t.Errorf("two starts: %s", got)
	}
	// Three lines of one shutdown, then the next one.
	stop := h(7, 1, "M", "Received SIGTERM scheduling shutdown...") + h(7, 2, "M", "User requested shutdown...") + h(7, 3, "M", "Redis is now ready to exit, bye bye...")
	if got := kinds(stop + h(9, 4, "M", "Ready to accept connections") + h(9, 5, "M", "Received SIGINT scheduling shutdown...")); got != "redis-stop,redis-start,redis-stop" {
		t.Errorf("stop: %s", got)
	}
	// AOF rewrite: child, parent and the final line.
	aof := h(7, 1, "M", "Background append only file rewriting started by pid 8") +
		h(8, 2, "C", "SYNC append only file rewrite performed") +
		h(7, 3, "M", "Background AOF rewrite terminated with success") +
		h(7, 3, "M", "Background AOF rewrite finished successfully")
	if got := kinds(aof); got != "aof-rewrite-start,aof-rewrite-done" {
		t.Errorf("aof: %s", got)
	}
	// Two saves with their start lines are both shown; completions with no start line are
	// shown when they are far enough apart.
	rdb := h(7, 1, "M", "Background saving started by pid 8") + h(8, 2, "C", "DB saved on disk") + h(7, 2, "M", "Background saving terminated with success") +
		h(7, 5, "M", "Background saving started by pid 9") + h(9, 6, "C", "DB saved on disk") + h(7, 6, "M", "Background saving terminated with success")
	if got := kinds(rdb); got != "bgsave-start,bgsave-done,bgsave-start,bgsave-done" {
		t.Errorf("rdb: %s", got)
	}
	lone := h(7, 1, "M", "Background saving terminated with success") + strings.Replace(h(7, 1, "M", "Background saving terminated with success"), "14:00:01", "14:00:40", 1)
	if got := kinds(lone); got != "bgsave-done,bgsave-done" {
		t.Errorf("lone done: %s", got)
	}
	// Failures are never merged.
	fail := h(7, 1, "M", "Background saving error") + h(7, 1, "M", "Background saving terminated by signal 9")
	if got := kinds(fail); got != "bgsave-fail,bgsave-fail" {
		t.Errorf("fail: %s", got)
	}
}

func TestLogMessageTable(t *testing.T) {
	cases := []struct{ msg, kind, family string }{
		{"Background saving started by pid 1240", KindBgsaveStart, "rdb"},
		{"Background RDB transfer started by pid 12", KindBgsaveStart, "full-sync"},
		{"Background saving terminated by signal 9", KindBgsaveFail, "rdb"},
		{"Can't save in background: fork: Cannot allocate memory", KindBgsaveFail, "fork"},
		{"Background AOF rewrite terminated with error", KindBgsaveFail, "aof"},
		{"Can't rewrite append only file in background: fork: Cannot allocate memory", KindBgsaveFail, "fork"},
		{"Client id=9 addr=192.0.2.1:1 scheduled to be closed ASAP for overcoming of output buffer limits.", KindClientBufferClosed, "output-buffer-limit"},
		{"Closing client that reached max query buffer length: id=9 addr=192.0.2.1:1", KindClientBufferClosed, "other"},
		{"MASTER <-> REPLICA sync: Finished with success", KindReplicationEvent, "master-link-up"},
		{"MASTER <-> REPLICA sync: receiving 1234 bytes from master", KindReplicationEvent, "full-sync"},
		{"Connection with master lost.", KindReplicationEvent, "master-link-down"},
		{"MASTER timeout: no data nor PING received...", KindReplicationEvent, "master-link-down"},
		{"Connection with replica 192.0.2.5:6379 lost.", KindReplicationEvent, "replica-lost"},
		{"Synchronization with replica 192.0.2.5:6379 succeeded", KindReplicationEvent, "replica-connected"},
		{"Full resync requested by replica 192.0.2.5:6379", KindReplicationEvent, "full-sync"},
		{"Successful partial resynchronization with master.", KindReplicationEvent, "partial-sync"},
		{"WARNING overcommit_memory is set to 0! Background save may fail under low memory condition.", KindConfigWarning, "overcommit-memory"},
		{"WARNING you have Transparent Huge Pages (THP) support enabled in your kernel.", KindConfigWarning, "transparent-huge-pages"},
		{"WARNING: The TCP backlog setting of 511 cannot be enforced", KindConfigWarning, "tcp-backlog"},
		{"You requested maxclients of 10000 requiring at least 10032 max file descriptors.", KindConfigWarning, "maxclients"},
		{"Warning: no config file specified, using the default config.", KindConfigWarning, "no-config-file"},
	}
	for _, c := range cases {
		r := classifyLog([]byte(c.msg))
		if r == nil || r.kind != c.kind || r.family != c.family {
			t.Errorf("%q -> %+v want %s/%s", c.msg, r, c.kind, c.family)
		}
	}
	for _, m := range []string{"", "100 changes in 300 seconds. Saving...", "Client closed connection", "x"} {
		if r := classifyLog([]byte(m)); r != nil {
			t.Errorf("%q matched %+v", m, r)
		}
	}
}

// The kinds, families, roles and attributes are the export policy's
// (internal/evidence/caseexport.go and caseexport_enums.go).
func TestLogTablesMatchExport(t *testing.T) {
	raw, err := os.ReadFile("../../evidence/caseexport_enums.go")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(raw), "\r", "")
	quoted := func(re *regexp.Regexp) map[string]bool {
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("%v not found in the export enums", re)
		}
		out := map[string]bool{}
		for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
			out[q[1]] = true
		}
		return out
	}
	families := quoted(regexp.MustCompile(`(?s)\bredisLogFamilies\s*=\s*fold\((.*?)\)\n`))
	kinds := quoted(regexp.MustCompile(`(?s)"redis-log":\s*set\((.*?)\),?\n`))
	used := map[string]bool{}
	usedKinds := map[string]bool{}
	for _, r := range logRules {
		if !families[r.family] {
			t.Errorf("family %q is not in the export enum", r.family)
		}
		if !kinds[r.kind] {
			t.Errorf("kind %q is not in the export kinds", r.kind)
		}
		if r.prefix != strings.ToLower(r.prefix) || r.has != strings.ToLower(r.has) {
			t.Errorf("rule %q is not lower case", r.prefix)
		}
		used[r.family] = true
		usedKinds[r.kind] = true
	}
	for k := range kinds {
		if !usedKinds[k] {
			t.Errorf("export kind %q is never emitted", k)
		}
	}
	var unused []string
	for f := range families {
		if !used[f] {
			unused = append(unused, f)
		}
	}
	sort.Strings(unused)
	t.Logf("export families with no rule: %v", unused)
	roles := quoted(regexp.MustCompile(`(?s)\bredisRoles\s*=\s*fold\((.*?)\)\n`))
	for _, v := range logRoles {
		if !roles[v] {
			t.Errorf("role %q is not in the export enum", v)
		}
	}
	got := evidence.ExportAttrNames(NameLog)
	if len(got) != len(logAllowed) {
		t.Errorf("export attrs %v, importer %v", got, logAllowed)
	}
	for k := range logAllowed {
		if !got[k] {
			t.Errorf("attr %q is not exported", k)
		}
	}
}

func TestLogDetect(t *testing.T) {
	d := NewLog()
	cases := []struct {
		name, data, file string
		want             int
	}{
		{"new form", logNew, "x.txt", 90},
		{"file name", logNew, "redis-server.log", 95},
		{"mixed forms", logGood, "x.txt", 75},
		{"year-less", "[10] 05 Mar 10:00:00.000 * Background saving started by pid 1\n", "x.log", 75},
		{"unknown message", "1:M 05 Mar 2001 10:00:00.000 * hello\n", "x.log", 80},
		{"slowlog", slowGood, "slowlog.txt", 0},
		{"info", infoGood, "x.txt", 0},
		{"mongo", `{"t":{"$date":"2001-09-09T01:00:00Z"}}`, "mongod.log", 0},
		{"csv", "a,b,c\n1,2,3\n", "x.csv", 0},
	}
	for _, c := range cases {
		if got := d.Detect([]byte(c.data), c.file); got != c.want {
			t.Errorf("%s: %d want %d", c.name, got, c.want)
		}
	}
	if d.Detect(utf16le(logGood), "") == 0 {
		t.Error("UTF-16 log not detected")
	}
	for _, other := range []importer.Importer{NewSlowlog(), NewLatency(), NewInfo()} {
		if other.Detect([]byte(logGood), "redis.log") != 0 {
			t.Errorf("%s claims a redis.log", other.Name())
		}
	}
}

func TestLogSuppliedKeys(t *testing.T) {
	spec, ok := NewLog().(importer.SuppliedKeyer).SuppliedKeys()[logYearKey]
	if !ok {
		t.Fatal("no year key")
	}
	for in, want := range map[string]bool{"2001": true, "2000": true, "2100": true, "1999": false, "2101": false, "20.5": false, "abc": false, "": false} {
		if _, got := spec.Canonical(in); got != want {
			t.Errorf("year %q: %v want %v", in, got, want)
		}
	}
}

func TestLogPrivacy(t *testing.T) {
	in := `1:M 25 Oct 2001 14:02:40.000 # Client id=91 addr=192.0.2.10:52144 name=SENTINEL-7f3a closed for overcoming of output buffer limits.
1:M 25 Oct 2001 14:02:41.000 * Synchronization with replica 192.0.2.77:6379 succeeded SENTINEL-7f3a
1:M 25 Oct 2001 14:02:42.000 * Some line with SENTINEL-7f3a and 198.51.100.7 in it
`
	evs, rep, err := collect(t, NewLog(), in, logOpts(t))
	if err != nil || len(evs) != 2 {
		t.Fatalf("%v %d", err, len(evs))
	}
	out := fmt.Sprintf("%#v|%#v|%v", evs, rep, err)
	for _, bad := range []string{sentinel, "192.0.2", "198.51.100", "web-a-conn"} {
		if strings.Contains(out, bad) {
			t.Errorf("output holds %q: %s", bad, out)
		}
	}
	// A year error and a time zone error carry no content.
	o := logOpts(t)
	o.Supplied = nil
	_, _, err = collect(t, NewLog(), "[1] 25 Oct 14:02:40.000 * SENTINEL-7f3a\n", o)
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Errorf("error %v", err)
	}
}

// The caps are ceilings for this importer too.
func TestLogCapsAreCeilings(t *testing.T) {
	d := NewLog().DefaultLimits()
	got := capLimits(importer.Limits{MaxBytes: 1 << 60, MaxRows: 1 << 60, MaxLineBytes: 1 << 40}, d)
	if got.MaxBytes != d.MaxBytes || got.MaxRows != d.MaxRows || got.MaxLineBytes != d.MaxLineBytes {
		t.Errorf("caps raised: %+v", got)
	}
	if d.MaxBytes != 512<<20 || d.MaxRows != 5_000_000 || d.MaxLineBytes != 64<<10 {
		t.Errorf("defaults %+v", d)
	}
}

func TestLogWindow(t *testing.T) {
	o := logOpts(t)
	o.Window = importer.Window{From: time.Date(2001, 10, 25, 18, 3, 0, 0, time.UTC)}
	evs, rep := mustCollect(t, NewLog(), logGood, o)
	for _, e := range evs {
		if e.End.Before(o.Window.From) {
			t.Errorf("event before the window: %v", e.Start)
		}
	}
	if len(evs) != 2 || rep.Rows != 8 {
		t.Errorf("%d events %+v", len(evs), rep)
	}
}
