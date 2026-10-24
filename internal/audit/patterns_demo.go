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

func demoScenarioRecords(now time.Time, rows []demoScenarioRow, prefix string) []RequestRecord {
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
			ID: fmt.Sprintf("%s-%s-%s", demoPatternSource, prefix, r.label), SourceID: demoPatternSource,
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
	}
	return demoScenarioRecords(now, rows, "pattern")
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
	return demoScenarioRecords(now, rows, "onset")
}
