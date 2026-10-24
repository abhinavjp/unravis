package ledger

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func pct(v float64) *float64 { return &v }

// strongFacts is the roadmap §8.6 positive demo: three independent episodes,
// a unique join, a stated 3-of-3 presence rate, named alternatives, timezone
// recorded and no confounder.
func strongFacts() Facts {
	return Facts{
		PresentK:        3,
		TotalN:          3,
		Counterexamples: []string{},
		Alternatives: []Alternative{
			{Text: "Client-side wait for a pooled connection", Status: AltOpen},
			{Text: "Scheduled index rebuild", Status: AltRuledOut},
		},
		JoinAmbiguousPct: pct(0),
		TimezoneRecorded: true,
	}
}

var strongEvidence = []string{"ep:2026-10-21t09", "ep:2026-10-22t14", "ep:2026-10-23t11"}

const demoText = "Audit-create stalls coincide with a SQL lock wait on the audit table"

func newMem(t *testing.T) Store {
	t.Helper()
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustCreate(t *testing.T, s Store, text string) Hypothesis {
	t.Helper()
	h, err := s.Create(text)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func guardReasons(t *testing.T, err error) []string {
	t.Helper()
	var ge *GuardError
	if !errors.As(err, &ge) {
		t.Fatalf("want *GuardError, got %v", err)
	}
	return ge.Reasons
}

func hasReason(reasons []string, sub string) bool {
	for _, r := range reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func TestCreateStartsOpenWithNoFacts(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, "  "+demoText+"  ")
	if h.State != StateOpen || h.Text != demoText {
		t.Fatalf("got %+v", h)
	}
	if len(h.ID) != 12 || strings.Trim(h.ID, "0123456789abcdef") != "" {
		t.Fatalf("id %q is not 12 hex", h.ID)
	}
	if !reflect.DeepEqual(h.Facts, Facts{Counterexamples: []string{}, Alternatives: []Alternative{}}) {
		t.Fatalf("facts = %+v", h.Facts)
	}
	if len(h.EvidenceIDs) != 0 || len(h.History) != 0 || len(h.Guards) == 0 {
		t.Fatalf("got %+v", h)
	}
	if _, err := s.Create("   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty text: %v", err)
	}
}

func TestPositiveDemoCanBeMarkedStrong(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	if g := Guards(strongFacts(), strongEvidence); len(g) != 0 {
		t.Fatalf("guards = %q", g)
	}
	got, err := s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: strongEvidence, Facts: strongFacts()})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateStrong || len(got.Guards) != 0 || got.Guards == nil {
		t.Fatalf("got state %q guards %#v", got.State, got.Guards)
	}
}

func TestNegativeDemoOneVolleyIsBlocked(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	f := strongFacts()
	f.PresentK, f.TotalN = 1, 1 // the 43 requests of one volley are one episode
	_, err := s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: strongEvidence, Facts: f})
	reasons := guardReasons(t, err)
	if !hasReason(reasons, "1 independent episode;") {
		t.Fatalf("reasons = %q", reasons)
	}
	list, _ := s.List()
	if list[0].State != StateOpen || list[0].Facts.TotalN != 0 {
		t.Fatalf("stored entry changed: %+v", list[0])
	}
	if !hasReason(list[0].Guards, "0 independent episodes") {
		t.Fatalf("list guards = %q", list[0].Guards)
	}
}

// R8-B7: three episodes where the event merely overlaps in time, no
// alternatives and no stated presence rate must not reach strong correlation.
func TestNoProofByOverlap(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	f := Facts{
		TotalN:           0, // presence rate not stated
		Alternatives:     nil,
		JoinAmbiguousPct: pct(0),
		TimezoneRecorded: true,
		OverlapOnly:      true,
	}
	_, err := s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: strongEvidence, Facts: f})
	reasons := guardReasons(t, err)
	for _, want := range []string{
		"temporal overlap alone never reaches strong correlation",
		"No alternative explanation is named",
		"The presence rate is not stated",
	} {
		if !hasReason(reasons, want) {
			t.Errorf("missing %q in %q", want, reasons)
		}
	}
	// Even with the episode count stated, overlap alone still blocks.
	f.TotalN, f.PresentK = 3, 3
	f.Alternatives = strongFacts().Alternatives
	_, err = s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: strongEvidence, Facts: f})
	if r := guardReasons(t, err); len(r) != 1 || !hasReason(r, "temporal overlap alone") {
		t.Fatalf("reasons = %q", r)
	}
}

