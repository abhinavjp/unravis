package store

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// perfKind says what shape a perf role has on the stored document.
type perfKind int

const (
	perfNumber    perfKind = iota // one number
	perfFlag                      // one boolean
	perfNamed                     // list of {name, ms}
	perfSlowList                  // list of {count, ms}; no text subfield is ever projected
	perfValueList                 // list of {name, value}
)

// perfField wires one perf role to the stored document. key is the flat
// projection key (always starts with "_perf"). mongoPath is the stored field
// ("" means the role is not wired yet: the key projects a literal null). For a
// list kind, subA and subB are the stored element fields: the name (or count)
// and the number (ms or value).
//
// Adding a real path later is a one-line edit of recordPerfFields.
type perfField struct {
	key       string
	mongoPath string
	kind      perfKind
	subA      string
	subB      string
}

// recordPerfFields is the one table behind the projection and the decoder.
// Only the two roles verified in the sandbox are wired; every other role is
// listed as absent until its stored name is confirmed.
var recordPerfFields = []perfField{
	{key: "_perfHitCount", mongoPath: "RequestHitCount", kind: perfNumber},
	{key: "_perfFilterTotalMs", mongoPath: "PreActionFilterTotalMilliseconds", kind: perfNumber},
	{key: "_perfSqlCount", mongoPath: "SqlCommandCount", kind: perfNumber},
	{key: "_perfSqlMs", mongoPath: "SqlMilliseconds", kind: perfNumber},
	{key: "_perfRedisCount", mongoPath: "RedisCallCount", kind: perfNumber},
	{key: "_perfRedisMs", mongoPath: "RedisMilliseconds", kind: perfNumber},
	{key: "_perfHttpCount", mongoPath: "HttpCallCount", kind: perfNumber},
	{key: "_perfHttpMs", mongoPath: "HttpMilliseconds", kind: perfNumber},
	{key: "_perfFilters", mongoPath: "PreActionFilterTimings", kind: perfNamed, subA: "FilterName", subB: "ElapsedMilliseconds"},
	{key: "_perfSlowSql", kind: perfSlowList},
	{key: "_perfSlowHttp", kind: perfSlowList},
	{key: "_perfSteps", kind: perfNamed},
	{key: "_perfPressure", kind: perfValueList},
	{key: "_perfTextStored", kind: perfFlag},
}

func perfLimit(key string) int {
	switch key {
	case "_perfFilters":
		return audit.PerfMaxFilters
	case "_perfSlowSql", "_perfSlowHttp":
		return audit.PerfMaxSlow
	case "_perfSteps":
		return audit.PerfMaxSteps
	case "_perfPressure":
		return audit.PerfMaxPressure
	}
	return 0
}

func perfTypeIs(expr string, types ...string) bson.D {
	typeOf := bson.D{{Key: "$type", Value: expr}}
	if len(types) == 1 {
		return bson.D{{Key: "$eq", Value: bson.A{typeOf, types[0]}}}
	}
	alts := make(bson.A, 0, len(types))
	for _, t := range types {
		alts = append(alts, bson.D{{Key: "$eq", Value: bson.A{typeOf, t}}})
	}
	return bson.D{{Key: "$or", Value: alts}}
}

// perfNumberExpr yields expr when it is a stored number, otherwise null.
func perfNumberExpr(expr string) bson.D {
	return bson.D{{Key: "$cond", Value: bson.A{perfTypeIs(expr, "double", "int", "long", "decimal"), expr, nil}}}
}

// perfNameExpr yields expr cut to the name cap when it is a string, otherwise null.
func perfNameExpr(expr string) bson.D {
	return bson.D{{Key: "$cond", Value: bson.A{
		perfTypeIs(expr, "string"),
		bson.D{{Key: "$substrCP", Value: bson.A{expr, 0, audit.PerfMaxName}}},
		nil,
	}}}
}

