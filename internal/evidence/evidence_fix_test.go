package evidence

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/ledger"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

// Regression tests for the adversarial review of the export engine.

// stallsFor makes count stalls for each user, plus noUser stalls without one.
func stallsFor(users, count, noUser int) []audit.Stall {
	var out []audit.Stall
	for u := 0; u < users; u++ {
		for i := 0; i < count; i++ {
			out = append(out, stall(1000+u, "Orders", t0.Add(time.Duration(u*count+i)*time.Second)))
		}
	}
	for i := 0; i < noUser; i++ {
		s := stall(0, "Orders", t0.Add(time.Hour+time.Duration(i)*time.Second))
		s.UserID = nil
		out = append(out, s)
	}
	return out
}

// withStalls sets the stalls and the B1/B2 totals to match them.
func withStalls(in Input, stalls []audit.Stall) Input {
	in.Report.Stalls = stalls
	in.Report.Findings[0].N = len(stalls)
	in.Report.Findings[1].N = len(stalls)
	return in
}

// Review item 1: a masked pseudonym cell must not be recoverable from the B1
// total, and the list length must not reveal a small number of users.
func TestPseudonymDifferencing(t *testing.T) {
	withheld := map[string][]audit.Stall{
		// 21 + 3 = 24 printed as B1: 24 - 21 would give the masked 3.
		"one small cell": append(stallsFor(1, 21, 0), stall(17, "Orders", t1), stall(17, "Orders", t1), stall(17, "Orders", t1)),
		// Every cell is 20 or more, but the stalls without a user (3) would be total minus the sum.
		"small no-user remainder": stallsFor(20, 20, 3),
		// Every cell is 20 or more, but the list length would say 19 users.
		"fewer than 20 users": stallsFor(19, 20, 0),
	}
	for name, stalls := range withheld {
		in := withStalls(baseInput(), stalls)
		in.Options.IncludePseudonyms = true
		e, raw := mustBuild(t, in)
		if e.Body.Pseudonyms != nil || bytes.Contains(raw, []byte(`"pseudonyms"`)) {
			t.Errorf("%s: pseudonym table printed: %+v", name, e.Body.Pseudonyms)
		}
		if !hasStatement(e, "Pseudonym table withheld: a cell was below 20.") {
			t.Errorf("%s: statements = %q", name, e.Manifest.Masking.Statements)
		}
		if bytes.Contains(raw, []byte("stable within this export")) {
			t.Errorf("%s: says labels are shown although the table is withheld", name)
		}
	}

	// 20 users with 20 stalls each and 20 without a user: every cell is safe.
	in := withStalls(baseInput(), stallsFor(20, 20, 20))
	in.Options.IncludePseudonyms = true
	e, _ := mustBuild(t, in)
	if len(e.Body.Pseudonyms) != 20 {
		t.Fatalf("pseudonyms = %d, want 20", len(e.Body.Pseudonyms))
	}
	for _, p := range e.Body.Pseudonyms {
		if !strings.HasPrefix(p.Label, "user-") || p.Requests.Masked || p.Requests.Value == nil || *p.Requests.Value < ThinCell {
			t.Errorf("row %+v: every printed row is a user with a count of 20 or more, never an \"others\" row", p)
		}
	}
	if !hasStatement(e, "Users are shown as keyed pseudonyms (user- followed by 8 hex characters). They are stable within this export only: each preview uses a new key, so the same user gets a different label in any other export.") {
		t.Errorf("statements = %q", e.Manifest.Masking.Statements)
	}
	for _, s := range e.Manifest.Masking.Statements {
		if strings.Contains(s, "within this case") {
			t.Errorf("statement claims per-case stability: %q", s)
		}
	}
}

