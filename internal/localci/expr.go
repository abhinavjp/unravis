package localci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// ExprContext is the value of an expression evaluation.
type ExprContext struct {
	// Vars holds the top-level contexts (github, runner, env, matrix, needs,
	// steps, inputs, vars, secrets, job, ...). Values are map[string]any,
	// []any, string, bool, float64 (other numeric kinds are accepted) or nil.
	Vars map[string]any

	// JobStatus is the job status so far: "success" (default when empty),
	// "failure" or "cancelled". It drives success()/failure()/cancelled().
	JobStatus string

	// Workdir is the directory hashFiles() resolves patterns against.
	Workdir string
}

// ExprError is returned for every syntax or evaluation failure.
type ExprError struct {
	Src    string // the expression source
	Offset int    // byte offset into Src
	Msg    string
}

func (e *ExprError) Error() string {
	return fmt.Sprintf("expression %q at offset %d: %s", e.Src, e.Offset, e.Msg)
}

// ---------------------------------------------------------------- lexer

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tNum
	tStr
	tPunct
)

type token struct {
	kind tokKind
	s    string
	num  float64
	off  int
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'':
			start := i
			i++
			var sb strings.Builder
			closed := false
			for i < len(src) {
				if src[i] == '\'' {
					if i+1 < len(src) && src[i+1] == '\'' {
						sb.WriteByte('\'')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				sb.WriteByte(src[i])
				i++
			}
			if !closed {
				return nil, &ExprError{src, start, "unterminated string literal"}
			}
			toks = append(toks, token{kind: tStr, s: sb.String(), off: start})
		case isDigit(c) || (c == '-' && i+1 < len(src) && (isDigit(src[i+1]) || src[i+1] == '.')):
			start := i
			if c == '-' {
				i++
			}
			if i+1 < len(src) && src[i] == '0' && (src[i+1] == 'x' || src[i+1] == 'X') {
				i += 2
				for i < len(src) && isHex(src[i]) {
					i++
				}
			} else {
				for i < len(src) && (isDigit(src[i]) || src[i] == '.') {
					i++
				}
				if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
					j := i + 1
					if j < len(src) && (src[j] == '+' || src[j] == '-') {
						j++
					}
					if j < len(src) && isDigit(src[j]) {
						for j < len(src) && isDigit(src[j]) {
							j++
						}
						i = j
					}
				}
			}
			n, ok := parseNumber(src[start:i])
			if !ok || math.IsNaN(n) {
				return nil, &ExprError{src, start, "invalid number " + strconv.Quote(src[start:i])}
			}
			toks = append(toks, token{kind: tNum, s: src[start:i], num: n, off: start})
		case isIdentStart(c):
			start := i
			for i < len(src) && (isIdentStart(src[i]) || isDigit(src[i]) || src[i] == '-') {
				i++
			}
			toks = append(toks, token{kind: tIdent, s: src[start:i], off: start})
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "&&", "||", "<=", ">=":
				toks = append(toks, token{kind: tPunct, s: two, off: i})
				i += 2
				continue
			}
			switch c {
			case '(', ')', '[', ']', '.', ',', '!', '<', '>', '*':
				toks = append(toks, token{kind: tPunct, s: string(c), off: i})
				i++
			default:
				return nil, &ExprError{src, i, fmt.Sprintf("unexpected character %q", string(rune(c)))}
			}
		}
	}
	toks = append(toks, token{kind: tEOF, off: len(src)})
	return toks, nil
}

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// parseNumber parses a numeric literal or numeric string the way Actions does
// (decimal, exponent, 0x hex, optional sign). It rejects inf/nan words.
func parseNumber(s string) (float64, bool) {
	neg := false
	t := s
	if strings.HasPrefix(t, "-") {
		neg = true
		t = t[1:]
	} else if strings.HasPrefix(t, "+") {
		t = t[1:]
	}
	if t == "" {
		return 0, false
	}
	if len(t) > 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
		n, err := strconv.ParseUint(t[2:], 16, 64)
		if err != nil {
			return 0, false
		}
		f := float64(n)
		if neg {
			f = -f
		}
		return f, true
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(isDigit(c) || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-') {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, false
	}
	if neg {
		f = -f
	}
	return f, true
}

