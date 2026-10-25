package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// objects returns every schema object as "type name: sql", sorted by name, minus
// the names in skip, so two snapshots can be compared.
func objects(t *testing.T, c *Case, skip ...string) map[string]string {
	t.Helper()
	rows, err := c.db.Query("SELECT type, name, COALESCE(sql, '') FROM sqlite_master")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	out := map[string]string{}
	for rows.Next() {
		var typ, name, sqlText string
		if err := rows.Scan(&typ, &name, &sqlText); err != nil {
			t.Fatal(err)
		}
		if !skipped[name] {
			out[typ+" "+name] = sqlText
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func scalar(t *testing.T, c *Case, q string) int {
	t.Helper()
	var n int
	if err := c.db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// downgradeToV3 makes c look like a case written by the previous build: the two
// schema-4 tables gone, the version 3.
func downgradeToV3(t *testing.T, c *Case) {
	t.Helper()
	for _, stmt := range []string{
		"DROP TABLE case_sources", "DROP TABLE case_envs",
		"UPDATE meta SET v='3' WHERE k='schema_version'",
	} {
		if _, err := c.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

// A case written at schema 3 opens on this build, gains the two tag tables and
// the new version in one step, and every existing read returns what it did. A
// second open is a no-op that keeps what was tagged in between.
func TestVersionThreeCaseMigratesToFourOnce(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Old v3")
	c := mustOpen(t, s, info.ID)
	mustImport(t, c, "keep", 5)
	if schemaOf(t, c) != strconv.Itoa(schemaVersion) || !tableExists(t, c, "case_sources") || !tableExists(t, c, "case_envs") {
		t.Fatal("a new case lacks the tag tables or is not at the current schema")
	}
	downgradeToV3(t, c)

	before := objects(t, c)
	beforeInfo := c.Info()
	beforeImports, err := c.Imports(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvents, err := c.Events(ctx, casestore.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c = mustOpen(t, s, info.ID)
	if v := schemaOf(t, c); v != "4" {
		t.Fatalf("schema version after open = %s, want 4", v)
	}
	if !tableExists(t, c, "case_sources") || !tableExists(t, c, "case_envs") {
		t.Fatal("the migration did not add the tag tables")
	}
	// Nothing existing changed: the same objects with the same SQL, plus the two tables.
	after := objects(t, c, "case_sources", "case_envs", "sqlite_autoindex_case_envs_1")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("existing schema objects changed by the migration:\n before %v\n after  %v", before, after)
	}
	imports, err := c.Imports(ctx)
	if err != nil || !reflect.DeepEqual(imports, beforeImports) {
		t.Errorf("imports changed: %v %v", imports, err)
	}
	events, err := c.Events(ctx, casestore.EventQuery{})
	if err != nil || len(events) != len(beforeEvents) || len(events) != 5 {
		t.Fatalf("events changed: %d (was %d) %v", len(events), len(beforeEvents), err)
	}
	for i := range events {
		if events[i].EvidenceID != beforeEvents[i].EvidenceID || !events[i].Start.Equal(beforeEvents[i].Start) {
			t.Errorf("event %d changed", i)
		}
	}
	gotInfo := c.Info()
	if gotInfo.Envs != 0 || gotInfo.Sources != 0 {
		t.Errorf("a migrated case reports tags: %+v", gotInfo)
	}
	gotInfo.Envs, gotInfo.Sources = beforeInfo.Envs, beforeInfo.Sources
	if !reflect.DeepEqual(gotInfo, beforeInfo) {
		t.Errorf("Info changed:\n before %+v\n after  %+v", beforeInfo, gotInfo)
	}
	if got, err := c.Envs(ctx); err != nil || len(got) != 0 {
		t.Errorf("migrated Envs: %v %v", got, err)
	}
	if got, err := c.Sources(ctx); err != nil || len(got) != 0 {
		t.Errorf("migrated Sources: %v %v", got, err)
	}

	// The migrated case takes tags, and a reopen runs no second migration.
	if err := c.SetEnvs(ctx, []string{"Production", "Staging"}); err != nil {
		t.Fatal(err)
	}
	src, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis", Env: "Production", Portal: "Admin", API: "Orders", ImportID: imports[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if i := c.Info(); i.Envs != 2 || i.Sources != 1 {
		t.Errorf("Info after tagging: %+v", i)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = mustOpen(t, s, info.ID)
	if v := schemaOf(t, c); v != "4" {
		t.Fatalf("schema version after reopen = %s", v)
	}
	if got, _ := c.Envs(ctx); !reflect.DeepEqual(got, []string{"Production", "Staging"}) {
		t.Errorf("Envs after reopen: %v", got)
	}
	if got, _ := c.Sources(ctx); len(got) != 1 || !reflect.DeepEqual(got[0], src) {
		t.Errorf("Sources after reopen: %+v, want %+v", got, src)
	}
	if i := c.Info(); i.Envs != 2 || i.Sources != 1 {
		t.Errorf("Info after reopen: %+v", i)
	}
	if n := scalar(t, c, "SELECT COUNT(*) FROM meta WHERE k='schema_version'"); n != 1 {
		t.Errorf("schema_version rows = %d", n)
	}
}

// Older cases (schema 1 and 2) reach 4 in the same step and gain every table.
func TestOlderCasesMigrateStraightToFour(t *testing.T) {
	ctx := context.Background()
	for _, from := range []string{"1", "2"} {
		s, _ := newTestStore(t)
		info := mustCreate(t, s, "Old")
		c := mustOpen(t, s, info.ID)
		mustImport(t, c, "keep", 3)
		for _, stmt := range []string{
			"DROP TABLE case_sources", "DROP TABLE case_envs", "DROP TABLE discover_watermarks", "DROP TABLE discover_reads",
			"UPDATE meta SET v='" + from + "' WHERE k='schema_version'",
		} {
			if _, err := c.db.ExecContext(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		c = mustOpen(t, s, info.ID)
		for _, tbl := range []string{"case_sources", "case_envs", "discover_watermarks", "discover_reads"} {
			if !tableExists(t, c, tbl) {
				t.Errorf("from %s: table %s missing after migration", from, tbl)
			}
		}
		if v := schemaOf(t, c); v != "4" {
			t.Errorf("from %s: version %s", from, v)
		}
		if recs, err := c.Imports(ctx); err != nil || len(recs) != 1 {
			t.Errorf("from %s: imports %v %v", from, recs, err)
		}
	}
}

// Rollback note: a schema-4 case is refused by a build that knows only schema 3
// (and by any older one), with the existing "written by a newer version" error,
// and a case this build wrote is not refused by this build.
func TestSchemaFourCaseIsRefusedByAVersionThreeBuild(t *testing.T) {
	if schemaVersion != 4 {
		t.Fatalf("schemaVersion = %d, this test is written for 4", schemaVersion)
	}
	if !refusesSchema(4, 3) || !refusesSchema(4, 2) || !refusesSchema(4, 1) {
		t.Error("a build that knows schema 3 would open a schema-4 case")
	}
	if refusesSchema(3, 4) || refusesSchema(4, 4) {
		t.Error("this build refuses a case it can read")
	}

	// The same guard in the real open path: a case one version ahead of this build.
	ctx := context.Background()
	s, _ := newTestStore(t)
	info := mustCreate(t, s, "Ahead")
	c := mustOpen(t, s, info.ID)
	if v := schemaOf(t, c); v != "4" {
		t.Fatalf("a new case is at schema %s, want 4", v)
	}
	if _, err := c.db.ExecContext(ctx, "UPDATE meta SET v=? WHERE k='schema_version'", strconv.Itoa(schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, info.ID); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Open = %v, want ErrNewerSchema", err)
	}
}

// Tags live in the encrypted case database, not beside it: nothing the user typed
// shows up in a file of the case folder in plain form.
func TestTagsAreNotWrittenInPlain(t *testing.T) {
	ctx := context.Background()
	s, root := newTestStore(t)
	info := mustCreate(t, s, "Plain check")
	c := mustOpen(t, s, info.ID)
	if err := c.SetEnvs(ctx, []string{sentinel + "-env"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis", Label: sentinel + "-label", Portal: sentinel + "-portal", API: sentinel + "-api"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, string(info.ID)))
	if err != nil || len(entries) == 0 {
		t.Fatalf("case folder: %v", err)
	}
	for _, e := range entries {
		if hasAny(readFile(t, filepath.Join(root, string(info.ID), e.Name())), []byte(sentinel)) {
			t.Errorf("%s holds a tag in plain form", e.Name())
		}
	}
	c = mustOpen(t, s, info.ID)
	if got, err := c.Sources(ctx); err != nil || len(got) != 1 || got[0].Label != sentinel+"-label" {
		t.Errorf("positive control: tags not readable through the store: %+v %v", got, err)
	}
}
