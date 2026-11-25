package cmdtree

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func auditHelpSnapshot() string {
	st := &state{io: IO{Out: io.Discard, Err: io.Discard, Getenv: func(string) string { return "" }}}
	var paths []string
	var walk func(*cobra.Command, string)
	walk = func(c *cobra.Command, path string) {
		paths = append(paths, path)
		for _, sub := range c.Commands() {
			if !sub.Hidden {
				walk(sub, path+" "+sub.Name())
			}
		}
	}
	root := st.root()
	for _, c := range root.Commands() {
		if c.Name() == "audit" {
			walk(c, "unravis audit")
		}
	}
	sort.Strings(paths)

	var out strings.Builder
	for _, path := range paths {
		var buf bytes.Buffer
		st := &state{io: IO{Out: &buf, Err: &buf, Getenv: func(string) string { return "" }}}
		root := st.root()
		root.SetArgs(append(strings.Fields(path)[1:], "--help"))
		if _, err := root.ExecuteC(); err != nil {
			panic(fmt.Sprintf("help for %s: %v", path, err))
		}
		fmt.Fprintf(&out, "===== %s =====\n%s", path, strings.ReplaceAll(buf.String(), "\r\n", "\n"))
		if !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func TestSnapshotAuditHelp(t *testing.T) {
	const golden = "testdata/audit-help.golden"
	got := auditHelpSnapshot()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("no snapshot: run with UPDATE_GOLDEN=1 (%v)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("audit help changed. If it is meant, run: UPDATE_GOLDEN=1 go test ./internal/clicore/cmdtree -run SnapshotAuditHelp\n--- snapshot\n%s--- now\n%s", strings.ReplaceAll(string(want), "\r\n", "\n"), got)
	}
}
