package blackbox

import (
	"io"
	"slices"
	"sort"
	"sync"
	"time"
)

// ResolvedTarget is one resolved collection target. This packet owns the
// shape; the resolver (BBX-1-08) fills it.
type ResolvedTarget struct {
	Key     string // "pool:<name>" or "svc:<name>"
	PID     uint32
	Start   int64  // unix creation time, part of the identity
	Runtime uint8  // 0 unknown, 1 .NET Framework (clr.dll), 2 .NET 10 (coreclr/hostfxr)
	Pool    string // app pool name when Key is "pool:<name>"
}

// GroupDeps carries what a group factory needs. RunHost wires Targets (nil
// when no targets are selected) and ProcessSetChanged from the Resolver.
type GroupDeps struct {
	Source            CounterSource
	Engine            *Engine
	Targets           func() []ResolvedTarget
	Config            Config
	Cores             int
	ProcessSetChanged func() bool
}

// Group is one counter group, collected serially within the per-group budget.
type Group interface {
	Name() string
	Optional() bool
	Collect(now time.Time, dt float64) (any, error)
}

// GroupFactory builds a group; each group file registers one from init().
type GroupFactory func(GroupDeps) (Group, error)

type registration struct {
	optional bool
	factory  GroupFactory
}

var (
	registryMu sync.Mutex
	registry   = map[string]registration{}
)

// RegisterGroup adds a factory to the registry (called from init()).
func RegisterGroup(name string, optional bool, f GroupFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = registration{optional: optional, factory: f}
}

// BuildGroups instantiates every registered factory in name order. A factory
// that errors is skipped silently: the group is absent, nothing recorded.
func BuildGroups(d GroupDeps) []Group {
	registryMu.Lock()
	regs := make(map[string]registration, len(registry))
	for name, reg := range registry {
		regs[name] = reg
	}
	registryMu.Unlock()
	names := make([]string, 0, len(regs))
	for name := range regs {
		names = append(names, name)
	}
	sort.Strings(names)
	var groups []Group
	for _, name := range names {
		g, err := regs[name].factory(d)
		if err != nil {
			continue
		}
		groups = append(groups, g)
	}
	return groups
}

// registeredNames lists the registered group names, sorted (for tests).
func registeredNames() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// assignIf sets *dst only when v holds the matching pointer type: a wrong
// type must leave the field untouched (missing, never zero-filled).
func assignIf[T any](dst **T, v any) {
	if g, ok := v.(*T); ok {
		*dst = g
	}
}

// applyGroup writes a group result to its Sample field. Unknown names and
// wrong types are ignored, never a panic.
func applyGroup(s *Sample, name string, v any) {
	switch name {
	case "http":
		assignIf(&s.HTTP, v)
	case "iis":
		assignIf(&s.IIS, v)
	case "diskCpu":
		assignIf(&s.DiskCPU, v)
	case "sqlHost":
		assignIf(&s.SQLHost, v)
	case "clr":
		assignIf(&s.CLR, v)
	case "proc10":
		assignIf(&s.Proc10, v)
	case "procIO":
		assignIf(&s.ProcIO, v)
	case "events":
		assignIf(&s.Events, v)
	case "tcp":
		assignIf(&s.TCP, v)
	case "aspnet":
		assignIf(&s.ASPNET, v)
	case "memPressure":
		assignIf(&s.MemPressure, v)
	case "vm":
		assignIf(&s.VM, v)
	case "dumps":
		assignIf(&s.Dumps, v)
	}
}

// groupCollector wraps the host Collector with the budgeted group pass.
// Groups run serially in the existing tick; no goroutine is started.
type groupCollector struct {
	inner  Collector
	groups []Group
	budget *Budget
	last   time.Time
}

// NewGroupCollector wraps inner so each group runs under the budget.
func NewGroupCollector(inner Collector, groups []Group, b *Budget) Collector {
	return &groupCollector{inner: inner, groups: groups, budget: b}
}

func (c *groupCollector) Collect(now time.Time) (Sample, error) {
	s, err := c.inner.Collect(now)
	if err != nil {
		return s, err
	}
	var dt float64
	if !c.last.IsZero() {
		dt = now.Sub(c.last).Seconds()
	}
	c.last = now
	slow := c.budget.SlowGroups()
	for _, g := range c.groups {
		name := g.Name()
		if c.budget.GroupSkipped(name, g.Optional()) {
			if slices.Contains(slow, name) {
				// Consume the one-cycle slow mark so the group is re-probed.
				c.budget.NoteSkipped(name)
				s.Missing = append(s.Missing, "skipped: slow")
			} else {
				s.Missing = append(s.Missing, name)
			}
			continue
		}
		c.runGroup(g, now, dt, &s)
	}
	s.Missing = dedupMissing(s.Missing)
	return s, nil
}

// runGroup times one group and applies its result. A panic is a failure of
// that group only: recovered and recorded in Missing, never a crashed tick.
func (c *groupCollector) runGroup(g Group, now time.Time, dt float64, s *Sample) {
	name := g.Name()
	defer func() {
		if recover() != nil {
			s.Missing = append(s.Missing, name)
		}
	}()
	start := time.Now()
	v, err := g.Collect(now, dt)
	c.budget.GroupTiming(name, time.Since(start), g.Optional())
	if err != nil {
		s.Missing = append(s.Missing, name)
		return
	}
	applyGroup(s, name, v)
}

func (c *groupCollector) Close() error {
	err := c.inner.Close()
	for _, g := range c.groups {
		if cl, ok := g.(io.Closer); ok {
			if cerr := cl.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}
	}
	return err
}

// dedupMissing keeps the first occurrence of each name, in order.
func dedupMissing(names []string) []string {
	if len(names) < 2 {
		return names
	}
	seen := make(map[string]bool, len(names))
	out := names[:0]
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
