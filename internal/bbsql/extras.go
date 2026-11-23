// extras.go is the opt-in "extra samplers" seam (P1b): read-only samplers beyond
// the seven Standard ones, switched on by the owner's profile
// (sql.profile = everything | deep, or an explicit sql.extras list).
//
// Everything the Standard path guarantees holds here too: one connection, a
// fixed compile-time statement per sampler that passes lintStatement at init,
// a C6 row cap written into the SQL as TOP n+1, a fixed output struct with a
// column contract, numbers and enums only. What differs is the failure model.
// An extra is optional evidence, so one extra that fails must never degrade the
// Standard samples: it keeps its own back-off, its own continuity epoch and its
// own off-switch, and it never touches State (no OnFailure, OnSlow, OnSuccess).
// Only an instance-level failure (login, network, TLS, database, the C8
// ceiling) goes through Runner.fail, exactly as it would for a Standard sample.
//
// The extras registry is separate from the Standard one on purpose: Samplers()
// is the Standard set and stays exactly that, so a profile of "standard" (the
// default) sends nothing new.
package bbsql

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Profiles name which extras run. "standard" is the default and sends none.
const (
	ProfileStandard   = "standard"
	ProfileEverything = "everything"
	ProfileDeep       = "deep"
)

// ExtraTier says which profile first includes an extra.
type ExtraTier int

const (
	// TierEverything is the "collect everything possible" set: light, read-only
	// DMV samples whose cost was reviewed.
	TierEverything ExtraTier = iota + 1
	// TierDeep is reserved for heavier samples that only the deep profile adds.
	TierDeep
)

// Cadences an extra may use. The Standard registry allows only 15 s and 60 s;
// the slower ones exist because most extras are slow-moving facts.
const (
	ExtraEvery30s = 30 * time.Second
	ExtraEvery5m  = 5 * time.Minute
	ExtraEveryHr  = time.Hour
	ExtraEveryDay = 24 * time.Hour
)

// extraCadenceOK is the allowed cadence set for an extra: one place to widen,
// and a registration outside it stops the process at startup.
var extraCadenceOK = map[time.Duration]bool{
	BaseInterval: true, ExtraEvery30s: true, DemotedInterval: true,
	ExtraEvery5m: true, ExtraEveryHr: true, ExtraEveryDay: true,
}

// extraMaxFailures is how many consecutive failures switch ONE extra off for the
// rest of the process. It never touches State, so it can never auto-off the
// Standard samples.
const extraMaxFailures = 10

// extraBackoffBase and extraBackoffMax bound the retry delay after a failure of
// one extra: base * 2^fails, capped.
const (
	extraBackoffBase = 15 * time.Second
	extraBackoffMax  = 30 * time.Minute
)

type extraEntry struct {
	kind string
	tier ExtraTier
	new  func() Sampler
}

var (
	extraMu       sync.Mutex
	extraRegistry []extraEntry

	// extraStatements is the extras' own statement table. The statements are ALSO
	// in the private `statements` map (that is the send policy), but never in the
	// exported Statements view the Standard tests pin.
	extraStatements = map[string]string{}
	extraCaps       = map[string]int{}
	// extraSources records the FROM sources the extras approved, so a test can
	// pin the set and a new source cannot slip in without a test edit.
	extraSources = map[string]bool{}
	// extraColumnKeys is the extras' column -> JSON key table, consulted after
	// columnKeys. A column may not be mapped two different ways.
	extraColumnKeys = map[string]string{}
)

// RegisterExtraFactory registers an extra sampler. It is called from an init()
// and panics on a programming error (a cadence outside extraCadenceOK, a nil
// factory, a kind registered twice), because a bad extra must stop the build's
// tests, not be silently dropped in production.
func RegisterExtraFactory(tier ExtraTier, new func() Sampler) {
	if new == nil {
		panic("bbsql: nil extra factory")
	}
	probe := new()
	if probe == nil {
		panic("bbsql: extra factory returned nil")
	}
	if !extraCadenceOK[probe.Interval()] {
		panic("bbsql: extra " + probe.Kind() + " has a cadence outside the allowed set")
	}
	if tier != TierEverything && tier != TierDeep {
		panic("bbsql: extra " + probe.Kind() + " has no tier")
	}
	extraMu.Lock()
	defer extraMu.Unlock()
	for _, e := range extraRegistry {
		if e.kind == probe.Kind() {
			panic("bbsql: extra kind registered twice: " + probe.Kind())
		}
	}
	extraRegistry = append(extraRegistry, extraEntry{kind: probe.Kind(), tier: tier, new: new})
}

// topRowRe finds the TOP n an extra statement carries.
var topRowRe = regexp.MustCompile(`(?i)\bTOP ([0-9]+)\b`)

