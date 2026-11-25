package bbsql

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/querytext"
)

// extras_qtext_test.go covers the one sampler that reads statement text. The
// point of these tests is the gates: the text statement is unreachable unless
// the owner's flag built it AND the unlock is in the context, and the lint
// carve-out is exactly the two fixed fragments and nothing else. Nothing here
// connects to anything; every row is a fake.

const (
	xtHashA = "0x0123456789ABCDEF"
	xtHashB = "0xFEDCBA9876543210"
	xtHashC = "0x00000000000000AA"
)

func xtRow(hash string, text any) Row { return Row{"qhash": hash, "q_text": text} }

func xtStep(s *qtextSampler, at time.Time, rows ...Row) ([]QText, bool, error) {
	s.Observe(Observation{Time: at, Epoch: 1})
	payload, truncated, err := s.Decode(rows)
	if err != nil || payload == nil {
		return nil, truncated, err
	}
	out, ok := payload.([]QText)
	if !ok {
		return nil, truncated, fmt.Errorf("payload is %T, want []QText", payload)
	}
	return out, truncated, nil
}

func TestXTRegistrationIsGatedOutOfEveryProfile(t *testing.T) {
	for _, p := range []string{ProfileEverything, ProfileDeep, "standard", "", "bogus"} {
		for _, k := range ExtrasForProfile(p) {
			if k == qtextKind {
				t.Errorf("profile %q lists the text sampler", p)
			}
		}
	}
	for _, k := range ExtraKinds() {
		if k == qtextKind {
			t.Error("ExtraKinds lists the text sampler")
		}
	}
	if got := Extras([]string{qtextKind}); len(got) != 0 {
		t.Errorf("Extras built the text sampler without the unlock: %d", len(got))
	}
	if got := ExtrasWithText([]string{qtextKind}, 0); len(got) != 0 {
		t.Errorf("ExtrasWithText(…, 0) built the text sampler: %d", len(got))
	}
	if got := ExtrasWithText([]string{qtextKind}, -5); len(got) != 0 {
		t.Errorf("ExtrasWithText(…, -5) built the text sampler: %d", len(got))
	}
	got := ExtrasWithText([]string{qtextKind}, 500)
	if len(got) != 1 || got[0].ID() != "bb:qtext" || got[0].Interval() != ExtraEvery5m {
		t.Fatalf("ExtrasWithText(…, 500) = %v, want one bb:qtext at 5m", got)
	}
	if s, ok := got[0].(*qtextSampler); !ok || s.maxChars != 500 {
		t.Errorf("the scrub cap was not passed through: %#v", got[0])
	}
	for _, s := range Samplers() {
		if s.Kind() == qtextKind {
			t.Error("the text sampler is in the Standard list")
		}
	}
}

func TestXTStatementContract(t *testing.T) {
	text, ok := Statement("bb:qtext")
	if !ok {
		t.Fatal("bb:qtext is not registered")
	}
	if !IsAllowed(text) || !isTextStatement(text) {
		t.Error("the text statement must be allowlisted and marked as a text statement")
	}
	if !strings.Contains(text, "TOP 41") || !strings.Contains(text, ", 8000)") {
		t.Errorf("the statement must cap rows (TOP 41) and text (8000): %s", text)
	}
	if got := statementCap(text); got != CapQText {
		t.Errorf("statementCap = %d, want %d", got, CapQText)
	}
	if err := lintStatement("bb:qtext", text); err != nil {
		t.Errorf("lint: %v", err)
	}
	if len(textStatementTexts) != 1 {
		t.Errorf("exactly one statement needs the unlock, have %d", len(textStatementTexts))
	}
	for id, other := range extraStatements {
		if id != "bb:qtext" && isTextStatement(other) {
			t.Errorf("%s is marked as a text statement", id)
		}
	}
	if _, ok := Statements["bb:qtext"]; ok {
		t.Error("the text statement leaked into the exported Statements view")
	}
}

