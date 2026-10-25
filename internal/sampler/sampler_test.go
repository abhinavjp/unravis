package sampler

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/winlive"
)

var base = time.Date(2001, 10, 25, 13, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func pseudo(kind, value string) string {
	h := fnv.New32a()
	h.Write([]byte(kind + value))
	return fmt.Sprintf("instance-%08x", h.Sum32())
}

// fakeSource is a Source driven by two functions of the call number. Call 0 is
// the baseline read at start; call n is the n-th tick.
type fakeSource struct {
	mu       sync.Mutex
	cores    int
	n        int
	hostFn   func(n int) (HostSample, error)
	procFn   func(n int) []ProcSample
	closed   bool
	hostErrs int // fail this many Host calls from the first tick on
}

func (f *fakeSource) Cores() int { return f.cores }
func (f *fakeSource) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeSource) Host() (HostSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.n
	f.n++
	if n > 0 && f.hostErrs > 0 {
		f.hostErrs--
		f.n--
		return HostSample{}, errors.New("host read failed")
	}
	return f.hostFn(n)
}
func (f *fakeSource) Processes(dst []ProcSample) ([]ProcSample, error) {
	f.mu.Lock()
	n := f.n - 1
	f.mu.Unlock()
	return append(dst, f.procFn(n)...), nil
}

// 25 percent busy at every tick.
func steadyHost(n int) (HostSample, error) {
	return HostSample{
		Times:          HostTimes{Idle: uint64(n) * 750, Kernel: uint64(n) * 1000},
		AvailableBytes: 4_000_000_000 + uint64(n), CommittedBytes: 6_000_000_000,
	}, nil
}

func proc(pid uint32, image string, cpu float64, private uint64) ProcSample {
	return ProcSample{
		PID: pid, Image: image, Threads: 10, Have: HaveTimes | HaveMemory | HaveHandles,
		Start: at(-3600), CPUSeconds: cpu, WorkingSet: private + 1000, PrivateBytes: private, Handles: 100,
	}
}

// A small server: w3wp uses 3 CPU seconds per 15 s tick.
func smallServer(n int) []ProcSample {
	return []ProcSample{
		proc(1200, "w3wp.exe", float64(3*n), 500_000_000),
		proc(3300, "sqlservr.exe", 1, 900_000_000),
		proc(4000, "chrome.exe", float64(n), 100_000_000),
	}
}

func newFake(procFn func(int) []ProcSample) *fakeSource {
	return &fakeSource{cores: 4, hostFn: steadyHost, procFn: procFn}
}

func newTestEngine(f *fakeSource, maxEvents int) *engine {
	e := newEngine(f, pseudo, 15, maxEvents, nil)
	if err := e.prime(at(0)); err != nil {
		panic(err)
	}
	return e
}

func parse(t *testing.T, body []byte) ([]evidence.Event, importer.Report) {
	t.Helper()
	evs, rep, err := importer.Collect(context.Background(), winlive.New(), strings.NewReader(string(body)), importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "n0",
		Host: &importer.HostHint{Label: "web-a", LogicalCores: 4},
	})
	if err != nil {
		t.Fatalf("the batch does not parse: %v\n%s", err, body)
	}
	if rep.Rejected != 0 {
		t.Fatalf("%d rows rejected\n%s", rep.Rejected, body)
	}
	return evs, rep
}

func attr(e evidence.Event, k string) string { return e.Attrs[k] }

func cpuEvents(evs []evidence.Event, process string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Kind == "metric" && attr(e, "metric") == "cpu" && attr(e, "process") == process {
			out = append(out, e)
		}
	}
	return out
}

