package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/evtx"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/discover/localread"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
	"api-audit-explorer/internal/sampler"
)

// Everything here is synthetic. discSentinel marks the machine name and the
// configuration secrets that must never reach a response. The log folder name is
// deliberately not the sentinel: the scan offers folder names as portal and API
// suggestions (see internal/discover/suggest.go), so that name may appear.

const (
	discSentinel = "Sentinel-7f3a"
	discLogDir   = `D:\Logs\portal-a\IIS`
	discAppHost  = `C:\Windows\System32\inetsrv\config\applicationHost.config`
)

var (
	discDay  = time.Date(2001, 9, 11, 0, 0, 0, 0, time.UTC)
	discFrom = time.Date(2001, 9, 10, 0, 0, 0, 0, time.UTC)
	discTo   = time.Date(2001, 9, 14, 0, 0, 0, 0, time.UTC)
)

const discW3CHead = "#Software: Microsoft Internet Information Services 10.0\n#Version: 1.0\n#Date: 2001-09-11 00:00:00\n" +
	"#Fields: date time s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) sc-status sc-substatus sc-win32-status time-taken\n"

func discRow(date string, sec int) string {
	return fmt.Sprintf("%s 00:00:%02d 192.0.2.10 GET /api/orders - 443 - 192.0.2.99 Mozilla/5.0 200 0 0 12\n", date, sec)
}

func discSiteXML(id, physical, inner string) string {
	return `<site name="web-a ` + discSentinel + `" id="` + id + `">` +
		`<application path="/" applicationPool="pool-a"><virtualDirectory path="/" physicalPath="` + physical + `" userName="` + discSentinel + `" password="` + discSentinel + `" /></application>` +
		`<bindings><binding protocol="https" bindingInformation="198.51.100.7:443:web-a.example.test ` + discSentinel + `" /></bindings>` +
		inner + `</site>`
}

