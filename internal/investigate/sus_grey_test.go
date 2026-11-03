package investigate

import (
	"strings"
	"testing"
)

var nineIDs = []string{"S8", "S9", "S10", "S11", "S12", "D16", "D17", "M11", "M12"}

// noDataInput is a full input with every source the nine need taken away.
func noDataInput() Input {
	in := fullInput()
	for i := range in.Requests {
		in.Requests[i].P = nil
	}
	in.HTTPErrPresent = false
	in.HTTPErr = nil
	in.IISPresent = false
	in.IIS = nil
	in.Lanes = nil
	return in
}

func TestNineGreyWithoutData(t *testing.T) {
	r := Analyze(noDataInput())
	for _, id := range nineIDs {
		wantSuspect(t, r, id, ResCannotTest, Grey)
	}
	has := func(id, want string) {
		t.Helper()
		if why := suspect(t, r, id).Why; !strings.Contains(why, want) {
			t.Errorf("%s why = %q, want it to contain %q", id, why, want)
		}
	}
	has("S8", "no PayloadRecordTime in the audit")
	has("S9", "no IIS data")
	has("S10", "no HTTPERR log imported")
	has("S11", "no HTTPERR log imported")
	for _, id := range []string{"D16", "D17", "M11", "M12"} {
		has(id, "It needs:")
	}
	t.Logf("S12 why = %q", suspect(t, r, "S12").Why)
}

func TestS9GreyWithoutSubStatus(t *testing.T) {
	in := fullInput()
	in.IISPresent = true
	in.IISSubStatus = false
	in.IIS = []IISRow{
		{Start: at(10, 30), End: at(10, 30), TakenMs: 10, Path: "/a", Status: "404"},
		{Start: at(10, 31), End: at(10, 31), TakenMs: 12, Path: "/b", Status: "404"},
	}
	r := Analyze(in)
	wantSuspect(t, r, "S9", ResCannotTest, Grey)
	if why := suspect(t, r, "S9").Why; !strings.Contains(why, "the IIS rows carry no sub-status") {
		t.Errorf("S9 why = %q", why)
	}
}

func TestNineNeverGreenWithoutData(t *testing.T) {
	r := Analyze(noDataInput())
	for _, id := range nineIDs {
		s := suspect(t, r, id)
		if s.Colour == Green || s.Result == ResContra {
			t.Errorf("%s = %s/%s with no data", id, s.Result, s.Colour)
		}
	}
}
