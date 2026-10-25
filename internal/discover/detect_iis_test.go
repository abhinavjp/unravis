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

// All data here is synthetic: web-a, *.example.test, RFC 5737 addresses, 2001
// dates. SENTINEL-7f3a marks every secret and host name that must not reach a
// finding, a gap or the JSON.
const sentinel = "SENTINEL-7f3a"

const (
	w3cHead = "#Software: Microsoft Internet Information Services 10.0\n#Version: 1.0\n#Date: 2001-09-11 00:00:00\n" +
		"#Fields: date time s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) sc-status sc-substatus sc-win32-status time-taken\n" +
		"2001-09-11 00:00:01 192.0.2.10 GET /api/orders - 443 - 192.0.2.99 Mozilla/5.0 200 0 0 12\n"
	httpErrHead = "#Software: Microsoft HTTP API 2.0\n#Version: 1.0\n#Date: 2001-09-11 00:00:00\n" +
		"#Fields: date time c-ip c-port s-ip s-port cs-version cs-method cs-uri sc-status s-siteid s-reason s-queuename\n" +
		"2001-09-11 00:00:01 192.0.2.99 51000 192.0.2.10 443 HTTP/1.1 GET /api/orders 503 1 Disabled -\n"
	frebHead = `<failedRequest url="http://web-a.example.test:80/api/orders" siteId="1" appPoolId="pool-a" processId="1234" verb="GET" ` +
		`failureReason="STATUS_CODE" statusCode="500.0" triggerStatusCode="500" timeTaken="120">` + "\n"
	stdoutHead = "info: Microsoft.Hosting.Lifetime[14]\n      Now listening on: http://web-a.example.test:5000\n"
)

const (
	appHostPath = `C:\Windows\System32\inetsrv\config\applicationHost.config`
	httpErrKey  = `HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`
)

var day = time.Date(2001, 9, 11, 0, 0, 0, 0, time.UTC)