// Review item 1, other records: B12 counts a subset of the stalls B1 counts, so
// printing both when they differ by 1-19 reveals a small count.
func TestComplementarySuppressionB12(t *testing.T) {
	cases := []struct {
		b1, b12  int
		wantText string
	}{
		{30, 25, withheldText}, // 30 - 25 = 5 would be revealed
		{30, 5, "<20"},         // the part is masked already; total minus "<20" is only a range
		{50, 25, "25"},         // the difference, 25, is not small
		{25, 25, "25"},         // no difference
	}
	for _, c := range cases {
		in := withStalls(baseInput(), stallsFor(1, c.b1, 0))
		in.Report.Findings[3].N = c.b12
		e, raw := mustBuild(t, in)
		b12 := e.Body.Records[3]
		if b12.Rule != "B12" || b12.N.Text != c.wantText {
			t.Errorf("B1 %d, B12 %d: B12 n = %+v, want %q", c.b1, c.b12, b12.N, c.wantText)
		}
		if c.wantText == withheldText {
			if !b12.N.Masked || b12.N.Value != nil {
				t.Errorf("withheld count carries a value: %+v", b12.N)
			}
			if h, _ := recordHash(b12); h != b12.Provenance.Hash {
				t.Errorf("record hash not recomputed after withholding")
			}
			if !strings.Contains(string(raw), `is 20 or more. It is hidden so that subtracting it from another count shown cannot reveal a count below 20.`) {
				t.Errorf("no statement explains \"withheld\"")
			}
		}
	}
}

// Review item 2: a masked n must not carry the engine's n<10 flag.
func TestLowNForcedWhenMasked(t *testing.T) {
	for _, c := range []struct {
		n        int
		engine   bool
		wantLowN bool
	}{{15, false, true}, {5, true, true}, {19, false, true}, {25, false, false}} {
		in := baseInput()
		in.Report.Findings[2].N = c.n // B6: no other record is a part of it
		in.Report.Findings[2].LowN = c.engine
		e, _ := mustBuild(t, in)
		if r := e.Body.Records[2]; r.LowN != c.wantLowN {
			t.Errorf("n=%d engine lowN=%v: export lowN = %v, want %v", c.n, c.engine, r.LowN, c.wantLowN)
		}
	}
}

// ledgerText builds an export whose ledger holds text and returns that entry.
func ledgerText(t *testing.T, text string) string {
	t.Helper()
	in := baseInput()
	in.Options.IncludeLedger = true
	in.Ledger = []ledger.Hypothesis{{ID: "h", Text: text, State: ledger.StateOpen, EvidenceIDs: []string{"p2:b1:0011223344556677"}}}
	e, _ := mustBuild(t, in)
	if len(e.Body.Ledger) != 1 {
		t.Fatalf("ledger = %+v", e.Body.Ledger)
	}
	return e.Body.Ledger[0].Text
}

// Review item 3: identifier shapes the first scrubber missed.
func TestLedgerScrubberShapes(t *testing.T) {
	cases := map[string][]string{
		"seen from fe80::1ff:fe23:4567:890a today":        {"fe80", "890a", "4567"},
		"client 2001:db8:85a3::8a2e:370:7334 again":       {"2001", "db8", "7334"},
		"addr:fe80::1 and ::ffff:10.9.8.7":                {"fe80", "10.9.8.7", "ffff"},
		"tagged ip_10.1.2.3 and ip10.1.2.3":               {"10.1.2.3"},
		"called api.example.com/v1/users/98765?token=abc": {"example", "98765", "token"},
		"bare host db01.internal and shop.example.com":    {"db01", "example"},
		"hit /api/orders/98765 twice":                     {"/api", "orders", "98765"},
		`file C:\Users\bob\dump.txt and \\fs01\share\x`:   {"bob", "dump", "fs01", "share"},
		"mail jürgen@exämple.de or root@localhost":        {"jürgen", "exämple", "root", "localhost", "@"},
		`body {"user_id": 98765} and {"userId": 98765}`:   {"98765"},
		"account 98765, uid 98765, uid=98765, cust#98765": {"98765"},
		"user id: 98,765 and user-4243":                   {"98", "765", "4243"},
		"see call 1003 at host demo-sql.example.invalid":  {"demo-sql", "example", "invalid"},
		"intranet box app01.corp and db.local":            {"app01", "db.local"},
		"bare server01.domain is down":                    {"server01", "domain"},
		"internal name build-7.internal.test reached":     {"build-7", "internal.test"},
	}
	for in, bad := range cases {
		got := ledgerText(t, in)
		for _, b := range bad {
			if strings.Contains(strings.ToLower(got), strings.ToLower(b)) {
				t.Errorf("ledger %q -> %q still contains %q", in, got, b)
			}
		}
	}
	// Clock times, dates and a pseudonym-like word are kept.
	if got := ledgerText(t, "at 10:30:00 on 2026-10-24, rule B12"); got != "at 10:30:00 on 2026-10-24, rule B12" {
		t.Errorf("harmless text changed: %q", got)
	}
	if got := ledgerText(t, "OrdersController.Create was slow"); !strings.Contains(got, "OrdersController.Create") {
		t.Errorf("Controller.Action was scrubbed: %q", got)
	}
	if got := ledgerText(t, "build 1.2.3 shipped"); !strings.Contains(got, "1.2.3") {
		t.Errorf("a version number was scrubbed: %q", got)
	}
}