func TestEngineCarriesCPUAcrossBatches(t *testing.T) {
	f := newFake(smallServer)
	e := newTestEngine(f, 1_000_000)
	var all []evidence.Event
	var first, second []byte
	for i := 1; i <= 6; i++ {
		if err := e.tick(at(15 * i)); err != nil {
			t.Fatal(err)
		}
		if i == 3 {
			_, first, _ = e.cut()
		}
	}
	_, second, _ = e.cut()
	for _, body := range [][]byte{first, second} {
		evs, _ := parse(t, body)
		all = append(all, evs...)
	}
	got := cpuEvents(all, "w3wp")
	if len(got) != 6 {
		t.Fatalf("w3wp has %d cpu events across two batches, want one per tick (6)", len(got))
	}
	for _, ev := range got {
		// 3 CPU seconds in 15 s on 4 cores is 5 percent of the host.
		if v := *ev.Value; v < 4.999 || v > 5.001 {
			t.Errorf("cpu at %v = %v, want 5", ev.Start, v)
		}
	}
	// The second batch opens with a carry row for each process it lists.
	lines := strings.Split(string(second), "\n")
	if !strings.Contains(lines[2], ",carry,") {
		t.Errorf("the second batch has no carry row first:\n%s", second)
	}
	if !strings.Contains(string(second), "w3wp,1200,"+at(-3600).Format(winlive.TimeFormat)+",cpu-seconds,") {
		t.Errorf("no w3wp carry row in:\n%s", second)
	}
	// Without the carry rows the first tick of the second batch gives no CPU.
	var stripped []string
	for _, l := range lines {
		if !strings.Contains(l, ",carry,") {
			stripped = append(stripped, l)
		}
	}
	evs, _ := parse(t, []byte(strings.Join(stripped, "\n")))
	if n := len(cpuEvents(evs, "w3wp")); n != 2 {
		t.Errorf("without carry rows the batch gives %d cpu events, want 2", n)
	}
}

func TestEngineHostRowsAndNoRawNames(t *testing.T) {
	f := newFake(smallServer)
	e := newTestEngine(f, 1_000_000)
	for i := 1; i <= 3; i++ {
		if err := e.tick(at(15 * i)); err != nil {
			t.Fatal(err)
		}
	}
	name, body, ok := e.cut()
	if !ok || name != "win-live-2001-10-25T13-00-15Z.csv" {
		t.Fatalf("name %q ok %v", name, ok)
	}
	if strings.Contains(string(body), "chrome") {
		t.Errorf("a raw process name is in the batch:\n%s", body)
	}
	evs, _ := parse(t, body)
	host := map[string]int{}
	for _, ev := range evs {
		if ev.Kind == "metric" && attr(ev, "process") == "" {
			host[attr(ev, "metric")]++
			if attr(ev, "metric") == "host-cpu" && (*ev.Value < 24.99 || *ev.Value > 25.01) {
				t.Errorf("host cpu %v, want 25", *ev.Value)
			}
		}
	}
	for _, m := range []string{"host-cpu", "mem-available", "mem-committed"} {
		if host[m] != 3 {
			t.Errorf("%s has %d events, want 3", m, host[m])
		}
	}
	if n := len(cpuEvents(evs, "")); n != 0 {
		t.Errorf("%d cpu events with no process", n)
	}
	// An empty batch is not written, and cut starts a fresh one.
	if _, _, ok := e.cut(); ok {
		t.Error("an empty batch was cut")
	}
}