// addFiles adds one file per name in dir. File i is i days after 2001-09-11, so
// the last name is the newest.
func addFiles(e *fake.Env, dir, head string, names ...string) {
	for i, n := range names {
		e.FS.AddFile(dir+`\`+n, 2048, day.AddDate(0, 0, i), []byte(head))
	}
}

func siteXML(id, physical, inner string) string {
	return `<site name="web-a ` + sentinel + `" id="` + id + `">` +
		`<application path="/" applicationPool="pool-a"><virtualDirectory path="/" physicalPath="` + physical + `" userName="` + sentinel + `" password="` + sentinel + `" /></application>` +
		`<bindings><binding protocol="https" bindingInformation="198.51.100.7:443:web-a.example.test ` + sentinel + `" /></bindings>` +
		inner + `</site>`
}

func appHostXML(sites, defaults string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><configuration>` +
		`<connectionStrings><add name="db" connectionString="Server=203.0.113.5;User Id=sa;Password=` + sentinel + `" /></connectionStrings>` +
		`<system.applicationHost><applicationPools><add name="pool-a" managedRuntimeVersion="v4.0">` +
		`<processModel identityType="SpecificUser" userName="` + sentinel + `" password="` + sentinel + `" /></add></applicationPools>` +
		`<sites>` + sites + `<siteDefaults>` + defaults + `</siteDefaults></sites></system.applicationHost></configuration>`
}

const stockDefaults = `<logFile logFormat="W3C" directory="%SystemDrive%\inetpub\logs\LogFiles" />` +
	`<traceFailedRequestsLogging directory="%SystemDrive%\inetpub\logs\FailedReqLogFiles" />`

func putAppHost(e *fake.Env, xml string) {
	e.FS.AddFile(appHostPath, int64(len(xml)), day, []byte(xml))
}

func logFile(dir string) string { return `<logFile directory="` + dir + `" />` }

func iisDetect(e *fake.Env) ([]discover.Finding, []discover.Gap) {
	return discover.NewIISDetector(discover.DefaultIISRoots()).Detect(context.Background(), e.Env(all.Registry()))
}

func httpErrDetect(e *fake.Env) ([]discover.Finding, []discover.Gap) {
	return discover.NewHTTPErrDetector(discover.DefaultIISRoots()).Detect(context.Background(), e.Env(all.Registry()))
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

func gapFor(gs []discover.Gap, display string) (discover.Gap, bool) {
	for _, g := range gs {
		if g.Display == display {
			return g, true
		}
	}
	return discover.Gap{}, false
}

func wantGap(t *testing.T, gs []discover.Gap, display string, reason discover.Reason) {
	t.Helper()
	g, ok := gapFor(gs, display)
	if !ok {
		t.Fatalf("no gap %q in %+v", display, gs)
	}
	if g.Reason != reason {
		t.Errorf("gap %q reason = %q, want %q", display, g.Reason, reason)
	}
}

func noSecrets(t *testing.T, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	// The JSON is what leaves the machine. Findings keep their file paths in an
	// unexported field for the importers, so the %+v dump is only checked for the
	// marker, which must not be in a path either.
	s := string(b)
	for _, bad := range []string{sentinel, "198.51.100", "web-a.example", "203.0.113", `\\`, "inetpub", "Windows"} {
		if strings.Contains(s, bad) {
			t.Errorf("JSON holds %q: %s", bad, s)
		}
	}
	if d := fmt.Sprintf("%+v", v); strings.Contains(d, sentinel) {
		t.Errorf("dump holds the marker: %s", d)
	}
}

func TestIISSitesFromConfig(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(
		siteXML("1", `%SystemDrive%\inetpub\wwwroot\web-a`, logFile(`D:\Logs\IIS`))+
			siteXML("2", `D:\Sites\web-b`, logFile(`D:\Logs\IIS`)),
		stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log", "u_ex010912.log", "u_ex010913.log")
	addFiles(e, `D:\Logs\IIS\W3SVC2`, w3cHead, "u_ex010911.log")

	scan := discover.Run(context.Background(), e.Env(all.Registry()), []discover.Detector{discover.NewIISDetector(discover.DefaultIISRoots())})
	fake.AssertFindingsValid(t, scan.Findings, all.Registry())
	if len(scan.Findings) != 2 {
		t.Fatalf("findings = %d, want 2: %+v", len(scan.Findings), scan.Findings)
	}
	for _, g := range scan.Gaps {
		t.Errorf("unexpected gap %+v", g)
	}
	one, two := scan.Findings[0], scan.Findings[1]
	if one.Display != "W3SVC1 (u_ex010913.log and 2 more)" {
		one, two = two, one
	}
	if one.Display != "W3SVC1 (u_ex010913.log and 2 more)" || one.Kind != discover.KindLogSet || one.Files != 3 ||
		one.SizeBytes != 3*2048 || one.Confidence != 90 || one.Importer != "iis-w3c" || one.Detector != "iis" ||
		one.Shows != "discover.iis.shows" || one.Cost != discover.CostSmall || one.HostLabel != "local" {
		t.Errorf("site 1 = %+v", one)
	}
	if !one.Oldest.Equal(day) || !one.Newest.Equal(day.AddDate(0, 0, 2)) {
		t.Errorf("site 1 dates = %v .. %v", one.Oldest, one.Newest)
	}
	if got := one.Paths(); len(got) != 3 || got[0] != `D:\Logs\IIS\W3SVC1\u_ex010911.log` || got[2] != `D:\Logs\IIS\W3SVC1\u_ex010913.log` {
		t.Errorf("site 1 paths (oldest first) = %v", got)
	}
	if two.Display != "W3SVC2 (u_ex010911.log)" || two.Kind != discover.KindLogFile || two.Files != 1 || two.Confidence != 90 {
		t.Errorf("site 2 = %+v", two)
	}
	noSecrets(t, scan)
}

func TestIISTwoSitesShareOneDir(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(
		siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\Shared`))+siteXML("2", `D:\Sites\web-b`, logFile(`D:\Logs\Shared`)),
		stockDefaults))
	addFiles(e, `D:\Logs\Shared`, w3cHead, "u_ex010911.log", "u_ex010912.log")

	found, gaps := iisDetect(e)
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v", gaps)
	}
	if len(found) != 1 {
		t.Fatalf("Detect gave %d findings, want 1 for one folder: %+v", len(found), found)
	}
	f := found[0]
	if strings.Join(f.Sites, ",") != "W3SVC1,W3SVC2" || f.Display != "W3SVC1, W3SVC2 (u_ex010912.log and 1 more)" || f.Files != 2 {
		t.Errorf("finding = %+v", f)
	}

	scan := discover.Run(context.Background(), e.Env(all.Registry()), []discover.Detector{discover.NewIISDetector(discover.DefaultIISRoots())})
	if len(scan.Findings) != 1 || strings.Join(scan.Findings[0].Sites, ",") != "W3SVC1,W3SVC2" {
		t.Errorf("Run merged to %+v", scan.Findings)
	}
}

func TestIISMissingDirGap(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("3", `D:\Sites\web-c`, logFile(`E:\Gone`)), stockDefaults))
	found, gaps := iisDetect(e)
	if len(found) != 0 {
		t.Errorf("findings = %+v", found)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v", gaps)
	}
	if g := gaps[0]; g.Display != "W3SVC3" || g.Reason != discover.ReasonNotFound || g.Detector != "iis" {
		t.Errorf("gap = %+v", g)
	}
	noSecrets(t, gaps)
}

func TestIISEmptyDirIsNotFound(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	e.FS.AddDir(`D:\Logs\IIS\W3SVC1`)
	e.FS.AddFile(`D:\Logs\IIS\W3SVC1\notes.txt`, 10, day, []byte("x"))
	found, gaps := iisDetect(e)
	if len(found) != 0 {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "W3SVC1", discover.ReasonNotFound)
}

func TestIISDeniedGapContract(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, w3cHead, "u_ex010911.log")
	fake.AssertDeniedGap(t, discover.NewIISDetector(discover.DefaultIISRoots()), e)
}

func TestIISDeniedGapContractNoConfig(t *testing.T) {
	e := fake.New()
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, w3cHead, "u_ex010911.log")
	fake.AssertDeniedGap(t, discover.NewIISDetector(discover.DefaultIISRoots()), e)
}

func TestIISOneSiteDenied(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(
		siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`))+siteXML("2", `D:\Sites\web-b`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	addFiles(e, `D:\Logs\IIS\W3SVC2`, w3cHead, "u_ex010911.log")
	e.FS.Deny(`D:\Logs\IIS\W3SVC1`)
	found, gaps := iisDetect(e)
	if len(found) != 1 || found[0].Display != "W3SVC2 (u_ex010911.log)" {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "W3SVC1", discover.ReasonNeedsElevation)
}

func TestIISDeniedHeadGivesGapNotFinding(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	// The folder lists, but the file itself is locked down.
	e.FS.Deny(`D:\Logs\IIS\W3SVC1\u_ex010911.log`)
	found, gaps := iisDetect(e)
	if len(found) != 0 {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "W3SVC1", discover.ReasonNeedsElevation)
}

func TestIISUnsafeAndUnparsedPaths(t *testing.T) {
	cases := []struct {
		name, dir string
		reason    discover.Reason
	}{
		{"UNC", `\\198.51.100.9\share\logs`, discover.ReasonUnsafePath},
		{"device prefix", `\\?\C:\logs`, discover.ReasonUnsafePath},
		{"parent segment", `D:\Logs\..\Other`, discover.ReasonUnsafePath},
		{"ADS", `D:\Logs\IIS:stream`, discover.ReasonUnsafePath},
		{"other variable", `%USERPROFILE%\logs`, discover.ReasonConfigUnparsed},
		{"half variable", `%SystemDrive\logs`, discover.ReasonConfigUnparsed},
		{"value shape", `D:\Logs\a=b`, discover.ReasonConfigUnparsed},
		{"secret word", `D:\Logs\password`, discover.ReasonConfigUnparsed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := fake.New()
			putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(c.dir)), stockDefaults))
			found, gaps := iisDetect(e)
			if len(found) != 0 {
				t.Errorf("findings = %+v", found)
			}
			wantGap(t, gaps, "W3SVC1", c.reason)
			if n := e.FS.Calls["readdir"]; n != 0 {
				t.Errorf("a refused path still reached ReadDir (%d calls)", n)
			}
			noSecrets(t, gaps)
		})
	}
}

