package mcp

import (
	"api-audit-explorer/internal/audit"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

// BL-364: what the code-mode type generator reads. One function hands out the real tool catalog (tools()) together with output schemas
// that are concrete down to the rows (BindingTools); the declarations are generated from it by internal/codemode/tsgen.
//
// The output schemas advertised in tools/list (outputschema.go) stay as they are: they name the type of each top-level field ("array",
// "object") and nothing below it, which is the BL-343 contract. A script needs the row types, so this file narrows them. A narrowed schema is
// built from the Go types the handlers really return (audit.Comparison, audit.Query, ... by reflection, so a new field shows up here with
// no hand edit) and, for the few rows the handlers build as maps (projectRecord, activity rows, timeline slots), from a short hand-written
// list. It can only narrow: bindings_test.go checks that every narrowed field keeps the advertised type, and that the ten tools' real
// answers validate against it with no field left undeclared.

// BindingTool is one tool as the type generator reads it.
type BindingTool struct {
	Name        string
	Description string
	// Input is the tool's input schema, as tools/list advertises it.
	Input map[string]any
	// Output is the narrowed output schema (JSON Schema 2020-12, with $defs).
	Output map[string]any
	// Untrusted lists the paths of the result whose strings come from audit data.
	Untrusted []string
	// ReadOnly and Idempotent are the tool's annotations.
	ReadOnly, Idempotent bool
}

// BindingTools returns the ten tools in catalog order. Every call returns fresh copies.
func BindingTools() []BindingTool {
	var out []BindingTool
	for _, def := range tools() {
		name, _ := def["name"].(string)
		description, _ := def["description"].(string)
		in := copyMap(def["inputSchema"])
		ann, _ := def["annotations"].(map[string]any)
		ro, _ := ann["readOnlyHint"].(bool)
		idem, _ := ann["idempotentHint"].(bool)
		out = append(out, BindingTool{
			Name: name, Description: description, Input: in, Output: narrowedOutput(name),
			Untrusted: append([]string(nil), toolShapes[name].untrusted...), ReadOnly: ro, Idempotent: idem,
		})
	}
	return out
}

// copyMap deep-copies a decoded schema through JSON, so a caller can never change the catalog.
func copyMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("mcp: schema is not JSON: " + err.Error())
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic("mcp: schema is not an object: " + err.Error())
	}
	return m
}

// narrowedOutput is outputSchema(tool) with the top-level fields typed below their first level.
func narrowedOutput(tool string) map[string]any {
	schema := copyMap(outputSchema(tool))
	props := schema["properties"].(map[string]any)
	b := &schemaBuilder{defs: map[string]any{}}
	for name, inner := range narrowing(tool, b) {
		advertised, _ := props[name].(map[string]any)
		if advertisedAllowsNull(advertised) {
			inner = nullable(inner)
		}
		props[name] = inner
	}
	if b.err != nil {
		panic("mcp: output schema of " + tool + ": " + b.err.Error())
	}
	if len(b.defs) > 0 {
		schema["$defs"] = b.defs
	}
	return schema
}

func advertisedAllowsNull(adv map[string]any) bool {
	switch t := adv["type"].(type) {
	case []any:
		for _, e := range t {
			if e == "null" {
				return true
			}
		}
	case string:
		return t == "null"
	}
	return false
}

