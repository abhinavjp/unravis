package blackbox

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// parseW3WPInstance splits a W3SVC_W3WP counter instance "<PID>_<AppPoolName>"
// on the FIRST underscore: pid = leading digits, pool = the remainder (may
// hold spaces and underscores). No underscore, non-numeric/zero pid or an
// empty pool is rejected.
func parseW3WPInstance(inst string) (pid uint32, pool string, ok bool) {
	head, tail, found := strings.Cut(inst, "_")
	if !found || head == "" || tail == "" {
		return 0, "", false
	}
	for _, c := range head {
		if c < '0' || c > '9' {
			return 0, "", false
		}
	}
	n, err := strconv.ParseUint(head, 10, 32)
	if err != nil || n == 0 {
		return 0, "", false
	}
	return uint32(n), tail, true
}

// svcProc is one running service: name and PID only — never a binary path or
// command line. start/gen are the W4-01 binding: the process identity
// (identityTicks) and discovery generation CONFIRMED from the inventory when
// the mapping was enumerated. They are filled by the resolver, never by the
// platform enumerator; start == 0 means the mapping is unverified and must not
// be emitted.
type svcProc struct {
	name  string
	pid   uint32
	start int64  // confirmed identityTicks at enumeration (0 = unverified)
	gen   uint64 // discovery generation the mapping was verified at
}

// Fixed target availability codes (W4-01/W4-02). They never carry provider
// text (handoff §3): a name that did not resolve is reported with one of these.
const (
	targetOK          = ""            // resolved
	targetNotFound    = "not-running" // no live, confirmed match
	targetUnavailable = "unavailable" // enumeration failed / mapping unverified
	targetAmbiguous   = "ambiguous"   // more than one legitimate candidate
)

// instBinding binds one cached W3SVC_W3WP instance to the process identity
// CONFIRMED from the inventory when the instance list was enumerated (W4-01).
// start is identityTicks (PID + lossless creation); gen is the discovery
// generation. A cached instance whose bound identity no longer matches the live
// inventory is never emitted, so a reused PID cannot inherit the mapping.
type instBinding struct {
	pid   uint32
	start int64
	gen   uint64
}

// mapBindState is the per-resolver mapping binding/backoff state (W4-01). The
// Resolver struct itself lives in resolve.go, which this wave does not modify,
// so the binding state is kept in a side table keyed by *Resolver. Every access
// happens under Resolver.mu (resolveLocked / the public locked wrappers), so no
// extra lock is needed. Entries live for the life of the resolver (one per
// recorder run).
type mapBindState struct {
	inst      map[string]instBinding // instance name -> confirmed identity
	instNext  time.Time              // earliest retry after a failed enumeration
	instFails int                    // consecutive enumeration failures
	svcNext   time.Time
	svcFails  int
	status    map[string]string // per-name fixed availability code this tick
}

var mapBindStates sync.Map // *Resolver -> *mapBindState

// bindState returns (creating once) the mapping binding state for r. Callers
// must hold r.mu.
func (r *Resolver) bindState() *mapBindState {
	if v, ok := mapBindStates.Load(r); ok {
		return v.(*mapBindState)
	}
	s := &mapBindState{inst: make(map[string]instBinding), status: make(map[string]string)}
	actual, _ := mapBindStates.LoadOrStore(r, s)
	return actual.(*mapBindState)
}

// mapRetryMax caps the enumeration retry backoff (W4-01).
const mapRetryMax = 2 * time.Minute

// mapRetryDelay doubles from 15 s up to mapRetryMax: a bounded backoff so a
// persistent enumeration failure is retried without a per-tick storm.
func mapRetryDelay(failures int) time.Duration {
	d := 15 * time.Second
	for i := 1; i < failures && d < mapRetryMax; i++ {
		d *= 2
	}
	if d > mapRetryMax {
		return mapRetryMax
	}
	return d
}

// ResolveTargets maps owner-selected targets to live processes. Identity is
// (PID, creation ticks) confirmed from the inventory — never PID alone; a PID
// whose creation time is unreadable is "not running", never a zeroed target.
// Each cached pool/service mapping is BOUND to the identity and generation it
// was verified at (W4-01): a failed enumeration invalidates the cached list
// (fail-closed) and a reused PID never inherits a stale mapping. Ambiguous
// worker/child candidates are never resolved first-wins (W4-02). Instance and
// service lists are re-enumerated on a process-set change and, after a failure,
// on a bounded backoff independent of unrelated churn. Unmatched selected names
// come back in the second slice, in order; their fixed availability codes are
// available via TargetStatus. Each call takes ONE process snapshot; the host
// gate shares a single per-tick result instead of calling this per consumer.
func (r *Resolver) ResolveTargets(targets []Target) ([]ResolvedTarget, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, missing, _ := r.resolveLocked(targets, r.nowFn(), false)
	return out, missing
}

