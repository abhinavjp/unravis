//go:build windows

package sampler

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"api-audit-explorer/internal/importer/procsnap"
	"api-audit-explorer/internal/importer/winlive"
)

// Real-OS smoke checks of the Windows source and a 30 second run. They run only
// with UNRAVIS_WINSMOKE=1 and report counts and shapes, never process names.

func winSmoke(t *testing.T) {
	t.Helper()
	if os.Getenv("UNRAVIS_WINSMOKE") != "1" {
		t.Skip("set UNRAVIS_WINSMOKE=1 to run the real-OS smoke checks")
	}
}

// startMarker runs a harmless long-lived process under a chosen image name: a
// copy of ping.exe in a temp folder, pinging the loopback address for count
// echoes (about one a second).
func startMarker(t *testing.T, image string, count int) *exec.Cmd {
	t.Helper()
	src := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	in, err := os.Open(src)
	if err != nil {
		t.Skipf("no ping.exe to copy: %v", err)
	}
	defer in.Close()
	dst := filepath.Join(t.TempDir(), image)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	out.Close()
	cmd := exec.Command(dst, "-n", strconv.Itoa(count), "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot run the copied image: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func handleCount(t *testing.T) uint32 {
	t.Helper()
	var n uint32
	cur := uintptr(^uintptr(0)) // the pseudo handle of this process
	if r, _, e := procGetProcessHandleCount.Call(cur, uintptr(unsafe.Pointer(&n))); r == 0 {
		t.Skipf("handle count: %v", e)
	}
	return n
}

type psRow struct {
	ws, priv   uint64
	handles    int
	threads    int
	cpu        float64
	haveCPU    bool
	haveMemory bool
}

// getProcess reads every process with Get-Process, as the reference.
func getProcess(t *testing.T) (map[uint32]psRow, uint64) {
	t.Helper()
	script := `$o=Get-CimInstance Win32_OperatingSystem; 'FREEKB,' + $o.FreePhysicalMemory;` +
		`Get-Process | ForEach-Object { $c=''; try { $c=[math]::Round($_.TotalProcessorTime.TotalSeconds,3) } catch {};` +
		`'{0},{1},{2},{3},{4},{5}' -f $_.Id,$_.WorkingSet64,$_.PrivateMemorySize64,$_.HandleCount,$_.Threads.Count,$c }`
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		t.Skipf("Get-Process is not available: %v", err)
	}
	rows := map[uint32]psRow{}
	var freeKB uint64
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n") {
		f := strings.Split(line, ",")
		if len(f) == 2 && f[0] == "FREEKB" {
			freeKB, _ = strconv.ParseUint(f[1], 10, 64)
			continue
		}
		if len(f) != 6 {
			continue
		}
		pid, err := strconv.ParseUint(f[0], 10, 32)
		if err != nil {
			continue
		}
		var r psRow
		r.ws, _ = strconv.ParseUint(f[1], 10, 64)
		r.priv, _ = strconv.ParseUint(f[2], 10, 64)
		r.handles, _ = strconv.Atoi(f[3])
		r.threads, _ = strconv.Atoi(f[4])
		r.haveMemory = r.ws > 0 && r.priv > 0
		if c, err := strconv.ParseFloat(f[5], 64); err == nil {
			r.cpu, r.haveCPU = c, true
		}
		rows[uint32(pid)] = r
	}
	return rows, freeKB
}

func within(a, b float64, rel, abs float64) bool {
	d := math.Abs(a - b)
	return d <= abs || d <= rel*math.Max(a, b)
}

