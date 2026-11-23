package cmdtree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/spf13/pflag"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/clisrv"
	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/pipesec"
	dashboard "api-audit-explorer/internal/server"
)

const ownerToken = "tok-0123456789abcdef0123456789abcdef"

// connListener hands the server the connections the test dials.
type connListener struct {
	ch   chan net.Conn
	done chan struct{}
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *connListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}
func (l *connListener) Addr() net.Addr { return pipeAddr("test") }

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }

// owner is the app side of a test: the real command line handler over the real dashboard handler and the demo data set, behind a
// connection made in memory, with a locator file on disk.
type owner struct {
	t      *testing.T
	runDir string
	rec    locator.Record
	ln     *connListener
	srv    *http.Server
	now    time.Time

	mu       sync.Mutex
	state    string
	helloPID int
	helloGen time.Time
	ops      []map[string]any // every op request the owner received
	hellos   int
	alive    bool
	dialErr  error
	killMid  bool // close the connection of the next op without answering
}

type ownerOpt func(*owner)

func newOwner(t *testing.T, backend clisrv.Backend, opts ...ownerOpt) *owner {
	t.Helper()
	o := &owner{t: t, runDir: t.TempDir(), state: "ready", alive: true}
	o.now = time.Now().UTC().Truncate(time.Hour)
	nonce := "0123456789abcdef0123456789abcdef"
	o.rec = locator.Record{Version: locator.Schema, PID: 4242, StartTime: time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond), Exe: `C:\App\unravis.exe`, AppVersion: "1.2.3",
		UI: `\\.\pipe\unravis.ui.` + nonce, AG: `\\.\pipe\unravis.ag.` + nonce}
	o.helloPID, o.helloGen = o.rec.PID, o.rec.StartTime
	for _, f := range opts {
		f(o)
	}
	if err := locator.Write(locator.FilePath(o.runDir, false), o.rec, locator.Options{}); err != nil {
		t.Fatal(err)
	}
	if backend == nil {
		dash := dashboard.New(audit.NewDemo(o.now), nil, ownerToken, false)
		backend = clisrv.MCPBackend(mcp.NewLocal(dash, ownerToken, mcp.WithIdentifiers(true)))
	}
	inner := clisrv.Handler(clisrv.Deps{
		Backend: backend,
		Info: func() clisrv.Info {
			o.mu.Lock()
			defer o.mu.Unlock()
			return clisrv.Info{PID: o.helloPID, Gen: o.helloGen.UTC().Format(time.RFC3339Nano), AppVersion: "1.2.3", Owner: "app"}
		},
		State: func() string { o.mu.Lock(); defer o.mu.Unlock(); return o.state },
		Logf:  func(string, ...any) {},
	})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		kill := o.killMid && r.URL.Path == "/cli/v1/op"
		if r.URL.Path == "/cli/v1/hello" {
			o.hellos++
		}
		o.mu.Unlock()
		if kill {
			hj, _ := w.(http.Hijacker)
			c, _, _ := hj.Hijack()
			_ = c.Close()
			return
		}
		if r.URL.Path == "/cli/v1/op" {
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			o.mu.Lock()
			o.ops = append(o.ops, m)
			o.mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		inner.ServeHTTP(w, r)
	})
	o.ln = &connListener{ch: make(chan net.Conn), done: make(chan struct{})}
	o.srv = &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = o.srv.Serve(o.ln) }()
	t.Cleanup(func() { _ = o.srv.Close(); _ = o.ln.Close() })
	return o
}

func (o *owner) set(f func(*owner)) { o.mu.Lock(); defer o.mu.Unlock(); f(o) }
func (o *owner) opsSeen() []map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]map[string]any(nil), o.ops...)
}

type fakeProber struct{ o *owner }

func (p fakeProber) ProcessStart(int) (time.Time, error) {
	p.o.mu.Lock()
	defer p.o.mu.Unlock()
	if !p.o.alive {
		return time.Time{}, errors.New("no such process")
	}
	return p.o.rec.StartTime, nil
}
func (p fakeProber) PipeExists(string) bool { p.o.mu.Lock(); defer p.o.mu.Unlock(); return p.o.alive }

