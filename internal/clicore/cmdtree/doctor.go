package cmdtree

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// doctorData is `unravis doctor` (spec 05 3.6): what this command line is, where it sits, and what it finds of the app. It reads the
// locator file and asks hello; it reads no token, shows no pipe name and no case title, and makes no network call.
type doctorData struct {
	CLI        versionData   `json:"cli"`
	InstallDir string        `json:"installDir"`
	AppExe     doctorExe     `json:"appExe"`
	MCP        string        `json:"mcpCommand"`
	Locator    doctorLocator `json:"locator"`
	Backend    doctorBackend `json:"backend"`
}

type doctorExe struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

type doctorLocator struct {
	State      string `json:"state"` // running, not_running, stale, unusable_version or unknown
	Owner      string `json:"owner,omitempty"`
	PID        int    `json:"pid,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
}

type doctorBackend struct {
	Reachable bool       `json:"reachable"`
	Proto     *protoInfo `json:"proto,omitempty"`
	State     string     `json:"state,omitempty"` // ready, locked or updating, from a verified hello
	Error     string     `json:"error,omitempty"` // the code of the failure when it is not reachable and the app was found
}

// installDirOf is the install folder for the path of this program: the thin command line sits in `<install>\bin`, the app program in
// `<install>`.
func installDirOf(exe string) string {
	dir := filepath.Dir(exe)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		return filepath.Dir(dir)
	}
	return dir
}

func (st *state) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check this command line and its link to the running app",
		Long: "Says which command line this is, where the app program should be, and what the command line finds of the running app: whether it runs, " +
			"whether it answered a check of who it is, and its state. Nothing is changed and nothing leaves this PC. Not finding the app is an answer, not a failure.",
		Args:        cobra.NoArgs,
		Annotations: emitting("doctor"),
		RunE: func(c *cobra.Command, args []string) error {
			d := doctorData{CLI: st.versionInfo()}
			if d.CLI.Exe != "" {
				d.InstallDir = installDirOf(d.CLI.Exe)
				d.AppExe.Path = filepath.Join(d.InstallDir, "unravis.exe")
				if fi, err := os.Stat(d.AppExe.Path); err == nil && fi.Mode().IsRegular() { //nolint:gosec // the path is built from this program's own location
					d.AppExe.Exists = true
				}
				d.MCP = d.AppExe.Path + " --mcp"
			}
			sess, e := st.attach(st.ctx)
			switch {
			case e == nil:
				d.Locator = doctorLocator{State: "running", Owner: sess.Hello.Owner, PID: sess.Hello.PID, AppVersion: sess.Hello.AppVersion}
				d.Backend = doctorBackend{Reachable: true, Proto: &protoInfo{sess.Hello.Proto.Major, sess.Hello.Proto.Minor}, State: sess.Hello.State}
			case e.Code == clicore.CodeCanceled || e.Code == clicore.CodeTimeout:
				return e // the person stopped the check, or it ran out of time: that is not a finding
			case e.Code == clicore.CodeAppNotRunning:
				d.Locator = doctorLocator{State: "not_running"}
			case e.Code == clicore.CodeStaleLocator:
				d.Locator = doctorLocator{State: "stale"}
			case e.Code == clicore.CodeVersionIncompatible:
				d.Locator = doctorLocator{State: "unusable_version"}
				d.Backend.Error = string(e.Code)
			default:
				d.Locator = doctorLocator{State: "unknown"}
				d.Backend.Error = string(e.Code)
			}
			return st.emit("doctor", d, clicore.Meta{Coverage: "complete", Identifiers: "hidden"}, func(w io.Writer) error { return doctorTable(w, d) })
		},
	}
}

func doctorTable(w io.Writer, d doctorData) error {
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	rows := [][]string{
		{"command line", d.CLI.App}, {"command line file", d.CLI.Exe}, {"install folder", d.InstallDir},
		{"app program", d.AppExe.Path}, {"app program found", yes(d.AppExe.Exists)}, {"MCP command", d.MCP},
		{"app", d.Locator.State},
	}
	if d.Locator.Owner != "" {
		rows = append(rows, []string{"app owner", d.Locator.Owner}, []string{"app process", fmt.Sprint(d.Locator.PID)}, []string{"app version", d.Locator.AppVersion})
	}
	rows = append(rows, []string{"app answered", yes(d.Backend.Reachable)})
	if d.Backend.Proto != nil {
		rows = append(rows, []string{"protocol", fmt.Sprintf("%d.%d", d.Backend.Proto.Major, d.Backend.Proto.Minor)}, []string{"app state", d.Backend.State})
	}
	if d.Backend.Error != "" {
		rows = append(rows, []string{"problem", d.Backend.Error})
	}
	if err := clicore.WriteTable(w, []string{"CHECK", "RESULT"}, rows); err != nil {
		return err
	}
	if d.CLI.Version != "" && d.Locator.AppVersion != "" && d.CLI.Version != d.Locator.AppVersion {
		_, err := fmt.Fprintf(w, "\nThis command line is %s and the app is %s. Update both to the same version.\n", clicore.Printable(d.CLI.Version), clicore.Printable(d.Locator.AppVersion))
		return err
	}
	return nil
}
