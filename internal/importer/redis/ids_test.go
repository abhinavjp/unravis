package redis

import (
	"testing"

	"api-audit-explorer/internal/evidence"
)

// idsOf collects the evidence IDs of one parse and fails on a repeat.
func idsOf(t *testing.T, evs []evidence.Event) []string {
	t.Helper()
	seen := map[string]bool{}
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		if seen[e.EvidenceID] {
			t.Fatalf("evidence id repeated across entries: %s", e.EvidenceID)
		}
		seen[e.EvidenceID] = true
		out = append(out, e.EvidenceID)
	}
	return out
}

func sameIDs(t *testing.T, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("second parse has %d ids, first had %d", len(b), len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("id %d changed between parses", i)
		}
	}
}

// redis-cli --json prints the whole reply on one line, so every entry shares a line number.
// Each entry still needs its own evidence ID, the same on every parse of the same input.
func TestSlowlogJSONEntriesHaveUniqueStableIDs(t *testing.T) {
	const js = `[[3,1003971760,900,["GET","a"],"192.0.2.10:1","web-a-conn"],` +
		`[2,1003971750,800,["SET","b","c"],"192.0.2.10:2","web-a-conn"],` +
		`[1,1003971740,700,["DEL","d"],"192.0.2.10:3","web-a-conn"]]` + "\n"
	first, _ := mustCollect(t, NewSlowlog(), js, slowOpts())
	if len(first) != 3 {
		t.Fatalf("want 3 events, got %d", len(first))
	}
	a := idsOf(t, first)
	second, _ := mustCollect(t, NewSlowlog(), js, slowOpts())
	sameIDs(t, a, idsOf(t, second))
}

func TestSlowlogTextEntriesHaveUniqueStableIDs(t *testing.T) {
	first, _ := mustCollect(t, NewSlowlog(), slowGood, slowOpts())
	if len(first) != 2 {
		t.Fatalf("want 2 events, got %d", len(first))
	}
	a := idsOf(t, first)
	second, _ := mustCollect(t, NewSlowlog(), slowGood, slowOpts())
	sameIDs(t, a, idsOf(t, second))
}

func TestLatencyJSONEntriesHaveUniqueStableIDs(t *testing.T) {
	const js = "# latency-event: fork\n[[1003971745,230],[1003971800,180],[1003971900,90]]\n"
	first, _ := mustCollect(t, NewLatency(), js, hostOpts())
	if len(first) != 3 {
		t.Fatalf("want 3 events, got %d", len(first))
	}
	a := idsOf(t, first)
	second, _ := mustCollect(t, NewLatency(), js, hostOpts())
	sameIDs(t, a, idsOf(t, second))
}

func TestLatencyTextEntriesHaveUniqueStableIDs(t *testing.T) {
	first, _ := mustCollect(t, NewLatency(), latGood, hostOpts())
	if len(first) != 3 {
		t.Fatalf("want 3 events, got %d", len(first))
	}
	a := idsOf(t, first)
	second, _ := mustCollect(t, NewLatency(), latGood, hostOpts())
	sameIDs(t, a, idsOf(t, second))
}
