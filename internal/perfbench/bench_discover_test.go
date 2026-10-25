package perfbench

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer/all"
)

const (
	benchW3CHead = "#Software: Microsoft Internet Information Services 10.0\n#Version: 1.0\n#Date: 2001-09-11 00:00:00\n" +
		"#Fields: date time s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) sc-status sc-substatus sc-win32-status time-taken\n" +
		"2001-09-11 00:00:01 192.0.2.10 GET /api/orders - 443 - 192.0.2.99 Mozilla/5.0 200 0 0 12\n"
	benchHTTPErrHead = "#Software: Microsoft HTTP API 2.0\n#Version: 1.0\n#Date: 2001-09-11 00:00:00\n" +
		"#Fields: date time c-ip c-port s-ip s-port cs-version cs-method cs-uri sc-status s-siteid s-reason s-queuename\n" +
		"2001-09-11 00:00:01 192.0.2.99 51000 192.0.2.10 443 HTTP/1.1 GET /api/orders 503 1 Disabled -\n"
	benchFrebHead = `<failedRequest url="http://web-a.example.test:80/api/orders" siteId="1" appPoolId="pool-a" processId="1234" verb="GET" ` +
		`failureReason="STATUS_CODE" statusCode="500.0" triggerStatusCode="500" timeTaken="120">` + "\n"
	benchLog4net = "2001-10-25 14:02:30,950 [12] INFO  Synthetic.Api.OrdersController - Request handled for alice@example.test\n" +
		"2001-10-25 14:02:31,400 [17] ERROR Synthetic.Api.OrdersController - Unhandled error for order-1\n"
	benchAppHostPath = `C:\Windows\System32\inetsrv\config\applicationHost.config`
)

// benchEnv builds a fake host with 5000 log files: 3000 IIS site logs over 10
// sites, 500 failed request traces, 500 HTTP.sys logs and 1000 application logs.
func benchEnv() *fake.Env {
	e := fake.New()
	day := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	site := func(i int) string { return "W3SVC" + strconv.Itoa(i) }

	xml := `<?xml version="1.0" encoding="UTF-8"?><configuration><system.applicationHost><sites>`
	for i := 1; i <= 10; i++ {
		xml += `<site name="web-a-site-` + strconv.Itoa(i) + `" id="` + strconv.Itoa(i) + `"><application path="/" applicationPool="pool-a">` +
			`<virtualDirectory path="/" physicalPath="C:\inetpub\site` + strconv.Itoa(i) + `" /></application></site>`
	}
	xml += `<siteDefaults><logFile logFormat="W3C" directory="%SystemDrive%\inetpub\logs\LogFiles" />` +
		`<traceFailedRequestsLogging directory="%SystemDrive%\inetpub\logs\FailedReqLogFiles" /></siteDefaults></sites></system.applicationHost></configuration>`
	e.FS.AddFile(benchAppHostPath, int64(len(xml)), day, []byte(xml))

	for s := 1; s <= 10; s++ {
		for i := 0; i < 300; i++ {
			name := day.AddDate(0, 0, i).Format("060102")
			e.FS.AddFile(`C:\inetpub\logs\LogFiles\`+site(s)+`\u_ex`+name+`.log`, 1<<20, day.AddDate(0, 0, i), []byte(benchW3CHead))
		}
	}
	for i := 0; i < 500; i++ {
		e.FS.AddFile(fmt.Sprintf(`C:\inetpub\logs\FailedReqLogFiles\W3SVC1\fr%06d.xml`, i), 40<<10, day.Add(time.Duration(i)*time.Minute), []byte(benchFrebHead))
	}
	for i := 0; i < 500; i++ {
		e.FS.AddFile(fmt.Sprintf(`C:\Windows\System32\LogFiles\HTTPERR\httperr%d.log`, i+1), 1<<20, day.AddDate(0, 0, i), []byte(benchHTTPErrHead))
	}
	cfg := `<?xml version="1.0" encoding="utf-8"?><configuration><log4net><appender name="Rolling" type="log4net.Appender.RollingFileAppender">` +
		`<file value="logs\app.log" /><datePattern value="'.'yyyyMMdd" /></appender><root><level value="INFO" /><appender-ref ref="Rolling" /></root></log4net></configuration>`
	e.FS.AddFile(`C:\inetpub\shop\web.config`, int64(len(cfg)), day, []byte(cfg))
	for i := 0; i < 1000; i++ {
		e.FS.AddFile(`C:\inetpub\shop\logs\app.log.`+day.AddDate(0, 0, i).Format("20060102"), 1<<20, day.AddDate(0, 0, i), []byte(benchLog4net))
	}
	e.Registry.Set(`HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`, "ErrorLoggingDir", `C:\Windows\System32\LogFiles\HTTPERR`)
	return e
}

// TestBenchDiscover scans the fake host with the IIS, HTTP.sys, application-log,
// host and web detectors. The fake file system is in memory, so this measures the
// detectors and the walk, not a disk.
func TestBenchDiscover(t *testing.T) {
	need(t)
	e := benchEnv()
	reg := all.Registry()
	detectors := []discover.Detector{
		discover.NewIISDetector(discover.DefaultIISRoots()),
		discover.NewHTTPErrDetector(discover.DefaultIISRoots()),
		discover.NewAppLog(nil, `C:\inetpub\shop`),
		discover.NewHost(discover.HostOptions{}),
		discover.NewWeb(),
	}
	for run := 1; run <= 3; run++ {
		m := startMeter("")
		scan := discover.Run(context.Background(), e.Env(reg), detectors)
		meas := m.finish()
		files := 0
		byDet := map[string]int{}
		for _, f := range scan.Findings {
			files += len(f.Paths())
			byDet[f.Detector+"/"+string(f.Kind)] += f.Files
		}
		if run == 1 {
			t.Logf("files covered by findings (detector/kind): %v", byDet)
		}
		report(t, fmt.Sprintf("discover scan of 5000 fake files (run %d)", run), meas,
			fmt.Sprintf("findings=%d files-in-findings=%d gaps=%d complete=%v scan-wall=%s fs-calls=%v",
				len(scan.Findings), files, len(scan.Gaps), scan.Complete, scan.Finished.Sub(scan.Started).Round(time.Millisecond), e.FS.Calls))
	}
}
