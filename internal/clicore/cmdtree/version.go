package cmdtree

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/version"
)

// versionLine is `unravis --version`: the same text the app program prints.
func (st *state) versionLine() int {
	_, _ = fmt.Fprintln(st.io.Out, version.String())
	return 0
}

type protoInfo struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

type versionData struct {
	App       string    `json:"app"`
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	Date      string    `json:"date"`
	CLISchema string    `json:"cliSchema"`
	Proto     protoInfo `json:"proto"`
	Role      string    `json:"role"`
	Exe       string    `json:"exe"`
}

func (st *state) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "version",
		Short:       "Print the version of this command line",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{opAnnotation: "version"},
		RunE: func(c *cobra.Command, args []string) error {
			exe := ""
			f := st.io.Exe
			if f == nil {
				f = os.Executable
			}
			if p, err := f(); err == nil {
				exe = p
			}
			d := versionData{App: version.Short(), Version: version.Version, Commit: version.Commit, Date: version.Date,
				CLISchema: clicore.SchemaVersion, Proto: protoInfo{clicore.ProtoMajor, clicore.ProtoMinor}, Role: "cli", Exe: exe}
			return st.emit("version", d, clicore.Meta{Coverage: "complete", Identifiers: "hidden"}, func(w io.Writer) error {
				rows := [][]string{{"app", d.App}, {"version", d.Version}, {"commit", d.Commit}, {"date", d.Date}, {"cliSchema", d.CLISchema},
					{"proto", fmt.Sprintf("%d.%d", d.Proto.Major, d.Proto.Minor)}, {"role", d.Role}, {"exe", d.Exe}}
				return clicore.WriteTable(w, []string{"FIELD", "VALUE"}, rows)
			})
		},
	}
}
