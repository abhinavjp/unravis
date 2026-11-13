package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordPerfNilBlockMarshalsNull(t *testing.T) {
	b, err := json.Marshal(RecordDetail{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"perf":null`) {
		t.Fatalf("want \"perf\":null in %s", b)
	}
}

func TestRecordPerfNilScalarMarshalsNull(t *testing.T) {
	b, err := json.Marshal(RecordPerf{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"sqlMs":null`) {
		t.Fatalf("want \"sqlMs\":null in %s", s)
	}
	if strings.Contains(s, `"sqlMs":0`) {
		t.Fatalf("nil sqlMs must not marshal as 0: %s", s)
	}
}