func TestIISRelativeLogDirUsesConfigFolder(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`..\..\..\..\Logs`)), stockDefaults))
	_, gaps := iisDetect(e)
	// Relative to the config folder, and "..", so it is refused rather than guessed.
	wantGap(t, gaps, "W3SVC1", discover.ReasonUnsafePath)
}

func TestIISJunctionDirIsSkipped(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(
		siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`))+siteXML("2", `D:\Sites\web-b`, logFile(`D:\Logs\IIS`)), stockDefaults))
	e.FS.AddReparse(`D:\Logs\IIS\W3SVC1`)
	addFiles(e, `D:\Logs\IIS\W3SVC2`, w3cHead, "u_ex010911.log")
	found, gaps := iisDetect(e)
	if len(found) != 1 || found[0].Display != "W3SVC2 (u_ex010911.log)" {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "W3SVC1", discover.ReasonUnsafePath)
	if n := e.FS.Calls["readdir"]; n != 1 {
		t.Errorf("ReadDir calls = %d, want 1 (the junction must not be listed)", n)
	}
}

func TestIISJunctionAboveTheLogDir(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Link\IIS`)), stockDefaults))
	e.FS.AddReparse(`D:\Link`)
	e.FS.AddFile(`D:\Link\IIS\W3SVC1\u_ex010911.log`, 10, day, []byte(w3cHead))
	found, gaps := iisDetect(e)
	// The fake hides everything under a junction, as the real FS does not follow it.
	if len(found) != 0 {
		t.Errorf("findings = %+v", found)
	}
	if _, ok := gapFor(gaps, "W3SVC1"); !ok {
		t.Errorf("gaps = %+v", gaps)
	}
}

func TestIISNonASCIIPath(t *testing.T) {
	e := fake.New()
	dir := `D:\Logs\Café-日本`
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(dir)), stockDefaults))
	addFiles(e, dir+`\W3SVC1`, w3cHead, "u_ex010911.log", "u_ex010912.log")
	scan := discover.Run(context.Background(), e.Env(all.Registry()), []discover.Detector{discover.NewIISDetector(discover.DefaultIISRoots())})
	fake.AssertFindingsValid(t, scan.Findings, all.Registry())
	if len(scan.Findings) != 1 || scan.Findings[0].Confidence != 90 || scan.Findings[0].Files != 2 {
		t.Fatalf("findings = %+v gaps = %+v", scan.Findings, scan.Gaps)
	}
	if p := scan.Findings[0].Paths()[0]; p != dir+`\W3SVC1\u_ex010911.log` {
		t.Errorf("path = %q", p)
	}
	b, _ := json.Marshal(scan)
	if strings.Contains(string(b), "Café") || strings.Contains(string(b), "日本") {
		t.Errorf("a folder name reached the JSON: %s", b)
	}
}

