package blackbox

import (
	"strings"
	"sync"
	"time"
)

// runtimeRetryEvery bounds how often a PENDING runtime classification (a
// successful module read with no CLR module yet, or a denied read) is retried
// for a needed identity. The retry is independent of process-set change, so a
// process that loads its runtime later is still detected, but it is never a
// per-tick full module scan: only needed identities are retried, at most once
// per interval.
const runtimeRetryEvery = 15 * time.Second

// filetimeTicksPerSecond is the FILETIME resolution: 100-nanosecond ticks.
const filetimeTicksPerSecond int64 = 10_000_000

// ProcInfo is one process in the inventory. Identity is (PID, StartTicks): a
// PID alone is not identity because Windows reuses PIDs after a recycle, and
// whole seconds are not identity because two lifetimes can share a second.
//
// StartTicks is the LOSSLESS creation identity (raw FILETIME, 100-ns ticks
// since 1601). Start is the creation time in unix SECONDS, kept for display,
// elapsed-time math and the existing wire/consumer shape; it is NOT the
// identity. Cache keys and process-set comparison use StartTicks (falling back
// to Start only when ticks are absent, e.g. a fake that sets seconds alone).
// Both are 0 when the creation time could not be read.
type ProcInfo struct {
	PID        uint32
	ParentPID  uint32
	Start      int64    // unix creation SECONDS (display/consumers); 0 when unreadable
	StartTicks int64    // raw FILETIME creation ticks: the lossless identity; 0 when unreadable
	Name       string   // image base name only; no command line, no environment
	Modules    []string // module base names, filled only on a fresh module read
	Runtime    uint8    // 0 unknown, 1 .NET Framework (clr.dll), 2 .NET 10 (coreclr/hostfxr)
}

// procKey is process identity: PID plus lossless creation ticks, never PID
// alone and never whole seconds. start holds identityTicks(ProcInfo).
type procKey struct {
	pid   uint32
	start int64
}

// identityTicks is the lossless creation identity of p: the raw FILETIME ticks
// when present, otherwise the unix seconds scaled to ticks (a seconds-only fake
// keeps second-granularity identity). 0 means the creation time is unreadable,
// so identity would degenerate to PID alone and the key is never cached.
func identityTicks(p ProcInfo) int64 {
	if p.StartTicks != 0 {
		return p.StartTicks
	}
	return p.Start * filetimeTicksPerSecond
}

// keyOf builds the identity key of p.
func keyOf(p ProcInfo) procKey { return procKey{pid: p.PID, start: identityTicks(p)} }

// keysOf builds the identity keys of procs in order.
func keysOf(procs []ProcInfo) []procKey {
	keys := make([]procKey, len(procs))
	for i, p := range procs {
		keys[i] = keyOf(p)
	}
	return keys
}

// runtimeState is one cached runtime classification. known is true only when a
// successful module read CONFIRMED a runtime (1 or 2). A successful read with
// no CLR module yet, or a denied read, leaves known false (pending): runtime 0
// is "not resolved yet", never a permanent "no runtime". next is the earliest
// time a pending entry may be re-read.
type runtimeState struct {
	runtime uint8
	known   bool
	next    time.Time
}

// classifyRuntime detects the runtime from loaded module base names only —
// never from the process name or config. clr.dll => 1 (.NET Framework);
// coreclr.dll or hostfxr.dll => 2 (.NET 10); both => 2; neither => 0.
func classifyRuntime(modules []string) uint8 {
	var framework, core bool
	for _, m := range modules {
		switch strings.ToLower(moduleBase(m)) {
		case "clr.dll":
			framework = true
		case "coreclr.dll", "hostfxr.dll":
			core = true
		}
	}
	switch {
	case core:
		return 2
	case framework:
		return 1
	default:
		return 0
	}
}