// Review item 3: look-alike letters and invisible characters must not smuggle
// "proven" past the scrubber or the Build backstop.
func TestProvenNormalised(t *testing.T) {
	disguised := []string{
		"pro\u200bven",                         // zero-width space
		"pro\u00adven cause",                   // soft hyphen
		"PR\u041eVEN",                          // Cyrillic capital O
		"\u0440roven",                          // Cyrillic small er
		"\uff50\uff52\uff4f\uff56\uff45\uff4e", // full-width
		"pr\u03bfven",                          // Greek small omicron
		"un\u2060proven",                       // word joiner
	}
	for _, d := range disguised {
		if !containsProven(d) {
			t.Errorf("containsProven(%q) = false", d)
		}
		got := ledgerText(t, "it is "+d+" now")
		if containsProven(got) || strings.Contains(strings.ToLower(normalizeText(got)), "proven") {
			t.Errorf("ledger %q -> %q still reads as proven", d, got)
		}
	}
	if got := ledgerText(t, "provenance of the row"); !strings.Contains(got, "provenance") {
		t.Errorf("provenance removed: %q", got)
	}
	// The backstop sees normalised text: an unscrubbed field with a disguised
	// "proven" fails the whole build.
	for _, d := range disguised {
		in := baseInput()
		in.ToolVersion = "0.13 " + d
		if _, err := Build(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("tool version %q: err = %v, want ErrInvalid", d, err)
		}
	}
	// A disguised label is not used as the source name.
	in := baseInput()
	in.SourceLabels = map[string]string{leakySource: "PR\u041eVEN audit"}
	if e, _ := mustBuild(t, in); e.Body.Records[0].Source != genericSource {
		t.Errorf("disguised label used: %q", e.Body.Records[0].Source)
	}
}

// Review item 3: anything that still looks like an identifier after scrubbing
// withholds the whole text.
func TestResidualRiskWithholds(t *testing.T) {
	for _, in := range []string{
		"call 5551234567 about it",
		"ticket 123456",
	} {
		if got := ledgerText(t, in); got != ledgerWithheld {
			t.Errorf("ledger %q -> %q, want %q", in, got, ledgerWithheld)
		}
	}
	// The scrubber removes every '@' and "://" it sees; the residual check is
	// the backstop if a pattern ever misses one.
	for _, s := range []string{"a @ b", "x://y", "ref 12345"} {
		if !residualRisk(s) {
			t.Errorf("residualRisk(%q) = false", s)
		}
	}
	if got := ledgerText(t, "ask a @ b"); strings.Contains(got, "@") {
		t.Errorf("'@' survived: %q", got)
	}
	in := baseInput()
	in.Report.Coverage = &audit.PatternCoverage{Run: false, Note: "Coverage skipped, ref 5551234567"}
	e, raw := mustBuild(t, in)
	if !strings.Contains(strings.Join(e.Body.Records[0].KnownGaps, " "), noteWithheld) || bytes.Contains(raw, []byte("5551234567")) {
		t.Errorf("coverage note not withheld: %q", e.Body.Records[0].KnownGaps)
	}
}

