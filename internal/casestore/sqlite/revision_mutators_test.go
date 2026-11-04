package sqlite

import (
	"context"
	"io"
	"reflect"
	"sort"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// The case revision guards the case export (a confirm answers 409 when the case
// changed after the preview). The export reads exactly two things from the
// store: Imports and Events (see server.buildCaseExport and evidence.CaseInput).
// The case view also lists tags, environments and the cookie policy, so those
// writes bump too. Every other write is deliberately left out, and this file is
// the list of who bumps and who does not, with the reason.

// revisionRule says what one mutating method does to the revision. act does the
// setup and returns the closure whose effect is measured.
type revisionRule struct {
	bumps bool
	why   string
	act   func(t *testing.T, c *Case) func()
}

func mustNoErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func revisionRules() map[string]revisionRule {
	ctx := context.Background()
	return map[string]revisionRule{
		"Import": {true, "adds an import record and its events: both are exported", func(t *testing.T, c *Case) func() {
			return func() { mustImport(t, c, "bump", 2) }
		}},
		"Import/quarantine": {false, "a refused file lands in the quarantine table only; the export reads Imports and Events, and the refused file has no import row or event", func(t *testing.T, c *Case) func() {
			mustImport(t, c, "seed", 1)
			return func() {
				_, err := c.Import(ctx, tMeta("refused.csv"), tBody("refused"), func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
					_, _ = io.Copy(io.Discard, r)
					return importer.Report{Importer: testImporter, Rows: 1}, &importer.QuarantineError{Reason: importer.ReasonFieldCount, Column: "c1"}
				})
				if err == nil {
					t.Error("a quarantined import returned no error")
				}
			}
		}},
		"SetEnvs": {true, "environment list shown with the case", func(t *testing.T, c *Case) func() {
			return func() { mustNoErr(t, "SetEnvs", c.SetEnvs(ctx, []string{"Production"})) }
		}},
		"AddSource": {true, "source tags are part of the case", func(t *testing.T, c *Case) func() {
			return func() {
				_, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis"})
				mustNoErr(t, "AddSource", err)
			}
		}},
		"UpdateSourceTags": {true, "source tags are part of the case", func(t *testing.T, c *Case) func() {
			src, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis"})
			mustNoErr(t, "AddSource", err)
			return func() {
				_, err := c.UpdateSourceTags(ctx, src.ID, "Production", "", "")
				mustNoErr(t, "UpdateSourceTags", err)
			}
		}},
		"RemoveSource": {true, "source tags are part of the case", func(t *testing.T, c *Case) func() {
			src, err := c.AddSource(ctx, casestore.SourceTag{Kind: "iis"})
			mustNoErr(t, "AddSource", err)
			return func() { mustNoErr(t, "RemoveSource", c.RemoveSource(ctx, src.ID)) }
		}},
		"SetCookieAllowlist": {true, "the cookie policy decides which cookie digests the events keep", func(t *testing.T, c *Case) func() {
			return func() {
				_, err := c.SetCookieAllowlist(ctx, []string{"sid"})
				mustNoErr(t, "SetCookieAllowlist", err)
			}
		}},
		"SaveView": {false, "saved views live in the workspace, not in the export", func(t *testing.T, c *Case) func() {
			return func() {
				_, err := c.SaveView(ctx, casestore.SavedView{Name: "v", Tool: "filters", Settings: []byte(`{"route":"A.B"}`)})
				mustNoErr(t, "SaveView", err)
			}
		}},
		"DeleteView": {false, "saved views live in the workspace, not in the export", func(t *testing.T, c *Case) func() {
			v, err := c.SaveView(ctx, casestore.SavedView{Name: "v", Tool: "filters", Settings: []byte(`{}`)})
			mustNoErr(t, "SaveView", err)
			return func() { mustNoErr(t, "DeleteView", c.DeleteView(ctx, v.ID)) }
		}},
		"PutChecklistItem": {false, "the checklist is a working note, not exported", func(t *testing.T, c *Case) func() {
			return func() {
				_, err := c.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "item", Order: casestore.OrderAuto})
				mustNoErr(t, "PutChecklistItem", err)
			}
		}},
		"DeleteChecklistItem": {false, "the checklist is a working note, not exported", func(t *testing.T, c *Case) func() {
			it, err := c.PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "item", Order: casestore.OrderAuto})
			mustNoErr(t, "PutChecklistItem", err)
			return func() { mustNoErr(t, "DeleteChecklistItem", c.DeleteChecklistItem(ctx, it.ID)) }
		}},
		"ReplaceRetries": {false, "the retry list is a to-do list for failed work, not exported", func(t *testing.T, c *Case) func() {
			return func() {
				_, err := c.ReplaceRetries(ctx, []casestore.RetryItem{{Kind: "import", Key: "k1", Label: "a.csv", Reason: "stopped"}})
				mustNoErr(t, "ReplaceRetries", err)
			}
		}},
		"NoteRetry": {false, "the retry list is a to-do list for failed work, not exported", func(t *testing.T, c *Case) func() {
			return func() {
				_, err := c.NoteRetry(ctx, casestore.RetryItem{Kind: "import", Key: "k2", Label: "b.csv", Reason: "stopped"})
				mustNoErr(t, "NoteRetry", err)
			}
		}},
		"RemoveRetry": {false, "the retry list is a to-do list for failed work, not exported", func(t *testing.T, c *Case) func() {
			it, err := c.NoteRetry(ctx, casestore.RetryItem{Kind: "import", Key: "k3", Label: "c.csv", Reason: "stopped"})
			mustNoErr(t, "NoteRetry", err)
			return func() { mustNoErr(t, "RemoveRetry", c.RemoveRetry(ctx, it.ID)) }
		}},
		"AddHealthRun": {false, "health runs are a log of checks, not exported", func(t *testing.T, c *Case) func() {
			return func() {
				_, err := c.AddHealthRun(ctx, casestore.HealthRun{Kind: "sql", Label: "h", Status: "ok", Report: []byte(`{}`)})
				mustNoErr(t, "AddHealthRun", err)
			}
		}},
		"SetLedgerBlob": {false, "the ledger blob is the case's own ledger file, outside the case export", func(t *testing.T, c *Case) func() {
			return func() { mustNoErr(t, "SetLedgerBlob", c.SetLedgerBlob(ctx, []byte(`{"b":2}`))) }
		}},
		"SetWorkspaceBlob": {false, "the workspace blob is the user's screen state, not exported", func(t *testing.T, c *Case) func() {
			return func() { mustNoErr(t, "SetWorkspaceBlob", c.SetWorkspaceBlob(ctx, []byte(`{"a":1}`))) }
		}},
		"PutDiscoverMark": {false, "discovery watermarks are a read position, not case content", func(t *testing.T, c *Case) func() {
			return func() {
				mustNoErr(t, "PutDiscoverMark", c.PutDiscoverMark(ctx, casestore.DiscoverFileKey{VolumeSerial: 1, FileIndex: 2}, casestore.DiscoverMark{Offset: 5}))
			}
		}},
		"DeleteDiscoverMark": {false, "discovery watermarks are a read position, not case content", func(t *testing.T, c *Case) func() {
			k := casestore.DiscoverFileKey{VolumeSerial: 1, FileIndex: 2}
			mustNoErr(t, "PutDiscoverMark", c.PutDiscoverMark(ctx, k, casestore.DiscoverMark{Offset: 5}))
			return func() { mustNoErr(t, "DeleteDiscoverMark", c.DeleteDiscoverMark(ctx, k)) }
		}},
		"RecordDiscoverRead": {false, "the discovery read log is not case content", func(t *testing.T, c *Case) func() {
			return func() {
				mustNoErr(t, "RecordDiscoverRead", c.RecordDiscoverRead(ctx, casestore.DiscoverRead{At: time.Now(), Detector: "d", Importer: "i", Kind: "file", Display: "x"}))
			}
		}},
	}
}