// moduleBase reduces a module path to its base name, handling both Windows
// back-slashes and forward slashes so it is correct on every platform.
func moduleBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// processSetChanged reports whether the set of (PID, start) keys differs.
// Order-independent; a recycled PID (same PID, new start) is a change.
func processSetChanged(prev, cur []procKey) bool {
	if len(prev) != len(cur) {
		return true
	}
	counts := make(map[procKey]int, len(prev))
	for _, k := range prev {
		counts[k]++
	}
	for _, k := range cur {
		counts[k]--
		if counts[k] < 0 {
			return true
		}
	}
	return false
}

// dotnetChildren returns the out-of-process .NET 10 children (runtime 2) of
// parentPID — e.g. a .NET 10 app hosted out-of-process under a w3wp parent.
func dotnetChildren(procs []ProcInfo, parentPID uint32) []ProcInfo {
	var out []ProcInfo
	for _, p := range procs {
		if p.ParentPID == parentPID && p.Runtime == 2 {
			out = append(out, p)
		}
	}
	return out
}

// Resolver builds the process inventory and detects runtimes. A CONFIRMED
// runtime is cached for the life of its identity; a PENDING one (no CLR module
// yet, or a denied read) is retried on a bounded schedule for needed identities,
// independent of process-set change. Modules are never re-read for every process
// every tick: the per-tick target path classifies only the selected identities
// and their children, and keys with an unreadable creation time are never cached.
// The snapshot and module loader are injected so tests need no Windows.
type Resolver struct {
	// mu guards every field below. A group worker from an earlier tick can still
	// be resolving targets while the tick goroutine asks for the process-set
	// verdict, so the resolver must be safe for concurrent callers.
	mu sync.Mutex

	snapshotFn    func() ([]ProcInfo, error)
	loadModulesFn func(pid uint32) ([]string, error)
	instancesFn   func() ([]string, error)  // W3SVC_W3WP counter instances (BBX-1-11)
	servicesFn    func() ([]svcProc, error) // running services with PIDs (BBX-1-11)
	nowFn         func() time.Time          // clock seam for the bounded pending retry

	last     []procKey                // process set at the last inventory
	haveLast bool                     // whether last holds a baseline yet
	cached   map[procKey]runtimeState // confirmed/pending runtimes by identity

	mapLastInst []procKey // process set at the last W3SVC_W3WP enumeration attempt
	mapLastSvc  []procKey // process set at the last service enumeration attempt
	mapInst     []string  // cached W3SVC_W3WP instances (last good list)
	mapSvc      []svcProc // cached running services (last good list)

	changedLast []procKey // process set at the last ProcessSetChanged call

	gen         uint64           // monotonic inventory generation (W3-03)
	lastTargets []ResolvedTarget // resolved targets at the last target comparison
}

// NewResolver wires the platform process snapshot and module loader.
func NewResolver() *Resolver {
	return &Resolver{
		snapshotFn:    snapshotProcesses,
		loadModulesFn: loadModules,
		instancesFn:   enumW3WPInstances,
		servicesFn:    enumServiceProcs,
		nowFn:         time.Now,
		cached:        make(map[procKey]runtimeState),
	}
}

// Generation is the current monotonic inventory generation. It advances when
// the process set changes or the resolved target set changes (a new identity, a
// departed one, or a runtime that became known). It never decreases. Groups
// compare it against their last successfully reconciled generation to decide
// whether to re-expand; a skipped or failed group keeps its older ack and stays
// dirty until it reconciles (W3-03).
func (r *Resolver) Generation() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// Inventory snapshots the processes and fills Runtime for every process (the
// full-inventory API). Confirmed runtimes are reused; pending ones are retried
// when due; an unreadable creation time is never cached and re-read every call.
// The per-tick target path (ResolveTargets / the host gate) instead classifies
// only the selected identities, bounding module reads (W3-05).
func (r *Resolver) Inventory() ([]ProcInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	procs, keys, setChanged, err := r.snapshotLocked()
	if err != nil {
		return nil, err
	}
	r.classifyLocked(procs, keys, nil, r.nowFn())
	if setChanged {
		r.gen++
	}
	return procs, nil
}