func (o *owner) attacher() clicore.Attacher {
	return clicore.Attacher{
		RunDir: func() (string, error) { return o.runDir, nil },
		Prober: fakeProber{o},
		Dial: func(ctx context.Context, rec locator.Record) (net.Conn, error) {
			o.mu.Lock()
			err := o.dialErr
			o.mu.Unlock()
			if err != nil {
				return nil, err
			}
			c1, c2 := net.Pipe()
			select {
			case o.ln.ch <- c2:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return c1, nil
		},
		UserAgent: "unravis-cli/test",
	}
}

func runOn(t *testing.T, a clicore.Attacher, stdin string, env map[string]string, args ...string) result {
	t.Helper()
	return runCtx(t, context.Background(), a, stdin, env, args...)
}

func runCtx(t *testing.T, ctx context.Context, a clicore.Attacher, stdin string, env map[string]string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(ctx, args, IO{
		In: strings.NewReader(stdin), Out: &out, Err: &errb,
		Getenv: func(n string) string { return env[n] },
		Exe:    func() (string, error) { return "/opt/unravis/bin/unravis.exe", nil },
		Attach: a.Attach,
	})
	return result{code, out.String(), errb.String()}
}

func (o *owner) run(args ...string) result { return runOn(o.t, o.attacher(), "", nil, args...) }

func data(t *testing.T, r result) map[string]any {
	t.Helper()
	if r.code != 0 || r.err != "" {
		t.Fatalf("exit %d, stderr %q, stdout %.300q", r.code, r.err, r.out)
	}
	return decode(t, r.out)["data"].(map[string]any)
}

func (o *owner) window() []string {
	f := func(d time.Duration) string { return o.now.Add(d).Format(time.RFC3339) }
	return []string{"--source", "demo-west", "--source", "demo-east", "--start", f(-time.Hour), "--end", f(0), "--baseline-start", f(-25 * time.Hour), "--baseline-end", f(-24 * time.Hour)}
}

func TestSourcesCompareAndFollowUp(t *testing.T) {
	o := newOwner(t, nil)
	src := data(t, o.run("sources", "list", "-o", "json"))
	if len(src["sources"].([]any)) != 2 {
		t.Fatalf("%v", src)
	}
	if tb := o.run("sources", "list"); tb.code != 0 || !strings.Contains(tb.out, "demo-west") || !strings.Contains(tb.out, "COLLECTION") {
		t.Errorf("table: %+v", tb)
	}
	cmp := o.run(append([]string{"audit", "compare", "--json"}, o.window()...)...)
	d := data(t, cmp)
	m := decode(t, cmp.out)["meta"].(map[string]any)
	id, _ := d["resultId"].(string)
	if len(id) != 32 || m["coverage"] != "complete" || m["identifiers"] != "shown" || decode(t, cmp.out)["operation"] != "audit.compare" {
		t.Fatalf("compare: %.400s", cmp.out)
	}
	tb := o.run(append([]string{"audit", "compare"}, o.window()...)...)
	if tb.code != 0 || !strings.Contains(tb.out, "P95 RATIO") || !strings.Contains(tb.out, "Result id: "+id[:0]) || !strings.Contains(tb.out, "unravis audit groups --result-id ") {
		t.Errorf("compare table: %+v", tb)
	}
	// the table's own handle pages the same result (a new process, the same owner)
	var handle string
	for _, line := range strings.Split(tb.out, "\n") {
		if strings.HasPrefix(line, "Result id: ") {
			handle = strings.Fields(line)[2]
		}
	}
	if len(handle) != 32 {
		t.Fatalf("no handle in %q", tb.out)
	}
	g := data(t, o.run("audit", "groups", "--result-id", handle, "--limit", "2", "-o", "json"))
	if n := len(g["groups"].([]any)); n == 0 || n > 2 {
		t.Errorf("groups: %v", g)
	}
	if tb := o.run("audit", "groups", "--result-id", handle, "--limit", "2"); tb.code != 0 || !strings.Contains(tb.out, "groups shown") {
		t.Errorf("groups table: %+v", tb)
	}
	// an unknown handle: RESULT_EXPIRED, exit 5, a next step
	r := o.run("audit", "groups", "--result-id", "00000000000000000000000000000000", "--json")
	if r.code != 5 || errCode(t, r) != "RESULT_EXPIRED" {
		t.Errorf("expired: %+v", r)
	}
	if e := decode(t, r.err)["error"].(map[string]any); e["retryable"] != false || e["action"] == "" {
		t.Errorf("error object: %v", e)
	}
	// the guide
	if r := o.run("audit", "guide"); r.code != 0 || !strings.Contains(r.out, "\n") || r.err != "" {
		t.Errorf("guide: %+v", r)
	}
	// an invalid limit comes back from the owner as INVALID_ARGUMENT
	if r := o.run("audit", "groups", "--result-id", handle, "--limit", "9999", "--json"); r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
		t.Errorf("limit: %+v", r)
	}
}

