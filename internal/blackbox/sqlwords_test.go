package blackbox

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDescribeSQLTable(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	good := SQLStatus{Enabled: true, State: "on", LastOK: &now}
	cases := []struct {
		name  string
		svc   SvcState
		err   error
		run   SQLStatus
		state string
		next  string
		grant bool
	}{
		{"not installed", SvcNotInstalled, nil, SQLStatus{State: "off"}, SQLWordNotInstalled, "install-sql", false},
		{"stopped", SvcStopped, nil, SQLStatus{State: "off"}, SQLWordNeedsGrant, "enable-sql", true},
		{"starting", SvcStartPending, nil, SQLStatus{State: "off"}, SQLWordStarting, "", false},
		{"running and reading", SvcRunning, nil, good, SQLWordWorking, "", false},
		{"running, refused", SvcRunning, nil, SQLStatus{Enabled: true, State: "denied", Failures: 3}, SQLWordNeedsGrant, "enable-sql", true},
		{"running, turned itself off", SvcRunning, nil, SQLStatus{State: "auto-off", Failures: 9}, SQLWordTurnedOff, "enable-sql", false},
		{"running, no good read yet", SvcRunning, nil, SQLStatus{Enabled: true, State: "backing-off", Failures: 1}, SQLWordWaiting, "", false},
		{"running, status missing", SvcRunning, nil, SQLStatus{State: "off"}, SQLWordWaiting, "", false},
		{"cannot read the service", SvcStopped, errors.New("access is denied: secret detail"), SQLStatus{}, SQLWordUnknown, "", false},
	}
	for _, tc := range cases {
		w := DescribeSQL(tc.svc, tc.err, tc.run)
		if w.State != tc.state || w.Next != tc.next || w.ShowGrant != tc.grant {
			t.Errorf("%s: %+v", tc.name, w)
		}
		if w.Say == "" || strings.Contains(w.Say, "secret detail") {
			t.Errorf("%s: say %q", tc.name, w.Say)
		}
	}
	// A status word the runner did not write from the fixed list never reaches the text.
	w := DescribeSQL(SvcRunning, nil, SQLStatus{Enabled: true, State: "C:\\secret\\path"})
	if strings.Contains(w.Say, "secret") {
		t.Errorf("a free-text state reached the sentence: %q", w.Say)
	}
}
