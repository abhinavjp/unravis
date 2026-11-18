package blackbox

import (
	"math/rand"
	"sort"
	"testing"
	"time"

	"api-audit-explorer/internal/sampler"
)

// The bounded selection must give exactly what a full sort of the whole list
// gives, for any mix of CPU change, working set, ties and unreadable fields.
func TestInsertTopEqualsFullSort(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 200; round++ {
		n := rng.Intn(60)
		h := &HostCollector{}
		list := make([]scored, 0, n)
		for i := 0; i < n; i++ {
			h.procs = append(h.procs, sampler.ProcSample{PID: uint32(i + 1)})
			list = append(list, scored{i: i, delta: float64(rng.Intn(5)), ws: uint64(rng.Intn(4))})
		}
		for _, k := range []int{1, 5, 10, 70} {
			var got []scored
			for _, sc := range list {
				got = insertTop(got, k, sc, h.cpuBefore)
			}
			want := append([]scored(nil), list...)
			sort.SliceStable(want, func(a, b int) bool { return h.cpuBefore(want[a], want[b]) })
			if len(want) > k {
				want = want[:k]
			}
			if len(got) != len(want) {
				t.Fatalf("cpu k=%d: %d rows, want %d", k, len(got), len(want))
			}
			for i := range got {
				if got[i].i != want[i].i {
					t.Fatalf("cpu k=%d row %d: %d, want %d", k, i, got[i].i, want[i].i)
				}
			}
			got = got[:0]
			for _, sc := range list {
				got = insertTop(got, k, sc, h.memBefore)
			}
			want = append(want[:0], list...)
			sort.SliceStable(want, func(a, b int) bool { return h.memBefore(want[a], want[b]) })
			if len(want) > k {
				want = want[:k]
			}
			for i := range got {
				if got[i].i != want[i].i {
					t.Fatalf("mem k=%d row %d: %d, want %d", k, i, got[i].i, want[i].i)
				}
			}
		}
	}
}

// With a few hundred processes the per-tick ranking allocates only the rows it
// returns, not sorted copies of the whole list.
func TestTopProcessesDoesNotAllocateWholeList(t *testing.T) {
	first, second := shapeProcs(400)
	src := &fakeSource{cores: 4, host: []sampler.HostSample{{}}, procs: [][]sampler.ProcSample{first, second, second}}
	h := NewHostCollector(src, &fakeExtras{}, true)
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if err := h.Prime(now); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Collect(now.Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	h.procs = append(h.procs[:0], second...)
	n := testing.AllocsPerRun(50, func() { _ = h.topProcesses(15) })
	// The row slice plus one cpu/ws/handle copy per listed row: far below the
	// several hundred a full sort of copies cost.
	if n > 60 {
		t.Fatalf("%v allocations per ranking of 400 processes", n)
	}
}