// ---------------------------------------------------------------- AST

type node interface {
	eval(e *evaluator) (any, error)
}

type litNode struct{ v any }

type nameNode struct {
	name string
	off  int
}

type propNode struct {
	obj node
	key node
}

type callNode struct {
	name string
	args []node
	off  int
}

type notNode struct{ x node }

type binNode struct {
	op   string
	l, r node
	off  int
}

var knownContexts = map[string]bool{
	"github": true, "runner": true, "env": true, "vars": true, "job": true,
	"jobs": true, "steps": true, "secrets": true, "strategy": true,
	"matrix": true, "needs": true, "inputs": true,
}

type funcSpec struct {
	min, max int // max < 0 means unbounded
}

var knownFuncs = map[string]funcSpec{
	"success":    {0, 0},
	"failure":    {0, 0},
	"always":     {0, 0},
	"cancelled":  {0, 0},
	"hashfiles":  {1, -1},
	"contains":   {2, 2},
	"startswith": {2, 2},
	"endswith":   {2, 2},
	"format":     {1, -1},
	"tojson":     {1, 1},
	"fromjson":   {1, 1},
	"join":       {1, 2},
}

// ---------------------------------------------------------------- parser

type parser struct {
	src  string
	toks []token
	pos  int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tEOF {
		p.pos++
	}
	return t
}
func (p *parser) errf(off int, f string, a ...any) error {
	return &ExprError{p.src, off, fmt.Sprintf(f, a...)}
}
func (p *parser) isPunct(s string) bool {
	t := p.peek()
	return t.kind == tPunct && t.s == s
}

func parseExpr(src string) (node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{src: src, toks: toks}
	if p.peek().kind == tEOF {
		return nil, p.errf(0, "empty expression")
	}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, p.errf(t.off, "unexpected %s", describe(t))
	}
	return n, nil
}

func describe(t token) string {
	switch t.kind {
	case tEOF:
		return "end of expression"
	case tStr:
		return "string literal"
	default:
		return strconv.Quote(t.s)
	}
}

func (p *parser) parseOr() (node, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isPunct("||") {
		t := p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &binNode{"||", l, r, t.off}
	}
	return l, nil
}

func (p *parser) parseAnd() (node, error) {
	l, err := p.parseEq()
	if err != nil {
		return nil, err
	}
	for p.isPunct("&&") {
		t := p.next()
		r, err := p.parseEq()
		if err != nil {
			return nil, err
		}
		l = &binNode{"&&", l, r, t.off}
	}
	return l, nil
}

func (p *parser) parseEq() (node, error) {
	l, err := p.parseRel()
	if err != nil {
		return nil, err
	}
	for p.isPunct("==") || p.isPunct("!=") {
		t := p.next()
		r, err := p.parseRel()
		if err != nil {
			return nil, err
		}
		l = &binNode{t.s, l, r, t.off}
	}
	return l, nil
}

func (p *parser) parseRel() (node, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.isPunct("<") || p.isPunct(">") || p.isPunct("<=") || p.isPunct(">=") {
		t := p.next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &binNode{t.s, l, r, t.off}
	}
	return l, nil
}

func (p *parser) parseUnary() (node, error) {
	if p.isPunct("!") {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &notNode{x}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (node, error) {
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isPunct("."):
			p.next()
			t := p.next()
			switch {
			case t.kind == tIdent:
				n = &propNode{n, &litNode{t.s}}
			case t.kind == tPunct && t.s == "*":
				return nil, p.errf(t.off, "unsupported: object filter '.*'")
			default:
				return nil, p.errf(t.off, "expected property name after '.', got %s", describe(t))
			}
		case p.isPunct("["):
			p.next()
			if p.isPunct("*") {
				return nil, p.errf(p.peek().off, "unsupported: object filter '[*]'")
			}
			k, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if !p.isPunct("]") {
				return nil, p.errf(p.peek().off, "expected ']', got %s", describe(p.peek()))
			}
			p.next()
			n = &propNode{n, k}
		default:
			return n, nil
		}
	}
}