func TestIISNonASCIIFileName(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	e.FS.AddFile(`D:\Logs\IIS\W3SVC1\u_ex010911-é.log`, 100, day, []byte(w3cHead))
	found, gaps := iisDetect(e)
	if len(found) != 1 || len(gaps) != 0 {
		t.Fatalf("findings = %+v gaps = %+v", found, gaps)
	}
	fake.AssertFindingsValid(t, found, all.Registry())
}

func TestIISDefaultLocationFallback(t *testing.T) {
	e := fake.New()
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, w3cHead, "u_ex010911.log", "u_ex010912.log")
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC10`, w3cHead, "u_ex010911.log")
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC2`, w3cHead, "u_ex010911.log")
	e.FS.AddFile(`C:\inetpub\logs\LogFiles\u_ex010911.log`, 10, day, []byte(w3cHead)) // not under a site folder
	addFiles(e, `C:\inetpub\logs\FailedReqLogFiles\W3SVC1`, frebHead, "fr000001.xml")
	found, gaps := iisDetect(e)
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v", gaps)
	}
	if len(found) != 4 {
		t.Fatalf("findings = %+v", found)
	}
	for _, f := range found {
		if f.Confidence != 70 {
			t.Errorf("%s %s: confidence %d, want 70 for a default location with a matching head", f.Importer, f.Display, f.Confidence)
		}
	}
	if f := byImporter(found, "iis-freb"); len(f) != 1 || f[0].Display != "W3SVC1 (fr000001.xml)" {
		t.Errorf("freb = %+v", f)
	}
	w := byImporter(found, "iis-w3c")
	if len(w) != 3 || w[0].Display != "W3SVC1 (u_ex010912.log and 1 more)" || w[1].Display != "W3SVC2 (u_ex010911.log)" || w[2].Display != "W3SVC10 (u_ex010911.log)" {
		t.Errorf("w3c = %+v", w)
	}
	fake.AssertFindingsValid(t, found, all.Registry())
}

func TestIISNoIISOnTheBox(t *testing.T) {
	e := fake.New()
	found, gaps := iisDetect(e)
	if len(found) != 0 || len(gaps) != 1 || gaps[0].Display != "IIS logs" || gaps[0].Reason != discover.ReasonNotFound {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}
}

func TestIISUnusableConfigFallsBack(t *testing.T) {
	cases := map[string]string{
		"doctype":   `<!DOCTYPE configuration [<!ENTITY a "` + sentinel + `">]><configuration>&a;</configuration>`,
		"not xml":   "this is not xml " + sentinel,
		"truncated": `<configuration><system.applicationHost><sites><site id="1">`,
	}
	for name, xml := range cases {
		t.Run(name, func(t *testing.T) {
			e := fake.New()
			putAppHost(e, xml)
			addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, w3cHead, "u_ex010911.log")
			found, gaps := iisDetect(e)
			wantGap(t, gaps, "IIS settings", discover.ReasonConfigUnparsed)
			if len(found) != 1 || found[0].Confidence != 70 {
				t.Errorf("findings = %+v", found)
			}
			noSecrets(t, gaps)
		})
	}
}

func TestIISConfigWithoutSitesFallsBack(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML("", stockDefaults))
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, w3cHead, "u_ex010911.log")
	found, gaps := iisDetect(e)
	if len(found) != 1 || len(gaps) != 0 {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}
}

func TestIISConfigTooLarge(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(appHostPath, discover.MaxConfigBytes+1, day, []byte("<configuration/>"))
	_, gaps := iisDetect(e)
	wantGap(t, gaps, "IIS settings", discover.ReasonTooLarge)
}

func TestIISDefaultsApplyWhenSiteSetsNothing(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, ""),
		`<logFile directory="%SystemDrive%\inetpub\logs\LogFiles" />`))
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, w3cHead, "u_ex010911.log")
	found, _ := iisDetect(e)
	if len(found) != 1 || found[0].Confidence != 90 {
		t.Errorf("a folder named in siteDefaults is config-declared: %+v", found)
	}
}

func TestIISNoLogDirAnywhereIsDefaultLocation(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, ""), ""))
	addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, w3cHead, "u_ex010911.log")
	found, _ := iisDetect(e)
	if len(found) != 1 || found[0].Confidence != 70 {
		t.Errorf("findings = %+v", found)
	}
}

func TestIISNonW3CFormatIsAGap(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, `<logFile logFormat="NCSA" directory="D:\Logs\IIS" />`), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	found, gaps := iisDetect(e)
	if len(found) != 0 {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "W3SVC1", discover.ReasonConfigUnparsed)
}