// narrowing lists, per tool, the schema of each top-level field that is typed below its first level.
func narrowing(tool string, b *schemaBuilder) map[string]any {
	of := func(v any) any { return b.of(reflect.TypeOf(v)) }
	array := func(items any) any { return map[string]any{"type": "array", "items": items} }
	comparison, query := of(audit.Comparison{}), of(audit.Query{})
	echoed := b.echoedQuery()
	sources, provenance := array(of(audit.SourceResult{})), array(of(audit.Provenance{}))
	switch tool {
	case "audit_list_sources":
		return map[string]any{"sources": array(of(audit.Source{}))}
	case "audit_compare":
		return map[string]any{"groups": array(comparison), "query": query, "sources": sources, "quality": of(audit.Quality{}), "provenance": provenance,
			"recordedEvents": strictObject(map[string]any{
				"current": integerSchema(), "reference": integerSchema(), "currentPerSecond": numberSchema(), "referencePerSecond": numberSchema(),
			}, "current", "reference", "currentPerSecond", "referencePerSecond")}
	case "audit_get_groups":
		return map[string]any{"groups": array(comparison)}
	case "audit_get_timeline":
		return map[string]any{"query": query, "group": comparison, "points": array(of(audit.Point{}))}
	case "audit_compare_timelines":
		return map[string]any{"query": query, "groups": array(comparison), "slots": array(strictObject(map[string]any{
			"at": stampSchema(), "start": stampSchema(), "end": stampSchema(),
			"groups": array(strictObject(map[string]any{
				"apiKey": stringSchema(), "count": integerSchema(),
				"sampleStatus": map[string]any{"type": "string", "enum": []any{"missing", "few_samples", "enough_samples"}},
				"currentP95Ms": nullable(numberSchema()), "baselineP95Ms": nullable(numberSchema()), "increaseMs": nullable(numberSchema()), "increasePercent": nullable(numberSchema()),
			}, "apiKey", "count", "sampleStatus", "currentP95Ms", "baselineP95Ms", "increaseMs", "increasePercent")),
		}, "at", "start", "end", "groups"))}
	case "audit_get_requests":
		return map[string]any{"query": echoed, "rows": array(b.projected(recordFields)), "sources": sources, "provenance": provenance}
	case "audit_get_slowest":
		return map[string]any{"query": echoed, "rows": array(b.projected(append(append([]string(nil), recordFields...), "sourceId", "controller", "action", "kind"))), "sources": sources, "provenance": provenance}
	case "audit_get_breakdown":
		row := of(audit.BreakdownRow{})
		return map[string]any{"rows": array(row), "other": row, "query": echoed, "sources": sources, "provenance": provenance}
	case "audit_get_activity":
		return map[string]any{"sourceIds": array(stringSchema()), "sources": sources,
			"rows": array(b.projected([]string{"sourceId", "controller", "action", "kind", "outcome", "initiatedAt", "completedAt", "durationMs", "apiTimeMs", "auditCreateTimeMs", "payloadRecordTimeMs"}))}
	}
	return nil
}

// echoedQuery is the query as echoQuery reports it (breakdown.go): audit.Query without the IP address filter. audit_get_requests,
// audit_get_slowest and audit_get_breakdown answer with it; the other tools echo the cached query whole.
func (b *schemaBuilder) echoedQuery() any {
	b.of(reflect.TypeOf(audit.Query{})) // makes sure the full definition exists
	def := copyMap(b.defs["Query"])
	delete(def["properties"].(map[string]any), "userIps")
	if req, ok := def["required"].([]any); ok {
		kept := req[:0]
		for _, r := range req {
			if r != "userIps" {
				kept = append(kept, r)
			}
		}
		def["required"] = kept
	}
	b.defs["EchoedQuery"] = def
	return map[string]any{"$ref": "#/$defs/EchoedQuery"}
}

// recordFields are the fields projectRecord copies from audit.RequestRecord (breakdown.go): an allow-list, never the URL, IP, agent or headers.
var recordFields = []string{"id", "state", "initiatedAt", "completedAt", "durationMs", "apiTimeMs", "auditCreateTimeMs", "payloadRecordTimeMs", "requestKB", "responseKB", "outcome", "status"}

// ---- schema construction ----

func strictObject(props map[string]any, required ...string) map[string]any {
	out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		out["required"] = req
	}
	return out
}

func stringSchema() map[string]any  { return map[string]any{"type": "string"} }
func integerSchema() map[string]any { return map[string]any{"type": "integer"} }
func numberSchema() map[string]any  { return map[string]any{"type": "number"} }
func stampSchema() map[string]any {
	return map[string]any{"type": "string", "format": "date-time"}
}

