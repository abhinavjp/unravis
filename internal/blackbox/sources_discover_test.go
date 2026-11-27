package blackbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// A fake machine: environment, small files and folder listings in memory. Keys are lower case with backslashes.

type fakeEntry struct {
	name string
	size int64
	mod  time.Time
	mode fs.FileMode
}

func (e fakeEntry) Name() string               { return e.name }
func (e fakeEntry) IsDir() bool                { return e.mode.IsDir() }
func (e fakeEntry) Type() fs.FileMode          { return e.mode.Type() }
func (e fakeEntry) Info() (fs.FileInfo, error) { return e, nil }
func (e fakeEntry) Size() int64                { return e.size }
func (e fakeEntry) Mode() fs.FileMode          { return e.mode }
func (e fakeEntry) ModTime() time.Time         { return e.mod }
func (e fakeEntry) Sys() any                   { return nil }

func fFile(name string, size int64, unix int64) fs.DirEntry {
	return fakeEntry{name: name, size: size, mod: time.Unix(unix, 0)}
}
func fDir(name string) fs.DirEntry  { return fakeEntry{name: name, mode: fs.ModeDir} }
func fLink(name string) fs.DirEntry { return fakeEntry{name: name, size: 5, mode: fs.ModeSymlink} }

type fakeMachine struct {
	vars   map[string]string // lower case names
	files  map[string]string
	dirs   map[string][]fs.DirEntry
	dirErr map[string]error
	sql    []sqlLogInstance
	redis  []string
}

func fk(p string) string {
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(p, "/", `\`), `\`))
}

func newMachine() *fakeMachine {
	return &fakeMachine{
		vars:   map[string]string{"windir": `C:\Windows`, "systemdrive": "C:"},
		files:  map[string]string{},
		dirs:   map[string][]fs.DirEntry{},
		dirErr: map[string]error{},
	}
}

func (m *fakeMachine) addDir(p string, es ...fs.DirEntry) { m.dirs[fk(p)] = es }
func (m *fakeMachine) addFile(p, content string)          { m.files[fk(p)] = content }
func (m *fakeMachine) iis(content string) {
	m.addFile(`C:\Windows\System32\inetsrv\config\applicationHost.config`, content)
}

func (m *fakeMachine) env() SourcesEnv {
	return SourcesEnv{
		Getenv: func(n string) string { return m.vars[strings.ToLower(n)] },
		ReadFile: func(p string, max int64) ([]byte, error) {
			s, ok := m.files[fk(p)]
			if !ok {
				return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
			}
			if int64(len(s)) > max {
				s = s[:max]
			}
			return []byte(s), nil
		},
		ReadDir: func(p string) ([]fs.DirEntry, error) {
			if err, ok := m.dirErr[fk(p)]; ok {
				return nil, &fs.PathError{Op: "open", Path: p, Err: err}
			}
			es, ok := m.dirs[fk(p)]
			if !ok {
				return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
			}
			return es, nil
		},
		SQLLogs:    func() []sqlLogInstance { return m.sql },
		RedisConfs: func() []string { return m.redis },
	}
}

var testNow = time.Date(2026, 11, 27, 21, 0, 0, 0, time.FixedZone("BST", 3600))

func discover(m *fakeMachine, cfg Config) SourcesDoc {
	if cfg.Folder == "" {
		cfg.Folder = `D:\UnravisBlackbox`
	}
	return DiscoverSources(m.env(), cfg, "HOST1", "1.2.3", testNow)
}

func find(t *testing.T, doc SourcesDoc, id string) SourceEntry {
	t.Helper()
	for _, e := range doc.Sources {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("entry %q not found in %v", id, ids(doc))
	return SourceEntry{}
}

func hasSource(doc SourcesDoc, id string) bool {
	for _, e := range doc.Sources {
		if e.ID == id {
			return true
		}
	}
	return false
}

func ids(doc SourcesDoc) []string {
	var out []string
	for _, e := range doc.Sources {
		out = append(out, e.ID)
	}
	return out
}

func hasKind(doc SourcesDoc, kind string) bool {
	for _, e := range doc.Sources {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func iisXML(defaults, sites, logSection string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><configuration><system.applicationHost><sites>` +
		`<siteDefaults><logFile ` + defaults + `/></siteDefaults>` + sites + `</sites>` + logSection +
		`</system.applicationHost></configuration>`
}

const defSite = `<site name="Default Web Site" id="1"><bindings/></site>`

func TestSourcesHeaderAndRecordings(t *testing.T) {
	m := newMachine()
	m.addDir(`D:\UnravisBlackbox`,
		fFile("bb-2026-11-26.ndjson", 100, 1000),
		fFile("BB-2026-11-27.NDJSON", 50, 2000),
		fFile("notes.txt", 999, 3000),
		fDir("bb-folder.ndjson"),
		fLink("bb-link.ndjson"),
	)
	doc := discover(m, Config{})
	if doc.Schema != SourcesSchema || doc.Host != "HOST1" || doc.Version != "1.2.3" {
		t.Fatalf("header: %+v", doc)
	}
	if !doc.Generated.Equal(testNow) || doc.Generated.Location() != time.UTC {
		t.Fatalf("generated not UTC now: %v", doc.Generated)
	}
	r := find(t, doc, "recordings")
	if r.Kind != SourceRecordings || r.Dir != `D:\UnravisBlackbox` || r.Pattern != "bb-*.ndjson" || r.Origin != "settings" {
		t.Fatalf("recordings: %+v", r)
	}
	if !r.Exists || !r.Readable || r.Files != 2 || r.Bytes != 150 || r.Newest != 2000 || r.Err != "" {
		t.Fatalf("recordings counts: %+v", r)
	}
}

func TestSourcesIISDefaultSite(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, defSite, ``))
	m.addDir(`C:\inetpub\logs\LogFiles\W3SVC1`, fFile("u_ex261127.log", 10, 5), fFile("x.txt", 1, 9))
	e := find(t, discover(m, Config{}), "iis.site.1")
	if e.Kind != SourceIIS || e.Label != "IIS site Default Web Site" || e.Pattern != "*.log" || e.Origin != "applicationHost.config" {
		t.Fatalf("entry: %+v", e)
	}
	if e.Dir != `C:\inetpub\logs\LogFiles\W3SVC1` || e.Files != 1 || e.Bytes != 10 || e.Newest != 5 {
		t.Fatalf("entry: %+v", e)
	}
}

