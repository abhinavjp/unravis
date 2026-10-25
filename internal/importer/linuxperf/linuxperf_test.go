package linuxperf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

const sentinel = "SENTINEL-7f3a"

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loc(t testing.TB, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// opts maps the one host of a file to the label host-1 (the raw names in the
// fixtures are lnx-a and so on, which must never reach an event).
func opts(cores int, tz *time.Location) importer.Options {
	return importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "nonce-1",
		Host: &importer.HostHint{Label: "host-1", Role: "web", TZ: tz, LogicalCores: cores},
		Pseudonymize: func(kind, value string) string {
			sum := sha256.Sum256([]byte(kind + "\x00" + value))
			return kind + "-" + hex.EncodeToString(sum[:4])
		},
	}
}

func parse(t testing.TB, imp importer.Importer, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), imp, bytes.NewReader(data), o)
}

func refusal(err error, r importer.Reason) bool {
	var qe *importer.QuarantineError
	return errors.As(err, &qe) && qe.Reason == r
}

func hasGap(rep importer.Report, g string) bool {
	for _, x := range rep.KnownGaps {
		if x == g {
			return true
		}
	}
	return false
}

func hasReject(rep importer.Report, r importer.Reason) bool {
	for _, e := range rep.Errors {
		if e.Reason == r {
			return true
		}
	}
	return false
}

func at(h, m, s int) time.Time { return time.Date(2001, 10, 25, h, m, s, 0, time.UTC) }

// pick returns the metric events with the given metric name and, when proc or
// cpu are not "", that process or cpu attribute.
func pick(evs []evidence.Event, metric, proc, cpu string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Attrs["metric"] != metric {
			continue
		}
		if proc != "" && e.Attrs["process"] != proc {
			continue
		}
		if cpu != "" && e.Attrs["cpu"] != cpu {
			continue
		}
		out = append(out, e)
	}
	return out
}

func vals(evs []evidence.Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, fmt.Sprint(*e.Value))
	}
	return "[" + strings.Join(s, " ") + "]"
}

// Generated, time-ordered files over 2*importer.HeadSize bytes. The default Large
// of the harness repeats the rows of Good after the last header, which for a
// two-group sadf file reads cpu rows under the memory header.

func sadfLarge() []byte {
	var b strings.Builder
	b.WriteString("# hostname;interval;timestamp;CPU;%usr;%nice;%sys;%iowait;%steal;%idle\n")
	base := time.Date(2001, 10, 25, 0, 0, 0, 0, time.UTC)
	for i := 0; b.Len() < 3*importer.HeadSize; i++ {
		ts := base.Add(time.Duration(i) * 10 * time.Second).Format("2006-01-02 15:04:05")
		for _, c := range []string{"-1", "0"} {
			fmt.Fprintf(&b, "lnx-a;10;%s UTC;%s;%d.50;0.00;3.25;0.40;0.00;%d.25\n", ts, c, i%50, 90-i%50)
		}
	}
	return []byte(b.String())
}

func pidstatLarge() []byte {
	var b strings.Builder
	b.WriteString("Linux 5.15.0-91-generic (lnx-a) \t10/25/2001 \t_x86_64_\t(4 CPU)\n\n")
	b.WriteString("# Time        UID       PID    %usr %system  %guest    %wait    %CPU   CPU  Command\n")
	base := int64(1003968000)
	for i := 0; b.Len() < 3*importer.HeadSize; i++ {
		for j, name := range []string{"/usr/bin/dotnet Api.dll", "redis-server *:6379", "nginx: worker process"} {
			fmt.Fprintf(&b, "%d  %d %d %d.00 %d.00 0.00 0.00 %d.00 %d %s\n", base+int64(i)*5, 33+j, 2000+j, i%40, i%30, i%40+i%30, j, name)
		}
	}
	return []byte(b.String())
}

func vmstatLarge() []byte {
	var b strings.Builder
	b.WriteString("procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu----- -----timestamp-----\n")
	b.WriteString(" r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st                 UTC\n")
	base := time.Date(2001, 10, 25, 0, 0, 0, 0, time.UTC)
	for i := 0; b.Len() < 3*importer.HeadSize; i++ {
		ts := base.Add(time.Duration(i) * time.Second).Format("2006-01-02 15:04:05")
		fmt.Fprintf(&b, " 1  0      0 %d  65536 524288    0    0    10    20  300  500 %2d  2 %2d  1  0 %s\n", 1000000+i, i%50, 97-i%50, ts)
	}
	return []byte(b.String())
}

func sadfFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: fixture(t, "sadf_cpu_mem.txt"), Bad: fixture(t, "bad_sadf.txt"), Large: sadfLarge(),
		Options: opts(4, time.UTC), NoFreeText: true,
	}
}

func pidstatFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: fixture(t, "pidstat_ur.txt"), Bad: fixture(t, "bad_pidstat.txt"), Large: pidstatLarge(),
		Options: opts(4, time.UTC), NoFreeText: true, // process is a server image or a derived label
	}
}

func vmstatFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: fixture(t, "vmstat_t.txt"), Bad: fixture(t, "bad_vmstat.txt"), Large: vmstatLarge(),
		Options: opts(4, time.UTC), NoFreeText: true,
	}
}

func TestConformanceSadf(t *testing.T)   { importertest.Run(t, NewSadf(), sadfFixtures(t)) }
func TestConformancePidstat(t *testing.T) { importertest.Run(t, NewPidstat(), pidstatFixtures(t)) }
func TestConformanceVmstat(t *testing.T)  { importertest.Run(t, NewVmstat(), vmstatFixtures(t)) }

func fuzzOne(f *testing.F, imp importer.Importer, fx importertest.Fixtures, seeds ...string) {
	f.Add(fx.Good)
	f.Add(fx.Bad)
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Add([]byte("\xff\xfeS\x00a\x00"))
	fx.Large = nil
	importertest.FuzzParse(f, imp, fx)
}

func FuzzParseSadf(f *testing.F) {
	fuzzOne(f, NewSadf(), sadfFixtures(f),
		"# hostname;interval;timestamp;CPU;%usr\nlnx-a;60;2001-10-25 13:00:00 UTC;-1;1.00\n",
		"# hostname;interval;timestamp;CPU;%idle\nlnx-a;-1;1004014860;LINUX-RESTART\n")
}

func FuzzParsePidstat(f *testing.F) {
	fuzzOne(f, NewPidstat(), pidstatFixtures(f),
		"# Time UID PID %CPU Command\n1004014860 0 1 1.00 x y z\n")
}

func FuzzParseVmstat(f *testing.F) {
	fuzzOne(f, NewVmstat(), vmstatFixtures(f),
		"procs memory\n r b swpd free buff cache si so bi bo in cs us sy id wa st UTC\n 1 0 0 1 1 1 0 0 0 0 0 0 1 1 98 0 0 2001-10-25 13:00:00\n")
}

func TestNamesAndAllowedAttrs(t *testing.T) {
	for imp, want := range map[importer.Importer]string{NewSadf(): "linux-sadf", NewPidstat(): "linux-pidstat", NewVmstat(): "linux-vmstat"} {
		if imp.Name() != want {
			t.Errorf("name %q, want %q", imp.Name(), want)
		}
	}
}

// ---- Detect

func TestDetectDoesNotConfuseTheThree(t *testing.T) {
	imps := []importer.Importer{NewSadf(), NewPidstat(), NewVmstat()}
	files := map[string][]byte{
		"linux-sadf": fixture(t, "sadf_cpu_mem.txt"), "linux-pidstat": fixture(t, "pidstat_ur.txt"),
		"linux-vmstat": fixture(t, "vmstat_t.txt"),
	}
	for owner, data := range files {
		for _, imp := range imps {
			got := imp.Detect(data, "export.txt")
			if imp.Name() == owner && got < 80 {
				t.Errorf("%s scores %d on its own file", owner, got)
			}
			if imp.Name() != owner && got != 0 {
				t.Errorf("%s scores %d on a %s file", imp.Name(), got, owner)
			}
		}
	}
	if got := NewVmstat().Detect([]byte("procs -----------memory---------- ---swap--\n r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st\n"), "x"); got < 40 || got >= 80 {
		t.Errorf("vmstat without -t scores %d, want a weak score", got)
	}
	for _, junk := range []string{"", "hello world\n", "a,b,c\n1,2,3\n", "# Time\n", "# hostname;x;y\n"} {
		for _, imp := range imps {
			if got := imp.Detect([]byte(junk), "x"); got != 0 {
				t.Errorf("%s scores %d on %q", imp.Name(), got, junk)
			}
		}
	}
}

