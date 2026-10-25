package discover_test

import (
	"context"
	"strings"
	"testing"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer/all"
)

const (
	redisSvcCmd = `"C:\Redis\redis-server.exe" --service-run "C:\Redis\redis.windows-service.conf" --requirepass ` + dbtMarker
	redisCfgAt  = `C:\Redis\redis.windows-service.conf`
)

func redisHost(conf string) (*fake.Env, map[string]string) {
	e := fake.New()
	dbtAddService(e, "Redis", "redis-server.exe", `C:\Redis`)
	dbtFile(e, redisCfgAt, conf)
	return e, map[string]string{"Redis": redisSvcCmd}
}

func runRedis(e *fake.Env, lines map[string]string) ([]discover.Finding, []discover.Gap) {
	return discover.RedisDetector{}.Detect(context.Background(), dbtEnv(e, lines))
}

func TestRedisSecretsNeverKept(t *testing.T) {
	conf := dbtLines(
		"# synthetic config",
		"port 6379",
		"requirepass "+dbtMarker,
		"masterauth "+dbtMarker,
		"user default on >"+dbtMarker+" ~* +@all",
		`tls-key-file C:\certs\`+dbtMarker+`.key`,
		"tls-key-file-pass "+dbtMarker,
		`dir "C:\\Redis\\data"`,
		`logfile "C:\\Redis\\logs\\redis.log"`,
	)
	e, lines := redisHost(conf)
	dbtFile(e, `C:\Redis\logs\redis.log`, dbtRedisLog)

	findings, gaps := runRedis(e, lines)
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings=%d gaps=%+v, want one finding and no gap", len(findings), gaps)
	}
	f := findings[0]
	if f.Detector != "redis" || f.Kind != discover.KindLogFile || f.Importer != "redis-log" {
		t.Errorf("finding = %+v", f)
	}
	if f.Confidence != 90 {
		t.Errorf("confidence = %d, want 90 (declared path, sniff agrees)", f.Confidence)
	}
	if p := f.Paths(); len(p) != 1 || p[0] != `C:\Redis\logs\redis.log` {
		t.Errorf("paths = %v", p)
	}
	if f.Files != 1 || f.SizeBytes != int64(len(dbtRedisLog)) {
		t.Errorf("files=%d size=%d", f.Files, f.SizeBytes)
	}
	dbtAssertClean(t, findings, gaps)
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestRedisDefaultConfigAndRelativeLogfile(t *testing.T) {
	// No command line available: the config is found beside the service image.
	e := fake.New()
	dbtAddService(e, "Redis", "REDIS-SERVER.EXE", `C:\Redis`)
	dbtFile(e, `C:\Redis\redis.windows.conf`, dbtLines(`dir C:\Redis\data`, `logfile redis.log`))
	dbtFile(e, `C:\Redis\data\redis.log`, dbtRedisLog)
	findings, gaps := runRedis(e, nil)
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
	}
	if p := findings[0].Paths(); len(p) != 1 || p[0] != `C:\Redis\data\redis.log` {
		t.Errorf("relative logfile should resolve against dir: %v", p)
	}
	if findings[0].Confidence != 70 {
		t.Errorf("confidence = %d, want 70 for a default config location", findings[0].Confidence)
	}

	// Relative logfile and no dir: resolves against the config folder.
	e2 := fake.New()
	dbtAddService(e2, "Redis", "redis-server.exe", `C:\Redis`)
	dbtFile(e2, `C:\Redis\redis.conf`, dbtLines(`logfile "logs/redis.log"`))
	dbtFile(e2, `C:\Redis\logs\redis.log`, dbtRedisLog)
	findings, gaps = runRedis(e2, nil)
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
	}
	if p := findings[0].Paths(); len(p) != 1 || p[0] != `C:\Redis\logs\redis.log` {
		t.Errorf("paths = %v", p)
	}
}

func TestRedisMemurai(t *testing.T) {
	e := fake.New()
	dbtAddService(e, "Memurai", "memurai.exe", `C:\Program Files\Memurai`)
	dbtFile(e, `C:\Program Files\Memurai\memurai.conf`, dbtLines(`logfile "C:\\Program Files\\Memurai\\memurai.log"`))
	dbtFile(e, `C:\Program Files\Memurai\memurai.log`, dbtRedisLog)
	findings, gaps := runRedis(e, nil)
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
	}
	if !strings.Contains(findings[0].Display, "Memurai") || findings[0].Importer != "redis-log" {
		t.Errorf("finding = %+v", findings[0])
	}
}

func TestRedisNoLogfileOrBadValue(t *testing.T) {
	cases := []struct {
		name string
		conf string
		want discover.Reason
	}{
		{"no logfile directive", dbtLines("port 6379", "requirepass "+dbtMarker), discover.ReasonNotFound},
		{"empty logfile logs to console", dbtLines(`logfile ""`), discover.ReasonNotFound},
		{"commented logfile", dbtLines(`# logfile C:\Redis\r.log`), discover.ReasonNotFound},
		{"unbalanced quote", dbtLines(`logfile "C:\Redis\r.log`), discover.ReasonConfigUnparsed},
		{"two values", dbtLines(`logfile C:\a.log C:\b.log`), discover.ReasonConfigUnparsed},
		{"secret word in value", dbtLines(`logfile C:\password\r.log`), discover.ReasonConfigUnparsed},
		{"unc path", dbtLines(`logfile \\fileserver\share\redis.log`), discover.ReasonUnsafePath},
		{"unc path quoted", dbtLines(`logfile "\\\\fileserver\\share\\redis.log"`), discover.ReasonUnsafePath},
		{"alternate data stream", dbtLines(`logfile C:\Redis\r.log:evil`), discover.ReasonUnsafePath},
		{"dotdot", dbtLines(`logfile C:\Redis\..\r.log`), discover.ReasonUnsafePath},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, lines := redisHost(c.conf)
			findings, gaps := runRedis(e, lines)
			if len(findings) != 0 {
				t.Fatalf("findings = %+v", findings)
			}
			dbtOneGap(t, gaps, c.want)
			dbtAssertClean(t, findings, gaps)
		})
	}
}

