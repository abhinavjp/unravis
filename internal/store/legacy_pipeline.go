package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// A legacy scan is deliberately bounded by both records and projected BSON
// bytes. The extra server row is a sentinel that makes hitting either bound
// fail closed instead of returning partial statistics.
const (
	maxLegacyMetadataRecords = int64(100000)
	maxLegacyMetadataBytes   = int64(16 * 1024 * 1024)
	legacyMetadataMarker     = "_auditLegacyMetadataV1"
)

var legacyMetadataFields = []string{
	"RequestInitiatedAt", "CreatedOn", "RequestCompletedAt", "StatusCode",
	"RequestPollingType", "UserAgent", "ControllerName", "ActionName",
	"RequestMainUrl", "ApiTime", "AuditCreateTime", "PayloadRecordTime",
	"RequestLengthInKiloBytes", "ResponseLengthInKiloBytes",
}

var legacyMetadataStringCaps = map[string]int{
	"ControllerName": 1024, "ActionName": 1024, "RequestMainUrl": 8192,
	"StatusCode": 128, "RequestPollingType": 128, "UserAgent": 512,
	"ApiTime": 128, "AuditCreateTime": 128, "PayloadRecordTime": 128,
	"RequestLengthInKiloBytes": 128, "ResponseLengthInKiloBytes": 128,
	"UserId": 128, "UserIp": 64, "PayrollUIVersion": 128,
}

// legacyRowMetadataFields adds the audit-evidence columns that only request
// rows and the record detail show. Summary scans keep the narrower
// legacyMetadataFields so these extra bytes never count against their limits.
var legacyRowMetadataFields = append(append([]string(nil), legacyMetadataFields...), "UserId", "UserIp", "PayrollUIVersion", "UpdatedOn")

type legacyBucketKey struct {
	controller, action, kind, outcome, state string
	atUnix                                   int64
	bin                                      int
}

// rewriteLegacyPipeline is the MongoDB 3.4 adapter boundary. Server-side
// analytics expressions are replaced by an indexed match and a bounded,
// metadata-only stream that is normalized by this package. Payload pipelines
// use only operators available in 3.4 and pass through unchanged.
func rewriteLegacyPipeline(pipeline mongo.Pipeline) (mongo.Pipeline, error) {
	if isLegacyMetadataPipeline(pipeline) {
		return pipeline, nil
	}
	if len(pipeline) == 0 || len(pipeline[0]) == 0 {
		return nil, errors.New("empty legacy aggregation")
	}
	first, ok := pipeline[0][0].Value.(bson.D)
	if !ok || pipeline[0][0].Key != "$match" {
		return nil, errors.New("unsupported legacy aggregation shape")
	}
	encoded, _ := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	text := string(encoded)
	if !strings.Contains(text, "_auditBucketAt") && !strings.Contains(text, "_auditKind") {
		// The payload lookup is already bounded to one _id and uses $type and
		// $substrCP, both supported by MongoDB 3.4.
		return pipeline, nil
	}
	return rawLegacyMetadataPipeline(first), nil
}

func isLegacyMetadataPipeline(pipeline mongo.Pipeline) bool {
	encoded, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	return err == nil && strings.Contains(string(encoded), legacyMetadataMarker)
}

func legacyMetadataPipeline(pipeline mongo.Pipeline) mongo.Pipeline {
	return legacyMetadataPipelineFor(pipeline, legacyMetadataFields)
}

// legacyRowMetadataPipeline is legacyMetadataPipeline for request rows: it also
// projects the audit-evidence columns.
func legacyRowMetadataPipeline(pipeline mongo.Pipeline) mongo.Pipeline {
	return legacyMetadataPipelineFor(pipeline, legacyRowMetadataFields)
}

