// Package dbhealth runs read-only health checks against a SQL Server or a MongoDB and turns what they return into plain findings.
// Every check is a fixed query or command written in this package: nothing a person types is ever run. A check that the login may not run
// (no VIEW SERVER STATE, no msdb access, no inprog privilege) answers "unavailable" instead of failing the whole run.
// Results are cumulative or point-in-time views the server keeps itself; the checks say which.
package dbhealth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Status ranks a finding. ok < note < warn < bad. unavailable is outside the ranking: the check could not run.
type Status string

const (
	OK          Status = "ok"
	Note        Status = "note"
	Warn        Status = "warn"
	Bad         Status = "bad"
	Unavailable Status = "unavailable"
)

func rank(s Status) int {
	switch s {
	case Note:
		return 1
	case Warn:
		return 2
	case Bad:
		return 3
	}
	return 0
}

// Finding is one check's answer. Summary is one plain sentence. Why says what the check looks at and what a bad result usually means.
type Finding struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Status  Status     `json:"status"`
	Summary string     `json:"summary"`
	Why     string     `json:"why,omitempty"`
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
}

// Report is one run. Label is the name the person gave the connection; it never holds a host, a user name or a password.
type Report struct {
	Kind       string    `json:"kind"` // "sql" or "mongo"
	Label      string    `json:"label"`
	Database   string    `json:"database,omitempty"`
	Version    string    `json:"version,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Overall    Status    `json:"overall"`
	Findings   []Finding `json:"findings"`
	// Lane is set by a lane run (RunSQLLane, RunMongoLane): what the database's own data says about the investigation's questions for one incident.
	Lane *LaneResult `json:"lane,omitempty"`
}

// Finish sets Overall to the worst finding that ran (ok when none did).
func (r *Report) Finish(now time.Time) {
	r.FinishedAt = now
	worst := OK
	for _, f := range r.Findings {
		if rank(f.Status) > rank(worst) {
			worst = f.Status
		}
	}
	r.Overall = worst
}

// Counts returns how many findings carry each status.
func (r Report) Counts() map[Status]int {
	out := map[Status]int{}
	for _, f := range r.Findings {
		out[f.Status]++
	}
	return out
}

const (
	checkTimeout = 20 * time.Second
	maxCell      = 160
)

// Row is one result row keyed by column name.
type Row map[string]any

// Queryer runs one fixed SQL text and returns its rows.
type Queryer interface {
	Query(ctx context.Context, text string) ([]Row, error)
}

// Env is what a check needs to know about the run.
type Env struct {
	Database   string
	Collection string // MongoDB only
	Now        time.Time
	Version   string  // filled by the server check
	UptimeMin float64 // filled by the server check; later checks read it
}

/* -------------------------------------------------------------- helpers */

func num(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case int32:
		return float64(x)
	case int:
		return float64(x)
	case float64:
		return x
	case float32:
		return float64(x)
	case bool:
		if x {
			return 1
		}
	case []byte:
		f, _ := strconv.ParseFloat(string(x), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	}
	return 0
}

func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format("2006-01-02 15:04")
	}
	return fmt.Sprint(v)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func fmtInt(f float64) string {
	return strconv.FormatInt(int64(f+0.5), 10)
}

func fmtF(f float64, digits int) string {
	return strconv.FormatFloat(f, 'f', digits, 64)
}

func pct(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return part * 100 / whole
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func duration(minutes float64) string {
	m := int(minutes + 0.5)
	switch {
	case m < 90:
		return plural(m, "minute", "minutes")
	case m < 48*60:
		return plural(m/60, "hour", "hours")
	}
	return plural(m/(24*60), "day", "days")
}

func unavailable(id, title, why, reason string) Finding {
	return Finding{ID: id, Title: title, Status: Unavailable, Summary: reason, Why: why}
}

// Cause words for an unavailable check. The driver's own message is never shown: it can carry names from the server.
const (
	reasonPermission = "This login is not allowed to run this check. Ask the database owner for read access to the system views, or skip it."
	reasonFailed     = "The check could not finish (it timed out or the server refused the query)."
	reasonNotThere   = "This server does not have what the check reads (an older version or a feature that is switched off)."
)
