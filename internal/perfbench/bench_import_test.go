package perfbench

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// importCase generates a file with gen, imports it through the real HTTP importer
// into a real encrypted case store, and reports the result.
func importCase(t *testing.T, name, importerName string, gen func(path string) (int64, error)) *rig {
	t.Helper()
	dir := benchDir(t)
	path := filepath.Join(dir, "input.dat")
	g0 := time.Now()
	size, err := gen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %.1f MiB in %s (not part of the measurement)", float64(size)/(1<<20), time.Since(g0).Round(time.Millisecond))
	r := newRig(t, dir)
	m := startMeter(r.caseDir)
	res, fsize := r.importPath(importerName, "web-a", path)
	meas := m.finish()
	total, files := r.caseFiles()
	rate := func(n int64) float64 { return float64(n) / meas.Wall.Seconds() }
	extra := fmt.Sprintf("input=%.1fMiB rows=%d emitted=%d rejected=%d status=%s rows/s=%.0f events/s=%.0f MiB/s=%.1f case-file=%.1fMiB (%s)",
		float64(fsize)/(1<<20), res.Report.Rows, res.Report.Emitted, res.Report.Rejected, res.Record.Status,
		rate(res.Report.Rows), rate(res.Report.Emitted), float64(fsize)/(1<<20)/meas.Wall.Seconds(), float64(total)/(1<<20), files)
	report(t, name, meas, extra)
	if res.Record.Status != "" && res.Record.Status != "ok" {
		t.Errorf("import status %q", res.Record.Status)
	}
	return r
}

func TestBenchIISImport1M(t *testing.T) {
	need(t)
	n := envInt("UNRAVIS_BENCH_IIS_LINES", 1_000_000)
	importCase(t, fmt.Sprintf("iis-w3c-%dlines", n), "iis-w3c", func(p string) (int64, error) { return genIIS(p, n, 86400) })
}

func TestBenchIISImport5M(t *testing.T) {
	need(t)
	n := envInt("UNRAVIS_BENCH_IIS5_LINES", 5_000_000)
	importCase(t, fmt.Sprintf("iis-w3c-%dlines", n), "iis-w3c", func(p string) (int64, error) { return genIIS(p, n, 86400) })
}

func TestBenchLog4net1M(t *testing.T) {
	need(t)
	n := envInt("UNRAVIS_BENCH_LOG4NET_LINES", 1_000_000)
	importCase(t, fmt.Sprintf("log4net-text-%dentries", n), "log4net-text", func(p string) (int64, error) {
		b, _, err := genLog4net(p, n)
		return b, err
	})
}

func TestBenchWinEvents500k(t *testing.T) {
	need(t)
	n := envInt("UNRAVIS_BENCH_EVENTS", 500_000)
	importCase(t, fmt.Sprintf("win-events-%devents", n), "win-events", func(p string) (int64, error) { return genEvents(p, n) })
}

// TestBenchCounters imports 200 counters over 24 h at 15 s (1.15M samples), then
// times the series and resource queries over the imported set.
func TestBenchCounters(t *testing.T) {
	need(t)
	var cols int
	// UNRAVIS_BENCH_COUNTER_SECONDS shortens the span (default 24 h) to see how the
	// queries scale: a smaller case finishes inside the scan budget.
	span := envInt("UNRAVIS_BENCH_COUNTER_SECONDS", 86400)
	r := importCase(t, fmt.Sprintf("win-counters-200x%ds@15s", span), "win-counters", func(p string) (int64, error) {
		b, rows, c, err := genPDH(p, span, 15)
		cols = c
		t.Logf("pdh: %d rows x %d columns", rows, c)
		return b, err
	})
	_ = cols
	from := time.Date(2001, 10, 24, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	reqs := envInt("UNRAVIS_BENCH_QUERIES", 50)

	sel := func(object, counter, instance string) string {
		s := "kind=counter;source=win-counters;host=web-a;attr.object=" + object + ";attr.counter=" + counter
		if instance != "" {
			s += ";attr.instance=" + instance
		}
		return s
	}
	seriesPath := func(sels []string, bucketMs int64) string {
		q := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}, "bucket": {fmt.Sprint(bucketMs)}}
		for _, s := range sels {
			q.Add("sel", s)
		}
		return "/api/cases/" + r.caseID + "/series?" + q.Encode()
	}
	var ten, fifty []string
	for k := 0; k < 50; k++ {
		s := sel("Process", "% Processor Time", fmt.Sprintf("w3wp#%d", k))
		fifty = append(fifty, s)
		if k < 10 {
			ten = append(ten, s)
		}
	}
	total := sel("Processor", "% Processor Time", "_Total")

	type q struct {
		name string
		path string
		reqs int // each request is slow when the scan budget is hit, so the secondary queries use fewer
	}
	short := envInt("UNRAVIS_BENCH_QUERIES_SHORT", 10)
	epq := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)},
		"ep": {from.Add(10 * time.Hour).Format(time.RFC3339) + "/" + from.Add(10*time.Hour+10*time.Minute).Format(time.RFC3339)}}
	queries := []q{
		{"series 1 selector, 5 min buckets (288)", seriesPath([]string{total}, 300_000), reqs},
		{"series 10 selectors, 5 min buckets", seriesPath(ten, 300_000), short},
		{"series 50 selectors, 5 min buckets", seriesPath(fifty, 300_000), short},
		{"series 1 selector, 1 min buckets (1440)", seriesPath([]string{total}, 60_000), short},
		{"resource (RC1-RC9), 24 h, one episode", "/api/cases/" + r.caseID + "/resource?" + epq.Encode(), reqs},
	}
	for _, qq := range queries {
		var lat []time.Duration
		var size int
		var partial bool
		var scanned int64
		m := startMeter("")
		for i := 0; i < qq.reqs; i++ {
			t0 := time.Now()
			res := r.do(http.MethodGet, qq.path, "", nil)
			b, err := io.ReadAll(res.Body)
			res.Body.Close()
			lat = append(lat, time.Since(t0))
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s: status %d: %.300s", qq.name, res.StatusCode, b)
			}
			size = len(b)
			partial = strings.Contains(string(b), `"partial":true`)
			var sc struct {
				Scanned int64 `json:"scanned"`
			}
			if json.Unmarshal(b, &sc) == nil {
				scanned = sc.Scanned
			}
		}
		meas := m.finish()
		report(t, "query "+qq.name, meas, fmt.Sprintf("requests=%d p50=%s p95=%s max=%s min=%s response=%dB partial=%v scanned=%d",
			qq.reqs, percentile(lat, 0.50).Round(time.Millisecond), percentile(lat, 0.95).Round(time.Millisecond),
			percentile(lat, 1).Round(time.Millisecond), percentile(lat, 0).Round(time.Millisecond), size, partial, scanned))
	}
}
