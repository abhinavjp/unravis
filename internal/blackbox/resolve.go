package blackbox

import "strings"

// ProcInfo is one process in the inventory. Identity is (PID, Start): a PID
// alone is not identity because Windows reuses PIDs after a recycle.
type ProcInfo struct {
	PID       uint32
	ParentPID uint32
	Start     int64    // unix creation time; 0 when it could not be read
	Name      string   // image base name only; no command line, no environment
	Modules   []string // module base names, filled only on a fresh module read
	Runtime   uint8    // 0 unknown, 1 .NET Framework (clr.dll), 2 .NET 10 (coreclr/hostfxr)
}

// procKey is process identity: PID plus creation time, never PID alone.
type procKey struct {
	pid   uint32
	start int64
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

// Resolver builds the process inventory and detects runtimes. Modules are read
// only when the process set changes, and only for keys not already cached. The
// snapshot and module loader are injected so tests need no Windows.
type Resolver struct {
	snapshotFn    func() ([]ProcInfo, error)
	loadModulesFn func(pid uint32) ([]string, error)

	last   []procKey
	cached map[procKey]uint8
}

// NewResolver wires the platform process snapshot and module loader.
func NewResolver() *Resolver {
	return &Resolver{
		snapshotFn:    snapshotProcesses,
		loadModulesFn: loadModules,
		cached:        make(map[procKey]uint8),
	}
}

// Inventory snapshots the processes and fills Runtime. When the process set is
// unchanged it reuses cached runtimes and reads no modules. When it changes it
// loads modules only for keys not cached (a recycled PID has a new start, so a
// new key). An access-denied module read leaves Runtime 0 (unknown), never a
// guess, and is not cached so a later set change retries it.
func (r *Resolver) Inventory() ([]ProcInfo, error) {
	procs, err := r.snapshotFn()
	if err != nil {
		return nil, err
	}
	keys := make([]procKey, len(procs))
	for i, p := range procs {
		keys[i] = procKey{pid: p.PID, start: p.Start}
	}

	if !processSetChanged(r.last, keys) {
		for i := range procs {
			procs[i].Runtime = r.cached[keys[i]]
		}
		return procs, nil
	}

	next := make(map[procKey]uint8, len(keys))
	for i := range procs {
		k := keys[i]
		rt, ok := r.cached[k]
		if !ok {
			mods, err := r.loadModulesFn(procs[i].PID)
			if err != nil {
				procs[i].Runtime = 0 // unknown, never guess; uncached -> retry on next change
				continue
			}
			procs[i].Modules = mods
			rt = classifyRuntime(mods)
		}
		next[k] = rt
		procs[i].Runtime = rt
	}
	r.last = keys
	r.cached = next
	return procs, nil
}
