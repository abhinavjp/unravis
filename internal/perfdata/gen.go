package perfdata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/sealedfile"
)

// Importer names of the two lanes.
const (
	ImporterIIS = "iis-w3c"
	ImporterSQL = "sql-xe"
)

const ackText = "Synthetic performance data, not from a real system."

// Stats is one progress report.
type Stats struct {
	Phase      string // "iis" or "sql"
	Chunk      int    // 1-based
	Chunks     int
	Rows       int64 // events written so far, both lanes
	TotalRows  int64
	Elapsed    time.Duration
	RowsPerSec float64
}

// Result is what Generate returns besides the manifest.
type Result struct {
	Manifest    Manifest
	CaseDir     string
	CaseBytes   int64
	Elapsed     time.Duration
	RowsPerSec  float64
	ManifestDir string
}

// CaseRoot is where the store keeps the case folders of a dataset in out.
func CaseRoot(out string) string { return filepath.Join(out, "cfg", "cases") }

type tally struct {
	start time.Time
	hours [][3]int64 // iis, sql, 5xx per hour
	days  [][3]int64
}

func newTally(start time.Time, days int) *tally {
	return &tally{start: start, hours: make([][3]int64, days*24), days: make([][3]int64, days)}
}

func (t *tally) add(ts time.Time, lane int, errs int64) {
	h := int(ts.Sub(t.start) / time.Hour)
	t.hours[h][lane]++
	t.days[h/24][lane]++
	if errs > 0 {
		t.hours[h][2] += errs
		t.days[h/24][2] += errs
	}
}

