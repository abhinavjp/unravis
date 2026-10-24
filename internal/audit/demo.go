package audit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type demoRepository struct {
	now     time.Time
	sources []Source
	records []RequestRecord
	// lateWrites marks the late-written rows of newDemoWithLateWrite by ID.
	lateWrites map[string]bool
}

type demoCursor struct {
	Offset      int    `json:"offset"`
	Fingerprint string `json:"fingerprint"`
}

func NewDemo(now time.Time) Repository {
	now = now.UTC().Truncate(time.Second)
	d := &demoRepository{now: now, sources: []Source{
		{ID: "demo-west", Label: "Demo West", Database: "demo", Collection: "audit"},
		{ID: "demo-east", Label: "Demo East", Database: "demo", Collection: "audit"},
	}}
	d.generate()
	return d
}

// NewDemoWithStaleWork is the demo plus work that never finished, so the
// incomplete-requests view has something to show. Per source it adds three
// Payroll.Run requests that are still incomplete, three Reports.Generate requests
// that only answered 202 Accepted (pending asynchronous work) and one Employees.List
// request with unusable data (invalid, which is counted apart from unfinished).
// Reports.Generate becomes an asynchronous operation here, because only an
// asynchronous request can be pending. NewDemo is unchanged: other tests rely on
// its rows all being complete.
func NewDemoWithStaleWork(now time.Time) Repository {
	repo := NewDemo(now).(*demoRepository)
	for i := range repo.records {
		if repo.records[i].Controller == "Reports" {
			repo.records[i].Kind, repo.records[i].PollingTypeRaw = "asynchronous", "Asynchronous "
		}
	}
	unfinished := func(sourceID, controller, action, kind, status, state string, label string, sample int, age time.Duration) RequestRecord {
		initiated := repo.now.Add(-age)
		created := initiated.Add(40 * time.Millisecond)
		userID := 1000 + sample
		return RequestRecord{ID: fmt.Sprintf("%s-%s-%s-%d", sourceID, controller, label, sample), SourceID: sourceID, Controller: controller, Action: action, Kind: kind, Outcome: "unknown", Status: status, State: state, URL: "/" + strings.ToLower(controller) + "/" + strings.ToLower(action), InitiatedAt: &initiated, RequestKB: 1, ResponseKB: 1, UserID: &userID, UserIP: fmt.Sprintf("203.0.113.%d", 50+sample), UserAgent: demoAgents[sample%len(demoAgents)], UIVersion: demoVersions[sample%len(demoVersions)], CreatedOn: &created}
	}
	for _, source := range repo.sources {
		for sample, age := range []time.Duration{14 * time.Minute, 11 * time.Minute, 4 * time.Minute} {
			repo.records = append(repo.records, unfinished(source.ID, "Payroll", "Run", "synchronous", "", "incomplete", "stale", sample, age))
		}
		for sample, age := range []time.Duration{13 * time.Minute, 9 * time.Minute, 2 * time.Minute} {
			repo.records = append(repo.records, unfinished(source.ID, "Reports", "Generate", "asynchronous", "Accepted", "incomplete", "queued", sample, age))
		}
		repo.records = append(repo.records, unfinished(source.ID, "Employees", "List", "synchronous", "200 OK", "invalid", "unusable", 0, 6*time.Minute))
	}
	return repo
}

// DemoTime returns the fixed clock used to generate the demo records.
func (d *demoRepository) DemoTime() time.Time { return d.now }

func (d *demoRepository) generate() {
	apis := []struct {
		controller, action string
		baseline, current  float64
	}{
		{"Payroll", "Run", 50, 250},
		{"Reports", "Generate", 100, 100},
		{"Employees", "List", 20, 20},
	}
	period := 15 * time.Minute
	baselineStart := d.now.Add(-2 * period)
	currentStart := d.now.Add(-period)
	const samples = 125
	for _, source := range d.sources {
		for apiIndex, api := range apis {
			for i := 0; i < samples; i++ {
				// Spread deterministic observations across each half-open quarter-hour window.
				baselineAt := baselineStart.Add(time.Duration(i) * period / samples)
				currentAt := currentStart.Add(time.Duration(i) * period / samples)
				d.records = append(d.records,
					demoRecord(source.ID, api.controller, api.action, baselineAt, api.baseline*(1+float64(i%5)*.01), apiIndex, i, "reference"),
					demoRecord(source.ID, api.controller, api.action, currentAt, api.current*(1+float64(i%5)*.01), apiIndex, i, "current"),
				)
			}
		}
	}
}