// Review item 5 (engine side): a hypothesis that references nothing in this
// export is not exported at all.
func TestLedgerOnlyRelatedHypotheses(t *testing.T) {
	in := baseInput()
	in.Options.IncludeLedger = true
	in.Ledger = append(in.Ledger,
		ledger.Hypothesis{ID: "old", Text: "earlier incident about tenant Zeta", State: ledger.StateOpen, EvidenceIDs: []string{"p2:b1:ffffffffffffffff"}},
		ledger.Hypothesis{ID: "none", Text: "no references at all", State: ledger.StateOpen},
	)
	e, raw := mustBuild(t, in)
	if len(e.Body.Ledger) != 1 || bytes.Contains(raw, []byte("Zeta")) || bytes.Contains(raw, []byte("no references")) {
		t.Fatalf("ledger = %+v", e.Body.Ledger)
	}
	found := false
	for _, s := range e.Manifest.Masking.Statements {
		found = found || strings.HasPrefix(s, "Only hypotheses that reference a finding in this export are included")
	}
	if !found {
		t.Errorf("statements = %q", e.Manifest.Masking.Statements)
	}
}

// Review item 4: Verify rejects trailing data, edits to the manifest, and any
// encoding but the canonical one; the hash is over the file's own bytes.
func TestVerifyCoversManifestAndTrailingData(t *testing.T) {
	in := baseInput()
	in.Options = Options{IncludeLedger: true}
	e, raw := mustBuild(t, in)
	if err := Verify(raw); err != nil {
		t.Fatalf("Verify(clean) = %v", err)
	}
	trimmed := bytes.TrimRight(raw, "\n")
	bad := map[string][]byte{
		"trailing object":      append(append([]byte{}, trimmed...), []byte(` {"smuggled":"user 4242 10.1.2.3"}`)...),
		"trailing brace":       append(append([]byte{}, trimmed...), []byte(`} {"smuggled":1}`)...),
		"trailing bracket":     append(append([]byte{}, trimmed...), []byte(`] arbitrary bytes here`)...),
		"masking edited":       bytes.Replace(raw, []byte(e.Manifest.Masking.Statements[0]), []byte("Nothing was masked."), 1),
		"tool version edited":  bytes.Replace(raw, []byte(`"0.13.0-test"`), []byte(`"9.9.9"`), 1),
		"created time edited":  bytes.Replace(raw, []byte(`"2026-10-24T12:00:00Z"`), []byte(`"2026-10-25T12:00:00Z"`), 1),
		"key spelling changed": bytes.Replace(raw, []byte(`"records"`), []byte(`"RECORDS"`), 1),
		"re-indented":          bytes.ReplaceAll(raw, []byte("\n  "), []byte("\n    ")),
		"html escaped":         bytes.ReplaceAll(raw, []byte("<20"), []byte(`\u003c20`)),
	}
	for name, b := range bad {
		if bytes.Equal(b, raw) {
			t.Fatalf("%s: tamper had no effect", name)
		}
		if err := Verify(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Verify = %v, want ErrInvalid", name, err)
		}
	}

	// The documented rule: set the bodySha256 value to "" and hash the bytes.
	if bytes.Contains(raw, []byte(`\u003c`)) || !bytes.Contains(raw, []byte("<20")) {
		t.Errorf("canonical form escapes HTML")
	}
	blank := bytes.Replace(raw, []byte(`"bodySha256": "`+e.Manifest.BodySHA256+`"`), []byte(`"bodySha256": ""`), 1)
	sum := sha256.Sum256(blank)
	if hex.EncodeToString(sum[:]) != e.Manifest.BodySHA256 {
		t.Errorf("hash is not SHA-256 of the file with the hash value blanked")
	}
}