func discAppHostXML(sites string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><configuration>` +
		`<connectionStrings><add name="db" connectionString="Server=203.0.113.5;User Id=sa;Password=` + discSentinel + `" /></connectionStrings>` +
		`<system.applicationHost><applicationPools><add name="pool-a" managedRuntimeVersion="v4.0">` +
		`<processModel identityType="SpecificUser" userName="` + discSentinel + `" password="` + discSentinel + `" /></add></applicationPools>` +
		`<sites>` + sites + `<siteDefaults><logFile logFormat="W3C" directory="%SystemDrive%\inetpub\logs\LogFiles" />` +
		`<traceFailedRequestsLogging directory="%SystemDrive%\inetpub\logs\FailedReqLogFiles" /></siteDefaults></sites></system.applicationHost></configuration>`
}

/* ------------------------------------------------------------------ fakes */

// discOpener is an in-memory localread.Opener: a file has an identity, a
// modification time and bytes, and a test can deny, swap or grow it.
type discOpener struct {
	mu    sync.Mutex
	files map[string]*discFile
	deny  map[string]bool
	next  uint64
}

type discFile struct {
	data []byte
	id   localread.FileID
	mod  time.Time
}

func newDiscOpener() *discOpener {
	return &discOpener{files: map[string]*discFile{}, deny: map[string]bool{}}
}

func (o *discOpener) put(path, data string, mod time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.next++
	o.files[strings.ToLower(path)] = &discFile{data: []byte(data), id: localread.FileID{VolumeSerial: 0xAB12, FileIndex: o.next}, mod: mod}
}

func (o *discOpener) appendTo(path, more string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f := o.files[strings.ToLower(path)]
	f.data = append(f.data, more...)
}

func (o *discOpener) denyAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for k := range o.files {
		o.deny[k] = true
	}
}

// swap gives every file a new identity: the file is no longer the scanned one.
func (o *discOpener) swap() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, f := range o.files {
		o.next++
		f.id = localread.FileID{VolumeSerial: 0xAB12, FileIndex: o.next}
	}
}

type discSource struct{ *bytes.Reader }

func (discSource) Close() error { return nil }

func (o *discOpener) OpenSource(canonical string, expect localread.FileID) (localread.Source, localread.Info, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	k := strings.ToLower(canonical)
	f := o.files[k]
	if f == nil {
		return nil, localread.Info{}, fmt.Errorf("open: %w", fs.ErrNotExist)
	}
	if o.deny[k] {
		return nil, localread.Info{}, localread.ErrAccessDenied
	}
	if !expect.IsZero() && expect != f.id {
		return nil, localread.Info{}, fmt.Errorf("identity changed: %w", localread.ErrUnsafePath)
	}
	data := append([]byte(nil), f.data...)
	return discSource{bytes.NewReader(data)}, localread.Info{ID: f.id, Size: int64(len(data)), ModTime: f.mod, Base: discover.WinBase(canonical)}, nil
}

type discNoLimit struct{}

func (discNoLimit) Wait(context.Context, int) error { return nil }

// discClock is the server's clock in the tests.
type discClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *discClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *discClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// discSampler is a sampler.Source of a small server.
type discSampler struct {
	mu sync.Mutex
	n  int
}

func (s *discSampler) Cores() int { return 4 }
func (s *discSampler) Close() error {
	return nil
}
func (s *discSampler) Host() (sampler.HostSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.n
	s.n++
	return sampler.HostSample{
		Times:          sampler.HostTimes{Idle: uint64(n) * 750, Kernel: uint64(n) * 1000},
		AvailableBytes: 4_000_000_000 + uint64(n), CommittedBytes: 6_000_000_000,
	}, nil
}

func (s *discSampler) Processes(dst []sampler.ProcSample) ([]sampler.ProcSample, error) {
	s.mu.Lock()
	n := s.n - 1
	s.mu.Unlock()
	have := sampler.HaveTimes | sampler.HaveMemory | sampler.HaveHandles
	start := time.Date(2001, 9, 11, 0, 0, 0, 0, time.UTC)
	return append(dst,
		sampler.ProcSample{PID: 1200, Image: "w3wp.exe", Threads: 10, Have: have, Start: start, CPUSeconds: float64(3 * n), WorkingSet: 500_001_000, PrivateBytes: 500_000_000, Handles: 100},
		sampler.ProcSample{PID: 3300, Image: "sqlservr.exe", Threads: 10, Have: have, Start: start, CPUSeconds: 1, WorkingSet: 900_001_000, PrivateBytes: 900_000_000, Handles: 100},
	), nil
}

// discGate blocks the channel list until released, so a scan stays running.
type discGate struct {
	discover.EventChannels
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	rel     sync.Once
}

func (g *discGate) List() ([]string, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return g.EventChannels.List()
}

func (g *discGate) open() { g.rel.Do(func() { close(g.release) }) }

/* ---------------------------------------------------------------- fixture */

type discQuery struct {
	channel  string
	from, to time.Time
	after    uint64
}

type discFix struct {
	t   *testing.T
	e   *caseEnv
	fe  *fake.Env
	op  *discOpener
	clk *discClock
	src *discSampler
	cfg DiscoverConfig

	qmu   sync.Mutex
	recs  map[string][]evtx.Record
	qerr  error
	calls []discQuery
}

func (f *discFix) query(_ context.Context, w io.Writer, channel string, from, to time.Time, after uint64) (evtx.Result, error) {
	f.qmu.Lock()
	defer f.qmu.Unlock()
	f.calls = append(f.calls, discQuery{channel, from, to, after})
	if f.qerr != nil {
		return evtx.Result{}, f.qerr
	}
	rw := evtx.NewRowWriter(w)
	for _, r := range f.recs[channel] {
		if r.RecordID <= after || r.Time.Before(from) || !r.Time.Before(to) {
			continue
		}
		if err := rw.Write(r); err != nil {
			return evtx.Result{}, err
		}
	}
	if err := rw.Flush(); err != nil {
		return evtx.Result{}, err
	}
	return evtx.Result{Rows: rw.Rows(), LastRecordID: rw.LastRecordID()}, nil
}

func (f *discFix) addRecords(channel string, ids ...uint64) {
	f.qmu.Lock()
	defer f.qmu.Unlock()
	for _, id := range ids {
		f.recs[channel] = append(f.recs[channel], evtx.Record{
			Time: time.Date(2001, 9, 11, 1, int(id), 0, 0, time.UTC), Computer: "web-a", Channel: channel,
			Provider: "Example-Provider", EventID: 1000, Level: 2, RecordID: id,
		})
	}
}

func (f *discFix) queryCalls() []discQuery {
	f.qmu.Lock()
	defer f.qmu.Unlock()
	return append([]discQuery(nil), f.calls...)
}

func (f *discFix) addLog(name, content string, mod time.Time) string {
	path := discLogDir + `\W3SVC1\` + name
	f.fe.FS.AddFile(path, int64(len(content)), mod, []byte(content))
	f.op.put(path, content, mod)
	return path
}

// newDiscFix builds a server over a fake host: one IIS site with two log files
// (three requests each), the Application and System channels, a host name that
// maps to web-a through hosts.json, and a fake clock.
func newDiscFix(t *testing.T, tweak func(*DiscoverConfig), opts ...Option) *discFix {
	t.Helper()
	f := &discFix{
		t: t, fe: fake.New(), op: newDiscOpener(), src: &discSampler{},
		clk:  &discClock{t: time.Date(2001, 9, 15, 12, 0, 0, 0, time.UTC)},
		recs: map[string][]evtx.Record{},
	}
	f.fe.Machine.NameList = []string{discSentinel + "-HOST", "web-a.example.test"}
	f.fe.Channels.Names = []string{"Application", "System"}
	xml := discAppHostXML(discSiteXML("1", `D:\Sites\web-a`, `<logFile directory="`+discLogDir+`" />`))
	f.fe.FS.AddFile(discAppHost, int64(len(xml)), discDay, []byte(xml))
	f.addLog("u_ex010911.log", discW3CHead+discRow("2001-09-11", 1)+discRow("2001-09-11", 2)+discRow("2001-09-11", 3), discDay)
	f.addLog("u_ex010912.log", discW3CHead+discRow("2001-09-12", 1)+discRow("2001-09-12", 2)+discRow("2001-09-12", 3), discDay.AddDate(0, 0, 1))
	f.cfg = DiscoverConfig{
		Env: f.fe.Env(nil), Opener: f.op, Query: f.query, Limiter: discNoLimit{}, OS: "windows",
		Now: f.clk.now, SamplerSource: f.src, SamplerFlush: time.Millisecond,
	}
	if tweak != nil {
		tweak(&f.cfg)
	}
	spool := t.TempDir()
	options := []Option{
		WithDiscover(f.cfg),
		withJobHooks(func(j *importJobs) { j.spoolDir = spool }),
	}
	options = append(options, opts...)
	f.e = newCaseEnv(t, envConfig{importers: all.Importers(), options: options})
	t.Cleanup(func() { f.e.handler().stopSamplerAll() })
	return f
}

// noLeak fails when a response holds a path, the machine name or a secret.
func noLeak(t *testing.T, what, body string) {
	t.Helper()
	low := strings.ToLower(body)
	for _, bad := range []string{"sentinel-7f3a", `d:\`, `c:\`, `\\`, "web-a.example", "inetpub", "198.51.100", "203.0.113"} {
		if strings.Contains(low, bad) {
			t.Errorf("%s: the response holds %q: %s", what, bad, body)
		}
	}
}

// req sends a request with the token and checks the answer for leaks.
func (f *discFix) req(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	res := f.e.json(method, path, body)
	noLeak(f.t, method+" "+path, res.Body.String())
	return res
}

func (f *discFix) startScan() string {
	f.t.Helper()
	res := f.req(http.MethodPost, "/api/discover/scans", `{}`)
	if res.Code != http.StatusAccepted {
		f.t.Fatalf("start scan = %d %s", res.Code, res.Body)
	}
	var out struct {
		ScanID string `json:"scanId"`
	}
	decodeBody(f.t, res, &out)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(out.ScanID) {
		f.t.Fatalf("scan id %q is not 32 lower-case hex digits", out.ScanID)
	}
	return out.ScanID
}

func (f *discFix) getScan(id string) scanDTO {
	f.t.Helper()
	res := f.req(http.MethodGet, "/api/discover/scans/"+id, "")
	if res.Code != http.StatusOK {
		f.t.Fatalf("get scan = %d %s", res.Code, res.Body)
	}
	var out scanDTO
	decodeBody(f.t, res, &out)
	return out
}

func (f *discFix) waitScan(id string) scanDTO {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if sc := f.getScan(id); sc.State != scanRunning {
			return sc
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatal("scan did not finish")
	return scanDTO{}
}

func (f *discFix) scan() (string, scanDTO) {
	f.t.Helper()
	id := f.startScan()
	sc := f.waitScan(id)
	if sc.State != scanDone {
		f.t.Fatalf("scan state = %q", sc.State)
	}
	return id, sc
}

func findingWhere(t *testing.T, sc scanDTO, what string, pred func(discover.Finding) bool) discover.Finding {
	t.Helper()
	for _, fd := range sc.Findings {
		if pred(fd) {
			return fd
		}
	}
	t.Fatalf("no %s finding in %+v", what, sc.Findings)
	return discover.Finding{}
}

func (f *discFix) iisFinding(sc scanDTO) discover.Finding {
	return findingWhere(f.t, sc, "iis-w3c", func(fd discover.Finding) bool { return fd.Importer == "iis-w3c" })
}

func (f *discFix) channelFinding(sc scanDTO, name string) discover.Finding {
	return findingWhere(f.t, sc, "channel "+name, func(fd discover.Finding) bool {
		return fd.Kind == discover.KindEventChannel && strings.Contains(fd.Display, name)
	})
}

func discBody(scan string, ids []string, from, to time.Time, mut ...func(map[string]any)) string {
	m := map[string]any{
		"scanId": scan, "findingIds": ids, "window": map[string]any{"from": from, "to": to},
		"ackGating": true, "ackText": evidence.GatingBanner(),
	}
	for _, f := range mut {
		f(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func importsPath(caseID string) string { return "/api/cases/" + caseID + "/discover/imports" }

// importJobs posts an import and waits for each job.
func (f *discFix) importJobs(caseID, scan string, ids []string, from, to time.Time) []importJobDTO {
	f.t.Helper()
	res := f.req(http.MethodPost, importsPath(caseID), discBody(scan, ids, from, to))
	if res.Code != http.StatusAccepted {
		f.t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	var ans discoverImportAnswer
	decodeBody(f.t, res, &ans)
	if len(ans.Jobs) != len(ids) {
		f.t.Fatalf("jobs = %+v, want %d", ans.Jobs, len(ids))
	}
	var out []importJobDTO
	for i, ref := range ans.Jobs {
		if ref.FindingID != ids[i] || ref.JobID == "" {
			f.t.Fatalf("job ref %d = %+v", i, ref)
		}
		out = append(out, f.settle(caseID, ref.JobID))
	}
	return out
}

func (f *discFix) settle(caseID, job string) importJobDTO {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res := f.req(http.MethodGet, jobPath(caseID, job), "")
		if res.Code != http.StatusOK {
			f.t.Fatalf("get job = %d %s", res.Code, res.Body)
		}
		var out importJobDTO
		decodeBody(f.t, res, &out)
		if out.State == jobDone || out.State == jobFailed {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatal("job did not finish")
	return importJobDTO{}
}

func (f *discFix) wantDone(j importJobDTO) discoverResult {
	f.t.Helper()
	if j.State != jobDone || j.Status != http.StatusCreated {
		f.t.Fatalf("job = %s status %d failure %+v result %s", j.State, j.Status, j.Failure, j.Result)
	}
	var out discoverResult
	if err := json.Unmarshal(j.Result, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *discFix) wantFailed(j importJobDTO, status int, reason string) {
	f.t.Helper()
	if j.State != jobFailed || j.Status != status || j.Failure == nil || j.Failure.Reason != reason {
		f.t.Fatalf("job = %s status %d failure %+v, want %d %s", j.State, j.Status, j.Failure, status, reason)
	}
}

func wantReason(t *testing.T, res *httptest.ResponseRecorder, status int, reason string) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("status = %d %s, want %d %s", res.Code, res.Body, status, reason)
	}
	if got := failureOf(t, res).Reason; got != reason {
		t.Fatalf("reason = %q, want %q (%s)", got, reason, res.Body)
	}
}

/* ----------------------------------------------------------- capabilities */

func TestDiscoverCapabilities(t *testing.T) {
	f := newDiscFix(t, nil)
	res := f.req(http.MethodGet, "/api/discover/capabilities", "")
	var caps capabilitiesDTO
	decodeBody(t, res, &caps)
	if res.Code != http.StatusOK || !caps.Supported || caps.Elevated || caps.OS != "windows" {
		t.Fatalf("capabilities = %d %+v", res.Code, caps)
	}

	g := newDiscFix(t, func(c *DiscoverConfig) {})
	g.fe.Machine.Admin = true
	decodeBody(t, g.req(http.MethodGet, "/api/discover/capabilities", ""), &caps)
	if !caps.Elevated {
		t.Fatalf("an administrator is not reported: %+v", caps)
	}
}

func TestDiscoverUnsupportedComputer(t *testing.T) {
	plain := newCaseEnv(t, envConfig{importers: all.Importers()})
	unsupported := newCaseEnv(t, envConfig{importers: all.Importers(), options: []Option{WithDiscover(DiscoverConfig{Unsupported: true, OS: "linux"})}})
	for name, e := range map[string]*caseEnv{"no config": plain, "unsupported": unsupported} {
		var caps capabilitiesDTO
		res := e.get("/api/discover/capabilities")
		decodeBody(t, res, &caps)
		if res.Code != http.StatusOK || caps.Supported || caps.Elevated || caps.OS == "" {
			t.Fatalf("%s: capabilities = %d %+v", name, res.Code, caps)
		}
		if name == "no config" && caps.OS != runtime.GOOS {
			t.Fatalf("os = %q", caps.OS)
		}
		wantReason(t, e.json(http.MethodPost, "/api/discover/scans", `{}`), http.StatusNotImplemented, "not-supported-os")
		wantReason(t, e.json(http.MethodGet, "/api/discover/scans/"+strings.Repeat("a", 32), ""), http.StatusNotImplemented, "not-supported-os")
		id := e.newCase("c")
		wantReason(t, e.json(http.MethodPost, importsPath(id), discBody(strings.Repeat("a", 32), []string{"abcd"}, discFrom, discTo)), http.StatusNotImplemented, "not-supported-os")
		wantReason(t, e.json(http.MethodPost, "/api/cases/"+id+"/sampler", `{"intervalSec":5,"ackGating":true,"ackText":`+mustJSON(evidence.GatingBanner())+`}`), http.StatusNotImplemented, "not-supported-os")
	}
}

/* ------------------------------------------------------------ scan routes */

func TestDiscoverScanLifecycle(t *testing.T) {
	f := newDiscFix(t, nil)
	id, sc := f.scan()
	if !sc.Complete || sc.StartedAt.IsZero() {
		t.Fatalf("scan = %+v", sc)
	}
	if sc.Host.Label != "web-a" || sc.Host.Proposed || !strings.HasSuffix(sc.Host.TZ, "UTC") {
		t.Fatalf("host = %+v", sc.Host)
	}
	iis := f.iisFinding(sc)
	if iis.Kind != discover.KindLogSet || iis.Files != 2 || iis.HostLabel != "web-a" || iis.ID == "" {
		t.Fatalf("iis finding = %+v", iis)
	}
	f.channelFinding(sc, "Application")
	f.channelFinding(sc, "System")
	live := findingWhere(t, sc, "live series", func(fd discover.Finding) bool { return fd.Kind == discover.KindLiveSeries })
	if live.Importer != "win-live" {
		t.Fatalf("live finding = %+v", live)
	}
	for _, fd := range sc.Findings {
		if fd.HostLabel != "web-a" {
			t.Errorf("finding %q names host %q", fd.Display, fd.HostLabel)
		}
	}
	if len(sc.Progress) == 0 {
		t.Fatal("no progress")
	}
	for _, p := range sc.Progress {
		if p.State != progDone {
			t.Errorf("progress %+v", p)
		}
	}
	// The scan answer has no field named like a path.
	res := f.req(http.MethodGet, "/api/discover/scans/"+id, "")
	if strings.Contains(strings.ToLower(res.Body.String()), `"path`) {
		t.Errorf("the scan names a path: %s", res.Body)
	}

	if res := f.req(http.MethodDelete, "/api/discover/scans/"+id, ""); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body)
	}
	wantReason(t, f.req(http.MethodGet, "/api/discover/scans/"+id, ""), http.StatusNotFound, "scan-unknown")
	wantReason(t, f.req(http.MethodDelete, "/api/discover/scans/"+id, ""), http.StatusNotFound, "scan-unknown")
	// Another scan can start once the first is gone.
	f.scan()
}

