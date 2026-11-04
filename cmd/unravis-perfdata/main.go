// Command unravis-perfdata builds a synthetic performance dataset (a case with
// millions of IIS rows and a thin SQL stream) for the Investigation Timeline
// stress tests. Output goes under --out: cfg/cases/<id>/ (the case, usable as an
// AUDIT_CONFIG_DIR) and manifest.json (the truth the tests compare against).
//
//	unravis-perfdata --size S --out .dev\perf-data\S
//	unravis-perfdata --months 3 --iis-rows 5000000 --out .dev\perf-data\x
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"api-audit-explorer/internal/perfdata"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "unravis-perfdata:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		size      = flag.String("size", "", "preset: T (3 days), S (1 month, 2M rows), M (3 months), L (6 months)")
		months    = flag.Int("months", 0, "window length in months, 1 to 12 (2M IIS rows per month)")
		iisRows   = flag.Int64("iis-rows", 0, "total IIS rows (overrides the preset)")
		sqlRows   = flag.Int64("sql-rows", 0, "total SQL rows (default: 1% of IIS rows)")
		end       = flag.String("end", "", "exclusive end of the window, RFC3339 at UTC midnight (default 2026-10-31T00:00:00Z)")
		seed      = flag.Uint64("seed", perfdata.DefaultSeed, "random seed")
		endpoints = flag.Int("endpoints", perfdata.DefaultEndpoints, "distinct endpoints")
		chunkDays = flag.Int("chunk-days", perfdata.DefaultChunkDays, "days per imported file")
		out       = flag.String("out", "", "output folder (required, must not hold a dataset yet)")
		title     = flag.String("case-title", "", "case title")
		raw       = flag.Bool("raw-files", false, "also write the W3C text and XE CSV files (size S or smaller)")
		quiet     = flag.Bool("quiet", false, "no progress lines")
	)
	flag.Parse()
	if *out == "" {
		return fmt.Errorf("--out is required")
	}
	var cfg perfdata.Config
	var err error
	switch {
	case *size != "" && *months != 0:
		return fmt.Errorf("use --size or --months, not both")
	case *size != "":
		cfg, err = perfdata.Preset(*size)
	case *months != 0:
		cfg, err = perfdata.ForMonths(*months)
	default:
		return fmt.Errorf("give --size T|S|M|L or --months N")
	}
	if err != nil {
		return err
	}
	if *iisRows > 0 {
		cfg.IISRows = *iisRows
		cfg.SQLRows = 0
	}
	if *sqlRows > 0 {
		cfg.SQLRows = *sqlRows
	}
	if *end != "" {
		t, perr := time.Parse(time.RFC3339, *end)
		if perr != nil {
			return fmt.Errorf("--end: %w", perr)
		}
		cfg.End = t
	}
	cfg.Seed, cfg.Endpoints, cfg.ChunkDays = *seed, *endpoints, *chunkDays
	cfg.Out, cfg.CaseTitle, cfg.RawFiles = *out, *title, *raw
	if cfg.Preset == "T" || cfg.Preset == "S" || cfg.Preset == "M" || cfg.Preset == "L" {
		// The preset's span stays unless --months was given.
		cfg.Preset = strings.ToUpper(cfg.Preset)
	}
	if err := cfg.Normalize(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("window %s to %s (%d days), %d IIS rows, %d SQL rows, seed %d\n",
		cfg.Start().Format("2006-01-02"), cfg.WindowEnd().Format("2006-01-02"), cfg.Days(), cfg.IISRows, cfg.SQLRows, cfg.Seed)
	var progress func(perfdata.Stats)
	if !*quiet {
		progress = func(s perfdata.Stats) {
			fmt.Printf("  %-3s file %d/%d  %d/%d rows  %.0f rows/s  %s\n", s.Phase, s.Chunk, s.Chunks, s.Rows, s.TotalRows, s.RowsPerSec, s.Elapsed.Round(time.Second))
		}
	}
	res, err := perfdata.Generate(ctx, cfg, progress)
	if err != nil {
		return err
	}
	fmt.Printf("done in %s: %.0f rows/s\n", res.Elapsed.Round(time.Second), res.RowsPerSec)
	fmt.Printf("case %s: %.2f GB in %s\n", res.Manifest.CaseID, float64(res.CaseBytes)/(1<<30), res.CaseDir)
	fmt.Printf("manifest: %s\n", res.ManifestDir+string(os.PathSeparator)+perfdata.ManifestName)
	fmt.Printf("config folder (AUDIT_CONFIG_DIR): %s\n", res.ManifestDir+string(os.PathSeparator)+"cfg")
	return nil
}