// Generate builds the dataset described by cfg in cfg.Out and returns what it
// holds. It refuses an output folder that already has a dataset.
func Generate(ctx context.Context, cfg Config, progress func(Stats)) (Result, error) {
	if err := cfg.Normalize(); err != nil {
		return Result{}, err
	}
	if cfg.Out == "" {
		return Result{}, errors.New("out is required")
	}
	if cfg.RawFiles && cfg.IISRows > 2*RowsPerMonth {
		return Result{}, errors.New("raw files are only written for sizes up to S")
	}
	if _, err := os.Stat(filepath.Join(cfg.Out, "cfg")); err == nil {
		return Result{}, fmt.Errorf("%s already holds a dataset; pick an empty folder", cfg.Out)
	}
	if err := os.MkdirAll(cfg.Out, 0o755); err != nil {
		return Result{}, err
	}
	prot, err := sealedfile.CurrentUser()
	if err != nil {
		return Result{}, fmt.Errorf("the case store needs Windows: %w", err)
	}
	const big = 64 << 30
	store, err := sqlite.New(sqlite.Options{
		Root: CaseRoot(cfg.Out), Protector: prot,
		MaxFileBytes: big, MaxFileRows: 1 << 40, MaxCaseBytes: big, MaxDBBytes: big, MaxTotalBytes: 4 * big,
		MinFreeBytes: -1,
	})
	if err != nil {
		return Result{}, err
	}
	defer store.Close()
	info, err := store.Create(ctx, cfg.CaseTitle)
	if err != nil {
		return Result{}, err
	}
	c, err := store.Open(ctx, info.ID)
	if err != nil {
		return Result{}, err
	}
	defer c.Close()

	began := time.Now()
	plan := BuildPlan(cfg)
	eps := newEndpoints(cfg.Endpoints)
	tl := newTally(cfg.Start(), cfg.Days())
	total := cfg.IISRows + cfg.SQLRows
	var done atomic.Int64
	lastReport := time.Now()
	report := func(phase string, chunk, chunks int, force bool) {
		if progress == nil || (!force && time.Since(lastReport) < 3*time.Second) {
			return
		}
		lastReport = time.Now()
		el := time.Since(began)
		n := done.Load()
		progress(Stats{Phase: phase, Chunk: chunk, Chunks: chunks, Rows: n, TotalRows: total, Elapsed: el, RowsPerSec: float64(n) / max(el.Seconds(), 0.001)})
	}

	chunks := (cfg.Days() + cfg.ChunkDays - 1) / cfg.ChunkDays
	for ch := 0; ch < chunks; ch++ {
		first := ch * cfg.ChunkDays
		last := min(first+cfg.ChunkDays, cfg.Days())
		label := cfg.Start().AddDate(0, 0, first).Format("20060102")
		for _, lane := range []string{LaneIIS, LaneSQL} {
			g := &chunkGen{ctx: ctx, cfg: cfg, plan: plan, eps: eps, tl: tl, caseID: string(info.ID), first: first, last: last, done: &done, report: func(force bool) { report(lane, ch+1, chunks, force) }}
			var meta casestore.ImportMeta
			var run casestore.RunFunc
			if lane == LaneIIS {
				meta = casestore.ImportMeta{Importer: ImporterIIS, FileName: "iis-" + label + ".log", HostLabel: "web-a"}
				run = g.runIIS
			} else {
				meta = casestore.ImportMeta{Importer: ImporterSQL, FileName: "xe-" + label + ".csv", HostLabel: "sql-a"}
				run = g.runSQL
			}
			meta.TZRule, meta.AckGating, meta.AckText = "utc-field", true, ackText
			body := strings.NewReader("perfdata " + lane + " seed=" + strconv.FormatUint(cfg.Seed, 10) + " from=" + label + " days=" + strconv.Itoa(last-first))
			g.fileLabel = meta.FileName
			g.raw = newRawSink(cfg, lane, meta.FileName)
			_, err := c.Import(ctx, meta, body, run)
			if cerr := g.raw.close(); err == nil {
				err = cerr
			}
			if err != nil {
				return Result{}, fmt.Errorf("%s chunk %d: %w", lane, ch+1, err)
			}
			report(lane, ch+1, chunks, true)
		}
	}

	// The emitted counts must be the plan, day by day.
	for d, dp := range plan.Days() {
		if tl.days[d][0] != dp.IIS || tl.days[d][1] != dp.SQL {
			return Result{}, fmt.Errorf("day %s: emitted %d/%d rows, planned %d/%d", dp.Day.Format("2006-01-02"), tl.days[d][0], tl.days[d][1], dp.IIS, dp.SQL)
		}
	}
	m := buildManifest(cfg, plan, tl, string(info.ID))
	if err := m.Save(cfg.Out); err != nil {
		return Result{}, err
	}
	el := time.Since(began)
	caseDir := filepath.Join(CaseRoot(cfg.Out), string(info.ID))
	return Result{
		Manifest: m, CaseDir: caseDir, CaseBytes: FolderBytes(caseDir), Elapsed: el,
		RowsPerSec: float64(total) / max(el.Seconds(), 0.001), ManifestDir: cfg.Out,
	}, nil
}

func buildManifest(cfg Config, plan *Plan, tl *tally, caseID string) Manifest {
	m := Manifest{
		Version: ManifestVersion, Seed: cfg.Seed, Preset: cfg.Preset, CaseID: caseID, CaseTitle: cfg.CaseTitle,
		WindowStart: cfg.Start(), WindowEnd: cfg.WindowEnd(), ChunkDays: cfg.ChunkDays,
		Bursts: plan.Bursts, Outages: plan.Outages,
	}
	sort.Slice(m.Bursts, func(i, j int) bool { return m.Bursts[i].Start.Before(m.Bursts[j].Start) })
	for d := range tl.days {
		c := tl.days[d]
		m.Totals.IIS += c[0]
		m.Totals.SQL += c[1]
		m.PerDay = append(m.PerDay, DayCount{Day: cfg.Start().AddDate(0, 0, d).Format("2006-01-02"), IIS: c[0], SQL: c[1], Errors5xxIS: c[2]})
	}
	for h, c := range tl.hours {
		if c[0] == 0 && c[1] == 0 {
			continue
		}
		hs := cfg.Start().Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
		m.PerHour = append(m.PerHour, HourCount{Hour: hs, IIS: c[0], SQL: c[1], Errors5xxIS: c[2]})
		if c[0] > m.BusiestHour.IIS {
			m.BusiestHour = Busiest{Hour: hs, IIS: c[0]}
		}
	}
	return m
}

