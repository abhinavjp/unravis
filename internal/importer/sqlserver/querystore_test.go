package sqlserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// Synthetic only: host sql-a, 2001 dates. Query Store keeps interval times in
// UTC and runtime durations in microseconds.

func qsFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: readFixture(t, "querystore_good.csv"),
		Bad:  readFixture(t, "querystore_bad.csv"),
		// Good repeated is all duplicate rows, so the byte-limit checks need
		// a large file of distinct ones.
		Large:      []byte(qsHeader + qsBulk(3000)),
		NoFreeText: true,
		Options:    importer.Options{Host: sqlA(t), FileName: "querystore.csv"},
	}
}

func TestQuerystoreConformance(t *testing.T) { importertest.Run(t, NewQuerystore(), qsFixtures(t)) }

const qsHeader = "start_time,end_time,query_id,plan_id,count_executions,avg_duration,max_duration,avg_cpu_time\n"

func qsRow(q int, dur string) string {
	return fmt.Sprintf("2001-06-15 02:00:00.0000000 +00:00,2001-06-15 03:00:00.0000000 +00:00,%d,7,3,%s,2000000,500000\n", q, dur)
}

// qsBulk is n good rows (distinct query ids) so that one bad row stays under
// the reject-fraction limit.
func qsBulk(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(qsRow(i, "1000000"))
	}
	return b.String()
}

func qsParse(t testing.TB, in string) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return collect(t, NewQuerystore(), []byte(in), testOpts(t))
}

func qsReason(t testing.TB, err error) (importer.Reason, string) {
	t.Helper()
	var q *importer.QuarantineError
	if !errors.As(err, &q) {
		t.Fatalf("error %v is not a quarantine", err)
	}
	return q.Reason, q.Column
}

// shape is the comparable part of an event: it leaves out the line and the id.
func shape(e evidence.Event) string {
	d := "-"
	if e.DurationMs != nil {
		d = fmt.Sprint(*e.DurationMs)
	}
	keys := make([]string, 0, len(e.Attrs))
	for k := range e.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var a []string
	for _, k := range keys {
		a = append(a, k+"="+e.Attrs[k])
	}
	return fmt.Sprintf("%s %s %s %s dur=%s %s", e.Kind, e.Start.Format(time.RFC3339), e.End.Format(time.RFC3339), e.Unit, d, strings.Join(a, ","))
}

func shapes(evs []evidence.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = shape(e)
	}
	return out
}