// The demo spreads clients over a few agents and UI versions so each breakdown
// dimension has more than one value to show.
var (
	demoAgents   = []string{"Demo Browser/1.0", "Demo Browser/2.0", "Demo Mobile/1.0", "Demo Automation/0.9"}
	demoVersions = []string{"demo-1.0", "demo-1.1", "demo-2.0"}
)

func demoRecord(sourceID, controller, action string, initiated time.Time, duration float64, apiIndex, sample int, period string) RequestRecord {
	id := fmt.Sprintf("%s-%s-%s-%02d", sourceID, controller, period, sample)
	completed := initiated.Add(time.Duration(duration * float64(time.Millisecond)))
	// Audit and API time add up to the total; the payload write happens after the response.
	auditTime := duration * .2
	apiTime := duration - auditTime
	payloadTime := duration * .1
	userID := 1000 + sample%7
	created := initiated.Add(time.Duration(auditTime * float64(time.Millisecond)))
	updated := completed.Add(time.Duration(payloadTime * float64(time.Millisecond)))
	return RequestRecord{ID: id, SourceID: sourceID, Controller: controller, Action: action, Kind: "synchronous", Outcome: "success", Status: "200 OK", URL: "/" + strings.ToLower(controller) + "/" + strings.ToLower(action), InitiatedAt: &initiated, CompletedAt: &completed, DurationMs: &duration, ApiTimeMs: &apiTime, AuditCreateTimeMs: &auditTime, PayloadRecordTimeMs: &payloadTime, RequestKB: float64((sample+apiIndex)%5 + 1), ResponseKB: float64((sample+apiIndex)%7 + 2), UserID: &userID, UserIP: fmt.Sprintf("203.0.113.%d", 10+sample%20), UserAgent: demoAgents[sample%len(demoAgents)], UIVersion: demoVersions[sample%len(demoVersions)], CreatedOn: &created, UpdatedOn: &updated, PollingTypeRaw: "Synchronous "}
}

func (d *demoRepository) Sources(ctx context.Context) ([]Source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]Source(nil), d.sources...), nil
}

func (d *demoRepository) AddSource(ctx context.Context, input SourceInput) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	_ = input
	return Source{}, fmt.Errorf("%w: demo mode does not accept database connection input", ErrInvalidInput)
}

func (d *demoRepository) DeleteSource(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, s := range d.sources {
		if s.ID == id {
			return fmt.Errorf("%w: demo sources are fixed", ErrInvalidInput)
		}
	}
	return fmt.Errorf("%w: source was not found", ErrNotFound)
}

func (d *demoRepository) Analyze(ctx context.Context, q Query) (Analysis, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return Analysis{}, err
	}
	if err := ValidateQuery(q); err != nil {
		return Analysis{}, err
	}
	q.Now = d.now
	selected := make(map[string]bool, len(q.SourceIDs))
	for _, id := range q.SourceIDs {
		if !d.hasSource(id) {
			return Analysis{}, fmt.Errorf("%w: source was not found", ErrNotFound)
		}
		selected[id] = true
	}
	scope := d.scopeFor(q.QueryMode)
	current := aggregateDemo(scope.windowRecords(d.records, q.Start, q.End), q, selected, true)
	reference := aggregateDemo(scope.windowRecords(d.records, q.BaselineStart, q.BaselineEnd), q, selected, false)
	if err := ctx.Err(); err != nil {
		return Analysis{}, err
	}
	sourceResults := make([]SourceResult, 0, len(q.SourceIDs))
	for _, id := range q.SourceIDs {
		sourceResults = append(sourceResults, SourceResult{SourceID: id, OK: true, Version: "demo"})
	}
	analysis, err := BuildAnalysis(q, current, reference, sourceResults)
	if err != nil {
		return Analysis{}, err
	}
	elapsed := time.Since(started)
	currentCounts, referenceCounts := bucketCountsBySource(current), bucketCountsBySource(reference)
	for _, id := range q.SourceIDs {
		analysis.Provenance = append(analysis.Provenance,
			d.demoProvenance(id, "current", q.Start, q.End, scope, currentCounts[id], currentCounts[id], elapsed),
			d.demoProvenance(id, "reference", q.BaselineStart, q.BaselineEnd, scope, referenceCounts[id], referenceCounts[id], elapsed),
		)
	}
	return analysis, nil
}