func TestSourcesIISMissingFolderStillListed(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, defSite, ``))
	e := find(t, discover(m, Config{}), "iis.site.1")
	if e.Exists || e.Readable || e.Err != "not-found" {
		t.Fatalf("entry: %+v", e)
	}
}

func TestSourcesIISSiteDirectoryOverride(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, `<site name="A" id="1"/><site name="B" id="2"><logFile directory="E:\Logs\B/"/></site>`, ``))
	doc := discover(m, Config{})
	if d := find(t, doc, "iis.site.1").Dir; d != `C:\inetpub\logs\LogFiles\W3SVC1` {
		t.Fatalf("site 1: %s", d)
	}
	if d := find(t, doc, "iis.site.2").Dir; d != `E:\Logs\B\W3SVC2` {
		t.Fatalf("site 2: %s", d)
	}
}

func TestSourcesIISDefaultsRelocatedWithVariable(t *testing.T) {
	m := newMachine()
	m.vars["systemdrive"] = "D:"
	m.iis(iisXML(`directory="%SystemDrive%\Logs\IIS"`, defSite, ``))
	if d := find(t, discover(m, Config{}), "iis.site.1").Dir; d != `D:\Logs\IIS\W3SVC1` {
		t.Fatalf("dir: %s", d)
	}
}

func TestSourcesIISSystemDriveFallsBackToWindirDrive(t *testing.T) {
	m := newMachine()
	delete(m.vars, "systemdrive")
	m.iis(iisXML(``, defSite, ``))
	if d := find(t, discover(m, Config{}), "iis.site.1").Dir; d != `C:\inetpub\logs\LogFiles\W3SVC1` {
		t.Fatalf("dir: %s", d)
	}
}