func TestFlagsAndRequestFileAskTheSame(t *testing.T) {
	o := newOwner(t, nil)
	flags := data(t, o.run(append([]string{"audit", "compare", "--json", "--limit", "5", "--sort", "p95_ratio", "--kind", "synchronous"}, o.window()...)...))
	seen := o.opsSeen()
	flagArgs, _ := json.Marshal(seen[len(seen)-1]["args"])
	file := filepath.Join(t.TempDir(), "q.json")
	if err := os.WriteFile(file, flagArgs, 0o600); err != nil {
		t.Fatal(err)
	}
	req := data(t, o.run("audit", "compare", "--request", file, "--json"))
	for k, v := range flags {
		if k == "resultId" || k == "timings" || k == "provenance" {
			continue // the handle is new each time; timings and provenance hold how long this run took
		}
		a, _ := json.Marshal(v)
		b, _ := json.Marshal(req[k])
		if !bytes.Equal(a, b) {
			t.Errorf("the flags and the file gave different %s:\n%.300s\n%.300s", k, a, b)
		}
	}
	// defaults of the flags
	var args map[string]any
	_ = json.Unmarshal(flagArgs, &args)
	q := args["query"].(map[string]any)
	if q["outcome"] != "all" || q["metric"] != "total" || q["bucketSeconds"] != float64(60) || q["kind"] != "synchronous" || args["limit"] != float64(5) || args["sort"] != "p95_ratio" {
		t.Errorf("args: %s", flagArgs)
	}
	if _, has := q["controller"]; has {
		t.Errorf("an unset filter was sent: %s", flagArgs)
	}
}

