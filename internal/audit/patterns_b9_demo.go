package audit

import (
	"fmt"
	"time"
)

// Demo data for B9 (step dating), the audit-create series (B6s) and the
// fixed-duration check (B6f). It sits on demo-west and stays out of the way of
// every earlier demo window:
//
//   - The step rows run from 32 to 9 days before now, about 11:00 to 12:45 UTC
//     each day, so no window of 5 days or less sees them. Four operations step
//     up: three on the same date and one a day later. One operation stays flat.
//     Each operation has 22 requests a day, so every day clears the 20-request
//     floor. Before its step an operation's requests take 200 to 400 ms; after,
//     900 to 1,100 ms.
//   - The band rows are audit-create delays between 26 and 31 s on 7 and 6 days
//     before now (three a day), plus four Hangfire rows in the same band that the
//     check leaves out. They start at least 6 days back, so no window of 5 days
//     or less sees them.
const (
	demoStepFirstDay = 32
	demoStepLastDay  = 9
	// demoStepDay is how many days before now the step starts for three operations;
	// the fourth steps one day later.
	demoStepDay      = 20
	demoStepPerDay   = 22
	demoStepSource   = demoPatternSource
	demoStallDayOne  = 6
	demoStallDayTwo  = 5
	demoStallPerDay  = 3
	demoStallBandLow = 26200.0
)

type demoStepOperation struct {
	controller, action string
	// stepDaysBack is the first day (counted back from now) of the slow segment, or
	// zero for an operation that does not step.
	stepDaysBack int
}

var demoStepOperations = []demoStepOperation{
	{"Invoices", "Search", demoStepDay},
	{"Orders", "Submit", demoStepDay},
	{"Catalog", "Browse", demoStepDay},
	{"Accounts", "Summary", demoStepDay - 1},
	{"Health", "Ping", 0},
}

// demoStepRecords builds the step rows.
func demoStepRecords(now time.Time) []RequestRecord {
	midnight := now.UTC().Truncate(24 * time.Hour)
	var rows []demoScenarioRow
	for daysBack := demoStepFirstDay; daysBack >= demoStepLastDay; daysBack-- {
		for opIndex, op := range demoStepOperations {
			slow := op.stepDaysBack > 0 && daysBack <= op.stepDaysBack
			for i := 0; i < demoStepPerDay; i++ {
				initiated := midnight.AddDate(0, 0, -daysBack).Add(11*time.Hour + time.Duration(i)*5*time.Minute)
				spread := float64((i*37 + daysBack*11 + opIndex*5) % 21)
				duration := 200 + spread*10
				if slow {
					duration = 900 + spread*10
				}
				rows = append(rows, demoScenarioRow{
					label:      fmt.Sprintf("%s-%s-%02d-%02d", op.controller, op.action, daysBack, i),
					controller: op.controller, action: op.action,
					offset: now.Sub(initiated), durationMs: duration, auditMs: 60, userID: 4000 + i%9,
				})
			}
		}
	}
	return demoScenarioRecords(now, demoStepSource, rows, "step")
}

// demoStallBandRecords builds the fixed-duration rows: six synchronous audit
// creates between 26.2 s and 30.7 s over two days, and four Hangfire rows in the
// same band on the first day.
func demoStallBandRecords(now time.Time) []RequestRecord {
	midnight := now.UTC().Truncate(24 * time.Hour)
	var rows []demoScenarioRow
	for dayIndex, daysBack := range []int{demoStallDayOne, demoStallDayTwo} {
		for i := 0; i < demoStallPerDay; i++ {
			initiated := midnight.AddDate(0, 0, -daysBack).Add(11*time.Hour + time.Duration(i)*37*time.Minute)
			audit := demoStallBandLow + float64(dayIndex*3+i)*900
			rows = append(rows, demoScenarioRow{
				label:      fmt.Sprintf("band-%d-%d", daysBack, i),
				controller: "Timesheets", action: "Approve",
				offset: now.Sub(initiated), durationMs: audit + 3000, auditMs: audit, userID: 4100 + i,
			})
		}
	}
	records := demoScenarioRecords(now, demoStepSource, rows, "stall")

	var hangfire []demoScenarioRow
	for i := 0; i < 4; i++ {
		initiated := midnight.AddDate(0, 0, -demoStallDayOne).Add(15*time.Hour + time.Duration(i)*11*time.Minute)
		hangfire = append(hangfire, demoScenarioRow{
			label:      fmt.Sprintf("hangfire-%d", i),
			controller: "Timesheets", action: "Approve",
			offset: now.Sub(initiated), durationMs: 33000, auditMs: demoStallBandLow + float64(i)*300, userID: 4200 + i,
		})
	}
	for _, r := range demoScenarioRecords(now, demoStepSource, hangfire, "stall") {
		r.Kind, r.PollingTypeRaw = "hangfire", "Hangfire "
		records = append(records, r)
	}
	return records
}
