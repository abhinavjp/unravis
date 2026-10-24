package mel

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
)

const dmyStart = "      Application started. Press Ctrl+C to shut down.\n"

func consoleStart(date string) string {
	return date + " 10:00:00 info: Microsoft.Hosting.Lifetime[0]\n" + dmyStart
}

func gapContaining(rep importer.Report, sub string) bool {
	for _, g := range rep.KnownGaps {
		if strings.Contains(g, sub) {
			return true
		}
	}
	return false
}

func supplied(o importer.Options, order string) importer.Options {
	o.Supplied = map[string]string{"date-order": order}
	return o
}

func startOf(t *testing.T, evs []evidence.Event) time.Time {
	t.Helper()
	st := byKind(evs, "app-start")
	if len(st) != 1 {
		t.Fatalf("app-start events: %d", len(st))
	}
	return st[0].Start
}

func TestConsoleSlashDateSuppliedDayFirst(t *testing.T) {
	evs, rep, err := collect(t, NewConsole(), consoleStart("05/03/2001"), supplied(baseOpts(time.UTC), "dmy"))
	if err != nil {
		t.Fatal(err)
	}
	if got := startOf(t, evs); !got.Equal(utc("2001-03-05 10:00:00.000")) {
		t.Fatalf("read as %s, want 5 March", got.Format("2006-01-02"))
	}
	if gapContaining(rep, "month first") {
		t.Errorf("a supplied order needs no month-first gap: %q", rep.KnownGaps)
	}
}

func TestConsoleSlashDateSuppliedMonthFirst(t *testing.T) {
	evs, _, err := collect(t, NewConsole(), consoleStart("05/03/2001"), supplied(baseOpts(time.UTC), "mdy"))
	if err != nil {
		t.Fatal(err)
	}
	if got := startOf(t, evs); !got.Equal(utc("2001-05-03 10:00:00.000")) {
		t.Fatalf("read as %s, want 3 May", got.Format("2006-01-02"))
	}
}

func TestConsoleAmbiguousSlashDateIsAGap(t *testing.T) {
	evs, rep, err := collect(t, NewConsole(), consoleStart("05/03/2001"), baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got := startOf(t, evs); !got.Equal(utc("2001-05-03 10:00:00.000")) {
		t.Fatalf("default read as %s, want 3 May", got.Format("2006-01-02"))
	}
	if !gapContaining(rep, "was not stated") || !gapContaining(rep, "month first") {
		t.Fatalf("no date-order gap: %q", rep.KnownGaps)
	}
}

func TestConsoleDayAbove12FixesTheOrder(t *testing.T) {
	data := consoleStart("25/10/2001") + consoleStart("05/03/2001")
	evs, rep, err := collect(t, NewConsole(), data, baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	st := byKind(evs, "app-start")
	if len(st) == 0 {
		t.Fatalf("no app-start events: rejected=%d", rep.Rejected)
	}
	last := st[len(st)-1].Start
	if !last.Equal(utc("2001-03-05 10:00:00.000")) {
		t.Fatalf("second date read as %s, want 5 March", last.Format("2006-01-02"))
	}
	if gapContaining(rep, "was not stated") || gapContaining(rep, "swapped") {
		t.Errorf("order was shown by the file; no gap expected: %q", rep.KnownGaps)
	}
}

func TestConsoleDayFirstAfterAmbiguousRowsIsAGap(t *testing.T) {
	data := consoleStart("05/03/2001") + consoleStart("25/10/2001")
	_, rep, err := collect(t, NewConsole(), data, baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !gapContaining(rep, "swapped") {
		t.Fatalf("earlier month-first rows not flagged: %q", rep.KnownGaps)
	}
}

func TestConsoleSuppliedOrderRejectsDatesThatDoNotFit(t *testing.T) {
	// 10/25/2001 cannot be day first, so a dmy file refuses the row.
	_, rep, err := collect(t, NewConsole(), strings.Repeat(consoleStart("05/03/2001"), 30)+consoleStart("10/25/2001"), supplied(baseOpts(time.UTC), "dmy"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 {
		t.Fatalf("rejected %d rows, want 1", rep.Rejected)
	}
}

func jsonStartLine(ts string) string {
	return `{"Timestamp":"` + ts + `","EventId":0,"LogLevel":"Information","Category":"Microsoft.Hosting.Lifetime",` +
		`"Message":"Application started. Press Ctrl+C to shut down.","State":{}}` + "\n"
}

func TestJSONSlashDateSuppliedDayFirst(t *testing.T) {
	evs, _, err := collect(t, NewJSON(), jsonStartLine("05/03/2001 10:00:00"), supplied(baseOpts(time.UTC), "dmy"))
	if err != nil {
		t.Fatal(err)
	}
	if got := startOf(t, evs); !got.Equal(utc("2001-03-05 10:00:00.000")) {
		t.Fatalf("read as %s, want 5 March", got.Format("2006-01-02"))
	}
	evs, rep, err := collect(t, NewJSON(), jsonStartLine("05/03/2001 10:00:00"), baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got := startOf(t, evs); !got.Equal(utc("2001-05-03 10:00:00.000")) || !gapContaining(rep, "month first") {
		t.Fatalf("default read as %s, gaps %q", got.Format("2006-01-02"), rep.KnownGaps)
	}
}

const beatText = "warn: Microsoft.AspNetCore.Server.Kestrel[22]\n" +
	"      As of \"05/03/2001 10:00:00 +00:00\", the heartbeat has been running for \"00:00:01.5000000\" which is longer than \"00:00:01\".\n"

func TestHeartbeatAsOfSuppliedDayFirst(t *testing.T) {
	evs, _, err := collect(t, NewConsole(), beatText, supplied(baseOpts(time.UTC), "dmy"))
	if err != nil {
		t.Fatal(err)
	}
	hb := byKind(evs, "heartbeat-late")
	if len(hb) != 1 || hb[0].End.Month() != time.March || hb[0].End.Day() != 5 {
		t.Fatalf("heartbeat: %+v", hb)
	}
	evs, rep, err := collect(t, NewConsole(), beatText, baseOpts(time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	hb = byKind(evs, "heartbeat-late")
	if len(hb) != 1 || hb[0].End.Month() != time.May || !gapContaining(rep, "month first") {
		t.Fatalf("default heartbeat: %+v gaps %q", hb, rep.KnownGaps)
	}
}

func TestSlashDateGapsAreNotCausal(t *testing.T) {
	for _, g := range []string{applog.GapDateOrderAssumed, applog.GapDateOrderMixed} {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
}

func TestDateOrderKeyOnEveryMelImporter(t *testing.T) {
	for _, imp := range []importer.Importer{NewConsole(), NewJSON(), NewStdout()} {
		spec, ok := importer.SuppliedKeysOf(imp)["date-order"]
		if !ok {
			t.Errorf("%s does not declare date-order", imp.Name())
			continue
		}
		for _, v := range []string{"mdy", "dmy"} {
			if _, ok := spec.Canonical(v); !ok {
				t.Errorf("%s refuses %q", imp.Name(), v)
			}
		}
		if _, ok := spec.Canonical("ymd"); ok {
			t.Errorf("%s accepts ymd", imp.Name())
		}
	}
}
