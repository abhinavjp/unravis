//go:build windows

package sqlite

// Throwaway scale measurement of the case store (counter / metric events). Skipped
// unless UNRAVIS_SCALE=1. Synthetic data only. Run:
//
//	UNRAVIS_SCALE=1 go test ./internal/casestore/sqlite -run Scale -v -timeout 20m

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

type procMemCounters struct {
	cb, PageFaultCount                                                  uint32
	PeakWorkingSetSize, WorkingSetSize, QuotaPeakPagedPoolUsage         uintptr
	QuotaPagedPoolUsage, QuotaPeakNonPagedPoolUsage, QuotaNonPagedUsage uintptr
	PagefileUsage, PeakPagefileUsage                                    uintptr
}

var (
	psapi          = syscall.NewLazyDLL("psapi.dll")
	getProcMemInfo = psapi.NewProc("GetProcessMemoryInfo")
)

func workingSet() uint64 {
	var c procMemCounters
	c.cb = uint32(unsafe.Sizeof(c))
	h, _ := syscall.GetCurrentProcess()
	r, _, _ := getProcMemInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb))
	if r == 0 {
		return 0
	}
	return uint64(c.WorkingSetSize)
}

type memPeak struct {
	heapAlloc, heapInuse, goSys, rss uint64
}

// sampleMem polls memory every 25 ms until stop is called, and returns the peaks.
func sampleMem() (stop func() memPeak) {
	runtime.GC()
	done := make(chan struct{})
	var wg sync.WaitGroup
	var p memPeak
	take := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		p.heapAlloc = max(p.heapAlloc, m.HeapAlloc)
		p.heapInuse = max(p.heapInuse, m.HeapInuse)
		p.goSys = max(p.goSys, m.Sys)
		p.rss = max(p.rss, workingSet())
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(25 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				take()
				return
			case <-t.C:
				take()
			}
		}
	}()
	return func() memPeak { close(done); wg.Wait(); return p }
}

func mb(b uint64) string { return strconv.FormatUint(b>>20, 10) + " MB" }

func (p memPeak) String() string {
	return "heapAlloc=" + mb(p.heapAlloc) + " heapInuse=" + mb(p.heapInuse) + " goSys=" + mb(p.goSys) + " RSS(workingset)=" + mb(p.rss)
}

// scaleRun streams n synthetic events without holding them: 80% counters (20
// counters x 5 instances = 100 series), 20% metrics (10 metrics x 4 processes =
// 40 series), 100 ms apart from testBase.
func scaleRun(c casestore.Case, label string, n int) casestore.RunFunc {
	id := string(c.Info().ID)
	objs := [5]string{"i0", "i1", "i2", "i3", "i4"}
	return func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: testImporter, KnownGaps: []string{"synthetic scale data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for i := 0; i < n; i++ {
			v := float64(i % 1000)
			line := int64(i + 2)
			e := evidence.Event{
				EvidenceID: evidence.EventID(id, testImporter, label, line, 0),
				Source:     testImporter, Host: "web-a", Value: &v, Line: line, TZRule: "utc-field",
				Start: testBase.Add(time.Duration(i) * 100 * time.Millisecond),
			}
			e.End = e.Start
			if i%5 == 4 {
				k := (i / 5) % 40
				e.Kind, e.Unit = "metric", "percent"
				e.Attrs = map[string]string{"metric": "m" + strconv.Itoa(k/4), "process": "p" + strconv.Itoa(k%4), "pid": strconv.Itoa(100 + k%4)}
			} else {
				k := (i - i/5) % 100
				e.Kind, e.Unit = "counter", "count"
				e.Attrs = map[string]string{"counter": "c" + strconv.Itoa(k/5), "object": "Memory", "instance": objs[k%5]}
			}
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	}
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return st.Size()
}

func dbFiles(c *Case) (total int64) {
	for _, suf := range []string{"", "-wal", "-shm"} {
		if n := fileSize(filepath.Join(c.dir, dbName+suf)); n > 0 {
			total += n
		}
	}
	return
}

func checkpoint(t *testing.T, c *Case) {
	t.Helper()
	if _, err := c.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
}

type seriesTest struct {
	name string
	sel  string
}

var scaleQueries = []seriesTest{
	{"one counter series (counter+object+instance, 1% of events)", "kind=counter;attr.counter=c7;attr.object=Memory;attr.instance=i2"},
	{"counter, no instance (5% of events)", "kind=counter;attr.counter=c7"},
	{"one metric series (metric+process, 0.5% of events)", "kind=metric;attr.metric=m3;attr.process=p1"},
}

func timeSeries(t *testing.T, c *Case, n int, sel string) (time.Duration, casestore.SeriesResult) {
	t.Helper()
	to := testBase.Add(time.Duration(n) * 100 * time.Millisecond)
	start := time.Now()
	res, err := casestore.Series(context.Background(), c, casestore.SeriesQuery{
		From: testBase, To: to, BucketMs: int64(n / 10), Sel: []string{sel},
	})
	d := time.Since(start)
	if err != nil {
		t.Fatalf("series %q: %v", sel, err)
	}
	return d, res
}

