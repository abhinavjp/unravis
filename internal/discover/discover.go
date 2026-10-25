// Package discover finds the log sources on the Windows host Unravis runs on.
//
// Detectors are pure Go over the interfaces in env.go, so every one is tested on
// any OS with the in-memory implementations in the fake package. Only
// winenv_windows.go calls the system. A scan reads metadata and nothing else;
// the paths it finds stay inside this package's Finding values and never reach
// the JSON a client sees.
package discover

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Kind is the shape of a finding.
type Kind string

const (
	KindLogFile      Kind = "log-file"
	KindLogSet       Kind = "log-set" // rotated set in one folder
	KindEventChannel Kind = "event-channel"
	KindLiveSeries   Kind = "live-series"
	KindExportNeeded Kind = "export-needed"
	KindRemoteOnly   Kind = "remote-only"
)

// Reason says why a source is missing from the results.
type Reason string

const (
	ReasonNeedsElevation Reason = "needs-elevation"
	ReasonNotFound       Reason = "not-found"
	ReasonNotSupportedOS Reason = "not-supported-os"
	ReasonUnreadable     Reason = "unreadable"
	ReasonTooLarge       Reason = "too-large"
	ReasonUnsafePath     Reason = "unsafe-path"
	ReasonConfigUnparsed Reason = "config-unparsed"
)

// Cost is how much a finding would read.
type Cost string

const (
	CostSmall  Cost = "small"  // under 64 MiB
	CostMedium Cost = "medium" // under 1 GiB
	CostLarge  Cost = "large"  // 1 GiB or more, a window is required
)

// CostForSize maps a size in bytes to a Cost.
func CostForSize(size int64) Cost {
	switch {
	case size < 64<<20:
		return CostSmall
	case size < 1<<30:
		return CostMedium
	default:
		return CostLarge
	}
}

// Finding is one importable source as the client sees it. It has no path field:
// the canonical paths sit in the unexported paths slice and leave the package
// only through Paths, which the server calls for its in-memory scan store.
type Finding struct {
	ID         string    `json:"id"`
	Detector   string    `json:"detector"`
	Kind       Kind      `json:"kind"`
	Importer   string    `json:"importer,omitempty"`
	HostLabel  string    `json:"host"`
	Display    string    `json:"display"`
	Sites      []string  `json:"sites,omitempty"`
	Confidence int       `json:"confidence"`
	Shows      string    `json:"shows"` // wording key, see wording.go
	SizeBytes  int64     `json:"sizeBytes"`
	Files      int       `json:"files"`
	Oldest     time.Time `json:"oldest,omitzero"`
	Newest     time.Time `json:"newest,omitzero"`
	NeedsAdmin bool      `json:"needsAdmin"`
	Cost       Cost      `json:"cost"`
	How        string    `json:"how,omitempty"` // wording key, for export-needed and remote-only

	paths  []string
	detail string // what Display shows after the site IDs, kept so a merge can rebuild Display
}

// Paths returns a copy of the canonical paths behind the finding. Only the
// server's scan store should call it.
func (f Finding) Paths() []string { return append([]string(nil), f.paths...) }

// SetPaths stores canonical paths on the finding. Run refuses a finding whose
// paths fail ValidLogPath.
func (f *Finding) SetPaths(p ...string) { f.paths = append([]string(nil), p...) }

// SetSites records the site IDs a finding covers and the text that follows them
// in Display, for example SetSites([]string{"W3SVC2"}, "u_ex010911.log and 13 more").
// A merge of two findings for one folder rebuilds Display from the merged IDs.
func (f *Finding) SetSites(sites []string, detail string) {
	f.Sites = append([]string(nil), sites...)
	f.detail = detail
	f.Display = SiteDisplay(f.Sites, detail)
}

// SiteDisplay builds "W3SVC1, W3SVC2 (detail)". With no sites it is detail alone.
func SiteDisplay(sites []string, detail string) string {
	if len(sites) == 0 {
		return detail
	}
	if detail == "" {
		return strings.Join(sites, ", ")
	}
	return strings.Join(sites, ", ") + " (" + detail + ")"
}

// Gap is a source the scan could not report on, and why.
type Gap struct {
	Detector string `json:"detector"`
	Kind     Kind   `json:"kind,omitempty"`
	Display  string `json:"display"`
	Reason   Reason `json:"reason"`
}

// Detector looks for one family of sources. Detect must honour ctx, must not
// touch the system except through env, and must turn every failed probe into a
// Gap with GapFromError; an access-denied error always means needs-elevation.
type Detector interface {
	Name() string
	Detect(ctx context.Context, env Env) ([]Finding, []Gap)
}