// revisionReaders are the exported methods of *Case that change nothing the
// export or the case view reads. Close ends the handle; it writes no content.
var revisionReaders = map[string]bool{
	"Info": true, "Close": true, "Pseudonymize": true, "IntegrityCheck": true, "Quarantines": true,
	"Imports": true, "Events": true, "BucketCounts": true, "Each": true, "CookiePolicy": true,
	"DiscoverKey": true, "DiscoverMark": true, "DiscoverReads": true, "HealthRuns": true, "LedgerBlob": true,
	"SavedViews": true, "ChecklistItems": true, "Retries": true, "Revision": true, "Envs": true,
	"Sources": true, "WorkspaceBlob": true,
}

// TestRevisionMutatorsAreAllClassified fails when *Case gains an exported
// method that is in neither list, so a new write cannot skip the decision.
func TestRevisionMutatorsAreAllClassified(t *testing.T) {
	rules := revisionRules()
	known := map[string]bool{}
	for name := range rules {
		base := name
		for i := range name {
			if name[i] == '/' {
				base = name[:i]
				break
			}
		}
		known[base] = true
	}
	for name := range revisionReaders {
		if known[name] {
			t.Errorf("%s is listed both as a reader and as a mutator", name)
		}
		known[name] = true
	}
	typ := reflect.TypeOf(&Case{})
	have := map[string]bool{}
	var missing []string
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		have[name] = true
		if !known[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("Case methods not classified for the revision (add to revisionRules or revisionReaders): %v", missing)
	}
	for name := range known {
		if !have[name] {
			t.Errorf("%s is classified but *Case has no such method (stale entry)", name)
		}
	}
}

// TestRevisionMutatorMatrix runs every mutator once and checks that the
// revision moved exactly when the rule says it must.
func TestRevisionMutatorMatrix(t *testing.T) {
	rules := revisionRules()
	names := make([]string, 0, len(rules))
	for name := range rules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rule := rules[name]
		t.Run(name, func(t *testing.T) {
			c := newRevisionCase(t, "matrix")
			act := rule.act(t, c)
			before := mustRevision(t, c)
			importsBefore, eventsBefore := exportedShape(t, c)
			act()
			after := mustRevision(t, c)
			importsAfter, eventsAfter := exportedShape(t, c)
			switch {
			case rule.bumps && after <= before:
				t.Errorf("revision %d -> %d: %s must bump (%s)", before, after, name, rule.why)
			case !rule.bumps && after != before:
				t.Errorf("revision %d -> %d: %s must not bump (%s)", before, after, name, rule.why)
			}
			if !rule.bumps && (importsAfter != importsBefore || eventsAfter != eventsBefore) {
				t.Errorf("%s left the revision alone but changed exported content: imports %d -> %d, events %d -> %d",
					name, importsBefore, importsAfter, eventsBefore, eventsAfter)
			}
		})
	}
}

// exportedShape counts what the case export reads: import records and events.
func exportedShape(t *testing.T, c *Case) (imports, events int) {
	t.Helper()
	ctx := context.Background()
	recs, err := c.Imports(ctx)
	mustNoErr(t, "Imports", err)
	evs, err := c.Events(ctx, casestore.EventQuery{Limit: casestore.MaxEventQuery})
	mustNoErr(t, "Events", err)
	return len(recs), len(evs)
}
