package bbsource

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

func groupSample(seq int, at, members string) string {
	return `{"v":1,"run":"r","seq":` + itoa(int64(seq)) + `,"t":"` + at + `",` + members + `}` + "\n"
}

func TestEventLogCountsReadWithFixedWordsOnly(t *testing.T) {
	m := `"events":{"events":[{"channel":1,"eventId":7034,"level":2,"count":3,"t":"2001-09-09T01:47:10Z"},` +
		`{"channel":9,"eventId":1,"level":2,"count":1,"t":"2001-09-09T01:47:10Z"}]}`
	evs, rep, err := parse(t, groupSample(1, "2001-09-09T01:47:15Z", m), importer.Options{Limits: importer.Limits{MaxRejectFraction: 1}})
	if err != nil {
		t.Fatal(err)
	}
	w := ofKind(evs, KindWinEvent)
	if len(w) != 1 || w[0].Attrs["log"] != "System" || w[0].Attrs["id"] != "7034" || w[0].Severity != "error" || *w[0].Value != 3 || w[0].Unit != "count" {
		t.Fatalf("win events %+v", w)
	}
	if w[0].Start.Format("15:04:05") != "01:47:10" {
		t.Errorf("an entry is filed at its own time, got %v", w[0].Start)
	}
	if !contains(rep.KnownGaps, GapGroupDamaged) {
		t.Error("an entry of an unknown channel must be said")
	}
}

func TestSQLLogEntriesUseTheSQLImportersKinds(t *testing.T) {
	m := `"sqlLog":{"errors":[{"instance":"PROD-SENTINEL","kind":"error","number":823,"severity":24,"count":2,"t":"2001-09-09T01:47:10Z"},` +
		`{"instance":"x","kind":"startup","count":1,"t":"2001-09-09T01:47:11Z"},{"instance":"x","kind":"slowio","database":5,"seconds":15,"count":4,"t":"2001-09-09T01:47:12Z"}],` +
		`"health":[{"instance":"x","kind":"deadlock","count":1,"t":"2001-09-09T01:47:13Z"},{"instance":"x","kind":"longwait","count":2,"maxMs":9000,"t":"2001-09-09T01:47:14Z"}]}`
	evs, rep, err := parse(t, groupSample(1, "2001-09-09T01:47:15Z", m), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	er := ofKind(evs, "error")
	if len(er) != 1 || er[0].Attrs["error_number"] != "823" || er[0].Attrs["sql_severity"] != "24" || er[0].Severity != "critical" || *er[0].Value != 2 {
		t.Errorf("error %+v", er)
	}
	if len(ofKind(evs, "start")) != 1 || len(ofKind(evs, "io-stall")) != 1 || len(ofKind(evs, "deadlock")) != 1 {
		t.Error("kinds of the SQL importers are missing")
	}
	if w := ofKind(evs, "wait"); len(w) != 1 || w[0].DurationMs == nil || *w[0].DurationMs != 9000 {
		t.Errorf("wait %+v", w)
	}
	for _, e := range evs {
		for _, v := range e.Attrs {
			if strings.Contains(v, "SENTINEL") {
				t.Errorf("an instance name reached an event: %v", e.Attrs)
			}
		}
	}
	if !contains(rep.KnownGaps, GapSQLInstance) {
		t.Error("the missing instance must be said")
	}
}

func TestDumpFilesShowOnceAndNeverByName(t *testing.T) {
	d1 := `{"name":"w3wp-SENTINEL.dmp","size":123456,"mtime":"2001-09-09T01:47:10Z"}`
	old := `{"name":"old.dmp","size":9,"mtime":"2001-09-08T01:00:00Z"}`
	m := `"dumps":{"dumps":[` + d1 + `,` + old + `]}`
	evs, rep, err := parse(t, groupSample(1, "2001-09-09T01:47:15Z", m)+groupSample(2, "2001-09-09T01:47:30Z", m), importer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	d := ofKind(evs, KindDumpFile)
	if len(d) != 1 || *d[0].Value != 123456 || d[0].Unit != "bytes" || len(d[0].Attrs) != 0 {
		t.Errorf("dumps %+v", d)
	}
	if !contains(rep.KnownGaps, GapDumpsExisting) {
		t.Error("existing dumps must be said")
	}
}