func perfFieldExpr(f perfField) any {
	if f.mongoPath == "" {
		return nil
	}
	path := "$" + f.mongoPath
	switch f.kind {
	case perfNumber:
		return perfNumberExpr(path)
	case perfFlag:
		return bson.D{{Key: "$cond", Value: bson.A{perfTypeIs(path, "bool"), path, nil}}}
	}
	element := func(sub string) string { return "$$e." + sub }
	var in bson.D
	switch f.kind {
	case perfNamed:
		in = bson.D{{Key: "name", Value: perfNameExpr(element(f.subA))}, {Key: "ms", Value: perfNumberExpr(element(f.subB))}}
	case perfSlowList:
		in = bson.D{{Key: "count", Value: perfNumberExpr(element(f.subA))}, {Key: "ms", Value: perfNumberExpr(element(f.subB))}}
	case perfValueList:
		in = bson.D{{Key: "name", Value: perfNameExpr(element(f.subA))}, {Key: "value", Value: perfNumberExpr(element(f.subB))}}
	default:
		return nil
	}
	return bson.D{{Key: "$cond", Value: bson.A{
		perfTypeIs(path, "array"),
		bson.D{{Key: "$map", Value: bson.D{
			{Key: "input", Value: bson.D{{Key: "$slice", Value: bson.A{path, perfLimit(f.key)}}}},
			{Key: "as", Value: "e"},
			{Key: "in", Value: in},
		}}},
		nil,
	}}}
}

// recordPerfProjection returns the flat _perf* keys appended to the single
// record projection. Numbers, flags and bounded lists are guarded with $cond and
// $type so a value of an unexpected type becomes null; a role without a wired
// path projects a literal null. Only MongoDB 3.4 operators are used, and no text
// or command field is ever projected: slow SQL and HTTP entries carry a count
// and a time only.
func recordPerfProjection() bson.D {
	projection := make(bson.D, 0, len(recordPerfFields))
	for _, f := range recordPerfFields {
		projection = append(projection, bson.E{Key: f.key, Value: perfFieldExpr(f)})
	}
	return projection
}

// lightProjection shares the detail projection's guards and field mappings,
// but reads only the three scalar totals shown on request rows.
func lightProjection() bson.D {
	projection := make(bson.D, 0, 3)
	for _, f := range recordPerfFields {
		switch f.key {
		case "_perfSqlMs", "_perfRedisMs", "_perfHttpMs":
			projection = append(projection, bson.E{Key: f.key, Value: perfFieldExpr(f)})
		}
	}
	return projection
}

// decodeRecordLight accepts raw BSON so an invalid total cannot fail a row's
// typed metadata decode. A recorded zero remains present.
func decodeRecordLight(sql, redis, http bson.RawValue) *audit.RecordLight {
	light := &audit.RecordLight{
		SQLMs: perfNumberValue(sql), RedisMs: perfNumberValue(redis), HTTPMs: perfNumberValue(http),
	}
	if light.SQLMs == nil && light.RedisMs == nil && light.HTTPMs == nil {
		return nil
	}
	return light
}

// perfNumberValue reads a stored number. NaN, infinities and negatives become nil.
func perfNumberValue(value bson.RawValue) *float64 {
	var number float64
	switch value.Type {
	case bson.TypeDouble:
		number = value.Double()
	case bson.TypeInt32:
		number = float64(value.Int32())
	case bson.TypeInt64:
		number = float64(value.Int64())
	case bson.TypeDecimal128:
		parsed, ok := parseDecimal(value)
		if !ok {
			return nil
		}
		number = parsed
	default:
		return nil
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return nil
	}
	return &number
}

func parseDecimal(value bson.RawValue) (float64, bool) {
	d, ok := value.Decimal128OK()
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(d.String(), 64)
	return parsed, err == nil
}

func perfNameValue(value bson.RawValue) string {
	text, ok := value.StringValueOK()
	if !ok {
		return ""
	}
	text = strings.ToValidUTF8(strings.TrimSpace(text), "�")
	if utf8.RuneCountInString(text) > audit.PerfMaxName {
		text = string([]rune(text)[:audit.PerfMaxName])
	}
	return text
}

