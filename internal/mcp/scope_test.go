package mcp

import (
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/audit"
)

// The scope of a bridge is checked on the typed input of every request that names sources, and anything it cannot read is refused.
func TestCheckScopeFailsClosed(t *testing.T) {
	scoped := newBridge(nil, nil, WithSources([]string{"a", "b"}))
	q := func(ids ...string) audit.Query { return audit.Query{SourceIDs: ids} }
	for name, tc := range map[string]struct {
		input any
		ok    bool
	}{
		"no input (list, info)": {nil, true},
		"query in scope":        {q("a", "b"), true},
		"query one outside":     {q("a", "c"), false},
		"query none":            {q(), false},
		"activity in scope":     {audit.ActivityInput{SourceIDs: []string{"a"}}, true},
		"activity outside":      {audit.ActivityInput{SourceIDs: []string{"c"}}, false},
		"activity none":         {audit.ActivityInput{}, false},
		"page in scope":         {audit.PageInput{Query: q("b")}, true},
		"page outside":          {audit.PageInput{Query: q("c")}, false},
		"page none":             {audit.PageInput{}, false},
		"breakdown in scope":    {audit.BreakdownInput{Query: q("a")}, true},
		"breakdown outside":     {audit.BreakdownInput{Query: q("c")}, false},
		"unknown input type":    {map[string]any{"sourceIds": []string{"a"}}, false},
		"pointer is not known":  {&audit.Query{SourceIDs: []string{"a"}}, false},
	} {
		err := scoped.checkScope(tc.input)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil {
			var ce *CodedError
			if !errors.As(err, &ce) || ce.Wire != WirePermissionRequired || strings.Contains(err.Error(), "\"") {
				t.Errorf("%s: %#v", name, err)
			}
		}
	}
	// no scope: nothing is checked (the stdio server and the command line)
	if err := newBridge(nil, nil).checkScope(q("anything")); err != nil {
		t.Errorf("unscoped: %v", err)
	}
	// scoped with an empty list: nothing is in scope
	if err := newBridge(nil, nil, WithSources(nil)).checkScope(q("a")); err == nil {
		t.Error("an empty scope let a source through")
	}
}