func aggregateDemo(records []RequestRecord, q Query, selected map[string]bool, current bool) []Bucket {
	type bucketKey struct {
		source, controller, action, kind, outcome string
		state                                     string
		at                                        int64
		bin                                       int
	}
	type aggregate struct {
		count         int64
		sum, min, max float64
		set           bool
		phases        Bucket // only the phase sums and counts and the pending and status class counts are used
	}
	q = q.Normalized()
	groups := map[bucketKey]*aggregate{}
	start, end := q.BaselineStart, q.BaselineEnd
	if current {
		start, end = q.Start, q.End
	}
	for _, r := range records {
		if !selected[r.SourceID] || r.InitiatedAt == nil || r.InitiatedAt.Before(start) || !r.InitiatedAt.Before(end) {
			continue
		}
		if q.Controller != "" && r.Controller != q.Controller || q.Action != "" && r.Action != q.Action {
			continue
		}
		state, class, value, ok := demoSummaryRow(r, q)
		if !ok || !demoRecordMatches(q, r, state, class) {
			continue
		}
		bin := 0
		if state == "valid" {
			bin = BinIndex(*value)
		}
		seconds := int64(q.BucketSeconds)
		epoch := r.InitiatedAt.Unix()
		at := epoch - epoch%seconds
		key := bucketKey{r.SourceID, r.Controller, r.Action, r.Kind, r.Outcome, state, at, bin}
		a := groups[key]
		if a == nil {
			a = &aggregate{}
			if state == "valid" {
				a.min, a.max = *value, *value
			}
			groups[key] = a
		}
		a.count++
		switch class {
		case StatusPending:
			a.phases.PendingAsync++
		case StatusClientError:
			a.phases.Count4xx++
		case StatusServerError:
			a.phases.Count5xx++
		case StatusHangfireFailed:
			a.phases.CountHangfireFailed++
		}
		if state == "valid" {
			a.sum += *value
			addDemoPhase(&a.phases.SumAuditMs, &a.phases.NAudit, r.AuditCreateTimeMs)
			addDemoPhase(&a.phases.SumApiMs, &a.phases.NApi, r.ApiTimeMs)
			addDemoPhase(&a.phases.SumPayloadMs, &a.phases.NPayload, r.PayloadRecordTimeMs)
			if *value < a.min {
				a.min = *value
			}
			if *value > a.max {
				a.max = *value
			}
		}
		a.set = true
	}
	keys := make([]bucketKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.source != b.source {
			return a.source < b.source
		}
		if a.controller != b.controller {
			return a.controller < b.controller
		}
		if a.action != b.action {
			return a.action < b.action
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if a.outcome != b.outcome {
			return a.outcome < b.outcome
		}
		if a.state != b.state {
			return a.state < b.state
		}
		if a.at != b.at {
			return a.at < b.at
		}
		return a.bin < b.bin
	})
	buckets := make([]Bucket, 0, len(keys))
	for _, k := range keys {
		a := groups[k]
		buckets = append(buckets, Bucket{SourceID: k.source, Controller: k.controller, Action: k.action, Kind: k.kind, Outcome: k.outcome, State: k.state, At: time.Unix(k.at, 0).UTC(), Bin: k.bin, Count: a.count, SumMs: a.sum, MinMs: a.min, MaxMs: a.max, PendingAsync: a.phases.PendingAsync, Count4xx: a.phases.Count4xx, Count5xx: a.phases.Count5xx, CountHangfireFailed: a.phases.CountHangfireFailed, SumAuditMs: a.phases.SumAuditMs, SumApiMs: a.phases.SumApiMs, SumPayloadMs: a.phases.SumPayloadMs, NAudit: a.phases.NAudit, NApi: a.phases.NApi, NPayload: a.phases.NPayload})
	}
	return buckets
}