func TestIISConfidenceFollowsTheSniff(t *testing.T) {
	cases := []struct {
		name     string
		head     string
		declared bool
		want     int
	}{
		{"declared, head agrees", w3cHead, true, 90},
		{"declared, name only", "not a log\n", true, 40},
		{"default, head agrees", w3cHead, false, 70},
		{"default, name only", "not a log\n", false, 40},
		{"head is another importer", httpErrHead, true, 40},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := fake.New()
			if c.declared {
				putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
				addFiles(e, `D:\Logs\IIS\W3SVC1`, c.head, "u_ex010911.log")
			} else {
				addFiles(e, `C:\inetpub\logs\LogFiles\W3SVC1`, c.head, "u_ex010911.log")
			}
			found, _ := iisDetect(e)
			if len(found) != 1 || found[0].Confidence != c.want {
				t.Errorf("findings = %+v, want confidence %d", found, c.want)
			}
		})
	}
}

func TestIISNoRegistryMeansNameOnly(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	found, _ := discover.NewIISDetector(discover.DefaultIISRoots()).Detect(context.Background(), e.Env(nil))
	if len(found) != 1 || found[0].Confidence != 40 {
		t.Errorf("findings = %+v", found)
	}
}

func TestIISCostFromSizeAlone(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	e.FS.AddFile(`D:\Logs\IIS\W3SVC1\u_ex010911.log`, 40<<30, day, []byte(w3cHead)) // sparse: only the head exists
	found, _ := iisDetect(e)
	if len(found) != 1 || found[0].Cost != discover.CostLarge || found[0].SizeBytes != 40<<30 {
		t.Errorf("findings = %+v", found)
	}
	if n := e.FS.Calls["head"]; n != 2 { // the config and one log head
		t.Errorf("Head calls = %d, want 2", n)
	}
}

func TestIISFrebFromConfig(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)+`<traceFailedRequestsLogging directory="D:\Logs\Traces" enabled="true" />`), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	addFiles(e, `D:\Logs\Traces\W3SVC1`, frebHead, "fr000001.xml", "fr000002.xml", "freb.xsl")
	found, gaps := iisDetect(e)
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v", gaps)
	}
	f := byImporter(found, "iis-freb")
	if len(f) != 1 || f[0].Files != 2 || f[0].Confidence != 90 || f[0].Shows != "discover.freb.shows" || f[0].Display != "W3SVC1 (fr000002.xml and 1 more)" {
		t.Errorf("freb = %+v", f)
	}
}

func TestIISFrebKeepsNewest500(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	for i := 1; i <= 600; i++ {
		e.FS.AddFile(fmt.Sprintf(`C:\inetpub\logs\FailedReqLogFiles\W3SVC1\fr%06d.xml`, i), 100, day.Add(time.Duration(i)*time.Minute), []byte(frebHead))
	}
	found, _ := iisDetect(e)
	f := byImporter(found, "iis-freb")
	if len(f) != 1 || f[0].Files != 500 || !f[0].Oldest.Equal(day.Add(101*time.Minute)) {
		t.Errorf("freb = %+v", f)
	}
}

func TestIISFrebFolderMissingIsQuiet(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	found, gaps := iisDetect(e)
	if len(found) != 1 || len(gaps) != 0 {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}
}

func TestIISFrebDeniedIsAGap(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	addFiles(e, `C:\inetpub\logs\FailedReqLogFiles\W3SVC1`, frebHead, "fr000001.xml")
	e.FS.Deny(`C:\inetpub\logs\FailedReqLogFiles`)
	found, gaps := iisDetect(e)
	if len(found) != 1 {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "W3SVC1 failed request traces", discover.ReasonNeedsElevation)
}

const webConfig = `<?xml version="1.0" encoding="utf-8"?><configuration><location path="." inheritInChildApplications="false"><system.webServer>` +
	`<aspNetCore processPath="dotnet" arguments=".\web-a.dll --password ` + sentinel + `" stdoutLogEnabled="%ENABLED%" stdoutLogFile="%FILE%" hostingModel="inprocess">` +
	`<environmentVariables><environmentVariable name="Db" value="Server=203.0.113.5;Password=` + sentinel + `" /></environmentVariables>` +
	`</aspNetCore></system.webServer></location></configuration>`

func putWebConfig(e *fake.Env, dir, enabled, file string) {
	x := strings.NewReplacer("%ENABLED%", enabled, "%FILE%", file).Replace(webConfig)
	e.FS.AddFile(dir+`\web.config`, int64(len(x)), day, []byte(x))
}