func TestXTLintCarveOutIsNarrow(t *testing.T) {
	// The fragments themselves pass only inside the registered statement.
	if err := lintStatementFrags("bb:other", stmtQText, nil); err == nil {
		t.Error("the text statement passed with no carve-out")
	}
	// Another statement that reads the text function is refused even under the
	// qtext id's carve-out, because it lacks the fixed expression fragment.
	raw := strings.Replace(stmtQText, textExpr, "st.text", 1)
	if err := lintStatement("bb:qtext", raw); err == nil {
		t.Error("raw st.text, without the cut and scrub-ready expression, passed the lint")
	}
	// A second use of the handle, the plan, the offsets or a host column is
	// refused: the carve-out removes each fragment once.
	extras := []string{
		", qs.plan_handle AS leak",
		", qs.sql_handle AS leak",
		", qs.statement_start_offset AS leak",
		", st.dbid AS dbid, qs.last_execution_time AS host_name",
		", qs.query_plan_hash AS query_plan",
	}
	for _, add := range extras {
		mut := strings.Replace(stmtQText, " AS q_text", " AS q_text"+add, 1)
		if err := lintStatement("bb:qtext", mut); err == nil {
			t.Errorf("a mutated text statement with %q passed the lint", add)
		}
	}
	// Other statements never get the carve-out.
	mut := strings.Replace(stmtQHash, "FROM sys.dm_exec_query_stats", "FROM "+textFromSource, 1)
	if err := lintStatement("bb:qhash", mut); err == nil {
		t.Error("bb:qhash was allowed to read the text function")
	}
	if _, ok := extraLintFrags["bb:qhash"]; ok && len(extraLintFrags["bb:qhash"]) != 0 {
		t.Errorf("bb:qhash has lint fragments: %v", extraLintFrags["bb:qhash"])
	}
	// A mutated text statement is not on the allowlist at all.
	if IsAllowed(strings.Replace(stmtQText, "TOP 41", "TOP 4100", 1)) {
		t.Error("a changed text statement is allowlisted")
	}
}

func TestXTSendGatesRefuseWithoutTheUnlock(t *testing.T) {
	text := stmtQText
	q := &recQuery{replies: map[string][]Row{text: {xtRow(xtHashA, "SELECT 1")}}}
	if _, err := Query(context.Background(), q, text, time.Second); err == nil {
		t.Error("Query sent the text statement without the unlock")
	}
	if len(q.texts()) != 0 {
		t.Errorf("the text statement reached the wire without the unlock: %v", q.texts())
	}
	if _, err := (DBQueryer{}).Query(context.Background(), text); err != errNotAllowed {
		t.Errorf("DBQueryer without the unlock: %v, want errNotAllowed", err)
	}
	if _, err := (pinnedQueryer{}).Query(context.Background(), text); err != errNotAllowed {
		t.Errorf("pinnedQueryer without the unlock: %v, want errNotAllowed", err)
	}
	// An unlock never lets a non-allowlisted text through.
	ctx := withTextUnlock(context.Background())
	if _, err := Query(ctx, q, text+" ", time.Second); err == nil {
		t.Error("the unlock let a mutated statement through")
	}
	if _, err := (DBQueryer{}).Query(ctx, "SELECT 1"); err != errNotAllowed {
		t.Errorf("DBQueryer with the unlock and a foreign statement: %v", err)
	}
	// With the unlock the gate opens.
	if _, err := Query(ctx, q, text, time.Second); err != nil {
		t.Errorf("Query with the unlock: %v", err)
	}
	if got := q.texts(); len(got) != 1 || got[0] != text {
		t.Errorf("sent %v, want exactly the text statement", got)
	}
	// Standard statements are not affected by the unlock either way.
	if !isAllowedCtx(context.Background(), Statements["bb:waits"]) {
		t.Error("a Standard statement needs no unlock")
	}
}

func xtTickRunner(t *testing.T, q *recQuery, now time.Time) *Runner {
	t.Helper()
	return newFakeRunner(t, q, runnerOpts{now: now, samplers: []Sampler{baseSampler("bb:waits", "waits")}})
}

