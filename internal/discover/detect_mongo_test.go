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
	mongoSvcCmd = `"C:\Mongo\bin\mongod.exe" --config "C:\Mongo\mongod.cfg" --service`
	mongoCfgAt  = `C:\Mongo\mongod.cfg`
)

func mongoHost(cfg string) (*fake.Env, map[string]string) {
	e := fake.New()
	dbtAddService(e, "MongoDB", "mongod.exe", `C:\Mongo\bin`)
	dbtFile(e, mongoCfgAt, cfg)
	return e, map[string]string{"MongoDB": mongoSvcCmd}
}

func runMongo(e *fake.Env, lines map[string]string) ([]discover.Finding, []discover.Gap) {
	return discover.MongoDetector{}.Detect(context.Background(), dbtEnv(e, lines))
}

func TestMongoSecretsNeverKept(t *testing.T) {
	cfg := dbtLines(
		"# mongod.conf",
		"storage:",
		`  dbPath: C:\Mongo\data`,
		"systemLog:",
		"  destination: file",
		`  path: C:\Mongo\log\mongod.log`,
		"  logAppend: true",
		"security:",
		`  keyFile: C:\`+dbtMarker+`\keyfile`,
		"  authorization: enabled",
		"net:",
		"  port: 27017",
		"  tls:",
		"    mode: requireTLS",
		`    certificateKeyFile: C:\certs\a.pem`,
		"    certificateKeyFilePassword: "+dbtMarker,
		"setParameter:",
		"  authenticationMechanisms: "+dbtMarker,
	)
	e, lines := mongoHost(cfg)
	dbtFile(e, `C:\Mongo\log\mongod.log`, dbtMongoLog)

	findings, gaps := runMongo(e, lines)
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
	}
	f := findings[0]
	if f.Detector != "mongo" || f.Kind != discover.KindLogFile || f.Importer != "mongod-json" || f.Confidence != 90 {
		t.Errorf("finding = %+v", f)
	}
	if p := f.Paths(); len(p) != 1 || p[0] != `C:\Mongo\log\mongod.log` {
		t.Errorf("paths = %v", p)
	}
	dbtAssertClean(t, findings, gaps)
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestMongoYAMLForms(t *testing.T) {
	good := []struct {
		name, cfg, want string
	}{
		{"double quoted", "systemLog:\n  path: \"C:\\\\Mongo\\\\log\\\\mongod.log\"\n", `C:\Mongo\log\mongod.log`},
		{"single quoted", "systemLog:\n  path: 'C:\\Mongo\\log\\mongod.log'\n", `C:\Mongo\log\mongod.log`},
		{"forward slashes", "systemLog:\n  path: C:/Mongo/log/mongod.log\n", `C:\Mongo\log\mongod.log`},
		{"trailing comment", "systemLog:\n  path: C:\\Mongo\\log\\mongod.log   # the log\n", `C:\Mongo\log\mongod.log`},
		{"comments and blank lines", "# top\n\nsystemLog:   # block\n\n  # note\n  destination: file\n  path: C:\\Mongo\\log\\mongod.log\n", `C:\Mongo\log\mongod.log`},
		{"tab-free four space indent", "systemLog:\n    path: C:\\Mongo\\log\\mongod.log\n", `C:\Mongo\log\mongod.log`},
		{"document start marker", "---\nsystemLog:\n  path: C:\\Mongo\\log\\mongod.log\n", `C:\Mongo\log\mongod.log`},
		{"relative to config folder", "systemLog:\n  path: log\\mongod.log\n", `C:\Mongo\log\mongod.log`},
		{"deeper path key ignored", "systemLog:\n  path: C:\\Mongo\\log\\mongod.log\n  component:\n    query:\n      path: C:\\Other\\bad.log\n", `C:\Mongo\log\mongod.log`},
		{"other block path ignored", "storage:\n  path: C:\\Other\\bad.log\nsystemLog:\n  path: C:\\Mongo\\log\\mongod.log\nnet:\n  path: C:\\Other\\worse.log\n", `C:\Mongo\log\mongod.log`},
		{"BOM and CRLF", "\xef\xbb\xbfsystemLog:\r\n  path: C:\\Mongo\\log\\mongod.log\r\n", `C:\Mongo\log\mongod.log`},
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			e, lines := mongoHost(c.cfg)
			dbtFile(e, c.want, dbtMongoLog)
			findings, gaps := runMongo(e, lines)
			if len(gaps) != 0 || len(findings) != 1 {
				t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
			}
			if p := findings[0].Paths(); len(p) != 1 || p[0] != c.want {
				t.Errorf("paths = %v, want %s", p, c.want)
			}
		})
	}

	bad := []struct {
		name, cfg string
		want      discover.Reason
	}{
		{"syslog destination", "systemLog:\n  destination: syslog\n  path: C:\\Mongo\\log\\mongod.log\n", discover.ReasonNotFound},
		{"no systemLog block", "storage:\n  dbPath: C:\\Mongo\\data\n", discover.ReasonNotFound},
		{"no path key", "systemLog:\n  destination: file\n", discover.ReasonNotFound},
		{"empty path", "systemLog:\n  path:\n", discover.ReasonNotFound},
		{"flow style systemLog", "systemLog: {path: C:\\Mongo\\log\\mongod.log}\n", discover.ReasonConfigUnparsed},
		{"flow style path", "systemLog:\n  path: [C:\\a.log]\n", discover.ReasonConfigUnparsed},
		{"unknown escape in double quotes", "systemLog:\n  path: \"C:\\Mongo\\log\\mongod.log\"\n", discover.ReasonConfigUnparsed},
		{"unterminated quote", "systemLog:\n  path: \"C:\\\\Mongo\\\\log\n", discover.ReasonConfigUnparsed},
		{"secret word", "systemLog:\n  path: C:\\password\\mongod.log\n", discover.ReasonConfigUnparsed},
		{"unc path", "systemLog:\n  path: \\\\fileserver\\share\\mongod.log\n", discover.ReasonUnsafePath},
		{"unc path quoted", "systemLog:\n  path: '\\\\fileserver\\share\\mongod.log'\n", discover.ReasonUnsafePath},
		{"long path prefix", "systemLog:\n  path: \\\\?\\C:\\Mongo\\mongod.log\n", discover.ReasonUnsafePath},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			e, lines := mongoHost(c.cfg)
			findings, gaps := runMongo(e, lines)
			if len(findings) != 0 {
				t.Fatalf("findings = %+v", findings)
			}
			dbtOneGap(t, gaps, c.want)
		})
	}
}

func TestMongoDefaultConfigLocation(t *testing.T) {
	e := fake.New()
	dbtAddService(e, "MongoDB", "mongod.exe", `C:\Mongo\bin`)
	dbtFile(e, `C:\Mongo\bin\mongod.cfg`, "systemLog:\n  path: C:\\Mongo\\log\\mongod.log\n")
	dbtFile(e, `C:\Mongo\log\mongod.log`, dbtMongoLog)
	findings, gaps := runMongo(e, nil)
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
	}
	if findings[0].Confidence != 70 {
		t.Errorf("confidence = %d, want 70 for a default config location", findings[0].Confidence)
	}
}

func TestMongoTextFormatLogLowersConfidence(t *testing.T) {
	// A pre-4.4 text log is not what the mongod-json importer reads, so the head
	// sniff does not confirm it.
	e, lines := mongoHost("systemLog:\n  path: C:\\Mongo\\log\\mongod.log\n")
	dbtFile(e, `C:\Mongo\log\mongod.log`, "2001-09-09T01:40:00.000+0000 I CONTROL  [initandlisten] MongoDB starting : pid=4242 port=27017\n")
	findings, gaps := runMongo(e, lines)
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
	}
	if findings[0].Confidence != 40 {
		t.Errorf("confidence = %d, want 40", findings[0].Confidence)
	}
}

func TestMongoWithoutConfigFlag(t *testing.T) {
	e := fake.New()
	dbtAddService(e, "MongoDB", "mongod.exe", `C:\Mongo\bin`)
	findings, gaps := runMongo(e, map[string]string{"MongoDB": `"C:\Mongo\bin\mongod.exe" --dbpath C:\data --logpath C:\x.log`})
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonNotFound)
}

func TestMongoMissingServiceIsSilent(t *testing.T) {
	e := fake.New()
	dbtAddService(e, "Spooler", "spoolsv.exe", `C:\Windows\System32`)
	findings, gaps := runMongo(e, nil)
	if len(findings) != 0 || len(gaps) != 0 {
		t.Fatalf("findings=%+v gaps=%+v, want neither for a host without MongoDB", findings, gaps)
	}
}

func TestMongoConfigMissingAndOversize(t *testing.T) {
	e := fake.New()
	dbtAddService(e, "MongoDB", "mongod.exe", `C:\Mongo\bin`)
	findings, gaps := runMongo(e, map[string]string{"MongoDB": mongoSvcCmd})
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonNotFound)

	e2, lines := mongoHost("")
	big := strings.Repeat("# filler line\n", discover.MaxConfigBytes/14+10)
	e2.FS.AddFile(mongoCfgAt, int64(len(big)), dbtTime, []byte(big))
	findings, gaps = runMongo(e2, lines)
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonConfigUnparsed)
}

func TestMongoDenied(t *testing.T) {
	e, _ := mongoHost("systemLog:\n  path: C:\\Mongo\\log\\mongod.log\n")
	dbtFile(e, `C:\Mongo\log\mongod.log`, dbtMongoLog)
	fake.AssertDeniedGap(t, discover.MongoDetector{}, e)

	stages := map[string]func(e *fake.Env){
		"config file": func(e *fake.Env) { e.FS.Deny(mongoCfgAt) },
		"log folder":  func(e *fake.Env) { e.FS.Deny(`C:\Mongo\log`) },
	}
	for name, deny := range stages {
		t.Run(name, func(t *testing.T) {
			e, lines := mongoHost("systemLog:\n  path: C:\\Mongo\\log\\mongod.log\n")
			dbtFile(e, `C:\Mongo\log\mongod.log`, dbtMongoLog)
			deny(e)
			findings, gaps := runMongo(e, lines)
			if len(findings) != 0 {
				t.Fatalf("findings = %+v", findings)
			}
			dbtOneGap(t, gaps, discover.ReasonNeedsElevation)
		})
	}
}

func TestMongoConfigArg(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
		ok   bool
	}{
		{`"C:\Mongo\bin\mongod.exe" --config "C:\Mongo\mongod.cfg" --service`, `C:\Mongo\mongod.cfg`, true},
		{`"C:\Mongo\bin\mongod.exe" --config C:\Mongo\mongod.cfg`, `C:\Mongo\mongod.cfg`, true},
		{`"C:\Mongo\bin\mongod.exe" --config=C:\Mongo\mongod.cfg --service`, `C:\Mongo\mongod.cfg`, true},
		{`"C:\Mongo\bin\mongod.exe" -f C:\Mongo\mongod.cfg`, `C:\Mongo\mongod.cfg`, true},
		{`C:\Program Files\Mongo\bin\mongod.exe --service --config "C:\Program Files\Mongo\mongod.cfg"`, `C:\Program Files\Mongo\mongod.cfg`, true},
		{`"C:\Mongo\bin\mongod.exe" --keyFile C:\` + dbtMarker + ` --config C:\a.cfg`, `C:\a.cfg`, true},
		{`"C:\Mongo\bin\mongod.exe" --keyFile C:\` + dbtMarker, ``, false},
		{`"C:\Mongo\bin\mongod.exe" --config`, ``, false},
		{`"C:\Mongo\bin\mongod.exe"`, ``, false},
		{``, ``, false},
	}
	for _, c := range cases {
		got, ok := discover.MongoConfigArg(c.cmd)
		if got != c.want || ok != c.ok {
			t.Errorf("MongoConfigArg(%q) = %q, %v; want %q, %v", c.cmd, got, ok, c.want, c.ok)
		}
		if strings.Contains(got, dbtMarker) {
			t.Errorf("MongoConfigArg(%q) returned the marker", c.cmd)
		}
	}
}