func TestDiscoverScanExpiresAfterAnHour(t *testing.T) {
	f := newDiscFix(t, nil)
	id, _ := f.scan()
	f.clk.advance(59 * time.Minute)
	f.getScan(id)
	f.clk.advance(2 * time.Minute)
	wantReason(t, f.req(http.MethodGet, "/api/discover/scans/"+id, ""), http.StatusNotFound, "scan-expired")
	cid := f.e.newCase("c")
	wantReason(t, f.req(http.MethodPost, importsPath(cid), discBody(id, []string{"abcd1234"}, discFrom, discTo)), http.StatusNotFound, "scan-expired")
	wantReason(t, f.req(http.MethodDelete, "/api/discover/scans/"+id, ""), http.StatusNotFound, "scan-expired")
}

func TestDiscoverUnknownScanIDs(t *testing.T) {
	f := newDiscFix(t, nil)
	for _, id := range []string{strings.Repeat("a", 32), "zz", strings.Repeat("A", 32), strings.Repeat("a", 33)} {
		wantReason(t, f.req(http.MethodGet, "/api/discover/scans/"+id, ""), http.StatusNotFound, "scan-unknown")
	}
	// A path with a further segment is no scan route at all.
	if res := f.req(http.MethodGet, "/api/discover/scans/"+strings.Repeat("a", 32)+"/x", ""); res.Code != http.StatusNotFound {
		t.Fatalf("extra segment = %d", res.Code)
	}
}

