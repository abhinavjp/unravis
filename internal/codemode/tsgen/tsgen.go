// Package tsgen turns the JSON Schemas of the MCP tool catalog into one TypeScript declaration file (unravis.d.ts) for code mode (BL-364).
//
// It is a pure function of its input: no file, network, clock or MCP code. The caller (internal/codemode/bindings) feeds it the real tool
// catalog, so the declarations and tools/list come from one source and a drift test can compare them.
//
// What the declarations are for: a model writes a script against `unravis.audit_x(...)`, the broker strips the types and runs the JS.
// The file is also shown to the model (unravis_types, `unravis code types`). So everything that reaches it is treated as hostile text:
// descriptions, property names, enum values and tool names are escaped or sanitized so a schema can never close a comment, inject a
// declaration or break a line (see safeComment, quote, ident). Nothing here can widen what a script may do; it only describes it.
//
// Supported schema subset (everything else becomes `unknown`, never an error, so a new keyword cannot stop the build of the types):
// type (single or list), enum, const, properties, required, additionalProperties, items, oneOf and anyOf of simple branches, a single
// allOf, local $ref to $defs or definitions, and the one if/else form the MCP output schemas use (see the envelope below). Not supported:
// patternProperties (an index signature of unknown is added), prefixItems, not, dependent*, unevaluated*, remote or dynamic refs.
// Inline nesting is capped at MaxDepth; a $ref is emitted as a name (the definition is written once), so a recursive schema ends.
package tsgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"api-audit-explorer/internal/jcs"
)

// MaxDepth is how deep inline types nest before the walker writes `unknown`.
const MaxDepth = 12

// maxDefs bounds the named definitions one generation may write.
const maxDefs = 512

// maxTools bounds the tools of one file.
const maxTools = 256

// Tool is one callable of the namespace.
type Tool struct {
	// Name is the tool name as the catalog has it. It is sanitized into a function name.
	Name string
	// Description is written above the function.
	Description string
	// Notes are extra comment lines above the function (limits, warnings).
	Notes []string
	// Input and Output are decoded JSON Schemas (map[string]any, as encoding/json produces). Output may be nil (the result is unknown).
	Input, Output map[string]any
}

// Spec is everything one file is made of.
type Spec struct {
	// Namespace is the global the functions live in ("unravis").
	Namespace string
	// Header lines are written as // comments under the hash line.
	Header []string
	Tools  []Tool
}

// Result is the generated file and the hash it carries.
type Result struct {
	DTS string
	// SchemaSHA256 is the SHA-256 (hex) of the canonical (RFC 8785) form of the Spec's namespace, header and tools. It is the first line of
	// the file, so a reader can tell two files from one catalog apart without comparing text.
	SchemaSHA256 string
}

// Generate writes the declaration file for spec. The same spec always gives the same bytes.
func Generate(spec Spec) (Result, error) {
	if !isIdent(spec.Namespace) {
		return Result{}, errors.New("tsgen: namespace is not an identifier")
	}
	if len(spec.Tools) == 0 || len(spec.Tools) > maxTools {
		return Result{}, errors.New("tsgen: between 1 and 256 tools are needed")
	}
	sum, err := hashSpec(spec)
	if err != nil {
		return Result{}, err
	}
	g := &gen{defs: map[string]*def{}, order: nil, taken: map[string]string{}, defJSON: map[string]string{}}
	var funcs strings.Builder
	seenFunc := map[string]string{}
	for _, tool := range spec.Tools {
		fn := funcName(tool.Name)
		if prev, dup := seenFunc[fn]; dup {
			return Result{}, fmt.Errorf("tsgen: tools %q and %q both become %q", prev, tool.Name, fn)
		}
		seenFunc[fn] = tool.Name
		base := pascal(tool.Name)
		in, out := base+"Input", base+"Output"
		if err := g.reserve(in, "tool "+tool.Name); err != nil {
			return Result{}, err
		}
		if err := g.reserve(out, "tool "+tool.Name); err != nil {
			return Result{}, err
		}
		inputOptional, err := g.topType(in, tool.Input, true)
		if err != nil {
			return Result{}, fmt.Errorf("tsgen: %s input: %w", tool.Name, err)
		}
		hasOut := tool.Output != nil
		if hasOut {
			if _, err := g.topType(out, tool.Output, false); err != nil {
				return Result{}, fmt.Errorf("tsgen: %s output: %w", tool.Name, err)
			}
		} else if err := g.addType(out, "unknown", ""); err != nil {
			return Result{}, err
		}
		var doc []string
		doc = append(doc, wrap(tool.Description, 100)...)
		for _, n := range tool.Notes {
			doc = append(doc, wrap(n, 100)...)
		}
		if fn != tool.Name {
			doc = append(doc, "Tool name: "+quote(tool.Name))
		}
		funcs.WriteString(jsdoc("  ", doc))
		opt := ""
		if inputOptional {
			opt = "?"
		}
		fmt.Fprintf(&funcs, "  function %s(input%s: %s): Promise<%s>;\n", fn, opt, in, out)
	}
	if err := g.drain(); err != nil {
		return Result{}, err
	}
	var b strings.Builder
	b.WriteString("// schema_sha256: " + sum + "\n")
	for _, line := range spec.Header {
		for _, l := range wrap(line, 110) {
			b.WriteString("// " + safeLine(l) + "\n")
		}
	}
	b.WriteString("\ndeclare namespace " + spec.Namespace + " {\n")
	for _, d := range g.sorted() {
		b.WriteString(jsdoc("  ", wrap(d.doc, 100)))
		b.WriteString("  type " + d.name + " = " + indent(d.body, "  ") + ";\n")
	}
	b.WriteString("\n")
	b.WriteString(funcs.String())
	b.WriteString("}\n")
	return Result{DTS: b.String(), SchemaSHA256: sum}, nil
}

