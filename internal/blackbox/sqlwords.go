package blackbox

import "time"

// B3 (read-only part): the SQL status in plain words, and the one next step. Nothing here connects to SQL, starts a
// service or runs a grant (rule C14: the Blackbox prints the grant, the owner runs it). The words come from two things
// that are already safe to read: the state of the SQL service, and the public status the SQL runner writes (the four
// fields of SQLStatus).
//
// Why no automatic start: a safe pre-check that the grant is in place would need an offline connect with the service
// account, and a start before the grant latches a denial that survives restarts (W8-03). Until that design is settled
// (docs/blackbox/WHOLE-STACK.md 4.2), the owner's grant and `enable-sql` stay the way in.

// Words of SQLWords.State: one fixed word for each situation.
const (
	SQLWordNotInstalled = "not-installed" // the SQL service is not there
	SQLWordNeedsGrant   = "needs-grant"   // installed, stopped (or refused): the owner runs the grant, then enable-sql
	SQLWordStarting     = "starting"      // the service is changing state
	SQLWordWorking      = "ok"            // running and the runner reports a good read
	SQLWordWaiting      = "waiting"       // running, but the runner is backing off or has no good read yet
	SQLWordTurnedOff    = "turned-off"    // running, the runner turned itself off after failures: owner action
	SQLWordUnknown      = "unknown"       // the service state could not be read
)

// SQLWords is the answer of `unravis-blackbox sql-status`.
type SQLWords struct {
	State string `json:"state"`
	Say   string `json:"say"`
	Next  string `json:"next,omitempty"` // the one command or action that moves it on
	// ShowGrant is true when the grant text is the useful next thing to show.
	ShowGrant bool       `json:"showGrant"`
	Service   string     `json:"service"`
	Runner    SQLStatus  `json:"runner"`
	LastOK    *time.Time `json:"lastOK,omitempty"`
}

// ReadSQLStatus reads the SQL runner's public status for the Blackbox at folder (see readSQLStatus for the rules).
func ReadSQLStatus(folder string, now time.Time) SQLStatus { return readSQLStatus(folder, now) }

// DescribeSQL turns the service state and the runner's public status into plain words. svcErr is a failure to read the
// service state. It is a pure function so the whole table is testable on any platform.
func DescribeSQL(svc SvcState, svcErr error, run SQLStatus) SQLWords {
	w := SQLWords{Service: svc.String(), Runner: run, LastOK: run.LastOK}
	if svcErr != nil {
		w.Service = "unknown"
		w.State, w.Say = SQLWordUnknown, "The SQL service could not be read. Run this as an administrator."
		return w
	}
	switch svc {
	case SvcNotInstalled:
		w.State = SQLWordNotInstalled
		w.Say = "Deep SQL recording is off: the SQL service is not installed."
		w.Next = "install-sql"
	case SvcStopped:
		w.State = SQLWordNeedsGrant
		w.Say = "The SQL service is installed and stopped. Run the grant in SSMS, then enable-sql."
		w.Next = "enable-sql"
		w.ShowGrant = true
	case SvcStartPending, SvcStopPending:
		w.State, w.Say = SQLWordStarting, "The SQL service is changing state. Read this again in a moment."
	case SvcRunning:
		switch {
		case run.State == "denied":
			w.State = SQLWordNeedsGrant
			w.Say = "SQL refused the login. Run the grant in SSMS, then enable-sql."
			w.Next, w.ShowGrant = "enable-sql", true
		case run.State == "auto-off":
			w.State = SQLWordTurnedOff
			w.Say = "The SQL reader turned itself off after repeated failures. Fix the cause, then enable-sql."
			w.Next = "enable-sql"
		case run.Enabled && run.State == "on" && run.LastOK != nil:
			w.State, w.Say = SQLWordWorking, "SQL is being recorded."
		default:
			w.State = SQLWordWaiting
			w.Say = "The SQL service is running but has not had a good read yet (state " + safeWord(run.State) + ")."
		}
	default:
		w.State, w.Say = SQLWordUnknown, "The SQL service is in a state this version does not know."
	}
	return w
}

// safeWord keeps the runner's state word only when it is one of the fixed ones; anything else prints as "unknown".
func safeWord(s string) string {
	switch s {
	case "off", "on", "backing-off", "auto-off", "denied":
		return s
	}
	return "unknown"
}