func ancmHost(enabled, file string) *fake.Env {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `%SystemDrive%\inetpub\wwwroot\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	putWebConfig(e, `C:\inetpub\wwwroot\web-a`, enabled, file)
	return e
}

func TestIISANCMStdout(t *testing.T) {
	e := ancmHost("true", `.\logs\stdout`)
	addFiles(e, `C:\inetpub\wwwroot\web-a\logs`, stdoutHead, "stdout_20010911000000_1234.log", "stdout_20010912000000_1234.log", "stdout.txt", "stdout_bad_1234.log", "other_20010911000000_1.log")
	scan := discover.Run(context.Background(), e.Env(all.Registry()), []discover.Detector{discover.NewIISDetector(discover.DefaultIISRoots())})
	fake.AssertFindingsValid(t, scan.Findings, all.Registry())
	f := byImporter(scan.Findings, "ancm-stdout")
	if len(f) != 1 {
		t.Fatalf("ancm findings = %+v, all = %+v gaps = %+v", f, scan.Findings, scan.Gaps)
	}
	if f[0].Files != 2 || f[0].Confidence != 90 || f[0].Shows != "discover.ancm.shows" || f[0].Display != "W3SVC1 (stdout_20010912000000_1234.log and 1 more)" || f[0].Kind != discover.KindLogSet {
		t.Errorf("ancm = %+v", f[0])
	}
	if got := f[0].Paths(); len(got) != 2 || got[0] != `C:\inetpub\wwwroot\web-a\logs\stdout_20010911000000_1234.log` {
		t.Errorf("paths = %v", got)
	}
	noSecrets(t, scan)
}

func TestIISANCMAbsoluteStdoutAndEnvVariable(t *testing.T) {
	e := ancmHost("true", `%ProgramData%\web-a\stdout`)
	addFiles(e, `C:\ProgramData\web-a`, stdoutHead, "stdout_20010911000000_77.log")
	found, _ := iisDetect(e)
	if f := byImporter(found, "ancm-stdout"); len(f) != 1 || f[0].Display != "W3SVC1 (stdout_20010911000000_77.log)" {
		t.Errorf("ancm = %+v", f)
	}
}

func TestIISANCMEnabledButNoFilesIsAGap(t *testing.T) {
	e := ancmHost("true", `.\logs\stdout`)
	_, gaps := iisDetect(e)
	wantGap(t, gaps, "W3SVC1 console log", discover.ReasonNotFound)

	e = ancmHost("true", `.\logs\stdout`)
	e.FS.AddDir(`C:\inetpub\wwwroot\web-a\logs`)
	_, gaps = iisDetect(e)
	wantGap(t, gaps, "W3SVC1 console log", discover.ReasonNotFound)
}

func TestIISANCMDisabledAndEmptyIsQuiet(t *testing.T) {
	e := ancmHost("false", `.\logs\stdout`)
	found, gaps := iisDetect(e)
	if len(gaps) != 0 || len(byImporter(found, "ancm-stdout")) != 0 {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}
}

func TestIISANCMDisabledButOldFilesExist(t *testing.T) {
	e := ancmHost("false", `.\logs\stdout`)
	addFiles(e, `C:\inetpub\wwwroot\web-a\logs`, stdoutHead, "stdout_20010911000000_1234.log")
	found, _ := iisDetect(e)
	if f := byImporter(found, "ancm-stdout"); len(f) != 1 {
		t.Errorf("ancm = %+v", f)
	}
}

func TestIISANCMUnusableStdoutPath(t *testing.T) {
	for name, file := range map[string]string{
		"UNC":      `\\198.51.100.9\share\stdout`,
		"variable": `\\?\%home%\LogFiles\stdout`,
		"parent":   `..\..\..\stdout`,
	} {
		t.Run(name, func(t *testing.T) {
			e := ancmHost("true", file)
			found, gaps := iisDetect(e)
			if len(byImporter(found, "ancm-stdout")) != 0 {
				t.Errorf("findings = %+v", found)
			}
			if _, ok := gapFor(gaps, "W3SVC1 console log"); !ok {
				t.Errorf("gaps = %+v", gaps)
			}
			noSecrets(t, gaps)
		})
	}
}

func TestIISANCMWebConfigProblems(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		e := ancmHost("true", `.\logs\stdout`)
		e.FS.Deny(`C:\inetpub\wwwroot\web-a\web.config`)
		_, gaps := iisDetect(e)
		wantGap(t, gaps, "W3SVC1 console log", discover.ReasonNeedsElevation)
	})
	t.Run("missing", func(t *testing.T) {
		e := fake.New()
		putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
		addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
		found, gaps := iisDetect(e)
		if len(found) != 1 || len(gaps) != 0 {
			t.Errorf("findings = %+v gaps = %+v", found, gaps)
		}
	})
	t.Run("unparsed", func(t *testing.T) {
		e := ancmHost("true", `.\logs\stdout`)
		bad := `<!DOCTYPE c [<!ENTITY a "` + sentinel + `">]><configuration/>`
		e.FS.AddFile(`C:\inetpub\wwwroot\web-a\web.config`, int64(len(bad)), day, []byte(bad))
		_, gaps := iisDetect(e)
		wantGap(t, gaps, "W3SVC1 console log", discover.ReasonConfigUnparsed)
		noSecrets(t, gaps)
	})
	t.Run("share", func(t *testing.T) {
		e := fake.New()
		putAppHost(e, appHostXML(siteXML("1", `\\198.51.100.9\content\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
		addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
		found, gaps := iisDetect(e)
		if len(found) != 1 || len(gaps) != 0 {
			t.Errorf("a content share is skipped quietly: findings = %+v gaps = %+v", found, gaps)
		}
	})
}

