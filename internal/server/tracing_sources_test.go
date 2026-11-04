package server

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
)

// The sources the tracing answer names: the open audit sources, the audit sources
// each lane read, and the audit databases the case saved that no open connection
// carries.

func seedAuditDatabase(t *testing.T, e *caseEnv, id, name, class string) casestore.SourceTag {
	t.Helper()
	return seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: mongoDatabaseKind, Label: name, Classification: class})
}

func refIDs(refs []tracingRef) string {
	ids := make([]string, len(refs))
	for i, r := range refs {
		ids[i] = r.SourceID
	}
	return strings.Join(ids, ",")
}

func TestTracingNamesOpenAuditSourcesAndWhatTheLaneRead(t *testing.T) {
	rows := []auditRow{{time.Date(2001, 2, 3, 4, 0, 30, 0, time.UTC), ""}}
	f := &auditFake{sources: auditSources("a1", "a2"), rows: rows}
	e, id := auditHandlerEnv(t, f)
	out, raw := tracingPost(t, e, id, tracingBody(""))
	if refIDs(out.AuditSources) != "a1,a2" || out.AuditSources[1].Label != "Label a2" {
		t.Errorf("auditSources = %+v", out.AuditSources)
	}
	lane := tracingLaneOf(t, out, "audit")
	if refIDs(lane.SourcesRead) != "a1,a2" || lane.SourcesRead[0].Label != "Label a1" {
		t.Errorf("sourcesRead = %+v", lane.SourcesRead)
	}
	if len(out.SavedNotConnected) != 0 || len(lane.SavedNotConnected) != 0 {
		t.Errorf("nothing was saved, got %v / %v", out.SavedNotConnected, lane.SavedNotConnected)
	}
	// Nothing saved and nothing missing: the optional fields are not on the wire.
	if strings.Contains(raw, `"savedNotConnected"`) {
		t.Errorf("savedNotConnected is on the wire: %s", raw)
	}
	// The sql and iis lanes name no audit sources.
	if sql := tracingLaneOf(t, out, "sql"); len(sql.SourcesRead) != 0 {
		t.Errorf("sql sourcesRead = %+v", sql.SourcesRead)
	}

	// Naming one source reads and names that one; the whole listing is still named.
	f = &auditFake{sources: auditSources("a1", "a2"), rows: rows}
	e, id = auditHandlerEnv(t, f)
	out, _ = tracingPost(t, e, id, tracingBody(`"sourceIds":["a2"]`))
	if refIDs(out.AuditSources) != "a1,a2" {
		t.Errorf("a2 only: auditSources = %+v", out.AuditSources)
	}
	if lane := tracingLaneOf(t, out, "audit"); refIDs(lane.SourcesRead) != "a2" {
		t.Errorf("a2 only: sourcesRead = %+v", lane.SourcesRead)
	}
	if f.listCalls != 1 {
		t.Errorf("%d listing calls, want one", f.listCalls)
	}
}

func TestTracingSourcesReadLeavesOutAFailedSource(t *testing.T) {
	f := &auditFake{
		sources: auditSources("a1", "a2"),
		results: []audit.SourceResult{{SourceID: "a1", OK: true}, {SourceID: "a2", OK: false, ErrorCode: "error"}},
	}
	e, id := auditHandlerEnv(t, f)
	out, raw := tracingPost(t, e, id, tracingBody(""))
	lane := tracingLaneOf(t, out, "audit")
	if refIDs(lane.SourcesRead) != "a1" {
		t.Errorf("sourcesRead = %+v, want only a1", lane.SourcesRead)
	}
	if refIDs(lane.Sources) != "a1,a2" {
		t.Errorf("sources = %+v, want both asked", lane.Sources)
	}
	if strings.Contains(raw, "secretdb") {
		t.Errorf("body holds a live database name: %s", raw)
	}
}

