package cmdtree

import (
	"encoding/json"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicap"
	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/mcp"
)

// auditErrors are the codes an operation that runs an MCP tool can end with.
var auditErrors = []string{"INVALID_ARGUMENT", "PERMISSION_DENIED", "APPROVAL_REQUIRED", "RESULT_EXPIRED", "BUDGET_EXCEEDED", "UPSTREAM_UNAVAILABLE",
	"APP_NOT_RUNNING", "OWNER_BUSY", "UPDATE_IN_PROGRESS", "ENDPOINT_UNTRUSTED", "VERSION_INCOMPATIBLE", "RESULT_TOO_LARGE", "CANCELED", "TIMEOUT"}

type opInfo struct {
	Operation   string `json:"operation"`
	Description string `json:"description"`
}

// describe builds the description of one operation, or false when there is none.
func describe(op string) (map[string]any, bool) {
	if m, ok := clicore.NativeSchema(op); ok {
		return m, true
	}
	row, ok := clicap.Lookup(op)
	if !ok || row.Tool == "" {
		return nil, false
	}
	for _, t := range mcp.Catalog() {
		if t["name"] != row.Tool {
			continue
		}
		in, _ := t["inputSchema"].(map[string]any)
		if in != nil {
			in["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		}
		return map[string]any{
			"operation":   op,
			"description": t["description"],
			"input":       in,
			"output":      map[string]any{"type": "object", "description": "The structured result of the tool, without schemaVersion and tool (they are in the envelope)."},
			"errors":      auditErrors,
			"examples":    []any{},
		}, true
	}
	return nil, false
}

// operations lists every operation that can be described, sorted.
func operations() []opInfo {
	seen := map[string]bool{}
	var out []opInfo
	add := func(op string) {
		if seen[op] {
			return
		}
		seen[op] = true
		if m, ok := describe(op); ok {
			desc, _ := m["description"].(string)
			out = append(out, opInfo{op, firstSentence(desc)})
		}
	}
	for _, op := range clicore.NativeOps() {
		add(op)
	}
	for _, r := range clicap.Rows() {
		if r.Tool != "" {
			add(r.Op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation < out[j].Operation })
	return out
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	if r := []rune(s); len(r) > 110 {
		s = string(r[:107]) + "..."
	}
	return s
}

func (st *state) schemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "schema [operation]",
		Short:       "List the operations, or describe one (its input, output and errors)",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{opAnnotation: "schema"},
		RunE: func(c *cobra.Command, args []string) error {
			meta := clicore.Meta{Coverage: "complete", Identifiers: "hidden"}
			if len(args) == 0 {
				ops := operations()
				return st.emit("schema", map[string]any{"operations": ops}, meta, func(w io.Writer) error {
					rows := make([][]string, len(ops))
					for i, o := range ops {
						rows[i] = []string{o.Operation, o.Description}
					}
					return clicore.WriteTable(w, []string{"OPERATION", "DESCRIPTION"}, rows)
				})
			}
			m, ok := describe(args[0])
			if !ok {
				return clicore.NewError(clicore.CodeUnknownOperation, "There is no operation with that id.", "Run 'unravis schema' to list the operations.")
			}
			return st.emit("schema", m, meta, func(w io.Writer) error {
				b, err := json.MarshalIndent(m, "", "  ")
				if err != nil {
					return err
				}
				for _, line := range strings.Split(string(b), "\n") {
					if _, err := io.WriteString(w, clicore.Printable(line)+"\n"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}