func (p *parser) parsePrimary() (node, error) {
	t := p.next()
	switch t.kind {
	case tStr:
		return &litNode{t.s}, nil
	case tNum:
		return &litNode{t.num}, nil
	case tIdent:
		switch t.s {
		case "true":
			return &litNode{true}, nil
		case "false":
			return &litNode{false}, nil
		case "null":
			return &litNode{nil}, nil
		}
		if p.isPunct("(") {
			return p.parseCall(t)
		}
		if !knownContexts[strings.ToLower(t.s)] {
			return nil, p.errf(t.off, "unknown context or keyword %q", t.s)
		}
		return &nameNode{t.s, t.off}, nil
	case tPunct:
		if t.s == "(" {
			n, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			if !p.isPunct(")") {
				return nil, p.errf(p.peek().off, "expected ')', got %s", describe(p.peek()))
			}
			p.next()
			return n, nil
		}
	}
	return nil, p.errf(t.off, "unexpected %s", describe(t))
}

func (p *parser) parseCall(name token) (node, error) {
	lname := strings.ToLower(name.s)
	spec, ok := knownFuncs[lname]
	if !ok {
		return nil, p.errf(name.off, "unsupported function %s", name.s)
	}
	p.next() // (
	var args []node
	if p.isPunct(")") {
		p.next()
	} else {
		for {
			a, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.isPunct(",") {
				p.next()
				continue
			}
			if p.isPunct(")") {
				p.next()
				break
			}
			return nil, p.errf(p.peek().off, "expected ',' or ')', got %s", describe(p.peek()))
		}
	}
	if len(args) < spec.min || (spec.max >= 0 && len(args) > spec.max) {
		return nil, p.errf(name.off, "wrong number of arguments (%d) to %s", len(args), name.s)
	}
	return &callNode{lname, args, name.off}, nil
}

// ---------------------------------------------------------------- values

// norm converts common Go types to the canonical set used by the evaluator.
func norm(v any) any {
	switch x := v.(type) {
	case nil, string, bool, float64, map[string]any, []any:
		return v
	case int:
		return float64(x)
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case map[string]string:
		m := make(map[string]any, len(x))
		for k, s := range x {
			m[k] = s
		}
		return m
	case []string:
		a := make([]any, len(x))
		for i, s := range x {
			a[i] = s
		}
		return a
	}
	return v
}

type vkind int

const (
	kNull vkind = iota
	kBool
	kNum
	kStr
	kObj
)

func kindOf(v any) vkind {
	switch norm(v).(type) {
	case nil:
		return kNull
	case bool:
		return kBool
	case float64:
		return kNum
	case string:
		return kStr
	}
	return kObj
}

func truthy(v any) bool {
	switch x := norm(v).(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return !(x == 0 || math.IsNaN(x))
	case string:
		return x != ""
	}
	return true
}

func toNumber(v any) float64 {
	switch x := norm(v).(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		if f, ok := parseNumber(s); ok {
			return f
		}
		return math.NaN()
	}
	return math.NaN()
}

func looseEqual(a, b any) bool {
	ka, kb := kindOf(a), kindOf(b)
	if ka == kb {
		switch ka {
		case kNull:
			return true
		case kBool:
			return norm(a).(bool) == norm(b).(bool)
		case kNum:
			return norm(a).(float64) == norm(b).(float64)
		case kStr:
			return strings.EqualFold(norm(a).(string), norm(b).(string))
		}
		return false // objects/arrays compare by reference in Actions
	}
	return toNumber(a) == toNumber(b)
}

func formatNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func stringify(v any) string {
	switch x := norm(v).(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return formatNumber(x)
	}
	b, err := json.Marshal(norm(v))
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func getProp(v any, key string) any {
	m, ok := norm(v).(map[string]any)
	if !ok {
		return nil
	}
	if x, ok := m[key]; ok {
		return norm(x)
	}
	for k, x := range m {
		if strings.EqualFold(k, key) {
			return norm(x)
		}
	}
	return nil
}

// ---------------------------------------------------------------- evaluator

type evaluator struct {
	src string
	c   *ExprContext
}

func (e *evaluator) errf(off int, f string, a ...any) error {
	return &ExprError{e.src, off, fmt.Sprintf(f, a...)}
}

func (n *litNode) eval(*evaluator) (any, error) { return n.v, nil }

func (n *nameNode) eval(e *evaluator) (any, error) {
	if e.c == nil {
		return nil, nil
	}
	return getProp(e.c.Vars, n.name), nil
}

func (n *propNode) eval(e *evaluator) (any, error) {
	o, err := n.obj.eval(e)
	if err != nil {
		return nil, err
	}
	k, err := n.key.eval(e)
	if err != nil {
		return nil, err
	}
	switch x := norm(o).(type) {
	case map[string]any:
		return getProp(x, stringify(k)), nil
	case []any:
		if kindOf(k) == kNum {
			f := norm(k).(float64)
			if f == math.Trunc(f) && f >= 0 && f < float64(len(x)) {
				return norm(x[int(f)]), nil
			}
		}
	}
	return nil, nil
}

func (n *notNode) eval(e *evaluator) (any, error) {
	v, err := n.x.eval(e)
	if err != nil {
		return nil, err
	}
	return !truthy(v), nil
}

func (n *binNode) eval(e *evaluator) (any, error) {
	l, err := n.l.eval(e)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "&&":
		if !truthy(l) {
			return l, nil
		}
		return n.r.eval(e)
	case "||":
		if truthy(l) {
			return l, nil
		}
		return n.r.eval(e)
	}
	r, err := n.r.eval(e)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case "==":
		return looseEqual(l, r), nil
	case "!=":
		return !looseEqual(l, r), nil
	}
	// relational
	if kindOf(l) == kStr && kindOf(r) == kStr {
		a, b := strings.ToLower(norm(l).(string)), strings.ToLower(norm(r).(string))
		switch n.op {
		case "<":
			return a < b, nil
		case "<=":
			return a <= b, nil
		case ">":
			return a > b, nil
		default:
			return a >= b, nil
		}
	}
	a, b := toNumber(l), toNumber(r)
	switch n.op {
	case "<":
		return a < b, nil
	case "<=":
		return a <= b, nil
	case ">":
		return a > b, nil
	default:
		return a >= b, nil
	}
}

func (e *evaluator) status() string {
	if e.c == nil || e.c.JobStatus == "" {
		return "success"
	}
	return strings.ToLower(e.c.JobStatus)
}

