// Package perfdata builds a synthetic, fully deterministic dataset for the
// Investigation Timeline performance work: a case that holds millions of IIS
// request rows and a thin stream of SQL Server events over months, with daily
// and weekly rhythm, bursts of errors and one planned outage per calendar
// month. Every row is derived from the seed and the day, so two runs with the
// same Config give the same events (only the random case id differs).
//
// The generator writes straight into the case store (one import per chunk and
// lane) and a manifest.json that records what is in the case, so a stress test
// can compare the answer of the tracing route with the truth.
package perfdata

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Defaults of a Config.
const (
	DefaultSeed      uint64 = 42
	DefaultEndpoints        = 300
	DefaultChunkDays        = 30
	RowsPerMonth            = 2_000_000
)

// DefaultEnd is the end (exclusive) of the generated window.
var DefaultEnd = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)

// Config describes one dataset. Use Preset or fill the fields and call
// Normalize (Generate does it for you).
type Config struct {
	Preset    string    // "T", "S", "M", "L" or "custom"
	Months    int       // window length in calendar months (0: use SpanDays)
	SpanDays  int       // window length in days when Months is 0
	IISRows   int64     // total IIS request rows
	SQLRows   int64     // total SQL rows (0: one hundredth of IISRows)
	End       time.Time // exclusive end, UTC midnight (zero: DefaultEnd)
	Seed      uint64    // 0: DefaultSeed
	Endpoints int       // distinct endpoints (0: 300)
	ChunkDays int       // days per imported file (0: 30)
	Out       string    // output folder (required by Generate)
	CaseTitle string
	RawFiles  bool // also write the W3C text and XE CSV files (small presets only)

	start time.Time
	days  int
}

// Start is the first instant of the window (after Normalize).
func (c Config) Start() time.Time { return c.start }

// Days is the number of whole UTC days in the window (after Normalize).
func (c Config) Days() int { return c.days }

// WindowEnd is the exclusive end of the window (after Normalize).
func (c Config) WindowEnd() time.Time { return c.start.AddDate(0, 0, c.days) }

// Preset returns the Config of a named size: T (3 days, 20 000 rows), S (1 month,
// 2M), M (3 months, 6M) and L (6 months, 12M). The name is case-insensitive.
func Preset(name string) (Config, error) {
	c := Config{Preset: strings.ToUpper(strings.TrimSpace(name))}
	switch c.Preset {
	case "T":
		c.SpanDays, c.IISRows = 3, 20_000
	case "S":
		c.Months, c.IISRows = 1, 1*RowsPerMonth
	case "M":
		c.Months, c.IISRows = 3, 3*RowsPerMonth
	case "L":
		c.Months, c.IISRows = 6, 6*RowsPerMonth
	default:
		return Config{}, fmt.Errorf("unknown size %q (use T, S, M or L)", name)
	}
	return c, c.Normalize()
}

// ForMonths returns a custom Config for a window of n months (1 to 12) with
// RowsPerMonth IIS rows per month.
func ForMonths(n int) (Config, error) {
	if n < 1 || n > 12 {
		return Config{}, errors.New("months must be 1 to 12")
	}
	c := Config{Preset: "custom", Months: n, IISRows: int64(n) * RowsPerMonth}
	return c, c.Normalize()
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// monthsBack is t minus n calendar months, the day clamped to the length of the
// target month (31 Oct minus 1 month is 30 Sep).
func monthsBack(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	total := y*12 + int(m) - 1 - n
	ny, nm := total/12, time.Month(total%12+1)
	if dm := daysIn(ny, nm); d > dm {
		d = dm
	}
	return time.Date(ny, nm, d, 0, 0, 0, 0, time.UTC)
}

// Normalize fills the defaults and checks the Config.
func (c *Config) Normalize() error {
	if c.End.IsZero() {
		c.End = DefaultEnd
	}
	c.End = c.End.UTC()
	if !c.End.Equal(c.End.Truncate(24 * time.Hour)) {
		return errors.New("end must be at UTC midnight")
	}
	if c.Seed == 0 {
		c.Seed = DefaultSeed
	}
	if c.Endpoints == 0 {
		c.Endpoints = DefaultEndpoints
	}
	if c.ChunkDays == 0 {
		c.ChunkDays = DefaultChunkDays
	}
	if c.Endpoints < 1 || c.Endpoints > 900 {
		return errors.New("endpoints must be 1 to 900")
	}
	if c.ChunkDays < 1 || c.ChunkDays > 90 {
		return errors.New("chunk-days must be 1 to 90")
	}
	switch {
	case c.Months > 0:
		if c.Months > 12 {
			return errors.New("months must be 1 to 12")
		}
		c.start = monthsBack(c.End, c.Months)
		c.days = int(c.End.Sub(c.start) / (24 * time.Hour))
	case c.SpanDays > 0:
		c.days = c.SpanDays
		c.start = c.End.AddDate(0, 0, -c.days)
	default:
		return errors.New("set months or span days")
	}
	if c.days < 1 || c.days > 366 {
		return errors.New("the window must be 1 to 366 days")
	}
	if c.IISRows <= 0 {
		return errors.New("iis rows must be positive")
	}
	if c.SQLRows == 0 {
		c.SQLRows = c.IISRows / 100
	}
	if c.SQLRows < 0 {
		return errors.New("sql rows must not be negative")
	}
	if c.CaseTitle == "" {
		c.CaseTitle = "Performance dataset " + c.start.Format("2006-01-02") + " to " + c.End.AddDate(0, 0, -1).Format("2006-01-02")
	}
	return nil
}
