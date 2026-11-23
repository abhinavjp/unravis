package cmdtree

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

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

// doctorAttachWait is how long doctor waits for the app before it reports what it has: a busy pipe or an app that does not answer is a
// finding ("unknown", TIMEOUT), not a lost report. The command's own --timeout still ends the command as an error.
var doctorAttachWait = 5 * time.Second

// appProgram finds the install folder and the app program for the path of this program, the way the attach does: symbolic links and
// short names are resolved first. A thin command line that sits in the install folder itself is not the app program.
func appProgram(exe string) (installDir string, app doctorExe) {
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	installDir = clicore.InstallDir(exe)
	app.Path = filepath.Join(installDir, "unravis.exe")
	if !filepath.IsAbs(exe) {
		return installDir, app
	}
	fi, err := os.Stat(app.Path)
	if err != nil || !fi.Mode().IsRegular() {
		return installDir, app
	}
	if self, err := os.Stat(exe); err == nil && os.SameFile(self, fi) {
		return installDir, app // this very file: the command line is not the app
	}
	app.Exists = true
	return installDir, app
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
			if st.opts.profile != "" || st.io.Getenv("UNRAVIS_PROFILE") != "" {
				return usage("Automation profiles are not available in this version of the command line.")
			}
			d := doctorData{CLI: st.versionInfo()}
			if d.CLI.Exe != "" {
				d.InstallDir, d.AppExe = appProgram(d.CLI.Exe)
				d.MCP = d.AppExe.Path + " --mcp"
			}
			ctx, cancel := context.WithTimeout(st.ctx, doctorAttachWait)
			sess, e := st.attach(ctx)
			cancel()
			switch {
			case e == nil:
				d.Locator = doctorLocator{State: "running", Owner: sess.Hello.Owner, PID: sess.Hello.PID, AppVersion: sess.Hello.AppVersion}
				d.Backend = doctorBackend{Reachable: true, Proto: &protoInfo{sess.Hello.Proto.Major, sess.Hello.Proto.Minor}, State: sess.Hello.State}
			case st.ctx.Err() != nil:
				return e // the person stopped the check, or the command ran out of time: that is not a finding
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
	if d.CLI.Version != "dev" && d.CLI.Version != "" && d.Locator.AppVersion != "" && d.CLI.Version != d.Locator.AppVersion {
		_, err := fmt.Fprintf(w, "\nThis command line is %s and the app is %s. Update both to the same version.\n", clicore.Printable(d.CLI.Version), clicore.Printable(d.Locator.AppVersion))
		return err
	}
	return nil
}