func (n *callNode) eval(e *evaluator) (any, error) {
	switch n.name {
	case "success":
		return e.status() == "success", nil
	case "failure":
		return e.status() == "failure", nil
	case "cancelled":
		return e.status() == "cancelled", nil
	case "always":
		return true, nil
	}
	args := make([]any, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(e)
		if err != nil {
			return nil, err
		}
		args[i] = norm(v)
	}
	switch n.name {
	case "hashfiles":
		pats := make([]string, len(args))
		for i, a := range args {
			pats[i] = stringify(a)
		}
		dir := ""
		if e.c != nil {
			dir = e.c.Workdir
		}
		h, err := hashFiles(dir, pats)
		if err != nil {
			return nil, e.errf(n.off, "hashFiles: %v", err)
		}
		return h, nil
	case "contains":
		if arr, ok := args[0].([]any); ok {
			for _, x := range arr {
				if looseEqual(x, args[1]) {
					return true, nil
				}
			}
			return false, nil
		}
		return strings.Contains(strings.ToLower(stringify(args[0])), strings.ToLower(stringify(args[1]))), nil
	case "startswith":
		return strings.HasPrefix(strings.ToLower(stringify(args[0])), strings.ToLower(stringify(args[1]))), nil
	case "endswith":
		return strings.HasSuffix(strings.ToLower(stringify(args[0])), strings.ToLower(stringify(args[1]))), nil
	case "format":
		s, err := formatString(stringify(args[0]), args[1:])
		if err != nil {
			return nil, e.errf(n.off, "format: %v", err)
		}
		return s, nil
	case "tojson":
		b, err := json.MarshalIndent(args[0], "", "  ")
		if err != nil {
			return nil, e.errf(n.off, "toJSON: %v", err)
		}
		return string(b), nil
	case "fromjson":
		var v any
		if err := json.Unmarshal([]byte(stringify(args[0])), &v); err != nil {
			return nil, e.errf(n.off, "fromJSON: %v", err)
		}
		return v, nil
	case "join":
		sep := ","
		if len(args) > 1 {
			sep = stringify(args[1])
		}
		if arr, ok := args[0].([]any); ok {
			parts := make([]string, len(arr))
			for i, x := range arr {
				parts[i] = stringify(x)
			}
			return strings.Join(parts, sep), nil
		}
		return stringify(args[0]), nil
	}
	return nil, e.errf(n.off, "unsupported function %s", n.name)
}