// ---- sadf

func TestSadfEvents(t *testing.T) {
	evs, rep, err := parse(t, NewSadf(), fixture(t, "sadf_cpu_mem.txt"), opts(4, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// 6 cpu rows x 7 columns, 3 memory rows x 5 columns less one -nan cell.
	if rep.Rows != 10 || rep.Rejected != 0 || rep.Emitted != int64(len(evs)) || len(evs) != 56 {
		t.Fatalf("rows %d rejected %d emitted %d events %d", rep.Rows, rep.Rejected, rep.Emitted, len(evs))
	}
	for _, e := range evs {
		if e.Source != NameSadf || e.Kind != "metric" || e.Host != "host-1" || e.TZRule != clock.RuleUTCField || e.Value == nil {
			t.Fatalf("event %+v", e)
		}
		for k := range e.Attrs {
			if !NewSadf().AllowedAttrs()[k] {
				t.Errorf("attr %q is not allowlisted", k)
			}
		}
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "lnx-a" {
		t.Errorf("observed hosts %v", rep.ObservedHosts)
	}
	if rep.FieldRules["timestamp"] != clock.RuleUTCField || rep.Quality.TimezoneRule != clock.RuleUTCField {
		t.Errorf("rules %v %q", rep.FieldRules, rep.Quality.TimezoneRule)
	}
	// The whole host: idle, with unit percent-host, at each minute.
	idle := pick(evs, "cpu-idle", "", "all")
	if vals(idle) != "[83.75 43.5 2.5]" || idle[0].Unit != "percent-host" || !idle[0].Start.Equal(at(13, 1, 0)) || !idle[2].Start.Equal(at(13, 3, 0)) {
		t.Errorf("idle %s %v", vals(idle), idle)
	}
	// One core: its own cpu attribute and unit.
	c0 := pick(evs, "cpu-user", "", "0")
	if vals(c0) != "[20 60]" || c0[0].Unit != "percent-one-core" {
		t.Errorf("core 0 user %s %q", vals(c0), c0[0].Unit)
	}
	if vals(pick(evs, "cpu-user", "", "1")) != "[5]" {
		t.Errorf("core 1 user")
	}
	// Memory: KiB to bytes; -nan is missing, not zero.
	avail := pick(evs, "mem-avail", "", "")
	if vals(avail) != "[3.221225472e+09 2.68435456e+09 2.147483648e+09]" || avail[0].Unit != "bytes" {
		t.Errorf("avail %s", vals(avail))
	}
	if free := pick(evs, "mem-free", "", ""); vals(free) != "[1.073741824e+09 5.36870912e+08]" {
		t.Errorf("free %s", vals(free))
	}
	if used := pick(evs, "mem-used-percent", "", ""); vals(used) != "[80 90 95]" || used[0].Unit != "percent" {
		t.Errorf("used percent %s", vals(used))
	}
	if commit := pick(evs, "mem-commit-percent", "", ""); len(commit) != 3 {
		t.Errorf("commit %d", len(commit))
	}
	// Columns that are not read never appear.
	for _, m := range []string{"cpu-irq", "cpu-soft", "kbbuffers", "kbcached", "kbdirty"} {
		if len(pick(evs, m, "", "")) != 0 {
			t.Errorf("metric %s emitted", m)
		}
	}
	if !hasGap(rep, GapRestart) || !hasGap(rep, GapIntervalAvg) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	for _, g := range rep.KnownGaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestSadfTimeForms(t *testing.T) {
	ny := loc(t, "America/New_York")
	// A bare time is host-local: 09:01 EDT is 13:01 UTC.
	evs, rep, err := parse(t, NewSadf(), fixture(t, "sadf_local.txt"), opts(4, ny))
	if err != nil || len(evs) == 0 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	if !evs[0].Start.Equal(at(13, 1, 0)) || evs[0].TZRule != clock.RuleHostLocal || rep.Quality.TimezoneRule != clock.RuleHostLocal || !hasGap(rep, GapLocalTime) {
		t.Errorf("local %v %q gaps %v", evs[0].Start, evs[0].TZRule, rep.KnownGaps)
	}
	// ...and needs a host zone.
	if evs, _, err := parse(t, NewSadf(), fixture(t, "sadf_local.txt"), opts(4, nil)); !errors.Is(err, importer.ErrTimezoneUnlabelled) || len(evs) != 0 {
		t.Errorf("no zone: %d events, %v", len(evs), err)
	}
	// Epoch seconds are UTC whatever the host zone is.
	evs, _, err = parse(t, NewSadf(), fixture(t, "sadf_epoch.txt"), opts(4, ny))
	if err != nil || len(evs) == 0 || !evs[0].Start.Equal(at(13, 1, 0)) || evs[0].TZRule != clock.RuleUTCField {
		t.Fatalf("epoch: %d events, %v", len(evs), err)
	}
	// The other rows of a file of one form: a mixed file rejects the odd row.
	mixed := "# hostname;interval;timestamp;CPU;%idle\n" +
		"lnx-a;60;2001-10-25 13:01:00 UTC;-1;90.00\n" +
		"lnx-a;60;2001-10-25 13:02:00;-1;80.00\n"
	o := opts(4, ny)
	o.Limits.MaxRejectFraction = 0.9
	evs, rep, err = parse(t, NewSadf(), []byte(mixed), o)
	if err != nil || len(evs) != 1 || rep.Rejected != 1 || !hasReject(rep, importer.ReasonBadTime) {
		t.Errorf("mixed: %d events, rejected %d, %v", len(evs), rep.Rejected, err)
	}
}

func TestSadfDST(t *testing.T) {
	file := "# hostname;interval;timestamp;CPU;%idle\n" +
		"lnx-a;60;2001-10-25 09:00:00;-1;90.00\n" +
		"lnx-a;60;2001-10-28 01:30:00;-1;80.00\n" + // the hour that repeats
		"lnx-a;60;2001-04-01 02:30:00;-1;70.00\n" // the hour that does not exist
	o := opts(4, loc(t, "America/New_York"))
	o.Limits.MaxRejectFraction = 0.9
	evs, rep, err := parse(t, NewSadf(), []byte(file), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || rep.Rejected != 2 || !hasReject(rep, importer.ReasonDSTAmbiguous) || !hasReject(rep, importer.ReasonDSTNonexistent) {
		t.Errorf("%d events, rejected %d, errors %v", len(evs), rep.Rejected, rep.Errors)
	}
}

func TestSadfRefusals(t *testing.T) {
	hdr := "# hostname;interval;timestamp;CPU;%idle\n"
	cases := []struct {
		name string
		file string
		want importer.Reason
	}{
		{"first timestamp unreadable", hdr + "lnx-a;60;yesterday;-1;90.00\nlnx-a;60;2001-10-25 13:01:00 UTC;-1;90.00\n", importer.ReasonBadTime},
		{"first timestamp wrong shape", hdr + "lnx-a;60;2001-10-25T13:01:00Z;-1;90.00\n", importer.ReasonBadTime},
		{"no header", "lnx-a;60;2001-10-25 13:01:00 UTC;-1;90.00\n", importer.ReasonUnsupportedFormat},
		{"header without a timestamp column", "# hostname;interval;when;CPU;%idle\nlnx-a;60;x;-1;90\n", importer.ReasonUnsupportedFormat},
	}
	for _, c := range cases {
		evs, _, err := parse(t, NewSadf(), []byte(c.file), opts(4, time.UTC))
		if !refusal(err, c.want) || len(evs) != 0 {
			t.Errorf("%s: %d events, %v", c.name, len(evs), err)
		}
	}
}

func TestSadfHostRules(t *testing.T) {
	two := "# hostname;interval;timestamp;CPU;%idle\n" +
		"lnx-a;60;2001-10-25 13:01:00 UTC;-1;90.00\n" +
		"lnx-b;60;2001-10-25 13:01:00 UTC;-1;80.00\n"
	// One picked host stands for one name: a second name is refused.
	if evs, _, err := parse(t, NewSadf(), []byte(two), opts(4, time.UTC)); !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
		t.Errorf("second name: %d events, %v", len(evs), err)
	}
	// No host at all is refused.
	o := opts(4, time.UTC)
	o.Host = nil
	if evs, _, err := parse(t, NewSadf(), fixture(t, "sadf_cpu_mem.txt"), o); !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
		t.Errorf("no host: %d events, %v", len(evs), err)
	}
	// A mapping resolves each name; an unmapped name is refused.
	o = opts(4, time.UTC)
	o.Host = nil
	o.Hosts = func(name string) (importer.HostHint, bool) {
		switch name {
		case "lnx-a":
			return importer.HostHint{Label: "alpha"}, true
		case "lnx-b":
			return importer.HostHint{Label: "beta"}, true
		}
		return importer.HostHint{}, false
	}
	evs, rep, err := parse(t, NewSadf(), []byte(two), o)
	if err != nil || len(evs) != 2 || evs[0].Host != "alpha" || evs[1].Host != "beta" || len(rep.ObservedHosts) != 2 {
		t.Fatalf("mapped: %d events, %v, observed %v", len(evs), err, rep.ObservedHosts)
	}
	for _, e := range evs {
		if strings.Contains(fmt.Sprintf("%+v", e), "lnx-") {
			t.Errorf("raw host name in %+v", e)
		}
	}
	o.Hosts = func(string) (importer.HostHint, bool) { return importer.HostHint{}, false }
	if _, _, err := parse(t, NewSadf(), []byte(two), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("unmapped: %v", err)
	}
}

// A row that is read under the header of another group must not become a
// metric: percent columns over 100 reject the row.
func TestSadfRowUnderTheWrongHeaderIsRejected(t *testing.T) {
	file := "# hostname;interval;timestamp;CPU;%usr;%nice;%sys;%iowait;%steal;%idle\n" +
		"lnx-a;60;2001-10-25 13:01:00 UTC;-1;1.00;0.00;1.00;0.00;0.00;98.00\n" +
		"lnx-a;60;2001-10-25 13:02:00 UTC;1048576;3145728;4194304;80.00;65536;1048576\n"
	o := opts(4, time.UTC)
	o.Limits.MaxRejectFraction = 0.9
	evs, rep, err := parse(t, NewSadf(), []byte(file), o)
	if err != nil || len(evs) != 6 || rep.Rejected != 1 {
		t.Errorf("%d events, rejected %d, %v", len(evs), rep.Rejected, err)
	}
}

// ---- pidstat

func TestPidstatEvents(t *testing.T) {
	evs, rep, err := parse(t, NewPidstat(), fixture(t, "pidstat_ur.txt"), opts(4, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 12 || rep.Rejected != 0 || rep.Emitted != int64(len(evs)) || len(evs) != 60 {
		t.Fatalf("rows %d rejected %d emitted %d events %d", rep.Rows, rep.Rejected, rep.Emitted, len(evs))
	}
	for _, e := range evs {
		if e.Source != NamePidstat || e.Kind != "metric" || e.Host != "host-1" || e.TZRule != clock.RuleUTCField || e.Value == nil {
			t.Fatalf("event %+v", e)
		}
		if e.Attrs["process"] == "" || e.Attrs["pid"] == "" {
			t.Fatalf("attrs %v", e.Attrs)
		}
		for k := range e.Attrs {
			if !NewPidstat().AllowedAttrs()[k] {
				t.Errorf("attr %q is not allowlisted", k)
			}
		}
	}
	// %CPU is a percentage of one core: 100 over four cores is 25 percent of the host.
	cpu := pick(evs, "cpu", "dotnet", "")
	if vals(cpu) != "[25 17.5]" || cpu[0].Unit != "percent-host" || !cpu[0].Start.Equal(at(13, 1, 0)) || !cpu[1].Start.Equal(at(13, 2, 0)) {
		t.Errorf("dotnet cpu %s %v", vals(cpu), cpu)
	}
	if cpu[0].Attrs["pid"] != "2210" {
		t.Errorf("pid %q", cpu[0].Attrs["pid"])
	}
	// RSS is KiB, stored as bytes.
	rss := pick(evs, "rss", "dotnet", "")
	if vals(rss) != "[2.56e+09 2.6624e+09]" || rss[0].Unit != "bytes" {
		t.Errorf("rss %s", vals(rss))
	}
	if p := pick(evs, "mem-percent", "dotnet", ""); vals(p) != "[30 31]" || p[0].Unit != "percent" {
		t.Errorf("mem percent %s", vals(p))
	}
	if f := pick(evs, "majflt-per-sec", "dotnet", ""); vals(f) != "[0.02 0]" || f[0].Unit != "per-sec" {
		t.Errorf("majflt %s", vals(f))
	}
	// Names: server images stay (a path is cut), everything else is a derived
	// label, and a name that is not an image name is "other".
	names := map[string]bool{}
	for _, e := range evs {
		names[e.Attrs["process"]] = true
	}
	for _, want := range []string{"dotnet", "redis-server", "other"} {
		if !names[want] {
			t.Errorf("name %q missing in %v", want, names)
		}
	}
	for n := range names {
		switch {
		case n == "dotnet", n == "redis-server", n == "other", strings.HasPrefix(n, "instance-"):
		default:
			t.Errorf("stored name %q", n)
		}
	}
	for _, g := range rep.KnownGaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
	if !hasGap(rep, GapPidstatSeen) || !hasGap(rep, GapNoStartTime) || hasGap(rep, GapNoCores) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	if len(rep.ObservedHosts) != 1 || rep.ObservedHosts[0] != "lnx-a" {
		t.Errorf("observed %v", rep.ObservedHosts)
	}
	// No lifecycle events: a snapshot shows no start or end.
	for _, e := range evs {
		if e.Kind != "metric" {
			t.Errorf("kind %q", e.Kind)
		}
	}
}

func TestPidstatWithoutCoreCount(t *testing.T) {
	evs, rep, err := parse(t, NewPidstat(), fixture(t, "pidstat_ur.txt"), opts(0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	cpu := pick(evs, "cpu", "dotnet", "")
	if vals(cpu) != "[100 70]" || cpu[0].Unit != "percent-one-core" || !hasGap(rep, GapNoCores) {
		t.Errorf("cpu %s %q gaps %v", vals(cpu), cpu[0].Unit, rep.KnownGaps)
	}
}

// The command line is never stored: only the executable base name of its first
// word, and not even that unless it is a server image.
func TestPidstatNeverStoresArguments(t *testing.T) {
	file := "# Time UID PID %CPU Command\n" +
		"1004014860 0 10 1.00 /usr/bin/dotnet /srv/app/Api.dll --token " + sentinel + "\n" +
		"1004014860 0 11 2.00 /opt/acme-payroll/bin/payroll-worker --key " + sentinel + "\n" +
		"1004014860 0 12 3.00 nginx: worker process " + sentinel + "\n" +
		"1004014860 0 13 4.00 " + sentinel + "-tool --flag\n" +
		"1004014860 0 14 5.00 /usr/bin/redis-server *:6379 --requirepass " + sentinel + "\n" +
		"1004014860 0 15 6.00 weird/name;with|chars " + sentinel + "\n"
	for _, o := range []importer.Options{opts(4, time.UTC), func() importer.Options { o := opts(4, time.UTC); o.Pseudonymize = nil; return o }()} {
		evs, rep, err := parse(t, NewPidstat(), []byte(file), o)
		if err != nil || len(evs) != 6 {
			t.Fatalf("%d events, %v", len(evs), err)
		}
		all := strings.ToLower(fmt.Sprintf("%+v %+v", evs, rep))
		for _, banned := range []string{"sentinel", "7f3a", "payroll", "acme", "api.dll", "--token", "requirepass", "weird", "6379", "worker"} {
			if strings.Contains(all, banned) {
				t.Errorf("%q is stored: %.300s", banned, all)
			}
		}
		names := map[string]bool{}
		for _, e := range evs {
			names[e.Attrs["process"]] = true
		}
		if !names["dotnet"] || !names["redis-server"] {
			t.Errorf("server images not kept: %v", names)
		}
		if o.Pseudonymize == nil && (len(names) != 3 || !names["other"]) {
			t.Errorf("without a pseudonymiser: %v", names)
		}
	}
}

func TestPidstatRefusals(t *testing.T) {
	cases := []struct {
		name string
		file string
		want importer.Reason
	}{
		{"threads listing", "# Time UID TGID TID %usr %system %guest %wait %CPU CPU Command\n1004014860 0 1 1 1.00 1.00 0.00 0.00 2.00 0 x\n", importer.ReasonUnsupportedFormat},
		{"command not last", "# Time UID PID Command %CPU\n1004014860 0 1 x 1.00\n", importer.ReasonUnsupportedFormat},
		{"first time unreadable", "# Time UID PID %CPU Command\nnoon 0 1 1.00 x\n1004014860 0 1 1.00 x\n", importer.ReasonBadTime},
		{"rows without a header", "1004014860 0 1 1.00 x\n", importer.ReasonUnsupportedFormat},
		{"header only", "# Time UID PID %CPU Command\n", importer.ReasonUnsupportedFormat},
	}
	for _, c := range cases {
		evs, _, err := parse(t, NewPidstat(), []byte(c.file), opts(4, time.UTC))
		if c.name == "header only" {
			// A header and no rows is a file with nothing to read, not a refusal reason of its own.
			if len(evs) != 0 {
				t.Errorf("%s: %d events", c.name, len(evs))
			}
			continue
		}
		if !refusal(err, c.want) || len(evs) != 0 {
			t.Errorf("%s: %d events, %v", c.name, len(evs), err)
		}
	}
}

func TestPidstatHostRules(t *testing.T) {
	o := opts(4, time.UTC)
	o.Host = nil
	if _, _, err := parse(t, NewPidstat(), fixture(t, "pidstat_ur.txt"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("no host: %v", err)
	}
	o.Hosts = func(name string) (importer.HostHint, bool) {
		if name == "lnx-a" {
			return importer.HostHint{Label: "alpha", LogicalCores: 2}, true
		}
		return importer.HostHint{}, false
	}
	evs, _, err := parse(t, NewPidstat(), fixture(t, "pidstat_ur.txt"), o)
	if err != nil || len(evs) == 0 || evs[0].Host != "alpha" {
		t.Fatalf("mapped: %d events, %v", len(evs), err)
	}
	if cpu := pick(evs, "cpu", "dotnet", ""); vals(cpu) != "[50 35]" {
		t.Errorf("cpu over the mapped host's two cores: %s", vals(cpu))
	}
	// A banner naming another host than the mapped one is refused.
	o.Hosts = func(string) (importer.HostHint, bool) { return importer.HostHint{}, false }
	if _, _, err := parse(t, NewPidstat(), fixture(t, "pidstat_ur.txt"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("unmapped banner: %v", err)
	}
}

// ---- vmstat

func TestVmstatEvents(t *testing.T) {
	evs, rep, err := parse(t, NewVmstat(), fixture(t, "vmstat_t.txt"), opts(4, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// The first sample keeps only free; two more rows give us sy id wa st and free.
	if rep.Rows != 3 || rep.Rejected != 0 || rep.Emitted != int64(len(evs)) || len(evs) != 13 {
		t.Fatalf("rows %d rejected %d emitted %d events %d", rep.Rows, rep.Rejected, rep.Emitted, len(evs))
	}
	for _, e := range evs {
		if e.Source != NameVmstat || e.Kind != "metric" || e.Host != "host-1" || e.TZRule != clock.RuleUTCField || len(e.Attrs) != 1 {
			t.Fatalf("event %+v", e)
		}
	}
	idle := pick(evs, "cpu-idle", "", "")
	if vals(idle) != "[85 20]" || idle[0].Unit != "percent-host" || !idle[0].Start.Equal(at(13, 0, 2)) {
		t.Errorf("idle %s %v", vals(idle), idle)
	}
	free := pick(evs, "mem-free", "", "")
	if vals(free) != "[1.073741824e+09 1.06496e+09 1.05472e+09]" || free[0].Unit != "bytes" {
		t.Errorf("free %s", vals(free))
	}
	// The first row is an average since boot: no cpu event at 13:00:01.
	for _, e := range evs {
		if e.Start.Equal(at(13, 0, 1)) && e.Attrs["metric"] != "mem-free" {
			t.Errorf("first sample has %s", e.Attrs["metric"])
		}
	}
	if !hasGap(rep, GapFirstSample) || !hasGap(rep, GapNoAvail) || !hasGap(rep, GapMemoryUnit) {
		t.Errorf("gaps %v", rep.KnownGaps)
	}
	for _, g := range rep.KnownGaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
	if len(rep.ObservedHosts) != 0 {
		t.Errorf("a vmstat file has no host name, observed %v", rep.ObservedHosts)
	}
}

func TestVmstatLocalZone(t *testing.T) {
	ny := loc(t, "America/New_York")
	evs, rep, err := parse(t, NewVmstat(), fixture(t, "vmstat_a_local.txt"), opts(4, ny))
	if err != nil || len(evs) == 0 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	// 09:00:02 EDT is 13:00:02 UTC; -a lists inact and active, which are dropped.
	idle := pick(evs, "cpu-idle", "", "")
	if len(idle) != 1 || !idle[0].Start.Equal(at(13, 0, 2)) || idle[0].TZRule != clock.RuleHostLocal || !hasGap(rep, GapLocalTime) {
		t.Errorf("idle %v gaps %v", idle, rep.KnownGaps)
	}
	if len(pick(evs, "cpu-guest", "", "")) != 1 {
		t.Errorf("gu column not read")
	}
	// A zone name needs the host zone, and must be the one the host has.
	if _, _, err := parse(t, NewVmstat(), fixture(t, "vmstat_a_local.txt"), opts(4, nil)); !errors.Is(err, importer.ErrTimezoneUnlabelled) {
		t.Errorf("no zone: %v", err)
	}
	if evs, _, err := parse(t, NewVmstat(), fixture(t, "vmstat_a_local.txt"), opts(4, loc(t, "Europe/Berlin"))); !refusal(err, importer.ReasonTZMismatch) || len(evs) != 0 {
		t.Errorf("mismatch: %d events, %v", len(evs), err)
	}
}

func TestVmstatRefusals(t *testing.T) {
	const grp = "procs -----------memory---------- ---swap-- -----io---- -system-- ------cpu-----\n"
	const hdrNoTime = " r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st\n"
	const hdr = " r  b   swpd   free   buff  cache   si   so    bi    bo   in   cs us sy id wa st UTC\n"
	cases := []struct {
		name string
		file string
		want importer.Reason
	}{
		{"no -t", grp + hdrNoTime + " 1  0      0 1048576  65536 524288    0    0    10    20  300  500  5  2 92  1  0\n", importer.ReasonBadTime},
		{"first time unreadable", grp + hdr + " 1  0      0 1048576  65536 524288    0    0    10    20  300  500  5  2 92  1  0 nodate notime\n", importer.ReasonBadTime},
		{"rows without a header", " 1  0      0 1048576  65536 524288    0    0    10    20  300  500  5  2 92  1  0 2001-10-25 13:00:01\n", importer.ReasonUnsupportedFormat},
	}
	for _, c := range cases {
		evs, _, err := parse(t, NewVmstat(), []byte(c.file), opts(4, time.UTC))
		if !refusal(err, c.want) || len(evs) != 0 {
			t.Errorf("%s: %d events, %v", c.name, len(evs), err)
		}
	}
	o := opts(4, time.UTC)
	o.Host = nil
	if _, _, err := parse(t, NewVmstat(), fixture(t, "vmstat_t.txt"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("no host: %v", err)
	}
	// A second header (vmstat repeats it) with another zone name rejects its rows.
	two := grp + hdr + " 1  0      0 1048576  65536 524288    0    0    10    20  300  500  5  2 92  1  0 2001-10-25 13:00:01\n" +
		strings.Replace(hdr, "UTC", "EDT", 1) + " 1  0      0 1048576  65536 524288    0    0    10    20  300  500  5  2 92  1  0 2001-10-25 09:00:02\n"
	o = opts(4, time.UTC)
	o.Limits.MaxRejectFraction = 0.9
	if _, rep, err := parse(t, NewVmstat(), []byte(two), o); err != nil || rep.Rejected != 1 {
		t.Errorf("mixed zones: rejected %d, %v", rep.Rejected, err)
	}
}
