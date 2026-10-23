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

func demoRecord(sourceID, controller, action string, initiated time.Time, duration float64, apiIndex, sample int, period string) RequestRecord {
	id := fmt.Sprintf("%s-%s-%s-%02d", sourceID, controller, period, sample)
	completed := initiated.Add(time.Duration(duration * float64(time.Millisecond)))
	// Audit and API time add up to the total; the payload write happens after the response.
	auditTime := duration * .2
	apiTime := duration - auditTime
	payloadTime := duration * .1
	return RequestRecord{ID: id, SourceID: sourceID, Controller: controller, Action: action, Kind: "synchronous", Outcome: "success", Status: "200 OK", URL: "/" + strings.ToLower(controller) + "/" + strings.ToLower(action), InitiatedAt: &initiated, CompletedAt: &completed, DurationMs: &duration, ApiTimeMs: &apiTime, AuditCreateTimeMs: &auditTime, PayloadRecordTimeMs: &payloadTime, RequestKB: float64((sample+apiIndex)%5 + 1), ResponseKB: float64((sample+apiIndex)%7 + 2)}
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
	if err := ctx.Err(); err != nil {
		return Analysis{}, err
	}
	if err := ValidateQuery(q); err != nil {
		return Analysis{}, err
	}
	selected := make(map[string]bool, len(q.SourceIDs))
	for _, id := range q.SourceIDs {
		if !d.hasSource(id) {
			return Analysis{}, fmt.Errorf("%w: source was not found", ErrNotFound)
		}
		selected[id] = true
	}
	current, reference := aggregateDemo(d.records, q, selected, true), aggregateDemo(d.records, q, selected, false)
	if err := ctx.Err(); err != nil {
		return Analysis{}, err
	}
	sourceResults := make([]SourceResult, 0, len(q.SourceIDs))
	for _, id := range q.SourceIDs {
		sourceResults = append(sourceResults, SourceResult{SourceID: id, OK: true, Version: "demo"})
	}
	return BuildAnalysis(q, current, reference, sourceResults)
}

func aggregateDemo(records []RequestRecord, q Query, selected map[string]bool, current bool) []Bucket {
	type bucketKey struct {
		source, controller, action, kind, outcome string
		at                                        int64
		bin                                       int
	}
	type aggregate struct {
		count         int64
		sum, min, max float64
		set           bool
		phases        Bucket // only the phase sums and counts are used
	}
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
		if q.Kind != "all" && r.Kind != q.Kind || !BucketOutcomeMatches(q.Outcome, r.Outcome) {
			continue
		}
		value := r.DurationMs
		if q.Metric == "api" {
			value = r.ApiTimeMs
		}
		if value == nil || *value < 0 {
			continue
		}
		bin := BinIndex(*value)
		if bin < 0 {
			continue
		}
		seconds := int64(q.BucketSeconds)
		epoch := r.InitiatedAt.Unix()
		at := epoch - epoch%seconds
		key := bucketKey{r.SourceID, r.Controller, r.Action, r.Kind, r.Outcome, at, bin}
		a := groups[key]
		if a == nil {
			a = &aggregate{min: *value, max: *value}
			groups[key] = a
		}
		a.count++
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
		if a.at != b.at {
			return a.at < b.at
		}
		return a.bin < b.bin
	})
	buckets := make([]Bucket, 0, len(keys))
	for _, k := range keys {
		a := groups[k]
		buckets = append(buckets, Bucket{SourceID: k.source, Controller: k.controller, Action: k.action, Kind: k.kind, Outcome: k.outcome, State: "valid", At: time.Unix(k.at, 0).UTC(), Bin: k.bin, Count: a.count, SumMs: a.sum, MinMs: a.min, MaxMs: a.max, SumAuditMs: a.phases.SumAuditMs, SumApiMs: a.phases.SumApiMs, SumPayloadMs: a.phases.SumPayloadMs, NAudit: a.phases.NAudit, NApi: a.phases.NApi, NPayload: a.phases.NPayload})
	}
	return buckets
}

func addDemoPhase(sum *float64, n *int64, value *float64) {
	if value != nil && *value >= 0 {
		*sum += *value
		*n++
	}
}

func (d *demoRepository) Requests(ctx context.Context, input PageInput) (Page, error) {
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if err := ValidateQuery(input.Query); err != nil {
		return Page{}, err
	}
	if input.Limit < 1 || input.Limit > MaxPageSize {
		return Page{}, fmt.Errorf("%w: page limit must be between 1 and %d", ErrLimit, MaxPageSize)
	}
	fields, err := decodeCohortKey(input.APIKey)
	if err != nil {
		return Page{}, fmt.Errorf("%w: API key is invalid", ErrInvalidInput)
	}
	selected := false
	for _, id := range input.Query.SourceIDs {
		if id == fields[0] {
			selected = true
		}
		if !d.hasSource(id) {
			return Page{}, fmt.Errorf("%w: source was not found", ErrNotFound)
		}
	}
	if !selected || (input.Query.Controller != "" && input.Query.Controller != fields[1]) || (input.Query.Action != "" && input.Query.Action != fields[2]) || (input.Query.Kind != "all" && input.Query.Kind != fields[3]) || !BucketOutcomeMatches(input.Query.Outcome, fields[4]) {
		return Page{}, fmt.Errorf("%w: API key does not match query", ErrInvalidInput)
	}
	fingerprintBytes, _ := json.Marshal(struct {
		Query  Query  `json:"query"`
		APIKey string `json:"apiKey"`
	}{input.Query, input.APIKey})
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
	rows := make([]RequestRecord, 0)
	for _, r := range d.records {
		if r.InitiatedAt.Before(start) || !r.InitiatedAt.Before(end) {
			continue
		}
		if !r.InitiatedAt.Before(start) && r.InitiatedAt.Before(end) && r.SourceID == fields[0] && r.Controller == fields[1] && r.Action == fields[2] && r.Kind == fields[3] && r.Outcome == fields[4] {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !a.InitiatedAt.Equal(*b.InitiatedAt) {
			return a.InitiatedAt.After(*b.InitiatedAt)
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
	page := Page{Rows: append([]RequestRecord(nil), rows[offset:endIndex]...), Sources: []SourceResult{{SourceID: fields[0], OK: true, Version: "demo"}}}
	if endIndex < len(rows) {
		raw, _ := json.Marshal(demoCursor{Offset: endIndex, Fingerprint: fingerprint})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
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