func TestEachGuard(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*Facts, *[]string)
		reason string
	}{
		{"coverage gap", func(f *Facts, _ *[]string) { f.CoverageGap = true }, "The evidence window has a coverage gap."},
		{"two episodes", func(f *Facts, _ *[]string) { f.TotalN, f.PresentK = 2, 2 }, "2 independent episodes; strong correlation needs at least 3"},
		{"rate below minimum", func(f *Facts, _ *[]string) {
			f.TotalN, f.PresentK = 3, 2
			f.Counterexamples = []string{"episode 2 had no lock wait"}
		}, "The presence rate is 2 of 3, below the minimum"},
		{"k above n", func(f *Facts, _ *[]string) { f.PresentK = 4 }, "The presence rate 4 of 3 is not valid."},
		{"counterexamples missing", func(f *Facts, _ *[]string) { f.TotalN, f.PresentK = 5, 4 }, "0 counterexample(s) listed, but 1 independent episode lacked the linked event"},
		{"blank counterexample", func(f *Facts, _ *[]string) {
			f.TotalN, f.PresentK = 5, 4
			f.Counterexamples = []string{"  "}
		}, "0 counterexample(s) listed"},
		{"no alternative", func(f *Facts, _ *[]string) { f.Alternatives = nil }, "No alternative explanation is named"},
		{"alternative without text", func(f *Facts, _ *[]string) { f.Alternatives[1].Text = " " }, "Alternative explanation 2 has no text."},
		{"alternative bad status", func(f *Facts, _ *[]string) { f.Alternatives[0].Status = "proven" }, "Alternative explanation 1 has no valid status"},
		{"alternative empty status", func(f *Facts, _ *[]string) { f.Alternatives[0].Status = "" }, "Alternative explanation 1 has no valid status"},
		{"join not stated", func(f *Facts, _ *[]string) { f.JoinAmbiguousPct = nil }, "The join class is not stated"},
		{"join ambiguous", func(f *Facts, _ *[]string) { f.JoinAmbiguousPct = pct(10.5) }, "The join is ambiguous for 10.5% of rows, above the 10% limit."},
		{"timezone", func(f *Facts, _ *[]string) { f.TimezoneRecorded = false }, "The source's timezone mapping is not recorded."},
		{"confounder", func(f *Facts, _ *[]string) { f.ConfounderOverlap = true }, "A known confounder"},
		{"overlap only", func(f *Facts, _ *[]string) { f.OverlapOnly = true }, "temporal overlap alone never reaches strong correlation"},
		{"no evidence", func(_ *Facts, ev *[]string) { *ev = nil }, "No evidence references are linked."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := strongFacts()
			ev := append([]string(nil), strongEvidence...)
			c.edit(&f, &ev)
			g := Guards(f, ev)
			if len(g) != 1 || !strings.Contains(g[0], c.reason) {
				t.Fatalf("guards = %q, want exactly one containing %q", g, c.reason)
			}
		})
	}
}

func TestGuardBoundaries(t *testing.T) {
	ok := func(k, n int) bool {
		f := strongFacts()
		f.PresentK, f.TotalN = k, n
		for i := 0; i < n-k; i++ {
			f.Counterexamples = append(f.Counterexamples, fmt.Sprintf("episode %d had no lock wait", i+1))
		}
		return len(Guards(f, strongEvidence)) == 0
	}
	for _, c := range []struct {
		k, n int
		want bool
	}{{3, 3, true}, {4, 4, true}, {3, 4, false}, {4, 5, true}, {3, 5, false}, {8, 10, true}, {7, 10, false}} {
		if got := ok(c.k, c.n); got != c.want {
			t.Errorf("%d of %d: allowed = %v, want %v", c.k, c.n, got, c.want)
		}
	}
	f := strongFacts()
	f.JoinAmbiguousPct = pct(10)
	if g := Guards(f, strongEvidence); len(g) != 0 {
		t.Errorf("10%% ambiguous should pass: %q", g)
	}
}