// demoState is a demo row's state; the generated rows leave it empty and are
// complete.
func demoState(r RequestRecord) string {
	if r.State == "" {
		return "valid"
	}
	return r.State
}

// demoSummaryRow applies the summary's rules to one demo row: its state and
// status class, and its metric value. ok is false when the query's kind and
// outcome filters leave the row out of the summary, or when a complete row has
// no usable duration (the summary cannot bin it).
func demoSummaryRow(r RequestRecord, q Query) (state, class string, value *float64, ok bool) {
	state = demoState(r)
	class = StatusClass(r.Kind, state, r.Status)
	if !RowInSummary(q.Kind, q.Outcome, r.Kind, state, r.Outcome, class) {
		return state, class, nil, false
	}
	if state != "valid" {
		return state, class, nil, true
	}
	value = r.DurationMs
	if q.Metric == "api" {
		value = r.ApiTimeMs
	}
	if value == nil || *value < 0 || BinIndex(*value) < 0 {
		return state, class, nil, false
	}
	return state, class, value, true
}

// demoRecordMatches applies the shared record filters to one demo row: the
// identity, client, URL and status filters and the completion filter, the same
// rules the Mongo pipelines and the legacy reader apply. q must already be
// normalized so that Operation is folded into Controller and Action.
func demoRecordMatches(q Query, r RequestRecord, state, class string) bool {
	if !StateMatches(EffectiveCompletion(q), state, class == StatusPending, r.Outcome) {
		return false
	}
	if !RecordFilterMatches(q, RecordFacts{
		ID: r.ID, UserID: r.UserID, UserIP: r.UserIP, UserAgent: r.UserAgent,
		UIVersion: r.UIVersion, URL: r.URL, Status: r.Status,
	}) {
		return false
	}
	return !q.HasRanges() || RangeFilterMatches(q, demoRangeFacts(q, r, class == StatusPending))
}

// demoRangeFacts reads the values a range filter needs from one demo row. The
// observed duration is completion minus start, the same expression the pipelines
// use (not the stored DurationMs); age is measured from the repository clock
// (q.Now) against the start, falling back to CreatedOn. Only a record that has not
// completed (or a pending async request) has an age, as in the pipelines.
func demoRangeFacts(q Query, r RequestRecord, pending bool) RangeFacts {
	facts := RangeFacts{
		ApiTimeMs: r.ApiTimeMs, AuditCreateTimeMs: r.AuditCreateTimeMs,
		RequestKB: &r.RequestKB, ResponseKB: &r.ResponseKB,
	}
	at := r.InitiatedAt
	if at == nil {
		at = r.CreatedOn
	}
	if at != nil {
		if r.CompletedAt != nil {
			v := float64(r.CompletedAt.UnixMilli() - at.UnixMilli())
			facts.ObservedMs = &v
		}
		if !q.Now.IsZero() && (r.CompletedAt == nil || pending) {
			v := float64(q.Now.UnixMilli() - at.UnixMilli())
			facts.AgeMs = &v
		}
	}
	return facts
}

func addDemoPhase(sum *float64, n *int64, value *float64) {
	if value != nil && *value >= 0 {
		*sum += *value
		*n++
	}
}