func TestRedisLastDirectiveWins(t *testing.T) {
	e, lines := redisHost(dbtLines(`logfile C:\Redis\old.log`, `logfile C:\Redis\new.log`))
	dbtFile(e, `C:\Redis\new.log`, dbtRedisLog)
	findings, gaps := runRedis(e, lines)
	if len(gaps) != 0 || len(findings) != 1 || findings[0].Paths()[0] != `C:\Redis\new.log` {
		t.Fatalf("findings=%+v gaps=%+v", findings, gaps)
	}
}

func TestRedisMissingServiceIsSilent(t *testing.T) {
	e := fake.New()
	dbtAddService(e, "Spooler", "spoolsv.exe", `C:\Windows\System32`)
	dbtAddService(e, "Redis-Exporter", "redis_exporter.exe", `C:\Exp`)
	findings, gaps := runRedis(e, nil)
	if len(findings) != 0 || len(gaps) != 0 {
		t.Fatalf("findings=%+v gaps=%+v, want neither for a host without Redis", findings, gaps)
	}
}

func TestRedisConfigMissingAndOversize(t *testing.T) {
	// The service is there but its config is not.
	e := fake.New()
	dbtAddService(e, "Redis", "redis-server.exe", `C:\Redis`)
	findings, gaps := runRedis(e, map[string]string{"Redis": redisSvcCmd})
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonNotFound)

	// A config at the 4 MiB read cap is refused, not read from a cut-off tail.
	e2, lines := redisHost("")
	big := strings.Repeat("# filler line\n", discover.MaxConfigBytes/14+10)
	e2.FS.AddFile(redisCfgAt, int64(len(big)), dbtTime, []byte(big))
	findings, gaps = runRedis(e2, lines)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonConfigUnparsed)
}