func TestSourcesIISUnknownVariableLeavesEntryOut(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, `<site name="A" id="1"><logFile directory="%NOPE%\x"/></site><site name="B" id="2"/>`, ``))
	doc := discover(m, Config{})
	if hasSource(doc, "iis.site.1") || !hasSource(doc, "iis.site.2") {
		t.Fatalf("ids: %v", ids(doc))
	}
}

func TestSourcesIISCentral(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(`centralLogFileMode="CentralW3C"`, defSite+`<site name="B" id="2"/>`,
		`<log><centralW3CLogFile directory="F:\Central"/><centralBinaryLogFile directory="F:\Bin"/></log>`))
	doc := discover(m, Config{})
	e := find(t, doc, "iis.central")
	if e.Dir != `F:\Central` || e.Pattern != "*.log" || e.Label != "IIS central log" {
		t.Fatalf("entry: %+v", e)
	}
	for _, id := range ids(doc) {
		if strings.HasPrefix(id, "iis.site.") {
			t.Fatalf("per-site entry with central logging: %v", ids(doc))
		}
	}

	m.iis(iisXML(`centralLogFileMode="CentralBinary"`, defSite,
		`<log><centralW3CLogFile directory="F:\Central"/><centralBinaryLogFile directory="F:\Bin"/></log>`))
	e = find(t, discover(m, Config{}), "iis.central")
	if e.Dir != `F:\Bin` || e.Pattern != "*.log|*.ibl" {
		t.Fatalf("binary entry: %+v", e)
	}
}

func TestSourcesIISLoggingOff(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, `<site name="Quiet" id="3"><logFile enabled="false" logFormat="W3C"/></site>`, ``))
	e := find(t, discover(m, Config{}), "iis.site.3")
	if e.Label != "IIS site Quiet (logging off)" {
		t.Fatalf("label: %q", e.Label)
	}
}

func TestSourcesIISBadXML(t *testing.T) {
	for name, content := range map[string]string{
		"garbage":   "this is not xml <<<",
		"truncated": `<configuration><system.applicationHost><sites><site name="A" id="1"><logFile directory="C:\x"`,
		"empty":     "",
		"mismatch":  `<configuration><system.applicationHost></sites></configuration>`,
	} {
		m := newMachine()
		m.iis(content)
		doc := discover(m, Config{})
		if hasKind(doc, SourceIIS) {
			t.Errorf("%s: iis entries from bad xml: %v", name, ids(doc))
		}
		if !hasSource(doc, "recordings") {
			t.Errorf("%s: run failed as a whole", name)
		}
	}
	// No ReadFile at all, and no config file.
	doc := DiscoverSources(SourcesEnv{}, Config{Folder: `D:\X`}, "h", "v", testNow)
	if hasKind(doc, SourceIIS) || !hasSource(doc, "recordings") {
		t.Fatalf("empty env: %v", ids(doc))
	}
}

func TestSourcesIISRefusesBadDirectories(t *testing.T) {
	bad := []string{
		`\\server\share\logs`, `logs\iis`, `\logs`, `C:logs`, `D:\a\..\b`, `D:\..`, `//server/share`,
		`C:\a|b`, `C:\a\b:stream`,
	}
	for _, d := range bad {
		m := newMachine()
		m.iis(iisXML(``, `<site name="A" id="1"><logFile directory="`+d+`"/></site>`, ``))
		if doc := discover(m, Config{}); hasSource(doc, "iis.site.1") {
			t.Errorf("accepted %q", d)
		}
	}
	m := newMachine()
	m.iis(iisXML(`centralLogFileMode="CentralW3C"`, defSite, `<log><centralW3CLogFile directory="\\srv\logs"/></log>`))
	if doc := discover(m, Config{}); hasKind(doc, SourceIIS) {
		t.Errorf("accepted UNC central dir: %v", ids(doc))
	}
}

func TestSourcesIISNonNumericSiteIDSkipped(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, `<site name="A" id="1\..\x"/><site name="B" id=""/><site name="C" id="7"/>`, ``))
	got := ids(discover(m, Config{}))
	for _, id := range got {
		if strings.HasPrefix(id, "iis.site.") && id != "iis.site.7" {
			t.Fatalf("ids: %v", got)
		}
	}
}