func (d *demoRepository) Requests(ctx context.Context, input PageInput) (Page, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if err := ValidateQuery(input.Query); err != nil {
		return Page{}, err
	}
	input.Query = input.Query.Normalized()
	input.Query.Now = d.now
	if input.Limit < 1 || input.Limit > MaxPageSize {
		return Page{}, fmt.Errorf("%w: page limit must be between 1 and %d", ErrLimit, MaxPageSize)
	}
	if err := ValidateState(input.State); err != nil {
		return Page{}, err
	}
	order, err := ValidateOrder(input.Query, input.Order)
	if err != nil {
		return Page{}, err
	}
	oldest := order == OrderOldest
	state := input.State
	if state == StateAll {
		state = ""
	}
	// Without an API key the page is query-wide: every selected source.
	keyed := input.APIKey != ""
	var fields [5]string
	if keyed {
		var err error
		if fields, err = decodeCohortKey(input.APIKey); err != nil {
			return Page{}, fmt.Errorf("%w: API key is invalid", ErrInvalidInput)
		}
	}
	selected := !keyed
	sources := make(map[string]bool, len(input.Query.SourceIDs))
	for _, id := range input.Query.SourceIDs {
		if keyed && id == fields[0] {
			selected = true
		}
		if !d.hasSource(id) {
			return Page{}, fmt.Errorf("%w: source was not found", ErrNotFound)
		}
		sources[id] = true
	}
	if !selected || (keyed && ((input.Query.Controller != "" && input.Query.Controller != fields[1]) || (input.Query.Action != "" && input.Query.Action != fields[2]) || !CohortMatchesQuery(input.Query, fields[3], fields[4]))) {
		return Page{}, fmt.Errorf("%w: API key does not match query", ErrInvalidInput)
	}
	fingerprintOrder := ""
	if oldest {
		fingerprintOrder = OrderOldest
	}
	fingerprintBytes, _ := json.Marshal(struct {
		Query  Query  `json:"query"`
		APIKey string `json:"apiKey"`
		State  string `json:"state"`
		// Order is only set for oldest, so a newest cursor keeps its fingerprint.
		Order string `json:"order,omitempty"`
	}{input.Query, input.APIKey, state, fingerprintOrder})
	sum := sha256.Sum256(fingerprintBytes)
	fingerprint := base64.RawURLEncoding.EncodeToString(sum[:])
	offset := 0
	if input.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(input.Cursor)
		if e != nil {
			return Page{}, fmt.Errorf("%w: cursor is invalid", ErrInvalidInput)
		}
		var cursor demoCursor
		if e = json.Unmarshal(raw, &cursor); e != nil || cursor.Fingerprint != fingerprint || cursor.Offset < 0 {
			return Page{}, fmt.Errorf("%w: cursor is invalid", ErrInvalidInput)
		}
		offset = cursor.Offset
	}
	start, end := input.Query.Start, input.Query.End
	scope := d.scopeFor(input.Query.QueryMode)
	rows := make([]RequestRecord, 0)
	for _, r := range scope.windowRecords(d.records, start, end) {
		if !sources[r.SourceID] || r.InitiatedAt == nil || r.InitiatedAt.Before(start) || !r.InitiatedAt.Before(end) {
			continue
		}
		if input.Query.Controller != "" && r.Controller != input.Query.Controller || input.Query.Action != "" && r.Action != input.Query.Action {
			continue
		}
		if keyed && (r.SourceID != fields[0] || r.Controller != fields[1] || r.Action != fields[2] || r.Kind != fields[3] || r.Outcome != fields[4]) {
			continue
		}
		rowState, class, _, ok := demoSummaryRow(r, input.Query)
		if !ok || !StateMatches(state, rowState, class == StatusPending, r.Outcome) || !demoRecordMatches(input.Query, r, rowState, class) {
			continue
		}
		r.State = rowState
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.InitiatedAt.Equal(*b.InitiatedAt) {
			if oldest {
				return a.InitiatedAt.Before(*b.InitiatedAt)
			}
			return a.InitiatedAt.After(*b.InitiatedAt)
		}
		if oldest {
			return a.ID < b.ID
		}
		return a.ID > b.ID
	})
	if offset > len(rows) {
		return Page{}, fmt.Errorf("%w: cursor is out of range", ErrInvalidInput)
	}
	endIndex := offset + input.Limit
	if endIndex > len(rows) {
		endIndex = len(rows)
	}
	page := Page{Rows: append([]RequestRecord(nil), rows[offset:endIndex]...), Sources: make([]SourceResult, 0, len(input.Query.SourceIDs))}
	for _, id := range input.Query.SourceIDs {
		if !keyed || id == fields[0] {
			page.Sources = append(page.Sources, SourceResult{SourceID: id, OK: true, Version: "demo"})
		}
	}
	if endIndex < len(rows) {
		raw, _ := json.Marshal(demoCursor{Offset: endIndex, Fingerprint: fingerprint})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	matched, returned := map[string]int64{}, map[string]int64{}
	for _, r := range rows {
		matched[r.SourceID]++
	}
	for _, r := range page.Rows {
		returned[r.SourceID]++
	}
	elapsed := time.Since(started)
	for _, s := range page.Sources {
		page.Provenance = append(page.Provenance, d.demoProvenance(s.SourceID, "page", start, end, scope, matched[s.SourceID], returned[s.SourceID], elapsed))
	}
	return page, nil
}

