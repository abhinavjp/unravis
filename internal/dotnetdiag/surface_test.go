package dotnetdiag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// fixedSurface is every exported name of the package. There is no exported
// function that takes a provider, a keyword, a command id or a payload; the two
// message encoders are unexported. Adding a name here is a security decision.
var fixedSurface = []string{
	// functions
	"CLIDialer", "DefaultDialer", "DefaultOptions", "ExitCode", "ParsePreamble", "Probe", "RunAs", "Sentence",
	// types
	"Conn", "Dialer", "Error", "Options", "Peer", "Preamble", "Reason", "Result", "Target",
	// consts
	"PreambleLen",
	"ReasonBadReply", "ReasonBadStream", "ReasonBusy", "ReasonCancelled", "ReasonDenied",
	"ReasonDisabled", "ReasonNotRunning", "ReasonOther", "ReasonPeerMismatch", "ReasonRejected",
	"ReasonStopFailed", "ReasonTimeout", "ReasonUnsupported",
	// vars
	"ErrBadTarget",
}

// fixedMethods are the exported methods, as Type.Method.
var fixedMethods = []string{"Error.Error"}

func TestPublicSurfaceIsFixed(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var names, methods []string
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		// Every platform's file is read, whatever its build tag.
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					names = append(names, d.Name.Name)
					continue
				}
				// Methods of unexported types (the platform dialers) are only
				// reachable through the Dialer interface, so they add no name.
				if rn := recvName(d.Recv.List[0].Type); ast.IsExported(rn) {
					methods = append(methods, rn+"."+d.Name.Name)
				}
			case *ast.GenDecl:
				for _, sp := range d.Specs {
					switch sp := sp.(type) {
					case *ast.TypeSpec:
						if sp.Name.IsExported() {
							names = append(names, sp.Name.Name)
						}
					case *ast.ValueSpec:
						for _, id := range sp.Names {
							if id.IsExported() {
								names = append(names, id.Name)
							}
						}
					}
				}
			}
		}
	}
	compare(t, "names", names, fixedSurface)
	compare(t, "methods", methods, fixedMethods)
}

func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return "?"
}

func compare(t *testing.T, what string, got, want []string) {
	t.Helper()
	sort.Strings(got)
	// A name defined once per platform (RunAs) counts once.
	uniq := got[:0]
	for i, n := range got {
		if i == 0 || n != got[i-1] {
			uniq = append(uniq, n)
		}
	}
	got = uniq
	w := append([]string(nil), want...)
	sort.Strings(w)
	if strings.Join(got, ",") != strings.Join(w, ",") {
		t.Fatalf("the exported %s of internal/dotnetdiag changed: a reviewer must approve a new way to talk to the runtime.\n got  %v\n want %v", what, got, w)
	}
}
