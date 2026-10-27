package casestore

import (
	"errors"
	"testing"
	"time"
)

func TestCheckBeliefTable(t *testing.T) {
	O, C, Ag, Ad := StateOpen, StateClosed, StateArchiving, StateArchived
	rows := []struct {
		name            string
		a               Action
		believed, found State
		want            error
	}{
		// the nine rows of spec v7 section 9.1
		{"open presses close, found closed", ActionClose, O, C, ErrCaseChanged},
		{"open presses close, found archiving", ActionClose, O, Ag, ErrCaseChanged},
		{"open presses close, found archived", ActionClose, O, Ad, ErrCaseChanged},
		{"closed presses reopen, found open", ActionReopen, C, O, ErrCaseChanged},
		{"closed presses reopen, found archiving", ActionReopen, C, Ag, ErrCaseChanged},
		{"closed presses reopen, found archived", ActionReopen, C, Ad, ErrCaseChanged},
		{"open presses archive, found closed", ActionArchive, O, C, nil},
		{"open presses archive, found archiving", ActionArchive, O, Ag, ErrArchiveRunning},
		{"closed presses archive, found archiving", ActionArchive, C, Ag, ErrArchiveRunning},
		{"open presses archive, found archived", ActionArchive, O, Ad, ErrCaseChanged},
		{"closed presses archive, found archived", ActionArchive, C, Ad, ErrCaseChanged},
		{"archiving presses cancel, found archived", ActionCancelArchive, Ag, Ad, ErrCaseChanged},
		{"archiving presses cancel, found prior state", ActionCancelArchive, Ag, C, ErrCaseChanged},
		{"kept presses restore, found closed", ActionRestore, Ad, C, ErrCaseChanged},
		{"kept presses free, found closed", ActionFree, Ad, C, ErrCaseChanged},
		{"closed presses erase, found archiving", ActionErase, C, Ag, ErrCancelArchiveFirst},
		// the moves that agree
		{"open closes an open case", ActionClose, O, O, nil},
		{"closed reopens a closed case", ActionReopen, C, C, nil},
		{"open archives an open case", ActionArchive, O, O, nil},
		{"closed archives a closed case", ActionArchive, C, C, nil},
		{"archiving cancels", ActionCancelArchive, Ag, Ag, nil},
		{"kept restores", ActionRestore, Ad, Ad, nil},
		{"kept frees", ActionFree, Ad, Ad, nil},
		// exceptions
		{"archive believed archived is stale", ActionArchive, Ad, O, ErrCaseChanged},
		{"erase accepts any belief (open)", ActionErase, O, C, nil},
		{"erase accepts any belief (archived)", ActionErase, O, Ad, nil},
		{"erase of an erased case is not found", ActionErase, C, StateErased, ErrNotFound},
		{"restore as new needs archived", ActionRestoreAsNew, O, Ad, nil},
		{"restore as new ignores belief", ActionRestoreAsNew, "", Ad, nil},
		{"restore as new on a closed case", ActionRestoreAsNew, Ad, C, ErrCaseChanged},
		{"import into open", ActionAddImport, O, O, nil},
		{"import into closed", ActionAddImport, O, C, ErrCaseClosed},
		{"import into archiving", ActionAddImport, O, Ag, ErrCaseClosed},
		{"import into archived", ActionAddImport, O, Ad, ErrCaseClosed},
		// no belief supplied (an older client): the state still has to fit
		{"no belief, close an open case", ActionClose, "", O, nil},
		{"no belief, close a closed case", ActionClose, "", C, ErrCaseChanged},
		{"no belief, reopen a closed case", ActionReopen, "", C, nil},
		{"no belief, reopen an open case", ActionReopen, "", O, ErrCaseChanged},
		{"unknown action", Action("nope"), O, O, ErrCaseChanged},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got := CheckBelief(r.a, r.believed, r.found)
			if !errors.Is(got, r.want) || (r.want == nil) != (got == nil) {
				t.Fatalf("CheckBelief(%s, %q, %q) = %v, want %v", r.a, r.believed, r.found, got, r.want)
			}
		})
	}
}

func TestCheckMark(t *testing.T) {
	if CheckMark("") != nil || CheckMark(MarkArchiving) != nil {
		t.Fatal("an archiving or empty mark must not refuse a call by itself")
	}
	if !errors.Is(CheckMark(MarkFreeing), ErrFreeing) || !errors.Is(CheckMark(MarkErasing), ErrErasing) {
		t.Fatal("freeing must answer M19 and erasing M5")
	}
}

func TestStateOf(t *testing.T) {
	now := time.Now()
	cases := []struct {
		i    CaseInfo
		want State
	}{
		{CaseInfo{}, StateOpen},
		{CaseInfo{Closed: &now}, StateClosed},
		{CaseInfo{Closed: &now, Mark: MarkArchiving}, StateArchiving},
		{CaseInfo{Mark: MarkArchiving}, StateArchiving},
		{CaseInfo{Untrusted: true, Closed: &now}, StateUntrusted},
	}
	for _, c := range cases {
		if got := StateOf(c.i); got != c.want {
			t.Errorf("StateOf(%+v) = %q, want %q", c.i, got, c.want)
		}
	}
	if _, ok := ParseState(""); ok {
		t.Error("empty is not a state")
	}
	if s, ok := ParseState("closed"); !ok || s != StateClosed {
		t.Error("closed must parse")
	}
	if _, ok := ParseState("OPEN"); ok {
		t.Error("state words are lower case")
	}
}
