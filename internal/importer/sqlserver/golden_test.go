package sqlserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
)

// R8-B1 golden alignment. One synthetic event (a 35 s lock wait on sql-a,
// Pacific/Auckland, NZST = UTC+12 in June 2001) appears as:
//   - an XE wait_info row with timestamp_utc 2001-06-15 02:00:35 (UTC),
//   - an ERRORLOG line at 2001-06-15 14:00:35.00 (server-local),
//   - a PDH sample at 06/15/2001 14:00:35.000 with header bias -720,
//   - an audit interval [01:59:58Z, 02:00:40Z].
// All must align. Each mutation shifts one source by the host offset (12 h),
// the classic local-as-UTC or UTC-as-local mistake, and must fail.

const (
	goldenXEStamp       = "2001-06-15 02:00:35.0000000"
	goldenXEStampLocal  = "2001-06-15 14:00:35.0000000" // the local wall time written as UTC
	goldenLogStamp      = "2001-06-15 14:00:35.00"
	goldenLogStampAsUTC = "2001-06-15 02:00:35.00" // the UTC wall time written as local
	goldenHostOffset    = 12 * time.Hour
)

type goldenSet struct {
	xe, errorlog, audit []byte
}

func loadGolden(t *testing.T) goldenSet {
	return goldenSet{xe: readFixture(t, "golden/xe.csv"), errorlog: readFixture(t, "golden/ERRORLOG"), audit: readFixture(t, "golden/audit.json")}
}

func auditInterval(t *testing.T, b []byte) join.Interval {
	t.Helper()
	var a struct {
		ID                 string    `json:"id"`
		RequestInitiatedAt time.Time `json:"requestInitiatedAt"`
		CreatedOn          time.Time `json:"createdOn"`
	}
	if err := json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	return join.Interval{ID: a.ID, Start: a.RequestInitiatedAt.UTC(), End: a.CreatedOn.UTC()}
}

func ofKind(evs []evidence.Event, kind string) []evidence.Event {
	var out []evidence.Event
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// alignGolden parses every source with the production importers and reports
// the first source whose marker event does not uniquely overlap the audit
// interval, or does not line up with the other sources.
func alignGolden(t *testing.T, g goldenSet) error {
	t.Helper()
	iv := auditInterval(t, g.audit)
	xe, _, err := collect(t, NewXE(), g.xe, testOpts(t))
	if err != nil {
		return fmt.Errorf("xe parse: %w", err)
	}
	el, _, err := collect(t, NewErrorlog(), g.errorlog, testOpts(t))
	if err != nil {
		return fmt.Errorf("errorlog parse: %w", err)
	}
	sources := map[string][]evidence.Event{
		"xe":       ofKind(xe, KindWait),
		"errorlog": ofKind(el, KindIOStall),
	}
	marks := map[string]time.Time{}
	for name, evs := range sources {
		m := join.Overlap([]join.Interval{iv}, evs, goldenTol, nil)[0]
		if m.Class != evidence.JoinUnique {
			return fmt.Errorf("%s: join class %s with the audit interval", name, m.Class)
		}
		for _, e := range evs {
			if e.EvidenceID == m.Candidates[0] {
				marks[name] = e.End
			}
		}
	}
	if d := marks["xe"].Sub(marks["errorlog"]); d > goldenTol.Total() || d < -goldenTol.Total() {
		return fmt.Errorf("xe and errorlog marks differ by %v", d)
	}
	return nil
}

func TestGoldenAlignment(t *testing.T) {
	g := loadGolden(t)
	if err := alignGolden(t, g); err != nil {
		t.Fatalf("golden fixture does not align: %v", err)
	}

	// The aligned instant itself, so a matching shift in every source cannot pass.
	xe, _, _ := collect(t, NewXE(), g.xe, testOpts(t))
	el, _, _ := collect(t, NewErrorlog(), g.errorlog, testOpts(t))
	want := time.Date(2001, 6, 15, 2, 0, 35, 0, time.UTC)
	var lock evidence.Event
	for _, e := range ofKind(xe, KindWait) {
		if e.Attrs["wait_type"] == "LCK_M_X" {
			lock = e
		}
	}
	if !lock.End.Equal(want) || !lock.Start.Equal(want.Add(-35*time.Second)) {
		t.Errorf("xe lock wait = [%v, %v], want end %v and 35 s long", lock.Start, lock.End, want)
	}
	if io := ofKind(el, KindIOStall); len(io) != 1 || !io[0].Start.Equal(want) {
		t.Errorf("errorlog io-stall = %+v, want one at %v", io, want)
	}

	st := XEStatement(auditInterval(t, g.audit), xe, evidence.SourceQuality{}, goldenTol)
	if st.Text != evidence.OverlapUnique("system_health", "lock wait", 35) || st.Label != audit.EvidenceCorrelation {
		t.Errorf("golden statement = %+v", st)
	}
}

func TestGoldenMutationsFail(t *testing.T) {
	base := loadGolden(t)
	replace := func(t *testing.T, b []byte, old, new string) []byte {
		t.Helper()
		if bytes.Count(b, []byte(old)) != 1 {
			t.Fatalf("mutation anchor %q not found exactly once", old)
		}
		return bytes.Replace(b, []byte(old), []byte(new), 1)
	}
	cases := map[string]func(t *testing.T, g *goldenSet){
		"xe local written as UTC": func(t *testing.T, g *goldenSet) {
			g.xe = replace(t, g.xe, goldenXEStamp, goldenXEStampLocal)
		},
		"errorlog UTC written as local": func(t *testing.T, g *goldenSet) {
			g.errorlog = replace(t, g.errorlog, goldenLogStamp, goldenLogStampAsUTC)
		},
		"pdh shifted by the host offset": func(t *testing.T, g *goldenSet) {
			t.Skip("pdh.go waits for P6 (wincounters.NewImporter); testdata/golden/pdh.csv is ready for it")
		},
		"audit shifted by the host offset": func(t *testing.T, g *goldenSet) {
			iv := auditInterval(t, g.audit)
			g.audit = []byte(fmt.Sprintf(`{"id":%q,"requestInitiatedAt":%q,"createdOn":%q}`, iv.ID,
				iv.Start.Add(goldenHostOffset).Format(time.RFC3339), iv.End.Add(goldenHostOffset).Format(time.RFC3339)))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			g := base
			mutate(t, &g)
			if err := alignGolden(t, g); err == nil {
				t.Fatalf("mutation %q still aligns; the time-alignment check is not sensitive to a %v shift", name, goldenHostOffset)
			}
		})
	}
}

// A host picked with the wrong time zone is the same mistake seen from the
// other side: the ERRORLOG's own UTC adjustment line refuses the file.
func TestGoldenWrongHostZoneRefused(t *testing.T) {
	o := testOpts(t)
	o.Host.TZ = time.UTC
	_, _, err := collect(t, NewErrorlog(), loadGolden(t).errorlog, o)
	var q *importer.QuarantineError
	if !errors.As(err, &q) || q.Reason != importer.ReasonTZMismatch {
		t.Fatalf("errorlog with a UTC host and a +12:00 adjustment line: got %v, want tz-mismatch", err)
	}
}
