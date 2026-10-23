package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type pageCursor struct {
	At          time.Time `json:"at"`
	SourceID    string    `json:"sourceId"`
	ObjectID    string    `json:"objectId"`
	Metric      *float64  `json:"metric,omitempty"`
	Fingerprint string    `json:"fingerprint"`
}

type requestDocument struct {
	ID                  bson.ObjectID `bson:"_id"`
	SourceID            string        `bson:"sourceId"`
	Controller          string        `bson:"controller"`
	Action              string        `bson:"action"`
	Kind                string        `bson:"kind"`
	Outcome             string        `bson:"outcome"`
	Status              string        `bson:"status"`
	URL                 string        `bson:"url"`
	InitiatedAt         *time.Time    `bson:"initiatedAt"`
	CompletedAt         *time.Time    `bson:"completedAt"`
	DurationMs          *float64      `bson:"durationMs"`
	ApiTimeMs           *float64      `bson:"apiTimeMs"`
	AuditCreateTimeMs   *float64      `bson:"auditCreateTimeMs"`
	PayloadRecordTimeMs *float64      `bson:"payloadRecordTimeMs"`
	RequestKB           float64       `bson:"requestKB"`
	ResponseKB          float64       `bson:"responseKB"`
	UserID              *int          `bson:"userId"`
	UserIP              string        `bson:"userIp"`
	UserAgent           string        `bson:"userAgent"`
	UIVersion           string        `bson:"uiVersion"`
	CreatedOn           *time.Time    `bson:"createdOn"`
	UpdatedOn           *time.Time    `bson:"updatedOn"`
	PollingTypeRaw      string        `bson:"pollingTypeRaw"`
}

type pageRow struct {
	request audit.RequestRecord
	at      time.Time
	object  bson.ObjectID
}

