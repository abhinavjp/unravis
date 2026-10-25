package discover_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer/all"
)

const (
	sentinel = "SENTINEL-7f3a"
	site     = `C:\inetpub\shop`
)

var t0 = time.Date(2001, 10, 25, 12, 0, 0, 0, time.UTC)

const (
	log4netLine = "2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.OrdersController - Request handled for alice@example.test\n" +
		"2001-10-25 14:02:31,400 [17] ERROR Synthetic.Api.OrdersController - Unhandled error for order-1\n"
	log4netXMLLine = `<log4j:event logger="Synthetic.Api.OrdersController" timestamp="1003971751400" level="ERROR" thread="17"><log4j:message>Unhandled error for order-1</log4j:message></log4j:event>` + "\n"
	nlogLine       = "2001-10-25 14:02:30.9500|INFO|Synthetic.Api.OrdersController|Request handled for alice@example.test\n" +
		"2001-10-25 14:02:31.4000|ERROR|Synthetic.Api.OrdersController|Unhandled error for order-1\n"
	serilogLine = "2001-10-25 14:02:30.950 +13:00 [INF] HTTP GET /orders/o-1001 responded 200 in 1834.2000 ms\n" +
		"2001-10-25 14:02:31.400 +13:00 [ERR] Unhandled exception\n"
	clefLine = `{"@t":"2001-10-25T01:02:30.9500000Z","@mt":"HTTP {RequestMethod} {RequestPath} responded {StatusCode} in {Elapsed:0.0000} ms","RequestMethod":"GET","RequestPath":"/orders/o-1001","StatusCode":200,"Elapsed":1834.2,"SourceContext":"Serilog.AspNetCore.RequestLoggingMiddleware"}` + "\n" +
		`{"@t":"2001-10-25T01:02:31.1230000Z","@mt":"Cache call slow for {UserId}","@l":"Warning","UserId":"alice","SourceContext":"Synthetic.Api.CacheClient"}` + "\n"
)

// put adds a file whose size is its content length and whose age is relative to t0.
func put(e *fake.Env, path, content string, age time.Duration) {
	e.FS.AddFile(path, int64(len(content)), t0.Add(-age), []byte(content))
}

func detect(t *testing.T, e *fake.Env, dirs ...string) ([]discover.Finding, []discover.Gap) {
	t.Helper()
	if len(dirs) == 0 {
		dirs = []string{site}
	}
	reg := all.Registry()
	f, g := discover.NewAppLog(nil, dirs...).Detect(context.Background(), e.Env(reg))
	fake.AssertFindingsValid(t, f, reg)
	return f, g
}

func byImporter(fs []discover.Finding, imp string) []discover.Finding {
	var out []discover.Finding
	for _, f := range fs {
		if f.Importer == imp {
			out = append(out, f)
		}
	}
	return out
}

func gapWith(gs []discover.Gap, r discover.Reason) *discover.Gap {
	for i := range gs {
		if gs[i].Reason == r {
			return &gs[i]
		}
	}
	return nil
}

func mustOne(t *testing.T, fs []discover.Finding, imp string) discover.Finding {
	t.Helper()
	got := byImporter(fs, imp)
	if len(got) != 1 {
		t.Fatalf("want exactly one %s finding, got %d of %d findings", imp, len(got), len(fs))
	}
	return got[0]
}

func assertNoSentinel(t *testing.T, fs []discover.Finding, gs []discover.Gap) {
	t.Helper()
	blob, err := json.Marshal(struct {
		F []discover.Finding
		G []discover.Gap
	}{fs, gs})
	if err != nil {
		t.Fatal(err)
	}
	all := string(blob) + fmt.Sprintf("%#v %#v", fs, gs)
	for _, f := range fs {
		all += strings.Join(f.Paths(), "|")
	}
	if strings.Contains(strings.ToLower(all), strings.ToLower(sentinel)) {
		t.Fatalf("%s leaked into findings or gaps", sentinel)
	}
}

