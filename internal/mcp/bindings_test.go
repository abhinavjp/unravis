package mcp

import (
	"api-audit-explorer/internal/audit"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// BL-364: the narrowed output schemas the type generator reads (bindings.go) against the catalog and against what the ten tools really
// answer on the real handler path (demo repository behind the real dashboard handler).

func TestBindingToolsAreTheCatalog(t *testing.T) {
	got := BindingTools()
	cat := tools()
	if len(got) != len(cat) || len(got) != 10 {
		t.Fatalf("%d binding tools for %d catalog tools", len(got), len(cat))
	}
	for i, tool := range got {
		if tool.Name != cat[i]["name"] || tool.Description != cat[i]["description"] {
			t.Errorf("tool %d: %s differs from the catalog", i, tool.Name)
		}
		in, _ := json.Marshal(tool.Input)
		want, _ := json.Marshal(cat[i]["inputSchema"])
		if string(in) != string(want) {
			t.Errorf("%s: input schema differs from tools/list", tool.Name)
		}
		if tool.Output == nil || tool.Output["properties"] == nil {
			t.Errorf("%s: no output schema", tool.Name)
		}
		if !tool.ReadOnly {
			t.Errorf("%s: not read-only", tool.Name)
		}
	}
	// A caller cannot change the catalog through what it got.
	got[0].Input["type"] = "changed"
	got[0].Output["type"] = "changed"
	if again := BindingTools(); again[0].Input["type"] == "changed" || again[0].Output["type"] == "changed" {
		t.Error("BindingTools hands out the catalog's own maps")
	}
}

// typeSet is the JSON types a schema accepts, "ref" for a reference.
func typeSet(s any) map[string]bool {
	out := map[string]bool{}
	m, ok := s.(map[string]any)
	if !ok {
		return out
	}
	switch t := m["type"].(type) {
	case string:
		out[t] = true
	case []any:
		for _, e := range t {
			out[e.(string)] = true
		}
	}
	if _, ok := m["$ref"]; ok {
		out["object"] = true
	}
	if one, ok := m["oneOf"].([]any); ok {
		for _, b := range one {
			for k := range typeSet(b) {
				out[k] = true
			}
		}
	}
	return out
}

func TestNarrowingOnlyNarrows(t *testing.T) {
	for _, tool := range BindingTools() {
		adv := outputSchema(tool.Name)
		advProps := adv["properties"].(map[string]any)
		props := tool.Output["properties"].(map[string]any)
		for name, a := range advProps {
			n, ok := props[name]
			if !ok {
				t.Errorf("%s: field %s of the advertised schema is gone", tool.Name, name)
				continue
			}
			na, nn := typeSet(a), typeSet(n)
			for typ := range nn {
				if !na[typ] && !(typ == "integer" && na["number"]) {
					t.Errorf("%s.%s: narrowed type %s is not advertised (%v)", tool.Name, name, typ, na)
				}
			}
			if na["null"] != nn["null"] && len(na) > 0 {
				t.Errorf("%s.%s: null-ness changed (advertised %v, narrowed %v)", tool.Name, name, na, nn)
			}
		}
		for name := range props {
			if _, ok := advProps[name]; !ok {
				t.Errorf("%s: narrowed schema adds field %s", tool.Name, name)
			}
		}
		for _, key := range []string{"required", "if", "else", "type", "additionalProperties"} {
			a, _ := json.Marshal(adv[key])
			n, _ := json.Marshal(tool.Output[key])
			if string(a) != string(n) {
				t.Errorf("%s: %s differs: %s vs %s", tool.Name, key, a, n)
			}
		}
	}
}

// strict closes every object: a field the schema does not name is then an error, so a field added to a response without a schema change
// is found here.
func strict(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if k == "properties" || k == "$defs" {
				m := map[string]any{}
				for pk, pv := range e.(map[string]any) {
					m[pk] = strict(pv)
				}
				out[k] = m
				continue
			}
			out[k] = strict(e)
		}
		if _, ok := out["properties"]; ok {
			out["additionalProperties"] = false
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = strict(e)
		}
		return out
	}
	return v
}