func TestDiscoverKeepsFourScans(t *testing.T) {
	f := newDiscFix(t, nil)
	var ids []string
	for i := 0; i < 5; i++ {
		id, _ := f.scan()
		ids = append(ids, id)
		f.clk.advance(time.Second)
	}
	wantReason(t, f.req(http.MethodGet, "/api/discover/scans/"+ids[0], ""), http.StatusNotFound, "scan-expired")
	for _, id := range ids[1:] {
		f.getScan(id)
	}
}

func TestDiscoverOneScanAtATime(t *testing.T) {
	gate := &discGate{entered: make(chan struct{}), release: make(chan struct{})}
	f := newDiscFix(t, func(c *DiscoverConfig) {
		gate.EventChannels = c.Env.Channels
		c.Env.Channels = gate
	})
	t.Cleanup(gate.open)
	id := f.startScan()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the scan did not start")
	}
	wantReason(t, f.req(http.MethodPost, "/api/discover/scans", `{}`), http.StatusConflict, "scan-busy")
	if sc := f.getScan(id); sc.State != scanRunning || len(sc.Findings) != 0 {
		t.Fatalf("running scan = %+v", sc)
	}
	// A scan that has not finished cannot be imported from.
	cid := f.e.newCase("c")
	wantReason(t, f.req(http.MethodPost, importsPath(cid), discBody(id, []string{"abcd1234"}, discFrom, discTo)), http.StatusConflict, "scan-not-ready")
	gate.open()
	if sc := f.waitScan(id); sc.State != scanDone {
		t.Fatalf("scan = %+v", sc)
	}
	f.startScan()
}

func TestDiscoverDeleteCancelsARunningScan(t *testing.T) {
	gate := &discGate{entered: make(chan struct{}), release: make(chan struct{})}
	f := newDiscFix(t, func(c *DiscoverConfig) {
		gate.EventChannels = c.Env.Channels
		c.Env.Channels = gate
	})
	t.Cleanup(gate.open)
	id := f.startScan()
	<-gate.entered
	if res := f.req(http.MethodDelete, "/api/discover/scans/"+id, ""); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", res.Code)
	}
	wantReason(t, f.req(http.MethodGet, "/api/discover/scans/"+id, ""), http.StatusNotFound, "scan-unknown")
	gate.open()
	f.e.handler().disc.wg.Wait()
	f.scan()
}

func TestDiscoverDeniedFolderIsAGapNotAFinding(t *testing.T) {
	f := newDiscFix(t, nil)
	f.fe.FS.Deny(`D:\Logs`)
	_, sc := f.scan()
	for _, fd := range sc.Findings {
		if fd.Importer == "iis-w3c" {
			t.Fatalf("a denied folder gave a finding: %+v", fd)
		}
	}
	found := false
	for _, g := range sc.Gaps {
		if g.Reason == discover.ReasonNeedsElevation {
			found = true
		}
	}
	if !found {
		t.Fatalf("no needs-elevation gap in %+v", sc.Gaps)
	}
}

/* ---------------------------------------------------------- import routes */

func TestDiscoverImportRefusals(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	iis := f.iisFinding(sc)
	live := findingWhere(t, sc, "live series", func(fd discover.Finding) bool { return fd.Kind == discover.KindLiveSeries })
	ids := []string{iis.ID}

	cases := []struct {
		name   string
		body   string
		status int
		reason string
	}{
		{"no acknowledgement", discBody(sid, ids, discFrom, discTo, func(m map[string]any) { m["ackGating"] = false }), 400, "ack-required"},
		{"no ack text", discBody(sid, ids, discFrom, discTo, func(m map[string]any) { m["ackText"] = "" }), 400, "ack-required"},
		{"wrong ack text", discBody(sid, ids, discFrom, discTo, func(m map[string]any) { m["ackText"] = "I agree" }), 400, "ack-mismatch"},
		{"reversed window", discBody(sid, ids, discTo, discFrom), 400, "bad-window"},
		{"empty window", discBody(sid, ids, discFrom, discFrom), 400, "bad-window"},
		{"window over 31 days", discBody(sid, ids, discFrom, discFrom.Add(31*24*time.Hour+time.Second)), 400, "bad-window"},
		{"no window", discBody(sid, ids, discFrom, discTo, func(m map[string]any) { delete(m, "window") }), 400, "bad-window"},
		{"no findings", discBody(sid, []string{}, discFrom, discTo), 400, "bad-findings"},
		{"too many findings", discBody(sid, manyIDs(65), discFrom, discTo), 400, "bad-findings"},
		{"repeated finding", discBody(sid, []string{iis.ID, iis.ID}, discFrom, discTo), 400, "bad-findings"},
		{"malformed finding", discBody(sid, []string{"a b"}, discFrom, discTo), 400, "bad-findings"},
		{"path as finding", discBody(sid, []string{`D:\Logs\x.log`}, discFrom, discTo), 400, "bad-findings"},
		{"short finding", discBody(sid, []string{"abc"}, discFrom, discTo), 400, "bad-findings"},
		{"unknown scan", discBody(strings.Repeat("b", 32), ids, discFrom, discTo), 404, "scan-unknown"},
		{"malformed scan", discBody("not-a-scan", ids, discFrom, discTo), 404, "scan-unknown"},
		{"forged finding", discBody(sid, []string{"forged-id-1234"}, discFrom, discTo), 404, "finding-unknown"},
		{"one forged among real", discBody(sid, []string{iis.ID, "forged-id-1234"}, discFrom, discTo), 404, "finding-unknown"},
		{"live series", discBody(sid, []string{live.ID}, discFrom, discTo), 422, "live-series-use-sampler"},
		{"live series among real", discBody(sid, []string{iis.ID, live.ID}, discFrom, discTo), 422, "live-series-use-sampler"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantReason(t, f.req(http.MethodPost, importsPath(cid), c.body), c.status, c.reason)
		})
	}
	if n := len(f.e.importsOf(cid)); n != 0 {
		t.Fatalf("refused requests stored %d imports", n)
	}
	if f.e.slotsHeld() != 0 {
		t.Fatalf("limiter slots = %d", f.e.slotsHeld())
	}
}

func manyIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("finding-%04d", i)
	}
	return out
}

