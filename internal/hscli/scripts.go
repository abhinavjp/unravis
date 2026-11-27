package hscli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// promote and release run the PowerShell flows that already exist (tools/release/Promote-Develop.ps1 and Release.ps1).
// They are not registry actions: they change a repository and GitHub, not the CI store, and the scripts keep their
// own checks. What the command adds is the freeze: `hold check` runs first, and a freeze that covers the project
// stops it with exit 5. A hold store that cannot be read stops it with exit 2, because not knowing is not "free".

var (
	projectRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	repoRe      = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaRe       = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)
	versionRe   = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	branchRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
	requesterRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._@-]{0,63}$`)
)

// freezeCheck runs `hold check` and returns the exit code to stop with, or -1 to go on.
func (c *run) freezeCheck(project, stateDir string) int {
	args := []string{"hold", "check", "--project", project}
	if stateDir != "" {
		args = append(args, "--state-dir", stateDir)
	}
	switch code := c.d.Engine(c.ctx, args, c.out, c.err, c.environ); code {
	case ExitOK:
		return -1
	case ExitHeld:
		return ExitHeld
	default:
		_, _ = fmt.Fprintf(c.err, "%s: the freeze windows could not be checked, so nothing was started.\n", c.prog)
		return ExitUsage
	}
}

// scriptExit keeps the exit codes a script is allowed to give (0 to 5) and turns anything else into "failed".
func scriptExit(code int) int {
	if code >= 0 && code <= ExitHeld {
		return code
	}
	return ExitFailed
}

func (c *run) runScript(name string, args []string) int {
	script, err := findScript(c.d.Roots(), c.environ, name)
	if err != nil {
		return c.usageErr("%v", err)
	}
	return scriptExit(c.d.Script(c.ctx, script, args, c.out, c.err, c.environ))
}

func (c *run) checkValue(cmd, flagName, value string, re *regexp.Regexp) bool {
	if value == "" || re.MatchString(value) {
		return true
	}
	c.usageErr("%s: --%s %q is not a valid value", cmd, flagName, value)
	return false
}

// cmdPromote is `promote [develop]`: Promote-Develop.ps1, which promotes develop (or develop up to a commit) to master
// with one pull request. develop is the only step there is today.
func cmdPromote(c *run, args []string) int {
	pos, flags := positionals(args, "project", "up-to", "wait-minutes", "ci-wait-minutes", "repo", "state-dir")
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	project := fs.String("project", defaultProject, "")
	upTo := fs.String("up-to", "", "")
	repo := fs.String("repo", "", "")
	stateDir := fs.String("state-dir", "", "")
	dry := fs.Bool("dry-run", false, "")
	noMerge := fs.Bool("no-merge", false, "")
	skipNotes := fs.Bool("skip-notes-check", false, "")
	wait := fs.Int("wait-minutes", 0, "")
	ciWait := fs.Int("ci-wait-minutes", 0, "")
	if err := fs.Parse(flags); err != nil {
		return c.usageErr("promote: %v (see: %s help)", err, c.prog)
	}
	if len(pos) > 1 || (len(pos) == 1 && pos[0] != "develop") {
		return c.usageErr("promote: the only step is develop (develop to master); got %q", strings.Join(pos, " "))
	}
	if !c.checkValue("promote", "project", *project, projectRe) || !c.checkValue("promote", "up-to", *upTo, shaRe) || !c.checkValue("promote", "repo", *repo, repoRe) {
		return ExitUsage
	}
	if *wait < 0 || *ciWait < 0 {
		return c.usageErr("promote: the minutes cannot be negative")
	}
	if code := c.freezeCheck(*project, *stateDir); code >= 0 {
		return code
	}
	sargs := []string{"-Project", *project}
	if *upTo != "" {
		sargs = append(sargs, "-UpTo", *upTo)
	}
	if *repo != "" {
		sargs = append(sargs, "-Repo", *repo)
	}
	if *stateDir != "" {
		sargs = append(sargs, "-CiStore", *stateDir)
	}
	if *dry {
		sargs = append(sargs, "-DryRun")
	}
	if *noMerge {
		sargs = append(sargs, "-NoMerge")
	}
	if *skipNotes {
		sargs = append(sargs, "-SkipNotesCheck")
	}
	if *wait > 0 {
		sargs = append(sargs, "-WaitMinutes", strconv.Itoa(*wait))
	}
	if *ciWait > 0 {
		sargs = append(sargs, "-CiWaitMinutes", strconv.Itoa(*ciWait))
	}
	return c.runScript("Promote-Develop.ps1", sargs)
}

// cmdRelease is `release <rc|stable>`: Release.ps1, which cuts the release. `release --status` only reads, so the
// freeze does not stop it. The passphrase of the signing key is never read here: the script asks for it itself.
func cmdRelease(c *run, args []string) int {
	pos, flags := positionals(args, "project", "version", "requester", "wait-minutes", "repo", "branch", "state-dir")
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	project := fs.String("project", defaultProject, "")
	version := fs.String("version", "", "")
	requester := fs.String("requester", "", "")
	repo := fs.String("repo", "", "")
	branch := fs.String("branch", "", "")
	stateDir := fs.String("state-dir", "", "")
	wait := fs.Int("wait-minutes", 0, "")
	status := fs.Bool("status", false, "")
	asJSON := fs.Bool("json", false, "")
	yes := fs.Bool("yes", false, "")
	dry := fs.Bool("dry-run", false, "")
	checkOnly := fs.Bool("check-only", false, "")
	if err := fs.Parse(flags); err != nil {
		return c.usageErr("release: %v (see: %s help)", err, c.prog)
	}
	if !c.checkValue("release", "project", *project, projectRe) || !c.checkValue("release", "version", *version, versionRe) ||
		!c.checkValue("release", "repo", *repo, repoRe) || !c.checkValue("release", "branch", *branch, branchRe) ||
		!c.checkValue("release", "requester", *requester, requesterRe) {
		return ExitUsage
	}
	if *wait < 0 {
		return c.usageErr("release: the minutes cannot be negative")
	}
	if *status {
		if len(pos) != 0 {
			return c.usageErr("release: --status takes no channel")
		}
		sargs := []string{"-Status"}
		if *asJSON {
			sargs = append(sargs, "-Json")
		}
		if *repo != "" {
			sargs = append(sargs, "-Repo", *repo)
		}
		return c.runScript("Release.ps1", sargs)
	}
	if len(pos) != 1 || (pos[0] != "rc" && pos[0] != "stable") {
		return c.usageErr("release: say which channel: %s release rc  or  %s release stable", c.prog, c.prog)
	}
	if code := c.freezeCheck(*project, *stateDir); code >= 0 {
		return code
	}
	sargs := []string{"-Channel", pos[0]}
	if *version != "" {
		sargs = append(sargs, "-Version", *version)
	}
	if *requester != "" {
		sargs = append(sargs, "-Requester", *requester)
	}
	if *repo != "" {
		sargs = append(sargs, "-Repo", *repo)
	}
	if *branch != "" {
		sargs = append(sargs, "-Branch", *branch)
	}
	if *wait > 0 {
		sargs = append(sargs, "-WaitMinutes", strconv.Itoa(*wait))
	}
	if *yes {
		sargs = append(sargs, "-Yes")
	}
	if *dry || *checkOnly {
		sargs = append(sargs, "-CheckOnly")
	}
	return c.runScript("Release.ps1", sargs)
}

// ---------------------------------------------------------------- finding and running the scripts

// repoEnv names the folder of the Unravis checkout when the program is not run from inside one.
const repoEnv = "HOMESTRETCH_REPO"

func defaultRoots() []string {
	var roots []string
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd)
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	return roots
}

// findScript finds tools/release/<name> in the checkout: the folder in HOMESTRETCH_REPO, else the first folder at
// or above one of the roots that has it.
func findScript(roots, environ []string, name string) (string, error) {
	rel := filepath.Join("tools", "release", name)
	if dir := envValue(environ, repoEnv); dir != "" {
		p := filepath.Join(dir, rel)
		if isFile(p) {
			return p, nil
		}
		return "", fmt.Errorf("%s is set to %q, but %s is not in it", repoEnv, dir, rel)
	}
	for _, root := range roots {
		dir, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		for i := 0; i < 16; i++ {
			if p := filepath.Join(dir, rel); isFile(p) {
				return p, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("could not find %s: run this from inside the Unravis checkout, or set %s to its folder", rel, repoEnv)
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// envValue returns the last value of name in environ ("" when absent); names compare without regard to case, as on Windows.
func envValue(environ []string, name string) string {
	for i := len(environ) - 1; i >= 0; i-- {
		k, v, ok := cutEnv(environ[i])
		if ok && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func cutEnv(s string) (k, v string, ok bool) {
	for i := 1; i < len(s); i++ { // a leading '=' belongs to the name (Windows drive variables)
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// runPowerShell runs one script in PowerShell 7 (pwsh), else Windows PowerShell, with this terminal for input.
func runPowerShell(ctx context.Context, script string, args []string, stdout, stderr io.Writer, environ []string) int {
	exe, err := exec.LookPath("pwsh")
	if err != nil {
		exe, err = exec.LookPath("powershell")
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "homestretch: PowerShell was not found (looked for pwsh and powershell), so the script cannot run.")
		return ExitUsage
	}
	argv := append([]string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}, args...)
	cmd := exec.CommandContext(ctx, exe, argv...) //nolint:gosec // the program is PowerShell from PATH; the script is the repository's own, and every argument was checked above
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, stdout, stderr, environ
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		_, _ = fmt.Fprintf(stderr, "homestretch: PowerShell could not be started: %v\n", err)
		return ExitUsage
	}
	return ExitOK
}