// Requests returns a bounded metadata page in deterministic cross-source order.
func (s *Store) Requests(ctx context.Context, input audit.PageInput) (audit.Page, error) {
	return s.requests(ctx, input, false)
}
func (s *Store) requests(ctx context.Context, input audit.PageInput, overlap bool) (audit.Page, error) {
	return s.requestsMode(ctx, input, overlap, false)
}
func (s *Store) requestsMode(ctx context.Context, input audit.PageInput, overlap, slowest bool) (audit.Page, error) {
	if err := audit.ValidateQuery(input.Query); err != nil {
		return audit.Page{}, fmt.Errorf("%w: %v", audit.ErrInvalidInput, err)
	}
	if input.Limit < 1 || input.Limit > audit.MaxPageSize {
		return audit.Page{}, fmt.Errorf("%w: page limit must be between 1 and %d", audit.ErrLimit, audit.MaxPageSize)
	}
	cohort := [5]string{"", input.Query.Controller, input.Query.Action, input.Query.Kind, input.Query.Outcome}
	if input.APIKey != "" {
		var err error
		cohort, err = decodeCohort(input.APIKey)
		if err != nil {
			return audit.Page{}, fmt.Errorf("%w: API key is invalid", audit.ErrInvalidInput)
		}
		if err := validateCohortQuery(input.Query, cohort); err != nil {
			return audit.Page{}, err
		}
	}
	fingerprint := pageFingerprint(input.Query, input.APIKey)
	if overlap {
		fingerprint = pageFingerprint(input.Query, "activity-context")
	}
	var slow *slowFilter
	if slowest {
		fingerprint = slowFingerprint(input.Query, input.MinMs)
		slow = &slowFilter{min: input.MinMs}
	}
	var after *pageCursor
	if input.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(input.Cursor)
		if err != nil {
			return audit.Page{}, fmt.Errorf("%w: cursor is invalid", audit.ErrInvalidInput)
		}
		var parsed pageCursor
		if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Fingerprint != fingerprint || parsed.At.IsZero() || (slowest && !slowCursorMetricValid(parsed.Metric)) {
			return audit.Page{}, fmt.Errorf("%w: cursor does not match this query", audit.ErrInvalidInput)
		}
		objectID, err := bson.ObjectIDFromHex(parsed.ObjectID)
		if err != nil || parsed.SourceID == "" {
			return audit.Page{}, fmt.Errorf("%w: cursor is invalid", audit.ErrInvalidInput)
		}
		selected := false
		for _, id := range input.Query.SourceIDs {
			if id == parsed.SourceID {
				selected = true
			}
		}
		if !selected || (input.APIKey != "" && parsed.SourceID != cohort[0]) {
			return audit.Page{}, fmt.Errorf("%w: cursor source does not match query", audit.ErrInvalidInput)
		}
		parsed.ObjectID = objectID.Hex()
		after = &parsed
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	selectedIDs := input.Query.SourceIDs
	if input.APIKey != "" {
		selectedIDs = []string{cohort[0]}
	}
	profiles := make([]*profile, 0, len(selectedIDs))
	releases := make([]func(), 0, len(selectedIDs))
	for _, id := range selectedIDs {
		p, release, err := s.leaseSource(id)
		if err != nil {
			for _, done := range releases {
				done()
			}
			return audit.Page{}, err
		}
		profiles, releases = append(profiles, p), append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	rows := make([]pageRow, 0, len(selectedIDs)*(input.Limit+1))
	var legacyRecords atomic.Int64
	var legacyBytes atomic.Int64
	page := audit.Page{Sources: make([]audit.SourceResult, 0, len(profiles))}
	for _, p := range profiles {
		if err := acquire(ctx, s.global); err != nil {
			code, message := sourceAdmissionError(err)
			page.Sources = append(page.Sources, audit.SourceResult{SourceID: p.source.ID, ErrorCode: code, Error: message})
			continue
		}
		s.mu.RLock()
		per := s.perSource[p.source.ID]
		s.mu.RUnlock()
		if per == nil {
			<-s.global
			page.Sources = append(page.Sources, audit.SourceResult{SourceID: p.source.ID, ErrorCode: "source_unavailable", Error: audit.SourceQueryErrorMessage("source_unavailable")})
			continue
		}
		if err := acquire(ctx, per); err != nil {
			<-s.global
			code, message := sourceAdmissionError(err)
			page.Sources = append(page.Sources, audit.SourceResult{SourceID: p.source.ID, ErrorCode: code, Error: message})
			continue
		}
		var sourceRows []pageRow
		pipeline := requestPipeline(input.Query, cohort, p.source.ID, after, input.Limit+1)
		if overlap {
			pipeline = activityPipeline(input.Query, p.source.ID, after, input.Limit+1)
		}
		if slowest {
			pipeline = slowestPipeline(input.Query, p.source.ID, after, input.MinMs, input.Limit+1)
		}
		var err error
		if usesLegacyMongo(p.shared.client) {
			sourceRows, err = readLegacyRequestRowsSorted(ctx, p, input.Query, cohort, after, legacyRowMetadataPipeline(pipeline), input.Limit+1, &legacyRecords, &legacyBytes, overlap, slow)
			if err == nil {
				rows = append(rows, sourceRows...)
			}
		} else {
			cur, aggregateErr := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
			err = aggregateErr
			if err == nil {
				sourceRows = make([]pageRow, 0, input.Limit+1)
				for cur.Next(ctx) {
					var doc requestDocument
					if decodeErr := cur.Decode(&doc); decodeErr != nil {
						err = errors.New("decode failure")
						break
					}
					if doc.ID.IsZero() || doc.InitiatedAt == nil {
						continue
					}
					sourceRows = append(sourceRows, pageRow{request: audit.RequestRecord{
						ID: doc.ID.Hex(), SourceID: p.source.ID, Controller: doc.Controller, Action: doc.Action,
						Kind: doc.Kind, Outcome: doc.Outcome, Status: doc.Status, URL: doc.URL,
						InitiatedAt: doc.InitiatedAt, CompletedAt: doc.CompletedAt, DurationMs: doc.DurationMs,
						ApiTimeMs: doc.ApiTimeMs, AuditCreateTimeMs: doc.AuditCreateTimeMs, PayloadRecordTimeMs: doc.PayloadRecordTimeMs,
						RequestKB: doc.RequestKB, ResponseKB: doc.ResponseKB,
						UserID: doc.UserID, UserIP: doc.UserIP, UserAgent: doc.UserAgent, UIVersion: doc.UIVersion,
						CreatedOn: doc.CreatedOn, UpdatedOn: doc.UpdatedOn, PollingTypeRaw: doc.PollingTypeRaw,
					}, at: *doc.InitiatedAt, object: doc.ID})
				}
				if err == nil {
					err = cur.Err()
				}
				closeMongoCursor(cur)
				if err == nil {
					rows = append(rows, sourceRows...)
				}
			}
		}
		<-per
		<-s.global
		if err != nil {
			if errors.Is(err, audit.ErrLimit) {
				return audit.Page{}, err
			}
			code, message := sourceQueryError(ctx, err)
			page.Sources = append(page.Sources, audit.SourceResult{SourceID: p.source.ID, ErrorCode: code, Error: message})
			continue
		}
		warning := p.indexWarning
		if warning != "" {
			warning += " "
		}
		warning += "Rows without valid effective timestamps are omitted from sorted request results; legacy rows may use CreatedOn when RequestInitiatedAt is absent."
		page.Sources = append(page.Sources, audit.SourceResult{SourceID: p.source.ID, OK: true, Version: p.version, IndexWarning: warning})
	}
	sort.Slice(rows, func(i, j int) bool {
		if slowest {
			return slowRowBefore(rows[i], rows[j], input.Query.Metric)
		}
		return pageRowBefore(rows[i], rows[j])
	})
	more := len(rows) > input.Limit
	if more {
		rows = rows[:input.Limit]
	}
	page.Rows = make([]audit.RequestRecord, len(rows))
	for i := range rows {
		page.Rows[i] = rows[i].request
	}
	if more && len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor := pageCursor{At: last.at.UTC(), SourceID: last.request.SourceID, ObjectID: last.object.Hex(), Fingerprint: fingerprint}
		if slowest {
			page.Truncated = true
			cursor.Metric = audit.RequestMetric(last.request, input.Query.Metric)
		}
		raw, _ := json.Marshal(cursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

func requestPipeline(q audit.Query, cohort [5]string, sourceID string, after *pageCursor, limit int) mongo.Pipeline {
	startMatch := bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$gte", Value: q.Start}, {Key: "$lt", Value: q.End}}}}
	fallback := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "RequestInitiatedAt", Value: bson.D{{Key: "$exists", Value: false}}}}, bson.D{{Key: "RequestInitiatedAt", Value: nil}}}}},
		bson.D{{Key: "CreatedOn", Value: bson.D{{Key: "$gte", Value: q.Start}, {Key: "$lt", Value: q.End}}}},
	}}}
	match := bson.D{{Key: "$or", Value: bson.A{startMatch, fallback}}}
	if cohort[1] != "" {
		match = append(match, bson.E{Key: "ControllerName", Value: cohort[1]})
	}
	if cohort[2] != "" {
		match = append(match, bson.E{Key: "ActionName", Value: cohort[2]})
	}
	if sourceID == "" {
		return nil
	}
	date := func(a, b string) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + a}}, "date"}}}, "$" + a, bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + b}}, "date"}}}, "$" + b, nil}}}}}}
	}
	polling := bson.D{{Key: "$toLower", Value: bson.D{{Key: "$trim", Value: bson.D{{Key: "input", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$RequestPollingType", ""}}}}}}}}}
	agent := bson.D{{Key: "$toLower", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$UserAgent", ""}}}}}
	kind := bson.D{{Key: "$switch", Value: bson.D{{Key: "branches", Value: bson.A{
		bson.D{{Key: "case", Value: bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{polling, "hangfire"}}}, bson.D{{Key: "$eq", Value: bson.A{agent, "hangfire"}}}}}}}, {Key: "then", Value: "hangfire"}},
		bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{polling, "synchronous"}}}}, {Key: "then", Value: "synchronous"}},
		bson.D{{Key: "case", Value: bson.D{{Key: "$eq", Value: bson.A{polling, "asynchronous"}}}}, {Key: "then", Value: "asynchronous"}},
	}}, {Key: "default", Value: "unknown"}}}}
	statusText := bson.D{{Key: "$toUpper", Value: bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: "$StatusCode"}, {Key: "to", Value: "string"}, {Key: "onError", Value: ""}, {Key: "onNull", Value: ""}}}}}}
	statusNumber := bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: "$StatusCode"}, {Key: "to", Value: "int"}, {Key: "onError", Value: nil}, {Key: "onNull", Value: nil}}}}
	pending := pendingAsyncExpr(kind, statusText)
	statusClass := statusClassExpr(kind, statusText, statusNumber)
	outcome := outcomeFromClassExpr()
	completed := date("RequestCompletedAt", "RequestCompletedAt")
	observed := bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditAt"}}, "date"}}}, bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$_auditCompleted"}}, "date"}}}}}}, bson.D{{Key: "$subtract", Value: bson.A{"$_auditCompleted", "$_auditAt"}}}, nil}}}
	duration := bson.D{{Key: "$cond", Value: bson.A{"$_auditPending", nil, observed}}}
	stages := mongo.Pipeline{
		bson.D{{Key: "$match", Value: match}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditAt", Value: date("RequestInitiatedAt", "CreatedOn")}, {Key: "_auditCompleted", Value: completed}, {Key: "_auditKind", Value: kind}, {Key: "_auditPending", Value: pending}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_statusClass", Value: statusClass}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "_auditOutcome", Value: outcome}, {Key: "_auditDuration", Value: duration}}}},
	}
	cohortMatch := bson.D{{Key: "_auditAt", Value: bson.D{{Key: "$type", Value: "date"}}}}
	if cohort[3] != "" && cohort[3] != "all" {
		cohortMatch = append(cohortMatch, bson.E{Key: "_auditKind", Value: bson.D{{Key: "$in", Value: stringsToBSON(audit.KindMembers(cohort[3])...)}}})
	}
	if cohort[4] != "" && cohort[4] != "all" {
		cohortMatch = append(cohortMatch, outcomeFilterElem(cohort[4]))
		if q.Outcome != cohort[4] && audit.IsStatusClassOutcome(q.Outcome) {
			cohortMatch = append(cohortMatch, outcomeFilterElem(q.Outcome))
		}
	}
	stages = append(stages, bson.D{{Key: "$match", Value: cohortMatch}})
	if after != nil {
		lastID, _ := bson.ObjectIDFromHex(after.ObjectID)
		var afterMatch bson.A
		if sourceID > after.SourceID {
			afterMatch = bson.A{bson.D{{Key: "_auditAt", Value: bson.D{{Key: "$lt", Value: after.At}}}}, bson.D{{Key: "_auditAt", Value: after.At}}}
		} else if sourceID == after.SourceID {
			afterMatch = bson.A{bson.D{{Key: "_auditAt", Value: bson.D{{Key: "$lt", Value: after.At}}}}, bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "_auditAt", Value: after.At}}, bson.D{{Key: "_id", Value: bson.D{{Key: "$lt", Value: lastID}}}}}}}}
		} else {
			afterMatch = bson.A{bson.D{{Key: "_auditAt", Value: bson.D{{Key: "$lt", Value: after.At}}}}}
		}
		stages = append(stages, bson.D{{Key: "$match", Value: bson.D{{Key: "$or", Value: afterMatch}}}})
	}
	stages = append(stages,
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_auditAt", Value: -1}, {Key: "_id", Value: -1}}}},
		bson.D{{Key: "$limit", Value: limit}},
		bson.D{{Key: "$project", Value: bson.D{
			{Key: "_id", Value: 1}, {Key: "sourceId", Value: sourceID}, {Key: "controller", Value: stringExpr("$ControllerName")}, {Key: "action", Value: stringExpr("$ActionName")}, {Key: "kind", Value: "$_auditKind"}, {Key: "outcome", Value: "$_auditOutcome"}, {Key: "status", Value: stringExpr("$StatusCode")}, {Key: "url", Value: stringExpr("$RequestMainUrl")}, {Key: "initiatedAt", Value: "$_auditAt"}, {Key: "completedAt", Value: "$_auditCompleted"}, {Key: "durationMs", Value: duration},
			{Key: "apiTimeMs", Value: numberExpr("$ApiTime")}, {Key: "auditCreateTimeMs", Value: numberExpr("$AuditCreateTime")}, {Key: "payloadRecordTimeMs", Value: numberExpr("$PayloadRecordTime")}, {Key: "requestKB", Value: numberExprDefault("$RequestLengthInKiloBytes")}, {Key: "responseKB", Value: numberExprDefault("$ResponseLengthInKiloBytes")},
				{Key: "userId", Value: userIDExpr("$UserId")}, {Key: "userIp", Value: cappedStringExpr("$UserIp", "UserIp")}, {Key: "userAgent", Value: cappedStringExpr("$UserAgent", "UserAgent")}, {Key: "uiVersion", Value: cappedStringExpr("$PayrollUIVersion", "PayrollUIVersion")},
				{Key: "createdOn", Value: dateOnlyExpr("$CreatedOn")}, {Key: "updatedOn", Value: dateOnlyExpr("$UpdatedOn")}, {Key: "pollingTypeRaw", Value: stringExpr("$RequestPollingType")},
		}}},
	)
	return stages
}

