package dbhealth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The live probe for BL-016 (tools/dev/qsoffprobe): one question asked of a real SQL Server, "do the Query Store views return rows while the
// state is OFF on this build?". It runs the SAME read the app runs (RunSQLLane) and adds only fixed read-only selects of its own (the server
// version and the Query Store options row, to print the desired state). Nothing here switches Query Store on or off or writes to the server;
// the probe wraps its connection in a guard that refuses any text that is not a plain read, so a later edit cannot add a write by accident.

// QSProbe is what one probe run found. Zero values mean "not read" and are printed as such.
type QSProbe struct {
	Database string
	Version  string
	Edition  string
	// BaseErr is set when the version could not be read.
	BaseErr string
	// OptionsRead is true when the Query Store options row was read; Actual and Desired are then its values.
	OptionsRead bool
	Actual      string
	Desired     string
	// Check is the Query Store check of the lane, the same sentence and state the app shows.
	Check LaneCheck
	// State is the lane's Query Store state ("off", "on", ...); empty when the lane gave none.
	State     string
	FirstData *time.Time
	LastData  *time.Time
	// Points is how many Query Store blocks were read in the windows asked; Executions is the sum of their executions.
	Points     int
	Executions int64
	// Refused is how many query texts the read-only guard refused (always 0 unless the code changed).
	Refused int
}

// Verdicts of the probe, one plain line each.
const (
	VerdictRows       = "YES: the Query Store views returned rows while the state is OFF on this build."
	VerdictNoRows     = "NO: Query Store is OFF here and the views returned no history on this build."
	VerdictNoneInSpan = "NO ROWS READ: Query Store is OFF and reports kept blocks, but no rows came back for them. See the lane sentence."
	VerdictNotOff     = "NOT ANSWERED: this database is not in the OFF state, so this run says nothing about OFF history."
	VerdictUnread     = "NOT ANSWERED: the history could not be read (see the lane sentence)."
)

// Verdict is the one-line answer to the question the probe exists for.
func (p QSProbe) Verdict() string {
	switch {
	case p.State == "" && p.Check.State == "":
		return VerdictUnread
	case p.State != QSStateOff:
		return VerdictNotOff
	case p.Points > 0:
		return VerdictRows
	case p.FirstData == nil && p.Check.State != StateFailed:
		return VerdictNoRows
	case p.FirstData == nil:
		return VerdictUnread
	default:
		return VerdictNoneInSpan
	}
}