func TestEngineChoosesTopProcessesAndServerImages(t *testing.T) {
	procs := func(n int) []ProcSample {
		out := []ProcSample{proc(1200, "w3wp.exe", 1, 10), proc(3300, "sqlservr.exe", 1, 10)} // idle servers
		for i := 0; i < 40; i++ {
			// Process i uses i+1 CPU seconds per tick; private bytes fall as i rises.
			out = append(out, proc(uint32(5000+i), fmt.Sprintf("app%02d.exe", i), float64((i+1)*n), uint64(1_000_000*(100-i))))
		}
		return out
	}
	f := newFake(procs)
	e := newTestEngine(f, 1_000_000)
	if err := e.tick(at(15)); err != nil {
		t.Fatal(err)
	}
	_, body, _ := e.cut()
	evs, _ := parse(t, body)
	pids := map[string]bool{}
	for _, ev := range evs {
		if p := attr(ev, "pid"); p != "" {
			pids[p] = true
		}
	}
	// 15 busiest by CPU (i = 25..39) and 15 by private bytes (i = 0..14) are
	// disjoint, plus the two server images.
	if len(pids) != 15+15+2 {
		t.Fatalf("%d processes listed, want 32", len(pids))
	}
	for _, want := range []string{"1200", "3300", "5039", "5025", "5000", "5014"} {
		if !pids[want] {
			t.Errorf("pid %s is not listed", want)
		}
	}
	for _, not := range []string{"5015", "5024"} {
		if pids[not] {
			t.Errorf("pid %s is listed but is in neither top list", not)
		}
	}
}

func TestEngineLeavesOutProcessesItCannotName(t *testing.T) {
	procs := func(n int) []ProcSample {
		return []ProcSample{
			proc(1200, "w3wp.exe", float64(3*n), 500),
			proc(4000, "chrome.exe", float64(n), 600),
			proc(4001, "my app.exe", float64(n), 700), // not an image name: "other"
		}
	}
	for name, p := range map[string]func(string, string) string{
		"empty":     func(string, string) string { return "" },
		"non-hex":   func(string, string) string { return "instance-zzzz" },
		"nil":       nil,
		"unrelated": func(string, string) string { return "chrome" },
	} {
		f := newFake(procs)
		e := newEngine(f, p, 15, 1_000_000, nil)
		if err := e.prime(at(0)); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 2; i++ {
			if err := e.tick(at(15 * i)); err != nil {
				t.Fatal(err)
			}
		}
		_, body, ok := e.cut()
		if !ok {
			t.Fatalf("%s: no batch", name)
		}
		if strings.Contains(string(body), "chrome") || strings.Contains(string(body), "my app") || strings.Contains(string(body), "other") {
			t.Errorf("%s: a raw name is in the batch:\n%s", name, body)
		}
		evs, _ := parse(t, body)
		if n := len(cpuEvents(evs, "w3wp")); n != 2 {
			t.Errorf("%s: w3wp has %d cpu events, want 2", name, n)
		}
	}
}

