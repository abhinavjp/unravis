package evidence

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/ledger"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	t0      = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	t1      = time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)
	testNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	// A connection string as a source id: it must never reach an export.
	leakySource = "mongodb://admin:pw@10.1.2.3:27017/audit"
)

func intp(v int) *int { return &v }

func stall(user int, ctrl string, at time.Time) audit.Stall {
	return audit.Stall{SourceID: leakySource, ID: "507f1f77bcf86cd799439011", Controller: ctrl, Action: "Get",
		InitiatedAt: at, ObservedMs: 61000, UserID: intp(user), Episode: 1}
}

// fixture builds a report holding a known user id 4242, an IP, a URL, an
// email and an ObjectId-like string in a controller name.
func fixture() audit.PatternReport {
	ctrl := "Orders507f1f77bcf86cd799439011"
	stalls := []audit.Stall{
		stall(4242, ctrl, t0.Add(time.Minute)),
		stall(4242, ctrl, t0.Add(time.Minute+300*time.Millisecond)),
		stall(17, ctrl, t0.Add(2*time.Minute)),
	}
	return audit.PatternReport{
		Params: audit.PatternParams{StallMs: 45000, EpisodeGapSeconds: 600, AuditStallMs: 5000, AuditStallShare: 0.3, VolleyWindowMs: 1000},
		Stalls: stalls,
		Episodes: []audit.Episode{{Index: 1, Start: t0, End: t0.Add(3 * time.Minute), Requests: 3, Users: 2, MaxMs: 61000,
			Operations: []string{ctrl + ".Get"}}},
		Volleys: []audit.Volley{{Episode: 1, UserID: 4242, Requests: 2, FirstStart: t0.Add(time.Minute)}},
		AuditStalls: []audit.AuditStall{{SourceID: leakySource, ID: "507f1f77bcf86cd799439012", Controller: ctrl, Action: "Post",
			InitiatedAt: t0, ObservedMs: 9000, AuditCreateTimeMs: 7300, Share: 0.81}},
		Recurrence: audit.RecurrenceSingleEpisode,
		Findings: []audit.Finding{
			{Rule: "B1", Label: audit.EvidenceSymptom, Headline: "3 requests took 45 s or longer.", N: 3, LowN: true, EvidenceID: "p2:b1:0011223344556677"},
			{Rule: "B2", Label: audit.EvidenceSymptom, Headline: "Seen once: single episode, 3 requests.", N: 3, LowN: true, EvidenceID: "p2:b2:0011223344556677"},
			{Rule: "B6", Label: audit.EvidenceSymptom, Headline: "Audit create took 7.3 s as measured from the client side.", N: 1, LowN: true, EvidenceID: "p2:b6:0011223344556677"},
			{Rule: "B12", Label: audit.EvidenceCorrelation, Headline: "One user started 2 requests of 45 s or longer within 1 s of each other.", N: 2, LowN: true, EvidenceID: "p2:b12:0011223344556677"},
		},
		Quality:  audit.PatternQuality{Incomplete: 4, Pending: 0, Invalid: 25},
		Coverage: &audit.PatternCoverage{Run: false, Note: "Coverage did not run (see https://x.example/secret?a=1 or ask ops@corp.example at 10.1.2.3). Run Coverage first."},
		Provenance: []audit.Provenance{
			{SourceID: leakySource, Window: "current", Start: t0.Add(30 * time.Minute), End: t1, Completeness: audit.RanToEnd,
				Note: "fallback via https://x.example/secret?a=1"},
			{SourceID: leakySource, Window: "current", Start: t0, End: t1.Add(-time.Hour), Completeness: audit.Partial, PartialReason: "rowcap"},
		},
		Warnings: []string{"user 4242 from 10.1.2.3 hit https://x.example/secret?a=1 ops@corp.example"},
	}
}

