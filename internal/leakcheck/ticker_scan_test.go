package leakcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// BL-424: a timer that fires several times a second keeps the CPU out of its low
// power states (on Windows a sub-second ticker arms a high resolution timer). This
// scan finds every time.NewTicker and time.Tick in the non-test Go code. A ticker
// passes when its period is a constant of one second or more. Anything else (a
// shorter period, or a period the scan cannot work out) must carry a comment
//
//	// idle-ok: <why it does not run while the app is idle>
//
// on the line of the call or the line above it.

const idleOK = "idle-ok:"

type tickerFinding struct {
	File, Why string
	Line      int
}

var timeUnits = map[string]int64{
	"Nanosecond": 1, "Microsecond": 1e3, "Millisecond": 1e6, "Second": 1e9, "Minute": 60e9, "Hour": 3600e9,
}

// scanTickers parses the non-test .go files under root and returns the tickers that
// are neither a constant of at least one second nor marked idle-ok.
func scanTickers(root string) ([]tickerFinding, error) {
	byDir := map[string][]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || n == "testdata" || strings.HasPrefix(n, ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			byDir[filepath.Dir(p)] = append(byDir[filepath.Dir(p)], p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []tickerFinding
	for dir, files := range byDir {
		fset := token.NewFileSet()
		var parsed []*ast.File
		for _, f := range files {
			af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			parsed = append(parsed, af)
		}
		consts := packageConsts(parsed)
		for _, af := range parsed {
			marked := map[int]bool{}
			for _, cg := range af.Comments {
				for _, c := range cg.List {
					if strings.Contains(c.Text, idleOK) {
						marked[fset.Position(c.Pos()).Line] = true
					}
				}
			}
			ast.Inspect(af, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "NewTicker" && sel.Sel.Name != "Tick") {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "time" {
					return true
				}
				pos := fset.Position(call.Pos())
				if marked[pos.Line] || marked[pos.Line-1] {
					return true
				}
				ns, known := evalDuration(call.Args[0], consts, 0)
				switch {
				case !known:
					out = append(out, tickerFinding{pos.Filename, "period is not a constant the scan can read", pos.Line})
				case ns < int64(time.Second):
					out = append(out, tickerFinding{pos.Filename, "period is under one second", pos.Line})
				}
				return true
			})
		}
		_ = dir
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// packageConsts collects the package-level constants and initialised variables.
func packageConsts(files []*ast.File) map[string]ast.Expr {
	m := map[string]ast.Expr{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			// A package-level var with a constant initializer counts too (a period tests
			// shorten, such as the updater's autoTick); the scan reads its initial value.
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				if len(vs.Names) == len(vs.Values) {
					for i, n := range vs.Names {
						m[n.Name] = vs.Values[i]
					}
				}
			}
		}
	}
	return m
}

// evalDuration works out a constant duration (in nanoseconds) from the shapes the
// code uses: 50 * time.Millisecond, time.Minute, a named constant, time.Duration(n).
func evalDuration(e ast.Expr, consts map[string]ast.Expr, depth int) (int64, bool) {
	if depth > 8 {
		return 0, false
	}
	switch v := e.(type) {
	case *ast.ParenExpr:
		return evalDuration(v.X, consts, depth+1)
	case *ast.BasicLit:
		if v.Kind != token.INT {
			return 0, false
		}
		var n int64
		for _, c := range strings.ReplaceAll(v.Value, "_", "") {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int64(c-'0')
		}
		return n, true
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok && id.Name == "time" {
			u, ok := timeUnits[v.Sel.Name]
			return u, ok
		}
	case *ast.Ident:
		if x, ok := consts[v.Name]; ok {
			return evalDuration(x, consts, depth+1)
		}
	case *ast.CallExpr:
		if len(v.Args) == 1 {
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Duration" {
				return evalDuration(v.Args[0], consts, depth+1)
			}
		}
	case *ast.BinaryExpr:
		a, ok1 := evalDuration(v.X, consts, depth+1)
		b, ok2 := evalDuration(v.Y, consts, depth+1)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch v.Op {
		case token.MUL:
			return a * b, true
		case token.ADD:
			return a + b, true
		case token.SUB:
			return a - b, true
		case token.QUO:
			if b != 0 {
				return a / b, true
			}
		}
	}
	return 0, false
}

// TestNoUnexplainedTickers is the BL-424 guard over the real code base.
func TestNoUnexplainedTickers(t *testing.T) {
	for _, dir := range []string{"../../internal", "../../cmd"} {
		got, err := scanTickers(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range got {
			t.Errorf("%s:%d: %s; use a period of one second or more, or explain with a comment %q on or above the call",
				f.File, f.Line, f.Why, "// "+idleOK+" <reason>")
		}
	}
}

// TestTickerScanFindsASubSecondTicker is the negative case: the scan really fails
// on a short ticker, and the comment really clears it.
func TestTickerScanFindsASubSecondTicker(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("bad.go", "package x\nimport \"time\"\nfunc f() { _ = time.NewTicker(10 * time.Millisecond) }\n")
	write("named.go", "package x\nimport \"time\"\nconst tick = 250 * time.Millisecond\nfunc g() { _ = time.NewTicker(tick) }\n")
	write("unknown.go", "package x\nimport \"time\"\nfunc h(d time.Duration) { _ = time.NewTicker(d) }\n")
	write("tick.go", "package x\nimport \"time\"\nfunc i() { _ = time.Tick(time.Millisecond) }\n")
	write("ok.go", "package x\nimport \"time\"\nconst slow = time.Minute\nfunc j() { _ = time.NewTicker(slow); _ = time.NewTicker(2 * time.Second) }\n")
	write("marked.go", "package x\nimport \"time\"\nfunc k(d time.Duration) {\n\t// idle-ok: only while a case is being erased\n\t_ = time.NewTicker(5 * time.Millisecond)\n\t_ = time.NewTicker(d) // idle-ok: a running recording's interval\n}\n")
	write("bad_test.go", "package x\nimport \"time\"\nfunc l() { _ = time.NewTicker(time.Millisecond) }\n")
	got, err := scanTickers(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, f := range got {
		files[filepath.Base(f.File)] = true
	}
	for _, want := range []string{"bad.go", "named.go", "unknown.go", "tick.go"} {
		if !files[want] {
			t.Errorf("the scan missed %s: %+v", want, got)
		}
	}
	for _, not := range []string{"ok.go", "marked.go", "bad_test.go"} {
		if files[not] {
			t.Errorf("the scan flagged %s: %+v", not, got)
		}
	}
}
