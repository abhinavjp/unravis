package clicore

import (
	"embed"
	"encoding/json"
	"sort"
	"strings"
)

//go:embed schemas/*.json
var schemaFS embed.FS

// NativeOps lists the operations that have an embedded description (the operations the command line answers itself), sorted.
func NativeOps() []string {
	entries, _ := schemaFS.ReadDir("schemas")
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".json") {
			out = append(out, strings.TrimSuffix(n, ".json"))
		}
	}
	sort.Strings(out)
	return out
}

// NativeSchema returns the embedded description of an operation: {operation, input, output, errors, examples}.
func NativeSchema(op string) (map[string]any, bool) {
	if op == "" || strings.ContainsAny(op, `/\`) {
		return nil, false
	}
	b, err := schemaFS.ReadFile("schemas/" + op + ".json")
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	return m, true
}