// readOnlyBad lists the words that never belong in one of the probe's texts. EXEC is allowed only for the two error-log reads the lane makes.
var readOnlyBad = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|grant|revoke|truncate|merge|shutdown|kill|reconfigure|dbcc|xp_cmdshell)\b|;`)
var readOnlyExec = regexp.MustCompile(`(?i)\bexec(ute)?\b`)

// ErrProbeNotReadOnly is returned by the guard for a text that is not a plain read.
var ErrProbeNotReadOnly = errors.New("refused: the probe only runs read-only SQL")

// readOnlyQueryer passes a text on only when it is a single plain read.
type readOnlyQueryer struct {
	inner   Queryer
	refused int
}

func (g *readOnlyQueryer) Query(ctx context.Context, text string) ([]Row, error) {
	if readOnlyBad.MatchString(text) || (readOnlyExec.MatchString(text) && !strings.Contains(text, "sp_readerrorlog 0, 1,") && !strings.Contains(text, "sp_enumerrorlogs 1")) {
		g.refused++
		return nil, ErrProbeNotReadOnly
	}
	return g.inner.Query(ctx, text)
}

// qsProbeSlack is added to the newest block start when the probe aims its windows at the kept history (a block is at least this long
// in the default setting; a longer block is still touched because the windows are read by block start).
const qsProbeSlack = time.Hour

// ProbeQueryStoreOff reads one database the way the app does and says what came back. q must be scoped to the database. The first pass uses
// tiny windows at "now" and only learns the range of the kept blocks; when there is one, the second pass aims one day of incident window and
// one day of reference window at the newest kept blocks, which is where an OFF database's history sits.
func ProbeQueryStoreOff(ctx context.Context, q Queryer, label, database string, now clock) QSProbe {
	g := &readOnlyQueryer{inner: q}
	p := QSProbe{Database: database}
	if rows, err := g.Query(ctx, qLaneBase); err == nil && len(rows) > 0 {
		p.Version, p.Edition = text(rows[0]["version"]), text(rows[0]["edition"])
	} else if err != nil {
		p.BaseErr = classify(err)
	} else {
		p.BaseErr = "the server gave no version row"
	}
	if rows, err := g.Query(ctx, qLaneQSOptions); err == nil && len(rows) > 0 {
		o := evalQSOptions(rows)
		p.OptionsRead, p.Actual, p.Desired = true, o.state, o.desired
	}

	t := now().UTC()
	req := LaneRequest{
		Incident:  Window{Start: t.Add(-time.Hour), End: t},
		Reference: Window{Start: t.Add(-2 * time.Hour), End: t.Add(-time.Hour)},
		Database:  database, Login: "unravis_ro",
	}
	rep := RunSQLLane(ctx, g, label, req, now)
	fill := func(r Report) {
		if r.Lane == nil {
			return
		}
		for _, c := range r.Lane.Checks {
			if c.ID == CheckQueryStore {
				p.Check = c
			}
		}
		if qs := r.Lane.QueryStore; qs != nil {
			p.State, p.FirstData, p.LastData = qs.State, qs.FirstData, qs.LastData
		}
		p.Points, p.Executions = 0, 0
		for _, pt := range r.Lane.Series {
			p.Points++
			p.Executions += pt.Execs
		}
	}
	fill(rep)
	if p.State == QSStateOff && p.FirstData != nil && p.LastData != nil {
		end := p.LastData.Add(qsProbeSlack)
		req.Incident = Window{Start: end.Add(-24 * time.Hour), End: end}
		req.Reference = Window{Start: end.Add(-48 * time.Hour), End: end.Add(-24 * time.Hour)}
		fill(RunSQLLane(ctx, g, label, req, now))
	}
	p.Refused = g.refused
	return p
}

// Format prints the probe as plain lines for the person running it.
func (p QSProbe) Format() string {
	var b strings.Builder
	line := func(k, v string) { fmt.Fprintf(&b, "%-28s %s\n", k+":", v) }
	or := func(s, none string) string {
		if strings.TrimSpace(s) == "" {
			return none
		}
		return s
	}
	tm := func(t *time.Time) string {
		if t == nil {
			return "none"
		}
		return utcText(*t)
	}
	if p.BaseErr != "" {
		line("Server version", "not read ("+p.BaseErr+")")
	} else {
		line("Server version", or(p.Version, "not read")+" "+or(p.Edition, ""))
	}
	line("Database", p.Database)
	if p.OptionsRead {
		line("Query Store desired state", or(p.Desired, "none reported"))
		line("Query Store actual state", or(p.Actual, "none reported"))
	} else {
		line("Query Store desired state", "not read")
		line("Query Store actual state", "not read")
	}
	line("Lane state", or(p.State, "unknown"))
	line("First kept block (start)", tm(p.FirstData))
	line("Last kept block (start)", tm(p.LastData))
	line("Rows read (blocks)", fmt.Sprintf("%d (%d executions)", p.Points, p.Executions))
	line("Query Store check", or(p.Check.State, "not run"))
	fmt.Fprintf(&b, "What the app would show:\n  %s\n", or(p.Check.Summary, "(no sentence)"))
	fmt.Fprintf(&b, "Texts refused by the read-only guard: %d\n", p.Refused)
	fmt.Fprintf(&b, "\nVERDICT: %s\n", p.Verdict())
	return b.String()
}