func TestDiscoverFindingOfAnotherScanIsRefused(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	first, sc := f.scan()
	iis := f.iisFinding(sc)
	second, _ := f.scan()
	wantReason(t, f.req(http.MethodPost, importsPath(cid), discBody(second, []string{iis.ID}, discFrom, discTo)), http.StatusNotFound, "finding-unknown")
	// The first scan still knows its own finding.
	if jobs := f.importJobs(cid, first, []string{iis.ID}, discFrom, discTo); len(jobs) != 1 {
		t.Fatal("no job")
	}
}

func TestDiscoverImportIntoAClosedCaseIsRefused(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	if res := f.req(http.MethodPost, "/api/cases/"+cid+"/close", ""); res.Code != http.StatusOK {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	wantReason(t, f.req(http.MethodPost, importsPath(cid), discBody(sid, []string{f.iisFinding(sc).ID}, discFrom, discTo)), http.StatusConflict, "case-closed")
	wantReason(t, f.req(http.MethodPost, importsPath("aaaaaaaaaaaaaaaaaaaaaaaaaa"), discBody(sid, []string{"abcd1234"}, discFrom, discTo)), http.StatusNotFound, "case-not-found")
}

func TestDiscoverUnmappedHostIsRefused(t *testing.T) {
	f := newDiscFix(t, nil)
	f.fe.Machine.NameList = []string{"unmapped-box"}
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	if !sc.Host.Proposed || sc.Host.Label != "local" {
		t.Fatalf("host = %+v", sc.Host)
	}
	wantReason(t, f.req(http.MethodPost, importsPath(cid), discBody(sid, []string{f.iisFinding(sc).ID}, discFrom, discTo)), http.StatusUnprocessableEntity, string(importer.ReasonUnknownHost))
	wantReason(t, f.req(http.MethodPost, "/api/cases/"+cid+"/sampler", samplerBody(5)), http.StatusUnprocessableEntity, string(importer.ReasonUnknownHost))
}

func TestDiscoverProposalOffersTheMachineNamesAsAliases(t *testing.T) {
	f := newDiscFix(t, nil)
	f.fe.Machine.NameList = []string{" BOX-Z ", "box-z", "", "local", "box-z.example.test", "b", "c", "d"}
	_, sc := f.scan()
	if !sc.Host.Proposed {
		t.Fatalf("host = %+v", sc.Host)
	}
	want := []string{"BOX-Z", "box-z.example.test", "b", "c"}
	if len(sc.Host.Aliases) != len(want) {
		t.Fatalf("aliases = %v, want %v", sc.Host.Aliases, want)
	}
	for i := range want {
		if sc.Host.Aliases[i] != want[i] {
			t.Fatalf("aliases = %v, want %v", sc.Host.Aliases, want)
		}
	}
}

func TestDiscoverChannelNeedsAQueryFunc(t *testing.T) {
	f := newDiscFix(t, func(c *DiscoverConfig) { c.Query = nil })
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	wantReason(t, f.req(http.MethodPost, importsPath(cid), discBody(sid, []string{f.channelFinding(sc, "Application").ID}, discFrom, discTo)), http.StatusNotImplemented, "not-supported-os")
}

func TestDiscoverIISImportAdvancesTheWatermark(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	iis := f.iisFinding(sc)

	// First pull: both files, three requests each.
	res := f.wantDone(f.importJobs(cid, sid, []string{iis.ID}, discFrom, discTo)[0])
	if res.Importer != "iis-w3c" || res.Files != 2 || res.Record == nil || res.Record.Rows != 6 || res.Record.HostLabel != "web-a" || len(res.Gaps) != 0 {
		t.Fatalf("first pull = %+v record %+v", res, res.Record)
	}
	if res.Record.FileName != iis.Display {
		t.Errorf("record file name = %q, want the finding's display %q", res.Record.FileName, iis.Display)
	}
	evs, _ := f.e.eventsOf(cid, "")
	if len(evs) != 6 {
		t.Fatalf("events = %d, want 6", len(evs))
	}
	if n := len(f.e.importsOf(cid)); n != 2 {
		t.Fatalf("imports = %d, want one per file", n)
	}

	// Second pull of the same files, same scan: nothing is new.
	f.wantFailed(f.importJobs(cid, sid, []string{iis.ID}, discFrom, discTo)[0], http.StatusConflict, "duplicate")
	if evs, _ := f.e.eventsOf(cid, ""); len(evs) != 6 {
		t.Fatalf("events after a repeat = %d", len(evs))
	}

	// The newest file grows. A fresh scan finds the same files; the watermark
	// lives in the case, so only the new request comes in.
	newest := discLogDir + `\W3SVC1\u_ex010912.log`
	f.op.appendTo(newest, discRow("2001-09-12", 10))
	sid2, sc2 := f.scan()
	res = f.wantDone(f.importJobs(cid, sid2, []string{f.iisFinding(sc2).ID}, discFrom, discTo)[0])
	if res.Files != 1 || res.Record.Rows != 1 {
		t.Fatalf("third pull = files %d rows %d", res.Files, res.Record.Rows)
	}
	evs, _ = f.e.eventsOf(cid, "")
	if len(evs) != 7 {
		t.Fatalf("events = %d, want 7", len(evs))
	}
	starts := map[time.Time]bool{}
	for _, e := range evs {
		starts[e.Start] = true
	}
	if len(starts) != 7 {
		t.Fatalf("the pulls overlap: %d distinct request times in 7 events", len(starts))
	}
	if n := len(f.e.importsOf(cid)); n != 3 {
		t.Fatalf("imports = %d, want 3", n)
	}

	// The audit table has one row per file read and no path.
	c, err := f.e.store.Open(context.Background(), casestore.CaseID(cid))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rows, err := c.(casestore.Discoverer).DiscoverReads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.Result)
		if r.Importer != "iis-w3c" || r.Kind != string(discover.KindLogSet) || r.Display != iis.Display || len(r.PathHMAC) != 64 || len(r.BytesSHA256) != 64 && r.Result != "empty" {
			t.Errorf("audit row = %+v", r)
		}
		if !r.WindowFrom.Equal(discFrom) || !r.WindowTo.Equal(discTo) {
			t.Errorf("audit window = %v..%v", r.WindowFrom, r.WindowTo)
		}
		noLeak(t, "audit row", fmt.Sprintf("%+v", r))
	}
	if want := "ok ok empty empty empty ok"; strings.Join(got, " ") != want {
		t.Fatalf("audit results = %v, want %s", got, want)
	}
}

func TestDiscoverWindowTrimsTheImport(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	// Only the first day.
	res := f.wantDone(f.importJobs(cid, sid, []string{f.iisFinding(sc).ID}, discFrom, discDay.Add(12*time.Hour))[0])
	if res.Record.Rows != 3 {
		t.Fatalf("rows = %d, want the three of the first day", res.Record.Rows)
	}
	evs, _ := f.e.eventsOf(cid, "")
	for _, e := range evs {
		if !e.Start.Before(discDay.Add(12 * time.Hour)) {
			t.Errorf("event at %v is outside the window", e.Start)
		}
	}
}