func TestSourcesHTTPErrAndEventLogs(t *testing.T) {
	m := newMachine()
	doc := discover(m, Config{})
	if hasSource(doc, "httperr") || hasSource(doc, "evt.application") || hasSource(doc, "evt.system") {
		t.Fatalf("defaults listed without a folder: %v", ids(doc))
	}

	m.addDir(`C:\Windows\System32\LogFiles\HTTPERR`, fFile("httperr1.log", 7, 70), fFile("other.log", 1, 1))
	m.addDir(`C:\Windows\System32\winevt\Logs`,
		fFile("Application.evtx", 1000, 10), fFile("System.evtx", 2000, 20), fFile("Security.evtx", 9, 9))
	doc = discover(m, Config{})
	h := find(t, doc, "httperr")
	if h.Kind != SourceHTTPErr || h.Pattern != "httperr*.log" || h.Origin != "default" || h.Files != 1 || h.Bytes != 7 {
		t.Fatalf("httperr: %+v", h)
	}
	a := find(t, doc, "evt.application")
	if a.Kind != SourceWinEvents || a.Channel != "Application" || a.Pattern != "Application.evtx" ||
		a.Dir != `C:\Windows\System32\winevt\Logs` || a.Files != 1 || a.Bytes != 1000 {
		t.Fatalf("application: %+v", a)
	}
	if s := find(t, doc, "evt.system"); s.Channel != "System" || s.Bytes != 2000 {
		t.Fatalf("system: %+v", s)
	}
	if hasSource(doc, "evt.security") {
		t.Fatal("only Application and System")
	}
}

func TestSourcesWindirFallbackAndOtherDrive(t *testing.T) {
	m := newMachine()
	delete(m.vars, "windir")
	m.addDir(`C:\Windows\System32\LogFiles\HTTPERR`)
	if !hasSource(discover(m, Config{}), "httperr") {
		t.Fatal("fallback windir not used")
	}
	m = newMachine()
	m.vars["windir"] = `E:\WINNT`
	m.addDir(`E:\WINNT\System32\LogFiles\HTTPERR`)
	if e := find(t, discover(m, Config{}), "httperr"); e.Dir != `E:\WINNT\System32\LogFiles\HTTPERR` {
		t.Fatalf("dir: %s", e.Dir)
	}
}

func TestSourcesSQL(t *testing.T) {
	m := newMachine()
	m.sql = []sqlLogInstance{
		{Label: "MSSQLSERVER", LogPath: `C:\Program Files\Microsoft SQL Server\MSSQL16.MSSQLSERVER\MSSQL\Log\ERRORLOG`},
		{Label: "Prod_1", LogPath: `D:\SQL\Log\ERRORLOG`},
		{Label: "Bad", LogPath: `\\srv\share\ERRORLOG`},
	}
	m.addDir(`C:\Program Files\Microsoft SQL Server\MSSQL16.MSSQLSERVER\MSSQL\Log`,
		fFile("ERRORLOG", 100, 50), fFile("ERRORLOG.1", 80, 40), fFile("ERRORLOG.2", 70, 30),
		fFile("system_health_0.xel", 5000, 60))
	doc := discover(m, Config{})
	d := find(t, doc, "sql.mssqlserver")
	if d.Kind != SourceSQL || d.Label != "SQL Server MSSQLSERVER error log" || d.Pattern != "ERRORLOG*" || d.Origin != "registry" ||
		d.Dir != `C:\Program Files\Microsoft SQL Server\MSSQL16.MSSQLSERVER\MSSQL\Log` {
		t.Fatalf("default instance: %+v", d)
	}
	if d.Files != 3 || d.Bytes != 250 || d.Newest != 50 {
		t.Fatalf("default instance counts: %+v", d)
	}
	n := find(t, doc, "sql.prod_1")
	if n.Dir != `D:\SQL\Log` || n.Label != "SQL Server Prod_1 error log" || n.Err != "not-found" {
		t.Fatalf("named instance: %+v", n)
	}
	if hasSource(doc, "sql.bad") {
		t.Fatal("UNC log path accepted")
	}
}

