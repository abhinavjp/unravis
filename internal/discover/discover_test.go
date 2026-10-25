package discover_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer/all"
)

// toy is a detector built from a function, so each test states its behaviour.
type toy struct {
	name string
	fn   func(ctx context.Context, env discover.Env) ([]discover.Finding, []discover.Gap)
}

func (d toy) Name() string { return d.name }
func (d toy) Detect(ctx context.Context, env discover.Env) ([]discover.Finding, []discover.Gap) {
	return d.fn(ctx, env)
}

func finding(detector, importer, display string, paths ...string) discover.Finding {
	f := discover.Finding{
		Detector: detector, Kind: discover.KindLogSet, Importer: importer, HostLabel: "WEB-A",
		Display: display, Confidence: 90, Shows: "discover.iis.shows", Files: 1, Cost: discover.CostSmall,
	}
	f.SetPaths(paths...)
	return f
}

func fixed(name string, fs ...discover.Finding) toy {
	return toy{name, func(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) { return fs, nil }}
}

func envWith(e *fake.Env) discover.Env { return e.Env(all.Registry()) }

func TestRunOrdersByWeight(t *testing.T) {
	env := envWith(fake.New())
	ch := discover.Finding{Detector: "events", Kind: discover.KindEventChannel, Importer: "win-events", Display: "Application", Confidence: 80, Shows: "discover.events.shows"}
	got := discover.Run(context.Background(), env, []discover.Detector{
		fixed("redis", finding("redis", "redis-log", "redis.log", `D:\Redis\redis.log`)),
		fixed("sql", finding("sql", "sql-errorlog", "ERRORLOG", `D:\SQL\Log\ERRORLOG`)),
		fixed("events", ch),
		fixed("iis", finding("iis", "iis-w3c", "W3SVC1", `D:\Logs\web-a`)),
		fixed("applog", finding("applog", "log4net-text", "app.log", `D:\Apps\web-a\app.log`)),
	})
	var order []string
	for _, f := range got.Findings {
		order = append(order, f.Importer)
	}
	want := []string{"log4net-text", "iis-w3c", "win-events", "sql-errorlog", "redis-log"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
	if !got.Complete || len(got.Gaps) != 0 {
		t.Fatalf("complete=%v gaps=%v", got.Complete, got.Gaps)
	}
	if got.Started.IsZero() || got.Finished.Before(got.Started) {
		t.Fatalf("times %v %v", got.Started, got.Finished)
	}
}

func TestWeightFallsBackToDetector(t *testing.T) {
	if discover.Weight(discover.Finding{Detector: "sql"}) != 50 {
		t.Error("detector weight")
	}
	if discover.Weight(discover.Finding{Detector: "x", Importer: "win-counters"}) != 10 {
		t.Error("importer weight")
	}
	if discover.Weight(discover.Finding{Detector: "x"}) <= 70 {
		t.Error("unknown source must rank last")
	}
}

// A Quartz.NET log ranks beside the other scheduler export, after the runtime
// counters and before SQL.
func TestQuartzWeightSitsWithTheSchedulers(t *testing.T) {
	q := discover.Weight(discover.Finding{Detector: "x", Importer: "schedule-quartz"})
	h := discover.Weight(discover.Finding{Detector: "x", Importer: "schedule-hangfire"})
	if q < h || q >= discover.Weight(discover.Finding{Importer: "sql-errorlog"}) {
		t.Errorf("quartz weight %d, hangfire %d", q, h)
	}
}

func TestRunFillsDetectorNameAndElevated(t *testing.T) {
	e := fake.New()
	e.Machine.Admin = true
	f := finding("", "iis-w3c", "W3SVC1", `D:\Logs\web-a`)
	d := toy{"iis", func(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) {
		return []discover.Finding{f}, []discover.Gap{{Display: "x", Reason: discover.ReasonNotFound}}
	}}
	got := discover.Run(context.Background(), envWith(e), []discover.Detector{d})
	if !got.Elevated {
		t.Error("Elevated not copied from the machine")
	}
	if got.Findings[0].Detector != "iis" || got.Gaps[0].Detector != "iis" {
		t.Errorf("detector name not filled: %q %q", got.Findings[0].Detector, got.Gaps[0].Detector)
	}
}

func TestRunPerDetectorTimeout(t *testing.T) {
	env := envWith(fake.New())
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	honours := toy{"honours", func(ctx context.Context, _ discover.Env) ([]discover.Finding, []discover.Gap) {
		<-ctx.Done()
		return nil, nil
	}}
	ignores := toy{"ignores", func(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) {
		<-release
		return []discover.Finding{finding("ignores", "iis-w3c", "late", `D:\Late`)}, nil
	}}
	after := fixed("after", finding("after", "iis-w3c", "W3SVC1", `D:\Logs\web-a`))

	start := time.Now()
	got := discover.RunWith(context.Background(), env, []discover.Detector{honours, ignores, after},
		discover.Limits{PerDetector: 40 * time.Millisecond, Global: 10 * time.Second})
	if time.Since(start) > 5*time.Second {
		t.Fatalf("scan took %v: a hung detector blocked it", time.Since(start))
	}
	if got.Complete {
		t.Error("Complete must be false after a timeout")
	}
	if len(got.Findings) != 1 || got.Findings[0].Detector != "after" {
		t.Fatalf("findings %+v: want only the detector that finished", got.Findings)
	}
	names := map[string]discover.Reason{}
	for _, g := range got.Gaps {
		names[g.Detector] = g.Reason
	}
	if names["honours"] != discover.ReasonUnreadable || names["ignores"] != discover.ReasonUnreadable {
		t.Errorf("gaps %+v", got.Gaps)
	}
}

func TestRunGlobalTimeoutSkipsLaterDetectors(t *testing.T) {
	env := envWith(fake.New())
	var ran atomic.Int32
	slow := toy{"slow", func(ctx context.Context, _ discover.Env) ([]discover.Finding, []discover.Gap) {
		<-ctx.Done()
		return nil, nil
	}}
	later := toy{"later", func(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) {
		ran.Add(1)
		return nil, nil
	}}
	got := discover.RunWith(context.Background(), env, []discover.Detector{slow, later},
		discover.Limits{PerDetector: 10 * time.Second, Global: 40 * time.Millisecond})
	if ran.Load() != 0 {
		t.Error("detector started after the global limit")
	}
	if got.Complete {
		t.Error("Complete must be false")
	}
	var skipped bool
	for _, g := range got.Gaps {
		if g.Detector == "later" && g.Reason == discover.ReasonUnreadable {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("no gap for the skipped detector: %+v", got.Gaps)
	}
}

func TestRunCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran atomic.Int32
	d := toy{"d", func(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) {
		ran.Add(1)
		return nil, nil
	}}
	got := discover.Run(ctx, envWith(fake.New()), []discover.Detector{d})
	if got.Complete || ran.Load() != 0 || len(got.Gaps) != 1 {
		t.Fatalf("complete=%v ran=%d gaps=%v", got.Complete, ran.Load(), got.Gaps)
	}
}

func TestRunRecoversFromPanic(t *testing.T) {
	env := envWith(fake.New())
	boom := toy{"boom", func(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) {
		panic(fmt.Sprintf("read D:\\Logs\\SENTINEL-7f3a: %v", errors.New("password=SENTINEL-7f3a")))
	}}
	nilDeref := toy{"nilderef", func(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) {
		var m map[string]int
		m["x"] = 1
		return nil, nil
	}}
	badName := badNamer{}
	ok := fixed("ok", finding("ok", "iis-w3c", "W3SVC1", `D:\Logs\web-a`))

	got := discover.Run(context.Background(), env, []discover.Detector{boom, nilDeref, badName, ok})
	if len(got.Findings) != 1 || got.Findings[0].Detector != "ok" {
		t.Fatalf("surviving findings: %+v", got.Findings)
	}
	if got.Complete {
		t.Error("Complete must be false after a panic")
	}
	reasons := map[string]discover.Reason{}
	for _, g := range got.Gaps {
		reasons[g.Detector] = g.Reason
	}
	for _, n := range []string{"boom", "nilderef"} {
		if reasons[n] != discover.ReasonUnreadable {
			t.Errorf("detector %s: gap %q", n, reasons[n])
		}
	}
	if len(got.Gaps) != 3 {
		t.Errorf("want 3 gaps, got %+v", got.Gaps)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "SENTINEL") || strings.Contains(string(b), "password") {
		t.Fatalf("panic text reached the scan: %s", b)
	}
}

type badNamer struct{}

func (badNamer) Name() string { panic("SENTINEL-7f3a in name") }
func (badNamer) Detect(context.Context, discover.Env) ([]discover.Finding, []discover.Gap) {
	panic("SENTINEL-7f3a in detect")
}

func TestRunDedupesSitesSharingOneFolder(t *testing.T) {
	env := envWith(fake.New())
	site := func(id, path string, admin bool, oldest, newest int) discover.Finding {
		f := finding("iis", "iis-w3c", "", path)
		f.NeedsAdmin = admin
		f.Oldest = time.Date(2001, 9, oldest, 0, 0, 0, 0, time.UTC)
		f.Newest = time.Date(2001, 9, newest, 0, 0, 0, 0, time.UTC)
		f.SetSites([]string{id}, "u_ex010911.log and 13 more")
		return f
	}
	got := discover.Run(context.Background(), env, []discover.Detector{
		fixed("iis",
			site("W3SVC10", `D:\Logs\web-a`, false, 5, 11),
			site("W3SVC2", `d:\LOGS\Web-A\`, true, 1, 9),
			site("W3SVC1", `D:/Logs/web-a`, false, 3, 14),
			site("W3SVC3", `D:\Logs\web-b`, false, 1, 2),
		),
	})
	if len(got.Findings) != 2 {
		t.Fatalf("got %d findings, want 2 (one merged, one apart): %+v", len(got.Findings), got.Findings)
	}
	var merged, other discover.Finding
	for _, f := range got.Findings {
		if len(f.Sites) == 3 {
			merged = f
		} else {
			other = f
		}
	}
	if !reflect.DeepEqual(merged.Sites, []string{"W3SVC1", "W3SVC2", "W3SVC10"}) {
		t.Errorf("sites %v", merged.Sites)
	}
	if merged.Display != "W3SVC1, W3SVC2, W3SVC10 (u_ex010911.log and 13 more)" {
		t.Errorf("display %q", merged.Display)
	}
	if !merged.NeedsAdmin {
		t.Error("NeedsAdmin must be true when any site needs it")
	}
	if !merged.Oldest.Equal(time.Date(2001, 9, 1, 0, 0, 0, 0, time.UTC)) || !merged.Newest.Equal(time.Date(2001, 9, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("range %v - %v", merged.Oldest, merged.Newest)
	}
	if other.NeedsAdmin || !reflect.DeepEqual(other.Sites, []string{"W3SVC3"}) {
		t.Errorf("other finding changed: %+v", other)
	}
	if merged.ID == other.ID {
		t.Error("IDs collide")
	}
}

func TestRunDoesNotMergeDifferentImporters(t *testing.T) {
	env := envWith(fake.New())
	got := discover.Run(context.Background(), env, []discover.Detector{
		fixed("web",
			finding("web", "nginx-access", "a", `D:\Logs\web-a`),
			finding("web", "apache-combined", "b", `D:\Logs\web-a`),
		),
	})
	if len(got.Findings) != 2 {
		t.Fatalf("same folder with two importers must stay two findings, got %d", len(got.Findings))
	}
}

func TestRunDropsUnsafePathWithGap(t *testing.T) {
	env := envWith(fake.New())
	bad := finding("iis", "iis-w3c", "W3SVC9", `\\web-a.example.test\share\u.log`)
	good := finding("iis", "iis-w3c", "W3SVC1", `D:\Logs\web-a`)
	got := discover.Run(context.Background(), env, []discover.Detector{fixed("iis", bad, good)})
	if len(got.Findings) != 1 || got.Findings[0].Display != "W3SVC1" {
		t.Fatalf("findings %+v", got.Findings)
	}
	if len(got.Gaps) != 1 || got.Gaps[0].Reason != discover.ReasonUnsafePath || got.Gaps[0].Display != "W3SVC9" {
		t.Fatalf("gaps %+v", got.Gaps)
	}
	if strings.Contains(fmt.Sprint(got.Gaps), "example.test") {
		t.Fatal("gap carries the path")
	}
}

func TestRunDropsUnknownImporter(t *testing.T) {
	f := finding("iis", "no-such-importer", "W3SVC1", `D:\Logs\web-a`)
	got := discover.Run(context.Background(), envWith(fake.New()), []discover.Detector{fixed("iis", f)})
	if len(got.Findings) != 0 || len(got.Gaps) != 1 || got.Gaps[0].Reason != discover.ReasonUnreadable {
		t.Fatalf("findings %v gaps %v", got.Findings, got.Gaps)
	}
	// With no registry in the Env the importer name is not checked.
	e := fake.New().Env(nil)
	got = discover.Run(context.Background(), e, []discover.Detector{fixed("iis", f)})
	if len(got.Findings) != 1 {
		t.Fatalf("no registry: %v", got.Findings)
	}
}

func TestFindingIDsAreRandomBase32(t *testing.T) {
	env := envWith(fake.New())
	var fs []discover.Finding
	for i := 0; i < 40; i++ {
		fs = append(fs, finding("iis", "iis-w3c", fmt.Sprintf("s%d", i), fmt.Sprintf(`D:\Logs\web-%d`, i)))
	}
	got := discover.Run(context.Background(), env, []discover.Detector{fixed("iis", fs...)})
	re := regexp.MustCompile(`^[a-z2-7]{26}$`)
	seen := map[string]bool{}
	for _, f := range got.Findings {
		if !re.MatchString(f.ID) {
			t.Fatalf("ID %q", f.ID)
		}
		if seen[f.ID] {
			t.Fatalf("duplicate ID %q", f.ID)
		}
		seen[f.ID] = true
	}
	if len(seen) != 40 {
		t.Fatalf("%d findings", len(seen))
	}
}

func TestScanJSONHasNoPath(t *testing.T) {
	f := finding("iis", "iis-w3c", "", `D:\Журналы\SENTINEL-7f3a-web-a`)
	f.SetSites([]string{"W3SVC1"}, "u_ex010911.log")
	got := discover.Run(context.Background(), envWith(fake.New()), []discover.Detector{fixed("iis", f)})
	if len(got.Findings) != 1 {
		t.Fatalf("findings %+v", got.Findings)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"SENTINEL", "Журналы", `D:\`, `D:\\`, "Logs"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("JSON carries %q: %s", bad, b)
		}
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	walkKeys(m, func(k string) {
		if strings.Contains(strings.ToLower(k), "path") {
			t.Errorf("JSON key %q names a path", k)
		}
	})
	// The paths are still there for the server, through the accessor.
	if p := got.Findings[0].Paths(); len(p) != 1 || !strings.Contains(p[0], "SENTINEL-7f3a") {
		t.Fatalf("Paths() = %v", p)
	}
	// The accessor hands out a copy.
	got.Findings[0].Paths()[0] = "changed"
	if got.Findings[0].Paths()[0] == "changed" {
		t.Fatal("Paths() exposes the internal slice")
	}
}

func walkKeys(v any, f func(string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			f(k)
			walkKeys(c, f)
		}
	case []any:
		for _, c := range x {
			walkKeys(c, f)
		}
	}
}

func TestFindingTypeHasNoExportedPathField(t *testing.T) {
	rt := reflect.TypeOf(discover.Finding{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.IsExported() && strings.Contains(strings.ToLower(f.Name+f.Tag.Get("json")), "path") {
			t.Errorf("exported field %s looks like a path", f.Name)
		}
	}
}

func TestCostForSize(t *testing.T) {
	cases := map[int64]discover.Cost{
		0: discover.CostSmall, 64<<20 - 1: discover.CostSmall,
		64 << 20: discover.CostMedium, 1<<30 - 1: discover.CostMedium,
		1 << 30: discover.CostLarge,
	}
	for size, want := range cases {
		if got := discover.CostForSize(size); got != want {
			t.Errorf("%d: %q want %q", size, got, want)
		}
	}
}

// --- needs-elevation contract ---

// recorder is a testing.TB that records failures instead of failing the run.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Fatalf(string, ...any) { r.failed = true }

func seededHost() *fake.Env {
	e := fake.New()
	e.FS.AddFile(`D:\Logs\web-a\u_ex010911.log`, 100, time.Date(2001, 9, 11, 0, 0, 0, 0, time.UTC), nil)
	return e
}

func TestAccessDeniedContract(t *testing.T) {
	correct := toy{"correct", func(_ context.Context, env discover.Env) ([]discover.Finding, []discover.Gap) {
		if _, err := env.FS.ReadDir(`D:\Logs\web-a`); err != nil {
			return nil, []discover.Gap{discover.GapFromError("correct", discover.KindLogSet, "W3SVC1", err)}
		}
		return []discover.Finding{finding("correct", "iis-w3c", "W3SVC1", `D:\Logs\web-a`)}, nil
	}}
	fake.AssertDeniedGap(t, correct, seededHost())

	swallows := toy{"swallows", func(_ context.Context, env discover.Env) ([]discover.Finding, []discover.Gap) {
		if _, err := env.FS.ReadDir(`D:\Logs\web-a`); err != nil {
			return nil, nil
		}
		return nil, nil
	}}
	rec := &recorder{TB: t}
	fake.AssertDeniedGap(rec, swallows, seededHost())
	if !rec.failed {
		t.Fatal("a detector that swallows access denied passed the contract")
	}

	wrongReason := toy{"wrong", func(_ context.Context, env discover.Env) ([]discover.Finding, []discover.Gap) {
		if _, err := env.FS.ReadDir(`D:\Logs\web-a`); err != nil {
			return nil, []discover.Gap{{Detector: "wrong", Display: "x", Reason: discover.ReasonUnreadable}}
		}
		return nil, nil
	}}
	rec = &recorder{TB: t}
	fake.AssertDeniedGap(rec, wrongReason, seededHost())
	if !rec.failed {
		t.Fatal("a gap with the wrong reason passed the contract")
	}
}

func TestDeniedProbesOnEveryFake(t *testing.T) {
	e := seededHost()
	e.Registry.Set(`HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`, "ErrorLoggingDir", `D:\Http`)
	e.DenyAll()
	env := e.Env(nil)

	probes := map[string]error{}
	_, probes["lstat"] = env.FS.Lstat(`D:\Logs\web-a`)
	_, probes["stat"] = env.FS.Stat(`D:\Logs\web-a\u_ex010911.log`)
	_, probes["readdir"] = env.FS.ReadDir(`D:\Logs\web-a`)
	_, _, probes["final"] = env.FS.Final(`D:\Logs\web-a`)
	_, probes["head"] = env.FS.Head(`D:\Logs\web-a\u_ex010911.log`, 10)
	_, probes["procs"] = env.Procs.List()
	_, probes["services"] = env.Services.List()
	_, probes["registry"] = env.Registry.String(`HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`, "ErrorLoggingDir")
	_, probes["channels"] = env.Channels.List()
	probes["canread"] = env.Channels.CanRead("Application")
	for name, err := range probes {
		if !discover.IsAccessDenied(err) {
			t.Errorf("%s: %v is not access denied", name, err)
		}
		if discover.ReasonFromError(err) != discover.ReasonNeedsElevation {
			t.Errorf("%s: reason %q", name, discover.ReasonFromError(err))
		}
	}
	if !errors.Is(discover.ErrAccessDenied, fs.ErrPermission) {
		t.Error("ErrAccessDenied must wrap fs.ErrPermission")
	}
	if len(env.Machine.Names()) == 0 {
		t.Error("Machine stays readable")
	}
}

// --- fake behaviour ---

func TestFakeFSBehaviour(t *testing.T) {
	e := fake.New()
	mod := time.Date(2001, 9, 11, 0, 0, 0, 0, time.UTC)
	e.FS.AddFile(`D:\Logs\web-a\u_ex010911.log`, 100, mod, []byte("#Software: Microsoft Internet Information Services 10.0\n"))
	e.FS.AddFile(`D:\Logs\web-a\u_ex010912.log`, 200, mod, nil)
	e.FS.AddReparse(`D:\Link`)
	e.FS.AddFile(`D:\Link\a.log`, 1, mod, nil)
	e.FS.Deny(`D:\Private`)
	e.FS.AddFile(`D:\Private\a.log`, 1, mod, nil)
	env := e.Env(nil)

	// Case-insensitive lookup and listing order.
	list, err := env.FS.ReadDir(`d:\LOGS\WEB-A`)
	if err != nil || len(list) != 2 || list[0].Name != "u_ex010911.log" || list[1].Size != 200 {
		t.Fatalf("ReadDir %v %v", list, err)
	}
	if list[0].VolumeSerial != 0 || list[0].FileID != 0 {
		t.Error("ReadDir must not fill file identity")
	}
	st, err := env.FS.Stat(`D:\Logs\web-a\u_ex010911.log`)
	if err != nil || st.FileID == 0 || st.VolumeSerial == 0 || st.IsDir {
		t.Fatalf("Stat %+v %v", st, err)
	}
	lst, _ := env.FS.Lstat(`D:\Logs\web-a\u_ex010911.log`)
	if lst.FileID != 0 {
		t.Error("Lstat must not fill file identity")
	}

	// Head is bounded.
	head, err := env.FS.Head(`D:\Logs\web-a\u_ex010911.log`, 9)
	if err != nil || string(head) != "#Software" {
		t.Fatalf("Head %q %v", head, err)
	}

	// Missing.
	if _, err := env.FS.Stat(`D:\Nope`); !discover.IsNotFound(err) || discover.ReasonFromError(err) != discover.ReasonNotFound {
		t.Fatalf("missing: %v", err)
	}

	// Reparse points: reported, never listed, and Final says so for what is below.
	li, err := env.FS.Lstat(`D:\Link`)
	if err != nil || !li.Reparse {
		t.Fatalf("Lstat link %+v %v", li, err)
	}
	if _, err := env.FS.ReadDir(`D:\Link`); err == nil {
		t.Error("ReadDir followed a reparse point")
	}
	if _, rp, err := env.FS.Final(`D:\Link\a.log`); err != nil || !rp {
		t.Errorf("Final below a reparse point: reparse=%v err=%v", rp, err)
	}
	if _, rp, _ := env.FS.Final(`D:\Logs\web-a\u_ex010911.log`); rp {
		t.Error("Final reported reparse for a plain file")
	}
	if c, _, _ := env.FS.Final(`d:\logs\WEB-A\U_EX010911.LOG`); c != `D:\Logs\web-a\u_ex010911.log` {
		t.Errorf("Final canonical %q", c)
	}

	// A denied subtree.
	for _, p := range []string{`D:\Private`, `D:\Private\a.log`} {
		if _, err := env.FS.Lstat(p); !discover.IsAccessDenied(err) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := env.FS.Lstat(`D:\Logs`); err != nil {
		t.Errorf("sibling of a denied folder: %v", err)
	}

	// The fake refuses what the real one refuses.
	for _, p := range []string{`\\web-a.example.test\share`, `C:\PROGRA~1`, `D:\a\..\b`, `rel\x`} {
		_, err := env.FS.Lstat(p)
		if err == nil || discover.ReasonFromError(err) != discover.ReasonUnsafePath {
			t.Errorf("%s: %v", p, err)
		}
		if strings.Contains(err.Error(), "example.test") || strings.Contains(err.Error(), "PROGRA") {
			t.Errorf("error text carries the path: %v", err)
		}
	}
}

func TestFakeFSTooManyEntries(t *testing.T) {
	e := fake.New()
	for i := 0; i < discover.MaxDirEntries+1; i++ {
		e.FS.AddFile(fmt.Sprintf(`D:\Big\f%05d.log`, i), 1, time.Time{}, nil)
	}
	list, err := e.Env(nil).FS.ReadDir(`D:\Big`)
	if !errors.Is(err, discover.ErrTooManyEntries) || len(list) != discover.MaxDirEntries {
		t.Fatalf("len %d err %v", len(list), err)
	}
	if discover.ReasonFromError(err) != discover.ReasonTooLarge {
		t.Error("reason")
	}
}

func TestFakeRegistryEnforcesAllowlist(t *testing.T) {
	e := fake.New()
	const k = `HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`
	e.Registry.Set(k, "ErrorLoggingDir", `D:\Http`)
	e.Registry.Set(`HKLM\SAM\SAM`, "Secret", "SENTINEL-7f3a")
	env := e.Env(nil)

	if v, err := env.Registry.String(k, "ErrorLoggingDir"); err != nil || v != `D:\Http` {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := env.Registry.String(`HKLM\SAM\SAM`, "Secret"); !errors.Is(err, discover.ErrRegistryNotAllowed) {
		t.Fatalf("outside the allowlist: %v", err)
	}
	if _, err := env.Registry.String(k, "Absent"); err == nil {
		t.Fatal("not allowed value must fail")
	}
	e.Registry.Set(k, "ErrorLoggingDir", `D:\Http`)
	e.Registry.Deny(k, "ErrorLoggingDir")
	if _, err := env.Registry.String(k, "ErrorLoggingDir"); !discover.IsAccessDenied(err) {
		t.Fatalf("denied value: %v", err)
	}
}

func TestAssertFindingsValid(t *testing.T) {
	ok := finding("iis", "iis-w3c", "W3SVC1", `D:\Logs\web-a`)
	fake.AssertFindingsValid(t, []discover.Finding{ok}, all.Registry())

	badShows := ok
	badShows.Shows = "discover.nope.shows"
	rec := &recorder{TB: t}
	fake.AssertFindingsValid(rec, []discover.Finding{badShows}, nil)
	if !rec.failed {
		t.Error("unknown Shows key accepted")
	}

	badPath := finding("iis", "iis-w3c", "W3SVC1", `\\h\s\x`)
	rec = &recorder{TB: t}
	fake.AssertFindingsValid(rec, []discover.Finding{badPath}, nil)
	if !rec.failed {
		t.Error("unsafe path accepted")
	}

	badImporter := finding("iis", "nope", "W3SVC1", `D:\Logs\web-a`)
	rec = &recorder{TB: t}
	fake.AssertFindingsValid(rec, []discover.Finding{badImporter}, all.Registry())
	if !rec.failed {
		t.Error("unknown importer accepted")
	}
}

func TestSystemEnvOnThisOS(t *testing.T) {
	env, err := discover.NewSystemEnv()
	if err != nil {
		if !errors.Is(err, discover.ErrNotSupportedOS) {
			t.Fatalf("unexpected error %v", err)
		}
		return
	}
	// Windows: a usable Env with every part present.
	if env.FS == nil || env.Procs == nil || env.Services == nil || env.Registry == nil || env.Channels == nil || env.Machine == nil {
		t.Fatalf("incomplete Env %+v", env)
	}
}