func perfElements(value bson.RawValue, limit int, each func(bson.Raw) bool) {
	array, ok := value.ArrayOK()
	if !ok {
		return
	}
	items, err := array.Values()
	if err != nil {
		return
	}
	kept := 0
	for _, item := range items {
		if kept >= limit {
			break
		}
		doc, ok := item.DocumentOK()
		if !ok {
			continue
		}
		if each(doc) {
			kept++
		}
	}
}

// decodeRecordPerf reads the flat _perf* keys of a record projection. It
// returns nil when the document carries no usable perf value at all, which is
// the case for every call the server saved without these fields.
func decodeRecordPerf(raw bson.Raw) *audit.RecordPerf {
	perf := &audit.RecordPerf{}
	found := false
	for _, f := range recordPerfFields {
		value := raw.Lookup(f.key)
		if value.Type == 0 || value.Type == bson.TypeNull {
			continue
		}
		limit := perfLimit(f.key)
		switch f.key {
		case "_perfHitCount":
			perf.HitCount = perfNumberValue(value)
			found = found || perf.HitCount != nil
		case "_perfFilterTotalMs":
			perf.FilterTotalMs = perfNumberValue(value)
			found = found || perf.FilterTotalMs != nil
		case "_perfSqlCount":
			perf.SQLCount = perfNumberValue(value)
			found = found || perf.SQLCount != nil
		case "_perfSqlMs":
			perf.SQLMs = perfNumberValue(value)
			found = found || perf.SQLMs != nil
		case "_perfRedisCount":
			perf.RedisCount = perfNumberValue(value)
			found = found || perf.RedisCount != nil
		case "_perfRedisMs":
			perf.RedisMs = perfNumberValue(value)
			found = found || perf.RedisMs != nil
		case "_perfHttpCount":
			perf.HTTPCount = perfNumberValue(value)
			found = found || perf.HTTPCount != nil
		case "_perfHttpMs":
			perf.HTTPMs = perfNumberValue(value)
			found = found || perf.HTTPMs != nil
		case "_perfTextStored":
			if b, ok := value.BooleanOK(); ok {
				perf.TextStored = &b
				found = true
			}
		case "_perfFilters":
			perf.Filters = decodeNamedList(value, limit)
			found = found || len(perf.Filters) > 0
		case "_perfSteps":
			perf.Steps = decodeNamedList(value, limit)
			found = found || len(perf.Steps) > 0
		case "_perfSlowSql":
			perf.SlowSQL = decodeSlowList(value, limit)
			found = found || len(perf.SlowSQL) > 0
		case "_perfSlowHttp":
			perf.SlowHTTP = decodeSlowList(value, limit)
			found = found || len(perf.SlowHTTP) > 0
		case "_perfPressure":
			perf.Pressure = decodeValueList(value, limit)
			found = found || len(perf.Pressure) > 0
		}
	}
	if !found {
		return nil
	}
	return perf
}

func decodeNamedList(value bson.RawValue, limit int) []audit.PerfNamed {
	var out []audit.PerfNamed
	perfElements(value, limit, func(doc bson.Raw) bool {
		name := perfNameValue(doc.Lookup("name"))
		if name == "" {
			return false
		}
		out = append(out, audit.PerfNamed{Name: name, Ms: perfNumberValue(doc.Lookup("ms"))})
		return true
	})
	return out
}

func decodeSlowList(value bson.RawValue, limit int) []audit.PerfSlow {
	var out []audit.PerfSlow
	perfElements(value, limit, func(doc bson.Raw) bool {
		out = append(out, audit.PerfSlow{Count: perfNumberValue(doc.Lookup("count")), Ms: perfNumberValue(doc.Lookup("ms"))})
		return true
	})
	return out
}

func decodeValueList(value bson.RawValue, limit int) []audit.PerfValue {
	var out []audit.PerfValue
	perfElements(value, limit, func(doc bson.Raw) bool {
		name := perfNameValue(doc.Lookup("name"))
		if name == "" {
			return false
		}
		out = append(out, audit.PerfValue{Name: name, Value: perfNumberValue(doc.Lookup("value"))})
		return true
	})
	return out
}
