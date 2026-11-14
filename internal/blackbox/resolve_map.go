package blackbox

import (
	"strconv"
	"strings"
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
// command line.
type svcProc struct {
	name string
	pid  uint32
}

// ResolveTargets maps owner-selected targets to live processes. Identity is
// (PID, creation ticks) confirmed from the inventory — never PID alone; a PID
// whose creation time is unreadable is "not running", never a zeroed target.
// Instance/service lists are re-enumerated only when the process set changes,
// and fresh module classification is bounded to the selected identities and
// their children (W3-05). Unmatched selected names come back in the second
// slice, in order. Each call takes ONE process snapshot; the host gate shares a
// single per-tick result instead of calling this per consumer.
func (r *Resolver) ResolveTargets(targets []Target) ([]ResolvedTarget, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, missing, _ := r.resolveLocked(targets, r.nowFn())
	return out, missing
}

// tickView is the IMMUTABLE per-tick resolution result shared by the host
// collector and every group (W3-05). Once stored it is never mutated, so a
// worker from an earlier tick can read it safely while the tick goroutine
// publishes a newer one. gen is the inventory generation the targets belong to.
type tickView struct {
	gen     uint64
	targets []ResolvedTarget
	missing []string
}

// resolveTick resolves ONE immutable inventory/target result for a collection
// cycle: one process snapshot, bounded module classification, the shared
// generation. The host gate stores the returned view and every consumer reads
// it, so a tick never re-inventories the host per consumer (W3-05).
func (r *Resolver) resolveTick(targets []Target, now time.Time) *tickView {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, missing, _ := r.resolveLocked(targets, now)
	return &tickView{gen: r.gen, targets: out, missing: missing}
}

// resolveLocked is the unlocked resolution body; callers must hold mu. It takes
// one snapshot, re-enumerates the instance/service lists on a process-set
// change, classifies only the selected identities (and their children), matches
// the targets, and advances the monotonic generation when the process set OR
// the resolved target set changed. On a snapshot error every selected name is
// missing and the generation/baseline are left untouched.
func (r *Resolver) resolveLocked(targets []Target, now time.Time) ([]ResolvedTarget, []string, error) {
	procs, keys, setChanged, err := r.snapshotLocked()
	if err != nil {
		var missing []string
		for _, t := range targets {
			missing = append(missing, t.Name)
		}
		return nil, missing, err
	}
	// Pools and services are cached INDEPENDENTLY: a failure of one enumerator
	// never blocks the other (a host with no IIS still resolves services) and
	// never forces re-enumeration of the healthy one. Each baseline advances
	// with the attempt, so a persistent failure retries on the next process-set
	// change, never every tick; the cached list advances only on success, so an
	// error keeps the last good list. Unreadable means missing, never a guess.
	// With nothing selected there is nothing to match, so the PDH/SCM enumerators
	// are not run at all (no startup or per-tick cost for a machine-wide run).
	if len(targets) > 0 {
		if processSetChanged(r.mapLastInst, keys) {
			r.mapLastInst = keys
			if inst, ierr := r.instancesFn(); ierr == nil {
				r.mapInst = inst
			}
		}
		if processSetChanged(r.mapLastSvc, keys) {
			r.mapLastSvc = keys
			if svc, serr := r.servicesFn(); serr == nil {
				r.mapSvc = svc
			}
		}
	}
	// Bound fresh module reads to what the selected targets need: the selected
	// pool/service PIDs plus their children (out-of-process .NET 10). Everything
	// else reuses a confirmed cache or stays unknown without a read (W3-05).
	r.classifyLocked(procs, keys, r.selectedPIDs(targets, procs), now)
	out, missing := r.matchTargets(targets, procs)
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
// instance/service lists, confirming identity from the inventory.
func (r *Resolver) matchTargets(targets []Target, procs []ProcInfo) ([]ResolvedTarget, []string) {
	byPID := make(map[uint32]ProcInfo, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
	}
	var out []ResolvedTarget
	var missing []string
	for _, t := range targets {
		var rt ResolvedTarget
		var ok bool
		switch t.Kind {
		case "appPool":
			rt, ok = matchPool(procs, byPID, r.mapInst, t.Name)
		case "service":
			rt, ok = matchService(byPID, r.mapSvc, t.Name)
		}
		if !ok {
			missing = append(missing, t.Name)
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

// matchPool finds the selected pool's w3wp by counter instance name and
// confirms (PID, creation) from the inventory. A runtime-0 w3wp with a live
// runtime-2 child targets the CHILD (out-of-process .NET 10); the key stays
// the pool's. Start in the result is unix seconds (consumer/wire shape); the
// lossless identity is the inventory's StartTicks.
func matchPool(procs []ProcInfo, byPID map[uint32]ProcInfo, instances []string, name string) (ResolvedTarget, bool) {
	for _, inst := range instances {
		pid, pool, ok := parseW3WPInstance(inst)
		if !ok || !strings.EqualFold(pool, name) {
			continue
		}
		p, live := byPID[pid]
		if !live || identityTicks(p) == 0 {
			continue // stale instance or unreadable creation time
		}
		if p.Runtime == 0 {
			for _, c := range dotnetChildren(procs, pid) {
				if identityTicks(c) != 0 {
					return ResolvedTarget{Key: "pool:" + name, PID: c.PID, Start: c.Start, Runtime: c.Runtime, Pool: name}, true
				}
			}
		}
		return ResolvedTarget{Key: "pool:" + name, PID: p.PID, Start: p.Start, Runtime: p.Runtime, Pool: name}, true
	}
	return ResolvedTarget{}, false
}

// matchService finds the selected running service by name (case-insensitive)
// and confirms (PID, creation) from the inventory.
func matchService(byPID map[uint32]ProcInfo, services []svcProc, name string) (ResolvedTarget, bool) {
	for _, s := range services {
		if s.pid == 0 || !strings.EqualFold(s.name, name) {
			continue
		}
		if p, live := byPID[s.pid]; live && identityTicks(p) != 0 {
			return ResolvedTarget{Key: "svc:" + name, PID: p.PID, Start: p.Start, Runtime: p.Runtime}, true
		}
	}
	return ResolvedTarget{}, false
}