func numberExpr(input string) bson.D {
	return bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: input}, {Key: "to", Value: "double"}, {Key: "onError", Value: nil}, {Key: "onNull", Value: nil}}}}
}

func stringExpr(input string) bson.D {
	return bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: input}, {Key: "to", Value: "string"}, {Key: "onError", Value: ""}, {Key: "onNull", Value: ""}}}}
}

// cappedStringExpr is stringExpr limited to the legacy metadata cap for field,
// so display-only columns cannot grow a row without bound.
func cappedStringExpr(input, field string) bson.D {
	return bson.D{{Key: "$substrCP", Value: bson.A{stringExpr(input), 0, legacyMetadataStringCaps[field]}}}
}

// userIDExpr converts with $convert to a 32-bit integer. $convert truncates a
// fractional double or decimal toward zero (77.9 becomes 77) and returns null
// (onError) for out-of-range numbers and for strings that are not whole
// integers. The legacy path (legacyInt) is stricter: it returns null for any
// fractional value, so the two differ only for fractional numeric UserId
// values, which the audit schema should never contain.
func userIDExpr(input string) bson.D {
	return bson.D{{Key: "$convert", Value: bson.D{{Key: "input", Value: input}, {Key: "to", Value: "int"}, {Key: "onError", Value: nil}, {Key: "onNull", Value: nil}}}}
}