func TestQuerystoreEventsCSV(t *testing.T) {
	evs, rep, err := collect(t, NewQuerystore(), readFixture(t, "querystore_good.csv"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	// Rows: runtime 101/11; runtime 202/21 + wait Lock; wait Buffer IO (its
	// runtime half repeats the row above); the same Buffer IO row again (a
	// duplicate); runtime 101/12 regular; runtime 101/12 aborted.
	if len(evs) != 6 || rep.Rows != 6 || rep.Rejected != 0 || rep.Emitted != 6 {
		t.Fatalf("events %d rows %d rejected %d emitted %d, want 6 6 0 6\n%v", len(evs), rep.Rows, rep.Rejected, rep.Emitted, shapes(evs))
	}
	first := evs[0]
	if first.Source != QSName || first.Kind != KindQueryInterval || first.Host != "sql-a" {
		t.Fatalf("first event %+v", first)
	}
	// avg_duration is in microseconds: 1,250,000 us is 1250 ms.
	if first.DurationMs == nil || *first.DurationMs != 1250 || first.Unit != "ms" {
		t.Fatalf("duration %v unit %q, want 1250 ms", first.DurationMs, first.Unit)
	}
	if !first.Start.Equal(time.Date(2001, 6, 15, 2, 0, 0, 0, time.UTC)) || !first.End.Equal(time.Date(2001, 6, 15, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("interval %v - %v, want 02:00Z - 03:00Z", first.Start, first.End)
	}
	want := map[string]string{"query_id": "101", "plan_id": "11", "executions": "40", "max_duration_ms": "9800", "avg_cpu_ms": "900",
		"avg_logical_reads": "5400.5", "avg_rows": "12", "forced": "false", "execution_type": "regular",
		"last_execution": "2001-06-15T02:59:10Z"}
	if fmt.Sprint(first.Attrs) != fmt.Sprint(want) {
		t.Fatalf("attrs %v, want %v", first.Attrs, want)
	}
	// The wait stat is in milliseconds already and is its own event of kind wait.
	var waits []evidence.Event
	for _, e := range evs {
		if e.Kind == KindWait {
			waits = append(waits, e)
		}
	}
	if len(waits) != 2 || waits[0].Attrs["wait_category"] != "Lock" || waits[0].Attrs["wait_class"] != "lock" ||
		waits[0].DurationMs == nil || *waits[0].DurationMs != 4200.5 || waits[0].Attrs["query_id"] != "202" ||
		waits[1].Attrs["wait_category"] != "Buffer IO" || waits[1].Attrs["wait_class"] != "other" || *waits[1].DurationMs != 120 {
		t.Fatalf("waits %v", shapes(waits))
	}
	// The aborted run of query 101 is its own event, not a duplicate.
	last := evs[len(evs)-1]
	if last.Attrs["execution_type"] != "aborted" || last.Attrs["forced"] != "true" || *last.DurationMs != 60000 {
		t.Fatalf("last event %v", shape(last))
	}
	if rep.Quality.TimezoneRule != "utc-field" {
		t.Errorf("timezone rule %q, want utc-field", rep.Quality.TimezoneRule)
	}
	for _, g := range []string{gapQSAverages, gapQSCapture, gapQSText, gapQSDup} {
		if !contains(rep.KnownGaps, g) {
			t.Errorf("known gaps %v lack %q", rep.KnownGaps, g)
		}
	}
	for _, e := range evs {
		if err := e.Validate(NewQuerystore().AllowedAttrs()); err != nil {
			t.Errorf("event %s: %v", shape(e), err)
		}
	}
}

// The text of a query is never an event, an attribute, a gap, an error or a
// report field, quoted commas and apostrophes included.
func TestQuerystoreNeverKeepsText(t *testing.T) {
	for _, name := range []string{"querystore_good.csv", "querystore_good.json"} {
		evs, rep, err := collect(t, NewQuerystore(), readFixture(t, name), testOpts(t))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		all, _ := json.Marshal(struct {
			E []evidence.Event
			R importer.Report
		}{evs, rep})
		for _, banned := range []string{"ZZ-TEXT-MARK", "SELECT", "UPDATE", "dbo.", "brien"} {
			if bytes.Contains(all, []byte(banned)) {
				t.Errorf("%s: output contains query text %q", name, banned)
			}
		}
	}
}

func TestQuerystoreEventsJSON(t *testing.T) {
	evs, rep, err := collect(t, NewQuerystore(), readFixture(t, "querystore_good.json"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != 3 || rep.Rejected != 0 || len(evs) != 4 {
		t.Fatalf("rows %d rejected %d events %d, want 3 0 4\n%v", rep.Rows, rep.Rejected, len(evs), shapes(evs))
	}
	if evs[0].Kind != KindQueryInterval || *evs[0].DurationMs != 1250 || evs[0].Attrs["forced"] != "false" || evs[0].Attrs["avg_rows"] != "12" {
		t.Fatalf("first %s", shape(evs[0]))
	}
	// Row 2 has runtime stats and a wait category.
	if evs[1].Kind != KindQueryInterval || *evs[1].DurationMs != 300 || evs[2].Kind != KindWait || *evs[2].DurationMs != 4200.5 {
		t.Fatalf("row 2: %s / %s", shape(evs[1]), shape(evs[2]))
	}
	// Row 3: forced is a JSON boolean and a null avg_rowcount is absent.
	if evs[3].Attrs["forced"] != "true" || evs[3].Attrs["avg_rows"] != "" || *evs[3].DurationMs != 8200 {
		t.Fatalf("row 3: %s", shape(evs[3]))
	}
	if !contains(rep.KnownGaps, gapQSText) {
		t.Errorf("gaps %v: the dropped query text column is not reported", rep.KnownGaps)
	}
}

// A FOR JSON PATH query with ROOT wraps the rows in an object.
func TestQuerystoreJSONRootAndNDJSONStyle(t *testing.T) {
	obj := `{"rows":[{"start_time":"2001-06-15T02:00:00","end_time":"2001-06-15T03:00:00","query_id":5,"avg_duration":2500000}]}`
	evs, _, err := qsParse(t, obj)
	if err != nil || len(evs) != 1 || *evs[0].DurationMs != 2500 {
		t.Fatalf("ROOT wrapper: %v %v", err, shapes(evs))
	}
	evs, _, err = qsParse(t, `{"start_time":"2001-06-15T02:00:00","query_id":5}`)
	if err != nil || len(evs) != 1 {
		t.Fatalf("single object: %v %v", err, shapes(evs))
	}
}

func TestQuerystoreSqlcmdOutput(t *testing.T) {
	evs, rep, err := collect(t, NewQuerystore(), readFixture(t, "querystore_sqlcmd.txt"), testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	// The dashes row and the "(2 rows affected)" footer are not rows; NULL is absent.
	if rep.Rows != 2 || rep.Rejected != 0 || len(evs) != 2 {
		t.Fatalf("rows %d rejected %d events %d, want 2 0 2", rep.Rows, rep.Rejected, len(evs))
	}
	if evs[0].Attrs["avg_rows"] != "12" || evs[1].Attrs["avg_rows"] != "" || *evs[1].DurationMs != 8200 {
		t.Fatalf("events %v", shapes(evs))
	}
	if got := NewQuerystore().Detect(readFixture(t, "querystore_sqlcmd.txt"), "out.txt"); got < 70 {
		t.Errorf("Detect on sqlcmd output = %d", got)
	}
}

func TestQuerystoreMissingColumns(t *testing.T) {
	cases := []struct {
		name, in string
		reason   importer.Reason
		column   string
	}{
		{"no time column", "query_id,plan_id,avg_duration\n1,2,3\n", importer.ReasonUnsupportedFormat, "start_time"},
		{"no query id", "start_time,end_time,plan_id,avg_duration\n2001-06-15 02:00:00,2001-06-15 03:00:00,2,3\n", importer.ReasonUnsupportedFormat, "query_id"},
		{"header only", "start_time,query_id\n", "", ""},
		{"neither", "plan_id,avg_duration\n2,3\n", importer.ReasonUnsupportedFormat, "query_id"},
		{"two start columns", "start_time,interval_start_time,query_id\n2001-06-15 02:00:00,2001-06-15 02:00:00,1\n", importer.ReasonUnsupportedFormat, "start_time"},
	}
	for _, c := range cases {
		evs, _, err := qsParse(t, c.in)
		if c.reason == "" {
			if err != nil || len(evs) != 0 {
				t.Errorf("%s: err %v events %d, want an empty import", c.name, err, len(evs))
			}
			continue
		}
		r, col := qsReason(t, err)
		if r != c.reason || col != c.column || len(evs) != 0 {
			t.Errorf("%s: reason %q column %q events %d, want %q %q 0", c.name, r, col, len(evs), c.reason, c.column)
		}
	}
	// JSON: the first object decides.
	_, _, err := qsParse(t, `[{"query_id":1,"avg_duration":5}]`)
	if r, col := qsReason(t, err); r != importer.ReasonUnsupportedFormat || col != "start_time" {
		t.Errorf("json without time: %q %q", r, col)
	}
	_, _, err = qsParse(t, `[{"start_time":"2001-06-15T02:00:00","avg_duration":5}]`)
	if r, col := qsReason(t, err); r != importer.ReasonUnsupportedFormat || col != "query_id" {
		t.Errorf("json without query_id: %q %q", r, col)
	}
	// Only the interval end, or only the interval start, is enough; a minimal
	// file with just the two required columns gives an event with no duration.
	for _, hdr := range []string{"end_time", "start_time"} {
		evs, _, err := qsParse(t, hdr+",query_id\n2001-06-15 03:00:00 +00:00,9\n")
		if err != nil || len(evs) != 1 || evs[0].DurationMs != nil || !evs[0].Start.Equal(evs[0].End) {
			t.Errorf("%s only: %v %v", hdr, err, shapes(evs))
		}
	}
	// The error never carries the user's header or any cell.
	_, _, err = qsParse(t, "SENTINEL-7f3a,plan_id\nSENTINEL-7f3a,1\n")
	if err == nil || strings.Contains(err.Error(), "SENTINEL-7f3a") {
		t.Errorf("error for an unknown header: %v", err)
	}
	// Column names are case-insensitive.
	evs, _, err := qsParse(t, "START_TIME,Query_Id,AVG_DURATION\n2001-06-15 02:00:00,4,3000\n")
	if err != nil || len(evs) != 1 || *evs[0].DurationMs != 3 {
		t.Errorf("upper case headers: %v %v", err, shapes(evs))
	}
}

func TestQuerystoreNeedsHost(t *testing.T) {
	o := testOpts(t)
	o.Host = nil
	_, _, err := collect(t, NewQuerystore(), readFixture(t, "querystore_good.csv"), o)
	if !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("no host: %v", err)
	}
}

// A BOM and CRLF line ends give the same events as the plain file.
func TestQuerystoreBOMAndCRLF(t *testing.T) {
	plain := readFixture(t, "querystore_good.csv")
	want, _, err := collect(t, NewQuerystore(), plain, testOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	crlf := bytes.ReplaceAll(plain, []byte("\n"), []byte("\r\n"))
	variants := map[string][]byte{
		"bom":      append([]byte{0xEF, 0xBB, 0xBF}, plain...),
		"crlf":     crlf,
		"bom+crlf": append([]byte{0xEF, 0xBB, 0xBF}, crlf...),
		"utf16le":  utf16le(string(plain)),
	}
	for name, in := range variants {
		got, rep, err := collect(t, NewQuerystore(), in, testOpts(t))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if fmt.Sprint(shapes(got)) != fmt.Sprint(shapes(want)) || rep.Rejected != 0 {
			t.Errorf("%s: events differ\n got %v\nwant %v", name, shapes(got), shapes(want))
		}
		if got := NewQuerystore().Detect(in, "x.csv"); got < 70 {
			t.Errorf("%s: Detect = %d", name, got)
		}
	}
}

// One megabyte of query text in a cell is read, dropped and never kept.
func TestQuerystoreHugeTextCell(t *testing.T) {
	chunk := "SELECT a, b FROM dbo.t WHERE n = ''ZZ-HUGE'' AND c IN (1, 2, 3) \r\n"
	text := strings.Repeat(chunk, (1<<20)/len(chunk)+1)
	if len(text) < 1<<20 {
		t.Fatal("test text too small")
	}
	csv := "start_time,end_time,query_id,plan_id,avg_duration,query_sql_text\n" +
		"2001-06-15 02:00:00 +00:00,2001-06-15 03:00:00 +00:00,1,2,4000000,\"" + text + "\"\n" +
		"2001-06-15 02:00:00 +00:00,2001-06-15 03:00:00 +00:00,2,2,5000000,\"short, text\"\n"
	evs, rep, err := qsParse(t, csv)
	if err != nil || len(evs) != 2 || rep.Rows != 2 || *evs[0].DurationMs != 4000 {
		t.Fatalf("1 MB cell: err %v events %d", err, len(evs))
	}
	out, _ := json.Marshal(struct {
		E []evidence.Event
		R importer.Report
	}{evs, rep})
	if bytes.Contains(out, []byte("ZZ-HUGE")) || bytes.Contains(out, []byte("SELECT")) || len(out) > 20<<10 {
		t.Errorf("text reached the output (%d bytes)", len(out))
	}
	// The same cell in JSON.
	js := `[{"start_time":"2001-06-15T02:00:00","query_id":1,"avg_duration":4000000,"query_sql_text":` + mustJSON(t, text) + `},` +
		`{"start_time":"2001-06-15T02:00:00","query_id":2,"avg_duration":5000000}]`
	evs, _, err = qsParse(t, js)
	if err != nil || len(evs) != 2 {
		t.Fatalf("1 MB json cell: err %v events %d", err, len(evs))
	}
	// Past the line cap the file is refused with a limit error, never a panic or an echo.
	huge := "start_time,query_id,query_sql_text\n2001-06-15 02:00:00,1,\"" + strings.Repeat("ZZ-HUGE ", 3<<18) + "\"\n"
	_, _, err = qsParse(t, huge)
	var le *importer.LimitError
	if err == nil || (!errors.As(err, &le) && !errors.As(err, new(*importer.QuarantineError))) || strings.Contains(err.Error(), "ZZ-HUGE") {
		t.Errorf("3 MB cell: %v", err)
	}
	// An unbroken JSON string past the bound is refused too.
	_, _, err = qsParse(t, `[{"start_time":"2001-06-15T02:00:00","query_id":1,"query_sql_text":"`+strings.Repeat("x", 20<<20)+`"}]`)
	if err == nil || strings.Contains(err.Error(), "xxxx") {
		t.Errorf("20 MB json cell: %v", err)
	}
}

func mustJSON(t testing.TB, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Negative, NaN, infinite, overflowing and malformed numbers reject the row
// and keep the rest of the file.
func TestQuerystoreBadNumbers(t *testing.T) {
	for _, bad := range []string{"-5", "-0.5", "NaN", "nan", "Infinity", "-Infinity", "Inf", "1e999", "1e309", "abc", "0x10", "1.2.3", "9999999999999999999999", "--1", "1e", "$5", "1 2"} {
		in := qsHeader + qsBulk(40) + qsRow(100, bad)
		evs, rep, err := qsParse(t, in)
		if err != nil {
			t.Errorf("%q: %v", bad, err)
			continue
		}
		if len(evs) != 40 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadNumber || rep.Errors[0].Line != 42 {
			t.Errorf("%q: events %d rejected %d errors %v, want 40 events and one bad-number at line 42", bad, len(evs), rep.Rejected, rep.Errors)
		}
		for _, e := range evs {
			if e.DurationMs != nil && (*e.DurationMs < 0 || *e.DurationMs != *e.DurationMs) {
				t.Errorf("%q: event duration %v", bad, *e.DurationMs)
			}
		}
	}
	// The same in the other numeric columns, ids and counts.
	for _, col := range []string{"query_id", "plan_id", "count_executions", "max_duration", "avg_cpu_time"} {
		row := map[string]string{"query_id": "5", "plan_id": "7", "count_executions": "3", "max_duration": "2000000", "avg_cpu_time": "500000"}
		row[col] = "-1"
		in := qsHeader + qsBulk(40) + fmt.Sprintf("2001-06-15 02:00:00,2001-06-15 03:00:00,%s,%s,%s,1000,%s,%s\n",
			row["query_id"], row["plan_id"], row["count_executions"], row["max_duration"], row["avg_cpu_time"])
		evs, rep, err := qsParse(t, in)
		if err != nil || len(evs) != 40 || rep.Rejected != 1 {
			t.Errorf("negative %s: err %v events %d rejected %d", col, err, len(evs), rep.Rejected)
		}
	}
	// A fractional id or count, a non-zero offset and a reversed interval are rejected.
	for name, row := range map[string]string{
		"fraction id":     "2001-06-15 02:00:00,2001-06-15 03:00:00,5.5,7,3,1000,2000,500\n",
		"offset":          "2001-06-15 02:00:00 +05:30,2001-06-15 03:00:00 +05:30,5,7,3,1000,2000,500\n",
		"reversed":        "2001-06-15 03:00:00,2001-06-15 02:00:00,5,7,3,1000,2000,500\n",
		"far future":      "2999-06-15 02:00:00,2999-06-15 03:00:00,5,7,3,1000,2000,500\n",
		"not a time":      "yesterday,today,5,7,3,1000,2000,500\n",
		"day past a week": "2001-06-15 02:00:00,2001-06-15 03:00:00,5,7,3,999999999999999,2000,500\n",
	} {
		evs, rep, err := qsParse(t, qsHeader+qsBulk(40)+row)
		if err != nil || len(evs) != 40 || rep.Rejected != 1 {
			t.Errorf("%s: err %v events %d rejected %d", name, err, len(evs), rep.Rejected)
		}
	}
	// A file that is mostly bad is refused.
	_, _, err := qsParse(t, qsHeader+qsRow(1, "NaN")+qsRow(2, "-1")+qsRow(3, "100"))
	if r, _ := qsReason(t, err); r != importer.ReasonRejectFraction {
		t.Errorf("mostly bad file: %v", err)
	}
	// JSON numbers get the same rules.
	var js strings.Builder
	js.WriteString(`[{"start_time":"2001-06-15T02:00:00","query_id":1,"avg_duration":-3},{"start_time":"2001-06-15T02:00:00","query_id":2,"avg_duration":1e999},` +
		`{"start_time":"2001-06-15T02:00:00","query_id":3,"avg_duration":"NaN"},{"start_time":"2001-06-15T02:00:00","query_id":4,"avg_duration":[1]}`)
	for i := 5; i < 105; i++ {
		fmt.Fprintf(&js, `,{"start_time":"2001-06-15T02:00:00","query_id":%d,"avg_duration":1000}`, i)
	}
	js.WriteString("]")
	evs, rep, err := qsParse(t, js.String())
	if err != nil || len(evs) != 100 || rep.Rejected != 4 {
		t.Errorf("json bad numbers: err %v events %d rejected %d, want 100 and 4", err, len(evs), rep.Rejected)
	}
}

// A repeated row, as a join or a re-run export writes it, counts once; rows that
// differ in plan, interval or execution type do not.
func TestQuerystoreDuplicateRows(t *testing.T) {
	row := qsRow(1, "1000000")
	evs, rep, err := qsParse(t, qsHeader+row+row+row)
	if err != nil || len(evs) != 1 || rep.Rows != 3 || rep.Rejected != 0 {
		t.Fatalf("three copies: err %v events %d rows %d", err, len(evs), rep.Rows)
	}
	if !contains(rep.KnownGaps, gapQSDup) {
		t.Errorf("duplicates not reported: %v", rep.KnownGaps)
	}
	other := "2001-06-15 03:00:00.0000000 +00:00,2001-06-15 04:00:00.0000000 +00:00,1,7,3,1000000,2000000,500000\n"
	otherPlan := "2001-06-15 02:00:00.0000000 +00:00,2001-06-15 03:00:00.0000000 +00:00,1,8,3,1000000,2000000,500000\n"
	evs, rep, err = qsParse(t, qsHeader+row+other+otherPlan+row)
	if err != nil || len(evs) != 3 {
		t.Fatalf("distinct rows: err %v events %d, want 3", err, len(evs))
	}
	if contains(rep.KnownGaps, gapQSDup) != true {
		t.Errorf("the repeated first row is not reported")
	}
	evs, rep, _ = qsParse(t, qsHeader+row+other)
	if len(evs) != 2 || contains(rep.KnownGaps, gapQSDup) {
		t.Errorf("no duplicates, yet events %d gaps %v", len(evs), rep.KnownGaps)
	}
	// The wait rows of one runtime row: one runtime event, one wait event per category.
	hdr := "start_time,end_time,query_id,plan_id,avg_duration,wait_category_desc,avg_query_wait_time\n"
	w := func(cat, ms string) string {
		return "2001-06-15 02:00:00 +00:00,2001-06-15 03:00:00 +00:00,1,7,1000000," + cat + "," + ms + "\n"
	}
	evs, _, err = qsParse(t, hdr+w("Lock", "10")+w("Latch", "20")+w("Lock", "10")+w("CPU", "30"))
	if err != nil || len(evs) != 4 {
		t.Fatalf("wait join: err %v events %v", err, shapes(evs))
	}
	runtime := 0
	for _, e := range evs {
		if e.Kind == KindQueryInterval {
			runtime++
		}
	}
	if runtime != 1 {
		t.Errorf("runtime events = %d, want 1", runtime)
	}
}

func TestQuerystoreWaitCategories(t *testing.T) {
	hdr := "start_time,query_id,wait_category_desc,avg_query_wait_time\n"
	in := hdr + "2001-06-15 02:00:00,1,lock,5\n2001-06-15 02:00:00,2,Some Future Category,5\n2001-06-15 02:00:00,3,Preemptive,5\n"
	evs, _, err := qsParse(t, in)
	if err != nil || len(evs) != 3 {
		t.Fatalf("err %v events %d", err, len(evs))
	}
	got := []string{evs[0].Attrs["wait_category"] + "/" + evs[0].Attrs["wait_class"], evs[1].Attrs["wait_category"] + "/" + evs[1].Attrs["wait_class"],
		evs[2].Attrs["wait_category"] + "/" + evs[2].Attrs["wait_class"]}
	if strings.Join(got, " ") != "Lock/lock Unknown/other Preemptive/preemptive" {
		t.Errorf("categories %v", got)
	}
	for _, e := range evs {
		if e.Kind != KindWait {
			t.Errorf("a row with only a wait category gave a %s event", e.Kind)
		}
	}
	// A category that is not a word is rejected and never echoed.
	in = hdr + strings.Repeat("2001-06-15 02:00:00,1,Lock,5\n", 40) + "2001-06-15 02:00:00,2,SENTINEL-7f3a;DROP,5\n"
	_, rep, err := qsParse(t, in)
	if err != nil || rep.Rejected != 1 {
		t.Fatalf("err %v rejected %d", err, rep.Rejected)
	}
}

func TestQuerystoreDetect(t *testing.T) {
	imp := NewQuerystore()
	cases := map[string]struct {
		head []byte
		min  int
		max  int
	}{
		"csv":              {readFixture(t, "querystore_good.csv"), 90, 100},
		"json":             {readFixture(t, "querystore_good.json"), 70, 100},
		"no query id":      {[]byte("start_time,end_time,avg_duration\n1,2,3\n"), 0, 0},
		"query id alone":   {[]byte("query_id,name\n1,x\n"), 0, 0},
		"no time, claimed": {[]byte("query_id,plan_id,avg_duration\n1,2,3\n"), 55, 100},
		"xe":               {readFixture(t, "xe_good.csv"), 0, 0},
		"pdh":              {readFixture(t, "pdh_good.csv"), 0, 0},
		"errorlog":         {readFixture(t, "errorlog_good.log"), 0, 0},
		"empty":            {nil, 0, 0},
		"binary":           {[]byte{0, 1, 2, 3, 0xff, 0xfe}, 0, 0},
		"json no query id": {[]byte(`[{"plan_id":1,"avg_duration":2}]`), 0, 0},
	}
	for name, c := range cases {
		if got := imp.Detect(c.head, "export.csv"); got < c.min || got > c.max {
			t.Errorf("%s: Detect = %d, want %d..%d", name, got, c.min, c.max)
		}
	}
}

// Every attribute the importer writes is in its declared allowlist, and no
// value is more than a number or a fixed word.
func TestQuerystoreAttrsAreNumbersOrWords(t *testing.T) {
	allowed := NewQuerystore().AllowedAttrs()
	for _, name := range []string{"querystore_good.csv", "querystore_good.json", "querystore_sqlcmd.txt"} {
		evs, _, err := collect(t, NewQuerystore(), readFixture(t, name), testOpts(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			for k, v := range e.Attrs {
				if !allowed[k] {
					t.Errorf("%s: attribute %q not allowed", name, k)
				}
				if len(v) > 40 {
					t.Errorf("%s: attribute %q has a %d byte value", name, k, len(v))
				}
			}
		}
	}
}

func FuzzParseQuerystore(f *testing.F) {
	f.Add(readFixture(f, "querystore_good.json"))
	f.Add(readFixture(f, "querystore_sqlcmd.txt"))
	fx := qsFixtures(f)
	importertest.FuzzParse(f, NewQuerystore(), fx)
}