// registerExtraStatement adds one extra statement to the send policy. It lints
// the text, checks the C6 contract (TOP == rowCap+1, or a single-row statement
// with no TOP and no GROUP BY), approves the single FROM source it reads, and
// panics on any violation or duplicate. Called from an init().
func registerExtraStatement(id, text string, rowCap int, source string) {
	if !strings.HasPrefix(id, "bb:") {
		panic("bbsql: extra statement id must start with bb: " + id)
	}
	if _, dup := statements[id]; dup {
		panic("bbsql: duplicate statement id " + id)
	}
	for _, other := range statements {
		if other == text {
			panic("bbsql: duplicate statement text for " + id)
		}
	}
	if rowCap < 1 {
		panic("bbsql: extra statement " + id + " has no row cap")
	}
	if m := topRowRe.FindStringSubmatch(text); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n != rowCap+1 {
			panic(fmt.Sprintf("bbsql: extra statement %s: TOP must be the row cap plus one (%d)", id, rowCap+1))
		}
	} else if rowCap != CapSingleRow || strings.Contains(text, "GROUP BY") {
		panic("bbsql: extra statement " + id + " has no TOP n but is not a single-row statement")
	}
	if source == "" {
		panic("bbsql: extra statement " + id + " names no source")
	}
	lintApprovedFrom[source] = true
	extraSources[source] = true
	if err := lintStatement(id, text); err != nil {
		panic("bbsql: extra statement " + id + " fails the C2 grammar: " + err.Error())
	}
	statements[id] = text
	extraStatements[id] = text
	extraCaps[text] = rowCap
}

// registerExtraColumns maps result columns to JSON keys for the extras. The
// same column may be mapped again only to the same key, and never differently
// from the Standard table.
func registerExtraColumns(m map[string]string) {
	for col, key := range m {
		if k, ok := columnKeys[col]; ok && k != key {
			panic("bbsql: extra column " + col + " conflicts with the standard mapping")
		}
		if k, ok := extraColumnKeys[col]; ok && k != key {
			panic("bbsql: extra column " + col + " is mapped two ways")
		}
		extraColumnKeys[col] = key
	}
}

// registerExtraRowSpec pins the column contract of an extra's output struct.
func registerExtraRowSpec(typ reflect.Type, spec rowSpec) {
	if _, dup := rowSpecs[typ]; dup {
		panic("bbsql: duplicate row spec for " + typ.String())
	}
	rowSpecs[typ] = spec
}

// ExtraKinds lists every registered extra kind, sorted.
func ExtraKinds() []string {
	extraMu.Lock()
	defer extraMu.Unlock()
	out := make([]string, 0, len(extraRegistry))
	for _, e := range extraRegistry {
		out = append(out, e.kind)
	}
	sort.Strings(out)
	return out
}

// ExtrasForProfile maps a profile name to the extra kinds it turns on. An
// unknown profile turns on nothing (fail closed to Standard).
func ExtrasForProfile(profile string) []string {
	var upTo ExtraTier
	switch profile {
	case ProfileEverything:
		upTo = TierEverything
	case ProfileDeep:
		upTo = TierDeep
	default:
		return nil
	}
	extraMu.Lock()
	defer extraMu.Unlock()
	var out []string
	for _, e := range extraRegistry {
		if e.tier <= upTo {
			out = append(out, e.kind)
		}
	}
	sort.Strings(out)
	return out
}

// Extras returns FRESH sampler instances for the requested kinds, sorted by
// kind. Unknown kinds and kinds that collide with a Standard kind are ignored,
// so a config typo or a stale list can never change what is sent.
func Extras(kinds []string) []Sampler {
	if len(kinds) == 0 {
		return nil
	}
	want := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	registryMu.Lock()
	for _, e := range registry {
		delete(want, e.kind)
	}
	registryMu.Unlock()
	extraMu.Lock()
	entries := make([]extraEntry, 0, len(want))
	for _, e := range extraRegistry {
		if want[e.kind] {
			entries = append(entries, e)
		}
	}
	extraMu.Unlock()
	out := make([]Sampler, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.new())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}

// extraRun is the health of ONE extra. It is deliberately not part of State.
type extraRun struct {
	// epoch advances on every miss or failure of this extra; the sampler sees
	// Runner.gen + epoch, so its delta never spans an unobserved interval.
	epoch uint64
	fails int
	// skipUntil is the back-off after a failure.
	skipUntil time.Time
	// off latches for the rest of the process (permission denied, or too many
	// consecutive failures).
	off bool
}

func (r *Runner) extraState(kind string) *extraRun {
	if r.extraRuns == nil {
		r.extraRuns = make(map[string]*extraRun)
	}
	run, ok := r.extraRuns[kind]
	if !ok {
		run = &extraRun{}
		r.extraRuns[kind] = run
	}
	return run
}