func hashSpec(spec Spec) (string, error) {
	raw, err := json.Marshal(spec2json(spec))
	if err != nil {
		return "", fmt.Errorf("tsgen: schema is not JSON: %w", err)
	}
	canon, err := jcs.Canonicalize(raw)
	if err != nil {
		return "", fmt.Errorf("tsgen: schema cannot be canonicalized: %w", err)
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

func spec2json(spec Spec) map[string]any {
	tools := make([]any, len(spec.Tools))
	for i, t := range spec.Tools {
		tools[i] = map[string]any{"name": t.Name, "description": t.Description, "notes": nonNil(t.Notes), "input": t.Input, "output": t.Output}
	}
	return map[string]any{"namespace": spec.Namespace, "header": nonNil(spec.Header), "tools": tools}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// def is one named type of the namespace.
type def struct {
	name, body, doc string
}

type gen struct {
	defs  map[string]*def   // by TS name
	order []string          // names in the order they were added (sorted again on output)
	taken map[string]string // TS name -> who owns it
	// defJSON is the schema each named definition was written from, to catch two different schemas under one name.
	defJSON map[string]string
	// schemas the $ref of the current top-level schema can reach, and the work still to write.
	roots   []map[string]any
	pending []pendingDef
}

type pendingDef struct {
	name   string
	schema any
	root   map[string]any
}

// builtinTypes are the global types the generated text itself uses or a script commonly names. A schema definition with one of these
// names would shadow it inside the namespace and change what the rest of the file means, so the name is refused.
var builtinTypes = map[string]bool{
	"Promise": true, "Record": true, "Required": true, "Pick": true, "Array": true, "Partial": true, "Readonly": true, "Omit": true,
	"Exclude": true, "Extract": true, "NonNullable": true, "String": true, "Number": true, "Boolean": true, "Object": true, "Date": true,
	"Error": true, "Map": true, "Set": true, "Function": true, "Symbol": true, "BigInt": true, "RegExp": true, "JSON": true, "Math": true,
}

func (g *gen) reserve(name, owner string) error {
	if builtinTypes[name] {
		return fmt.Errorf("tsgen: %s needs the type name %s, which is a built-in type", owner, name)
	}
	if prev, ok := g.taken[name]; ok {
		return fmt.Errorf("tsgen: %s and %s both need the type name %s", prev, owner, name)
	}
	g.taken[name] = owner
	return nil
}

// addType writes one named type. A second type of the same name is an error, never a silent first-wins.
func (g *gen) addType(name, body, doc string) error {
	if _, ok := g.defs[name]; ok {
		return fmt.Errorf("tsgen: the type name %s is written twice", name)
	}
	g.defs[name] = &def{name: name, body: body, doc: doc}
	g.order = append(g.order, name)
	return nil
}

func (g *gen) sorted() []*def {
	names := append([]string(nil), g.order...)
	sort.Strings(names)
	out := make([]*def, len(names))
	for i, n := range names {
		out[i] = g.defs[n]
	}
	return out
}

// topType writes the named type of one tool's input or output schema. It reports whether every property is optional (input only).
func (g *gen) topType(name string, schema map[string]any, input bool) (allOptional bool, err error) {
	w := &walker{g: g, root: schema, name: name}
	body, doc := w.top(schema)
	if err := g.addType(name, body, doc); err != nil {
		return false, err
	}
	if input {
		allOptional = w.noRequired(schema)
	}
	return allOptional, w.err
}

// drain writes the definitions that $refs asked for.
func (g *gen) drain() error {
	for len(g.pending) > 0 {
		p := g.pending[0]
		g.pending = g.pending[1:]
		if _, done := g.defs[p.name]; done {
			continue
		}
		if len(g.defs) >= maxDefs {
			return errors.New("tsgen: too many definitions")
		}
		w := &walker{g: g, root: p.root, name: p.name}
		body, doc := w.top(p.schema)
		if w.err != nil {
			return w.err
		}
		if err := g.addType(p.name, body, doc); err != nil {
			return err
		}
	}
	return nil
}

type walker struct {
	g    *gen
	root map[string]any
	// name is the TS name of the type being written; a variant form (if/else) is only read at its top, where it can be named.
	name string
	err  error
}

// noRequired reports whether a schema has no required property (so a call may omit the argument).
func (w *walker) noRequired(schema map[string]any) bool {
	req, _ := schema["required"].([]any)
	return len(req) == 0
}

// top renders the body of a named type: depth restarts at 0, a variant union (if/else) is written as named parts.
func (w *walker) top(schema any) (string, string) {
	m, ok := schema.(map[string]any)
	if !ok {
		return w.render(schema, 0), ""
	}
	return w.render(m, 0), describe(m)
}

// render returns the TS type text of a schema at the given inline depth.
func (w *walker) render(schema any, depth int) string {
	if depth > MaxDepth {
		return "unknown"
	}
	switch s := schema.(type) {
	case bool:
		if s {
			return "unknown"
		}
		return "never"
	case map[string]any:
		return w.renderObjectSchema(s, depth)
	}
	return "unknown"
}

func (w *walker) renderObjectSchema(s map[string]any, depth int) string {
	if ref, ok := s["$ref"].(string); ok {
		return w.ref(ref, depth)
	}
	if c, ok := s["const"]; ok {
		return literal(c)
	}
	if e, ok := s["enum"].([]any); ok {
		return w.enum(e)
	}
	if one := firstList(s, "oneOf", "anyOf"); one != nil {
		return w.union(one, depth)
	}
	if all, ok := s["allOf"].([]any); ok {
		if len(all) == 1 {
			return w.render(all[0], depth+1)
		}
		return "unknown"
	}
	switch t := s["type"].(type) {
	case string:
		return w.typed(t, s, depth)
	case []any:
		var parts []string
		for _, e := range t {
			name, ok := e.(string)
			if !ok {
				return "unknown"
			}
			parts = append(parts, w.typed(name, s, depth))
		}
		return joinUnion(parts)
	}
	if _, ok := s["properties"]; ok {
		return w.typed("object", s, depth)
	}
	if _, ok := s["items"]; ok {
		return w.typed("array", s, depth)
	}
	return "unknown"
}

func firstList(s map[string]any, keys ...string) []any {
	for _, k := range keys {
		if l, ok := s[k].([]any); ok {
			return l
		}
	}
	return nil
}

func (w *walker) typed(t string, s map[string]any, depth int) string {
	switch t {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		if _, tuple := s["prefixItems"]; tuple {
			return "unknown[]"
		}
		item, ok := s["items"]
		if !ok {
			return "unknown[]"
		}
		inner := w.render(item, depth+1)
		if strings.ContainsAny(inner, "|&") && !strings.HasPrefix(inner, "{") {
			inner = "(" + inner + ")"
		}
		return inner + "[]"
	case "object":
		return w.object(s, depth)
	}
	return "unknown"
}

// complex reports a branch that would make a union hard to read or wrong to guess.
func complex(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for _, k := range []string{"oneOf", "anyOf", "allOf", "not", "if"} {
		if _, has := m[k]; has {
			return true
		}
	}
	return false
}

func (w *walker) union(branches []any, depth int) string {
	if len(branches) == 0 || len(branches) > 16 {
		return "unknown"
	}
	parts := make([]string, 0, len(branches))
	for _, b := range branches {
		if complex(b) {
			return "unknown"
		}
		parts = append(parts, w.render(b, depth+1))
	}
	return joinUnion(parts)
}

func joinUnion(parts []string) string {
	seen := map[string]bool{}
	var uniq []string
	for _, p := range parts {
		if p == "unknown" {
			return "unknown"
		}
		if !seen[p] {
			seen[p] = true
			uniq = append(uniq, p)
		}
	}
	if len(uniq) == 0 {
		return "never"
	}
	return strings.Join(uniq, " | ")
}

func (w *walker) enum(values []any) string {
	if len(values) == 0 || len(values) > 256 {
		return "unknown"
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		l := literal(v)
		if l == "unknown" {
			return "unknown"
		}
		parts = append(parts, l)
	}
	return joinUnion(parts)
}

// literal writes a JSON scalar as a TS literal type. Strings are escaped with encoding/json and the two characters TS would read as a
// line end.
func literal(v any) string {
	switch x := v.(type) {
	case string:
		return quote(x)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	case float64:
		if x == float64(int64(x)) && x > -1e15 && x < 1e15 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case json.Number:
		return x.String()
	}
	return "unknown"
}

func (w *walker) ref(ref string, depth int) string {
	name, schema, ok := w.resolve(ref)
	if !ok {
		return "unknown"
	}
	// An alias that only points at another definition is followed (with a visited set), so `type A = A` can never be written.
	seen := map[string]bool{name: true}
	for hops := 0; hops < MaxDepth; hops++ {
		m, isMap := schema.(map[string]any)
		next, isRef := "", false
		if isMap {
			next, isRef = m["$ref"].(string)
		}
		if !isRef {
			break
		}
		n2, s2, ok2 := w.resolve(next)
		if !ok2 || seen[n2] {
			return "unknown"
		}
		seen[n2] = true
		name, schema = n2, s2
	}
	if m, isMap := schema.(map[string]any); isMap {
		if _, still := m["$ref"]; still {
			return "unknown"
		}
	}
	tsName := w.g.refName(name)
	if builtinTypes[tsName] {
		w.err = fmt.Errorf("tsgen: definition %q needs the type name %s, which is a built-in type", name, tsName)
		return "unknown"
	}
	raw, _ := json.Marshal(schema)
	if owner, taken := w.g.taken[tsName]; !taken {
		w.g.taken[tsName] = "definition " + name
		w.g.defJSON[tsName] = string(raw)
		w.g.pending = append(w.g.pending, pendingDef{name: tsName, schema: schema, root: w.root})
	} else if owner != "definition "+name || w.g.defJSON[tsName] != string(raw) {
		// The same name from two tools must mean the same schema; otherwise one of the two types would be wrong.
		w.err = fmt.Errorf("tsgen: definition %q collides with %s", name, owner)
		return "unknown"
	}
	return tsName
}

func (g *gen) refName(def string) string {
	n := pascal(def)
	if n == "" {
		n = "Def"
	}
	return n
}

// resolve follows a local reference (#/$defs/x or #/definitions/x) in the schema being walked.
func (w *walker) resolve(ref string) (string, any, bool) {
	for _, prefix := range []string{"#/$defs/", "#/definitions/"} {
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		key := strings.TrimPrefix(ref, prefix)
		if key == "" || strings.Contains(key, "/") {
			return "", nil, false
		}
		container, _ := w.root[strings.TrimSuffix(strings.TrimPrefix(prefix, "#/"), "/")].(map[string]any)
		if schema, ok := container[key]; ok {
			return key, schema, true
		}
	}
	return "", nil, false
}

// object renders an object type, with its fields sorted by name.
func (w *walker) object(s map[string]any, depth int) string {
	props, _ := s["properties"].(map[string]any)
	required := map[string]bool{}
	if req, ok := s["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}
	extra := w.extra(s, depth)
	if variants, ok := w.variants(s); ok && depth == 0 && w.name != "" {
		return w.variantObject(s, props, required, extra, variants, depth)
	}
	return w.fields(props, required, extra, depth, nil, nil)
}

// extra is the type of the index signature of an object ("" for none).
func (w *walker) extra(s map[string]any, depth int) string {
	if _, ok := s["patternProperties"]; ok {
		return "unknown"
	}
	switch ap := s["additionalProperties"].(type) {
	case bool:
		if ap {
			return "unknown"
		}
	case map[string]any:
		return w.render(ap, depth+1)
	}
	return ""
}

// fields writes `{ name: T; ... }`. forceOptional names are written optional, forceNever names as `?: never`.
func (w *walker) fields(props map[string]any, required map[string]bool, extra string, depth int, forceOptional, forceNever map[string]bool) string {
	if len(props) == 0 {
		if extra == "" {
			return "Record<string, never>"
		}
		return "Record<string, " + extra + ">"
	}
	names := make([]string, 0, len(props))
	for n := range props {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("{\n")
	for _, n := range names {
		prop := props[n]
		if m, ok := prop.(map[string]any); ok {
			b.WriteString(jsdoc("  ", docLines(m)))
		}
		opt := "?"
		if required[n] && !forceOptional[n] {
			opt = ""
		}
		typ := w.render(prop, depth+1)
		if forceNever[n] {
			opt, typ = "?", "never"
		}
		b.WriteString("  " + member(n) + opt + ": " + indent(typ, "  ") + ";\n")
	}
	if extra != "" {
		b.WriteString("  [key: string]: " + indent(extra, "  ") + ";\n")
	}
	b.WriteString("}")
	return b.String()
}

// variants reads the one if/else form the MCP output schemas use: `if` holds only `required`, there is no `then`, and `else` holds only
// `required`. It means: when the if-fields are present the object is the failure form, otherwise the else-fields are required.
func (w *walker) variants(s map[string]any) (v struct{ ifReq, elseReq []string }, ok bool) {
	ifs, hasIf := s["if"].(map[string]any)
	els, hasElse := s["else"].(map[string]any)
	if !hasIf || !hasElse || s["then"] != nil {
		return v, false
	}
	v.ifReq, ok = onlyRequired(ifs)
	if !ok {
		return v, false
	}
	v.elseReq, ok = onlyRequired(els)
	return v, ok && len(v.ifReq) > 0
}

func onlyRequired(m map[string]any) ([]string, bool) {
	if len(m) != 1 {
		return nil, false
	}
	list, ok := m["required"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		name, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, name)
	}
	return out, true
}

// variantObject writes the failure and success forms. Both share one body (written as the type `<Name>`, see variantNames) so a large
// output is not repeated; the caller sees `Failure | Success`.
func (w *walker) variantObject(s, props map[string]any, required map[string]bool, extra string, v struct{ ifReq, elseReq []string }, depth int) string {
	optional := map[string]bool{}
	for _, n := range v.ifReq {
		optional[n] = true
	}
	for _, n := range v.elseReq {
		optional[n] = true
	}
	base := w.fields(props, required, extra, depth, optional, nil)
	pick := func(names []string) string {
		quoted := make([]string, 0, len(names))
		for _, n := range names {
			if _, known := props[n]; known {
				quoted = append(quoted, quote(n))
			}
		}
		if len(quoted) == 0 {
			return ""
		}
		sort.Strings(quoted)
		return "Required<Pick<" + "BASE" + ", " + strings.Join(quoted, " | ") + ">>"
	}
	failure := "BASE"
	if p := pick(v.ifReq); p != "" {
		failure += " & " + p
	}
	success := "BASE"
	if p := pick(v.elseReq); p != "" {
		success += " & " + p
	}
	never := map[string]bool{}
	for _, n := range v.ifReq {
		if _, known := props[n]; known {
			never[n] = true
		}
	}
	if len(never) > 0 {
		names := make([]string, 0, len(never))
		for n := range never {
			names = append(names, member(n)+"?: never")
		}
		sort.Strings(names)
		success += " & { " + strings.Join(names, "; ") + " }"
	}
	// The shared body is its own type so a large output is not written twice; the two forms are named too.
	baseName, failName, okName := w.name+"Base", w.name+"Failure", w.name+"Success"
	for _, n := range []string{baseName, failName, okName} {
		if err := w.g.reserve(n, "variant of "+w.name); err != nil {
			w.err = err
			return "unknown"
		}
	}
	for _, t := range [][3]string{
		{baseName, base, ""},
		{failName, strings.ReplaceAll(failure, "BASE", baseName), "The call failed: error is set and the tool's own fields are absent."},
		{okName, strings.ReplaceAll(success, "BASE", baseName), "The call worked: the fields the tool always returns are set."},
	} {
		if err := w.g.addType(t[0], t[1], t[2]); err != nil {
			w.err = err
			return "unknown"
		}
	}
	return failName + " | " + okName
}

// ---- names, comments, escaping ----

var reserved = map[string]bool{
	"break": true, "case": true, "catch": true, "class": true, "const": true, "continue": true, "debugger": true, "default": true, "delete": true,
	"do": true, "else": true, "enum": true, "export": true, "extends": true, "false": true, "finally": true, "for": true, "function": true,
	"if": true, "import": true, "in": true, "instanceof": true, "new": true, "null": true, "return": true, "super": true, "switch": true,
	"this": true, "throw": true, "true": true, "try": true, "typeof": true, "var": true, "void": true, "while": true, "with": true, "yield": true,
	"let": true, "static": true, "implements": true, "interface": true, "package": true, "private": true, "protected": true, "public": true,
	"await": true, "async": true, "type": true, "namespace": true, "declare": true, "module": true, "any": true, "unknown": true, "never": true,
}

func isIdent(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// funcName makes a tool name a safe function name: other characters become _, a leading digit or reserved word gets a _ in front.
func funcName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 100 {
		s = s[:100]
	}
	if s == "" || (s[0] >= '0' && s[0] <= '9') || reserved[s] {
		s = "_" + s
	}
	return s
}

// pascal turns audit_get_slowest into AuditGetSlowest; anything that is not a letter or digit separates words.
func pascal(name string) string {
	var b strings.Builder
	upper := true
	for _, r := range name {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if upper {
				b.WriteRune(unicode.ToUpper(r))
			} else {
				b.WriteRune(r)
			}
			upper = false
		} else {
			upper = true
		}
	}
	s := b.String()
	if len(s) > 100 {
		s = s[:100]
	}
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	return s
}

// member writes a property name, quoted unless it is a plain identifier.
func member(name string) string {
	if isIdent(name) {
		return name
	}
	return quote(name)
}

// quote is a JSON string literal made safe inside a .d.ts: encoding/json escapes controls, quotes and U+2028/2029; the other format and
// control characters (bidi overrides, zero width, C1) are escaped too, so no character in the file can reorder or hide what a reader sees.
func quote(s string) string {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	out := strings.TrimRight(buf.String(), "\n")
	var b strings.Builder
	for _, r := range out {
		if r > 0x7e && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029') {
			fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// safeLine makes text fit on one comment line: control characters, line separators and bidi controls become spaces.
func safeLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == ' ' || r == '\u0085':
			b.WriteByte(' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			// dropped: no zero width or direction trick in a comment
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// safeComment makes one line safe inside /** ... */: it can never end the comment.
func safeComment(s string) string {
	s = safeLine(s)
	s = strings.ReplaceAll(s, "*/", "*\u200a/")
	s = strings.ReplaceAll(s, "/*", "/\u200a*")
	return s
}

// wrap breaks text into lines of about n bytes at spaces. The text may hold newlines (kept as line breaks).
func wrap(text string, n int) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		words := strings.Fields(para)
		cur := ""
		for _, w := range words {
			if cur != "" && len(cur)+1+len(w) > n {
				lines = append(lines, cur)
				cur = w
				continue
			}
			if cur == "" {
				cur = w
			} else {
				cur += " " + w
			}
		}
		if cur != "" {
			lines = append(lines, cur)
		}
	}
	return lines
}

func jsdoc(pad string, lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(pad + "/**\n")
	for _, l := range lines {
		l = safeComment(l)
		if l == "" {
			continue
		}
		b.WriteString(pad + " * " + l + "\n")
	}
	b.WriteString(pad + " */\n")
	return b.String()
}

func indent(s, pad string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	return strings.ReplaceAll(s, "\n", "\n"+pad)
}

// describe is the description line(s) of a named type.
func describe(m map[string]any) string {
	d, _ := m["description"].(string)
	return d
}

// docLines is the comment of one property: its description and the constraints a script author should know.
func docLines(m map[string]any) []string {
	var lines []string
	if d, ok := m["description"].(string); ok {
		lines = append(lines, wrap(d, 100)...)
	}
	var c []string
	if f, ok := m["format"].(string); ok {
		c = append(c, "format "+f)
	}
	for _, k := range []string{"minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems"} {
		if v, ok := m[k]; ok {
			c = append(c, k+" "+literal(v))
		}
	}
	if p, ok := m["pattern"].(string); ok {
		c = append(c, "pattern "+p)
	}
	if u, ok := m["uniqueItems"].(bool); ok && u {
		c = append(c, "unique items")
	}
	if len(c) > 0 {
		lines = append(lines, "["+strings.Join(c, ", ")+"]")
	}
	return lines
}
