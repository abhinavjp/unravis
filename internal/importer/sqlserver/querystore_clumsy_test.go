package sqlserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// A sentinel in any cell, good row or bad, never reaches an event, the report or
// the error, in CSV or JSON.
func TestQuerystoreSentinelInAnyCellNeverLeaks(t *testing.T) {
	const s = "SENTINEL-7f3a"
	h := "start_time,end_time,query_id,plan_id,count_executions,avg_duration,max_duration,avg_cpu_time,avg_logical_io_reads,avg_rowcount,wait_category_desc,avg_query_wait_time,execution_type_desc,is_forced_plan\n"
	good := "2001-06-15 02:00:00,2001-06-15 03:00:00,%s,1,1,1,1,1,1,1,%s,5,%s,0\n"
	var in []string
	for _, c := range [][3]string{{"1", "Lock", "Regular"}, {s, "Lock", "Regular"}, {"2", s, "Regular"}, {"3", "Lock", s}, {"4", "=" + s, "Regular"}} {
		in = append(in, h+fmt.Sprintf(good, c[0], c[1], c[2]))
	}
	in = append(in,
		h+s+","+s+",1,1,1,1,1,1,1,1,,,,\n",
		h+"2001-06-15 02:00:00,2001-06-15 03:00:00,1,1,1,1,1,1,1,1,"+s+","+s+","+s+","+s+"\n",
		`[{"start_time":"2001-06-15T02:00:00","query_id":"`+s+`","avg_duration":"`+s+`","wait_category_desc":"`+s+`"}]`,
		`[{"start_time":"`+s+`","query_id":1,"`+s+`":"`+s+`"}]`,
		"\xef\xbb\xbf"+h+s+"\n",
	)
	for i, doc := range in {
		evs, rep, err := qsParse(t, doc)
		all, _ := json.Marshal(struct {
			E []evidence.Event
			R importer.Report
		}{evs, rep})
		if bytes.Contains(all, []byte(s)) {
			t.Errorf("input %d: sentinel in events or report: %s", i, all)
		}
		if err != nil && strings.Contains(err.Error(), s) {
			t.Errorf("input %d: sentinel in error: %v", i, err)
		}
		var q *importer.QuarantineError
		if errors.As(err, &q) && strings.Contains(q.Column+string(q.Reason), s) {
			t.Errorf("input %d: sentinel in quarantine column", i)
		}
	}
}

// numberShape is the case-export number shape (internal/evidence/caseexport.go).
var numberShape = regexp.MustCompile(`^-?[0-9]{1,15}(?:\.[0-9]{1,6})?$`)

// A "-0" cell is a number in [0, max] but must not reach an event as "-0".
func TestQuerystoreNegativeZeroIsZero(t *testing.T) {
	in := qsHeader + qsBulk(5) + "2001-06-15 02:00:00,2001-06-15 03:00:00,100,7,-0,-0,-0,-0\n"
	evs, _, err := qsParse(t, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		for k, v := range e.Attrs {
			if strings.HasPrefix(v, "-") {
				t.Errorf("attr %s=%q is negative", k, v)
			}
		}
		if e.DurationMs != nil && math.Signbit(*e.DurationMs) {
			t.Errorf("duration is negative zero")
		}
	}
}

// Every numeric attr an event carries stays inside the export shape, so a
// large id cannot be silently dropped from an export.
func TestQuerystoreIDsStayInExportShape(t *testing.T) {
	in := qsHeader + qsBulk(40) + qsRow(1000000000000000, "1000")
	evs, rep, err := qsParse(t, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		for _, k := range []string{"query_id", "plan_id"} {
			if v, ok := e.Attrs[k]; ok && !numberShape.MatchString(v) {
				t.Errorf("%s=%q is outside the export number shape (rejected %d)", k, v, rep.Rejected)
			}
		}
	}
}

// An interval spanning years is not a Query Store interval; it is rejected like
// any other bad time rather than drawn across the whole timeline.
func TestQuerystoreRefusesYearsLongInterval(t *testing.T) {
	row := "2001-06-15 02:00:00,2011-06-15 03:00:00,5,7,3,1000,2000,500\n"
	evs, rep, err := qsParse(t, qsHeader+qsBulk(40)+row)
	if err != nil || len(evs) != 40 || rep.Rejected != 1 || rep.Errors[0].Reason != importer.ReasonBadTime {
		t.Errorf("err %v events %d rejected %d errors %v, want 40 events and one bad-time", err, len(evs), rep.Rejected, rep.Errors)
	}
}