func TestIISSentinelNeverAppears(t *testing.T) {
	e := ancmHost("true", `.\logs\stdout`)
	addFiles(e, `C:\inetpub\wwwroot\web-a\logs`, stdoutHead, "stdout_20010911000000_1234.log")
	e.FS.AddFile(`C:\Windows\System32\LogFiles\HTTPERR\httperr1.log`, 100, day, []byte(httpErrHead))
	e.Registry.Set(httpErrKey, "ErrorLoggingDir", `%windir%\System32\LogFiles`)
	scan := discover.Run(context.Background(), e.Env(all.Registry()), []discover.Detector{
		discover.NewIISDetector(discover.DefaultIISRoots()), discover.NewHTTPErrDetector(discover.DefaultIISRoots()),
	})
	if len(scan.Findings) < 3 {
		t.Fatalf("findings = %+v gaps = %+v", scan.Findings, scan.Gaps)
	}
	noSecrets(t, scan)
	for _, f := range scan.Findings {
		for _, p := range f.Paths() {
			if strings.Contains(p, sentinel) {
				t.Errorf("path holds the marker: %q", p)
			}
		}
	}
}

func TestHTTPErrFromRegistry(t *testing.T) {
	t.Run("parent folder, expanded", func(t *testing.T) {
		e := fake.New()
		e.Registry.Set(httpErrKey, "ErrorLoggingDir", `%SystemRoot%\System32\LogFiles`)
		addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, httpErrHead, "httperr1.log", "httperr2.log", "notes.txt")
		found, gaps := httpErrDetect(e)
		if len(gaps) != 0 || len(found) != 1 {
			t.Fatalf("findings = %+v gaps = %+v", found, gaps)
		}
		f := found[0]
		if f.Importer != "iis-httperr" || f.Detector != "httperr" || f.Files != 2 || f.Confidence != 90 || f.Shows != "discover.httperr.shows" ||
			f.Display != "httperr2.log and 1 more" || f.Kind != discover.KindLogSet || len(f.Sites) != 0 {
			t.Errorf("finding = %+v", f)
		}
		fake.AssertFindingsValid(t, found, all.Registry())
	})
	t.Run("folder named HTTPERR", func(t *testing.T) {
		e := fake.New()
		e.Registry.Set(httpErrKey, "ErrorLoggingDir", `D:\Logs\HTTPERR`)
		addFiles(e, `D:\Logs\HTTPERR`, httpErrHead, "httperr1.log")
		found, _ := httpErrDetect(e)
		if len(found) != 1 || found[0].Confidence != 90 || found[0].Kind != discover.KindLogFile {
			t.Errorf("findings = %+v", found)
		}
	})
	t.Run("files directly in the folder", func(t *testing.T) {
		e := fake.New()
		e.Registry.Set(httpErrKey, "ErrorLoggingDir", `D:\HttpLogs`)
		addFiles(e, `D:\HttpLogs`, httpErrHead, "httperr1.log")
		found, _ := httpErrDetect(e)
		if len(found) != 1 || found[0].Confidence != 90 {
			t.Errorf("findings = %+v", found)
		}
	})
	t.Run("declared folder is missing", func(t *testing.T) {
		e := fake.New()
		e.Registry.Set(httpErrKey, "ErrorLoggingDir", `E:\Gone`)
		addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, httpErrHead, "httperr1.log")
		found, gaps := httpErrDetect(e)
		if len(found) != 0 {
			t.Errorf("a missing declared folder falls back to the default: %+v", found)
		}
		wantGap(t, gaps, "HTTP error log", discover.ReasonNotFound)
	})
}

func TestHTTPErrDefaultLocation(t *testing.T) {
	e := fake.New()
	addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, httpErrHead, "httperr1.log")
	found, gaps := httpErrDetect(e)
	if len(gaps) != 0 || len(found) != 1 || found[0].Confidence != 70 {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}

	e = fake.New()
	addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, "not a log\n", "httperr1.log")
	if found, _ = httpErrDetect(e); len(found) != 1 || found[0].Confidence != 40 {
		t.Errorf("name only: %+v", found)
	}
}

func TestHTTPErrNothingThere(t *testing.T) {
	found, gaps := httpErrDetect(fake.New())
	if len(found) != 0 || len(gaps) != 1 || gaps[0].Display != "HTTP error log" || gaps[0].Reason != discover.ReasonNotFound {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}
}

func TestHTTPErrBadRegistryValue(t *testing.T) {
	for name, val := range map[string]string{
		"other variable": `%USERPROFILE%\Logs`,
		"UNC":            `\\198.51.100.9\share\http`,
	} {
		t.Run(name, func(t *testing.T) {
			e := fake.New()
			e.Registry.Set(httpErrKey, "ErrorLoggingDir", val)
			addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, httpErrHead, "httperr1.log")
			found, gaps := httpErrDetect(e)
			want := discover.ReasonConfigUnparsed
			if name == "UNC" {
				want = discover.ReasonUnsafePath
			}
			wantGap(t, gaps, "HTTP error log", want)
			if len(found) != 1 || found[0].Confidence != 70 {
				t.Errorf("the default folder is still checked: %+v", found)
			}
			noSecrets(t, gaps)
		})
	}
}

