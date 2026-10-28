package casestore

import (
	"context"
	"strings"
	"time"
	"unicode"
)

// Bounds of the health runs a case keeps.
const (
	MaxHealthRuns   = 20
	MaxLaneDays     = 60 // lane days (runs with a Key) are kept apart from the plain runs, so neither pushes the other out
	MaxHealthKey    = 512
	MaxHealthReport = 256 << 10 // bytes of one stored report
)

// HealthRun is one saved run of the SQL Server or MongoDB health checks. Report is the finished report as JSON (see internal/dbhealth); it holds
// check results only, never a host, a user name or a password. Kind is "sql" or "mongo".
type HealthRun struct {
	ID     int64
	At     time.Time
	Kind   string
	Label  string
	Status string
	Report []byte
	// Key is empty for a plain health run. A lane day (a SQL or MongoDB read of one incident window) carries a key that names its source and
	// window: a newer run with the same key replaces the older one, and lane days have their own cap (MaxLaneDays).
	Key string
}

// HealthStore is implemented by a Case that keeps health runs. It is a separate interface, like Tagger, so it breaks no Case.
type HealthStore interface {
	// AddHealthRun stores a run and returns it with ID and At set. Only the newest MaxHealthRuns are kept. ErrBadTag for a Kind that is not
	// "sql" or "mongo", or a report over MaxHealthReport. Refused with ErrCaseClosed once the case is closed.
	AddHealthRun(ctx context.Context, r HealthRun) (HealthRun, error)
	// HealthRuns lists the saved runs, newest first. A case that has none (or an older case without the table) returns an empty list.
	HealthRuns(ctx context.Context) ([]HealthRun, error)
}

// CleanHealthRun checks and trims a run before it is stored.
func CleanHealthRun(r HealthRun) (HealthRun, error) {
	if r.Kind != "sql" && r.Kind != "mongo" {
		return HealthRun{}, ErrBadTag
	}
	if len(r.Report) == 0 || len(r.Report) > MaxHealthReport {
		return HealthRun{}, ErrBadTag
	}
	label, ok := cleanTagText(r.Label, MaxTagTextChars)
	if !ok {
		return HealthRun{}, ErrBadTag
	}
	switch r.Status {
	case "ok", "note", "warn", "bad", "unavailable":
	default:
		return HealthRun{}, ErrBadTag
	}
	if len(r.Key) > MaxHealthKey || strings.ContainsFunc(r.Key, unicode.IsControl) {
		return HealthRun{}, ErrBadTag
	}
	r.Label = label
	return r, nil
}

// TrimHealthRuns applies the retention rules to a list that is newest first: a run whose Key matches a newer run is dropped, then the newest
// MaxHealthRuns plain runs and the newest MaxLaneDays keyed runs are kept.
func TrimHealthRuns(runs []HealthRun) []HealthRun {
	out := make([]HealthRun, 0, len(runs))
	seen := map[string]bool{}
	plain, keyed := 0, 0
	for _, r := range runs {
		if r.Key == "" {
			if plain++; plain > MaxHealthRuns {
				continue
			}
		} else {
			if seen[r.Key] {
				continue
			}
			seen[r.Key] = true
			if keyed++; keyed > MaxLaneDays {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}