func TestSourcesRedis(t *testing.T) {
	m := newMachine()
	m.redis = []string{
		`C:\Program Files\Redis\redis.windows-service.conf`,
		`C:\Program Files\Memurai\memurai.conf`,
		`C:\Redis3\redis.conf`,
		`C:\Redis4\redis.conf`,
		`C:\Redis5\redis.conf`,
	}
	m.addFile(m.redis[0], "# logfile \"C:/ignored/old.log\"\nport 6379\nrequirepass SuperSecret123\nlogfile \"C:/Redis/Logs/redis_log.txt\"\n")
	m.addFile(m.redis[1], "masterauth TopSecretMaster\nlogfile 'Logs/memurai.log'\n")
	m.addFile(m.redis[2], "requirepass Another$ecret\nlogfile stdout\n")
	m.addFile(m.redis[3], "logfile \"\"\n")
	m.addFile(m.redis[4], "requirepass NeverShown\nlogfile \"C:/first.log\"\nlogfile \"D:\\\\Data\\\\last.log\"\n")
	m.addDir(`C:\Redis\Logs`, fFile("redis_log.txt", 11, 100), fFile("redis_log.txt.1", 22, 90), fFile("other.txt", 5, 80))
	doc := discover(m, Config{})

	r1 := find(t, doc, "redis.1")
	if r1.Kind != SourceRedis || r1.Dir != `C:\Redis\Logs` || r1.Pattern != "redis_log.txt*" ||
		r1.Label != "Redis log (redis.windows-service.conf)" || r1.Origin != "redis settings" {
		t.Fatalf("redis.1: %+v", r1)
	}
	if r1.Files != 2 || r1.Bytes != 33 || r1.Newest != 100 {
		t.Fatalf("redis.1 counts: %+v", r1)
	}
	r2 := find(t, doc, "redis.2")
	if r2.Dir != `C:\Program Files\Memurai\Logs` || r2.Pattern != "memurai.log*" {
		t.Fatalf("redis.2 (relative): %+v", r2)
	}
	if hasSource(doc, "redis.3") || hasSource(doc, "redis.4") {
		t.Fatalf("stdout or empty logfile gave an entry: %v", ids(doc))
	}
	r5 := find(t, doc, "redis.5")
	if r5.Dir != `D:\Data` || r5.Pattern != "last.log*" {
		t.Fatalf("redis.5 (last line wins, escaped backslashes): %+v", r5)
	}

	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SuperSecret123", "TopSecretMaster", "Another$ecret", "NeverShown", "requirepass", "masterauth"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("%q leaked into the document: %s", secret, b)
		}
	}
}

func TestSourcesRedisCapAndRejects(t *testing.T) {
	m := newMachine()
	for i := 1; i <= 12; i++ {
		p := fmt.Sprintf(`C:\R%d\redis.conf`, i)
		m.redis = append(m.redis, p)
		m.addFile(p, fmt.Sprintf("logfile \"C:/L%d/r.log\"\n", i))
	}
	doc := discover(m, Config{})
	n := 0
	for _, e := range doc.Sources {
		if e.Kind == SourceRedis {
			n++
		}
	}
	if n != 8 {
		t.Fatalf("redis entries: %d", n)
	}

	m = newMachine()
	m.redis = []string{`C:\A\redis.conf`, `C:\B\redis.conf`, `C:\C\redis.conf`, `C:\D\missing.conf`}
	m.addFile(m.redis[0], `logfile "\\srv\share\r.log"`)
	m.addFile(m.redis[1], `logfile "..\..\r.log"`)
	m.addFile(m.redis[2], `logfile "C:\x\..\y\r.log"`)
	if doc := discover(m, Config{}); hasKind(doc, SourceRedis) {
		t.Fatalf("accepted bad redis paths: %v", ids(doc))
	}
}

