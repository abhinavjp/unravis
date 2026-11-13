package blackbox

import (
	"strconv"
	"strings"
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
// (PID, Start) confirmed from the inventory — never PID alone; a PID whose
// creation time is unreadable (Start 0) is "not running", never a zeroed
// target. Instance/service lists are re-enumerated only when the process set
// changes. Unmatched selected names come back in the second slice, in order.
func (r *Resolver) ResolveTargets(targets []Target) ([]ResolvedTarget, []string) {
	var missing []string
	procs, err := r.Inventory()
	if err != nil {
		for _, t := range targets {
			missing = append(missing, t.Name)
		}
		return nil, missing
	}
	byPID := make(map[uint32]ProcInfo, len(procs))
	keys := make([]procKey, len(procs))
	for i, p := range procs {
		byPID[p.PID] = p
		keys[i] = procKey{pid: p.PID, start: p.Start}
	}
	// Pools and services are cached INDEPENDENTLY: a failure of one enumerator
	// never blocks the other (a host with no IIS still resolves services) and
	// never forces re-enumeration of the healthy one. Each baseline advances
	// with the attempt, so a persistent failure retries on the next process-set
	// change, never every tick; the cached list advances only on success, so an
	// error keeps the last good list. Unreadable means missing, never a guess.
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
	var out []ResolvedTarget
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

// matchPool finds the selected pool's w3wp by counter instance name and
// confirms (PID, Start) from the inventory. A runtime-0 w3wp with a live
// runtime-2 child targets the CHILD (out-of-process .NET 10); the key stays
// the pool's.
func matchPool(procs []ProcInfo, byPID map[uint32]ProcInfo, instances []string, name string) (ResolvedTarget, bool) {
	for _, inst := range instances {
		pid, pool, ok := parseW3WPInstance(inst)
		if !ok || !strings.EqualFold(pool, name) {
			continue
		}
		p, live := byPID[pid]
		if !live || p.Start == 0 {
			continue // stale instance or unreadable creation time
		}
		if p.Runtime == 0 {
			for _, c := range dotnetChildren(procs, pid) {
				if c.Start != 0 {
					return ResolvedTarget{Key: "pool:" + name, PID: c.PID, Start: c.Start, Runtime: c.Runtime, Pool: name}, true
				}
			}
		}
		return ResolvedTarget{Key: "pool:" + name, PID: p.PID, Start: p.Start, Runtime: p.Runtime, Pool: name}, true
	}
	return ResolvedTarget{}, false
}

// matchService finds the selected running service by name (case-insensitive)
// and confirms (PID, Start) from the inventory.
func matchService(byPID map[uint32]ProcInfo, services []svcProc, name string) (ResolvedTarget, bool) {
	for _, s := range services {
		if s.pid == 0 || !strings.EqualFold(s.name, name) {
			continue
		}
		if p, live := byPID[s.pid]; live && p.Start != 0 {
			return ResolvedTarget{Key: "svc:" + name, PID: p.PID, Start: p.Start, Runtime: p.Runtime}, true
		}
	}
	return ResolvedTarget{}, false
}