func formatString(f string, args []any) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(f); i++ {
		c := f[i]
		switch c {
		case '{':
			if i+1 < len(f) && f[i+1] == '{' {
				sb.WriteByte('{')
				i++
				continue
			}
			j := strings.IndexByte(f[i:], '}')
			if j < 0 {
				return "", fmt.Errorf("unterminated '{' in format string")
			}
			idx, err := strconv.Atoi(f[i+1 : i+j])
			if err != nil || idx < 0 {
				return "", fmt.Errorf("invalid placeholder %q", f[i:i+j+1])
			}
			if idx >= len(args) {
				return "", fmt.Errorf("placeholder {%d} has no argument", idx)
			}
			sb.WriteString(stringify(args[idx]))
			i += j
		case '}':
			if i+1 < len(f) && f[i+1] == '}' {
				sb.WriteByte('}')
				i++
				continue
			}
			return "", fmt.Errorf("unmatched '}' in format string")
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------- hashFiles

func hasMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

func matchSegs(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchSegs(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	if ok, _ := path.Match(pat[0], name[0]); !ok {
		return false
	}
	return matchSegs(pat[1:], name[1:])
}

func hashFiles(workdir string, patterns []string) (string, error) {
	if workdir == "" {
		workdir = "."
	}
	fsys := os.DirFS(workdir)
	var inc, exc [][]string
	walk := false
	direct := map[string]bool{}
	for _, raw := range patterns {
		p := strings.ReplaceAll(raw, "\\", "/")
		neg := strings.HasPrefix(p, "!")
		p = strings.TrimPrefix(p, "!")
		p = strings.TrimPrefix(p, "./")
		p = strings.Trim(p, "/")
		if p == "" {
			continue
		}
		segs := strings.Split(p, "/")
		for _, s := range segs {
			if _, err := path.Match(s, ""); err != nil {
				return "", fmt.Errorf("bad pattern %q: %v", raw, err)
			}
		}
		if neg {
			exc = append(exc, segs)
			continue
		}
		inc = append(inc, segs)
		if hasMeta(p) {
			walk = true
		} else {
			direct[p] = true
		}
	}
	files := map[string]bool{}
	for p := range direct {
		if st, err := fs.Stat(fsys, p); err == nil && st.Mode().IsRegular() {
			files[p] = true
		}
	}
	if walk {
		err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			name := strings.Split(p, "/")
			for _, pat := range inc {
				if matchSegs(pat, name) {
					files[p] = true
					break
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	var list []string
	for p := range files {
		name := strings.Split(p, "/")
		excluded := false
		for _, pat := range exc {
			if matchSegs(pat, name) {
				excluded = true
				break
			}
		}
		if !excluded {
			list = append(list, p)
		}
	}
	if len(list) == 0 {
		return "", nil
	}
	sort.Strings(list)
	outer := sha256.New()
	for _, p := range list {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		outer.Write(sum[:])
	}
	return hex.EncodeToString(outer.Sum(nil)), nil
}

// ---------------------------------------------------------------- public API

// CheckExpr validates syntax and function names of one expression (without the
// ${{ }} wrapper) without evaluating it.
func CheckExpr(src string) error {
	_, err := parseExpr(src)
	return err
}

// EvalExpr evaluates one expression (without the ${{ }} wrapper).
func EvalExpr(src string, c *ExprContext) (any, error) {
	n, err := parseExpr(src)
	if err != nil {
		return nil, err
	}
	return n.eval(&evaluator{src: src, c: c})
}

type exprSpan struct {
	start, end int    // byte range of the whole ${{ ... }}
	src        string // trimmed expression source
}

// scanExprs finds every ${{ ... }} in s. A "}}" inside a single-quoted string
// does not close the expression. Unterminated is the offset of an unclosed
// "${{", or -1.
func scanExprs(s string) (spans []exprSpan, unterminated int) {
	i := 0
	for {
		j := strings.Index(s[i:], "${{")
		if j < 0 {
			return spans, -1
		}
		start := i + j
		k := start + 3
		inStr := false
		end := -1
		for k < len(s) {
			if s[k] == '\'' {
				inStr = !inStr
			} else if !inStr && s[k] == '}' && k+1 < len(s) && s[k+1] == '}' {
				end = k + 2
				break
			}
			k++
		}
		if end < 0 {
			return spans, start
		}
		spans = append(spans, exprSpan{start, end, strings.TrimSpace(s[start+3 : end-2])})
		i = end
	}
}

// ExprsIn returns the raw expression sources inside every ${{ }} of s.
func ExprsIn(s string) []string {
	spans, _ := scanExprs(s)
	out := make([]string, len(spans))
	for i, sp := range spans {
		out[i] = sp.src
	}
	return out
}

// Interpolate replaces every ${{ expr }} in s with its string value.
func Interpolate(s string, c *ExprContext) (string, error) {
	spans, bad := scanExprs(s)
	if bad >= 0 {
		return "", &ExprError{s, bad, "unterminated ${{"}
	}
	var sb strings.Builder
	last := 0
	for _, sp := range spans {
		sb.WriteString(s[last:sp.start])
		v, err := EvalExpr(sp.src, c)
		if err != nil {
			return "", err
		}
		sb.WriteString(stringify(v))
		last = sp.end
	}
	sb.WriteString(s[last:])
	return sb.String(), nil
}

// condSource returns the expression text of an `if:` value, unwrapping a
// surrounding ${{ }} when present.
func condSource(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "${{") {
		if spans, bad := scanExprs(t); bad < 0 && len(spans) == 1 && spans[0].start == 0 && spans[0].end == len(t) {
			return spans[0].src
		}
	}
	return t
}

// EvalCond evaluates an `if:` value. An empty string means success(). When the
// expression has no status function an implicit "success() &&" is applied.
func EvalCond(s string, c *ExprContext) (bool, error) {
	src := condSource(s)
	status := "success"
	if c != nil && c.JobStatus != "" {
		status = strings.ToLower(c.JobStatus)
	}
	if src == "" {
		return status == "success", nil
	}
	n, err := parseExpr(src)
	if err != nil {
		return false, err
	}
	low := strings.ToLower(src)
	hasStatus := false
	for _, f := range []string{"success(", "failure(", "always(", "cancelled("} {
		if strings.Contains(low, f) {
			hasStatus = true
			break
		}
	}
	if !hasStatus && status != "success" {
		return false, nil
	}
	v, err := n.eval(&evaluator{src: src, c: c})
	if err != nil {
		return false, err
	}
	return truthy(v), nil
}