func baseInput() Input {
	return Input{
		Report:       fixture(),
		SourceLabels: map[string]string{leakySource: "Audit (sandbox)"},
		Ledger: []ledger.Hypothesis{{
			ID: "h1", Text: "User 4242 at 10.1.2.3 hit https://x.example/secret?a=1, mail ops@corp.example, row 507f1f77bcf86cd799439011, 3 stalls.",
			State: ledger.StateSupported, EvidenceIDs: []string{"p2:b1:0011223344556677", "p2:zz:ffff", "p2:yy:eeee"},
			Notes: "secret note about tenant Acme", Facts: ledger.Facts{Counterexamples: []string{"episode at 4242"}},
		}},
		ToolVersion: "0.13.0-test",
		Now:         testNow,
		Key:         bytes.Repeat([]byte{0xA5}, 32),
	}
}

func mustBuild(t *testing.T, in Input) (Export, []byte) {
	t.Helper()
	e, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := Canonical(e)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	return e, raw
}

func assertAbsent(t *testing.T, raw []byte, needles ...string) {
	t.Helper()
	low := strings.ToLower(string(raw))
	for _, n := range needles {
		if strings.Contains(low, strings.ToLower(n)) {
			t.Errorf("export contains %q:\n%s", n, raw)
		}
	}
}

var forbidden = []string{"4242", "10.1.2.3", "x.example", "secret", "ops@corp.example", "507f1f77bcf86cd799439011",
	"Orders", "mongodb://", "admin", "27017", "tenant", "Acme"}

func TestAllowlistDefaultExcludesIdentifiers(t *testing.T) {
	in := baseInput()
	e, raw := mustBuild(t, in)
	assertAbsent(t, raw, forbidden...)
	if containsProven(string(raw)) {
		t.Errorf("export contains \"proven\"")
	}
	if e.Body.Pseudonyms != nil || e.Body.Ledger != nil {
		t.Fatalf("defaults must omit pseudonyms and ledger: %+v", e.Body)
	}
	if bytes.Contains(raw, []byte(`"pseudonyms"`)) || bytes.Contains(raw, []byte(`"ledger"`)) {
		t.Fatalf("defaults must not print pseudonym or ledger keys")
	}
	if len(e.Body.Records) != 4 {
		t.Fatalf("records = %d, want 4", len(e.Body.Records))
	}
	for _, r := range e.Body.Records {
		if r.Source != "Audit (sandbox)" || r.Provenance.Ref != "Audit (sandbox)" {
			t.Errorf("source = %q ref = %q", r.Source, r.Provenance.Ref)
		}
	}
	if !bytes.HasSuffix(raw, []byte("}\n")) || !bytes.Contains(raw, []byte("\n  \"manifest\"")) {
		t.Errorf("canonical form is not 2-space indented with trailing newline")
	}
}

func TestAllowlistWithPseudonymsAndLedger(t *testing.T) {
	in := baseInput()
	in.Options = Options{IncludePseudonyms: true, IncludeLedger: true}
	e, raw := mustBuild(t, in)
	assertAbsent(t, raw, forbidden...)
	if containsProven(string(raw)) {
		t.Errorf("export contains \"proven\"")
	}
	assertAbsent(t, raw, hex.EncodeToString(in.Key), string(in.Key))
	keyJSON, _ := json.Marshal(in.Key) // base64 form
	assertAbsent(t, raw, strings.Trim(string(keyJSON), `"`))

	// Two users with small counts: the table is withheld, not printed with "<20" cells.
	if e.Body.Pseudonyms != nil || !hasStatement(e, "Pseudonym table withheld: a cell was below 20.") {
		t.Fatalf("pseudonyms = %+v, statements = %q", e.Body.Pseudonyms, e.Manifest.Masking.Statements)
	}
	if len(e.Body.Ledger) != 1 {
		t.Fatalf("ledger = %+v", e.Body.Ledger)
	}
	l := e.Body.Ledger[0]
	if !strings.Contains(l.Text, "[removed]") || !strings.Contains(l.Text, "<20 stalls") {
		t.Errorf("ledger text not cleaned: %q", l.Text)
	}
	want := []string{"p2:b1:0011223344556677", notInExport}
	if strings.Join(l.EvidenceIDs, "|") != strings.Join(want, "|") {
		t.Errorf("evidence ids = %v, want %v", l.EvidenceIDs, want)
	}
}