func TestTracingSavedAuditDatabaseNotConnected(t *testing.T) {
	rows := []auditRow{{time.Date(2001, 2, 3, 4, 0, 30, 0, time.UTC), ""}}
	f := &auditFake{sources: auditSources("a1"), rows: rows} // its database is "secretdb"
	e, id := auditHandlerEnv(t, f)
	seedAuditDatabase(t, e, id, "orders_audit", casestore.ClassAudit)
	seedAuditDatabase(t, e, id, "Orders_Audit", casestore.ClassAudit) // the same name again
	seedAuditDatabase(t, e, id, "secretdb", casestore.ClassAudit)     // open: carried by a1
	seedAuditDatabase(t, e, id, "scratch", casestore.ClassOther)      // not an audit database
	seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis", Label: "web-a"})

	out, raw := tracingPost(t, e, id, tracingBody(""))
	if got := strings.Join(out.SavedNotConnected, "|"); got != "orders_audit" {
		t.Errorf("response savedNotConnected = %q", got)
	}
	lane := tracingLaneOf(t, out, "audit")
	if got := strings.Join(lane.SavedNotConnected, "|"); got != "orders_audit" {
		t.Errorf("lane savedNotConnected = %q", got)
	}
	// The connected audit source is still read, and the database tags are not
	// case files: the only source is the iis tag, with no "no file" warning for
	// the databases.
	if lane.State != calltrace.StateEvents || refIDs(lane.SourcesRead) != "a1" {
		t.Errorf("audit lane = %+v", lane)
	}
	if len(out.Sources) != 1 || out.Sources[0].Label != "web-a" {
		t.Errorf("response sources = %+v, want only the iis tag", out.Sources)
	}
	// The iis tag has no file, so that one warning is expected, once.
	if n := countWarning(out.Warnings, tracingNoFileWarn); n != 1 {
		t.Errorf("%d no-file warnings, want 1 (the iis tag): %v", n, out.Warnings)
	}
	if strings.Contains(raw, "scratch") {
		t.Errorf("an other-class database is named: %s", raw)
	}

	// Naming an audit source keeps the list: the picker still shows what is saved.
	out, _ = tracingPost(t, e, id, tracingBody(`"sourceIds":["a1"]`))
	if got := strings.Join(out.SavedNotConnected, "|"); got != "orders_audit" {
		t.Errorf("a1 only: savedNotConnected = %q", got)
	}
}

func countWarning(warnings []string, want string) int {
	n := 0
	for _, w := range warnings {
		if w == want {
			n++
		}
	}
	return n
}

func TestTracingSavedDatabasesWithNothingOpenGiveANoDataAuditLane(t *testing.T) {
	f := &auditFake{sources: nil}
	e, id := auditHandlerEnv(t, f)
	seedAuditDatabase(t, e, id, "orders_audit", casestore.ClassAudit)
	out, _ := tracingPost(t, e, id, tracingBody(""))
	if laneKeys(out) != "audit,sql,iis" {
		t.Fatalf("lanes %s, want audit,sql,iis", laneKeys(out))
	}
	lane := tracingLaneOf(t, out, "audit")
	if lane.State != calltrace.StateNoData || !lane.Complete || lane.Reason != tracingAuditSavedOnlyMsg ||
		len(lane.Buckets) != 0 || len(lane.SourcesRead) != 0 || strings.Join(lane.SavedNotConnected, "|") != "orders_audit" {
		t.Errorf("audit lane = %+v", lane)
	}
	if len(f.plans) != 0 {
		t.Errorf("%d Rollup calls with nothing open", len(f.plans))
	}
	if len(out.Sources) != 0 || countWarning(out.Warnings, tracingNoFileWarn) != 0 {
		t.Errorf("a saved database is not a file source: sources %+v warnings %v", out.Sources, out.Warnings)
	}

	// No saved database and nothing open: no audit lane, as before.
	e, id = auditHandlerEnv(t, &auditFake{})
	out, _ = tracingPost(t, e, id, tracingBody(""))
	if laneKeys(out) != "sql,iis" {
		t.Errorf("lanes %s, want sql,iis", laneKeys(out))
	}
}

func TestTracingFileOnlySelectionMakesNoAuditListing(t *testing.T) {
	f := &auditFake{sources: auditSources("a1")}
	e, id := auditHandlerEnv(t, f)
	seedAuditDatabase(t, e, id, "orders_audit", casestore.ClassAudit)
	tag := seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis", Label: "web-a"})
	out, raw := tracingPost(t, e, id, tracingBody(`"sourceIds":["`+tracingTagID(tag.ID)+`"]`))
	if f.listCalls != 0 || len(f.plans) != 0 || laneKeys(out) != "sql,iis" {
		t.Errorf("lanes %s, %d list calls, %d Rollup calls", laneKeys(out), f.listCalls, len(f.plans))
	}
	if len(out.AuditSources) != 0 || len(out.SavedNotConnected) != 0 || strings.Contains(raw, `"auditSources"`) || strings.Contains(raw, `"savedNotConnected"`) {
		t.Errorf("a file-only request names audit sources: %s", raw)
	}
}

func TestSavedNotConnectedNames(t *testing.T) {
	items := []audit.Source{{ID: "a1", Label: "Payments", Database: "pay_db"}, {ID: "a2", Label: "x", Database: ""}}
	for _, tc := range []struct {
		name  string
		saved []string
		want  string
	}{
		{"none saved", nil, ""},
		{"all open by database", []string{"pay_db", "PAY_DB"}, ""},
		{"open by label, other case", []string{"payments"}, ""},
		{"missing kept in order", []string{"b", "pay_db", "a", "B"}, "b|a"},
		{"blank dropped", []string{"", "  ", "c"}, "c"},
	} {
		if got := strings.Join(savedNotConnectedNames(tc.saved, items), "|"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := savedNotConnectedNames([]string{"a"}, nil); len(got) != 1 {
		t.Errorf("nothing open: %v", got)
	}
}