func TestSourcesAppFolders(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, defSite, ``))
	m.addDir(`C:\inetpub\logs\LogFiles\W3SVC1`)
	m.addDir(`D:\Apps\Billing\Logs`,
		fFile("a.log", 1, 1), fFile("b.TXT", 2, 2), fFile("c.json", 4, 3), fFile("d.ndjson", 8, 4), fFile("e.bin", 16, 5))
	cfg := Config{LogPaths: []string{
		`C:\inetpub\logs\LogFiles\W3SVC1`, // covered by iis
		`c:\INETPUB\logs\logfiles\w3svc1\`,
		`D:\UnravisBlackbox`, // the recordings folder
		`C:\Windows\System32\LogFiles\HTTPERR`,
		`D:/Apps/Billing/Logs/`,
		`d:\apps\billing\logs`, // repeat
		`\\srv\share`,
		`relative\path`,
		`D:\Apps\Other`,
	}}
	doc := discover(m, cfg)
	a1 := find(t, doc, "app.1")
	if a1.Kind != SourceApp || a1.Dir != `D:\Apps\Billing\Logs` || a1.Label != "Log folder from Blackbox settings" ||
		a1.Pattern != "*.log|*.txt|*.json|*.ndjson" || a1.Origin != "settings" {
		t.Fatalf("app.1: %+v", a1)
	}
	if a1.Files != 4 || a1.Bytes != 15 || a1.Newest != 4 {
		t.Fatalf("app.1 counts: %+v", a1)
	}
	if a2 := find(t, doc, "app.2"); a2.Dir != `D:\Apps\Other` {
		t.Fatalf("app.2: %+v", a2)
	}
	if hasSource(doc, "app.3") {
		t.Fatalf("too many app entries: %v", ids(doc))
	}
}

func TestSourcesFolderErrors(t *testing.T) {
	m := newMachine()
	m.dirErr[fk(`D:\UnravisBlackbox`)] = fs.ErrPermission
	m.addDir(`D:\Apps\A`)
	m.dirErr[fk(`D:\Apps\A`)] = errors.New("disk on fire")
	m.dirErr[fk(`D:\Apps\B`)] = fs.ErrNotExist
	doc := discover(m, Config{LogPaths: []string{`D:\Apps\A`, `D:\Apps\B`}})

	r := find(t, doc, "recordings")
	if !r.Exists || r.Readable || r.Err != "access-denied" || r.Files != 0 {
		t.Fatalf("access denied: %+v", r)
	}
	a := find(t, doc, "app.1")
	if a.Readable || a.Err != "error" {
		t.Fatalf("other error: %+v", a)
	}
	b := find(t, doc, "app.2")
	if b.Exists || b.Readable || b.Err != "not-found" {
		t.Fatalf("not found: %+v", b)
	}

	env := m.env()
	env.ReadDir = nil
	doc = DiscoverSources(env, Config{Folder: `D:\X`}, "h", "v", testNow)
	if r := find(t, doc, "recordings"); r.Err != "error" || r.Readable {
		t.Fatalf("nil ReadDir: %+v", r)
	}
}

func TestSourcesDefaultErrorsAreKept(t *testing.T) {
	// A default folder that exists but cannot be read is shown, only "not found" is dropped.
	m := newMachine()
	m.dirErr[fk(`C:\Windows\System32\LogFiles\HTTPERR`)] = fs.ErrPermission
	h := find(t, discover(m, Config{}), "httperr")
	if h.Err != "access-denied" || !h.Exists {
		t.Fatalf("httperr: %+v", h)
	}
}

func TestSourcesTruncation(t *testing.T) {
	old := scanLimit
	scanLimit = 3
	defer func() { scanLimit = old }()

	m := newMachine()
	var es []fs.DirEntry
	for i := 0; i < 5; i++ {
		es = append(es, fFile(fmt.Sprintf("bb-%d.ndjson", i), 10, int64(i)))
	}
	m.addDir(`D:\UnravisBlackbox`, es...)
	r := find(t, discover(m, Config{}), "recordings")
	if !r.Truncated || r.Files != 3 || r.Bytes != 30 {
		t.Fatalf("truncated: %+v", r)
	}

	m.addDir(`D:\UnravisBlackbox`, es[:3]...)
	if r := find(t, discover(m, Config{}), "recordings"); r.Truncated || r.Files != 3 {
		t.Fatalf("exact fit: %+v", r)
	}
}

func TestSourcesSortedStableAndCapped(t *testing.T) {
	m := newMachine()
	var sites strings.Builder
	for i := 250; i >= 1; i-- {
		fmt.Fprintf(&sites, `<site name="S%d" id="%d"/>`, i, i)
	}
	m.iis(iisXML(``, sites.String(), ``))
	m.sql = []sqlLogInstance{{Label: "B", LogPath: `D:\B\ERRORLOG`}, {Label: "A", LogPath: `D:\A\ERRORLOG`}}

	d1 := discover(m, Config{})
	d2 := discover(m, Config{})
	if !reflect.DeepEqual(d1, d2) {
		t.Fatal("two runs differ")
	}
	if len(d1.Sources) != maxSourceEntries {
		t.Fatalf("entries: %d", len(d1.Sources))
	}
	got := ids(d1)
	if !sort.StringsAreSorted(got) {
		t.Fatalf("not sorted: %v", got[:10])
	}

	m2 := newMachine()
	m2.sql = m.sql
	got = ids(discover(m2, Config{LogPaths: []string{`D:\Z`, `D:\Y`}}))
	want := []string{"app.1", "app.2", "recordings", "sql.a", "sql.b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids %v want %v", got, want)
	}
}

func TestSourcesNilEnvFunctions(t *testing.T) {
	doc := DiscoverSources(SourcesEnv{}, Config{Folder: `D:\X`, LogPaths: []string{`D:\Y`}}, "h", "", testNow)
	// The defaults stay: with no ReadDir the folder is "error", not "not-found".
	want := []string{"app.1", "evt.application", "evt.system", "httperr", "recordings"}
	if got := ids(doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("ids %v want %v", got, want)
	}
	for _, e := range doc.Sources {
		if e.Err != "error" || e.Readable {
			t.Fatalf("entry: %+v", e)
		}
	}
}

func TestWriteSourcesFile(t *testing.T) {
	dir := t.TempDir()
	m := newMachine()
	m.addDir(`D:\UnravisBlackbox`, fFile("bb-1.ndjson", 10, 1))
	doc := discover(m, Config{})

	if err := WriteSourcesFile(dir, doc); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSourcesFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Sources, doc.Sources) || got.Host != doc.Host || got.Schema != SourcesSchema {
		t.Fatalf("round trip: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, SourcesName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}

	// Same content, newer Generated: the file is not touched.
	target := filepath.Join(dir, SourcesName)
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatal(err)
	}
	doc2 := doc
	doc2.Generated = doc.Generated.Add(15 * time.Minute)
	if err := WriteSourcesFile(dir, doc2); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(old) {
		t.Fatal("identical content was rewritten")
	}
	if got, _ := ReadSourcesFile(dir); !got.Generated.Equal(doc.Generated) {
		t.Fatal("Generated changed on a skipped write")
	}

	// Different content: written.
	doc3 := doc2
	doc3.Sources = append([]SourceEntry(nil), doc.Sources...)
	doc3.Sources[0].Files = 99
	if err := WriteSourcesFile(dir, doc3); err != nil {
		t.Fatal(err)
	}
	got, err = ReadSourcesFile(dir)
	if err != nil || got.Sources[0].Files != 99 || !got.Generated.Equal(doc2.Generated) {
		t.Fatalf("changed doc not written: %+v %v", got, err)
	}

	// A broken old file is replaced.
	if err := os.WriteFile(target, []byte("{not json"), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	if err := WriteSourcesFile(dir, doc3); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadSourcesFile(dir); err != nil || got.Sources[0].Files != 99 {
		t.Fatalf("broken file not replaced: %v", err)
	}
}

func TestWriteSourcesFileMissingDir(t *testing.T) {
	err := WriteSourcesFile(filepath.Join(t.TempDir(), "nope"), SourcesDoc{Schema: SourcesSchema})
	if err == nil {
		t.Fatal("expected an error")
	}
}