func TestSourceFallsBackToGeneric(t *testing.T) {
	in := baseInput()
	in.SourceLabels = nil
	e, _ := mustBuild(t, in)
	if e.Body.Records[0].Source != genericSource {
		t.Errorf("no label: source = %q", e.Body.Records[0].Source)
	}
	in = baseInput()
	in.Report.Provenance = append(in.Report.Provenance, audit.Provenance{SourceID: "other", Start: t0, End: t1})
	e, _ = mustBuild(t, in)
	if e.Body.Records[0].Source != genericSource {
		t.Errorf("two sources: source = %q", e.Body.Records[0].Source)
	}
	in = baseInput()
	in.SourceLabels = map[string]string{leakySource: "mongodb://10.1.2.3/audit"}
	e, _ = mustBuild(t, in)
	if e.Body.Records[0].Source != genericSource {
		t.Errorf("label that is a connection string: source = %q", e.Body.Records[0].Source)
	}
}

func TestRecordFields(t *testing.T) {
	e, _ := mustBuild(t, baseInput())
	r := e.Body.Records[0]
	if !r.WindowStart.Equal(t0) || !r.WindowEnd.Equal(t1) {
		t.Errorf("window = %v..%v, want %v..%v", r.WindowStart, r.WindowEnd, t0, t1)
	}
	if r.Version != SchemaVersion || r.JoinClass != "none" || r.Provenance.Kind != "query" || r.Tolerance == "" {
		t.Errorf("record = %+v", r)
	}
	if r.Quality.Threshold != "stall at or above 45 s" || r.Quality.TimezoneRule != timezoneRule || r.Quality.Sampling != "" {
		t.Errorf("quality = %+v", r.Quality)
	}
	if e.Body.Records[1].Quality.Threshold != "" {
		t.Errorf("B2 threshold = %q, want empty", e.Body.Records[1].Quality.Threshold)
	}
	if r.Headline != "<20 requests took 45 s or longer." {
		t.Errorf("B1 headline = %q", r.Headline)
	}
	if h := e.Body.Records[2].Headline; !strings.HasPrefix(h, "Audit create took 7.3 s as measured from the client side.") {
		t.Errorf("B6 headline lost the §6.6 wording: %q", h)
	}
	gaps := strings.Join(r.KnownGaps, " | ")
	for _, want := range []string{"<20 incomplete rows", "25 invalid rows", "Field coverage was not measured", "A read was partial (row cap reached)"} {
		if !strings.Contains(gaps, want) {
			t.Errorf("known gaps missing %q: %s", want, gaps)
		}
	}
	if strings.Contains(gaps, "pending") {
		t.Errorf("zero pending must not print: %s", gaps)
	}
	h, err := recordHash(r)
	if err != nil || h != r.Provenance.Hash || len(h) != 64 {
		t.Errorf("record hash %q, recomputed %q (%v)", r.Provenance.Hash, h, err)
	}
	if e.Manifest.ToolVersion != "0.13.0-test" || !e.Manifest.CreatedAt.Equal(testNow) {
		t.Errorf("manifest = %+v", e.Manifest)
	}
	if !hasStatement(e, "The key that makes pseudonyms is not in this file.") {
		t.Errorf("statements = %q", e.Manifest.Masking.Statements)
	}
}

func hasStatement(e Export, want string) bool {
	for _, s := range e.Manifest.Masking.Statements {
		if s == want {
			return true
		}
	}
	return false
}

func TestWindowError(t *testing.T) {
	in := baseInput()
	in.Report.Provenance = nil
	if _, err := Build(in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no provenance: err = %v, want ErrInvalid", err)
	}
}

func TestBadLabel(t *testing.T) {
	for _, label := range []string{"Proven cause", "proven", "Cause", ""} {
		in := baseInput()
		in.Report.Findings[0].Label = label
		if _, err := Build(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("label %q: err = %v, want ErrInvalid", label, err)
		}
	}
}

func TestUnknownRuleRefused(t *testing.T) {
	in := baseInput()
	in.Report.Findings[0].Rule = "B99"
	if _, err := Build(in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown rule: err = %v", err)
	}
}

