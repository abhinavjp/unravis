package investigate

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"api-audit-explorer/internal/audit"
)

// updateHistory appends the current (Version, fingerprint) pair to
// testdata/investigate/thresholds.history.json. Run it once, on purpose, after a
// deliberate change that also bumped Version:
//
//	go test ./internal/investigate -run TestThresholdsHistory -update-history
var updateHistory = flag.Bool("update-history", false, "append the current threshold version and fingerprint to the history file")

const historyPath = "../../testdata/investigate/thresholds.history.json"

type historyEntry struct {
	Version     string `json:"version"`
	Fingerprint string `json:"fingerprint"`
}

func readHistory(t *testing.T) []historyEntry {
	t.Helper()
	raw, err := os.ReadFile(historyPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	var h []historyEntry
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("parse history: %v", err)
	}
	return h
}

// TestThresholdsHistory is the "changing a threshold without bumping Version
// fails" proof. The history is append-only: one fingerprint per Version.
func TestThresholdsHistory(t *testing.T) {
	cur := Current()
	h := readHistory(t)

	if *updateHistory {
		for _, e := range h {
			if e.Version == cur.Version {
				if e.Fingerprint != cur.Fingerprint() {
					t.Fatalf("version %s is already recorded with a different fingerprint; bump Version instead of rewriting history", cur.Version)
				}
				return
			}
		}
		h = append(h, historyEntry{cur.Version, cur.Fingerprint()})
		out, _ := json.MarshalIndent(h, "", "  ")
		if err := os.MkdirAll(filepath.Dir(historyPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(historyPath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	seen := map[string]string{}
	for _, e := range h {
		if prev, ok := seen[e.Version]; ok && prev != e.Fingerprint {
			t.Fatalf("history holds two fingerprints for version %s", e.Version)
		}
		seen[e.Version] = e.Fingerprint
	}
	fp, ok := seen[cur.Version]
	if !ok {
		t.Fatalf("version %s is not in %s; run the test with -update-history after bumping Version on purpose", cur.Version, historyPath)
	}
	if fp != cur.Fingerprint() {
		t.Fatalf("a threshold changed (fingerprint %s, recorded %s) but Version is still %s: bump Version in thresholds.go, then run with -update-history", cur.Fingerprint(), fp, cur.Version)
	}
}

func TestThresholdsVersionLiteral(t *testing.T) {
	if Version != "2026-10-27" {
		t.Fatalf("Version = %q; the first table is 2026-10-27", Version)
	}
	if Current().Version != Version {
		t.Fatal("Current().Version must repeat Version")
	}
}

// TestThresholdsFingerprintMoves proves the fingerprint reacts to any single
// value, so the history test cannot be fooled by an edit it does not see.
func TestThresholdsFingerprintMoves(t *testing.T) {
	base := Current()
	baseFP := base.Fingerprint()
	v := reflect.ValueOf(&base).Elem()
	typ := v.Type()
	n := 0
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Float64:
			old := f.Float()
			f.SetFloat(old + 1)
			if base.Fingerprint() == baseFP {
				t.Errorf("changing %s did not change the fingerprint", typ.Field(i).Name)
			}
			f.SetFloat(old)
			n++
		case reflect.Int:
			old := f.Int()
			f.SetInt(old + 1)
			if base.Fingerprint() == baseFP {
				t.Errorf("changing %s did not change the fingerprint", typ.Field(i).Name)
			}
			f.SetInt(old)
			n++
		}
	}
	if n != len(base.Entries()) {
		t.Errorf("Entries lists %d values but the struct has %d numeric fields", len(base.Entries()), n)
	}
	if base.Fingerprint() != baseFP {
		t.Error("fingerprint not restored")
	}
}

func TestThresholdsMatchDecisions(t *testing.T) {
	c := Current()
	if c.GroupSizeP95 != audit.MinSampleCount || c.GroupSizeP99 != audit.P99MinSampleCount {
		t.Errorf("group sizes %d/%d differ from audit constants %d/%d", c.GroupSizeP95, c.GroupSizeP99, audit.MinSampleCount, audit.P99MinSampleCount)
	}
	checks := []struct {
		name      string
		got, want float64
	}{
		{"A22 lambda", c.ArrivalRatePerSec, 40},
		{"A22 clock tolerance ms", c.ClockToleranceMs, 1000},
		{"A23 percent", c.ChangeMinPercent, 30},
		{"A23 ms", c.ChangeMinMs, 100},
		{"A24 factor", c.OnsetFactor, 2},
		{"A24 minutes", float64(c.OnsetRunMinutes), 5},
		{"A35 gap warn", float64(c.GapWarnMinutes), 2},
	}
	for _, k := range checks {
		if k.got != k.want {
			t.Errorf("%s = %v, decision says %v", k.name, k.got, k.want)
		}
	}
}

func TestThresholdsNames(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Current().Entries() {
		if e.Name == "" || seen[e.Name] {
			t.Errorf("entry name %q is empty or repeated", e.Name)
		}
		seen[e.Name] = true
	}
}
