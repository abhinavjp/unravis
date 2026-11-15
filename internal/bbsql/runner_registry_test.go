package bbsql

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// registryTestSampler is the minimal Sampler needed to exercise registration.
// Decode is not part of the registry contract, so it returns no rows.
type registryTestSampler struct {
	id       string
	kind     string
	interval time.Duration
}

func (s *registryTestSampler) ID() string              { return s.id }
func (s *registryTestSampler) Kind() string            { return s.kind }
func (s *registryTestSampler) Interval() time.Duration { return s.interval }

func (s *registryTestSampler) Decode([]Row) (any, bool, error) { return nil, false, nil }

// The registry is process-global, so every test in this file namespaces its
// Kind with t.Name() and only asserts on its own entries.
func registryKindFor(t *testing.T) string { return "reg-" + t.Name() }

func registrySamplersWithKind(kind string) []Sampler {
	var out []Sampler
	for _, s := range Samplers() {
		if s.Kind() == kind {
			out = append(out, s)
		}
	}
	return out
}

func registryKindsWithPrefix(prefix string) []string {
	var out []string
	for _, s := range Samplers() {
		if strings.HasPrefix(s.Kind(), prefix) {
			out = append(out, s.Kind())
		}
	}
	return out
}

func TestRegisterSamplerNilIgnored(t *testing.T) {
	before := len(Samplers())
	RegisterSampler(nil)
	if got := len(Samplers()); got != before {
		t.Fatalf("RegisterSampler(nil) changed registry size: got %d, want %d", got, before)
	}
}

func TestRegisterSamplerDuplicateKindIgnored(t *testing.T) {
	kind := registryKindFor(t)
	first := &registryTestSampler{id: "bb:requests", kind: kind, interval: BaseInterval}
	second := &registryTestSampler{id: "bb:waits", kind: kind, interval: BaseInterval}
	RegisterSampler(first)
	RegisterSampler(second)

	got := registrySamplersWithKind(kind)
	if len(got) != 1 {
		t.Fatalf("duplicate Kind produced %d entries, want 1", len(got))
	}
	if got[0].ID() != first.id {
		t.Fatalf("duplicate Kind replaced first entry: got %q, want %q", got[0].ID(), first.id)
	}
}

func TestSamplersStableOrder(t *testing.T) {
	prefix := registryKindFor(t) + "-"
	for _, suffix := range []string{"c", "a", "e", "b", "d"} {
		RegisterSampler(&registryTestSampler{id: "bb:requests", kind: prefix + suffix, interval: BaseInterval})
	}
	want := []string{prefix + "a", prefix + "b", prefix + "c", prefix + "d", prefix + "e"}
	first := registryKindsWithPrefix(prefix)
	if len(first) != len(want) {
		t.Fatalf("got %d samplers, want %d", len(first), len(want))
	}
	for i := range want {
		if first[i] != want[i] {
			t.Fatalf("Samplers() not sorted by Kind at %d: got %q, want %q", i, first[i], want[i])
		}
	}
	second := registryKindsWithPrefix(prefix)
	for i := range first {
		if second[i] != first[i] {
			t.Fatalf("Samplers() order changed between calls at %d: got %q, want %q", i, second[i], first[i])
		}
	}
}

func TestSamplersSnapshotNotAliased(t *testing.T) {
	kind := registryKindFor(t)
	RegisterSampler(&registryTestSampler{id: "bb:requests", kind: kind, interval: BaseInterval})

	got := Samplers()
	if len(got) == 0 {
		t.Fatal("registry unexpectedly empty")
	}
	mutated := "reg-mutated-" + t.Name()
	got[0] = &registryTestSampler{id: "bb:waits", kind: mutated, interval: BaseInterval}

	if leaked := registrySamplersWithKind(mutated); len(leaked) != 0 {
		t.Fatalf("mutating the Samplers() slice changed the registry: found %d %q entries", len(leaked), mutated)
	}
	if kept := registrySamplersWithKind(kind); len(kept) != 1 {
		t.Fatalf("registry entry vanished after snapshot mutation: got %d entries", len(kept))
	}
}

func TestSamplerIDIsAllowlisted(t *testing.T) {
	RegisterSampler(&registryTestSampler{id: "bb:requests", kind: registryKindFor(t), interval: BaseInterval})

	for _, s := range Samplers() {
		if _, ok := Statement(s.ID()); !ok {
			t.Fatalf("sampler %q uses non-allowlisted id %q", s.Kind(), s.ID())
		}
	}

	sampling := []string{"bb:requests", "bb:waits", "bb:schedulers", "bb:semaphores", "bb:tempdb", "bb:filestats", "bb:ag"}
	for _, id := range sampling {
		if text, ok := Statement(id); !ok || text == "" {
			t.Fatalf("sampling statement %q is not resolvable", id)
		}
	}

	ceiling := []string{"bb:ceiling_role", "bb:ceiling_perms"}
	for _, id := range ceiling {
		if _, ok := Statement(id); !ok {
			t.Fatalf("ceiling statement %q should remain allowlisted for CheckCeiling", id)
		}
		for _, s := range Samplers() {
			if s.ID() == id {
				t.Fatalf("ceiling statement %q registered as a sampler", id)
			}
		}
	}
}

func TestConcurrentRegisterSafe(t *testing.T) {
	const workers = 8
	prefix := registryKindFor(t) + "-w"
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			RegisterSampler(&registryTestSampler{id: "bb:requests", kind: prefix + strconv.Itoa(i), interval: BaseInterval})
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = Samplers()
			}
		}()
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		kind := prefix + strconv.Itoa(i)
		if got := registrySamplersWithKind(kind); len(got) != 1 {
			t.Fatalf("concurrent registration lost kind %q: got %d entries", kind, len(got))
		}
	}
}

func TestSamplerIntervalAllowed(t *testing.T) {
	base := registryKindFor(t) + "-base"
	demoted := registryKindFor(t) + "-demoted"
	bad := registryKindFor(t) + "-bad"
	RegisterSampler(&registryTestSampler{id: "bb:requests", kind: base, interval: BaseInterval})
	RegisterSampler(&registryTestSampler{id: "bb:requests", kind: demoted, interval: DemotedInterval})
	RegisterSampler(&registryTestSampler{id: "bb:requests", kind: bad, interval: 30 * time.Second})

	for _, kind := range []string{base, demoted} {
		if got := registrySamplersWithKind(kind); len(got) != 1 {
			t.Fatalf("allowed interval rejected for kind %q: got %d entries", kind, len(got))
		}
	}
	if got := registrySamplersWithKind(bad); len(got) != 0 {
		t.Fatalf("unsupported interval registered for kind %q: got %d entries", bad, len(got))
	}
}