func legacyMetadataPipelineFor(pipeline mongo.Pipeline, fields []string) mongo.Pipeline {
	if isLegacyMetadataPipeline(pipeline) {
		return pipeline
	}
	if len(pipeline) == 0 || len(pipeline[0]) == 0 || pipeline[0][0].Key != "$match" {
		return nil
	}
	match, ok := pipeline[0][0].Value.(bson.D)
	if !ok {
		return nil
	}
	return rawLegacyMetadataPipelineFor(match, fields)
}

func rawLegacyMetadataPipeline(match bson.D) mongo.Pipeline {
	return rawLegacyMetadataPipelineFor(match, legacyMetadataFields)
}

func rawLegacyMetadataPipelineFor(match bson.D, fields []string) mongo.Pipeline {
	projection := bson.D{{Key: "_id", Value: 1}, {Key: legacyMetadataMarker, Value: "v1"}}
	var oversized bson.A
	for _, field := range fields {
		cap, isString := legacyMetadataStringCaps[field]
		if isString {
			oversized = append(oversized, bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$" + field}}, "string"}}},
				bson.D{{Key: "$gt", Value: bson.A{bson.D{{Key: "$strLenCP", Value: "$" + field}}, cap}}}, false,
			}}})
		}
		projection = append(projection, bson.E{Key: field, Value: boundedMetadataValue(field, cap, isString)})
	}
	projection = append(projection, bson.E{Key: "_auditMetadataOversized", Value: bson.D{{Key: "$or", Value: oversized}}})
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: match}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
		bson.D{{Key: "$limit", Value: maxLegacyMetadataRecords + 1}},
		bson.D{{Key: "$project", Value: projection}},
	}
}

func boundedMetadataValue(field string, stringCap int, stringAllowed bool) bson.D {
	input := "$" + field
	typeOf := bson.D{{Key: "$type", Value: input}}
	if !stringAllowed {
		return bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{typeOf, "date"}}}, input, nil}}}
	}
	stringValue := any(nil)
	if stringAllowed {
		stringValue = bson.D{{Key: "$substrCP", Value: bson.A{input, 0, stringCap}}}
	}
	return bson.D{{Key: "$cond", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{typeOf, "date"}}}, input,
		bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{typeOf, "string"}}}, stringValue,
			bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$in", Value: bson.A{typeOf, bson.A{"int", "long", "double", "decimal", "bool"}}}}, input, nil,
			}}},
		}}},
	}}}
}

func legacyString(value bson.RawValue) string {
	if value.Type == bson.TypeString {
		return value.StringValue()
	}
	if value.Type == bson.TypeNull || value.Type == 0 {
		return ""
	}
	var decoded any
	if value.Unmarshal(&decoded) != nil || decoded == nil {
		return ""
	}
	return fmt.Sprint(decoded)
}

func legacyDate(value bson.RawValue) *time.Time {
	if value.Type != bson.TypeDateTime {
		return nil
	}
	var decoded time.Time
	if value.Unmarshal(&decoded) != nil {
		return nil
	}
	return &decoded
}

func legacyNumber(value bson.RawValue) *float64 {
	if value.Type == 0 || value.Type == bson.TypeNull {
		return nil
	}
	if value.Type == bson.TypeDateTime {
		if millis, ok := value.DateTimeOK(); ok {
			number := float64(millis)
			return &number
		}
		return nil
	}
	var decoded any
	if value.Unmarshal(&decoded) != nil || decoded == nil {
		return nil
	}
	var number float64
	switch v := decoded.(type) {
	case float64:
		number = v
	case int32:
		number = float64(v)
	case int64:
		number = float64(v)
	case int:
		number = float64(v)
	case bson.Decimal128:
		parsed, err := strconv.ParseFloat(v.String(), 64)
		if err != nil {
			return nil
		}
		number = parsed
	case bool:
		if v {
			number = 1
		}
	case time.Time:
		number = float64(v.UnixMilli())
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil
		}
		number = parsed
	default:
		return nil
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return &number
}