// ExtrasOff reports the extra kinds switched off for this process, sorted. It
// is a diagnostics seam: it carries kinds only, never an error text.
func (r *Runner) ExtrasOff() []string {
	var out []string
	for kind, run := range r.extraRuns {
		if run.off {
			out = append(out, kind)
		}
	}
	sort.Strings(out)
	return out
}

// dueExtras lists the extras due now, least recently run first so a slow tick
// never starves the tail of the list.
func (r *Runner) dueExtras(now time.Time) []Sampler {
	var due []Sampler
	for _, s := range r.extras {
		run := r.extraState(s.Kind())
		if run.off {
			continue
		}
		if run.skipUntil.After(now) && run.skipUntil.Sub(now) <= extraBackoffMax {
			continue
		}
		if !r.due(s, now) {
			continue
		}
		due = append(due, s)
	}
	sort.SliceStable(due, func(i, j int) bool {
		a, aok := r.lastRun[due[i].Kind()]
		b, bok := r.lastRun[due[j].Kind()]
		if aok != bok {
			return !aok // never run first
		}
		if !a.Equal(b) {
			return a.Before(b)
		}
		return due[i].Kind() < due[j].Kind()
	})
	return due
}

// tickExtras runs the extras that are due, after the Standard samples and
// inside the same tick budget. It returns an error only for an instance-level
// failure (already routed through fail) or a filesystem write failure.
func (r *Runner) tickExtras(ctx, parent context.Context, now time.Time) error {
	if !r.extrasReady {
		r.extras = Extras(r.EnabledExtras)
		r.extrasReady = true
	}
	if len(r.extras) == 0 {
		return nil
	}
	due := r.dueExtras(now)
	for i, s := range due {
		kind := s.Kind()
		run := r.extraState(kind)
		limit := itemLimit(s.Interval())
		timeout := remaining(ctx, limit)
		if timeout <= 0 {
			// The tick budget is spent: these extras miss a scheduled
			// observation. No strike, they stay due for the next tick.
			for _, rest := range due[i:] {
				r.extraState(rest.Kind()).epoch++
			}
			break
		}
		clamped := timeout < limit
		if g, ok := s.(Gate); ok {
			open, err := r.gateIsOpen(ctx, g, timeout)
			if err != nil {
				if herr := r.extraFailed(parent, s, run, now, err, clamped); herr != nil {
					return herr
				}
				continue
			}
			if !open {
				r.lastRun[kind] = now
				run.epoch++
				continue
			}
		}
		text, ok := Statement(s.ID())
		if !ok {
			run.off = true
			r.logf("extra %s: %s", kind, "not-allowed")
			continue
		}
		rows, err := Query(ctx, r.q, text, timeout)
		if err != nil {
			if herr := r.extraFailed(parent, s, run, now, err, clamped); herr != nil {
				return herr
			}
			continue
		}
		if obs, ok := s.(ObservingSampler); ok {
			obs.Observe(Observation{Time: r.now(), Epoch: r.gen + run.epoch})
		}
		payload, truncated, err := s.Decode(rows)
		if err != nil {
			if herr := r.extraFailed(parent, s, run, now, err, false); herr != nil {
				return herr
			}
			continue
		}
		if err := r.appendSample(SamplePath(r.Dir, now), SampleDoc{
			V: 1, T: now.UTC(), Kind: kind, Truncated: truncated, Rows: payload,
		}); err != nil {
			r.fsWriteFailures++
			r.logf("tick: %s", CodeWriteFailed)
			return err
		}
		run.fails = 0
		r.lastRun[kind] = now
	}
	return nil
}

// extraFailed routes one extra's failure. Instance-level failures go through
// the Standard fail path; everything else stays with the extra.
func (r *Runner) extraFailed(parent context.Context, s Sampler, run *extraRun, now time.Time, err error, clamped bool) error {
	e := Classify(err)
	run.epoch++ // the next observation of this extra must rebaseline
	if parent.Err() != nil {
		return e // a service stop is nobody's fault
	}
	if IsCeilingFailure(err) {
		return r.fail(err)
	}
	switch e.Code {
	case CodeLoginFailed, CodeTooManyRights, CodeNetwork, CodeTLS, CodeDatabase:
		return r.fail(err)
	case CodePermission:
		run.off = true
		r.logf("extra %s: %s", s.Kind(), e.Code)
		return nil
	case CodeTimeout:
		if clamped {
			return nil // ran out of tick budget, not the extra's fault
		}
	}
	run.fails++
	r.lastRun[s.Kind()] = now
	if run.fails >= extraMaxFailures {
		run.off = true
		r.logf("extra %s: off after %d failures", s.Kind(), run.fails)
		return nil
	}
	back := extraBackoffBase << uint(min(run.fails, 8))
	run.skipUntil = now.Add(min(back, extraBackoffMax))
	r.logf("extra %s: %s", s.Kind(), e.Code)
	return nil
}
