package audit

import (
	"fmt"
	"strings"
	"time"
)

// NewDemoWithPatterns is NewDemoWithStaleWork plus a deterministic incident
// scenario for the P2-lite patterns, so the demo has stalls to show. The base
// demo has no request near 45 s. All scenario rows sit on demo-west between
// 3h30m and 1h before now, outside the 30 minutes the base demo fills, so no
// existing window or count changes. With "Last 4 hours" selected the patterns
// read three independent episodes (recurring), one of them a three-request
// volley from one user, plus two audit-create stalls (B6).
//
// A second, older set sits at least 19 hours back, so it only shows in a window
// of a day or more: three days of stalls on Documents.Export that start near
// minute :17 of the hour (B3a), on an operation that is a small share of traffic
// but most of those stalls (A3).
func NewDemoWithPatterns(now time.Time) Repository {
	repo := NewDemoWithStaleWork(now).(*demoRepository)
	repo.records = append(repo.records, demoPatternRecords(repo.now)...)
	repo.records = append(repo.records, demoOnsetRecords(repo.now)...)
	repo.records = append(repo.records, demoIdleRecords(repo.now, demoIdlePositiveSource, true)...)
	repo.records = append(repo.records, demoIdleRecords(repo.now, demoIdleNegativeSource, false)...)
	return repo
}

// demoPatternSource is the source that carries the scenario.
const demoPatternSource = "demo-west"

// demoScenarioRow is one scenario request. offset is how long before now it started.
type demoScenarioRow struct {
	label              string
	controller, action string
	offset             time.Duration
	durationMs         float64
	auditMs            float64
	userID             int
}

func demoScenarioRecords(now time.Time, source string, rows []demoScenarioRow, prefix string) []RequestRecord {
	records := make([]RequestRecord, 0, len(rows))
	for i, r := range rows {
		initiated := now.Add(-r.offset)
		duration, audit := r.durationMs, r.auditMs
		api := duration - audit
		payload := 40.0
		completed := initiated.Add(time.Duration(duration * float64(time.Millisecond)))
		created := initiated.Add(time.Duration(audit * float64(time.Millisecond)))
		updated := completed.Add(time.Duration(payload * float64(time.Millisecond)))
		userID := r.userID
		records = append(records, RequestRecord{
			ID: fmt.Sprintf("%s-%s-%s", source, prefix, r.label), SourceID: source,
			Controller: r.controller, Action: r.action, Kind: "synchronous", Outcome: "success", Status: "200 OK",
			URL:         "/" + strings.ToLower(r.controller) + "/" + strings.ToLower(r.action),
			InitiatedAt: &initiated, CompletedAt: &completed, DurationMs: &duration, ApiTimeMs: &api,
			AuditCreateTimeMs: &audit, PayloadRecordTimeMs: &payload, RequestKB: 2, ResponseKB: 3,
			UserID: &userID, UserIP: fmt.Sprintf("203.0.113.%d", 80+i%100), UserAgent: demoAgents[i%len(demoAgents)],
			UIVersion: demoVersions[i%len(demoVersions)], CreatedOn: &created, UpdatedOn: &updated, PollingTypeRaw: "Synchronous ",
		})
	}
	return records
}

func demoPatternRecords(now time.Time) []RequestRecord {
	rows := []demoScenarioRow{
		// Episode 1: two users, starts 40 s apart.
		{"e1-a", "Payroll", "Run", 3*time.Hour + 30*time.Minute, 52000, 180, 1001},
		{"e1-b", "Payroll", "Run", 3*time.Hour + 29*time.Minute + 20*time.Second, 61000, 210, 1002},
		// Audit-create stall (B6) between episodes 1 and 2.
		{"b6-a", "Employees", "List", 2*time.Hour + 50*time.Minute, 9000, 6200, 1004},
		// Episode 2: one request.
		{"e2-a", "Reports", "Generate", 2*time.Hour + 15*time.Minute, 48000, 150, 1005},
		// Audit-create stall (B6) between episodes 2 and 3.
		{"b6-b", "Reports", "Generate", 1*time.Hour + 40*time.Minute, 14000, 5400, 1006},
		// Episode 3: a volley, one user starting three requests within 1 s.
		{"e3-a", "Payroll", "Run", 1 * time.Hour, 60000, 160, 1003},
		{"e3-b", "Payroll", "Run", 1*time.Hour - 300*time.Millisecond, 64100, 170, 1003},
		{"e3-c", "Payroll", "Run", 1*time.Hour - 700*time.Millisecond, 68000, 190, 1003},
		// Quick requests for B12r, none of them a stall. User 1001 starts Payroll.Run
		// again three times, 10 s apart, while the 52 s stall e1-a is still running.
		{"r1-a", "Payroll", "Run", 3*time.Hour + 30*time.Minute - 10*time.Second, 2000, 100, 1001},
		{"r1-b", "Payroll", "Run", 3*time.Hour + 30*time.Minute - 20*time.Second, 2000, 100, 1001},
		{"r1-c", "Payroll", "Run", 3*time.Hour + 30*time.Minute - 30*time.Second, 2000, 100, 1001},
		// Not repeats: user 1002 starts another operation during e1-b, and user 1005
		// starts Reports.Generate after the 48 s stall e2-a was already done.
		{"r2-a", "Employees", "List", 3*time.Hour + 29*time.Minute + 5*time.Second, 2000, 100, 1002},
		{"r2-b", "Reports", "Generate", 2*time.Hour + 14*time.Minute, 2000, 100, 1005},
	}
	return demoScenarioRecords(now, demoPatternSource, rows, "pattern")
}