// snapshotLocked takes ONE process snapshot, computes identity keys, reports
// whether the set changed since the last inventory, advances the baseline and
// prunes retired identities from the cache. Callers must hold mu. On a snapshot
// error the baseline is kept so the next good snapshot still detects the change.
func (r *Resolver) snapshotLocked() ([]ProcInfo, []procKey, bool, error) {
	procs, err := r.snapshotFn()
	if err != nil {
		return nil, nil, false, err
	}
	keys := keysOf(procs)
	setChanged := !r.haveLast || processSetChanged(r.last, keys)
	r.last, r.haveLast = keys, true
	r.pruneCache(keys)
	return procs, keys, setChanged, nil
}

// pruneCache drops cached identities that are no longer present, so a retired
// identity has a bounded cache lifetime (one inventory) even if the process set
// never changes again.
func (r *Resolver) pruneCache(keys []procKey) {
	if len(r.cached) == 0 {
		return
	}
	live := make(map[procKey]struct{}, len(keys))
	for _, k := range keys {
		live[k] = struct{}{}
	}
	for k := range r.cached {
		if _, ok := live[k]; !ok {
			delete(r.cached, k)
		}
	}
}

// classifyLocked fills Runtime for procs. selected nil means "every identity"
// (the full Inventory); otherwise only PIDs in selected are classified, bounding
// fresh module reads to what the selected targets need (W3-05). A confirmed
// runtime is reused without a read; a pending one is re-read only when its retry
// is due; an unreadable creation time is re-read every call and never cached.
func (r *Resolver) classifyLocked(procs []ProcInfo, keys []procKey, selected map[uint32]bool, now time.Time) {
	for i := range procs {
		k := keys[i]
		needed := selected == nil || selected[procs[i].PID]
		if k.start == 0 {
			// Unreadable creation time: identity would be PID alone. Never
			// cached; re-read whenever this identity is needed.
			if needed {
				r.detectRuntime(&procs[i])
			}
			continue
		}
		st, ok := r.cached[k]
		if ok && st.known {
			procs[i].Runtime = st.runtime // confirmed: stable for the identity's life
			continue
		}
		if !needed {
			if ok {
				procs[i].Runtime = st.runtime // last pending value; no read
			}
			continue
		}
		if ok && now.Before(st.next) {
			procs[i].Runtime = st.runtime // pending, retry not due yet
			continue
		}
		mods, err := r.loadModulesFn(procs[i].PID)
		if err != nil {
			// Denied/failed: unknown, never a guess; pending, retry when due.
			r.cached[k] = runtimeState{next: now.Add(runtimeRetryEvery)}
			procs[i].Runtime = 0
			continue
		}
		procs[i].Modules = mods
		rt := classifyRuntime(mods)
		r.cached[k] = runtimeState{runtime: rt, known: rt != 0, next: now.Add(runtimeRetryEvery)}
		procs[i].Runtime = rt
	}
}

// ProcessSetChanged reports whether the process set changed since the last
// call: one snapshot, compared as (PID, creation) keys against the previous
// call's keys (the first call reports true). No modules are read — change
// detection needs identity only. A snapshot error reports false (no evidence
// of change, never a guess) and keeps the baseline so the next call retries.
// Kept for callers that want a simple edge verdict; the host gate uses the
// monotonic Generation instead so a skipped group never loses a change (W3-03).
func (r *Resolver) ProcessSetChanged() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	procs, err := r.snapshotFn()
	if err != nil {
		return false
	}
	keys := keysOf(procs)
	changed := processSetChanged(r.changedLast, keys)
	r.changedLast = keys
	return changed
}

// detectRuntime reads one process's modules and fills Modules/Runtime. A failed
// read leaves Runtime 0 (unknown, never a guess). Used for unreadable-identity
// keys, which are never cached.
func (r *Resolver) detectRuntime(p *ProcInfo) {
	mods, err := r.loadModulesFn(p.PID)
	if err != nil {
		return
	}
	p.Modules = mods
	p.Runtime = classifyRuntime(mods)
}