func TestEngineAccessDeniedGivesNameAndThreadsOnce(t *testing.T) {
	procs := func(n int) []ProcSample {
		return []ProcSample{
			proc(1200, "w3wp.exe", float64(3*n), 500),
			{PID: 3300, Image: "sqlservr.exe", Threads: 77}, // could not be opened
		}
	}
	f := newFake(procs)
	var noted []string
	e := newEngine(f, pseudo, 15, 1_000_000, func(label string, pid uint32) { noted = append(noted, fmt.Sprintf("%s/%d", label, pid)) })
	if err := e.prime(at(0)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if err := e.tick(at(15 * i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(noted) != 1 || noted[0] != "sqlservr/3300" {
		t.Errorf("partial notes = %v, want one for sqlservr/3300", noted)
	}
	if len(e.partial) != 1 {
		t.Errorf("%d partial processes, want 1", len(e.partial))
	}
	_, body, _ := e.cut()
	evs, _ := parse(t, body)
	got := map[string]int{}
	for _, ev := range evs {
		if attr(ev, "process") == "sqlservr" {
			got[attr(ev, "metric")]++
		}
	}
	if len(got) != 1 || got["threads"] != 3 {
		t.Errorf("sqlservr metrics = %v, want threads only, 3 times", got)
	}
}

func TestEngineMarksProcessStartAndGone(t *testing.T) {
	procs := func(n int) []ProcSample {
		out := []ProcSample{proc(1200, "w3wp.exe", float64(3*n), 500)}
		if n >= 2 && n < 4 { // a second w3wp lives for ticks 2 and 3
			p := proc(2200, "w3wp.exe", float64(n), 400)
			p.Start = at(15 + 5) // started 20 s after the baseline, 10 s before tick 2
			out = append(out, p)
		}
		return out
	}
	f := newFake(procs)
	e := newTestEngine(f, 1_000_000)
	for i := 1; i <= 5; i++ {
		if err := e.tick(at(15 * i)); err != nil {
			t.Fatal(err)
		}
	}
	_, body, _ := e.cut()
	evs, _ := parse(t, body)
	var starts, gones []evidence.Event
	for _, ev := range evs {
		switch ev.Kind {
		case "process-start":
			starts = append(starts, ev)
		case "process-gone":
			gones = append(gones, ev)
		}
	}
	if len(starts) != 1 || attr(starts[0], "pid") != "2200" || !starts[0].Start.Equal(at(20)) {
		t.Fatalf("process-start events = %+v", starts)
	}
	// Pid 1200 was there when the run began: no start mark for it.
	if len(gones) != 1 || attr(gones[0], "pid") != "2200" || !gones[0].End.Equal(at(60)) || !gones[0].Start.Equal(at(45)) {
		t.Fatalf("process-gone events = %+v", gones)
	}
}

func TestEngineRefusesATickPastTheCap(t *testing.T) {
	f := newFake(smallServer)
	e := newTestEngine(f, 40)
	var ticks int
	for i := 1; i <= 20; i++ {
		err := e.tick(at(15 * i))
		if errors.Is(err, errLimit) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		ticks++
	}
	if ticks == 0 || ticks == 20 {
		t.Fatalf("%d ticks fit under a cap of 40 events", ticks)
	}
	if e.events > 40 {
		t.Errorf("%d events counted, cap 40", e.events)
	}
	_, body, _ := e.cut()
	evs, _ := parse(t, body)
	if len(evs) > 40 {
		t.Errorf("the batch gives %d events, cap 40", len(evs))
	}
}

// --- the Sampler ----------------------------------------------------------

type fakeClock struct{ c chan time.Time }

func newFakeClock() *fakeClock { return &fakeClock{c: make(chan time.Time)} }

func (f *fakeClock) ticker(time.Duration) (<-chan time.Time, func()) { return f.c, func() {} }

// send delivers one tick and waits until the sampler has handled it.
func (f *fakeClock) send(t *testing.T, s *Sampler, tm time.Time, wantSamples int) {
	t.Helper()
	select {
	case f.c <- tm:
	case <-s.Done():
		return
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler does not take a tick")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status().Samples >= wantSamples {
			return
		}
		select {
		case <-s.Done():
			return
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the sampler did not reach %d samples", wantSamples)
}

type importLog struct {
	mu     sync.Mutex
	names  []string
	bodies [][]byte
	err    error
}

func (l *importLog) Import(name string, body []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.names = append(l.names, name)
	l.bodies = append(l.bodies, append([]byte(nil), body...))
	return nil
}

func (l *importLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.names)
}

func startFake(t *testing.T, f *fakeSource, clk *fakeClock, log *importLog, mod func(*Config)) *Sampler {
	t.Helper()
	cfg := Config{IntervalSeconds: 15, Import: log.Import, Pseudonymize: pseudo, Source: f}
	if mod != nil {
		mod(&cfg)
	}
	s, err := start(cfg, clk.ticker, func() time.Time { return base })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

func TestStartValidatesTheConfig(t *testing.T) {
	log := &importLog{}
	for _, iv := range []int{-5, 1, 7, 10, 45, 120} {
		if _, err := Start(Config{IntervalSeconds: iv, Import: log.Import, Source: newFake(smallServer)}); err == nil {
			t.Errorf("interval %d was accepted", iv)
		}
	}
	if _, err := Start(Config{Source: newFake(smallServer)}); err == nil {
		t.Error("a config with no import callback was accepted")
	}
	for _, iv := range []int{0, 5, 15, 30, 60} {
		f := newFake(smallServer)
		s, err := Start(Config{IntervalSeconds: iv, Import: log.Import, Source: f})
		if err != nil {
			t.Errorf("interval %d: %v", iv, err)
			continue
		}
		s.Stop()
		if !f.closed {
			t.Errorf("interval %d: the source was not closed", iv)
		}
	}
	if runtime.GOOS != "windows" {
		if _, err := Start(Config{Import: log.Import}); !errors.Is(err, ErrNotSupportedOS) {
			t.Errorf("no source off Windows: %v, want ErrNotSupportedOS", err)
		}
	}
	// A source that cannot be read at the start refuses the run.
	f := newFake(smallServer)
	f.hostFn = func(int) (HostSample, error) { return HostSample{}, errors.New("no host") }
	if _, err := Start(Config{Import: log.Import, Source: f}); err == nil {
		t.Error("an unreadable source was accepted")
	}
	if !f.closed {
		t.Error("the unreadable source was not closed")
	}
}

func TestSamplerFlushesEveryFiveMinutesAndOnStop(t *testing.T) {
	clk, log := newFakeClock(), &importLog{}
	s := startFake(t, newFake(smallServer), clk, log, nil)
	for i := 1; i <= 19; i++ { // 19 ticks of 15 s: 4 min 45 s
		clk.send(t, s, at(15*i), i)
	}
	if n := log.count(); n != 0 {
		t.Fatalf("%d batches written before five minutes", n)
	}
	clk.send(t, s, at(300), 20) // five minutes
	deadline := time.Now().Add(5 * time.Second)
	for log.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := log.count(); n != 1 {
		t.Fatalf("%d batches at five minutes, want 1", n)
	}
	clk.send(t, s, at(315), 21)
	clk.send(t, s, at(330), 22)
	s.Stop()
	if n := log.count(); n != 2 {
		t.Fatalf("%d batches after stop, want 2", n)
	}
	if st := s.Status(); st.Running || st.Stop != StopUser || st.Samples != 22 {
		t.Errorf("status %+v", st)
	}
	if log.names[0] != "win-live-2001-10-25T13-00-15Z.csv" || log.names[1] != "win-live-2001-10-25T13-05-15Z.csv" {
		t.Errorf("names %v", log.names)
	}
	// Both batches parse, and CPU runs unbroken across them: one event per tick.
	var all []evidence.Event
	for _, b := range log.bodies {
		evs, _ := parse(t, b)
		all = append(all, evs...)
	}
	if n := len(cpuEvents(all, "w3wp")); n != 22 {
		t.Errorf("w3wp has %d cpu events, want 22", n)
	}
	s.Stop() // again: no effect
}

func TestSamplerStopsAtTheEventCap(t *testing.T) {
	clk, log := newFakeClock(), &importLog{}
	f := newFake(smallServer)
	s := startFake(t, f, clk, log, func(c *Config) { c.MaxEvents = 60 })
	samples := 0
	for i := 1; i <= 50 && s.Status().Running; i++ {
		clk.send(t, s, at(15*i), i)
		samples = i
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler did not stop at the cap")
	}
	st := s.Status()
	if st.Running || st.Stop != StopLimit {
		t.Fatalf("status %+v, want stopped at the limit", st)
	}
	if st.Events > 60 || st.Events == 0 || samples >= 50 {
		t.Errorf("events %d (cap 60), ticks sent %d", st.Events, samples)
	}
	if !f.closed {
		t.Error("the source was not closed")
	}
	// What was sampled before the cap is imported.
	if log.count() != 1 {
		t.Fatalf("%d batches, want the last one written at the stop", log.count())
	}
	evs, _ := parse(t, log.bodies[0])
	if len(evs) > 60 {
		t.Errorf("%d events in the batch, cap 60", len(evs))
	}
}

func TestSamplerStopsAtTheDurationCap(t *testing.T) {
	clk, log := newFakeClock(), &importLog{}
	s := startFake(t, newFake(smallServer), clk, log, func(c *Config) { c.MaxDuration = time.Minute })
	for i := 1; i <= 4 && s.Status().Running; i++ {
		clk.send(t, s, at(15*i), i)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler did not stop at the duration cap")
	}
	if st := s.Status(); st.Stop != StopLimit || st.Samples != 4 {
		t.Errorf("status %+v, want the limit after 4 samples", st)
	}
	if log.count() != 1 {
		t.Errorf("%d batches, want 1", log.count())
	}
}

func TestSamplerStopsWhenTheImportFails(t *testing.T) {
	clk := newFakeClock()
	log := &importLog{err: errors.New("the case is closed")}
	s := startFake(t, newFake(smallServer), clk, log, func(c *Config) { c.FlushEvery = time.Minute })
	for i := 1; i <= 4; i++ { // the flush at one minute fails
		clk.send(t, s, at(15*i), i)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		// The failure is noticed after the flush; one more tick lets the loop see it.
		clk.send(t, s, at(75), 5)
		select {
		case <-s.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("the sampler kept running after an import failed")
		}
	}
	st := s.Status()
	if st.Stop != StopWriteError || st.Err == nil || st.Err.Error() != "the case is closed" {
		t.Errorf("status %+v, want a write error carrying the cause", st)
	}
}

func TestSamplerStopsWhenTheHostCannotBeRead(t *testing.T) {
	clk, log := newFakeClock(), &importLog{}
	f := newFake(smallServer)
	f.hostErrs = 1
	s := startFake(t, f, clk, log, nil)
	clk.send(t, s, at(15), 0) // one failed read is skipped
	clk.send(t, s, at(30), 1)
	if st := s.Status(); !st.Running || st.Samples != 1 {
		t.Fatalf("status %+v after one failed read", st)
	}
	f.mu.Lock()
	f.hostErrs = 3
	f.mu.Unlock()
	for i := 0; i < 3; i++ {
		clk.send(t, s, at(45+15*i), 1)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler did not stop after three failed reads")
	}
	if st := s.Status(); st.Stop != StopSourceError || st.Err == nil {
		t.Errorf("status %+v", st)
	}
	if log.count() != 1 {
		t.Errorf("%d batches, want the one sample written at the stop", log.count())
	}
}

func TestSamplerStoppedAtOnceWritesNothing(t *testing.T) {
	clk, log := newFakeClock(), &importLog{}
	s := startFake(t, newFake(smallServer), clk, log, nil)
	s.Stop()
	if log.count() != 0 {
		t.Errorf("%d batches from a run with no sample", log.count())
	}
	if st := s.Status(); st.Running || st.Stop != StopUser {
		t.Errorf("status %+v", st)
	}
}

func TestSamplerReportsPartialProcesses(t *testing.T) {
	clk, log := newFakeClock(), &importLog{}
	f := newFake(func(n int) []ProcSample {
		return []ProcSample{proc(1200, "w3wp.exe", float64(n), 1), {PID: 3300, Image: "sqlservr.exe", Threads: 9}}
	})
	var mu sync.Mutex
	var labels []string
	s := startFake(t, f, clk, log, func(c *Config) {
		c.OnPartial = func(l string, _ uint32) { mu.Lock(); labels = append(labels, l); mu.Unlock() }
	})
	clk.send(t, s, at(15), 1)
	clk.send(t, s, at(30), 2)
	if st := s.Status(); st.Partial != 1 {
		t.Errorf("partial = %d, want 1", st.Partial)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(labels) != 1 || labels[0] != "sqlservr" {
		t.Errorf("OnPartial labels %v", labels)
	}
}

func TestTopKKeepsTheLargest(t *testing.T) {
	var k topK
	for i := 0; i < 100; i++ {
		k.add(i, float64((i*37)%101))
	}
	if k.n != TopN {
		t.Fatalf("n = %d", k.n)
	}
	for j := 1; j < k.n; j++ {
		if k.score[j] > k.score[j-1] {
			t.Errorf("scores not in order: %v", k.score)
		}
	}
	if k.score[0] != 100 {
		t.Errorf("largest score %v, want 100", k.score[0])
	}
	var small topK
	small.add(7, 1)
	if small.n != 1 || small.idx[0] != 7 {
		t.Errorf("small = %+v", small)
	}
}

func TestOSSourceOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has a source")
	}
	if _, err := OSSource(); !errors.Is(err, ErrNotSupportedOS) {
		t.Errorf("OSSource = %v", err)
	}
}

// --- Stop is bounded (security review F2) ---------------------------------

func shortStop(t *testing.T) {
	t.Helper()
	old := stopTimeout
	stopTimeout = 150 * time.Millisecond
	t.Cleanup(func() { stopTimeout = old })
}

func waitDone(t *testing.T, s *Sampler) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler never finished once the blocker was released")
	}
}

func waitGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines, %d before: the sampler leaked", n, before)
	}
}