const log4netConfig = `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <connectionStrings>
    <add name="Db" connectionString="Server=db;User Id=sa;Password=SENTINEL-7f3a" />
  </connectionStrings>
  <log4net>
    <appender name="SENTINEL-7f3a" type="log4net.Appender.RollingFileAppender">
      <file value="logs\app.log" />
      <datePattern value="'.'yyyyMMdd" />
      <layout type="log4net.Layout.PatternLayout"><conversionPattern value="%date SENTINEL-7f3a" /></layout>
    </appender>
    <appender name="Ado" type="log4net.Appender.AdoNetAppender">
      <connectionString value="Data Source=db;Password=SENTINEL-7f3a" />
      <commandText value="INSERT INTO Log SENTINEL-7f3a" />
    </appender>
    <appender name="Mail" type="log4net.Appender.SmtpAppender">
      <smtpHost value="SENTINEL-7f3a" /><username value="SENTINEL-7f3a" /><password value="SENTINEL-7f3a" />
    </appender>
    <root><level value="INFO" /><appender-ref ref="Rolling" /></root>
  </log4net>
</configuration>`

func TestLog4netRollingFileAppender(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	put(e, site+`\logs\app.log.20010101`, log4netLine, 48*time.Hour)
	put(e, site+`\logs\app.log.20010102`, log4netLine, 24*time.Hour)
	put(e, site+`\logs\app.log.zip`, "PK", 0)
	put(e, site+`\logs\other.txt`, "x", 0)

	fs, gs := detect(t, e)
	f := mustOne(t, fs, "log4net-text")
	if f.Kind != discover.KindLogSet || f.Files != 3 || f.Confidence != 90 {
		t.Fatalf("kind=%s files=%d conf=%d", f.Kind, f.Files, f.Confidence)
	}
	if f.Shows != "discover.log4net.shows" || f.Detector != "applog" || f.HostLabel != "WEB-A" {
		t.Fatalf("finding fields: %+v", f)
	}
	paths := f.Paths()
	if len(paths) != 3 || !strings.HasSuffix(paths[0], `app.log.20010101`) || !strings.HasSuffix(paths[2], `app.log`) {
		t.Fatalf("paths oldest first expected, got %v", paths)
	}
	if strings.Contains(f.Display, `\`) {
		t.Fatalf("display holds a path: %q", f.Display)
	}
	if f.Newest.Before(f.Oldest) || f.SizeBytes != int64(3*len(log4netLine)) {
		t.Fatalf("size/time wrong: %+v", f)
	}
	if len(gs) != 0 {
		t.Fatalf("unexpected gaps %v", gs)
	}
	assertNoSentinel(t, fs, gs)
}

func TestLog4netXmlLayoutPicksXmlImporter(t *testing.T) {
	cfg := `<configuration><log4net>
  <appender name="x" type="log4net.Appender.FileAppender">
    <file value="C:\logs\shop\events.xml" />
    <layout type="log4net.Layout.XmlLayout" />
  </appender></log4net></configuration>`
	e := fake.New()
	put(e, site+`\log4net.config`, cfg, 0)
	put(e, `C:\logs\shop\events.xml`, log4netXMLLine, 0)
	fs, gs := detect(t, e)
	f := mustOne(t, fs, "log4net-xml")
	if f.Kind != discover.KindLogFile || f.Confidence != 90 || len(gs) != 0 {
		t.Fatalf("kind=%s conf=%d gaps=%v", f.Kind, f.Confidence, gs)
	}
}

func TestSniffDisagreementLowersConfidence(t *testing.T) {
	cfg := `<configuration><log4net><appender type="log4net.Appender.FileAppender"><file value="app.log"/></appender></log4net></configuration>`
	e := fake.New()
	put(e, site+`\web.config`, cfg, 0)
	put(e, site+`\app.log`, log4netXMLLine, 0) // declared text, content is xml
	fs, _ := detect(t, e)
	f := mustOne(t, fs, "log4net-xml")
	if f.Confidence != 70 {
		t.Fatalf("confidence %d, want 70", f.Confidence)
	}

	e2 := fake.New()
	put(e2, site+`\web.config`, cfg, 0)
	put(e2, site+`\app.log`, "", 0) // nothing to sniff
	fs, _ = detect(t, e2)
	f = mustOne(t, fs, "log4net-text")
	if f.Confidence != 40 {
		t.Fatalf("empty file confidence %d, want 40", f.Confidence)
	}
}

const nlogConfig = `<?xml version="1.0"?>
<nlog xmlns="http://www.nlog-project.org/schemas/NLog.xsd" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <targets>
    <target name="SENTINEL-7f3a" xsi:type="File" fileName="${basedir}/logs/${shortdate}.log"
            archiveFileName="${basedir}/archive/app.{#}.log" layout="SENTINEL-7f3a ${message}" />
    <target name="db" xsi:type="Database" connectionString="Server=x;Password=SENTINEL-7f3a" dbProvider="SENTINEL-7f3a" />
    <target name="mail" xsi:type="Mail" smtpServer="SENTINEL-7f3a" smtpPassword="SENTINEL-7f3a" smtpUserName="SENTINEL-7f3a" />
    <target name="seq" xsi:type="BufferingWrapper"><target xsi:type="Seq" serverUrl="http://SENTINEL-7f3a" apiKey="SENTINEL-7f3a" /></target>
  </targets>
  <rules><logger name="*" minlevel="Info" writeTo="logfile" /></rules>
</nlog>`

func TestNLogFileTargetShortdateAndArchive(t *testing.T) {
	e := fake.New()
	put(e, site+`\NLog.config`, nlogConfig, 0)
	put(e, site+`\logs\2001-10-24.log`, nlogLine, 24*time.Hour)
	put(e, site+`\logs\2001-10-25.log`, nlogLine, 0)
	put(e, site+`\logs\notes.log`, "x", 0)
	put(e, site+`\archive\app.1.log`, nlogLine, 72*time.Hour)
	put(e, site+`\archive\app.2.log`, nlogLine, 48*time.Hour)

	fs, gs := detect(t, e)
	got := byImporter(fs, "nlog-text")
	if len(got) != 2 {
		t.Fatalf("want 2 nlog findings (active, archive), got %d: %v", len(got), fs)
	}
	var active, archive discover.Finding
	for _, f := range got {
		if strings.Contains(strings.Join(f.Paths(), ""), `\archive\`) {
			archive = f
		} else {
			active = f
		}
	}
	if active.Files != 2 || archive.Files != 2 || active.Confidence != 90 {
		t.Fatalf("active=%d archive=%d conf=%d", active.Files, archive.Files, active.Confidence)
	}
	for _, p := range active.Paths() {
		if strings.HasSuffix(p, "notes.log") {
			t.Fatalf("notes.log is not part of the set")
		}
	}
	if len(gs) != 0 {
		t.Fatalf("gaps %v", gs)
	}
	assertNoSentinel(t, fs, gs)
}

func TestNLogDateFormatRenderer(t *testing.T) {
	cfg := `<nlog><targets><target type="File" fileName="${basedir}\logs\app-${date:format=yyyyMMdd}.txt"/></targets></nlog>`
	e := fake.New()
	put(e, site+`\nlog.config`, cfg, 0)
	put(e, site+`\logs\app-20010101.txt`, nlogLine, time.Hour)
	put(e, site+`\logs\app-20010102.txt`, nlogLine, 0)
	fs, gs := detect(t, e)
	f := mustOne(t, fs, "nlog-text")
	if f.Files != 2 || len(gs) != 0 {
		t.Fatalf("files=%d gaps=%v", f.Files, gs)
	}
}

func TestNLogLayoutRenderersAreRefused(t *testing.T) {
	for _, name := range []string{
		`${environment:variable=SENTINEL-7f3a}\app.log`,
		`${basedir}\${machinename}.log`,
		`${gdc:item=SENTINEL-7f3a}.log`,
		`${basedir}\logs\${logger}.log`,
		`${basedir}\logs\${date:format=yyyy:culture=SENTINEL-7f3a}.log`,
		`${basedir}\logs\${shortdate`,
		`logs\${basedir}\app.log`,
		`${date:format=yyyy-MM-dd:universaltime=maybe}.log`,
		`${shortdate:universaltime=true}.log`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := fmt.Sprintf(`<nlog><targets><target xsi:type="File" fileName="%s"/></targets></nlog>`, name)
			e := fake.New()
			put(e, site+`\nlog.config`, cfg, 0)
			put(e, site+`\app.log`, nlogLine, 0)
			fs, gs := detect(t, e)
			if len(fs) != 0 {
				t.Fatalf("a refused layout produced a finding: %v", fs)
			}
			g := gapWith(gs, discover.ReasonConfigUnparsed)
			if g == nil {
				t.Fatalf("want a config-unparsed gap, got %v", gs)
			}
			assertNoSentinel(t, fs, gs)
		})
	}
}

func TestNLogJSONConfigInAppsettings(t *testing.T) {
	cfg := `{"NLog":{"targets":{"async":{"type":"AsyncWrapper","target":{"type":"File","fileName":"${basedir}/logs/nlog-${shortdate}.log"}},
	  "db":{"type":"Database","connectionString":"Server=x;Password=SENTINEL-7f3a"}}}}`
	e := fake.New()
	put(e, site+`\appsettings.json`, cfg, 0)
	put(e, site+`\logs\nlog-2001-10-25.log`, nlogLine, 0)
	fs, gs := detect(t, e)
	mustOne(t, fs, "nlog-text")
	assertNoSentinel(t, fs, gs)
}

const appsettingsBase = `{
  // comments and trailing commas are legal in appsettings
  "ConnectionStrings": { "Default": "Server=db;Password=SENTINEL-7f3a", },
  "Logging": { "LogLevel": { "Default": "SENTINEL-7f3a" } },
  "ApplicationInsights": { "InstrumentationKey": "SENTINEL-7f3a", "ConnectionString": "InstrumentationKey=SENTINEL-7f3a" },
  "Serilog": {
    "Using": ["Serilog.Sinks.File", "Serilog.Sinks.Seq"],
    "MinimumLevel": "Information",
    "WriteTo": [
      { "Name": "Console" },
      { "Name": "Seq", "Args": { "serverUrl": "http://SENTINEL-7f3a", "apiKey": "SENTINEL-7f3a" } },
      { "Name": "File", "Args": { "path": "logs/base-.txt", "rollingInterval": "Day", "connection": "SENTINEL-7f3a" } }
    ]
  }
}`

func TestSerilogFileSink(t *testing.T) {
	e := fake.New()
	put(e, site+`\appsettings.json`, appsettingsBase, 0)
	put(e, site+`\logs\base-20010101.txt`, serilogLine, 48*time.Hour)
	put(e, site+`\logs\base-20010102.txt`, serilogLine, 24*time.Hour)
	put(e, site+`\logs\base-20010102_001.txt`, serilogLine, 0)
	put(e, site+`\logs\base-.zip`, "x", 0)
	fs, gs := detect(t, e)
	f := mustOne(t, fs, "serilog-text")
	if f.Files != 3 || f.Confidence != 90 || f.Shows != "discover.serilog.shows" {
		t.Fatalf("files=%d conf=%d shows=%s", f.Files, f.Confidence, f.Shows)
	}
	// ApplicationInsights section gives a remote-only finding.
	var ai *discover.Finding
	for i := range fs {
		if fs[i].Kind == discover.KindRemoteOnly {
			ai = &fs[i]
		}
	}
	if ai == nil || ai.Importer != "" || ai.How != "discover.appinsights.how" || len(ai.Paths()) != 0 {
		t.Fatalf("app insights finding wrong: %+v", ai)
	}
	if len(gs) != 0 {
		t.Fatalf("gaps %v", gs)
	}
	assertNoSentinel(t, fs, gs)
}

func TestSerilogProductionOverrideOrder(t *testing.T) {
	prod := `{ "Serilog": { "WriteTo": [ {}, {}, { "Args": { "path": "logs/prod-.txt" } } ] } }`
	dev := `{ "Serilog": { "WriteTo": [ {}, {}, { "Args": { "path": "logs/dev-.txt" } } ] } }`
	e := fake.New()
	put(e, site+`\appsettings.json`, appsettingsBase, 0)
	put(e, site+`\appsettings.Production.json`, prod, 0)
	put(e, site+`\appsettings.Development.json`, dev, 0)
	put(e, site+`\logs\base-20010101.txt`, serilogLine, time.Hour)
	put(e, site+`\logs\prod-20010101.txt`, serilogLine, time.Hour)
	put(e, site+`\logs\dev-20010101.txt`, serilogLine, time.Hour)
	fs, gs := detect(t, e)
	f := mustOne(t, fs, "serilog-text")
	if len(f.Paths()) != 1 || !strings.HasSuffix(f.Paths()[0], `prod-20010101.txt`) {
		t.Fatalf("Production overlay should win, got %v", f.Paths())
	}
	if len(gs) != 0 {
		t.Fatalf("gaps %v", gs)
	}

	// Another environment is honoured when asked for.
	d := discover.NewAppLog(nil, site)
	d.Environment = "Development"
	fs2, _ := d.Detect(context.Background(), e.Env(all.Registry()))
	f2 := mustOne(t, fs2, "serilog-text")
	if !strings.HasSuffix(f2.Paths()[0], `dev-20010101.txt`) {
		t.Fatalf("Development overlay should win, got %v", f2.Paths())
	}
}

func TestSerilogOverlayCanChangeLaterKeys(t *testing.T) {
	// The overlay replaces only the keys it names; the base still supplies Name.
	base := `{"Serilog":{"WriteTo":[{"Name":"File","Args":{"path":"logs/a-.txt","rollingInterval":"Day"}}]}}`
	prod := `{"Serilog":{"WriteTo":[{"Args":{"formatter":"Serilog.Formatting.Compact.CompactJsonFormatter, Serilog.Formatting.Compact"}}]}}`
	e := fake.New()
	put(e, site+`\appsettings.json`, base, 0)
	put(e, site+`\appsettings.production.json`, prod, 0)
	put(e, site+`\logs\a-20010101.txt`, clefLine, 0)
	fs, gs := detect(t, e)
	f := mustOne(t, fs, "serilog-clef")
	if f.Confidence != 90 || len(gs) != 0 {
		t.Fatalf("conf=%d gaps=%v", f.Confidence, gs)
	}
}

func TestSerilogRollingFilePathFormat(t *testing.T) {
	cfg := `{"Serilog":{"WriteTo":[{"Name":"RollingFile","Args":{"pathFormat":"D:\\Logs\\shop\\log-{Date}.txt"}}]}}`
	e := fake.New()
	put(e, site+`\appsettings.json`, cfg, 0)
	put(e, `D:\Logs\shop\log-20010101.txt`, serilogLine, time.Hour)
	put(e, `D:\Logs\shop\log-20010102.txt`, serilogLine, 0)
	fs, gs := detect(t, e)
	f := mustOne(t, fs, "serilog-text")
	if f.Files != 2 || len(gs) != 0 {
		t.Fatalf("files=%d gaps=%v", f.Files, gs)
	}
}

func TestSerilogUnknownBraceTokenIsRefused(t *testing.T) {
	cfg := `{"Serilog":{"WriteTo":[{"Name":"File","Args":{"path":"logs/{MachineName}-.txt"}}]}}`
	e := fake.New()
	put(e, site+`\appsettings.json`, cfg, 0)
	put(e, site+`\logs\x-.txt`, serilogLine, 0)
	fs, gs := detect(t, e)
	if len(fs) != 0 || gapWith(gs, discover.ReasonConfigUnparsed) == nil {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}
}

func TestMELFileSink(t *testing.T) {
	cfg := `{"Logging":{"LogLevel":{"Default":"SENTINEL-7f3a"},"File":{"Path":"logs/app.log","Secret":"SENTINEL-7f3a"}}}`
	e := fake.New()
	put(e, site+`\appsettings.json`, cfg, 0)
	put(e, site+`\logs\app.log`, serilogLine, 0)
	put(e, site+`\logs\app.log.1`, serilogLine, time.Hour)
	fs, gs := detect(t, e)
	var mel []discover.Finding
	mel = append(mel, byImporter(fs, "mel-json")...)
	mel = append(mel, byImporter(fs, "mel-console")...)
	if len(mel) != 1 || mel[0].Shows != "discover.mel.shows" || mel[0].Files != 2 {
		t.Fatalf("mel findings: %+v", mel)
	}
	assertNoSentinel(t, fs, gs)
}

func TestPresenceFindings(t *testing.T) {
	e := fake.New()
	put(e, site+`\ApplicationInsights.config`, `<ApplicationInsights><InstrumentationKey>SENTINEL-7f3a</InstrumentationKey></ApplicationInsights>`, 0)
	put(e, site+`\bin\Hangfire.Core.dll`, "MZ", 0)
	put(e, site+`\bin\Hangfire.SqlServer.dll`, "MZ", 0)
	fs, gs := detect(t, e)
	if len(fs) != 2 || len(gs) != 0 {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}
	kinds := map[discover.Kind]discover.Finding{}
	for _, f := range fs {
		kinds[f.Kind] = f
		if f.Importer != "" || len(f.Paths()) != 0 || f.Confidence != 40 {
			t.Fatalf("presence finding must be boolean only: %+v", f)
		}
	}
	if kinds[discover.KindRemoteOnly].How != "discover.appinsights.how" || kinds[discover.KindExportNeeded].How != "discover.hangfire.how" {
		t.Fatalf("how keys: %+v", kinds)
	}
	assertNoSentinel(t, fs, gs)
}

func TestAppInsightsElementInWebConfig(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, `<configuration><ApplicationInsights><x key="SENTINEL-7f3a"/></ApplicationInsights></configuration>`, 0)
	fs, gs := detect(t, e)
	if len(fs) != 1 || fs[0].Kind != discover.KindRemoteOnly {
		t.Fatalf("findings=%v", fs)
	}
	assertNoSentinel(t, fs, gs)
}

// The secret test (spec 2.2): SENTINEL-7f3a sits in every non-path attribute of
// every config the detector reads, and must reach no finding, gap or JSON.
func TestSentinelNeverLeaves(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\nlog.config`, nlogConfig, 0)
	put(e, site+`\appsettings.json`, appsettingsBase, 0)
	put(e, site+`\ApplicationInsights.config`, sentinel, 0)
	put(e, site+`\app.exe.config`, `<configuration><appSettings><add key="k" value="SENTINEL-7f3a"/></appSettings></configuration>`, 0)
	put(e, site+`\bin\Hangfire.Core.dll`, "MZ", 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	put(e, site+`\logs\2001-10-25.log`, nlogLine, 0)
	put(e, site+`\logs\base-20010102.txt`, serilogLine, 0)
	// A path value that carries the marker is refused, and the gap is fixed text.
	put(e, site+`\app.config`, `<configuration><log4net><appender type="log4net.Appender.FileAppender"><file value="C:\logs\password=SENTINEL-7f3a\x.log"/></appender></log4net></configuration>`, 0)

	reg := all.Registry()
	scan := discover.Run(context.Background(), e.Env(reg), []discover.Detector{discover.NewAppLog(nil, site)})
	if len(scan.Findings) < 4 {
		t.Fatalf("expected findings from every family, got %d", len(scan.Findings))
	}
	if gapWith(scan.Gaps, discover.ReasonConfigUnparsed) == nil {
		t.Fatalf("the refused path should be a config-unparsed gap: %v", scan.Gaps)
	}
	blob, err := json.Marshal(scan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), sentinel) || strings.Contains(fmt.Sprintf("%#v", scan), sentinel) {
		t.Fatalf("marker leaked into the scan")
	}
	for _, f := range scan.Findings {
		for _, p := range f.Paths() {
			if strings.Contains(p, sentinel) {
				t.Fatalf("marker in a path")
			}
		}
	}
}

func TestUNCAndUnsafePathsAreRefused(t *testing.T) {
	cases := map[string]struct {
		file, content string
		want          discover.Reason
	}{
		"log4net UNC": {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="\\fileserver\share\app.log"/></appender></log4net></c>`, discover.ReasonUnsafePath},
		"nlog UNC":    {`\nlog.config`, `<nlog><targets><target type="File" fileName="\\fileserver\share\app.log"/></targets></nlog>`, discover.ReasonUnsafePath},
		"serilog UNC": {`\appsettings.json`, `{"Serilog":{"WriteTo":[{"Name":"File","Args":{"path":"\\\\fileserver\\share\\app-.txt"}}]}}`, discover.ReasonUnsafePath},
		"dotdot":      {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="..\..\Windows\app.log"/></appender></log4net></c>`, discover.ReasonUnsafePath},
		"device":      {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="\\.\C:\app.log"/></appender></log4net></c>`, discover.ReasonUnsafePath},
		"ads":         {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="C:\logs\app.log:hidden"/></appender></log4net></c>`, discover.ReasonUnsafePath},
		"alias 8.3":   {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="C:\PROGRA~1\app.log"/></appender></log4net></c>`, discover.ReasonUnsafePath},
		"reserved":    {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="C:\logs\con.log"/></appender></log4net></c>`, discover.ReasonUnsafePath},
		"secret word": {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="C:\logs\token\app.log"/></appender></log4net></c>`, discover.ReasonConfigUnparsed},
		"key=":        {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="C:\logs\a;b=c\app.log"/></appender></log4net></c>`, discover.ReasonConfigUnparsed},
		"url":         {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="https://example.test/app.log"/></appender></log4net></c>`, discover.ReasonUnsafePath},
		"env token":   {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="%TEMP%\app.log"/></appender></log4net></c>`, discover.ReasonConfigUnparsed},
		"dollar":      {`\web.config`, `<c><log4net><appender type="FileAppender"><file value="${TMP}\app.log"/></appender></log4net></c>`, discover.ReasonConfigUnparsed},
		"relative serilog escape": {`\appsettings.json`, `{"Serilog":{"WriteTo":[{"Name":"File","Args":{"path":"../../outside/app-.txt"}}]}}`, discover.ReasonUnsafePath},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := fake.New()
			put(e, site+c.file, c.content, 0)
			fs, gs := detect(t, e)
			if len(fs) != 0 {
				t.Fatalf("findings from an unsafe path: %v", fs)
			}
			if g := gapWith(gs, c.want); g == nil {
				t.Fatalf("want a %s gap, got %v", c.want, gs)
			}
			for _, g := range gs {
				if strings.ContainsAny(g.Display, `\/:`) {
					t.Fatalf("gap display carries path text: %q", g.Display)
				}
			}
		})
	}
}