func TestRequestInputs(t *testing.T) {
	o := newOwner(t, nil)
	guide := `{}`
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	u16 := func(s string) []byte {
		b := []byte{0xFF, 0xFE}
		for _, c := range utf16.Encode([]rune(s)) {
			b = append(b, byte(c), byte(c>>8))
		}
		return b
	}
	for name, body := range map[string][]byte{"plain": []byte(guide), "bom": append([]byte{0xEF, 0xBB, 0xBF}, guide...), "utf16": u16(guide), "spaces": []byte("\n  " + guide + "\n")} {
		if r := o.run("audit", "guide", "--request", write(name, body), "--json"); r.code != 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// from the input
	if r := runOn(t, o.attacher(), guide, nil, "audit", "guide", "--request", "-", "--json"); r.code != 0 {
		t.Errorf("stdin: %+v", r)
	}
	for name, tc := range map[string]struct {
		args  []string
		stdin string
		code  string
		exit  int
	}{
		"not an object":  {[]string{"audit", "guide", "--request", "-", "--json"}, `[1]`, "INVALID_ARGUMENT", 2},
		"not json":       {[]string{"audit", "guide", "--request", "-", "--json"}, `{"a":`, "INVALID_ARGUMENT", 2},
		"empty":          {[]string{"audit", "guide", "--request", "-", "--json"}, ``, "INVALID_ARGUMENT", 2},
		"too large":      {[]string{"audit", "guide", "--request", "-", "--json"}, `{"p":"` + strings.Repeat("a", clicore.MaxRequestBytes) + `"}`, "REQUEST_TOO_LARGE", 2},
		"no file":        {[]string{"audit", "guide", "--request", filepath.Join(dir, "very-secret-name.json"), "--json"}, "", "INVALID_ARGUMENT", 2},
		"a folder":       {[]string{"audit", "guide", "--request", dir, "--json"}, "", "INVALID_ARGUMENT", 2},
		"with a flag":    {[]string{"audit", "compare", "--request", "-", "--source", "x", "--json"}, `{}`, "INVALID_ARGUMENT", 2},
		"with --limit":   {[]string{"audit", "groups", "--request", "-", "--limit", "3", "--json"}, `{}`, "INVALID_ARGUMENT", 2},
		"offline cmd":    {[]string{"version", "--request", "-", "--json"}, `{}`, "INVALID_ARGUMENT", 2},
		"unknown fields": {[]string{"audit", "guide", "--request", "-", "--json"}, `{"principal":"cli-ci"}`, "INVALID_ARGUMENT", 2},
		"repeated name":  {[]string{"audit", "guide", "--request", "-", "--json"}, `{"a":1,"a":2}`, "INVALID_ARGUMENT", 2},
	} {
		r := runOn(t, o.attacher(), tc.stdin, nil, tc.args...)
		if r.code != tc.exit || errCode(t, r) != tc.code {
			t.Errorf("%s: %+v", name, r)
		}
		if strings.Contains(r.err, "very-secret-name") {
			t.Errorf("%s: the file name is echoed: %s", name, r.err)
		}
	}
}

// Every field flag is a field of the tool's input schema (spec 3.2).
func TestFlagsMatchToolSchemas(t *testing.T) {
	props := func(tool string) (top, query map[string]any) {
		for _, c := range mcp.Catalog() {
			if c["name"] == tool {
				in := c["inputSchema"].(map[string]any)
				top = in["properties"].(map[string]any)
				if q, ok := top["query"].(map[string]any); ok {
					query = q["properties"].(map[string]any)
				}
			}
		}
		return
	}
	top, query := props("audit_compare")
	for flag, field := range compareFlags {
		if _, ok := query[field]; !ok {
			t.Errorf("--%s sets query.%s, which the tool does not have", flag, field)
		}
	}
	for flag, field := range compareTop {
		if _, ok := top[field]; !ok {
			t.Errorf("--%s sets %s, which the tool does not have", flag, field)
		}
	}
	top, _ = props("audit_get_groups")
	for flag, field := range groupsFlags {
		if _, ok := top[field]; !ok {
			t.Errorf("--%s sets %s, which the tool does not have", flag, field)
		}
	}
	// and every flag that the command declares is in the maps
	st := &state{}
	for _, name := range []string{"compare", "groups"} {
		for _, c := range st.auditCmd().Commands() {
			if c.Name() != name {
				continue
			}
			want := compareFlags
			if name == "groups" {
				want = groupsFlags
			}
			c.Flags().VisitAll(func(f *pflag.Flag) {
				if _, ok := want[f.Name]; !ok {
					if _, ok := compareTop[f.Name]; !ok || name != "compare" {
						t.Errorf("%s --%s is in no flag map", name, f.Name)
					}
				}
			})
		}
	}
}

func TestCompareUsageErrors(t *testing.T) {
	o := newOwner(t, nil)
	good := o.window()
	without := func(flag string) []string {
		var out []string
		for i := 0; i < len(good); i++ {
			if good[i] == flag {
				i++
				continue
			}
			out = append(out, good[i])
		}
		return out
	}
	for name, args := range map[string][]string{
		"no source":         without("--source"),
		"no start":          without("--start"),
		"no baseline end":   without("--baseline-end"),
		"bad time":          append(without("--end"), "--end", "tomorrow-secret"),
		"time with no zone": append(without("--end"), "--end", "2026-11-23T09:00:00"),
		"bad user id":       append(append([]string{}, good...), "--user-id", "abc-secret"),
		"bad bucket":        append(append([]string{}, good...), "--bucket-seconds", "x"),
	} {
		r := o.run(append([]string{"audit", "compare", "--json"}, args...)...)
		if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" || strings.Contains(r.err, "secret") {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if n := len(o.opsSeen()); n != 0 {
		t.Errorf("a bad command line reached the owner: %d calls", n)
	}
	// the owner checks the rest
	r := o.run(append([]string{"audit", "compare", "--json", "--bucket-seconds", "1"}, good...)...)
	if r.code != 2 || errCode(t, r) != "INVALID_ARGUMENT" {
		t.Errorf("owner side: %+v", r)
	}
	if r := o.run("audit", "compare", "--source", "x", "--profile", "cli-ci", "--json"); r.code != 2 {
		t.Errorf("--profile: %+v", r)
	}
	if r := runOn(t, o.attacher(), "", map[string]string{"UNRAVIS_PROFILE": "cli-ci"}, "sources", "list", "--json"); r.code != 2 {
		t.Errorf("UNRAVIS_PROFILE: %+v", r)
	}
}

func TestDefaultTimeoutOfCompare(t *testing.T) {
	o := newOwner(t, nil)
	deadline := func() float64 { s := o.opsSeen(); return s[len(s)-1]["deadlineMs"].(float64) }
	o.run(append([]string{"audit", "compare", "--json"}, o.window()...)...)
	if deadline() != float64(CompareTimeout.Milliseconds()) {
		t.Errorf("compare: %v", deadline())
	}
	o.run("sources", "list", "--json")
	if deadline() != float64(DefaultTimeout.Milliseconds()) {
		t.Errorf("sources: %v", deadline())
	}
	o.run(append([]string{"audit", "compare", "--json", "--timeout", "90s"}, o.window()...)...)
	if deadline() != 90000 {
		t.Errorf("flag: %v", deadline())
	}
	runOn(t, o.attacher(), "", map[string]string{"UNRAVIS_TIMEOUT": "45s"}, append([]string{"audit", "compare", "--json"}, o.window()...)...)
	if deadline() != 45000 {
		t.Errorf("variable: %v", deadline())
	}
}

func TestAttachFailures(t *testing.T) {
	// no locator
	o := newOwner(t, nil)
	_ = os.Remove(locator.FilePath(o.runDir, false))
	r := o.run("sources", "list", "--json")
	if r.code != 3 || errCode(t, r) != "APP_NOT_RUNNING" {
		t.Errorf("no locator: %+v", r)
	}
	if e := decode(t, r.err)["error"].(map[string]any); e["retryable"] != true {
		t.Errorf("not running before the request was sent is retryable: %v", e)
	}
	// a locator of a dead process
	o = newOwner(t, nil)
	o.set(func(o *owner) { o.alive = false })
	if r := o.run("sources", "list", "--json"); r.code != 3 || errCode(t, r) != "STALE_LOCATOR" {
		t.Errorf("stale: %+v", r)
	}
	// a locator of a newer format
	o = newOwner(t, nil)
	p := locator.FilePath(o.runDir, false)
	b, _ := os.ReadFile(p) //nolint:gosec // a file of this test
	_ = os.WriteFile(p, bytes.Replace(b, []byte(`"version":2`), []byte(`"version":9`), 1), 0o600)
	if r := o.run("sources", "list", "--json"); r.code != 5 || errCode(t, r) != "VERSION_INCOMPATIBLE" {
		t.Errorf("newer locator: %+v", r)
	}
	// a locator that is not valid
	_ = os.WriteFile(p, []byte(`{"version":2`), 0o600)
	if r := o.run("sources", "list", "--json"); r.code != 3 || errCode(t, r) != "ENDPOINT_UNAVAILABLE" {
		t.Errorf("broken locator: %+v", r)
	}
	// the process that answers is not the one in the locator: nothing but hello is sent
	for name, f := range map[string]func(*owner){
		"other pid":   func(o *owner) { o.helloPID = 99 },
		"other start": func(o *owner) { o.helloGen = o.helloGen.Add(time.Hour) },
	} {
		o = newOwner(t, nil)
		o.set(f)
		r := o.run("sources", "list", "--json")
		if r.code != 3 || errCode(t, r) != "ENDPOINT_UNTRUSTED" {
			t.Errorf("%s: %+v", name, r)
		}
		if n := len(o.opsSeen()); n != 0 {
			t.Errorf("%s: %d operations were sent to a process that failed the check", name, n)
		}
	}
	// states
	o = newOwner(t, nil)
	o.set(func(o *owner) { o.state = "locked" })
	if r := o.run("sources", "list", "--json"); r.code != 4 || errCode(t, r) != "CASE_LOCKED" {
		t.Errorf("locked: %+v", r)
	}
	if r := o.run("app", "status", "--json"); r.code != 0 {
		t.Errorf("status of a locked app: %+v", r)
	}
	o.set(func(o *owner) { o.state = "updating" })
	if r := o.run("sources", "list", "--json"); r.code != 3 || errCode(t, r) != "UPDATE_IN_PROGRESS" {
		t.Errorf("updating: %+v", r)
	}
	// a failed dial check
	for name, tc := range map[string]struct {
		err  error
		code string
		exit int
	}{
		"impostor":    {pipesec.ErrServerMismatch, "ENDPOINT_UNTRUSTED", 3},
		"we refused":  {pipesec.ErrPeerDenied, "PERMISSION_DENIED", 4},
		"busy":        {pipesec.ErrPipeBusy, "OWNER_BUSY", 3},
		"other error": {errors.New("secret detail of the pipe"), "ENDPOINT_UNAVAILABLE", 3},
	} {
		o = newOwner(t, nil)
		o.set(func(o *owner) { o.dialErr = tc.err })
		r := o.run("sources", "list", "--json")
		if r.code != tc.exit || errCode(t, r) != tc.code || strings.Contains(r.err, "secret detail") {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestProtocolMismatch(t *testing.T) {
	// the owner's major is not ours, or its minor is older: the command line stops before any operation
	a := clicore.Attacher{}
	_ = a
	for name, h := range map[string]string{
		"newer major": `{"v":1,"pid":4242,"gen":"%GEN%","proto":{"major":2,"minor":0},"state":"ready"}`,
		"older minor": `{"v":1,"pid":4242,"gen":"%GEN%","proto":{"major":1,"minor":-1},"state":"ready"}`,
		"no version":  `{"pid":4242,"gen":"%GEN%","proto":{"major":1,"minor":0},"state":"ready"}`,
		"not json":    `<html>`,
	} {
		o := newOwner(t, nil)
		srv := http.NewServeMux()
		srv.HandleFunc("/cli/v1/hello", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.ReplaceAll(h, "%GEN%", o.rec.StartTime.UTC().Format(time.RFC3339Nano)))
		})
		srv.HandleFunc("/cli/v1/op", func(w http.ResponseWriter, r *http.Request) { t.Errorf("%s: an operation was sent", name) })
		o.srv.Handler = srv
		r := o.run("sources", "list", "--json")
		wantCode := "VERSION_INCOMPATIBLE"
		if name == "no version" || name == "not json" {
			wantCode = "ENDPOINT_UNTRUSTED"
		}
		if errCode(t, r) != wantCode {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestAppStatus(t *testing.T) {
	o := newOwner(t, nil)
	d := data(t, o.run("app", "status", "-o", "json"))
	if d["running"] != true || d["state"] != "ready" || d["pid"] != float64(4242) || d["owner"] != "app" {
		t.Errorf("%v", d)
	}
	if tb := o.run("app", "status"); tb.code != 0 || !strings.Contains(tb.out, "running") || !strings.Contains(tb.out, "true") {
		t.Errorf("table: %+v", tb)
	}
	o.set(func(o *owner) { o.alive = false })
	d = data(t, o.run("app", "status", "-o", "json"))
	if d["running"] != false || d["state"] != "stale" {
		t.Errorf("stale: %v", d)
	}
	_ = os.Remove(locator.FilePath(o.runDir, false))
	d = data(t, o.run("app", "status", "-o", "json"))
	if d["running"] != false || d["state"] != "stopped" {
		t.Errorf("stopped: %v", d)
	}
	// an untrusted answer is still an error here
	o = newOwner(t, nil)
	o.set(func(o *owner) { o.helloPID = 7 })
	if r := o.run("app", "status", "--json"); r.code != 3 || errCode(t, r) != "ENDPOINT_UNTRUSTED" {
		t.Errorf("untrusted: %+v", r)
	}
}

func TestOwnerGoneMidCallAndCancel(t *testing.T) {
	// the owner closes the connection while it works: the work may have run, so nothing is retryable
	o := newOwner(t, nil)
	o.set(func(o *owner) { o.killMid = true })
	r := o.run(append([]string{"audit", "compare", "--json"}, o.window()...)...)
	if r.code != 3 || errCode(t, r) != "UPSTREAM_UNAVAILABLE" {
		t.Fatalf("mid call: %+v", r)
	}
	if e := decode(t, r.err)["error"].(map[string]any); e["retryable"] != false {
		t.Errorf("a live query after the request was sent is never retryable: %v", e)
	}
	// Ctrl+C while the owner works: the command stops with CANCELED, and the owner's call is cancelled
	started := make(chan struct{})
	stopped := make(chan error, 1)
	blocker := fakeBackend(func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return nil, clicore.NewErrorAfterWork(clicore.CodeCanceled, "stopped", "")
	})
	o = newOwner(t, blocker)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan result, 1)
	go func() { done <- runCtx(t, ctx, o.attacher(), "", nil, "audit", "guide", "--json") }()
	<-started
	cancel()
	res := <-done
	if res.code != 6 || errCode(t, res) != "CANCELED" {
		t.Errorf("cancel: %+v", res)
	}
	select {
	case err := <-stopped:
		if err == nil {
			t.Error("the owner's call was not cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Error("the owner was not told to cancel")
	}
	// a deadline
	o = newOwner(t, fakeBackend(func(ctx context.Context, _ string, _ json.RawMessage) (any, *clicore.Error) {
		<-ctx.Done()
		return nil, clicore.NewErrorAfterWork(clicore.CodeTimeout, "slow", "")
	}))
	r = o.run("audit", "guide", "--timeout", "50ms", "--json")
	if r.code != 6 || errCode(t, r) != "TIMEOUT" {
		t.Errorf("deadline: %+v", r)
	}
}

type fakeBackend func(ctx context.Context, tool string, args json.RawMessage) (any, *clicore.Error)

func (f fakeBackend) Call(ctx context.Context, tool string, args json.RawMessage) (any, *clicore.Error) {
	return f(ctx, tool, args)
}

func TestOwnerAnswersAreNotTrusted(t *testing.T) {
	// a code that is not in the table, a retryable flag, a long message and a control character from the owner
	o := newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return nil, &clicore.Error{Code: "MADE_UP", Message: strings.Repeat("x", 5000) + "\x1b[31m", Retryable: true}
	}))
	r := o.run("audit", "guide", "--json")
	e := decode(t, r.err)["error"].(map[string]any)
	if r.code != 1 || e["code"] != "INTERNAL" || e["retryable"] != false || len(e["message"].(string)) > 700 {
		t.Errorf("%+v %v", r, e)
	}
	tb := o.run("audit", "guide")
	if strings.Contains(tb.err, "\x1b") {
		t.Errorf("a control character reached the terminal: %q", tb.err)
	}
	// a result that is partial says so, and the exit code is still 0
	o = newOwner(t, fakeBackend(func(context.Context, string, json.RawMessage) (any, *clicore.Error) {
		return map[string]any{"sources": []map[string]any{{"sourceId": "a", "ok": false}}}, nil
	}))
	r = o.run("audit", "guide", "--json")
	if r.code != 0 || decode(t, r.out)["meta"].(map[string]any)["coverage"] != "partial" {
		t.Errorf("partial: %+v", r)
	}
}

func TestOnlineOutputFileAndPipe(t *testing.T) {
	o := newOwner(t, nil)
	p := filepath.Join(t.TempDir(), "sources.json")
	if r := o.run("sources", "list", "-o", "json", "--output-file", p); r.code != 0 || r.out != "" {
		t.Fatalf("%+v", r)
	}
	b, err := os.ReadFile(p) //nolint:gosec // a file of this test
	if err != nil {
		t.Fatal(err)
	}
	if decode(t, string(b))["operation"] != "sources.list" {
		t.Errorf("%s", b)
	}
	// an owner failure writes no file
	q := filepath.Join(t.TempDir(), "x.json")
	if r := o.run("audit", "groups", "--result-id", "00000000000000000000000000000000", "-o", "json", "--output-file", q); r.code != 5 {
		t.Errorf("%+v", r)
	}
	if _, err := os.Stat(q); err == nil {
		t.Error("a file was written for a failed command")
	}
}

func TestNoTokenOnTheWire(t *testing.T) {
	// what the command line sends: no token header, no Origin, the user agent, a request that is JSON
	o := newOwner(t, nil)
	var seen http.Header
	inner := o.srv.Handler
	o.srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		if strings.Contains(strings.ToLower(r.Header.Get("X-Audit-Token")), "tok") || r.Header.Get("Origin") != "" {
			t.Errorf("headers: %v", r.Header)
		}
		inner.ServeHTTP(w, r)
	})
	if r := o.run("sources", "list", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if seen.Get("User-Agent") != "unravis-cli/test" || seen.Get("X-Audit-Token") != "" {
		t.Errorf("%v", seen)
	}
}
