# Unravis (formerly API Audit Explorer) — Investigation MVP

Source: the user's MVP statement, 2026-10-23, pasted verbatim below. It is the binding spec for the gap work.

## Task given
Review the existing API Audit Investigation implementation against this MVP.

### 1. Investigation Overview
* Traffic
* Completion
* P50/P90/P95/P99 latency
* 4xx/5xx failures
* Trends over time

### 2. Operation Performance
* Controller + Action
* Request count
* Completion rate
* Percentiles
* Failure rates
* Latency burden

### 3. Slow Requests
* Slowest individual requests
* Threshold/percentile filtering
* AuditCreateTime vs ApiTime
* Tail latency

### 4. Failure Investigation
* Status breakdown
* Failures by operation
* Failure trends
* Drill-down to requests

### 5. Incomplete Requests
* Missing completion evidence
* Completion rate
* Stale incomplete requests

### 6. Compare
* Current period vs baseline
* Traffic, latency, failure and completion changes
* Per-operation regression

### 7. Breakdown
* UserId
* PayrollUIVersion
* UserAgent
* UserIp
* RequestPollingType

### 8. Request Detail
* Full underlying audit evidence

## Required filters
* Date/time range
* Quick time range
* Time bucket/window size
* HTTP/Hangfire
* Polling type
* Controller
* Action
* Operation
* Status/status class
* Complete/incomplete
* UserId
* UI version
* Observed API Time
* Audit ID
* Slow-request threshold/percentile
* Request/response size
* UserAgent
* UserIp
* URL search
* AuditCreateTime
* ApiTime
* Incomplete age
* Comparison/baseline period

## Process
First inspect what is already implemented. Do not assume something is missing just because it is named differently.
For every feature and filter classify it as: Complete / Partially implemented / Missing / Incorrect.
Check the actual behaviour, calculations, drill-downs and data handling, not just whether UI/code exists.

## Rules
* Separate Synchronous, Asynchronous and Hangfire performance.
* Use ControllerName + ActionName as the operation.
* Use AuditCreateTime + ApiTime as Observed API Time.
* Calculate latency only from completed records.
* Never treat incomplete records as fast or successful.
* Keep 4xx separate from 5xx server failures.
* Every aggregate/finding must be drillable to the underlying ApiAuditLog records.
* Do not invent fields that the audit schema does not contain.

After the review, create a concise implementation plan containing only the gaps and fixes still required, ordered by dependency and investigation value. Then execute the plan completely. Preserve working functionality and avoid unnecessary rewrites or architecture changes.

## Naming
The product is renamed **Unravis** (user's choice, 2026-10-23).