// best runs a query reps times and returns the median.
func median(t *testing.T, c *Case, n int, sel string, reps int) (time.Duration, casestore.SeriesResult) {
	ds := make([]time.Duration, reps)
	var res casestore.SeriesResult
	for i := range ds {
		ds[i], res = timeSeries(t, c, n, sel)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	return ds[len(ds)/2], res
}

func reopen(t *testing.T, s *Store, c *Case) *Case {
	t.Helper()
	id := c.Info().ID
	_ = c.Close()
	return mustOpen(t, s, id)
}

func TestScaleMeasure(t *testing.T) {
	if os.Getenv("UNRAVIS_SCALE") != "1" {
		t.Skip("set UNRAVIS_SCALE=1 to run the scale measurement")
	}
	sizes := []int{100_000, 500_000, 2_000_000}
	if v := os.Getenv("UNRAVIS_SCALE_SIZES"); v != "" {
		sizes = nil
		for _, f := range splitComma(v) {
			n, _ := strconv.Atoi(f)
			sizes = append(sizes, n)
		}
	}
	for _, n := range sizes {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			s, _ := newTestStore(t, func(o *Options) { o.MaxFileRows = 10_000_000 })
			info := mustCreate(t, s, "scale")
			c := mustOpen(t, s, info.ID)

			// 1. import
			stop := sampleMem()
			began := time.Now()
			if _, err := c.Import(context.Background(), tMeta("scale.csv"), tBody("scale"), scaleRun(c, "scale", n)); err != nil {
				t.Fatal(err)
			}
			imp := time.Since(began)
			pk := stop()
			checkpoint(t, c)
			t.Logf("[%d] IMPORT %v  peaks: %s", n, imp.Round(time.Millisecond), pk)
			withIdx := dbFiles(c)
			t.Logf("[%d] case.db with indexes (checkpointed): %d MB", n, withIdx>>20)

			// 2. indexed series queries (case reopened: attrIdx rediscovered, indexes exist)
			c = reopen(t, s, c)
			stop = sampleMem()
			for _, q := range scaleQueries {
				first, res := timeSeries(t, c, n, q.sel)
				med, _ := median(t, c, n, q.sel, 3)
				t.Logf("[%d] INDEXED  %-62s first=%v median=%v count=%d scanned=%d partial=%v", n, q.name, first.Round(time.Millisecond), med.Round(time.Millisecond), res.Series[0].Count, res.Scanned, res.Partial)
			}
			t.Logf("[%d] INDEXED query peaks: %s", n, stop())

			// 3. size without the partial indexes
			for _, ix := range []string{"ev_kind_counter", "ev_kind_metric"} {
				if _, err := c.db.Exec("DROP INDEX " + ix); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.db.Exec("VACUUM"); err != nil {
				t.Logf("[%d] VACUUM failed: %v", n, err)
			}
			checkpoint(t, c)
			woIdx := dbFiles(c)
			t.Logf("[%d] case.db without ev_kind_counter/metric (vacuumed): %d MB  (indexes cost %d MB)", n, woIdx>>20, (withIdx-woIdx)>>20)

			// 4. scan fallback: build disabled (attrIdx=2), no hint, kind index only
			c = reopen(t, s, c)
			c.attrIdx.Store(2)
			stop = sampleMem()
			for _, q := range scaleQueries {
				first, res := timeSeries(t, c, n, q.sel)
				med, _ := median(t, c, n, q.sel, 2)
				t.Logf("[%d] FALLBACK %-62s first=%v median=%v count=%d scanned=%d partial=%v", n, q.name, first.Round(time.Millisecond), med.Round(time.Millisecond), res.Series[0].Count, res.Scanned, res.Partial)
			}
			t.Logf("[%d] FALLBACK query peaks: %s", n, stop())

			// 5. lazy build on an old case: reopened, indexes absent, attrIdx 0
			c = reopen(t, s, c)
			stop = sampleMem()
			q := scaleQueries[0]
			first, res1 := timeSeries(t, c, n, q.sel)
			pkBuild := stop()
			second, res2 := timeSeries(t, c, n, q.sel)
			t.Logf("[%d] LAZY BUILD first query=%v second=%v (build ~ %v) attrIdx=%d count=%d/%d partial=%v/%v", n, first.Round(time.Millisecond), second.Round(time.Millisecond), (first - second).Round(time.Millisecond), c.attrIdx.Load(), res1.Series[0].Count, res2.Series[0].Count, res1.Partial, res2.Partial)
			t.Logf("[%d] LAZY BUILD peaks: %s", n, pkBuild)
			checkpoint(t, c)
			t.Logf("[%d] case.db after lazy build: %d MB", n, dbFiles(c)>>20)
		})
	}
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
