package perfbench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/hosts"
	"api-audit-explorer/internal/importer/all"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/server"
)

// gateEnv turns the benchmarks on. Without it every test here skips.
const gateEnv = "UNRAVIS_BENCH"

const (
	benchToken = "0123456789abcdef0123456789abcdef"
	hostsJSON  = `{"version":1,"hosts":[{"label":"web-a","aliases":["web-a.example.test"],"role":"iis","tz":"UTC","logicalCores":32}]}`
)

func need(t *testing.T) {
	t.Helper()
	if os.Getenv(gateEnv) != "1" {
		t.Skip("set " + gateEnv + "=1 to run the performance measurements")
	}
}

// envInt reads an integer override, for example UNRAVIS_BENCH_IIS_LINES.
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// benchDir makes a temp directory for generated data and removes it when the
// test ends. UNRAVIS_BENCH_DIR chooses the parent (default: the system temp).
func benchDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp(os.Getenv("UNRAVIS_BENCH_DIR"), "unravis-bench-")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

/* ---------------------------------------------------------------- meter */

type sample struct {
	At        time.Duration
	HeapInuse uint64
	WS        uint64
}

// measurement is what a meter saw between start and finish.
type measurement struct {
	Wall                   time.Duration
	CPUUser, CPUKernel     time.Duration
	BaseHeap, BaseWS       uint64
	MaxHeapInuse, MaxSys   uint64
	MaxWS, PeakWS, MaxPriv uint64
	EndHeap, EndWS         uint64
	MaxDirBytes            int64
	Series                 []sample
}

// meter samples runtime.MemStats and the working set every 50 ms, and the size
// of dir (when set) once a second.
type meter struct {
	start        time.Time
	u0, k0       time.Duration
	baseHeap     uint64
	baseWS       uint64
	dir          string
	every        time.Duration
	stop, done   chan struct{}
	mu           sync.Mutex
	m            measurement
	lastDirCheck time.Time
}

func startMeter(dir string) *meter { return startMeterEvery(dir, 50*time.Millisecond) }

// startMeterEvery is startMeter with another sampling interval. The win-live
// sampler measurement uses 1 s so the meter's own ReadMemStats calls do not
// count as sampler cost.
func startMeterEvery(dir string, every time.Duration) *meter {
	debug.FreeOSMemory() // a clean baseline: the generator's garbage is returned first
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	ws, _, _ := osMem()
	m := &meter{dir: dir, every: every, stop: make(chan struct{}), done: make(chan struct{}), baseHeap: ms.HeapInuse, baseWS: ws}
	m.u0, m.k0 = osCPU()
	m.start = time.Now()
	go m.loop()
	return m
}

func (m *meter) observe() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	ws, peak, priv := osMem()
	m.mu.Lock()
	defer m.mu.Unlock()
	if ms.HeapInuse > m.m.MaxHeapInuse {
		m.m.MaxHeapInuse = ms.HeapInuse
	}
	if ms.Sys > m.m.MaxSys {
		m.m.MaxSys = ms.Sys
	}
	if ws > m.m.MaxWS {
		m.m.MaxWS = ws
	}
	if priv > m.m.MaxPriv {
		m.m.MaxPriv = priv
	}
	m.m.PeakWS = peak
	m.m.EndHeap, m.m.EndWS = ms.HeapInuse, ws
	now := time.Now()
	if len(m.m.Series) == 0 || now.Sub(m.start)-m.m.Series[len(m.m.Series)-1].At >= time.Second {
		m.m.Series = append(m.m.Series, sample{At: now.Sub(m.start), HeapInuse: ms.HeapInuse, WS: ws})
	}
	if m.dir != "" && now.Sub(m.lastDirCheck) >= time.Second {
		m.lastDirCheck = now
		if n := dirSize(m.dir); n > m.m.MaxDirBytes {
			m.m.MaxDirBytes = n
		}
	}
}

func (m *meter) loop() {
	defer close(m.done)
	tick := time.NewTicker(m.every)
	defer tick.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-tick.C:
			m.observe()
		}
	}
}

func (m *meter) finish() measurement {
	wall := time.Since(m.start)
	u1, k1 := osCPU()
	m.observe()
	close(m.stop)
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.m
	out.Wall = wall
	out.CPUUser, out.CPUKernel = u1-m.u0, k1-m.k0
	out.BaseHeap, out.BaseWS = m.baseHeap, m.baseWS
	return out
}

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

func mib(b uint64) string { return fmt.Sprintf("%.0f", float64(b)/(1<<20)) }

// growth prints heap-in-use and working set (MiB) at ten even points of the run,
// to show whether memory climbs with the input.
func (m measurement) growth() string {
	if len(m.Series) == 0 {
		return ""
	}
	var parts []string
	for i := 1; i <= 10; i++ {
		idx := (len(m.Series)*i)/10 - 1
		if idx < 0 {
			idx = 0
		}
		s := m.Series[idx]
		parts = append(parts, fmt.Sprintf("%s/%s", mib(s.HeapInuse), mib(s.WS)))
	}
	return strings.Join(parts, " ")
}

