package localci

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemClaimHost is a ClaimHost that keeps everything in memory, for tests and for trying the fleet out. Like the
// real host it gives exactly one winner to many creates of one name, and it stamps a record with its own clock.
type MemClaimHost struct {
	// Now is the host's clock; nil means time.Now. Tests move it to age a lease.
	Now func() time.Time

	mu      sync.Mutex
	refs    map[string]memRef
	failing error
	calls   map[string]int
}

type memRef struct {
	rec     FleetRecord
	written time.Time
}

func (h *MemClaimHost) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func memKey(ns, name string) string { return ns + "/" + name }

func (h *MemClaimHost) check(op, ns, name string) error {
	if h.calls == nil {
		h.calls = map[string]int{}
	}
	h.calls[op]++
	if h.failing != nil {
		return h.failing
	}
	if !ValidRefNS(ns) {
		return fmt.Errorf("ref namespace %q is not one of the fleet's", ns)
	}
	if name != "" && !ValidRefName(name) {
		return fmt.Errorf("%q is not a ref name", name)
	}
	return nil
}

// Fail makes every following call return err, until Fail(nil): a host that cannot be reached.
func (h *MemClaimHost) Fail(err error) {
	h.mu.Lock()
	h.failing = err
	h.mu.Unlock()
}

// Calls says how often op (list, create, update or delete) was called.
func (h *MemClaimHost) Calls(op string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[op]
}

// Backdate makes a ref look as if it was written d ago, for a lease nobody refreshed.
func (h *MemClaimHost) Backdate(ns, name string, d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.refs[memKey(ns, name)]; ok {
		r.written = h.now().Add(-d)
		h.refs[memKey(ns, name)] = r
	}
}

// Names lists the refs of ns, sorted.
func (h *MemClaimHost) Names(ns string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for k := range h.refs {
		if n, ok := strings.CutPrefix(k, ns+"/"); ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Record returns the record of a ref.
func (h *MemClaimHost) Record(ns, name string) (FleetRecord, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.refs[memKey(ns, name)]
	return r.rec, ok
}

// ListRefs implements ClaimHost.
func (h *MemClaimHost) ListRefs(_ context.Context, _, ns, prefix string) ([]RefRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.check("list", ns, ""); err != nil {
		return nil, err
	}
	var out []RefRecord
	for k, r := range h.refs {
		n, ok := strings.CutPrefix(k, ns+"/")
		if !ok || !strings.HasPrefix(n, prefix) {
			continue
		}
		out = append(out, RefRecord{Name: n, Rec: r.rec, Age: max(h.now().Sub(r.written), 0)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CreateRef implements ClaimHost.
func (h *MemClaimHost) CreateRef(_ context.Context, _, ns, name string, rec FleetRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.check("create", ns, name); err != nil {
		return err
	}
	if h.refs == nil {
		h.refs = map[string]memRef{}
	}
	if _, ok := h.refs[memKey(ns, name)]; ok {
		return ErrRefExists
	}
	h.refs[memKey(ns, name)] = memRef{rec: rec, written: h.now()}
	return nil
}

// UpdateRef implements ClaimHost.
func (h *MemClaimHost) UpdateRef(_ context.Context, _, ns, name string, rec FleetRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.check("update", ns, name); err != nil {
		return err
	}
	if _, ok := h.refs[memKey(ns, name)]; !ok {
		return ErrRefGone
	}
	h.refs[memKey(ns, name)] = memRef{rec: rec, written: h.now()}
	return nil
}

// DeleteRef implements ClaimHost.
func (h *MemClaimHost) DeleteRef(_ context.Context, _, ns, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.check("delete", ns, name); err != nil {
		return err
	}
	delete(h.refs, memKey(ns, name))
	return nil
}