func TestFilesWithUnsafeNamesAreLeftOut(t *testing.T) {
	cfg := `<c><log4net><appender type="FileAppender"><file value="logs\app.log"/><datePattern value="'-'yyyy"/></appender></log4net></c>`
	e := fake.New()
	put(e, site+`\web.config`, cfg, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	put(e, site+`\logs\app.log-2001`, log4netLine, time.Hour)
	fs, gs := detect(t, e)
	f := mustOne(t, fs, "log4net-text")
	if f.Files != 2 || len(gs) != 0 {
		t.Fatalf("files=%d gaps=%v", f.Files, gs)
	}
}

func TestAppLogAccessDeniedContract(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	fake.AssertDeniedGap(t, discover.NewAppLog(nil, site), e)
}

func TestDeniedPiecesBecomeGaps(t *testing.T) {
	// Config file denied.
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	e.FS.Deny(site + `\web.config`)
	fs, gs := detect(t, e)
	g := gapWith(gs, discover.ReasonNeedsElevation)
	if len(fs) != 0 || g == nil || g.Display != "web.config" {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}

	// Log folder denied; the config is fine.
	e = fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	e.FS.Deny(site + `\logs`)
	fs, gs = detect(t, e)
	if len(fs) != 0 || gapWith(gs, discover.ReasonNeedsElevation) == nil {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}
	for _, g := range gs {
		if g.Reason != discover.ReasonNeedsElevation {
			t.Fatalf("every gap here is a denied probe: %v", gs)
		}
	}

	// A log file whose head cannot be read.
	e = fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	e.FS.Deny(site + `\logs\app.log`)
	fs, gs = detect(t, e)
	if len(fs) != 0 || gapWith(gs, discover.ReasonNeedsElevation) == nil {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}
}

func TestMissingLogIsNotFound(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	fs, gs := detect(t, e)
	g := gapWith(gs, discover.ReasonNotFound)
	if len(fs) != 0 || g == nil {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}
	if g.Display != "app.log*" {
		t.Fatalf("gap display %q", g.Display)
	}
}

func TestReparseAndOversizeAndMalformed(t *testing.T) {
	// Log folder is a junction.
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	e.FS.AddReparse(site + `\logs`)
	fs, gs := detect(t, e)
	if len(fs) != 0 || gapWith(gs, discover.ReasonUnsafePath) == nil {
		t.Fatalf("junction: findings=%v gaps=%v", fs, gs)
	}

	// Site folder is a junction.
	e = fake.New()
	e.FS.AddReparse(site)
	fs, gs = detect(t, e)
	if len(fs) != 0 || gapWith(gs, discover.ReasonUnsafePath) == nil {
		t.Fatalf("site junction: findings=%v gaps=%v", fs, gs)
	}

	// Config over 4 MiB is not read.
	e = fake.New()
	e.FS.AddFile(site+`\web.config`, discover.MaxConfigBytes+1, t0, []byte(log4netConfig))
	fs, gs = detect(t, e)
	if len(fs) != 0 || gapWith(gs, discover.ReasonTooLarge) == nil {
		t.Fatalf("oversize: findings=%v gaps=%v", fs, gs)
	}

	// Entity bomb and DTD, broken XML and broken JSON.
	bomb := `<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;">]><configuration>&b;</configuration>`
	for name, c := range map[string]struct{ file, body string }{
		"dtd":        {`\web.config`, bomb},
		"xml syntax": {`\web.config`, `<configuration><log4net></configuration>`},
		"json":       {`\appsettings.json`, `{"Serilog": {`},
		"json tail":  {`\appsettings.json`, `{} {}`},
	} {
		e = fake.New()
		put(e, site+c.file, c.body, 0)
		fs, gs = detect(t, e)
		if len(fs) != 0 || gapWith(gs, discover.ReasonConfigUnparsed) == nil {
			t.Fatalf("%s: findings=%v gaps=%v", name, fs, gs)
		}
	}
}

func TestDeepJSONAndXMLAreRefused(t *testing.T) {
	deepJSON := strings.Repeat(`{"a":`, 70) + "1" + strings.Repeat("}", 70)
	deepXML := strings.Repeat("<a>", 70) + strings.Repeat("</a>", 70)
	e := fake.New()
	put(e, site+`\appsettings.json`, deepJSON, 0)
	put(e, site+`\web.config`, deepXML, 0)
	fs, gs := detect(t, e)
	if len(fs) != 0 || len(gs) != 2 {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}
}

func TestSiteDirsAndDedupe(t *testing.T) {
	e := fake.New()
	other := `C:\inetpub\other`
	cfg := `<c><log4net><appender type="FileAppender"><file value="C:\Logs\shared\app.log"/></appender></log4net></c>`
	put(e, site+`\web.config`, cfg, 0)
	put(e, other+`\web.config`, cfg, 0)
	put(e, `C:\Logs\shared\app.log`, log4netLine, 0)

	sites := func(ctx context.Context) []string {
		return []string{site, strings.ToLower(site), other, `\\server\share\site`, `relative\dir`}
	}
	reg := all.Registry()
	d := discover.NewAppLog(sites)
	scan := discover.Run(context.Background(), e.Env(reg), []discover.Detector{d})
	if len(scan.Findings) != 1 {
		t.Fatalf("two sites with one log folder must give one finding, got %d", len(scan.Findings))
	}
	unsafe := 0
	for _, g := range scan.Gaps {
		if g.Reason == discover.ReasonUnsafePath {
			unsafe++
		}
	}
	if unsafe != 1 { // identical gaps collapse; both bad folders give the same one
		t.Fatalf("unsafe gaps %d: %v", unsafe, scan.Gaps)
	}
}

func TestNoSitesNoFindings(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	fs, gs := discover.NewAppLog(nil).Detect(context.Background(), e.Env(all.Registry()))
	if len(fs) != 0 || len(gs) != 0 {
		t.Fatalf("findings=%v gaps=%v", fs, gs)
	}
}

func TestCancelledContextStops(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fs, _ := discover.NewAppLog(nil, site).Detect(ctx, e.Env(all.Registry()))
	if len(fs) != 0 {
		t.Fatalf("a cancelled detector reported %d findings", len(fs))
	}
}

func TestConfigNamesAreCaseInsensitive(t *testing.T) {
	e := fake.New()
	put(e, site+`\Web.Config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	fs, _ := detect(t, e)
	mustOne(t, fs, "log4net-text")
}

func TestNoImporterRegistryStillFinds(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	fs, _ := discover.NewAppLog(nil, site).Detect(context.Background(), e.Env(nil))
	if len(fs) != 1 || fs[0].Importer != "log4net-text" || fs[0].Confidence != 40 {
		t.Fatalf("findings=%v", fs)
	}
}

func TestNoDirectoryWalkBeyondSiteAndLogFolders(t *testing.T) {
	e := fake.New()
	put(e, site+`\web.config`, log4netConfig, 0)
	put(e, site+`\logs\app.log`, log4netLine, 0)
	put(e, `C:\Windows\System32\config\SAM`, "secret", 0)
	detect(t, e)
	if n := e.FS.Calls["head"]; n > 2 { // web.config and the sniffed log, nothing else
		t.Fatalf("head called %d times", n)
	}
}