// FolderBytes is the total size of the files under dir (0 when it is missing).
func FolderBytes(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if st, e := d.Info(); e == nil {
				n += st.Size()
			}
		}
		return nil
	})
	return n
}

// chunkGen holds what one import needs.
type chunkGen struct {
	cfg         Config
	plan        *Plan
	eps         *endpoints
	tl          *tally
	caseID      string
	fileLabel   string
	first, last int
	done        *atomic.Int64
	report      func(force bool)
	raw         *rawSink
	ctx         context.Context
}

const checkEvery = 4096

func (g *chunkGen) runIIS(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
	rep := importer.Report{Importer: ImporterIIS, KnownGaps: []string{"synthetic performance data"}}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return rep, err
	}
	line := int64(1)
	for d := g.first; d < g.last; d++ {
		dp := &g.plan.Days()[d]
		err := iisDay(g.cfg, g.eps, dp, func(row iisRow) error {
			line++
			dur := float64(row.ms)
			ev := evidence.Event{
				EvidenceID: evidence.EventID(g.caseID, ImporterIIS, g.fileLabel, line, 0),
				Source:     ImporterIIS, Kind: "request", Host: "web-a", Start: row.ts, End: row.ts,
				DurationMs: &dur, Line: line, TZRule: "utc-field",
				Attrs: map[string]string{
					"method": row.method, "stem": row.stem, "status": strconv.Itoa(row.status),
					"substatus": "0", "win32": "0", "client-ip": row.clientI,
				},
			}
			rep.Rows++
			if err := emit(ev); err != nil {
				return err
			}
			rep.Emitted++
			var e int64
			if row.status >= 500 {
				e = 1
			}
			g.tl.add(row.ts, 0, e)
			g.raw.iis(row)
			if g.done.Add(1)%checkEvery == 0 {
				g.report(false)
				if err := g.ctx.Err(); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (g *chunkGen) runSQL(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
	rep := importer.Report{Importer: ImporterSQL, KnownGaps: []string{"synthetic performance data"}}
	if _, err := io.Copy(io.Discard, r); err != nil {
		return rep, err
	}
	line := int64(1)
	for d := g.first; d < g.last; d++ {
		dp := &g.plan.Days()[d]
		err := sqlDay(g.cfg, dp, func(row sqlRow) error {
			line++
			ev := evidence.Event{
				EvidenceID: evidence.EventID(g.caseID, ImporterSQL, g.fileLabel, line, 0),
				Source:     ImporterSQL, Kind: row.kind, Host: "sql-a", Start: row.ts, End: row.ts,
				Severity: row.severity, Line: line, TZRule: "utc-field",
				Attrs: map[string]string{"event": row.event, "database": "AppDb"},
			}
			switch row.kind {
			case sqlWait:
				ev.Attrs["wait_type"], ev.Attrs["wait_class"] = row.waitType, row.class
			case sqlError:
				ev.Attrs["error_number"], ev.Attrs["sql_severity"] = strconv.Itoa(row.errNo), strconv.Itoa(row.sqlSev)
			}
			if row.ms > 0 {
				ms := row.ms
				ev.DurationMs, ev.Value, ev.Unit = &ms, &ms, "ms"
			}
			rep.Rows++
			if err := emit(ev); err != nil {
				return err
			}
			rep.Emitted++
			g.tl.add(row.ts, 1, 0)
			g.raw.sql(row)
			if g.done.Add(1)%checkEvery == 0 {
				g.report(false)
				if err := g.ctx.Err(); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}