func TestDiscoverDeniedAndSwappedFiles(t *testing.T) {
	t.Run("access denied at import", func(t *testing.T) {
		f := newDiscFix(t, nil)
		cid := f.e.newCase("c")
		sid, sc := f.scan()
		f.op.denyAll()
		f.wantFailed(f.importJobs(cid, sid, []string{f.iisFinding(sc).ID}, discFrom, discTo)[0], http.StatusForbidden, "needs-elevation")
		if n := len(f.e.importsOf(cid)); n != 0 {
			t.Fatalf("imports = %d", n)
		}
	})
	t.Run("file swapped after the scan", func(t *testing.T) {
		f := newDiscFix(t, nil)
		cid := f.e.newCase("c")
		sid, sc := f.scan()
		f.op.swap()
		f.wantFailed(f.importJobs(cid, sid, []string{f.iisFinding(sc).ID}, discFrom, discTo)[0], http.StatusUnprocessableEntity, "unsafe-path")
		if n := len(f.e.importsOf(cid)); n != 0 {
			t.Fatalf("imports = %d", n)
		}
	})
}

func TestDiscoverEventChannelImport(t *testing.T) {
	f := newDiscFix(t, nil)
	f.addRecords("Application", 10, 11, 12)
	cid := f.e.newCase("c")
	spool := f.e.handler().jobs.spoolDir
	sid, sc := f.scan()
	app := f.channelFinding(sc, "Application")

	res := f.wantDone(f.importJobs(cid, sid, []string{app.ID}, discFrom, discTo)[0])
	if res.Importer != "win-events" || res.Record == nil || res.Record.Rows != 3 || res.Files != 1 {
		t.Fatalf("first pull = %+v record %+v", res, res.Record)
	}
	calls := f.queryCalls()
	if len(calls) != 1 || calls[0].after != 0 || !calls[0].from.Equal(discFrom) || !calls[0].to.Equal(discTo) || !strings.Contains(app.Display, calls[0].channel) {
		t.Fatalf("query calls = %+v", calls)
	}
	if evs, _ := f.e.eventsOf(cid, ""); len(evs) != 3 {
		t.Fatalf("events = %d", len(evs))
	}

	// Nothing new: the query starts after record 12 and finds none.
	f.wantFailed(f.importJobs(cid, sid, []string{app.ID}, discFrom, discTo)[0], http.StatusConflict, "duplicate")
	if calls := f.queryCalls(); len(calls) != 2 || calls[1].after != 12 {
		t.Fatalf("query calls = %+v", calls)
	}

	// A new record comes in after the watermark.
	f.addRecords("Application", 13)
	res = f.wantDone(f.importJobs(cid, sid, []string{app.ID}, discFrom, discTo)[0])
	if res.Record.Rows != 1 {
		t.Fatalf("rows = %d", res.Record.Rows)
	}
	if calls := f.queryCalls(); len(calls) != 3 || calls[2].after != 12 {
		t.Fatalf("query calls = %+v", calls)
	}
	if evs, _ := f.e.eventsOf(cid, ""); len(evs) != 4 {
		t.Fatalf("events = %d", len(evs))
	}
	f.wantFailed(f.importJobs(cid, sid, []string{app.ID}, discFrom, discTo)[0], http.StatusConflict, "duplicate")
	if calls := f.queryCalls(); calls[3].after != 13 {
		t.Fatalf("the watermark did not move to 13: %+v", calls)
	}

	// The spool is gone after every pull.
	if entries, err := os.ReadDir(spool); err != nil || len(entries) != 0 {
		t.Fatalf("spool = %v %v", entries, err)
	}

	// Refusals of the reader map to fixed answers; no text of the error leaks.
	f.qerr = evtx.ErrAccessDenied
	f.wantFailed(f.importJobs(cid, sid, []string{app.ID}, discFrom, discTo)[0], http.StatusForbidden, "needs-elevation")
	f.qerr = errors.New(`query failed on C:\` + discSentinel + `\x`)
	f.wantFailed(f.importJobs(cid, sid, []string{app.ID}, discFrom, discTo)[0], http.StatusInternalServerError, "import-failed")
	f.qerr = evtx.ErrChannelRefused
	f.wantFailed(f.importJobs(cid, sid, []string{app.ID}, discFrom, discTo)[0], http.StatusUnprocessableEntity, "channel-refused")
	if entries, _ := os.ReadDir(spool); len(entries) != 0 {
		t.Fatalf("spool = %v", entries)
	}
}

func TestDiscoverImportsSeveralFindingsInOrder(t *testing.T) {
	f := newDiscFix(t, nil)
	f.addRecords("Application", 10)
	f.addRecords("System", 20)
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	ids := []string{f.iisFinding(sc).ID, f.channelFinding(sc, "Application").ID, f.channelFinding(sc, "System").ID}
	jobs := f.importJobs(cid, sid, ids, discFrom, discTo)
	for _, j := range jobs {
		f.wantDone(j)
	}
	if f.e.slotsHeld() != 0 {
		f.e.waitSlotsHeld(0)
	}
}

func TestDiscoverImportsAreLimited(t *testing.T) {
	f := newDiscFix(t, nil, withImportLimit(1))
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	// Hold the only slot as a running import would.
	slot, ok := f.e.handler().imports.tryAcquire()
	if !ok {
		t.Fatal("no slot")
	}
	res := f.req(http.MethodPost, importsPath(cid), discBody(sid, []string{f.iisFinding(sc).ID}, discFrom, discTo))
	wantReason(t, res, http.StatusTooManyRequests, "import-busy")
	slot()
	f.importJobs(cid, sid, []string{f.iisFinding(sc).ID}, discFrom, discTo)
}

/* ---------------------------------------------------------------- sampler */

func samplerBody(interval int) string {
	return fmt.Sprintf(`{"intervalSec":%d,"ackGating":true,"ackText":%s}`, interval, mustJSON(evidence.GatingBanner()))
}

func samplerPath(id string) string { return "/api/cases/" + id + "/sampler" }

type samplerAnswer struct {
	State      string `json:"state"`
	Since      string `json:"since"`
	Samples    int    `json:"samples"`
	StopReason string `json:"stopReason"`
}

func (f *discFix) sampler(id string) samplerAnswer {
	f.t.Helper()
	res := f.req(http.MethodGet, samplerPath(id), "")
	if res.Code != http.StatusOK {
		f.t.Fatalf("get sampler = %d %s", res.Code, res.Body)
	}
	var out samplerAnswer
	decodeBody(f.t, res, &out)
	return out
}

func TestSamplerValidation(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	for _, iv := range []int{0, 1, 4, 7, 10, 61, 300, -5} {
		wantReason(t, f.req(http.MethodPost, samplerPath(cid), samplerBody(iv)), http.StatusBadRequest, "bad-interval")
	}
	wantReason(t, f.req(http.MethodPost, samplerPath(cid), `{"intervalSec":5,"ackGating":false,"ackText":"x"}`), http.StatusBadRequest, "ack-required")
	wantReason(t, f.req(http.MethodPost, samplerPath(cid), `{"intervalSec":5,"ackGating":true}`), http.StatusBadRequest, "ack-required")
	wantReason(t, f.req(http.MethodPost, samplerPath(cid), `{"intervalSec":5,"ackGating":true,"ackText":"I agree"}`), http.StatusBadRequest, "ack-mismatch")
	wantReason(t, f.req(http.MethodPost, samplerPath("aaaaaaaaaaaaaaaaaaaaaaaaaa"), samplerBody(5)), http.StatusNotFound, "case-not-found")
	if st := f.sampler(cid); st.State != "idle" {
		t.Fatalf("sampler = %+v after refusals", st)
	}
}

func TestSamplerStartStopAndBusy(t *testing.T) {
	f := newDiscFix(t, nil)
	cid, other := f.e.newCase("one"), f.e.newCase("two")

	if st := f.sampler(cid); st.State != "idle" {
		t.Fatalf("sampler = %+v", st)
	}
	// Stopping nothing is fine.
	if res := f.req(http.MethodDelete, samplerPath(cid), ""); res.Code != http.StatusNoContent {
		t.Fatalf("delete idle = %d", res.Code)
	}

	res := f.req(http.MethodPost, samplerPath(cid), samplerBody(60))
	var started samplerAnswer
	decodeBody(t, res, &started)
	if res.Code != http.StatusAccepted || started.State != "recording" {
		t.Fatalf("start = %d %s", res.Code, res.Body)
	}
	if st := f.sampler(cid); st.State != "recording" || st.Since == "" || st.StopReason != "" {
		t.Fatalf("sampler = %+v", st)
	}
	// One sampler per process: the same case and another case are both refused.
	wantReason(t, f.req(http.MethodPost, samplerPath(cid), samplerBody(5)), http.StatusConflict, "sampler-busy")
	wantReason(t, f.req(http.MethodPost, samplerPath(other), samplerBody(5)), http.StatusConflict, "sampler-busy")
	if st := f.sampler(other); st.State != "idle" {
		t.Fatalf("the other case shows %+v", st)
	}
	// Another case's stop does not stop this recording.
	if res := f.req(http.MethodDelete, samplerPath(other), ""); res.Code != http.StatusNoContent {
		t.Fatalf("delete other = %d", res.Code)
	}
	if st := f.sampler(cid); st.State != "recording" {
		t.Fatalf("sampler = %+v after another case's stop", st)
	}

	if res := f.req(http.MethodDelete, samplerPath(cid), ""); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body)
	}
	if st := f.sampler(cid); st.State != "stopped" || st.StopReason != "" {
		t.Fatalf("sampler = %+v after the user stopped it", st)
	}
	// A new recording may follow, in any case.
	if res := f.req(http.MethodPost, samplerPath(other), samplerBody(15)); res.Code != http.StatusAccepted {
		t.Fatalf("restart = %d %s", res.Code, res.Body)
	}
	if st := f.sampler(cid); st.State != "idle" {
		t.Fatalf("the first case shows %+v after another recording started", st)
	}
	f.req(http.MethodDelete, samplerPath(other), "")
}

// A stale tab that presses Close gets M2 and must not stop a recording it had no right to stop.
func TestStaleCloseDoesNotStopTheSampler(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	if res := f.req(http.MethodPost, samplerPath(cid), samplerBody(60)); res.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", res.Code, res.Body)
	}
	wantFailure(t, f.e.believing(http.MethodPost, "/api/cases/"+cid+"/close", "", "closed"), 409, "case-changed", m2Text)
	if st := f.sampler(cid); st.State != "recording" {
		t.Fatalf("sampler = %+v after a refused close", st)
	}
	if res := f.e.believing(http.MethodPost, "/api/cases/"+cid+"/close", "", "open"); res.Code != http.StatusOK {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	if st := f.sampler(cid); st.State != "stopped" || st.StopReason != "case-closed" {
		t.Fatalf("sampler = %+v after the real close", st)
	}
}

func TestSamplerStopsWhenTheCaseCloses(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	if res := f.req(http.MethodPost, samplerPath(cid), samplerBody(60)); res.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", res.Code, res.Body)
	}
	if res := f.req(http.MethodPost, "/api/cases/"+cid+"/close", ""); res.Code != http.StatusOK {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	if st := f.sampler(cid); st.State != "stopped" || st.StopReason != "case-closed" {
		t.Fatalf("sampler = %+v", st)
	}
	// A closed case takes no recording.
	wantReason(t, f.req(http.MethodPost, samplerPath(cid), samplerBody(5)), http.StatusConflict, "case-closed")
}

func TestSamplerStopsWhenTheCaseIsErased(t *testing.T) {
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	if res := f.req(http.MethodPost, samplerPath(cid), samplerBody(60)); res.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", res.Code, res.Body)
	}
	if res := f.req(http.MethodDelete, "/api/cases/"+cid, `{"confirm":"`+cid+`"}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	wantReason(t, f.req(http.MethodGet, samplerPath(cid), ""), http.StatusNotFound, "case-not-found")
	// The process is free for another recording.
	other := f.e.newCase("two")
	if res := f.req(http.MethodPost, samplerPath(other), samplerBody(60)); res.Code != http.StatusAccepted {
		t.Fatalf("start after erase = %d %s", res.Code, res.Body)
	}
}

// The first batch of a real recording takes one interval (5 s at least).
func TestSamplerStoresBatchesIntoTheCase(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for a 5 second tick")
	}
	f := newDiscFix(t, nil)
	cid := f.e.newCase("c")
	if res := f.req(http.MethodPost, samplerPath(cid), samplerBody(5)); res.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", res.Code, res.Body)
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(f.e.importsOf(cid)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no batch was stored: sampler %+v", f.sampler(cid))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st := f.sampler(cid); st.State != "recording" || st.Samples < 1 {
		t.Fatalf("sampler = %+v", st)
	}
	if res := f.req(http.MethodDelete, samplerPath(cid), ""); res.Code != http.StatusNoContent {
		t.Fatalf("stop = %d", res.Code)
	}
	imps := f.e.importsOf(cid)
	if imps[0].Importer != "win-live" || imps[0].HostLabel != "web-a" || imps[0].Rows == 0 {
		t.Fatalf("import = %+v", imps[0])
	}
	evs, _ := f.e.eventsOf(cid, "")
	if len(evs) == 0 {
		t.Fatal("no events")
	}
	seen := false
	for _, e := range evs {
		for _, v := range e.Attrs {
			if strings.Contains(v, "w3wp") {
				seen = true
			}
		}
		if e.Kind == "w3wp" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("no sample of w3wp in %d events: %+v", len(evs), evs[0])
	}
	if st := f.sampler(cid); st.State != "stopped" || st.StopReason != "" {
		t.Fatalf("sampler = %+v", st)
	}
}

