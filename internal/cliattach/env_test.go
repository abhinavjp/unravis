package cliattach

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// The attach code reads no environment variable (spec 05 D25): the run folder comes from the known folder API.
func TestReadsNoEnvironment(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "Getenv", "LookupEnv", "Environ", "ExpandEnv", "Expand", "Setenv", "Unsetenv", "Clearenv":
					t.Errorf("%s: %s", fset.Position(sel.Pos()), sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestOffWindowsNothingIsFound(t *testing.T) {
	a := Default(func() (string, error) { return "/x/bin/unravis.exe", nil }, "ua")
	if a.RunDir == nil || a.Dial == nil || a.Prober == nil {
		t.Fatal("the attacher is incomplete")
	}
}

func TestInstallDir(t *testing.T) {
	for in, want := range map[string]string{"/p/Unravis/bin/unravis.exe": "/p/Unravis", "/p/Unravis/BIN/unravis.exe": "/p/Unravis", "/p/Unravis/unravis.exe": "/p/Unravis"} {
		if got := InstallDir(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
