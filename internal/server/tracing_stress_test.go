package server

// Stress test of the Investigation Timeline (the tracing route) on a large
// synthetic case made by cmd/unravis-perfdata. It is skipped unless
// UNRAVIS_STRESS_DIR names a dataset folder (the one that holds manifest.json
// and cfg\cases). Windows only: the case store seals its keys with DPAPI.
//
// Make a dataset and run:
//
//	go run ./cmd/unravis-perfdata --size S --out .dev\perf-data\S
//	$env:UNRAVIS_STRESS_DIR='D:\Projects\api-audit-explorer\.dev\perf-data\S'
//	go test ./internal/server -run TestTracingStress -v -timeout 30m -count=1
//
// Optional: UNRAVIS_STRESS_MAX_SEC (a call slower than this fails, default 120),
// UNRAVIS_STRESS_MAX_HEAP_MB (growth of the Go heap over the run, default 600).
//
// For every window the answer must be 200, smaller than 2 MB, free of errors,
// and its bucket sums and "scanned" must equal the manifest, row for row. A lane
// that ran out of time (reason "timeout") is allowed and counted; its complete
// buckets must still be right. Wall time is logged per call and summarised.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/perfdata"
	"api-audit-explorer/internal/sealedfile"
)

func stressEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

type stressRun struct {
	name    string
	wall    time.Duration
	bytes   int
	reasons string
}

type stressRig struct {
	t      *testing.T
	e      *caseEnv
	id     string
	m      perfdata.Manifest
	maxSec time.Duration
	mu     sync.Mutex
	runs   []stressRun
	seq    int64
	notes  []string
}

func (r *stressRig) nextSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return r.seq
}

func stressBody(from, to time.Time, seq int64) string {
	return `{"incident":{"start":"` + from.UTC().Format(time.RFC3339Nano) + `","end":"` + to.UTC().Format(time.RFC3339Nano) + `"},"requestSeq":` + strconv.FormatInt(seq, 10) + `}`
}

// call posts one window and returns the answer, its size and its wall time. It
// never calls t.Fatal, so goroutines may use it.
func (r *stressRig) call(from, to time.Time) (tracingResponse, int, time.Duration, string) {
	start := time.Now()
	res := r.e.json(http.MethodPost, tracingPath(r.id), stressBody(from, to, r.nextSeq()))
	wall := time.Since(start)
	if res.Code != http.StatusOK {
		return tracingResponse{}, res.Body.Len(), wall, "status " + strconv.Itoa(res.Code) + ": " + stressTrunc(res.Body.String(), 200)
	}
	var out tracingResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		return out, res.Body.Len(), wall, "bad json: " + err.Error()
	}
	return out, res.Body.Len(), wall, ""
}

func stressTrunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// check compares one answer with the manifest. exact is true for windows on
// whole hours; otherwise lo and hi bound the truth by the inner and outer
// whole-hour windows.
func (r *stressRig) check(name string, out tracingResponse, size int, wall time.Duration, from, to time.Time, exact bool) []string {
	var bad []string
	if size >= 2<<20 {
		bad = append(bad, name+": response is "+strconv.Itoa(size)+" bytes, over 2 MB")
	}
	if wall > r.maxSec {
		bad = append(bad, name+": took "+wall.String())
	}
	reasons := []string{}
	for _, lane := range []string{perfdata.LaneIIS, perfdata.LaneSQL} {
		var l *tracingLane
		for i := range out.Lanes {
			if out.Lanes[i].Key == lane {
				l = &out.Lanes[i]
			}
		}
		wantRows, wantErrs, _ := r.m.Sum(lane, from.Truncate(time.Hour), stressCeilHour(to))
		loRows, loErrs := int64(0), int64(0)
		if exact {
			loRows, loErrs = wantRows, wantErrs
		} else if lo, le, err := r.m.Sum(lane, stressCeilHour(from), to.Truncate(time.Hour)); err == nil && to.Truncate(time.Hour).After(stressCeilHour(from)) {
			loRows, loErrs = lo, le
		}
		if l == nil {
			if loRows > 0 {
				bad = append(bad, name+": no "+lane+" lane in the answer, expected at least "+strconv.FormatInt(loRows, 10)+" rows")
			}
			continue
		}
		switch l.ReasonCode {
		case "", tracingReasonRowCap, tracingReasonTimeout:
		default:
			bad = append(bad, name+": "+lane+" reason code "+l.ReasonCode)
		}
		if l.ReasonCode != "" {
			reasons = append(reasons, lane+"="+l.ReasonCode)
		}
		if l.State == "error" || l.Disabled != "" {
			bad = append(bad, name+": "+lane+" lane state "+l.State+" disabled "+l.Disabled)
		}
		var sum, errs, completeSum int64
		for _, b := range l.Buckets {
			sum += b.Count
			if b.Complete {
				completeSum += b.Count
			}
			if b.Errors != nil {
				errs += *b.Errors
			}
		}
		if l.ReasonCode == tracingReasonTimeout {
			if completeSum > wantRows {
				bad = append(bad, name+": "+lane+" timeout, complete buckets hold "+strconv.FormatInt(completeSum, 10)+" over the truth "+strconv.FormatInt(wantRows, 10))
			}
			continue
		}
		if sum < loRows || sum > wantRows || l.Scanned != sum {
			bad = append(bad, name+": "+lane+" bucket sum "+strconv.FormatInt(sum, 10)+" scanned "+strconv.FormatInt(l.Scanned, 10)+", manifest "+strconv.FormatInt(loRows, 10)+".."+strconv.FormatInt(wantRows, 10))
		}
		if lane == perfdata.LaneIIS && (errs < loErrs || errs > wantErrs) {
			bad = append(bad, name+": iis 5xx "+strconv.FormatInt(errs, 10)+", manifest "+strconv.FormatInt(loErrs, 10)+".."+strconv.FormatInt(wantErrs, 10))
		}
		if len(l.Events) > 2000 {
			bad = append(bad, name+": "+lane+" lane sends "+strconv.Itoa(len(l.Events))+" events")
		}
	}
	r.mu.Lock()
	r.runs = append(r.runs, stressRun{name: name, wall: wall, bytes: size, reasons: strings.Join(reasons, ",")})
	r.mu.Unlock()
	return bad
}

func stressCeilHour(t time.Time) time.Time {
	h := t.UTC().Truncate(time.Hour)
	if h.Equal(t.UTC()) {
		return h
	}
	return h.Add(time.Hour)
}

func stressPercentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}