func TestGuardsDeterministicOrder(t *testing.T) {
	g := Guards(Facts{CoverageGap: true, OverlapOnly: true, ConfounderOverlap: true}, nil)
	want := []string{
		"The evidence window has a coverage gap.",
		"0 independent episodes; strong correlation needs at least 3 (a volley counts as one episode).",
		"The presence rate is not stated (k of n episodes with the linked event present).",
		"No alternative explanation is named; name every plausible alternative with its status.",
		"The join class is not stated (share of rows with an ambiguous join).",
		"The source's timezone mapping is not recorded.",
		"A known confounder (a scheduled job or a recycle event) overlaps the episodes.",
		"The support is temporal overlap alone; temporal overlap alone never reaches strong correlation.",
		"No evidence references are linked.",
	}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("got\n%q\nwant\n%q", g, want)
	}
}

func TestGuardsOnlyBlockStrong(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	f := Facts{CoverageGap: true, OverlapOnly: true}
	for _, st := range []string{StateSupported, StateContradicted, StateNotTestable, StateOpen} {
		got, err := s.Update(h.ID, Input{Text: demoText, State: st, Facts: f})
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		if got.State != st || len(got.Guards) == 0 {
			t.Fatalf("%s: got %+v", st, got)
		}
	}
}

func TestInvalidState(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	for _, st := range []string{"", "proven_cause", "Open", "strong"} {
		if _, err := s.Update(h.ID, Input{Text: demoText, State: st}); !errors.Is(err, ErrInvalid) {
			t.Errorf("state %q: %v", st, err)
		}
	}
	if _, err := s.Update("nope", Input{Text: demoText, State: StateOpen}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id: %v", err)
	}
}

func TestIdentifierRejection(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	bad := []string{
		"request 507f1f77bcf86cd799439011 stalled",
		"reported by ops.team@example.com",
		"from host 10.20.30.40 only",
		"from host 2001:db8::1 only",
		"from fe80:0:0:0:200:f8ff:fe21:67cf",
		"see http://intranet/req/1",
		"see HTTPS://example.com/x",
	}
	for _, b := range bad {
		if _, err := s.Create(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("create %q: %v", b, err)
		}
		inputs := map[string]Input{
			"text":           {Text: b, State: StateOpen},
			"notes":          {Text: demoText, State: StateOpen, Notes: b},
			"counterexample": {Text: demoText, State: StateOpen, Facts: Facts{Counterexamples: []string{b}}},
			"alternative":    {Text: demoText, State: StateOpen, Facts: Facts{Alternatives: []Alternative{{Text: b, Status: AltOpen}}}},
		}
		for field, in := range inputs {
			if _, err := s.Update(h.ID, in); !errors.Is(err, ErrInvalid) {
				t.Errorf("%s %q: %v", field, b, err)
			}
		}
	}
	for _, id := range []string{"Ep:1", "-ep", "ep 1", "ep/1", strings.Repeat("a", 121), ""} {
		if _, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, EvidenceIDs: []string{id}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("evidence %q: %v", id, err)
		}
	}
	// Plain text with versions, times and short hex is fine.
	okText := "Mongo 7.0.16 stall at 09:14:05 on 2026-10-21, build abc123, ref p2:b1:0a1b2c3d4e5f6071"
	if _, err := s.Update(h.ID, Input{Text: okText, State: StateOpen, EvidenceIDs: []string{strings.Repeat("z", 120)}}); err != nil {
		t.Errorf("ok text: %v", err)
	}
}