// settle waits for the goroutine count to fall back to want or below.
func settle(want int) int {
	var n int
	for i := 0; i < 60; i++ {
		runtime.GC()
		n = runtime.NumGoroutine()
		if n <= want {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
	return n
}

type csvRow struct {
	ts, scope, process, pid, start, metric, value string
}

func bodyRows(t *testing.T, body []byte) []csvRow {
	t.Helper()
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("batch has %d lines", len(lines))
	}
	var rows []csvRow
	for _, l := range lines[2:] { // header and column line
		f := strings.Split(l, ",")
		if len(f) != 7 {
			t.Fatalf("a row has %d fields", len(f))
		}
		rows = append(rows, csvRow{f[0], f[1], f[2], f[3], f[4], f[5], f[6]})
	}
	return rows
}

func TestWinSmokeSamplerThirtySecondRun(t *testing.T) {
	winSmoke(t)
	runtime.GC()
	g0 := runtime.NumGoroutine()

	// The server marker (an image the sampler keeps by name) starts after the run
	// does and ends before it; the other marker is an ordinary image whose name
	// must never reach the batch.
	plainImage := fmt.Sprintf("zzprobe%d.exe", os.Getpid())

	log := &importLog{}
	s, err := Start(Config{IntervalSeconds: 5, Import: log.Import, Pseudonymize: pseudo})
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	defer s.Stop()

	time.Sleep(3 * time.Second)
	server := startMarker(t, "w3wp.exe", 60)
	plain := startMarker(t, plainImage, 60)
	serverPID := uint32(server.Process.Pid)

	// The reference reading, in the middle of the run.
	time.Sleep(12 * time.Second)
	src, err := OSSource()
	if err != nil {
		t.Fatal(err)
	}
	mine, err := src.Processes(nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, freeKB := getProcess(t)
	host, _ := src.Host()
	src.Close()

	// The server marker ends; the sampler must write its end mark.
	_ = server.Process.Kill()
	_ = server.Wait()
	_ = plain

	time.Sleep(time.Until(t0.Add(30 * time.Second)))
	stopStart := time.Now()
	s.Stop()
	stopTook := time.Since(stopStart)
	if stopTook > 3*time.Second {
		t.Errorf("Stop took %v", stopTook)
	}
	select {
	case <-s.Done():
	default:
		t.Error("Done is not closed after Stop")
	}
	st := s.Status()
	if st.Running || st.Stop != StopUser || st.Err != nil {
		t.Fatalf("status %+v", st)
	}
	if st.Samples < 5 || st.Samples > 7 {
		t.Errorf("%d samples in 30 s at 5 s", st.Samples)
	}
	if log.count() != 1 {
		t.Fatalf("%d batches, want 1", log.count())
	}
	body := log.bodies[0]
	t.Logf("run: samples=%d events=%d partial=%d batch=%d bytes stop=%v", st.Samples, st.Events, st.Partial, len(body), stopTook.Round(time.Millisecond))

	// The importer accepts the batch.
	evs, _ := parse(t, body)
	if len(evs) < 3*st.Samples {
		t.Errorf("%d events from %d samples", len(evs), st.Samples)
	}

	// The process-name filter: only server images keep their name; everything
	// else is a pseudonym or "other", and the plain marker's name appears nowhere.
	if strings.Contains(string(body), "zzprobe") || strings.Contains(strings.ToLower(string(body)), strings.ToLower(strings.TrimSuffix(plainImage, ".exe"))) {
		t.Fatal("the plain marker's image name reached the batch")
	}
	rows := bodyRows(t, body)
	real := map[string]bool{}
	servers := 0
	for _, p := range mine {
		k := imageKey(p.Image)
		if procsnap.IsServerImage(k) {
			servers++
		} else {
			real[k] = true
		}
	}
	perTick := map[string]map[string]bool{}
	var serverRows, nameRows int
	var starts, gones int
	var hostCPU []float64
	var hostTS []time.Time
	lastCPU := map[string]float64{}
	for _, r := range rows {
		if _, err := time.Parse(winlive.TimeFormat, r.ts); err != nil {
			t.Fatalf("a timestamp does not parse: %v", err)
		}
		switch r.scope {
		case winlive.ScopeHost:
			v, err := strconv.ParseFloat(r.value, 64)
			if err != nil || v < 0 {
				t.Fatalf("host value %q", r.value)
			}
			switch r.metric {
			case winlive.MetricHostCPU:
				if v > 100 {
					t.Errorf("host cpu %v", v)
				}
				hostCPU = append(hostCPU, v)
				ts, _ := time.Parse(winlive.TimeFormat, r.ts)
				hostTS = append(hostTS, ts)
			case winlive.MetricMemAvailable, winlive.MetricMemCommitted:
				if v == 0 {
					t.Errorf("%s is zero", r.metric)
				}
			default:
				t.Errorf("unknown host metric %q", r.metric)
			}
		case winlive.ScopeProc, winlive.ScopeMark, winlive.ScopeCarry:
			if !winlive.ValidProcess(r.process) {
				t.Fatalf("an invalid process label %q", "(label)")
			}
			if !procsnap.IsServerImage(r.process) && real[r.process] {
				t.Fatalf("a non-server image is stored under its own name")
			}
			if procsnap.IsServerImage(r.process) {
				serverRows++
			}
			if r.process == "other" {
				nameRows++
			}
			if r.pid == "" || r.start == "" {
				t.Fatalf("a %s row has no pid or start", r.scope)
			}
			if r.scope == winlive.ScopeProc {
				if perTick[r.ts] == nil {
					perTick[r.ts] = map[string]bool{}
				}
				perTick[r.ts][r.pid] = true
				v, err := strconv.ParseFloat(r.value, 64)
				if err != nil || v < 0 {
					t.Fatalf("proc value %q", r.value)
				}
				if r.metric == winlive.MetricThreads && v < 1 {
					t.Errorf("a process with %v threads", v)
				}
				if r.metric == winlive.MetricPrivateBytes && v == 0 {
					t.Errorf("a process with zero private bytes")
				}
				if r.metric == winlive.MetricCPUSeconds {
					key := r.pid + "/" + r.start
					if last, ok := lastCPU[key]; ok && v < last {
						t.Errorf("cumulative cpu went backwards")
					}
					lastCPU[key] = v
				}
			}
			if r.scope == winlive.ScopeMark && r.metric == winlive.MetricStart && r.pid == strconv.Itoa(int(serverPID)) {
				starts++
			}
			if r.scope == winlive.ScopeMark && r.metric == winlive.MetricGone && r.pid == strconv.Itoa(int(serverPID)) {
				gones++
			}
		default:
			t.Errorf("unknown scope %q", r.scope)
		}
	}
	t.Logf("rows=%d host-cpu=%d server-rows=%d other=%d ticks=%d", len(rows), len(hostCPU), serverRows, nameRows, len(perTick))
	if len(hostCPU) < st.Samples-1 {
		t.Errorf("%d host cpu rows for %d samples", len(hostCPU), st.Samples)
	}
	var nonzero int
	for _, v := range hostCPU {
		if v > 0 {
			nonzero++
		}
	}
	if nonzero == 0 {
		t.Error("host cpu is zero at every tick")
	}
	for i := 1; i < len(hostTS); i++ {
		if d := hostTS[i].Sub(hostTS[i-1]); d < 3*time.Second || d > 8*time.Second {
			t.Errorf("ticks %v apart at a 5 s interval", d)
		}
	}
	for ts, pids := range perTick {
		if len(pids) > 2*TopN+servers+2 {
			t.Errorf("tick %s lists %d processes (cap %d)", ts, len(pids), 2*TopN+servers+2)
		}
	}
	if serverRows == 0 {
		t.Error("the server marker never appears by name")
	}
	if starts != 1 {
		t.Errorf("%d start marks for the server marker, want 1", starts)
	}
	if gones != 1 {
		t.Errorf("%d end marks for the server marker, want 1", gones)
	}

	// Plausible against Get-Process: the same processes, read within seconds.
	var cmp, bad int
	var badWhy [5]int
	var freeOK bool
	for _, p := range mine {
		r, ok := ref[p.PID]
		if !ok || p.Have != HaveTimes|HaveMemory|HaveHandles || !r.haveMemory {
			continue
		}
		cmp++
		mis := false
		if !within(float64(p.PrivateBytes), float64(r.priv), 0.25, 8<<20) {
			badWhy[0]++
			mis = true
		}
		if !within(float64(p.WorkingSet), float64(r.ws), 0.5, 16<<20) {
			badWhy[1]++
			mis = true
		}
		if !within(float64(p.Handles), float64(r.handles), 0.2, 12) {
			badWhy[2]++
			mis = true
		}
		if !within(float64(p.Threads), float64(r.threads), 0.2, 3) {
			badWhy[3]++
			mis = true
		}
		if r.haveCPU && !(r.cpu >= p.CPUSeconds-0.02 && r.cpu-p.CPUSeconds < 20) {
			badWhy[4]++
			mis = true
		}
		if mis {
			bad++
		}
	}
	t.Logf("compared %d processes with Get-Process: %d differ (private=%d ws=%d handles=%d threads=%d cpu=%d)", cmp, bad, badWhy[0], badWhy[1], badWhy[2], badWhy[3], badWhy[4])
	if cmp < 20 {
		t.Errorf("only %d processes could be compared", cmp)
	}
	if bad*10 > cmp {
		t.Errorf("%d of %d processes differ from Get-Process", bad, cmp)
	}
	if freeKB > 0 {
		freeOK = within(float64(host.AvailableBytes), float64(freeKB)*1024, 0.5, 256<<20)
		if !freeOK {
			t.Errorf("available memory differs from the OS report")
		}
	}

	// No goroutine is left behind.
	if n := settle(g0); n > g0 {
		t.Errorf("goroutines: %d before, %d after", g0, n)
	}
}

// Stop returns at once, however far the next tick is, and a start/stop loop
// leaves nothing behind.
func TestWinSmokeSamplerStopIsPromptAndLeakFree(t *testing.T) {
	winSmoke(t)
	runtime.GC()
	g0 := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		log := &importLog{}
		s, err := Start(Config{IntervalSeconds: 60, Import: log.Import, Pseudonymize: pseudo})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
		begin := time.Now()
		s.Stop()
		if d := time.Since(begin); d > time.Second {
			t.Fatalf("Stop took %v at a 60 s interval", d)
		}
		s.Stop() // a second Stop is harmless
		if st := s.Status(); st.Running || st.Stop != StopUser {
			t.Fatalf("status %+v", st)
		}
		if log.count() != 0 {
			t.Fatalf("a batch was written with no tick: %d", log.count())
		}
	}
	if n := settle(g0); n > g0 {
		t.Errorf("goroutines: %d before, %d after 25 runs", g0, n)
	}
}

// The real source never leaks a process handle across many reads.
func TestWinSmokeSourceHandleStability(t *testing.T) {
	winSmoke(t)
	src, err := OSSource()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var buf []ProcSample
	read := func() {
		var err error
		buf, err = src.Processes(buf[:0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := src.Host(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		read()
	}
	before := handleCount(t)
	for i := 0; i < 100; i++ {
		read()
	}
	after := handleCount(t)
	if after > before+20 {
		t.Errorf("process handles grew from %d to %d over 100 reads of %d processes", before, after, len(buf))
	}
}