func TestTracingStress(t *testing.T) {
	dir := os.Getenv("UNRAVIS_STRESS_DIR")
	if dir == "" {
		t.Skip("set UNRAVIS_STRESS_DIR to a dataset made by cmd/unravis-perfdata")
	}
	prot, err := sealedfile.CurrentUser()
	if err != nil {
		t.Skip("the case store needs Windows:", err)
	}
	m, err := perfdata.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	const big = 64 << 30
	store, err := sqlite.New(sqlite.Options{
		Root: perfdata.CaseRoot(dir), Protector: prot,
		MaxFileBytes: big, MaxFileRows: 1 << 40, MaxCaseBytes: big, MaxDBBytes: big, MaxTotalBytes: 4 * big, MinFreeBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	e := tagEnv(t, store, noRepo{})
	rig := &stressRig{t: t, e: e, id: m.CaseID, m: m, maxSec: time.Duration(stressEnvInt("UNRAVIS_STRESS_MAX_SEC", 120)) * time.Second}

	// Heap sampler: the peak growth of HeapInuse over the whole run.
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base, peak := ms.HeapInuse, ms.HeapInuse
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				var s runtime.MemStats
				runtime.ReadMemStats(&s)
				if s.HeapInuse > peak {
					peak = s.HeapInuse
				}
			}
		}
	}()

	day := 24 * time.Hour
	dataDays := int(m.WindowEnd.Sub(m.WindowStart) / day)
	mid := m.WindowStart.Add(time.Duration(dataDays/2) * day)
	type window struct {
		name     string
		from, to time.Time
		exact    bool
	}
	var wins []window
	seen := map[string]bool{}
	add := func(name string, from, to time.Time, exact bool) {
		k := from.String() + to.String()
		if seen[k] {
			return
		}
		seen[k] = true
		wins = append(wins, window{name, from, to, exact})
	}
	for _, span := range []int{1, 7, 14, 30, 90, 186} {
		d := time.Duration(span) * day
		sn := strconv.Itoa(span) + "d"
		add(sn+"-end", m.WindowEnd.Add(-d), m.WindowEnd, true)
		add(sn+"-start", m.WindowStart, m.WindowStart.Add(d), true)
		add(sn+"-mid", mid.Add(-d/2), mid.Add(d/2), true)
	}
	// Not on the hour: the answer lies between the inner and the outer whole-hour windows.
	add("30d-offhour", m.WindowEnd.Add(-30*day+17*time.Minute), m.WindowEnd.Add(-23*time.Minute), false)
	add("186d-offhour", m.WindowEnd.Add(-186*day+17*time.Minute), m.WindowEnd.Add(-23*time.Minute), false)

	for _, w := range wins {
		w := w
		t.Run(w.name, func(t *testing.T) {
			out, size, wall, errText := rig.call(w.from, w.to)
			if errText != "" {
				t.Errorf("%s: %s", w.name, errText)
				return
			}
			for _, b := range rig.check(w.name, out, size, wall, w.from, w.to, w.exact) {
				t.Error(b)
			}
			var parts []string
			for _, l := range out.Lanes {
				parts = append(parts, l.Key+":"+strconv.FormatInt(l.Scanned, 10)+"/"+strconv.Itoa(len(l.Buckets))+"b/"+l.ReasonCode)
			}
			t.Logf("%-14s %8.2fs %7d B  %s  width=%ds", w.name, wall.Seconds(), size, strings.Join(parts, " "), out.Window.BucketSeconds)
		})
	}

	// Parallel: three people at once, a few rounds, on the widest windows.
	t.Run("parallel", func(t *testing.T) {
		long := []window{wins[0]}
		for _, w := range wins {
			if strings.HasPrefix(w.name, "186d") || strings.HasPrefix(w.name, "90d") || strings.HasPrefix(w.name, "30d-end") {
				long = append(long, w)
			}
		}
		for round := 0; round < 3; round++ {
			var wg sync.WaitGroup
			var bmu sync.Mutex
			var bad []string
			for i := 0; i < 3; i++ {
				w := long[(round+i)%len(long)]
				wg.Add(1)
				go func() {
					defer wg.Done()
					out, size, wall, errText := rig.call(w.from, w.to)
					var b []string
					if errText != "" {
						b = []string{"parallel " + w.name + ": " + errText}
					} else {
						b = rig.check("parallel-"+w.name, out, size, wall, w.from, w.to, w.exact)
					}
					bmu.Lock()
					bad = append(bad, b...)
					bmu.Unlock()
				}()
			}
			wg.Wait()
			for _, b := range bad {
				t.Error(b)
			}
		}
	})

	close(stop)
	<-done
	growth := int64(peak) - int64(base)
	walls := make([]time.Duration, 0, len(rig.runs))
	timeouts := 0
	for _, r := range rig.runs {
		walls = append(walls, r.wall)
		if strings.Contains(r.reasons, tracingReasonTimeout) {
			timeouts++
		}
	}
	sort.Slice(walls, func(i, j int) bool { return walls[i] < walls[j] })
	summary := []string{
		"dataset " + filepath.Base(dir) + ": " + strconv.FormatInt(m.Totals.IIS, 10) + " iis rows, " + strconv.FormatInt(m.Totals.SQL, 10) + " sql rows, " + strconv.Itoa(dataDays) + " days",
		"calls " + strconv.Itoa(len(walls)) + ", timeouts " + strconv.Itoa(timeouts),
		"wall min " + stressPercentile(walls, 0).Round(time.Millisecond).String() + " median " + stressPercentile(walls, 0.5).Round(time.Millisecond).String() +
			" p95 " + stressPercentile(walls, 0.95).Round(time.Millisecond).String() + " max " + stressPercentile(walls, 1).Round(time.Millisecond).String(),
		"heap growth " + strconv.FormatInt(growth>>20, 10) + " MB (peak " + strconv.FormatUint(peak>>20, 10) + " MB)",
	}
	for _, r := range rig.runs {
		summary = append(summary, "  "+r.name+" "+r.wall.Round(time.Millisecond).String()+" "+strconv.Itoa(r.bytes)+"B "+r.reasons)
	}
	for _, s := range summary[:4] {
		t.Log(s)
	}
	_ = os.WriteFile(filepath.Join(dir, "stress-results.txt"), []byte(strings.Join(summary, "\n")+"\n"), 0o644)
	if limit := int64(stressEnvInt("UNRAVIS_STRESS_MAX_HEAP_MB", 600)); growth>>20 > limit {
		t.Errorf("heap grew %d MB, limit %d MB", growth>>20, limit)
	}
}