func TestLengthLimits(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	long := strings.Repeat("x", MaxTextLen+1)
	if _, err := s.Create(long); !errors.Is(err, ErrInvalid) {
		t.Errorf("long text: %v", err)
	}
	if _, err := s.Create(strings.Repeat("é", MaxTextLen)); err != nil {
		t.Errorf("max runes: %v", err)
	}
	many := make([]string, MaxListLen+1)
	for i := range many {
		many[i] = fmt.Sprintf("ep:%d", i)
	}
	if _, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, EvidenceIDs: many}); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many evidence ids: %v", err)
	}
	if _, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, Facts: Facts{Counterexamples: many}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many counterexamples: %v", err)
	}
	if _, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, Facts: Facts{TotalN: -1}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative count: %v", err)
	}
	for _, bad := range []float64{101, 100.01, -0.5, -1, math.NaN(), math.Inf(1)} {
		if _, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, Facts: Facts{JoinAmbiguousPct: pct(bad)}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("join pct %v: %v", bad, err)
		}
	}
	for _, ok := range []float64{0, 100} {
		if _, err := s.Update(h.ID, Input{Text: demoText, State: StateOpen, Facts: Facts{JoinAmbiguousPct: pct(ok)}}); err != nil {
			t.Errorf("join pct %v: %v", ok, err)
		}
	}
}

func TestMaxHypotheses(t *testing.T) {
	s := newMem(t)
	var first Hypothesis
	for i := 0; i < MaxHypotheses; i++ {
		h := mustCreate(t, s, fmt.Sprintf("hypothesis %d", i))
		if i == 0 {
			first = h
		}
	}
	if _, err := s.Create("one more"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over cap: %v", err)
	}
	if _, err := s.Reuse(first.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reuse over cap: %v", err)
	}
}