// legacyInt reads a whole-number identifier such as UserId; fractional or
// out-of-range numbers are reported as absent rather than rounded.
func legacyInt(value bson.RawValue) *int {
	number := legacyNumber(value)
	if number == nil || *number != math.Trunc(*number) || math.Abs(*number) > math.MaxInt32 {
		return nil
	}
	result := int(*number)
	return &result
}

func legacyNumberDefault(value bson.RawValue) float64 {
	if n := legacyNumber(value); n != nil {
		return *n
	}
	return 0
}

func legacyKind(polling, userAgent string) string {
	polling = strings.ToLower(strings.TrimSpace(polling))
	userAgent = strings.ToLower(userAgent)
	switch {
	case polling == "hangfire" || userAgent == "hangfire":
		return "hangfire"
	case polling == "synchronous":
		return "synchronous"
	case polling == "asynchronous":
		return "asynchronous"
	default:
		return "unknown"
	}
}

func legacyOutcome(kind string, value bson.RawValue) string {
	status := strings.ToUpper(legacyString(value))
	if kind == "hangfire" {
		if status == "SUCCEEDED" {
			return "success"
		}
		if status == "FAILED" {
			return "failure"
		}
	}
	sets := statusNames()
	if containsString(sets.success, status) {
		return "success"
	}
	if containsString(sets.failure, status) {
		return "failure"
	}
	if number := legacyStatusNumber(value); number != nil {
		if *number >= 200 && *number < 400 {
			return "success"
		}
		if *number >= 400 && *number < 600 {
			return "failure"
		}
	}
	return "unknown"
}

func legacyStatusNumber(value bson.RawValue) *float64 {
	if value.Type == bson.TypeString {
		parsed, err := strconv.ParseInt(value.StringValue(), 10, 64)
		if err != nil {
			return nil
		}
		number := float64(parsed)
		return &number
	}
	switch value.Type {
	case bson.TypeInt32, bson.TypeInt64, bson.TypeDouble, bson.TypeDecimal128, bson.TypeBoolean:
	default:
		return nil
	}
	if number := legacyNumber(value); number != nil {
		truncated := math.Trunc(*number)
		if truncated < math.MinInt32 || truncated > math.MaxInt32 {
			return nil
		}
		return &truncated
	}
	return nil
}

func containsString(values bson.A, needle string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == needle {
			return true
		}
	}
	return false
}

type legacyRecord struct {
	ID                                        bson.ObjectID
	Controller, Action                        string
	Kind, Outcome, Status                     string
	URL                                       string
	At, Completed                             *time.Time
	Duration, API, AuditCreate, PayloadRecord *float64
	RequestKB, ResponseKB                     float64
	State                                     string
	AuditCreateMissing, PendingAsync          bool
	UserID                                    *int
	UserIP, UserAgent, UIVersion              string
	PollingTypeRaw                            string
	CreatedOn, UpdatedOn                      *time.Time
}