// report logs one result line and, when UNRAVIS_BENCH_OUT is set, appends it to
// that file so a run of several processes collects in one place.
func report(t *testing.T, name string, m measurement, extra string) {
	t.Helper()
	cpu := m.CPUUser + m.CPUKernel
	cores := 0.0
	if m.Wall > 0 {
		cores = cpu.Seconds() / m.Wall.Seconds()
	}
	line := fmt.Sprintf("RESULT %s wall=%.2fs cpu=%.2fs (user %.2f kernel %.2f) avg-cores=%.2f heapInuse-max=%sMiB sys-max=%sMiB ws-max=%sMiB peak-ws=%sMiB private-max=%sMiB base-heap=%sMiB base-ws=%sMiB end-heap=%sMiB end-ws=%sMiB",
		name, m.Wall.Seconds(), cpu.Seconds(), m.CPUUser.Seconds(), m.CPUKernel.Seconds(), cores,
		mib(m.MaxHeapInuse), mib(m.MaxSys), mib(m.MaxWS), mib(m.PeakWS), mib(m.MaxPriv), mib(m.BaseHeap), mib(m.BaseWS), mib(m.EndHeap), mib(m.EndWS))
	if m.MaxDirBytes > 0 {
		line += fmt.Sprintf(" peak-case-dir=%.0fMiB", float64(m.MaxDirBytes)/(1<<20))
	}
	if extra != "" {
		line += " " + extra
	}
	t.Log(line)
	t.Logf("GROWTH %s heap/ws MiB at 10%%..100%% of the run: %s", name, m.growth())
	if out := os.Getenv("UNRAVIS_BENCH_OUT"); out != "" {
		if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, line)
			fmt.Fprintf(f, "GROWTH %s %s\n", name, m.growth())
			_ = f.Close()
		}
	}
}

func percentile(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	idx := int(math.Ceil(p*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

/* ---------------------------------------------------------------- server */

// rig is a real encrypted SQLite case store behind the real HTTP handler.
type rig struct {
	t       *testing.T
	srv     *httptest.Server
	root    string
	client  *http.Client
	caseID  string
	caseDir string
}

// newRig builds the production wiring (cmd/audit-explorer/cases.go): the DPAPI
// protector, the SQLite store with the adiantum-encrypted VFS, every importer,
// and the case routes of internal/server. It then creates one case.
func newRig(t *testing.T, dir string) *rig {
	t.Helper()
	protector, err := sealedfile.CurrentUser()
	if err != nil {
		t.Skipf("encrypted storage is unavailable on this platform: %v", err)
	}
	root := filepath.Join(dir, "cases")
	store, err := sqlite.New(sqlite.Options{Root: root, Protector: protector})
	if err != nil {
		t.Fatal(err)
	}
	hm, err := hosts.Load(strings.NewReader(hostsJSON))
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(nil, nil, benchToken, false, server.WithCases(store, hm, all.Registry()))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	r := &rig{t: t, srv: srv, root: root, client: &http.Client{Timeout: 0}}
	res := r.do(http.MethodPost, "/api/cases", "application/json", strings.NewReader(`{"title":"perf bench"}`))
	var created struct {
		ID string `json:"id"`
	}
	r.decode(res, http.StatusCreated, &created)
	if created.ID == "" {
		t.Fatal("no case id")
	}
	r.caseID = created.ID
	r.caseDir = filepath.Join(root, created.ID)
	return r
}

func (r *rig) do(method, path, ctype string, body io.Reader) *http.Response {
	r.t.Helper()
	req, err := http.NewRequest(method, r.srv.URL+path, body)
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("X-Audit-Token", benchToken)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := r.client.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

func (r *rig) decode(res *http.Response, want int, v any) {
	r.t.Helper()
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		r.t.Fatal(err)
	}
	if res.StatusCode != want {
		r.t.Fatalf("status %d (want %d): %.300s", res.StatusCode, want, b)
	}
	if v != nil {
		if err := json.Unmarshal(b, v); err != nil {
			r.t.Fatalf("decode: %v: %.200s", err, b)
		}
	}
}

// importResult is the part of the import answer the notes use.
type importResult struct {
	Report struct {
		Rows     int64 `json:"rows"`
		Emitted  int64 `json:"emitted"`
		Rejected int64 `json:"rejected"`
	} `json:"report"`
	Record struct {
		Bytes  int64  `json:"bytes"`
		Rows   int64  `json:"rows"`
		Status string `json:"status"`
	} `json:"record"`
}

// importPath uploads path through POST /api/cases/{id}/imports, the real route:
// multipart parsing, the 64 KiB sniff, the importer, and the single SQLite
// transaction of the case store. The file is streamed from disk, never held.
func (r *rig) importPath(importerName, host, path string) (importResult, int64) {
	r.t.Helper()
	f, err := os.Open(path)
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var werr error
		defer func() { pw.CloseWithError(werr) }()
		for _, kv := range [][2]string{{"ackGating", "true"}, {"ackText", "Synthetic benchmark data, no real host."}, {"ackCustom", "true"}} {
			if werr = mw.WriteField(kv[0], kv[1]); werr != nil {
				return
			}
		}
		part, err := mw.CreateFormFile("file", filepath.Base(path))
		if err != nil {
			werr = err
			return
		}
		bw := bufio.NewWriterSize(part, 1<<20)
		if _, werr = io.Copy(bw, f); werr != nil {
			return
		}
		if werr = bw.Flush(); werr != nil {
			return
		}
		werr = mw.Close()
	}()
	q := url.Values{"importer": {importerName}}
	if host != "" {
		q.Set("host", host)
	}
	res := r.do(http.MethodPost, "/api/cases/"+r.caseID+"/imports?"+q.Encode(), mw.FormDataContentType(), pr)
	var out importResult
	r.decode(res, http.StatusCreated, &out)
	st, _ := f.Stat()
	return out, st.Size()
}

// caseFiles lists the case folder with sizes, for the notes.
func (r *rig) caseFiles() (total int64, desc string) {
	var parts []string
	_ = filepath.WalkDir(r.caseDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
			parts = append(parts, fmt.Sprintf("%s=%.1fMiB", filepath.Base(p), float64(info.Size())/(1<<20)))
		}
		return nil
	})
	return total, strings.Join(parts, ",")
}