// Scan is the outcome of one Run.
type Scan struct {
	Findings []Finding `json:"findings"`
	Gaps     []Gap     `json:"gaps"`
	Started  time.Time `json:"startedAt"`
	Finished time.Time `json:"finishedAt"`
	Elevated bool      `json:"elevated"`
	// Complete is false when the scan was cancelled or hit the global limit, so
	// some detectors did not finish.
	Complete bool `json:"complete"`
}

// Limits are the time bounds of a scan.
type Limits struct {
	PerDetector time.Duration
	Global      time.Duration
}

// DefaultLimits are 10 s per detector and 60 s for the whole scan (spec 1.4).
var DefaultLimits = Limits{PerDetector: 10 * time.Second, Global: 60 * time.Second}

// Run scans with DefaultLimits.
func Run(ctx context.Context, env Env, detectors []Detector) Scan {
	return RunWith(ctx, env, detectors, DefaultLimits)
}

// RunWith runs detectors one after another. A detector that panics, overruns
// its time or is cut off by the global limit adds an unreadable gap and the scan
// carries on. A detector that ignores its context cannot block the scan: it is
// abandoned, and its late result is dropped.
func RunWith(ctx context.Context, env Env, detectors []Detector, lim Limits) Scan {
	if lim.PerDetector <= 0 {
		lim.PerDetector = DefaultLimits.PerDetector
	}
	if lim.Global <= 0 {
		lim.Global = DefaultLimits.Global
	}
	s := Scan{Started: time.Now(), Complete: true}
	if env.Machine != nil {
		s.Elevated = env.Machine.Elevated()
	}
	gctx, cancel := context.WithTimeout(ctx, lim.Global)
	defer cancel()

	var found []Finding
	for _, d := range detectors {
		name := safeName(d)
		if gctx.Err() != nil {
			s.Complete = false
			s.Gaps = append(s.Gaps, Gap{Detector: name, Display: name, Reason: ReasonUnreadable})
			continue
		}
		fs, gs, ok := runOne(gctx, d, name, env, lim.PerDetector)
		if !ok {
			s.Complete = false
		}
		found = append(found, fs...)
		s.Gaps = append(s.Gaps, gs...)
	}
	found, extra := validate(found, env)
	s.Gaps = append(s.Gaps, extra...)
	s.Findings = finalise(found)
	s.Finished = time.Now()
	return s
}

// safeName reads a detector name without letting a panic escape.
func safeName(d Detector) (name string) {
	defer func() {
		if recover() != nil {
			name = "unknown"
		}
	}()
	if name = d.Name(); name == "" {
		name = "unknown"
	}
	return name
}

type detectResult struct {
	findings []Finding
	gaps     []Gap
	panicked bool
}

// runOne runs one detector under its own timeout. ok is false when the detector
// timed out, panicked or was cancelled.
func runOne(ctx context.Context, d Detector, name string, env Env, per time.Duration) (fs []Finding, gs []Gap, ok bool) {
	dctx, cancel := context.WithTimeout(ctx, per)
	defer cancel()
	ch := make(chan detectResult, 1)
	go func() {
		defer func() {
			// The panic value is dropped: it can carry a path or a config value.
			if recover() != nil {
				ch <- detectResult{gaps: []Gap{{Detector: name, Display: name, Reason: ReasonUnreadable}}, panicked: true}
			}
		}()
		f, g := d.Detect(dctx, env)
		ch <- detectResult{findings: f, gaps: g}
	}()
	select {
	case r := <-ch:
		for i := range r.findings {
			if r.findings[i].Detector == "" {
				r.findings[i].Detector = name
			}
		}
		for i := range r.gaps {
			if r.gaps[i].Detector == "" {
				r.gaps[i].Detector = name
			}
		}
		return r.findings, r.gaps, !r.panicked
	case <-dctx.Done():
		return nil, []Gap{{Detector: name, Display: name, Reason: ReasonUnreadable}}, false
	}
}

// validate drops findings that break the contract: a path that fails
// ValidLogPath, or an importer name the registry does not know. Each dropped
// finding becomes a gap, so nothing disappears silently.
func validate(in []Finding, env Env) (kept []Finding, gaps []Gap) {
	for _, f := range in {
		if reason, bad := checkFinding(f, env); bad {
			gaps = append(gaps, Gap{Detector: f.Detector, Kind: f.Kind, Display: f.Display, Reason: reason})
			continue
		}
		clean := make([]string, len(f.paths))
		for i, p := range f.paths {
			clean[i], _ = ValidLogPath(p, "")
		}
		f.paths = clean
		kept = append(kept, f)
	}
	return kept, gaps
}

func checkFinding(f Finding, env Env) (Reason, bool) {
	for _, p := range f.paths {
		if _, err := ValidLogPath(p, ""); err != nil {
			return ReasonUnsafePath, true
		}
	}
	if f.Importer != "" && env.Importers != nil {
		if _, ok := env.Importers.ByName(f.Importer); !ok {
			return ReasonUnreadable, true
		}
	}
	return "", false
}