func readLegacyBuckets(ctx context.Context, p *profile, q audit.Query, start time.Time, pipeline mongo.Pipeline, total, totalBytes, records, bytes *atomic.Int64) ([]audit.Bucket, error) {
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, err
	}
	defer closeMongoCursor(cur)
	grouped := make(map[legacyBucketKey]*audit.Bucket)
	for cur.Next(ctx) {
		if records.Add(1) > maxLegacyMetadataRecords {
			return nil, legacyScanLimit("record")
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, errors.New("could not decode legacy metadata")
		}
		if bytes.Add(int64(len(raw))) > maxLegacyMetadataBytes {
			return nil, legacyScanLimit("byte")
		}
		if raw.Lookup("_auditMetadataOversized").Boolean() {
			return nil, legacyScanLimit("metadata field")
		}
		record, ok := normalizeLegacyRecord(raw, q.Metric)
		if !ok || record.At == nil {
			continue
		}
		if record.At.Before(start) {
			continue
		}
		bucketAt := legacyBucketAt(*record.At, q.BucketSeconds)
		if start.Equal(q.BaselineStart) {
			bucketAt = start.UTC()
		}
		if q.Kind != "all" && record.State == "valid" && record.Kind != q.Kind {
			continue
		}
		if q.Outcome != "all" && record.State == "valid" && record.Outcome != q.Outcome && record.Outcome != "unknown" {
			continue
		}
		bin := 0
		if record.State == "valid" && record.Duration != nil {
			bin = audit.BinIndex(*record.Duration)
		}
		key := legacyBucketKey{controller: record.Controller, action: record.Action, kind: record.Kind, outcome: record.Outcome, state: record.State, atUnix: bucketAt.Unix(), bin: bin}
		bucket := grouped[key]
		if bucket == nil {
			if total.Add(1) > audit.MaxSummaryRows {
				return nil, fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrSummaryRowLimit)
			}
			bucket = &audit.Bucket{SourceID: p.source.ID, Controller: record.Controller, Action: record.Action, Kind: record.Kind, Outcome: record.Outcome, State: record.State, At: bucketAt.UTC(), Bin: bin}
			grouped[key] = bucket
		}
		bucket.Count++
		if record.PendingAsync {
			bucket.PendingAsync++
		}
		if record.State == "valid" && record.AuditCreateMissing && q.Metric != "api" {
			bucket.ObservedPartial++
		}
		if record.State == "valid" && record.Duration != nil {
			ms := *record.Duration
			bucket.SumMs += ms
			if bucket.Count == 1 || ms < bucket.MinMs {
				bucket.MinMs = ms
			}
			if bucket.Count == 1 || ms > bucket.MaxMs {
				bucket.MaxMs = ms
			}
			addLegacyPhase(&bucket.SumAuditMs, &bucket.NAudit, record.AuditCreate)
			addLegacyPhase(&bucket.SumApiMs, &bucket.NApi, record.API)
			addLegacyPhase(&bucket.SumPayloadMs, &bucket.NPayload, record.PayloadRecord)
		}
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	if records.Load() > maxLegacyMetadataRecords {
		return nil, legacyScanLimit("record")
	}
	result := make([]audit.Bucket, 0, len(grouped))
	for _, bucket := range grouped {
		encoded, err := bson.Marshal(bucket)
		if err != nil || totalBytes.Add(int64(len(encoded))) > maxAnalysisBytes {
			return nil, fmt.Errorf("%w: %w", audit.ErrLimit, audit.ErrSummaryBytesLimit)
		}
		result = append(result, *bucket)
	}
	return result, nil
}

// addLegacyPhase mirrors the server pipeline: a phase counts for a valid
// document only when it is a finite non-negative number.
func addLegacyPhase(sum *float64, n *int64, value *float64) {
	if value != nil && *value >= 0 && *value < math.MaxFloat64 {
		*sum += *value
		*n++
	}
}

func legacyBucketAt(at time.Time, seconds int) time.Time {
	width := int64(seconds)
	unix := at.UTC().Unix()
	quotient, remainder := unix/width, unix%width
	if remainder < 0 {
		quotient--
	}
	return time.Unix(quotient*width, 0).UTC()
}

func legacyScanLimit(kind string) error {
	return fmt.Errorf("%w: %w: MongoDB 3.4 metadata scan exceeded its %s limit (%d records, %d bytes); narrow the date or controller/action filters", audit.ErrLimit, audit.ErrLegacyScanLimit, kind, maxLegacyMetadataRecords, maxLegacyMetadataBytes)
}