func TestXTRunnerNeverSendsTextWithoutTheFlag(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]: fakeWaitRows(1),
		stmtQText:              {xtRow(xtHashA, "SELECT * FROM t WHERE a = 'x'")},
	}}
	// Naming the kind in EnabledExtras is not enough: only the flag builds it.
	r := xtTickRunner(t, q, now)
	r.EnabledExtras = []string{qtextKind}
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if q.count(stmtQText) != 0 || sampleCount(t, r.Dir, qtextKind) != 0 || len(textSampleLines(t, r.Dir)) != 0 {
		t.Errorf("text was sent (%d) or written (%d) with QueryTextMaxChars = 0", q.count(stmtQText), sampleCount(t, r.Dir, qtextKind))
	}
	// A text sampler injected with the flag off is refused and switched off.
	q2 := &recQuery{replies: q.replies}
	r2 := xtTickRunner(t, q2, now)
	r2.extras = []Sampler{&qtextSampler{maxChars: 500}}
	r2.extrasReady = true
	if err := r2.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if q2.count(stmtQText) != 0 {
		t.Error("the guard sent the text statement with the flag off")
	}
}

func TestXTRunnerSendsAndScrubsWithTheFlag(t *testing.T) {
	now := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	q := &recQuery{replies: map[string][]Row{
		Statements["bb:waits"]: fakeWaitRows(1),
		stmtQText: {
			xtRow(xtHashA, "SELECT name FROM users WHERE email = 'jane@example.com' AND id = 42"),
			xtRow(xtHashB, "UPDATE u SET password = 'hunter2' WHERE id = 1"),
		},
	}}
	r := xtTickRunner(t, q, now)
	r.QueryTextMaxChars = 1000
	if err := r.Tick(now); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if q.count(stmtQText) != 1 {
		t.Fatalf("the text statement was sent %d times, want 1", q.count(stmtQText))
	}
	var rows []QText
	if n := sampleCount(t, r.Dir, qtextKind); n != 0 {
		t.Errorf("%d text lines went into a bb-sql file, which the read key can fetch", n)
	}
	for _, d := range textSampleLines(t, r.Dir) {
		if d.Kind != qtextKind {
			continue
		}
		b, _ := json.Marshal(d.Rows)
		if err := json.Unmarshal(b, &rows); err != nil {
			t.Fatalf("rows: %v", err)
		}
	}
	if len(rows) != 2 {
		t.Fatalf("wrote %d text rows, want 2: %#v", len(rows), rows)
	}
	all, _ := json.Marshal(rows)
	for _, leak := range []string{"jane@example.com", "hunter2", "42"} {
		if strings.Contains(string(all), leak) {
			t.Errorf("the written text leaks %q: %s", leak, all)
		}
	}
	if rows[1].Text != querytext.Withheld {
		t.Errorf("the credential statement was not withheld: %q", rows[1].Text)
	}
}

func TestXTDecodeDropsAndKeeps(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	s := &qtextSampler{maxChars: 1000}
	out, truncated, err := xtStep(s, t0,
		xtRow(xtHashA, "SELECT a FROM t WHERE b = 'abcvalue'"),
		xtRow(xtHashA, "SELECT a FROM t WHERE b = 'other'"), // same hash again in the reply
		xtRow(xtHashB, nil), // encrypted: NULL text
		xtRow(xtHashC, "SELECT 1 /* unravis:bb:waits */"),                     // this tool's own statement
		xtRow("0x0000000000000001", "   \n  "),                                // blank
		xtRow("0x0000000000000002", "EXEC sp_x @password = 'p', @user = 'u'"), // credential word
	)
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if len(out) != 2 {
		t.Fatalf("kept %d rows, want 2: %#v", len(out), out)
	}
	if out[0].QHash != xtHashA || strings.Contains(out[0].Text, "abcvalue") || !strings.Contains(out[0].Text, "?") {
		t.Errorf("first row = %#v, want the hash with its literal replaced by ?", out[0])
	}
	if out[1].QHash != "0x0000000000000002" || out[1].Text != querytext.Withheld {
		t.Errorf("second row = %#v, want the Withheld marker", out[1])
	}
	keys := xsJSONKeys(t, out[0])
	xsKeysEqual(t, keys, []string{"qHash", "qText"})
}