func TestReuseRestartsAtOpen(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	if _, err := s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: strongEvidence, Facts: strongFacts(), Notes: "owner confirmed"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reuse(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == h.ID || r.Text != demoText || r.State != StateOpen || r.Notes != "" || len(r.EvidenceIDs) != 0 {
		t.Fatalf("reused = %+v", r)
	}
	if r.Facts.TotalN != 0 || len(r.Facts.Alternatives) != 0 {
		t.Fatalf("facts carried over: %+v", r.Facts)
	}
	if len(r.History) != 1 || r.History[0].State != StateStrong || r.History[0].Note != "earlier incident" || r.History[0].At.IsZero() {
		t.Fatalf("history = %+v", r.History)
	}
	if len(r.Guards) == 0 {
		t.Fatal("prior strong state must not count as support")
	}
	// The reused entry cannot be marked strong on its history alone.
	if _, err := s.Update(r.ID, Input{Text: demoText, State: StateStrong}); err == nil {
		t.Fatal("reused entry marked strong without facts")
	}
	list, _ := s.List()
	if len(list) != 2 || list[0].State != StateStrong {
		t.Fatalf("list = %+v", list)
	}
	if _, err := s.Reuse("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reuse missing: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newMem(t)
	a := mustCreate(t, s, "first")
	b := mustCreate(t, s, "second")
	c := mustCreate(t, s, "third")
	if err := s.Delete(b.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != c.ID {
		t.Fatalf("list = %+v", list)
	}
	if err := s.Delete(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestReturnedValuesAreCopies(t *testing.T) {
	s := newMem(t)
	h := mustCreate(t, s, demoText)
	in := Input{Text: demoText, State: StateSupported, EvidenceIDs: []string{"ep:1"}, Facts: strongFacts()}
	got, err := s.Update(h.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.EvidenceIDs[0] = "ep:changed"
	in.Facts.Alternatives[0].Text = "changed"
	*in.Facts.JoinAmbiguousPct = 50
	got.Facts.Alternatives[1].Text = "changed too"
	list, _ := s.List()
	if list[0].EvidenceIDs[0] != "ep:1" || list[0].Facts.Alternatives[0].Text == "changed" ||
		list[0].Facts.Alternatives[1].Text == "changed too" || *list[0].Facts.JoinAmbiguousPct != 0 {
		t.Fatalf("stored entry aliased caller data: %+v", list[0])
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "ledger.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := mustCreate(t, s, demoText)
	if _, err := s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: strongEvidence, Facts: strongFacts(), Notes: "owner confirmed by other means"}); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reuse(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	gone := mustCreate(t, s, "to delete")
	if err := s.Delete(gone.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := s.List()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s2.List()
	if len(after) != 2 || after[1].ID != r.ID {
		t.Fatalf("after = %+v", after)
	}
	for i := range before {
		b, a := before[i], after[i]
		if !b.CreatedAt.Equal(a.CreatedAt) || !b.UpdatedAt.Equal(a.UpdatedAt) {
			t.Fatalf("times differ: %v vs %v", b, a)
		}
		b.CreatedAt, b.UpdatedAt = a.CreatedAt, a.UpdatedAt
		for j := range b.History {
			b.History[j].At = a.History[j].At
		}
		if !reflect.DeepEqual(b, a) {
			t.Fatalf("round trip differs:\n%+v\n%+v", b, a)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `"guards"`) && strings.Contains(string(data), "independent episode") {
		t.Fatalf("guard reasons persisted instead of computed: %s", data)
	}
}

func TestCorruptFileRefused(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"garbage":       "{not json",
		"wrong version": `{"version":9,"hypotheses":[]}`,
		"bad state":     `{"version":1,"hypotheses":[{"id":"abc","text":"x","state":"proven_cause"}]}`,
		"duplicate id":  `{"version":1,"hypotheses":[{"id":"a","text":"x","state":"open"},{"id":"a","text":"y","state":"open"}]}`,
		"unknown field": `{"version":1,"hypotheses":[],"rows":[1]}`,
		"trailing":      `{"version":1,"hypotheses":[]} {}`,
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if s, err := Open(path); err == nil || s != nil {
			t.Errorf("%s: opened a corrupt file", name)
		}
		got, _ := os.ReadFile(path)
		if string(got) != content {
			t.Errorf("%s: file was modified", name)
		}
	}
	missing := filepath.Join(dir, "missing.json")
	s, err := Open(missing)
	if err != nil {
		t.Fatal(err)
	}
	if l, _ := s.List(); len(l) != 0 {
		t.Fatalf("missing file list = %v", l)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("open must not create the file before a change")
	}
}

func TestGuardErrorMessage(t *testing.T) {
	err := &GuardError{Reasons: []string{"a", "b"}}
	if err.Error() != "blocked: a; b" {
		t.Fatal(err.Error())
	}
}

func TestConcurrentAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 16
	const perWorker = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker*4)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				text := fmt.Sprintf("worker %d hypothesis %d", w, i)
				h, err := s.Create(text)
				if err != nil {
					errs <- err
					return
				}
				state := StateSupported
				facts := Facts{}
				ev := []string(nil)
				if i%2 == 0 {
					state, facts, ev = StateStrong, strongFacts(), strongEvidence
				}
				if _, err := s.Update(h.ID, Input{Text: text, State: state, EvidenceIDs: ev, Facts: facts}); err != nil {
					errs <- err
				}
				if _, err := s.List(); err != nil {
					errs <- err
				}
				if i%4 == 1 {
					if err := s.Delete(h.ID); err != nil {
						errs <- err
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	list, _ := s.List()
	want := workers * (perWorker - perWorker/4)
	if len(list) != want {
		t.Fatalf("len = %d, want %d", len(list), want)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if l2, _ := s2.List(); len(l2) != want {
		t.Fatalf("reopened len = %d, want %d", len(l2), want)
	}
	ids := map[string]bool{}
	for _, h := range list {
		if ids[h.ID] {
			t.Fatalf("duplicate id %s", h.ID)
		}
		ids[h.ID] = true
	}
}

func TestEvidenceIDRejectsObjectID(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Create("a hypothesis")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update(h.ID, Input{Text: "a hypothesis", State: StateOpen, EvidenceIDs: []string{"req:507f1f77bcf86cd799439011"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}