// finalise merges findings that share one set of canonical paths (two sites
// with one log folder), orders them and gives each an ID.
func finalise(in []Finding) []Finding {
	// Best first, so the merge keeps the most confident description.
	sort.SliceStable(in, func(i, j int) bool { return in[i].Confidence > in[j].Confidence })
	index := map[string]int{}
	var out []Finding
	for _, f := range in {
		k := dedupeKey(f)
		if i, ok := index[k]; ok {
			out[i] = merge(out[i], f)
			continue
		}
		index[k] = len(out)
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := Weight(out[i]), Weight(out[j])
		if wi != wj {
			return wi < wj
		}
		if out[i].Detector != out[j].Detector {
			return out[i].Detector < out[j].Detector
		}
		return out[i].Display < out[j].Display
	})
	for i := range out {
		out[i].ID = newID()
	}
	return out
}

// dedupeKey is the sorted lower-cased path set plus importer. A finding with no
// paths (a channel, an export hint) is keyed by what it says.
func dedupeKey(f Finding) string {
	if len(f.paths) == 0 {
		return strings.Join([]string{"nopath", f.Detector, string(f.Kind), f.Importer, f.Display}, "\x00")
	}
	keys := make([]string, len(f.paths))
	for i, p := range f.paths {
		keys[i] = CanonKey(p)
	}
	sort.Strings(keys)
	return "paths\x00" + f.Importer + "\x00" + strings.Join(keys, "\x00")
}

func merge(a, b Finding) Finding {
	a.NeedsAdmin = a.NeedsAdmin || b.NeedsAdmin
	set := map[string]bool{}
	var sites []string
	for _, s := range append(append([]string(nil), a.Sites...), b.Sites...) {
		if !set[s] {
			set[s] = true
			sites = append(sites, s)
		}
	}
	sort.Slice(sites, func(i, j int) bool { return naturalLess(sites[i], sites[j]) })
	if len(sites) > 0 {
		a.Sites = sites
		a.Display = SiteDisplay(sites, a.detail)
	}
	if a.Oldest.IsZero() || (!b.Oldest.IsZero() && b.Oldest.Before(a.Oldest)) {
		a.Oldest = b.Oldest
	}
	if b.Newest.After(a.Newest) {
		a.Newest = b.Newest
	}
	return a
}

// naturalLess orders W3SVC2 before W3SVC10.
func naturalLess(a, b string) bool {
	ia, ib := digitTail(a), digitTail(b)
	if a[:len(a)-len(ia)] == b[:len(b)-len(ib)] && ia != "" && ib != "" {
		if len(ia) != len(ib) {
			return len(ia) < len(ib)
		}
		return ia < ib
	}
	return a < b
}

func digitTail(s string) string {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	return s[i:]
}

// weights mirror the unexported tiers in internal/collect: host counters 10, app
// log 20, IIS 25, HTTPERR 26, events 27, process snapshot and live 30, runtime
// 40, SQL 50, Redis 60, Mongo 70. A finding is ranked by its importer first,
// then by its detector, so a detector that emits several tiers sorts correctly.
var importerWeight = map[string]int{
	"win-counters": 10,
	"log4net-text": 20, "log4net-xml": 20, "serilog-clef": 20, "serilog-text": 20,
	"nlog-text": 20, "mel-console": 20, "mel-json": 20, "ancm-stdout": 20, "appinsights": 20,
	"iis-w3c": 25, "iis-freb": 25,
	"iis-httperr":  26,
	"win-events":   27,
	"win-procsnap": 30, "win-live": 30,
	"dotnet-counters": 40, "schedule-hangfire": 40, "schedule-quartz": 41,
	"sql-errorlog": 50, "sql-pdh": 51, "sql-xe": 52, "schedule-agent": 53,
	"redis-info": 60, "redis-latency": 61, "redis-slowlog": 62, "redis-log": 63,
	"mongod-json": 70, "mongo-serverstatus": 71, "mongo-profile": 72, "mongo-currentop": 73, "mongostat-json": 74,
	"nginx-access": 28, "apache-combined": 28,
}

var detectorWeight = map[string]int{
	"applog": 20, "iis": 25, "httperr": 26, "events": 27, "web": 28, "host": 30,
	"runtime": 40, "sql": 50, "redis": 60, "mongo": 70,
}

// Weight is the sort rank of a finding. Lower comes first. An unknown source
// ranks last.
func Weight(f Finding) int {
	if w, ok := importerWeight[f.Importer]; ok {
		return w
	}
	if w, ok := detectorWeight[f.Detector]; ok {
		return w
	}
	return 100
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newID returns 26 random base32 characters (128 bits).
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("discover: no random source: %v", err))
	}
	return idEncoding.EncodeToString(b[:])
}
