package mcp

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Cases are an operator-side feature of the web app. The MCP surface stays at
// its ten tools, and none of its code reaches the case store.

var wantToolNames = []string{
	"audit_compare",
	"audit_compare_timelines",
	"audit_get_activity",
	"audit_get_breakdown",
	"audit_get_groups",
	"audit_get_guide",
	"audit_get_requests",
	"audit_get_slowest",
	"audit_get_timeline",
	"audit_list_sources",
}

func TestMCPAdvertisesExactlyTheTenExistingTools(t *testing.T) {
	var got []string
	for _, tool := range tools() {
		name, _ := tool["name"].(string)
		got = append(got, name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(wantToolNames, ",") {
		t.Fatalf("tools = %v\nwant    %v", got, wantToolNames)
	}
	for _, name := range got {
		for _, word := range []string{"case", "import", "host"} {
			if strings.Contains(name, word) {
				t.Errorf("tool %q looks like a case tool", name)
			}
		}
	}
}

func TestNoMCPCodeImportsTheCaseStore(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files found: %v", err)
	}
	const module = "api-audit-explorer/internal/"
	forbidden := []string{module + "casestore", module + "importer", module + "hosts"}
	fset := token.NewFileSet()
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		isTest := strings.HasSuffix(file, "_test.go")
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s imports %s", file, path)
				}
			}
			// Only the tests may reach the HTTP server (the bridge tests use it
			// as a fixture); the handlers themselves must not.
			if !isTest && (path == module+"server" || strings.HasPrefix(path, module+"server/")) {
				t.Errorf("%s imports %s", file, path)
			}
		}
	}
}
