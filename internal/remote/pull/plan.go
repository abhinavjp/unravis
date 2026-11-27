// Package pull copies files from a server to this PC through a transport.Transport: plan, stream, verify, scrub, hand over.
// It never writes to the server. Raw server text is never written to disk: every line is scrubbed before it reaches the staging folder,
// and a line that cannot be scrubbed is dropped and counted (fail closed).
package pull

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/remote/transport"
)

// Limits are the safety limits of one pull job. Zero fields fall back to DefaultLimits.
type Limits struct {
	PerFile     int64         // largest single file
	PerJob      int64         // largest total
	Parallel    int           // files at once
	BytesPerSec int64         // ceiling shared by the whole job
	MaxDuration time.Duration // the whole job
	MaxFiles    int
	MaxLine     int // longest line kept; a longer one is dropped
}

// DefaultLimits are the owner-approved defaults (design 4.6).
func DefaultLimits() Limits {
	return Limits{PerFile: 512 << 20, PerJob: 4 << 30, Parallel: 3, BytesPerSec: 25 << 20, MaxDuration: 20 * time.Minute, MaxFiles: 2000, MaxLine: 1 << 20}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.PerFile <= 0 {
		l.PerFile = d.PerFile
	}
	if l.PerJob <= 0 {
		l.PerJob = d.PerJob
	}
	if l.Parallel <= 0 {
		l.Parallel = d.Parallel
	}
	if l.BytesPerSec <= 0 {
		l.BytesPerSec = d.BytesPerSec
	}
	if l.MaxDuration <= 0 {
		l.MaxDuration = d.MaxDuration
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxLine <= 0 {
		l.MaxLine = d.MaxLine
	}
	return l
}

// Request names what to pull.
type Request struct {
	Kind    string    // blackbox, iis, redis, sqlserver, applog: decides how a line is scrubbed
	Dir     string    // one allowed folder on the server
	Pattern string    // file name pattern such as bb-*.ndjson; join several with |; empty means every file
	Since   time.Time // only files written at or after this; zero means no limit
}

// PlanFile is one file the job will pull.
type PlanFile struct {
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

// Skipped is a file left out of the plan, with a plain reason.
type Skipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Plan is what a job would do. A Problem means it will not run until the request is narrowed.
type Plan struct {
	Kind    string     `json:"kind"`
	Dir     string     `json:"dir"`
	Files   []PlanFile `json:"files"`
	Skipped []Skipped  `json:"skipped"`
	Bytes   int64      `json:"bytes"`
	Problem string     `json:"problem,omitempty"`
}

// ErrPlanTooLarge carries the plain sentence saying what to narrow.
type ErrPlanTooLarge struct{ Problem string }

func (e ErrPlanTooLarge) Error() string { return e.Problem }

// BuildPlan lists the folder and decides what fits the limits. It reads nothing but the listing.
func BuildPlan(ctx context.Context, t transport.Transport, req Request, lim Limits) (Plan, error) {
	lim = lim.withDefaults()
	pats := strings.Split(strings.ToLower(strings.TrimSpace(req.Pattern)), "|")
	for i, p := range pats {
		if pats[i] = strings.TrimSpace(p); pats[i] == "" {
			pats[i] = "*"
		}
		if _, err := path.Match(pats[i], "x"); err != nil {
			return Plan{}, errors.New("That file name pattern is not valid.") //nolint:staticcheck // shown to the person as a sentence
		}
	}
	infos, err := t.List(ctx, req.Dir)
	if err != nil {
		return Plan{}, err
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	p := Plan{Kind: req.Kind, Dir: req.Dir}
	for _, fi := range infos {
		switch {
		case !match(pats, fi.Name):
			continue
		case !req.Since.IsZero() && fi.MTime.Before(req.Since):
			p.Skipped = append(p.Skipped, Skipped{fi.Name, "older than the time range"})
		case fi.Size > lim.PerFile:
			p.Skipped = append(p.Skipped, Skipped{fi.Name, fmt.Sprintf("bigger than the %s limit for one file; pull a recent slice instead", human(lim.PerFile))})
		default:
			p.Files = append(p.Files, PlanFile{Name: fi.Name, Size: fi.Size, MTime: fi.MTime})
			p.Bytes += fi.Size
		}
	}
	switch {
	case len(p.Files) > lim.MaxFiles:
		p.Problem = fmt.Sprintf("That is %d files, more than the %d allowed in one job. Narrow the time range or the pattern.", len(p.Files), lim.MaxFiles)
	case p.Bytes > lim.PerJob:
		p.Problem = fmt.Sprintf("That is %s, more than the %s allowed in one job. Narrow the time range or the pattern.", human(p.Bytes), human(lim.PerJob))
	}
	return p, nil
}

// match reports whether name fits any of the patterns (already lower case).
func match(lowerPats []string, name string) bool {
	for _, p := range lowerPats {
		if ok, _ := path.Match(p, strings.ToLower(name)); ok {
			return true
		}
	}
	return false
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