func dateOnlyExpr(input string) bson.D {
	return bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: input}}, "date"}}}, input, nil}}}
}

func numberExprDefault(input string) bson.D {
	return bson.D{{Key: "$ifNull", Value: bson.A{numberExpr(input), 0}}}
}

func decodeCohort(key string) ([5]string, error) {
	var result [5]string
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return result, err
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil || len(values) != 5 {
		return result, errors.New("invalid cohort key")
	}
	copy(result[:], values)
	return result, nil
}

func validateCohortQuery(q audit.Query, cohort [5]string) error {
	selected := false
	for _, id := range q.SourceIDs {
		if id == cohort[0] {
			selected = true
		}
	}
	if !selected || (q.Controller != "" && q.Controller != cohort[1]) || (q.Action != "" && q.Action != cohort[2]) || !audit.KindMatches(q.Kind, cohort[3], false) || !audit.BucketOutcomeMatches(q.Outcome, cohort[4]) {
		return fmt.Errorf("%w: API key does not match query", audit.ErrInvalidInput)
	}
	return nil
}

func pageFingerprint(q audit.Query, apiKey string) string {
	q.Start, q.End = q.Start.UTC(), q.End.UTC()
	q.BaselineStart, q.BaselineEnd = q.BaselineStart.UTC(), q.BaselineEnd.UTC()
	b, _ := json.Marshal(struct {
		Query  audit.Query `json:"query"`
		APIKey string      `json:"apiKey"`
	}{q, apiKey})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func pageRowBefore(a, b pageRow) bool {
	if !a.at.Equal(b.at) {
		return a.at.After(b.at)
	}
	if a.request.SourceID != b.request.SourceID {
		return a.request.SourceID < b.request.SourceID
	}
	return a.object.Hex() > b.object.Hex()
}

func closeMongoCursor(cur mongoCursor) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cur.Close(ctx)
}

var _ mongoCursor = (*mongo.Cursor)(nil)