func TestLiveImporterIsNotListedAsAnImporter(t *testing.T) {
	f := newDiscFix(t, nil)
	res := f.req(http.MethodGet, "/api/importers", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "iis-w3c") {
		t.Fatalf("importers = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "win-live") {
		t.Fatalf("win-live is listed: %s", res.Body)
	}
}

/* ------------------------------------------------------------------- auth */

func TestDiscoverRoutesNeedTheTokenAndALocalOrigin(t *testing.T) {
	f := newDiscFix(t, nil)
	scan := strings.Repeat("a", 32)
	cid := "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/discover/capabilities", ""},
		{http.MethodPost, "/api/discover/scans", `{}`},
		{http.MethodGet, "/api/discover/scans/" + scan, ""},
		{http.MethodDelete, "/api/discover/scans/" + scan, ""},
		{http.MethodPost, importsPath(cid), discBody(scan, []string{"abcd1234"}, discFrom, discTo)},
		{http.MethodPost, samplerPath(cid), samplerBody(5)},
		{http.MethodGet, samplerPath(cid), ""},
		{http.MethodDelete, samplerPath(cid), ""},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			// No token.
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
			req.Header.Set("Content-Type", "application/json")
			if res := f.e.serve(req); res.Code != http.StatusUnauthorized {
				t.Errorf("no token = %d %s", res.Code, res.Body)
			}
			// A wrong token.
			req = httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Audit-Token", "wrong")
			if res := f.e.serve(req); res.Code != http.StatusUnauthorized {
				t.Errorf("wrong token = %d", res.Code)
			}
			// The token on a foreign Host.
			req = httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Audit-Token", testToken)
			req.Host = "evil.example:8765"
			res := httptest.NewRecorder()
			f.e.h.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden {
				t.Errorf("foreign host = %d %s", res.Code, res.Body)
			}
		})
	}
	if f.e.slotsHeld() != 0 || f.sampler(f.e.newCase("c")).State != "idle" {
		t.Error("a refused request had an effect")
	}
}