func TestStopDoesNotHangOnABlockedImport(t *testing.T) {
	shortStop(t)
	before := runtime.NumGoroutine()
	release := make(chan struct{})
	var mu sync.Mutex
	imports := 0
	clk := newFakeClock()
	s := startFake(t, newFake(smallServer), clk, &importLog{}, func(c *Config) {
		c.FlushEvery = time.Second // every tick cuts a batch
		c.Import = func(name string, body []byte) error {
			mu.Lock()
			imports++
			mu.Unlock()
			<-release
			return nil
		}
	})
	for i := 1; i <= 3; i++ {
		clk.send(t, s, at(15*i), i)
	}
	start := time.Now()
	s.Stop()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Stop took %v with the import blocked", d)
	}
	if st := s.Status(); st.Running || st.Stop != StopUser {
		t.Errorf("status after a bounded stop: %+v", st)
	}
	select {
	case <-s.Done():
		t.Fatal("Done closed while the import is still blocked")
	default:
	}
	close(release)
	waitDone(t, s)
	mu.Lock()
	n := imports
	mu.Unlock()
	if n < 2 {
		t.Errorf("%d imports after release, want the queued batches and the last one", n)
	}
	if st := s.Status(); st.Running || st.Stop != StopUser {
		t.Errorf("status after release: %+v", st)
	}
	waitGoroutines(t, before)
}