func TestRealAnswersValidateAgainstNarrowedSchemas(t *testing.T) {
	byName := map[string]BindingTool{}
	for _, tool := range BindingTools() {
		byName[tool.Name] = tool
	}
	covered := map[string][]string{
		"audit_list_sources": {"sources"}, "audit_compare": {"groups", "sources"}, "audit_get_groups": {"groups"}, "audit_get_timeline": {"points"},
		"audit_compare_timelines": {"slots", "groups"}, "audit_get_requests": {"rows"}, "audit_get_slowest": {"rows"}, "audit_get_breakdown": {"rows"},
	}
	for tool, res := range tenResults(t, false) {
		body := []byte(textOf(res))
		sc := compileSchema(t, strict(byName[tool].Output))
		if err := validate(t, sc, body); err != nil {
			t.Errorf("%s: the real answer does not match its narrowed schema: %v\n%.800s", tool, err, body)
		}
		// Not vacuous: the lists the schema types below their first level had rows in this answer.
		var answer map[string]any
		_ = json.Unmarshal(body, &answer)
		for _, field := range covered[tool] {
			if list, _ := answer[field].([]any); len(list) == 0 {
				t.Errorf("%s: %s is empty in the demo answer, so its row type was not exercised", tool, field)
			}
		}
		// A field nobody declared, and a field of the wrong type, are both refused.
		for _, mutate := range []func(map[string]any) bool{
			func(m map[string]any) bool { m["undeclared"] = 1; return true },
			func(m map[string]any) bool {
				for _, f := range covered[tool] {
					if rows, _ := m[f].([]any); len(rows) > 0 {
						if row, ok := rows[0].(map[string]any); ok {
							row["undeclaredRowField"] = true
							return true
						}
					}
				}
				return false
			},
		} {
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			if !mutate(m) {
				continue
			}
			broken, _ := json.Marshal(m)
			if validate(t, sc, broken) == nil {
				t.Errorf("%s: an undeclared field still validates", tool)
			}
		}
	}
}

// fill gives every field of a value a non-zero content, so omitempty drops nothing.
func fill(v reflect.Value, depth int) {
	if depth > 6 {
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.String:
		v.SetString("x")
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), depth+1)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fill(v.Index(0), depth+1)
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		e := reflect.New(v.Type().Elem()).Elem()
		fill(e, depth+1)
		v.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), e)
	case reflect.Struct:
		if v.Type() == timeType {
			v.Set(reflect.ValueOf(time.Date(2026, 11, 23, 1, 2, 3, 0, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), depth+1)
			}
		}
	}
}

// The reflection builder must agree with encoding/json for filled and for zero values of every Go type the handlers return.
func TestSchemaBuilderMatchesEncodingJSON(t *testing.T) {
	types := []any{audit.Comparison{}, audit.Query{}, audit.Point{}, audit.Quality{}, audit.Provenance{}, audit.Source{}, audit.SourceResult{}, audit.BreakdownRow{}, audit.RequestRecord{}}
	for _, sample := range types {
		typ := reflect.TypeOf(sample)
		b := &schemaBuilder{defs: map[string]any{}}
		root := b.of(typ)
		if b.err != nil {
			t.Fatalf("%s: %v", typ, b.err)
		}
		schema := map[string]any{"$ref": root.(map[string]any)["$ref"], "$defs": b.defs}
		sc := compileSchema(t, strict(schema))
		for label, value := range map[string]reflect.Value{"zero": reflect.New(typ).Elem(), "filled": func() reflect.Value { v := reflect.New(typ).Elem(); fill(v, 0); return v }()} {
			raw, err := json.Marshal(value.Interface())
			if err != nil {
				t.Fatal(err)
			}
			if err := validate(t, sc, raw); err != nil {
				// RequestRecord is only projected, never written whole.
				if typ.Name() == "RequestRecord" {
					continue
				}
				t.Errorf("%s (%s) does not match its schema: %v\n%.500s", typ, label, err, raw)
			}
		}
	}
}

func TestProjectedRowsMatchProjectRecord(t *testing.T) {
	var rec audit.RequestRecord
	fill(reflect.ValueOf(&rec).Elem(), 0)
	row := projectRecord(rec)
	got := make([]string, 0, len(row))
	for k := range row {
		got = append(got, k)
	}
	sort.Strings(got)
	want := append([]string(nil), recordFields...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("projectRecord writes %v, the schema lists %v", got, want)
	}
}