// nullable lets a schema also match null.
func nullable(s any) any {
	if m, ok := s.(map[string]any); ok {
		if t, isString := m["type"].(string); isString {
			out := map[string]any{}
			for k, v := range m {
				out[k] = v
			}
			out["type"] = []any{t, "null"}
			return out
		}
	}
	return map[string]any{"oneOf": []any{s, map[string]any{"type": "null"}}}
}

// schemaBuilder writes JSON Schema from Go types. Named struct types become $defs (once each) and are referenced.
type schemaBuilder struct {
	defs  map[string]any
	owner map[string]reflect.Type
	err   error
}

var timeType = reflect.TypeOf(time.Time{})

func (b *schemaBuilder) fail(format string, args ...any) {
	if b.err == nil {
		b.err = fmt.Errorf(format, args...)
	}
}

// of returns the schema of t as encoding/json writes it.
func (b *schemaBuilder) of(t reflect.Type) any {
	switch {
	case t == timeType:
		return stampSchema()
	case t.Kind() == reflect.Pointer:
		return nullable(b.of(t.Elem()))
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return integerSchema()
	case reflect.Float32, reflect.Float64:
		return numberSchema()
	case reflect.String:
		return stringSchema()
	case reflect.Slice:
		return map[string]any{"type": "array", "items": b.of(t.Elem())}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			b.fail("map key of %s is not a string", t)
			return map[string]any{}
		}
		return map[string]any{"type": "object", "additionalProperties": b.of(t.Elem())}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		return b.named(t)
	}
	b.fail("type %s has no schema", t)
	return map[string]any{}
}

// named writes a struct once under its Go name and returns a reference.
func (b *schemaBuilder) named(t reflect.Type) any {
	name := t.Name()
	ref := map[string]any{"$ref": "#/$defs/" + name}
	if b.owner == nil {
		b.owner = map[string]reflect.Type{}
	}
	if prev, ok := b.owner[name]; ok {
		if prev != t {
			b.fail("two types are called %s", name)
		}
		return ref
	}
	b.owner[name] = t
	b.defs[name] = map[string]any{} // reserved first: a recursive type finds its own name
	props, required := map[string]any{}, []string{}
	for _, f := range jsonFields(t) {
		inner := b.of(f.typ)
		if f.omitEmpty {
			// Absent when empty. A pointer is then absent, not null.
			if f.typ.Kind() == reflect.Pointer {
				inner = b.of(f.typ.Elem())
			}
		} else {
			required = append(required, f.name)
			// A nil slice or map is written as null.
			if f.typ.Kind() == reflect.Slice || f.typ.Kind() == reflect.Map {
				inner = nullable(inner)
			}
		}
		props[f.name] = inner
	}
	sort.Strings(required)
	b.defs[name] = strictObject(props, required...)
	return ref
}

type jsonField struct {
	name      string
	typ       reflect.Type
	omitEmpty bool
}

func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out = append(out, jsonField{name: name, typ: f.Type, omitEmpty: strings.Contains(","+opts+",", ",omitempty,")})
	}
	return out
}

// projected builds the schema of a map row that copies the named fields of audit.RequestRecord under their JSON names: every name is
// always present, with the record's type (a pointer stays nullable).
func (b *schemaBuilder) projected(names []string) any {
	byName := map[string]jsonField{}
	for _, f := range jsonFields(reflect.TypeOf(audit.RequestRecord{})) {
		byName[f.name] = f
	}
	props := map[string]any{}
	for _, n := range names {
		f, ok := byName[n]
		if !ok {
			b.fail("audit.RequestRecord has no field %q", n)
			continue
		}
		props[n] = b.of(f.typ)
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	if slices.Contains(names, "state") {
		// BL-349: the rows of audit_get_requests and audit_get_slowest may carry an alias of the record. A host's bridge adds it to every
		// row that has a record id; the stdio server does not, so it is not required.
		props["evidenceRef"] = map[string]any{"type": "string", "pattern": "^ev1_[a-z2-7]{26}$"}
	}
	return strictObject(props, sorted...)
}
