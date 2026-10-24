package iis

import (
	"encoding/json"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

func TestClassifyWASW2G(t *testing.T) {
	for id, kind := range map[int]string{
		5009: "worker-process-failure", 5010: "worker-process-failure",
		5011: "worker-process-failure", 5013: "worker-process-failure",
		5002: "apppool-disabled",
		// existing ids are unchanged
		5074: "apppool-recycle", 5117: "apppool-recycle", 5186: "apppool-idle-shutdown",
	} {
		k, _, ok := ClassifyWAS("Microsoft-Windows-WAS", id)
		if !ok || k != kind {
			t.Errorf("id %d = %q %v, want %q", id, k, ok, kind)
		}
	}
	// Uncited ids and other providers stay generic.
	for _, c := range []struct {
		p  string
		id int
	}{{"Microsoft-Windows-WAS", 5012}, {"Microsoft-Windows-WAS", 5001}, {"Microsoft-Windows-WAS", 9999},
		{"SyntheticProvider", 5011}, {"W3SVC", 5002}} {
		if k, _, ok := ClassifyWAS(c.p, c.id); ok {
			t.Errorf("%s %d classified as %q", c.p, c.id, k)
		}
	}
}

func TestW3CBytes(t *testing.T) {
	evs, rep, err := importer.Collect(t.Context(), NewW3C(), strings.NewReader(string(fixture(t, "w3c_bytes.log"))), baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 || rep.Rejected != 0 {
		t.Fatalf("events %d rejected %d", len(evs), rep.Rejected)
	}
	if a := evs[0].Attrs; a["bytes-out"] != "18450" || a["bytes-in"] != "512" {
		t.Errorf("row 0 attrs %v", a)
	}
	if a := evs[1].Attrs; a["bytes-out"] != "310" || a["bytes-in"] != "2048" {
		t.Errorf("row 1 attrs %v", a)
	}
	if a := evs[2].Attrs; a["bytes-out"] != "" || a["bytes-in"] != "" {
		t.Errorf("a '-' cell gave bytes: %v", a)
	}
	imp := NewW3C()
	for _, e := range evs {
		if err := e.Validate(imp.AllowedAttrs()); err != nil {
			t.Error(err)
		}
	}
	b, _ := json.Marshal(struct {
		R importer.Report
		E []any
	}{R: rep})
	if strings.Contains(string(b), importertest.Sentinel) {
		t.Error("sentinel in report")
	}
	for _, e := range evs {
		eb, _ := json.Marshal(e)
		if strings.Contains(string(eb), importertest.Sentinel) {
			t.Error("sentinel in event")
		}
	}
}

func TestW3CBytesBadValueRejectsRow(t *testing.T) {
	f := "#Software: Microsoft Internet Information Services 10.0\n" +
		"#Fields: date time s-computername cs-method cs-uri-stem sc-status sc-bytes cs-bytes time-taken\n" +
		strings.Repeat("2001-10-25 01:02:31 WEB-A GET /a 200 12 34 5\n", 60) +
		"2001-10-25 01:02:32 WEB-A GET /a 200 " + importertest.Sentinel + " 34 5\n" +
		"2001-10-25 01:02:33 WEB-A GET /a 200 -1 34 5\n"
	evs, rep, err := importer.Collect(t.Context(), NewW3C(), strings.NewReader(f), baseOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 60 || rep.Rejected != 2 {
		t.Fatalf("events %d rejected %d", len(evs), rep.Rejected)
	}
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), importertest.Sentinel) {
		t.Error("sentinel in report")
	}
}

func TestW3CBytesConformance(t *testing.T) {
	fx := w3cFixtures(t)
	fx.Good = clean(fixture(t, "w3c_bytes.log"))
	importertest.Run(t, NewW3C(), fx)
}

func FuzzW3CBytes(f *testing.F) {
	f.Add(fixture(f, "w3c_bytes.log"))
	importertest.FuzzParse(f, NewW3C(), w3cFixtures(f))
}