func TestVerify(t *testing.T) {
	in := baseInput()
	in.Options = Options{IncludePseudonyms: true, IncludeLedger: true}
	_, raw := mustBuild(t, in)
	if err := Verify(raw); err != nil {
		t.Fatalf("Verify(clean) = %v", err)
	}
	tampered := bytes.Replace(raw, []byte(`"lowN": true`), []byte(`"lowN": false`), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("tamper had no effect")
	}
	if err := Verify(tampered); !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify(tampered) = %v, want ErrInvalid", err)
	}
	extra := bytes.Replace(raw, []byte(`"records": [`), []byte(`"rawUserId": 4242, "records": [`), 1)
	if err := Verify(extra); err == nil {
		t.Errorf("Verify accepted an unknown body field")
	}
	for _, v := range []string{"0", "1", "3"} {
		wrongVersion := bytes.Replace(raw, []byte(`"schemaVersion": 2`), []byte(`"schemaVersion": `+v), 1)
		if bytes.Equal(wrongVersion, raw) {
			t.Fatal("version edit had no effect")
		}
		if err := Verify(wrongVersion); !errors.Is(err, ErrInvalid) {
			t.Errorf("Verify(schema %s edited in) = %v, want ErrInvalid", v, err)
		}
	}
	if err := Verify([]byte("not json")); err == nil {
		t.Errorf("Verify accepted garbage")
	}
}

func TestDeterminism(t *testing.T) {
	in := baseInput()
	in.Options = Options{IncludePseudonyms: true, IncludeLedger: true}
	_, a := mustBuild(t, in)
	_, b := mustBuild(t, baseInputWith(in.Options))
	if !bytes.Equal(a, b) {
		t.Fatalf("same input gave different bytes")
	}
}

func baseInputWith(o Options) Input {
	in := baseInput()
	in.Options = o
	return in
}

func TestMaskCountBoundary(t *testing.T) {
	c := MaskCount(19)
	if !c.Masked || c.Value != nil || c.Text != "<20" {
		t.Errorf("19 = %+v", c)
	}
	c = MaskCount(20)
	if c.Masked || c.Value == nil || *c.Value != 20 || c.Text != "20" {
		t.Errorf("20 = %+v", c)
	}
	if c := MaskCount(0); !c.Masked {
		t.Errorf("0 = %+v", c)
	}
	if c := maskCount64(1 << 40); c.Masked || c.Text != strconv.FormatInt(1<<40, 10) {
		t.Errorf("big = %+v", c)
	}
}

func TestMaskNumbers(t *testing.T) {
	cases := map[string]string{
		"3 stalls and 19 rows":         "<20 stalls and <20 rows",
		"20 rows and 45 s":             "20 rows and 45 s",
		"rule B12 user-3fa9 v2":        "rule B12 user-3fa9 v2",
		"1.5 s at 10:30 on 2026-10-24": "1.5 s at 10:30 on 2026-10-24",
		"5% share, 1,200 rows":         "5% share, 1,200 rows",
		"(7)":                          "(<20)",
		"<20 already":                  "<20 already",
		"":                             "",
		"wait 5 s":                     "wait <20 s", // over-masking a duration is the documented trade-off
	}
	for in, want := range cases {
		if got := maskNumbers(in); got != want {
			t.Errorf("maskNumbers(%q) = %q, want %q", in, got, want)
		}
		if got := maskNumbers(maskNumbers(in)); got != want {
			t.Errorf("maskNumbers not idempotent on %q: %q", in, got)
		}
	}
}

