package store

import (
	"reflect"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// stageHolds evaluates a derived-field equality stage against a row's derived
// fields.
func stageHolds(stage bson.D, derived map[string]any) bool {
	for _, e := range stage {
		if !reflect.DeepEqual(derived[e.Key], e.Value) {
			return false
		}
	}
	return true
}

func TestStateMappingHasOneDefinition(t *testing.T) {
	states := []string{audit.StateComplete, audit.StateIncomplete, audit.StateInvalid, audit.StatePending, audit.StateUnclassified}
	q := queryForTest(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	for _, state := range states {
		stage, ok := stateMatchStage(state)
		if !ok || len(stage) == 0 {
			t.Fatalf("stateMatchStage(%q) has no stage", state)
		}
		stages := drillMatchStages(q, [5]string{}, state, true)
		if got := stages[len(stages)-1]; !reflect.DeepEqual(got, stage) {
			t.Fatalf("drill stage for %s = %v, want %v", state, got, stage)
		}
		filter := q
		filter.Completion = state
		if got := recordPostMatch(filter); !reflect.DeepEqual(got, stage) {
			t.Fatalf("record filter stage for %s = %v, want %v", state, got, stage)
		}
		// The stage selects exactly the rows audit.StateMatches selects.
		for _, rowState := range []string{"valid", "incomplete", "invalid"} {
			for _, pending := range []bool{false, true} {
				for _, outcome := range []string{"success", "unknown", "server_error"} {
					derived := map[string]any{"_auditState": rowState, "_auditPending": pending, "_auditOutcome": outcome}
					if got, want := stageHolds(stage, derived), audit.StateMatches(state, rowState, pending, outcome); got != want {
						t.Fatalf("%s on %v: stage %v, StateMatches %v", state, derived, got, want)
					}
				}
			}
		}
	}
	for _, state := range []string{"", audit.StateAll, "bogus"} {
		if stage, ok := stateMatchStage(state); ok || len(stage) != 0 {
			t.Fatalf("stateMatchStage(%q) = %v, %v; want no stage", state, stage, ok)
		}
	}
	if got := drillMatchStages(q, [5]string{}, audit.StateAll, true); len(got) != 1 {
		t.Fatalf("state all must add no stage, got %v", got)
	}
}

func TestMalformedAuditIDFailsClosed(t *testing.T) {
	q := queryForTest(time.Now().UTC())
	q.AuditID = "not-an-object-id"
	value, ok := matchElem(recordMatch(q), "_id")
	if !ok {
		t.Fatal("a malformed audit id must still constrain _id, not widen the match")
	}
	condition, isOps := value.(bson.D)
	if !isOps || len(condition) != 1 || condition[0].Key != "$in" {
		t.Fatalf("_id condition = %#v, want an empty $in", value)
	}
	if list, isList := condition[0].Value.(bson.A); !isList || len(list) != 0 {
		t.Fatalf("_id $in = %#v, want empty", condition[0].Value)
	}
	doc := map[string]any{"_id": bson.NewObjectID()}
	if evalFirstMatch(t, recordMatch(q), doc) {
		t.Fatal("a malformed audit id matched a row")
	}
	q.AuditID = "0123456789abcdef01234567"
	value, _ = matchElem(recordMatch(q), "_id")
	if _, isID := value.(bson.ObjectID); !isID {
		t.Fatalf("a valid audit id must match as an ObjectID, got %#v", value)
	}
}

func TestStatusCodeNumberBranchNeedsCanonicalDigits(t *testing.T) {
	hasInt := func(code string) bool {
		q := queryForTest(time.Now().UTC())
		q.StatusCodes = []string{code}
		value, _ := matchElem(recordMatch(q), "StatusCode")
		for _, item := range value.(bson.D)[0].Value.(bson.A) {
			if _, isInt := item.(int); isInt {
				return true
			}
		}
		return false
	}
	for _, code := range []string{"404", " 404 "} {
		if !hasInt(code) {
			t.Errorf("%q must also match a numeric StatusCode", code)
		}
	}
	for _, code := range []string{"0404", "+404", "4o4", "NotFound"} {
		if hasInt(code) {
			t.Errorf("%q must not match a numeric StatusCode", code)
		}
	}
	// A stored number 404 is the text "404" on the legacy and demo paths, so a
	// code that is not that text matches on none of the three.
	q := queryForTest(time.Now().UTC())
	doc := map[string]any{"StatusCode": int32(404)}
	for code, want := range map[string]bool{"404": true, "0404": false, "+404": false} {
		q.StatusCodes = []string{code}
		if got := evalFirstMatch(t, recordMatch(q), doc); got != want {
			t.Errorf("modern %q on int 404 = %v, want %v", code, got, want)
		}
		if got := audit.RecordFilterMatches(q, audit.RecordFacts{Status: "404"}); got != want {
			t.Errorf("legacy and demo %q on 404 = %v, want %v", code, got, want)
		}
	}
}