// Breakdown counts the rows the query's summary and request list select, by one
// dimension. It applies the same row rules as Requests, so a row's count equals
// the number of requests its filter lists.
func (d *demoRepository) Breakdown(ctx context.Context, input BreakdownInput) (Breakdown, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return Breakdown{}, err
	}
	if err := ValidateBreakdown(input); err != nil {
		return Breakdown{}, err
	}
	q := input.Query.Normalized()
	if q.HasAgeRange() {
		q.Now = d.now
	}
	sources := make(map[string]bool, len(q.SourceIDs))
	for _, id := range q.SourceIDs {
		if !d.hasSource(id) {
			return Breakdown{}, fmt.Errorf("%w: source was not found", ErrNotFound)
		}
		sources[id] = true
	}
	scope := d.scopeFor(q.QueryMode)
	matched := map[string]int64{}
	builder := NewBreakdownBuilder(input.Dim, input.Top)
	builder.SetPooled(PooledKind(q.Kind))
	for _, r := range scope.windowRecords(d.records, q.Start, q.End) {
		if !sources[r.SourceID] || r.InitiatedAt == nil || r.InitiatedAt.Before(q.Start) || !r.InitiatedAt.Before(q.End) {
			continue
		}
		if q.Controller != "" && r.Controller != q.Controller || q.Action != "" && r.Action != q.Action {
			continue
		}
		state, class, value, ok := demoSummaryRow(r, q)
		if !ok || !demoRecordMatches(q, r, state, class) {
			continue
		}
		builder.AddRecord(r, state, class == StatusPending, class, value)
		matched[r.SourceID]++
	}
	out, err := builder.Build()
	if err != nil {
		return Breakdown{}, err
	}
	elapsed := time.Since(started)
	for _, id := range q.SourceIDs {
		out.Sources = append(out.Sources, SourceResult{SourceID: id, OK: true, Version: "demo"})
		out.Provenance = append(out.Provenance, d.demoProvenance(id, "breakdown", q.Start, q.End, scope, matched[id], matched[id], elapsed))
	}
	return out, ctx.Err()
}

func (d *demoRepository) Payload(ctx context.Context, input PayloadInput) (Payload, error) {
	if err := ctx.Err(); err != nil {
		return Payload{}, err
	}
	for _, r := range d.records {
		if r.ID == input.ID && r.SourceID == input.SourceID {
			return Payload{Request: "Illustrative demo request for " + r.ID, Response: "Illustrative demo response for " + r.ID}, nil
		}
	}
	return Payload{}, fmt.Errorf("%w: request was not found", ErrNotFound)
}

func (d *demoRepository) Record(ctx context.Context, input RecordInput) (RecordDetail, error) {
	if err := ctx.Err(); err != nil {
		return RecordDetail{}, err
	}
	for _, r := range d.records {
		if r.ID == input.ID && r.SourceID == input.SourceID {
			r.State = demoState(r)
			return RecordDetail{RequestRecord: r, State: r.State, Payload: Payload{Request: "Illustrative demo request for " + r.ID, Response: "Illustrative demo response for " + r.ID}}, nil
		}
	}
	return RecordDetail{}, fmt.Errorf("%w: request was not found", ErrNotFound)
}

func (d *demoRepository) Close(ctx context.Context) error { return ctx.Err() }

func (d *demoRepository) hasSource(id string) bool {
	for _, source := range d.sources {
		if source.ID == id {
			return true
		}
	}
	return false
}

func decodeCohortKey(key string) ([5]string, error) {
	var result [5]string
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return result, err
	}
	var fields []string
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 5 {
		return result, errors.New("invalid cohort key")
	}
	copy(result[:], fields)
	return result, nil
}