func TestRemoveUserIDs(t *testing.T) {
	ids := map[int]bool{4242: true, 7: true}
	cases := map[string]string{
		"user 4242 and UserId: 99 and user_id=5": "user [removed] and UserId: [removed] and user_id=[removed]",
		"saw 4242 twice, u4242, 42420":           "saw [removed] twice, u[removed], 42420",
		"7 stalls, users 3":                      "[removed] stalls, users 3",
	}
	for in, want := range cases {
		if got := removeUserIDs(in, ids); got != want {
			t.Errorf("removeUserIDs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubText(t *testing.T) {
	in := "see https://x.example/secret?a=1 www.x.example ops@corp.example 10.1.2.3 Orders507f1f77bcf86cd799439011 not proven cause, unproven"
	got := scrubText(in)
	for _, bad := range []string{"x.example", "secret", "ops@", "10.1.2.3", "507f1f77"} {
		if strings.Contains(got, bad) {
			t.Errorf("scrubText left %q in %q", bad, got)
		}
	}
	if containsProven(got) || !strings.Contains(scrubText("provenance kept"), "provenance") {
		t.Errorf("proven handling: %q", got)
	}
	if !strings.Contains(got, "Orders[removed]") {
		t.Errorf("scrubText = %q", got)
	}
}

// TestReidentification is the §8.9 falsification: small cells and a known user
// must not let a reader recover the user from the export.
func TestReidentification(t *testing.T) {
	in := baseInput()
	in.Report.Stalls = []audit.Stall{
		stall(7, "Orders", t0.Add(1*time.Minute)),
		stall(7, "Orders", t0.Add(2*time.Minute)),
		stall(7, "Orders", t0.Add(3*time.Minute)),
		stall(9, "Orders", t0.Add(4*time.Minute)),
	}
	in.Report.Findings[0].N = 4
	in.Report.Findings[1].N = 4
	in.Options = Options{IncludePseudonyms: true}
	e, raw := mustBuild(t, in)

	// No count under 20 is revealed anywhere.
	var counts []Count
	for _, r := range e.Body.Records {
		counts = append(counts, r.N)
	}
	// Every per-user cell is small, so the whole table is withheld.
	if e.Body.Pseudonyms != nil {
		t.Fatalf("pseudonyms = %+v", e.Body.Pseudonyms)
	}
	for _, c := range counts {
		if !c.Masked || c.Value != nil || c.Text != "<20" {
			t.Errorf("small count revealed: %+v", c)
		}
	}
	if bytes.Contains(raw, []byte(`"value"`)) {
		t.Errorf("a count value is printed:\n%s", raw)
	}
	for _, r := range e.Body.Records {
		for _, s := range append([]string{r.Headline}, r.KnownGaps...) {
			for _, d := range []string{" 4 ", " 3 requests", " 1 request", "One user", "single episode", "Two separate"} {
				if strings.Contains(" "+s+" ", d) {
					t.Errorf("text reveals a small count (%q): %q", d, s)
				}
			}
		}
	}

	// Without the key, a label cannot be mapped to a user id.
	other := bytes.Repeat([]byte{0x3C}, 32)
	for _, id := range []int{7, 9} {
		a, b := pseudonymLabel(in.Key, id), pseudonymLabel(other, id)
		if a == b {
			t.Errorf("user %d: same label under two keys (%s)", id, a)
		}
		for _, guess := range keylessGuesses(id) {
			if strings.HasPrefix(guess, strings.TrimPrefix(a, "user-")) {
				t.Errorf("user %d: label %s is derivable without the key", id, a)
			}
		}
		if strings.Contains(string(raw), a) {
			t.Errorf("label %s printed although the table is withheld", a)
		}
	}
	if pseudonymLabel(in.Key, 7) == pseudonymLabel(in.Key, 9) {
		t.Errorf("two users share a label")
	}
}

// keylessGuesses are what an attacker without the key might try: the id in
// decimal and hex, and unkeyed or empty-key hashes of it.
func keylessGuesses(id int) []string {
	s := strconv.Itoa(id)
	return []string{
		s, strconv.FormatInt(int64(id), 16), hex.EncodeToString([]byte(s)),
		strings.TrimPrefix(pseudonymLabel(nil, id), "user-"),
		strings.TrimPrefix(pseudonymLabel([]byte(s), id), "user-"),
		sha256Hex(s),
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestPseudonymsNeedKey(t *testing.T) {
	in := baseInput()
	in.Options.IncludePseudonyms = true
	in.Key = nil
	if _, err := Build(in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no key: err = %v", err)
	}
}

func TestNewKey(t *testing.T) {
	a, err := NewKey()
	if err != nil || len(a) != 32 {
		t.Fatalf("NewKey = %x, %v", a, err)
	}
	b, _ := NewKey()
	if bytes.Equal(a, b) {
		t.Fatalf("two keys are equal")
	}
}

func TestLedgerBadStateRefused(t *testing.T) {
	in := baseInput()
	in.Options.IncludeLedger = true
	in.Ledger[0].State = "proven_cause"
	if _, err := Build(in); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad ledger state: err = %v", err)
	}
}