// The quiet-gap (B6g) demo: one source where audit create is slow after quiet
// gaps and one where it is not. Both are Timesheets.Save, a synchronous request
// that takes 21 s with audit create at 0.1 s (quick) or 6.2 s (slow). That is
// under 30% of observed time and under 45 s, so none of these rows is a B1 stall
// or a B6 audit-create stall, and no earlier rule or count changes. Each burst is
// a leader followed by two requests 5 s and 10 s later, and bursts start 11
// minutes apart, so a leader follows a gap of 650 s and a follower one of 5 s.
// Every row starts between 17 h and 6 h 55 min before now, so no 4-hour window
// and no window of 30 minutes sees them.
const (
	demoIdlePositiveSource = "demo-west"
	demoIdleNegativeSource = "demo-east"

	// demoIdlePositiveBursts and demoIdleNegativeBursts are 24 and 56 bursts. The
	// negative source has 55 leaders after a quiet gap, enough (50) to print "not
	// observed".
	demoIdlePositiveBursts = 24
	demoIdleNegativeBursts = 56
)

// demoIdleRecords builds the bursts of one source. In the positive source every
// odd leader (12 of the 23 with a known gap) has slow audit create, and one
// follower does, so audit create is slow after quiet gaps and not after short
// ones. In the negative source slow audit create is spread evenly: 2 of the 55
// leaders and 4 of the 112 followers.
func demoIdleRecords(now time.Time, source string, positive bool) []RequestRecord {
	bursts := demoIdleNegativeBursts
	if positive {
		bursts = demoIdlePositiveBursts
	}
	const quick, slow = 120.0, 6200.0
	var rows []demoScenarioRow
	for i := 0; i < bursts; i++ {
		leader := 17*time.Hour - time.Duration(i)*11*time.Minute
		leaderAudit := quick
		if (positive && i%2 == 1) || (!positive && i > 0 && i%20 == 0) {
			leaderAudit = slow
		}
		rows = append(rows, demoScenarioRow{
			label: fmt.Sprintf("%02d-lead", i), controller: "Timesheets", action: "Save",
			offset: leader, durationMs: 21000, auditMs: leaderAudit, userID: 3001 + i%7,
		})
		for j := 0; j < 2; j++ {
			followerAudit := quick
			switch {
			case positive && i == 5 && j == 0:
				followerAudit = 5500
			case !positive && (2*i+j)%28 == 0:
				followerAudit = slow
			}
			rows = append(rows, demoScenarioRow{
				label: fmt.Sprintf("%02d-follow-%d", i, j+1), controller: "Timesheets", action: "Save",
				offset: leader - time.Duration(j+1)*5*time.Second, durationMs: 21000, auditMs: followerAudit, userID: 3001 + (i+j+1)%7,
			})
		}
	}
	return demoScenarioRecords(now, source, rows, "idle")
}

// demoOnsetDays is how many earlier days carry a Documents.Export episode.
const demoOnsetDays = 3

// demoOnsetMinute is the minute of the UTC hour where those episodes start.
const demoOnsetMinute = 17

// demoOnsetRecords builds, for each of the demoOnsetDays days before now, one
// episode of four Documents.Export stalls from four users (the first starts at
// minute :17:00 UTC), and four quick Documents.Export requests later that day.
// Every row starts at least 19 hours before now and none is a B6 or B12 row.
func demoOnsetRecords(now time.Time) []RequestRecord {
	var rows []demoScenarioRow
	hour := now.UTC().Truncate(time.Hour)
	for day := 1; day <= demoOnsetDays; day++ {
		base := hour.Add(-time.Duration(day) * 24 * time.Hour)
		onset := base.Add(demoOnsetMinute * time.Minute)
		for i := 0; i < 4; i++ {
			rows = append(rows, demoScenarioRow{
				label: fmt.Sprintf("d%d-stall-%d", day, i+1), controller: "Documents", action: "Export",
				offset: now.Sub(onset.Add(time.Duration(i) * 10 * time.Second)), durationMs: 50000 + float64(i)*1500, auditMs: 120,
				userID: 2001 + i,
			})
		}
		quick := base.Add(40 * time.Minute)
		for i := 0; i < 4; i++ {
			rows = append(rows, demoScenarioRow{
				label: fmt.Sprintf("d%d-fast-%d", day, i+1), controller: "Documents", action: "Export",
				offset: now.Sub(quick.Add(time.Duration(i) * 90 * time.Second)), durationMs: 800, auditMs: 90,
				userID: 2011 + i,
			})
		}
	}
	return demoScenarioRecords(now, demoPatternSource, rows, "onset")
}