func TestRedisReparseLogIsRefused(t *testing.T) {
	e, lines := redisHost(dbtLines(`logfile C:\Redis\logs\redis.log`))
	dbtFile(e, `C:\Redis\logs\redis.log`, dbtRedisLog)
	e.FS.AddReparse(`C:\Redis\logs`)
	findings, gaps := runRedis(e, lines)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonUnsafePath)
}

func TestRedisDenied(t *testing.T) {
	// Whole-host contract.
	e, _ := redisHost(dbtLines(`logfile C:\Redis\redis.log`))
	dbtFile(e, `C:\Redis\redis.log`, dbtRedisLog)
	fake.AssertDeniedGap(t, discover.RedisDetector{}, e)

	stages := []struct {
		name  string
		setup func(e *fake.Env, lines map[string]string)
	}{
		{"config file", func(e *fake.Env, _ map[string]string) { e.FS.Deny(redisCfgAt) }},
		{"log folder", func(e *fake.Env, _ map[string]string) { e.FS.Deny(`C:\Redis\logs`) }},
		{"service list", func(e *fake.Env, _ map[string]string) { e.Services.Err = discover.ErrAccessDenied }},
	}
	for _, s := range stages {
		t.Run(s.name, func(t *testing.T) {
			e, lines := redisHost(dbtLines(`logfile C:\Redis\logs\redis.log`))
			dbtFile(e, `C:\Redis\logs\redis.log`, dbtRedisLog)
			s.setup(e, lines)
			findings, gaps := runRedis(e, lines)
			if len(findings) != 0 {
				t.Fatalf("findings = %+v", findings)
			}
			dbtOneGap(t, gaps, discover.ReasonNeedsElevation)
		})
	}

	// A denied command line is reported, not swallowed into the default guess.
	e2, _ := redisHost(dbtLines(`logfile C:\Redis\redis.log`))
	env := dbtEnv(e2, map[string]string{})
	env.Services = &dbtServices{Services: e2.Services, Err: discover.ErrAccessDenied}
	findings, gaps := discover.RedisDetector{}.Detect(context.Background(), env)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonNeedsElevation)
}

func TestRedisConfigArg(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
		ok   bool
	}{
		{`"C:\Redis\redis-server.exe" --service-run C:\Redis\redis.conf`, `C:\Redis\redis.conf`, true},
		{`"C:\Redis\redis-server.exe" --service-run "C:\Program Files\Redis\redis.conf" --requirepass ` + dbtMarker, `C:\Program Files\Redis\redis.conf`, true},
		{`C:\Program Files\Redis\redis-server.exe --service-run redis.conf`, `redis.conf`, true},
		{`"C:\Redis\redis-server.exe" --service-name Redis --service-run redis.conf`, `redis.conf`, true},
		{`"C:\Redis\redis-server.exe" redis.conf --port 7000`, `redis.conf`, true},
		{`"C:\Redis\redis-server.exe" --requirepass ` + dbtMarker, ``, false},
		{`"C:\Redis\redis-server.exe" --service-run --requirepass ` + dbtMarker, ``, false},
		{`"C:\Redis\redis-server.exe"`, ``, false},
		{``, ``, false},
		{`"C:\Redis\redis-server.exe`, ``, false},
	}
	for _, c := range cases {
		got, ok := discover.RedisConfigArg(c.cmd)
		if got != c.want || ok != c.ok {
			t.Errorf("RedisConfigArg(%q) = %q, %v; want %q, %v", c.cmd, got, ok, c.want, c.ok)
		}
		if strings.Contains(got, dbtMarker) {
			t.Errorf("RedisConfigArg(%q) returned the marker", c.cmd)
		}
	}
}

func TestRedisHostWithoutServicesOrFS(t *testing.T) {
	findings, gaps := discover.RedisDetector{}.Detect(context.Background(), discover.Env{})
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonUnreadable)
	if gaps[0].Detector != "redis" {
		t.Errorf("gap = %+v", gaps[0])
	}
}
