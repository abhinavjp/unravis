package investigate

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// Golden tests lock the whole Result (thresholds included) of five named days. A
// golden file changes only on purpose: run `go test ./internal/investigate -update`
// and read the diff. A threshold change fails every golden file, and the
// thresholds test also fails until Version is bumped.
var updateGolden = flag.Bool("update", false, "rewrite the golden files in testdata/investigate")

const goldenDir = "../../testdata/investigate"

func goldenCheck(t *testing.T, name string, res Result) {
	t.Helper()
	got, err := json.MarshalIndent(res.Compact(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join(goldenDir, name+".golden.json")
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	// Git may check the file out with CRLF.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden file; run with -update and read the diff.\nfirst difference near byte %d", name, firstDiff(got, want))
	}
}

func firstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// demoInput pages every call of the demo repository (both sources) and builds an
// Input with the reference quarter-hour before the incident quarter-hour.
func demoInput(t *testing.T) Input {
	t.Helper()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	repo := audit.NewDemo(now)
	start, end := now.Add(-30*time.Minute), now
	q := audit.Query{
		SourceIDs: []string{"demo-west", "demo-east"}, Start: start, End: end,
		BaselineStart: start.Add(-time.Hour), BaselineEnd: start,
		BucketSeconds: 86400, Kind: "all", Outcome: "all", Metric: "total",
	}
	var rows []audit.RequestRecord
	cursor := ""
	for {
		page, err := repo.Requests(context.Background(), audit.PageInput{Query: q, Cursor: cursor, Limit: audit.MaxPageSize, Order: audit.OrderNewest})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, page.Rows...)
		if page.NextCursor == "" || len(page.Rows) == 0 {
			break
		}
		cursor = page.NextCursor
	}
	reqs, noStart := FromRecords(rows)
	return Input{
		Reference: Window{start, now.Add(-15 * time.Minute)},
		Incident:  Window{now.Add(-15 * time.Minute), now},
		Audit:     AuditPresent, Requests: reqs, NoStart: noStart,
	}
}

type goldenCase struct {
	name  string
	build func(t *testing.T) Input
	check func(t *testing.T, res Result)
}

func goldenCases() []goldenCase {
	ref, inc := stdWindows()
	return []goldenCase{
		{"demo-patterns", demoInput, func(t *testing.T, res Result) {
			if res.Facts.Counts.Used == 0 {
				t.Fatal("no demo calls were read")
			}
			// The demo's slowdown is a few hundred milliseconds on one route: the P95
			// rises and is dated, but no single part moves by the 100 ms mark, so no
			// tier is dated and no origin is named. That is what the golden file
			// records; it is not a pass.
			if res.Facts.Onset.Status != "found" {
				t.Errorf("demo onset = %+v", res.Facts.Onset)
			}
			if res.Origin.Status == OriginNamed {
				t.Errorf("demo named an origin: %+v", res.Origin)
			}
			if res.Steps[3].Readiness != PartlyReady {
				t.Errorf("step 3 = %q without IIS", res.Steps[3].Readiness)
			}
		}},
		{"no-incident", func(t *testing.T) Input {
			in := gen(genCfg{seed: 11, ref: ref, inc: inc, perMin: 30, iis: true})
			in.ReleasesEntered = true
			return in
		}, func(t *testing.T, res Result) {
			for _, s := range res.Suspects {
				if s.Group != GroupQ && (s.Colour == Red || s.Colour == Orange) {
					t.Errorf("%s is %s on a day with no incident", s.ID, s.Colour)
				}
			}
			if res.Origin.Status == OriginNamed {
				t.Errorf("origin named on a day with no incident: %+v", res.Origin)
			}
		}},
		{"sql-pool-exhausted", func(t *testing.T) Input {
			in := gen(genCfg{seed: 12, ref: ref, inc: inc, perMin: 30, iis: true,
				mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 450}}})
			lim := 10
			in.PoolLimit = &lim
			in.ReleasesEntered = true
			return in
		}, func(t *testing.T, res Result) {
			s := findSuspect(res, "S3")
			if s == nil || s.Colour != Red {
				t.Errorf("S3 = %+v, want red", s)
			}
			if res.Origin.Tier != "c" {
				t.Errorf("origin tier = %q, want c", res.Origin.Tier)
			}
			if res.Steps[10].Readiness == CannotTestYet {
				t.Errorf("step 10 = %q while the origin is named", res.Steps[10].Readiness)
			}
		}},
		{"missing-iis-logs", func(t *testing.T) Input {
			// An outside-time change happened but no IIS file was imported, so it
			// cannot be seen; the audit parts show nothing.
			in := gen(genCfg{seed: 13, ref: ref, inc: inc, perMin: 30, iis: false,
				mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 300}}})
			in.ReleasesEntered = false
			return in
		}, func(t *testing.T, res Result) {
			if res.Steps[3].Readiness != PartlyReady {
				t.Errorf("step 3 = %q", res.Steps[3].Readiness)
			}
			for _, id := range []string{"S1", "S2"} {
				s := findSuspect(res, id)
				if s == nil || s.Result == ResContra || s.Colour == Green {
					t.Errorf("%s = %+v: missing IIS must not rule it out", id, s)
				}
			}
			if res.Confidence.Word == ConfidenceHigh || res.Confidence.Word == ConfidenceMedium {
				t.Errorf("confidence %q without IIS", res.Confidence.Word)
			}
		}},
		{"gap-in-data", func(t *testing.T) Input {
			in := gen(genCfg{seed: 14, ref: ref, inc: inc, perMin: 30, iis: true,
				mods: []mod{{from: at(10, 20), to: at(11, 30), addC: 400}},
				gaps: []Window{{at(10, 50), at(10, 58)}}})
			in.ReleasesEntered = true
			return in
		}, func(t *testing.T, res Result) {
			if len(res.Steps[2].UntestedSpans) == 0 {
				t.Errorf("no untested span on step 2")
			}
			if res.Steps[1].Readiness == Ready {
				t.Errorf("step 1 is Ready with a gap in the data")
			}
		}},
	}
}

func findSuspect(res Result, id string) *Suspect {
	for i := range res.Suspects {
		if res.Suspects[i].ID == id {
			return &res.Suspects[i]
		}
	}
	return nil
}

func TestGolden(t *testing.T) {
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			res := Analyze(c.build(t))
			c.check(t, res)
			assertWording(t, c.name, res)
			// No golden case imports a lane, so no suspect that needs one may be
			// anything but "Cannot test yet" / grey (A5, A6).
			laneNeeded := map[string]bool{}
			for _, e := range Catalog() {
				laneNeeded[e.ID] = e.LaneRequired
			}
			for _, s := range res.Suspects {
				if laneNeeded[s.ID] && (s.Result != ResCannotTest || s.Colour != Grey) {
					t.Errorf("%s = %s/%s without its own data: %s", s.ID, s.Result, s.Colour, s.Why)
				}
			}
			if res.Version != Version {
				t.Errorf("version = %q", res.Version)
			}
			goldenCheck(t, c.name, res)
		})
	}
}