/* ------------------------------------------------------- window on any source */

const discHTTPERRHead = "#Software: Microsoft HTTP API 2.0\n#Version: 1.0\n#Date: 2001-09-11 00:00:00\n" +
	"#Fields: date time c-ip c-port s-ip s-port cs-version cs-method cs-uri sc-status s-siteid s-reason s-queuename\n"

func discHTTPERRRow(stamp string) string {
	return stamp + " 192.0.2.50 51000 192.0.2.10 443 HTTP/1.1 GET /api/orders 503 1 QueueFull pool-a\n"
}

// addHTTPERR puts a synthetic HTTP.sys error log in its default folder, so the
// scan finds it as an iis-httperr finding.
func (f *discFix) addHTTPERR(content string) {
	f.t.Helper()
	path := `C:\Windows\System32\LogFiles\HTTPERR\httperr1.log`
	f.fe.FS.AddFile(path, int64(len(content)), discDay, []byte(content))
	f.op.put(path, content, discDay)
}

func (f *discFix) httperrFinding(sc scanDTO) discover.Finding {
	return findingWhere(f.t, sc, "iis-httperr", func(fd discover.Finding) bool { return fd.Importer == "iis-httperr" })
}

// A window that holds none of the log's rows adds nothing and stores nothing.
func TestDiscoverWindowOutsideTheLogAddsNothing(t *testing.T) {
	f := newDiscFix(t, nil)
	f.addHTTPERR(discHTTPERRHead + discHTTPERRRow("2001-09-11 08:00:01") + discHTTPERRRow("2001-09-11 10:00:02") + discHTTPERRRow("2001-09-11 12:00:03"))
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	id := f.httperrFinding(sc).ID

	after := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	f.wantFailed(f.importJobs(cid, sid, []string{id}, after, after.Add(48*time.Hour))[0], http.StatusConflict, "duplicate")
	before := time.Date(2001, 8, 1, 0, 0, 0, 0, time.UTC)
	f.wantFailed(f.importJobs(cid, sid, []string{id}, before, before.Add(48*time.Hour))[0], http.StatusConflict, "duplicate")
	if evs, _ := f.e.eventsOf(cid, ""); len(evs) != 0 {
		t.Fatalf("events = %d, want none", len(evs))
	}
	if n := len(f.e.importsOf(cid)); n != 0 {
		t.Fatalf("imports = %d, want none", n)
	}
	// The refusal stored nothing, so the same bytes still import in a window that holds them.
	res := f.wantDone(f.importJobs(cid, sid, []string{id}, discFrom, discTo)[0])
	if res.Record == nil || res.Record.Rows != 3 {
		t.Fatalf("record = %+v, want all three rows", res.Record)
	}
}

func TestDiscoverWindowKeepsOnlyTheRowsInside(t *testing.T) {
	f := newDiscFix(t, nil)
	f.addHTTPERR(discHTTPERRHead + discHTTPERRRow("2001-09-11 08:00:01") + discHTTPERRRow("2001-09-11 10:00:02") +
		discHTTPERRRow("2001-09-11 10:00:04") + discHTTPERRRow("2001-09-11 12:00:03"))
	cid := f.e.newCase("c")
	sid, sc := f.scan()
	from, to := time.Date(2001, 9, 11, 9, 0, 0, 0, time.UTC), time.Date(2001, 9, 11, 11, 0, 0, 0, time.UTC)
	res := f.wantDone(f.importJobs(cid, sid, []string{f.httperrFinding(sc).ID}, from, to)[0])
	if res.Record == nil || res.Record.Rows != 2 {
		t.Fatalf("record = %+v, want the two rows inside the window", res.Record)
	}
	evs, _ := f.e.eventsOf(cid, "")
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2", len(evs))
	}
	for _, e := range evs {
		if e.Start.Before(from) || !e.Start.Before(to) {
			t.Errorf("event at %v is outside the window", e.Start)
		}
	}
}

// A source with no line-time probe cannot be cut by time before it is read, so
// the import itself drops what is outside the window, counts it and says so.
func TestImportRunWindowFiltersAnImporterWithoutAProbe(t *testing.T) {
	toy := toyImporter{name: "toy", score: 90}
	if _, ok := importer.ProbeFunc(toy, importer.Options{}); ok {
		t.Fatal("the stand-in importer has a prober")
	}
	data := toyCSV(toyRow(5, "web-a", "a", 1), toyRow(10, "web-a", "b", 1), toyRow(20, "web-a", "c", 1), toyRow(50, "web-a", "d", 1))
	at := func(sec int) time.Time { return time.Date(2001, 1, 1, 0, 0, sec, 0, time.UTC) }
	run := func(win importer.Window) ([]evidence.Event, importer.Report, error) {
		opts := importer.Options{Host: &importer.HostHint{Label: "web-a"}, Window: win}
		var got []evidence.Event
		rep, err := importRun(context.Background(), toy, opts, false, new(string))(strings.NewReader(data), func(e evidence.Event) error {
			got = append(got, e)
			return nil
		})
		return got, rep, err
	}

	evs, rep, err := run(importer.Window{From: at(8), To: at(30)})
	if err != nil || len(evs) != 2 || rep.Emitted != 2 || rep.Rows != 2 {
		t.Fatalf("window inside: events %d emitted %d rows %d err %v", len(evs), rep.Emitted, rep.Rows, err)
	}
	if !rep.First.Equal(at(10)) || !rep.Last.Equal(at(20)) {
		t.Errorf("coverage = %v..%v, want the kept rows", rep.First, rep.Last)
	}
	if len(rep.KnownGaps) != 1 || rep.KnownGaps[0] != importer.GapOutsideWindow {
		t.Errorf("known gaps = %v, want the outside-window gap", rep.KnownGaps)
	}

	if _, _, err := run(importer.Window{From: at(55), To: at(59)}); !errors.Is(err, importer.ErrOutOfWindow) {
		t.Fatalf("window outside: err = %v, want ErrOutOfWindow", err)
	}

	evs, rep, err = run(importer.Window{})
	if err != nil || len(evs) != 4 || len(rep.KnownGaps) != 0 {
		t.Fatalf("open window: events %d gaps %v err %v", len(evs), rep.KnownGaps, err)
	}
}