func readLegacyRequestRows(ctx context.Context, p *profile, q audit.Query, cohort [5]string, after *pageCursor, pipeline mongo.Pipeline, maxRows int, records, bytes *atomic.Int64) ([]pageRow, error) {
	return readLegacyRequestRowsMode(ctx, p, q, cohort, after, pipeline, maxRows, records, bytes, false)
}
func readLegacyRequestRowsMode(ctx context.Context, p *profile, q audit.Query, cohort [5]string, after *pageCursor, pipeline mongo.Pipeline, maxRows int, records, bytes *atomic.Int64, overlap bool) ([]pageRow, error) {
	return readLegacyRequestRowsSorted(ctx, p, q, cohort, after, pipeline, maxRows, records, bytes, overlap, false)
}
func readLegacyRequestRowsSorted(ctx context.Context, p *profile, q audit.Query, cohort [5]string, after *pageCursor, pipeline mongo.Pipeline, maxRows int, records, bytes *atomic.Int64, overlap, slowest bool) ([]pageRow, error) {
	cur, err := p.shared.client.Aggregate(ctx, p.source.Database, p.source.Collection, pipeline)
	if err != nil {
		return nil, err
	}
	defer closeMongoCursor(cur)
	rows := make([]pageRow, 0, 128)
	for cur.Next(ctx) {
		if records.Add(1) > maxLegacyMetadataRecords {
			return nil, legacyScanLimit("record")
		}
		var raw bson.Raw
		if err := cur.Decode(&raw); err != nil {
			return nil, errors.New("could not decode legacy request metadata")
		}
		if bytes.Add(int64(len(raw))) > maxLegacyMetadataBytes {
			return nil, legacyScanLimit("byte")
		}
		if raw.Lookup("_auditMetadataOversized").Boolean() {
			return nil, legacyScanLimit("metadata field")
		}
		record, _ := normalizeLegacyRecord(raw, "total")
		if record.ID.IsZero() || record.At == nil || (!overlap && (record.At.Before(q.Start) || !record.At.Before(q.End))) || (overlap && !audit.ActivityOverlaps(record.At, record.Completed, q.Start, q.End)) {
			continue
		}
		if cohort[3] != "" && cohort[3] != "all" && record.Kind != cohort[3] {
			continue
		}
		if cohort[4] != "" && cohort[4] != "all" && record.Outcome != cohort[4] {
			continue
		}
		if after != nil {
			cursorRow := pageRow{at: after.At, object: legacyObjectID(after.ObjectID), request: audit.RequestRecord{SourceID: after.SourceID}}
			candidate := pageRow{at: *record.At, object: record.ID, request: audit.RequestRecord{SourceID: p.source.ID}}
			if !pageRowBefore(cursorRow, candidate) {
				continue
			}
		}
		row := pageRow{request: audit.RequestRecord{
			ID: record.ID.Hex(), SourceID: p.source.ID, Controller: record.Controller, Action: record.Action,
			Kind: record.Kind, Outcome: record.Outcome, Status: record.Status, URL: record.URL,
			InitiatedAt: record.At, CompletedAt: record.Completed, DurationMs: record.Duration,
			ApiTimeMs: record.API, AuditCreateTimeMs: record.AuditCreate, PayloadRecordTimeMs: record.PayloadRecord,
			RequestKB: record.RequestKB, ResponseKB: record.ResponseKB,
			UserID: record.UserID, UserIP: record.UserIP, UserAgent: record.UserAgent, UIVersion: record.UIVersion,
			CreatedOn: record.CreatedOn, UpdatedOn: record.UpdatedOn, PollingTypeRaw: record.PollingTypeRaw,
		}, at: *record.At, object: record.ID}
		if slowest {
			if audit.RequestMetric(row.request, q.Metric) == nil {
				continue
			}
			rows = append(rows, row)
			sort.Slice(rows, func(i, j int) bool { return slowRowBefore(rows[i], rows[j], q.Metric) })
			if len(rows) > maxRows {
				rows = rows[:maxRows]
			}
		} else {
			insertPageRow(&rows, row, maxRows)
		}
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func legacyObjectID(value string) bson.ObjectID {
	id, _ := bson.ObjectIDFromHex(value)
	return id
}

func insertPageRow(rows *[]pageRow, candidate pageRow, limit int) {
	index := sort.Search(len(*rows), func(i int) bool { return pageRowBefore(candidate, (*rows)[i]) })
	if index >= limit {
		return
	}
	*rows = append(*rows, pageRow{})
	copy((*rows)[index+1:], (*rows)[index:])
	(*rows)[index] = candidate
	if len(*rows) > limit {
		*rows = (*rows)[:limit]
	}
}

func normalizeLegacyRecord(raw bson.Raw, metric string) (legacyRecord, bool) {
	var record legacyRecord
	record.ID, _ = raw.Lookup("_id").ObjectIDOK()
	record.Controller = legacyString(raw.Lookup("ControllerName"))
	record.Action = legacyString(raw.Lookup("ActionName"))
	record.Status = legacyString(raw.Lookup("StatusCode"))
	record.URL = legacyString(raw.Lookup("RequestMainUrl"))
	record.Kind = legacyKind(legacyString(raw.Lookup("RequestPollingType")), legacyString(raw.Lookup("UserAgent")))
	record.Outcome = legacyOutcome(record.Kind, raw.Lookup("StatusCode"))
	record.PendingAsync = strings.ToLower(strings.TrimSpace(legacyString(raw.Lookup("RequestPollingType")))) == "asynchronous" && strings.ToUpper(record.Status) == "ACCEPTED"
	if record.PendingAsync {
		record.Outcome = "unknown"
	}
	record.At = legacyDate(raw.Lookup("RequestInitiatedAt"))
	if raw.Lookup("RequestInitiatedAt").Type == 0 || raw.Lookup("RequestInitiatedAt").Type == bson.TypeNull {
		record.At = legacyDate(raw.Lookup("CreatedOn"))
		record.AuditCreateMissing = true
	}
	record.Completed = legacyDate(raw.Lookup("RequestCompletedAt"))
	record.API = legacyNumber(raw.Lookup("ApiTime"))
	record.AuditCreate = legacyNumber(raw.Lookup("AuditCreateTime"))
	record.PayloadRecord = legacyNumber(raw.Lookup("PayloadRecordTime"))
	record.RequestKB = legacyNumberDefault(raw.Lookup("RequestLengthInKiloBytes"))
	record.ResponseKB = legacyNumberDefault(raw.Lookup("ResponseLengthInKiloBytes"))
	record.UserID = legacyInt(raw.Lookup("UserId"))
	record.UserIP = legacyString(raw.Lookup("UserIp"))
	record.UserAgent = legacyString(raw.Lookup("UserAgent"))
	record.UIVersion = legacyString(raw.Lookup("PayrollUIVersion"))
	record.PollingTypeRaw = legacyString(raw.Lookup("RequestPollingType"))
	record.CreatedOn = legacyDate(raw.Lookup("CreatedOn"))
	record.UpdatedOn = legacyDate(raw.Lookup("UpdatedOn"))
	var duration *float64
	missing := record.At == nil || record.Completed == nil
	if metric == "api" {
		if !missing {
			duration = record.API
		}
		if raw.Lookup("ApiTime").Type == 0 || raw.Lookup("ApiTime").Type == bson.TypeNull {
			missing = true
		}
	} else if record.At != nil && record.Completed != nil {
		value := float64(record.Completed.UnixMilli()) - float64(record.At.UnixMilli())
		duration = &value
	}
	if record.PendingAsync {
		duration = nil
	}
	record.Duration = duration
	if record.PendingAsync {
		record.State = "incomplete"
	} else if duration == nil {
		if missing {
			record.State = "incomplete"
		} else {
			record.State = "invalid"
		}
	} else if math.IsNaN(*duration) || math.IsInf(*duration, 0) || *duration < 0 || *duration >= math.MaxFloat64 {
		record.State = "invalid"
	} else {
		record.State = "valid"
	}
	return record, true
}