// TargetStatus returns the fixed availability code for each selected name that
// did not resolve on the last resolution (W4-01/W4-02): "not-running",
// "unavailable" or "ambiguous". Resolved names are absent. Never provider text.
func (r *Resolver) TargetStatus() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	bs := r.bindState()
	out := make(map[string]string, len(bs.status))
	for k, v := range bs.status {
		out[k] = v
	}
	return out
}

// tickView is the IMMUTABLE per-tick resolution result shared by the host
// collector and every group (W3-05). Once stored it is never mutated, so a
// worker from an earlier tick can read it safely while the tick goroutine
// publishes a newer one. gen is the inventory generation the targets belong to.
// status carries the fixed availability code for each unresolved name.
type tickView struct {
	gen     uint64
	sqlSig  uint64 // signature of the sqlservr.exe processes (see sqlProcessSig)
	targets []ResolvedTarget
	missing []string
	status  map[string]string
}

// resolveTick resolves ONE immutable inventory/target result for a collection
// cycle: one process snapshot, bounded module classification, the shared
// generation. With nothing selected the snapshot is the cheap one: no creation
// time is read, because no target needs an identity and the groups only ask
// whether the process set changed. The host gate stores the returned view and every consumer reads
// it, so a tick never re-inventories the host per consumer (W3-05).
func (r *Resolver) resolveTick(targets []Target, now time.Time) *tickView {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, missing, _ := r.resolveLocked(targets, now, len(targets) == 0)
	bs := r.bindState()
	status := make(map[string]string, len(bs.status))
	for k, v := range bs.status {
		status[k] = v
	}
	return &tickView{gen: r.gen, sqlSig: r.sqlSig, targets: out, missing: missing, status: status}
}

// resolveLocked is the unlocked resolution body; callers must hold mu. It takes
// one snapshot, re-enumerates the instance/service lists on a process-set
// change (or a due bounded retry after a failure), binds each confirmed mapping
// to its identity, classifies only the selected identities (and their children),
// matches the targets, and advances the monotonic generation when the process
// set OR the resolved target set changed. On a snapshot error every selected
// name is missing and the generation/baseline are left untouched.
func (r *Resolver) resolveLocked(targets []Target, now time.Time, lite bool) ([]ResolvedTarget, []string, error) {
	procs, keys, setChanged, err := r.snapshotLocked(lite)
	if err != nil {
		var missing []string
		for _, t := range targets {
			missing = append(missing, t.Name)
		}
		return nil, missing, err
	}
	bs := r.bindState()
	bs.status = make(map[string]string)
	// Pools and services are cached INDEPENDENTLY: a failure of one enumerator
	// never blocks the other (a host with no IIS still resolves services) and
	// never forces re-enumeration of the healthy one. A failed enumeration
	// INVALIDATES its cached list (fail-closed, W4-01) — a stale mapping must
	// never be rebound to a reused PID — and is retried on a bounded backoff
	// independent of unrelated churn. With nothing selected there is nothing to
	// match, so the PDH/SCM enumerators are not run at all.
	if len(targets) > 0 {
		r.refreshInstLocked(procs, keys, now, bs)
		r.refreshSvcLocked(procs, keys, now, bs)
	}
	// Bound fresh module reads to what the selected targets need: the selected
	// pool/service PIDs plus their children (out-of-process .NET 10). Everything
	// else reuses a confirmed cache or stays unknown without a read (W3-05).
	r.classifyLocked(procs, keys, r.selectedPIDs(targets, procs), now)
	out, missing := r.matchTargets(targets, procs, bs)
	// The generation advances on a process-set change OR a resolved-target
	// change (a new/departed identity, or a runtime that became known), so a
	// group re-expands when a late-loading runtime makes a target usable even
	// though the process set is stable (W3-02/W3-03).
	if setChanged || targetsChanged(r.lastTargets, out) {
		r.gen++
	}
	r.lastTargets = append([]ResolvedTarget(nil), out...)
	return out, missing, nil
}