// gatedSource blocks Host calls while gate is set.
type gatedSource struct {
	*fakeSource
	mu      sync.Mutex
	block   bool
	entered chan struct{}
	release chan struct{}
}

func (g *gatedSource) Host() (HostSample, error) {
	g.mu.Lock()
	b := g.block
	g.mu.Unlock()
	if b {
		select {
		case g.entered <- struct{}{}:
		default:
		}
		<-g.release
	}
	return g.fakeSource.Host()
}

func TestStopDoesNotHangOnABlockedSourceRead(t *testing.T) {
	shortStop(t)
	before := runtime.NumGoroutine()
	g := &gatedSource{fakeSource: newFake(smallServer), entered: make(chan struct{}, 1), release: make(chan struct{})}
	clk, log := newFakeClock(), &importLog{}
	s := startFake(t, g.fakeSource, clk, log, func(c *Config) { c.Source = g })
	clk.send(t, s, at(15), 1)
	g.mu.Lock()
	g.block = true
	g.mu.Unlock()
	select {
	case clk.c <- at(30): // the tick that blocks in Host
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler does not take a tick")
	}
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Host was not reached")
	}
	start := time.Now()
	s.Stop()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Stop took %v with the source blocked", d)
	}
	if st := s.Status(); st.Running || st.Stop != StopUser {
		t.Errorf("status after a bounded stop: %+v", st)
	}
	close(g.release)
	waitDone(t, s)
	if log.count() != 1 {
		t.Errorf("%d batches, want the one written at the stop", log.count())
	}
	waitGoroutines(t, before)
}