func TestXTDecodeSendsAHashOncePerSixHours(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	s := &qtextSampler{maxChars: 1000}
	row := xtRow(xtHashA, "SELECT 1 FROM t")
	if out, _, err := xtStep(s, t0, row); err != nil || len(out) != 1 {
		t.Fatalf("first: %v %v", out, err)
	}
	if out, _, err := xtStep(s, t0.Add(5*time.Minute), row); err != nil || len(out) != 0 {
		t.Fatalf("second cycle must be quiet: %v %v", out, err)
	}
	if out, _, err := xtStep(s, t0.Add(qtextResend-time.Minute), row); err != nil || len(out) != 0 {
		t.Fatalf("just under 6h must be quiet: %v %v", out, err)
	}
	if out, _, err := xtStep(s, t0.Add(qtextResend+time.Minute), row); err != nil || len(out) != 1 {
		t.Fatalf("after 6h the hash is sent again: %v %v", out, err)
	}
}

func TestXTDecodeMemoryStaysBounded(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	s := &qtextSampler{maxChars: 100}
	for i := 0; i < qtextSeenMax+500; i++ {
		h := fmt.Sprintf("0x%016X", i)
		if _, _, err := xtStep(s, t0, xtRow(h, "SELECT 1")); err != nil {
			t.Fatal(err)
		}
		if len(s.seen) > qtextSeenMax {
			t.Fatalf("seen holds %d entries, bound is %d", len(s.seen), qtextSeenMax)
		}
	}
}

func TestXTDecodeRowCapAndValidation(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	s := &qtextSampler{maxChars: 1000}
	var rows []Row
	for i := 0; i < CapQText+1; i++ {
		rows = append(rows, xtRow(fmt.Sprintf("0x%016X", i), "SELECT 1"))
	}
	out, truncated, err := xtStep(s, t0, rows...)
	if err != nil || !truncated || len(out) != CapQText {
		t.Fatalf("cap: kept=%d truncated=%v err=%v, want %d,true,nil", len(out), truncated, err, CapQText)
	}
	bad := []string{"", "0x12", "ABCDEF0123456789", "0xGGGGGGGGGGGGGGGG", "0x0123456789ABCDEF\n", "0x0123456789ABCDEFAA"}
	for _, h := range bad {
		_, _, err := (&qtextSampler{maxChars: 100}).Decode([]Row{xtRow(h, "SELECT 1")})
		xsNoLeak(t, "bad hash "+h, err, "SELECT 1")
	}
	for name, r := range map[string]Row{
		"unknown column": {"qhash": xtHashA, "q_text": "x", "sql_handle": "0x00"},
		"missing text":   {"qhash": xtHashA},
		"missing hash":   {"q_text": "x"},
	} {
		if _, _, err := (&qtextSampler{maxChars: 100}).Decode([]Row{r}); err == nil {
			t.Errorf("%s: Decode accepted the row", name)
		}
	}
}

// textSampleLines reads the bb-sqlq-<hour> files, the only place statement text may be written.
func textSampleLines(t *testing.T, dir string) []SampleDoc {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, TextSamplePrefix+"*"+SampleSuffix))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []SampleDoc
	for _, p := range matches {
		data, err := os.ReadFile(p) //nolint:gosec // test reads files it globbed in its own temp dir
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		for _, raw := range strings.Split(string(data), "\n") {
			raw = strings.TrimSuffix(raw, "\r")
			if raw == "" {
				continue
			}
			var doc SampleDoc
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				t.Fatalf("bad ndjson line %q: %v", raw, err)
			}
			out = append(out, doc)
		}
	}
	return out
}

// The text file is never one of the files the read key can fetch, and the folder sweep keeps it to the same age and byte bounds.
func TestXTTextFileIsNotAServedSampleNameAndIsSwept(t *testing.T) {
	now := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	name := filepath.Base(TextSamplePath(t.TempDir(), now))
	if strings.HasPrefix(name, SamplePrefix) {
		t.Fatalf("%q looks like a served sample file", name)
	}
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	r := xtTickRunner(t, q, now)
	old := filepath.Join(r.Dir, TextSamplePrefix+"20260301-00"+SampleSuffix)
	cur := TextSamplePath(r.Dir, now)
	for _, p := range []string{old, cur} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.rotate(now); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("an old text file survived the age sweep (%v)", err)
	}
	if _, err := os.Stat(cur); err != nil {
		t.Errorf("the current text file was removed: %v", err)
	}
}