func TestHTTPErrRegistryDeniedUsesDefault(t *testing.T) {
	e := fake.New()
	e.Registry.Set(httpErrKey, "ErrorLoggingDir", `D:\HttpLogs`)
	e.Registry.Deny(httpErrKey, "ErrorLoggingDir")
	addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, httpErrHead, "httperr1.log")
	found, gaps := httpErrDetect(e)
	if len(found) != 1 || len(gaps) != 0 {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}
}

func TestHTTPErrDeniedGapContract(t *testing.T) {
	e := fake.New()
	e.Registry.Set(httpErrKey, "ErrorLoggingDir", `D:\HttpLogs`)
	addFiles(e, `D:\HttpLogs\HTTPERR`, httpErrHead, "httperr1.log")
	addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, httpErrHead, "httperr1.log")
	fake.AssertDeniedGap(t, discover.NewHTTPErrDetector(discover.DefaultIISRoots()), e)
}

func TestHTTPErrFolderDenied(t *testing.T) {
	e := fake.New()
	addFiles(e, `C:\Windows\System32\LogFiles\HTTPERR`, httpErrHead, "httperr1.log")
	e.FS.Deny(`C:\Windows\System32\LogFiles\HTTPERR`)
	found, gaps := httpErrDetect(e)
	if len(found) != 0 {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "HTTP error log", discover.ReasonNeedsElevation)
}

func TestHTTPErrJunctionIsSkipped(t *testing.T) {
	e := fake.New()
	e.FS.AddReparse(`C:\Windows\System32\LogFiles\HTTPERR`)
	found, gaps := httpErrDetect(e)
	if len(found) != 0 {
		t.Errorf("findings = %+v", found)
	}
	wantGap(t, gaps, "HTTP error log", discover.ReasonUnsafePath)
}

func TestIISRegistryReadIsOnTheAllowlist(t *testing.T) {
	// The fake Registry fails any read outside discover.RegistryAllowed, so a
	// detector that strays shows up as a missing finding here.
	e := fake.New()
	e.Registry.Set(httpErrKey, "ErrorLoggingDir", `D:\HttpLogs`)
	addFiles(e, `D:\HttpLogs\HTTPERR`, httpErrHead, "httperr1.log")
	if found, _ := httpErrDetect(e); len(found) != 1 {
		t.Errorf("findings = %+v", found)
	}
	if !discover.RegistryAllowed(httpErrKey, "ErrorLoggingDir") {
		t.Error("ErrorLoggingDir is not on the allowlist")
	}
}

func TestIISDetectorsHonourContext(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`))+siteXML("2", `D:\Sites\web-b`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	addFiles(e, `D:\Logs\IIS\W3SVC2`, w3cHead, "u_ex010911.log")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	found, _ := discover.NewIISDetector(discover.DefaultIISRoots()).Detect(ctx, e.Env(all.Registry()))
	if len(found) != 0 {
		t.Errorf("a cancelled scan still produced %d findings", len(found))
	}
}

func TestIISRootsOnAnotherDrive(t *testing.T) {
	roots := discover.IISRoots{SystemDrive: "D:", WinDir: `D:\WINNT`}
	e := fake.New()
	putAppHost2 := `<configuration><system.applicationHost><sites><site id="1"><logFile directory="%SystemDrive%\Logs" /></site></sites></system.applicationHost></configuration>`
	e.FS.AddFile(`D:\WINNT\System32\inetsrv\config\applicationHost.config`, int64(len(putAppHost2)), day, []byte(putAppHost2))
	addFiles(e, `D:\Logs\W3SVC1`, w3cHead, "u_ex010911.log")
	found, gaps := discover.NewIISDetector(roots).Detect(context.Background(), e.Env(all.Registry()))
	if len(found) != 1 || len(gaps) != 0 {
		t.Errorf("findings = %+v gaps = %+v", found, gaps)
	}
}

func TestIISRunSortsAndIdentifies(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(siteXML("1", `D:\Sites\web-a`, logFile(`D:\Logs\IIS`)), stockDefaults))
	addFiles(e, `D:\Logs\IIS\W3SVC1`, w3cHead, "u_ex010911.log")
	e.FS.AddFile(`C:\Windows\System32\LogFiles\HTTPERR\httperr1.log`, 100, day, []byte(httpErrHead))
	scan := discover.Run(context.Background(), e.Env(all.Registry()), []discover.Detector{
		discover.NewHTTPErrDetector(discover.DefaultIISRoots()), discover.NewIISDetector(discover.DefaultIISRoots()),
	})
	if len(scan.Findings) != 2 || scan.Findings[0].Importer != "iis-w3c" || scan.Findings[1].Importer != "iis-httperr" {
		t.Fatalf("findings = %+v", scan.Findings)
	}
	for _, f := range scan.Findings {
		if len(f.ID) != 26 {
			t.Errorf("finding id = %q", f.ID)
		}
	}
	fake.AssertFindingsValid(t, scan.Findings, all.Registry())
}