// refreshInstLocked re-enumerates the W3SVC_W3WP instance list on a process-set
// change or a due bounded retry, and binds each confirmed instance to the
// process identity (PID + creation ticks) and generation it was verified at
// (W4-01). Only instances whose PID is live with a readable creation time are
// cached; a failed enumeration clears the list and its bindings (fail-closed).
func (r *Resolver) refreshInstLocked(procs []ProcInfo, keys []procKey, now time.Time, bs *mapBindState) {
	setChanged := processSetChanged(r.mapLastInst, keys)
	due := bs.instFails > 0 && !now.Before(bs.instNext)
	if !setChanged && !due {
		return
	}
	r.mapLastInst = keys
	inst, err := r.instancesFn()
	if err != nil {
		// Fail-closed: no stale mapping may survive a failed refresh.
		r.mapInst = nil
		bs.inst = make(map[string]instBinding)
		bs.instFails++
		bs.instNext = now.Add(mapRetryDelay(bs.instFails))
		return
	}
	byPID := make(map[uint32]ProcInfo, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	bindings := make(map[string]instBinding)
	var confirmed []string
	for _, name := range inst {
		pid, _, ok := parseW3WPInstance(name)
		if !ok {
			continue
		}
		p, live := byPID[pid]
		if !live || identityTicks(p) == 0 {
			continue // stale instance or unreadable creation: never cached
		}
		bindings[name] = instBinding{pid: pid, start: identityTicks(p), gen: r.gen}
		confirmed = append(confirmed, name)
	}
	r.mapInst = confirmed
	bs.inst = bindings
	bs.instFails = 0
	bs.instNext = time.Time{}
}

// refreshSvcLocked re-enumerates running services on a process-set change or a
// due bounded retry and binds each confirmed service to its process identity
// and generation (W4-01). A failed enumeration clears the list (fail-closed).
func (r *Resolver) refreshSvcLocked(procs []ProcInfo, keys []procKey, now time.Time, bs *mapBindState) {
	setChanged := processSetChanged(r.mapLastSvc, keys)
	due := bs.svcFails > 0 && !now.Before(bs.svcNext)
	if !setChanged && !due {
		return
	}
	r.mapLastSvc = keys
	svc, err := r.servicesFn()
	if err != nil {
		r.mapSvc = nil
		bs.svcFails++
		bs.svcNext = now.Add(mapRetryDelay(bs.svcFails))
		return
	}
	byPID := make(map[uint32]ProcInfo, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	var confirmed []svcProc
	for _, s := range svc {
		if s.pid == 0 {
			continue
		}
		p, live := byPID[s.pid]
		if !live || identityTicks(p) == 0 {
			continue // unverified: never cached, never emitted
		}
		s.start = identityTicks(p)
		s.gen = r.gen
		confirmed = append(confirmed, s)
	}
	r.mapSvc = confirmed
	bs.svcFails = 0
	bs.svcNext = time.Time{}
}

// selectedPIDs is the set of PIDs whose runtime the selected targets need: the
// live PIDs behind selected pool instances and service names, plus their direct
// children (a runtime-0 w3wp may target a runtime-2 out-of-process child).
func (r *Resolver) selectedPIDs(targets []Target, procs []ProcInfo) map[uint32]bool {
	sel := make(map[uint32]bool)
	for _, t := range targets {
		switch t.Kind {
		case "appPool":
			for _, inst := range r.mapInst {
				if pid, pool, ok := parseW3WPInstance(inst); ok && strings.EqualFold(pool, t.Name) {
					sel[pid] = true
				}
			}
		case "service":
			for _, s := range r.mapSvc {
				if s.pid != 0 && strings.EqualFold(s.name, t.Name) {
					sel[s.pid] = true
				}
			}
		}
	}
	for _, p := range procs {
		if sel[p.ParentPID] {
			sel[p.PID] = true
		}
	}
	return sel
}

// matchTargets matches the selected names against the inventory and the cached
// instance/service lists, confirming identity from the inventory and the
// per-mapping binding (W4-01). Unresolved names are recorded with a fixed
// availability code in bs.status.
func (r *Resolver) matchTargets(targets []Target, procs []ProcInfo, bs *mapBindState) ([]ResolvedTarget, []string) {
	byPID := make(map[uint32]ProcInfo, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	var out []ResolvedTarget
	var missing []string
	for _, t := range targets {
		var rt ResolvedTarget
		var status string
		switch t.Kind {
		case "appPool":
			rt, status = matchPool(procs, byPID, r.mapInst, bs.inst, t.Name)
			if status == targetNotFound && bs.instFails > 0 {
				status = targetUnavailable
			}
		case "service":
			rt, status = matchService(byPID, r.mapSvc, t.Name)
			if status == targetNotFound && bs.svcFails > 0 {
				status = targetUnavailable
			}
		}
		if status != targetOK {
			missing = append(missing, t.Name)
			bs.status[t.Name] = status
			continue
		}
		out = append(out, rt)
	}
	return out, missing
}

// targetsChanged reports whether the resolved target set differs (identity,
// runtime or order). A runtime that became known changes the target even when
// the process set is stable, which must advance the generation.
func targetsChanged(prev, cur []ResolvedTarget) bool {
	if len(prev) != len(cur) {
		return true
	}
	for i := range prev {
		a, b := prev[i], cur[i]
		if a.Key != b.Key || a.PID != b.PID || a.Start != b.Start || a.Runtime != b.Runtime || a.Pool != b.Pool {
			return true
		}
	}
	return false
}

// matchPool finds the selected pool's w3wp workers by counter instance name and
// confirms each against its bound identity (PID + creation ticks) from the
// inventory (W4-01). It never resolves first-wins (W4-02): more than one live
// worker (web garden / overlap) is ambiguous. A runtime-0 worker targets its
// out-of-process .NET 10 child only when the child was created AFTER the worker
// (an orphan whose ParentPID was reused is rejected); more than one legitimate
// child is ambiguous. The key stays the pool's. Start in the result is unix
// seconds (consumer/wire shape); the lossless identity is the inventory's
// StartTicks.
func matchPool(procs []ProcInfo, byPID map[uint32]ProcInfo, instances []string, bindings map[string]instBinding, name string) (ResolvedTarget, string) {
	seen := make(map[procKey]bool)
	var workers []ProcInfo
	for _, inst := range instances {
		pid, pool, ok := parseW3WPInstance(inst)
		if !ok || !strings.EqualFold(pool, name) {
			continue
		}
		b, bound := bindings[inst]
		if !bound || b.start == 0 {
			continue // unverified mapping: never emitted
		}
		p, live := byPID[pid]
		if !live || identityTicks(p) == 0 {
			continue
		}
		if b.start != identityTicks(p) {
			continue // identity changed (reused PID): invalidate, fail closed
		}
		k := keyOf(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		workers = append(workers, p)
	}
	if len(workers) == 0 {
		return ResolvedTarget{}, targetNotFound
	}
	if len(workers) > 1 {
		return ResolvedTarget{}, targetAmbiguous // never silently pick a worker
	}
	w := workers[0]
	if w.Runtime == 0 {
		var children []ProcInfo
		for _, c := range dotnetChildren(procs, w.PID) {
			ct := identityTicks(c)
			if ct == 0 {
				continue // unreadable creation: unverified
			}
			if ct <= identityTicks(w) {
				continue // orphan: child not created after the current parent
			}
			children = append(children, c)
		}
		if len(children) == 1 {
			c := children[0]
			return ResolvedTarget{Key: "pool:" + name, PID: c.PID, Start: c.Start, StartTicks: identityTicks(c), Runtime: c.Runtime, Pool: name}, targetOK
		}
		if len(children) > 1 {
			return ResolvedTarget{}, targetAmbiguous // never silently pick a child
		}
		// No legitimate child: the worker itself is the target (runtime may
		// still be pending; the bounded runtime retry reclassifies it).
	}
	return ResolvedTarget{Key: "pool:" + name, PID: w.PID, Start: w.Start, StartTicks: identityTicks(w), Runtime: w.Runtime, Pool: name}, targetOK
}

// matchService finds the selected running service by name (case-insensitive)
// and confirms its bound identity (PID + creation ticks) from the inventory
// (W4-01). An unverified binding or a reused PID is never emitted.
func matchService(byPID map[uint32]ProcInfo, services []svcProc, name string) (ResolvedTarget, string) {
	for _, s := range services {
		if s.pid == 0 || !strings.EqualFold(s.name, name) {
			continue
		}
		p, live := byPID[s.pid]
		if !live || identityTicks(p) == 0 {
			continue
		}
		if s.start == 0 || s.start != identityTicks(p) {
			continue // unverified binding or reused PID: fail closed
		}
		return ResolvedTarget{Key: "svc:" + name, PID: p.PID, Start: p.Start, StartTicks: identityTicks(p), Runtime: p.Runtime}, targetOK
	}
	return ResolvedTarget{}, targetNotFound
}
