// Command unravis-ci runs one GitHub Actions workflow of this repository on this PC, with a strict
// supported subset, and lints the workflows for anything it cannot run.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/localci"
)

const version = "0.1.0"

// Exit codes.
const (
	exitOK          = 0
	exitFailed      = 1
	exitUsage       = 2
	exitUnsupported = 3
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(realMain(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Environ()))
}

func realMain(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "version", "--version":
		_, _ = fmt.Fprintln(stdout, "unravis-ci", version)
		return exitOK
	case "lint":
		return cmdLint(args[1:], stdout, stderr)
	case "run":
		return cmdRun(ctx, args[1:], stdout, stderr, environ)
	case "run-pr":
		return cmdRunPR(ctx, args[1:], stdout, stderr, environ)
	case "runs":
		return cmdRuns(args[1:], stdout, stderr, environ)
	case "log":
		return cmdLog(args[1:], stdout, stderr, environ)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	}
	_, _ = fmt.Fprintf(stderr, "unravis-ci: unknown command %q\n", args[0])
	usage(stderr)
	return exitUsage
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `usage:
  unravis-ci lint [--workflows dir] [--allow a.yml,b.yml]
  unravis-ci run <workflow.yml> --event push|pull_request|workflow_dispatch|schedule [--local DIR] [--state DIR]
        [--ref REF] [--sha SHA] [--base-ref B] [--head-ref H] [--input k=v]... [--var k=v]... [--job id]...
        [--slots N] [--config FILE] [--allow-env NAME]... [--dry-run]
  unravis-ci run-pr <number> --repo owner/name [--state DIR] [--workflow-for base=file.yml]... [--protect PATH]... [--trusted-base B]... [--allow-env NAME]... [--keep] [--slots N]
  unravis-ci runs [--state DIR] [--limit N]
  unravis-ci log <runid> [job|_checkout|_trust] [--state DIR]
  unravis-ci version
exit codes: 0 pass, 1 workflow failed, 2 usage or I/O error, 3 unsupported
`)
}

// multiFlag is a repeatable flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// parseInterspersed parses flags that may come after positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// ---------------------------------------------------------------- lint

func cmdLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("workflows", filepath.Join(".github", "workflows"), "folder with the workflow files")
	allow := fs.String("allow", "", "comma separated file names that stay on GitHub")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) > 0 {
		_, _ = fmt.Fprintln(stderr, "lint: unexpected argument", pos[0])
		return exitUsage
	}
	kept := map[string]bool{}
	for _, f := range strings.Split(*allow, ",") {
		if f = strings.TrimSpace(f); f != "" {
			kept[f] = true
		}
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "lint:", err)
		return exitUsage
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	code := exitOK
	for _, name := range names {
		path := filepath.ToSlash(filepath.Join(*dir, name))
		data, err := os.ReadFile(filepath.Join(*dir, name)) //nolint:gosec // path built by the tool/test itself, reading it is the point
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "lint:", err)
			return exitUsage
		}
		wf, uns, perr := localci.ParseWorkflow(path, data)
		_ = wf
		switch {
		case kept[name]:
			_, _ = fmt.Fprintf(stdout, "%s: kept on GitHub\n", name)
			for _, u := range uns {
				_, _ = fmt.Fprintf(stdout, "  (not run locally) %s\n", u)
			}
		case perr != nil:
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, perr)
			for _, u := range uns {
				_, _ = fmt.Fprintln(stdout, u)
			}
			return exitUsage
		case len(uns) > 0:
			_, _ = fmt.Fprintf(stdout, "%s: not supported\n", name)
			for _, u := range uns {
				_, _ = fmt.Fprintln(stdout, u)
			}
			code = exitUnsupported
		default:
			_, _ = fmt.Fprintf(stdout, "%s: ok\n", name)
		}
	}
	for f := range kept {
		found := false
		for _, n := range names {
			found = found || n == f
		}
		if !found {
			_, _ = fmt.Fprintf(stderr, "lint: --allow names %s, which is not in %s\n", f, *dir)
			return exitUsage
		}
	}
	return code
}

// ---------------------------------------------------------------- run

func cmdRun(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	event := fs.String("event", "", "push, pull_request, workflow_dispatch or schedule")
	local := fs.String("local", "", "the checkout (default: current directory)")
	state := fs.String("state", "", "folder for logs and run.json")
	ref := fs.String("ref", "", "git ref (default refs/heads/<current branch>)")
	sha := fs.String("sha", "", "commit sha (default HEAD of the checkout)")
	baseRef := fs.String("base-ref", "", "base branch of a pull request")
	headRef := fs.String("head-ref", "", "head branch of a pull request")
	slots := fs.Int("slots", 2, "jobs at once")
	cfgPath := fs.String("config", "", "local-ci.yml (default .github/local-ci.yml of the checkout, when present)")
	dry := fs.Bool("dry-run", false, "print the plan and run nothing")
	var inputs, vars, only, allowEnv multiFlag
	fs.Var(&inputs, "input", "workflow_dispatch input k=v (repeatable)")
	fs.Var(&vars, "var", "vars context value k=v (repeatable)")
	fs.Var(&only, "job", "run only this job id (repeatable)")
	fs.Var(&allowEnv, "allow-env", "NAME of an environment variable steps may see (repeatable; the default is a short allow-list)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "run: expected exactly one workflow file")
		usage(stderr)
		return exitUsage
	}
	switch *event {
	case "push", "pull_request", "workflow_dispatch", "schedule":
	default:
		_, _ = fmt.Fprintf(stderr, "run: --event must be push, pull_request, workflow_dispatch or schedule (got %q)\n", *event)
		return exitUsage
	}
	inMap, err := kvList(inputs)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run: --input:", err)
		return exitUsage
	}
	varMap, err := kvList(vars)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run: --var:", err)
		return exitUsage
	}
	workdir := *local
	if workdir == "" {
		workdir = "."
	}
	workdir, err = filepath.Abs(workdir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run:", err)
		return exitUsage
	}
	if st, err := os.Stat(workdir); err != nil || !st.IsDir() {
		_, _ = fmt.Fprintf(stderr, "run: --local %s is not a folder\n", workdir)
		return exitUsage
	}
	wfPath := pos[0]
	if _, err := os.Stat(wfPath); err != nil && !filepath.IsAbs(wfPath) { //nolint:gosec // the workflow path comes from the command line by design
		if alt := filepath.Join(workdir, wfPath); fileExists(alt) {
			wfPath = alt
		}
	}
	data, err := os.ReadFile(wfPath) //nolint:gosec // path built by the tool/test itself, reading it is the point
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run:", err)
		return exitUsage
	}
	wf, uns, perr := localci.ParseWorkflow(filepath.ToSlash(wfPath), data)
	if perr != nil {
		_, _ = fmt.Fprintln(stderr, "run:", perr)
		return exitUsage
	}
	if len(uns) > 0 {
		_, _ = fmt.Fprintf(stderr, "run: %s has items this runner does not support; nothing was run:\n", wfPath)
		for _, u := range uns {
			_, _ = fmt.Fprintln(stderr, " ", u)
		}
		return exitUnsupported
	}
	if len(wf.On) > 0 {
		if _, ok := wf.On[*event]; !ok {
			_, _ = fmt.Fprintf(stderr, "run: %s does not run on %s\n", wfPath, *event)
			return exitUsage
		}
	}
	cfg, err := loadConfig(*cfgPath, workdir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run:", err)
		return exitUsage
	}
	if *sha == "" {
		*sha, _ = gitOut(workdir, "rev-parse", "HEAD")
	}
	if *ref == "" {
		if b, _ := gitOut(workdir, "rev-parse", "--abbrev-ref", "HEAD"); b != "" && b != "HEAD" {
			*ref = "refs/heads/" + b
		}
	}
	stateDir := *state
	now := time.Now()
	if stateDir == "" {
		stateDir = defaultStateDir(environ, now, filepath.Base(wfPath))
	}
	gh := githubContext(workdir, wf, *event, *ref, *sha, *baseRef, *headRef, inMap, now, environ)
	opts := localci.RunOptions{
		Workflow: wf, Config: cfg, Workdir: workdir, StateDir: stateDir, GitHub: gh, Inputs: inMap, Vars: varMap,
		Env: scrubEnv(environ, allowEnv), Only: only, Slots: *slots, Log: stdout, DryRun: *dry,
		Redact: newRedactor(secrets(environ)),
	}
	if !*dry {
		_, _ = fmt.Fprintf(stdout, "running %s (%s) in %s\nstate: %s\n", wf.Name, *event, workdir, stateDir)
	}
	res, err := localci.Run(ctx, opts)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run:", err)
		if strings.Contains(err.Error(), "unsupported:") {
			return exitUnsupported
		}
		return exitUsage
	}
	if *dry {
		return exitOK
	}
	if werr := localci.WriteRunRecord(stateDir, res); werr != nil {
		_, _ = fmt.Fprintln(stderr, "run: could not write run.json:", werr)
	}
	printSummary(stdout, res)
	if res.Conclusion == "success" {
		return exitOK
	}
	return exitFailed
}

func fileExists(p string) bool {
	st, err := os.Stat(p) //nolint:gosec // the path comes from the command line by design
	return err == nil && !st.IsDir()
}

func kvList(items []string) (map[string]string, error) {
	m := map[string]string{}
	for _, it := range items {
		k, v, ok := strings.Cut(it, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%q is not k=v", it)
		}
		m[k] = v
	}
	return m, nil
}

func loadConfig(path, workdir string) (*localci.LocalConfig, error) {
	if path == "" {
		path = filepath.Join(workdir, ".github", "local-ci.yml")
		if !fileExists(path) {
			return localci.ParseLocalConfig(nil)
		}
	} else if !fileExists(path) && !filepath.IsAbs(path) && fileExists(filepath.Join(workdir, path)) {
		path = filepath.Join(workdir, path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path built by the tool/test itself, reading it is the point
	if err != nil {
		return nil, err
	}
	return localci.ParseLocalConfig(data)
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // fixed binary, controlled arguments
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func getenv(environ []string, key string) string {
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func defaultStateDir(environ []string, now time.Time, wfFile string) string {
	base := getenv(environ, "LOCALAPPDATA")
	if base == "" {
		base = filepath.Join(os.TempDir(), "Unravis-local")
	}
	name := strings.TrimSuffix(strings.TrimSuffix(wfFile, ".yml"), ".yaml")
	return filepath.Join(base, "Unravis", "ci", "runs", now.Format("20060102-150405")+"-"+name)
}

func githubContext(workdir string, wf *localci.Workflow, event, ref, sha, baseRef, headRef string, inputs map[string]string, now time.Time, environ []string) map[string]any {
	refName := ref
	for _, p := range []string{"refs/heads/", "refs/tags/"} {
		refName = strings.TrimPrefix(refName, p)
	}
	repo := "local/" + filepath.Base(workdir)
	if u, err := gitOut(workdir, "remote", "get-url", "origin"); err == nil && u != "" {
		u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
		u = strings.ReplaceAll(u, ":", "/")
		parts := strings.Split(u, "/")
		if len(parts) >= 2 {
			repo = parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
	}
	actor := firstNonEmpty(getenv(environ, "USERNAME"), getenv(environ, "USER"), "local")
	ev := map[string]any{}
	if event == "workflow_dispatch" {
		in := map[string]any{}
		for k, v := range inputs {
			in[k] = v
		}
		ev["inputs"] = in
	}
	if event == "pull_request" {
		base := map[string]any{"ref": baseRef}
		if baseRef != "" {
			for _, cand := range []string{"origin/" + baseRef, baseRef} {
				if s, err := gitOut(workdir, "rev-parse", "--verify", "-q", cand); err == nil && s != "" {
					base["sha"] = s
					break
				}
			}
		}
		ev["pull_request"] = map[string]any{"base": base, "head": map[string]any{"ref": headRef, "sha": sha}}
	}
	return map[string]any{
		"event_name": event, "ref": ref, "ref_name": refName, "sha": sha, "base_ref": baseRef, "head_ref": headRef,
		"repository": repo, "workspace": workdir, "run_id": strconv.FormatInt(now.Unix(), 10), "run_number": "1", "run_attempt": "1",
		"workflow": wf.Name, "actor": actor, "token": "", "event": ev,
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// allowedEnvNames are the variables steps keep (compared upper case). Everything else is dropped unless the
// caller adds it with --allow-env: an allow-list, because a deny-list can never know the name of the next secret.
var allowedEnvNames = map[string]bool{
	// locating programs and the system
	"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "SYSTEMDRIVE": true, "WINDIR": true, "COMSPEC": true,
	"OS": true, "NUMBER_OF_PROCESSORS": true, "PROCESSOR_ARCHITECTURE": true, "PROCESSOR_IDENTIFIER": true,
	"PROCESSOR_LEVEL": true, "PROCESSOR_REVISION": true, "COMPUTERNAME": true, "USERDOMAIN": true, "USERNAME": true,
	"USER": true, "LOGNAME": true, "SHELL": true, "TERM": true, "TZ": true, "LANG": true, "LANGUAGE": true,
	"PROGRAMFILES": true, "PROGRAMFILES(X86)": true, "PROGRAMW6432": true, "COMMONPROGRAMFILES": true,
	"COMMONPROGRAMFILES(X86)": true, "COMMONPROGRAMW6432": true, "PROGRAMDATA": true, "ALLUSERSPROFILE": true,
	"PUBLIC": true, "DRIVERDATA": true,
	// the profile and temp folders (the runner's own fetch needs the user's; steps get a throw-away profile)
	"HOME": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true, "APPDATA": true, "LOCALAPPDATA": true,
	"TEMP": true, "TMP": true, "TMPDIR": true,
	// toolchains and their caches (no credentials)
	"GOROOT": true, "GOPATH": true, "GOCACHE": true, "GOMODCACHE": true, "GOTOOLCHAIN": true, "GOPROXY": true,
	"GOSUMDB": true, "GOFLAGS": true, "NVM_HOME": true, "NVM_SYMLINK": true, "JAVA_HOME": true, "CARGO_HOME": true,
	"RUSTUP_HOME": true,
	// network: a proxy the machine needs (a proxy URL with a password is masked in logs by its name only when it
	// matches the secret rules, so keep credentials out of it)
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
}

// allowedEnvPrefixes are name prefixes kept as well.
var allowedEnvPrefixes = []string{"LC_", "XDG_"}

// scrubEnv keeps only the variables a step may see: the allow-list above plus the names in extra
// (--allow-env). A name that looks like a secret is dropped even when the allow-list would keep it, except when
// the caller names it in extra on purpose.
func scrubEnv(environ, extra []string) []string {
	allow := map[string]bool{}
	for _, n := range extra {
		allow[strings.ToUpper(strings.TrimSpace(n))] = true
	}
	var out []string
	for _, kv := range environ {
		name := envName(kv)
		u := strings.ToUpper(name)
		switch {
		case allow[u]:
		case secretName(name):
			continue
		case strings.HasPrefix(kv, "="): // Windows per-drive folders such as =C:
		case allowedEnvNames[u]:
		case hasAnyPrefix(u, allowedEnvPrefixes):
		default:
			continue
		}
		out = append(out, kv)
	}
	return out
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// envName is the name of an "NAME=value" entry. Windows has entries such as "=C:=C:\" whose name starts with "=".
func envName(kv string) string {
	if kv == "" {
		return ""
	}
	if i := strings.Index(kv[1:], "="); i >= 0 {
		return kv[:i+1]
	}
	return kv
}

// minSecretLen is the shortest value that is masked: shorter ones ("1", "true") would blank ordinary text.
const minSecretLen = 6

// secretValues returns the values of the variables whose names look like secrets (taken before the scrub),
// longest first.
func secretValues(environ []string) []string {
	var vals []string
	for _, kv := range environ {
		if kv == "" {
			continue
		}
		i := strings.Index(kv[1:], "=")
		if i < 0 {
			continue
		}
		if secretName(kv[:i+1]) {
			vals = append(vals, kv[i+2:])
		}
	}
	return longestFirst(vals)
}

// longestFirst drops short and repeated values and orders the rest longest first, so a secret that contains
// another one is masked whole.
func longestFirst(vals []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vals {
		if len(v) >= minSecretLen && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return len(out[a]) > len(out[b]) })
	return out
}

// ghAuthToken asks the `gh` login of the runner's user for its token, so that the value is masked even when no
// environment variable holds it. Failure (no gh, not logged in) gives "". Tests replace it.
var ghAuthToken = func(environ []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	cmd.Env = environ
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// secrets is everything the runner masks: the secret-looking variables and the gh login token.
func secrets(environ []string) []string {
	return longestFirst(append(secretValues(environ), ghAuthToken(environ)))
}

// newRedactor returns the one redactor the runner applies to every sink (step logs, console, summary.md,
// run.json): it replaces each secret value by ***.
func newRedactor(vals []string) func(string) string {
	return func(s string) string {
		for _, v := range vals {
			s = strings.ReplaceAll(s, v, "***")
		}
		return s
	}
}

// secretName reports whether a variable name looks like it holds a credential.
func secretName(name string) bool {
	u := strings.ToUpper(name)
	switch {
	case strings.HasSuffix(u, "_TOKEN"), u == "GH_TOKEN", u == "GITHUB_TOKEN",
		strings.Contains(u, "SECRET"), strings.Contains(u, "PASSWORD"), strings.Contains(u, "PASSWD"),
		strings.Contains(u, "PASSPHRASE"), strings.Contains(u, "CREDENTIAL"),
		strings.Contains(u, "APIKEY"), strings.Contains(u, "API_KEY"), strings.Contains(u, "PRIVATE_KEY"),
		strings.Contains(u, "ACCESS_KEY"), strings.Contains(u, "AUTH_TOKEN"),
		strings.HasPrefix(u, "PROTON"), strings.HasPrefix(u, "SIGNPATH"), strings.HasPrefix(u, "UNRAVIS_RELEASE"):
		return true
	}
	return false
}

func printSummary(w io.Writer, res *localci.RunResult) {
	_, _ = fmt.Fprintf(w, "\n%-50s %-16s %s\n", "JOB", "STATUS", "TIME")
	for _, j := range res.Jobs {
		d := ""
		if j.Status != "skipped" && j.Status != "not_run_locally" {
			d = j.End.Sub(j.Start).Round(time.Second).String()
		}
		_, _ = fmt.Fprintf(w, "%-50s %-16s %s\n", j.Label, j.Status, d)
		if j.Reason != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", j.Reason)
		}
		if j.Status == "failure" || j.Status == "cancelled" {
			for _, s := range j.Steps {
				if s.Conclusion == "failure" || s.Conclusion == "cancelled" || s.Outcome == "failure" {
					_, _ = fmt.Fprintf(w, "    step %d %s: %s", s.Index, s.Name, s.Conclusion)
					if s.Err != "" {
						_, _ = fmt.Fprintf(w, " (%s)", s.Err)
					}
					_, _ = fmt.Fprintf(w, "\n      log: %s\n", s.LogFile)
				}
			}
		}
	}
	_, _ = fmt.Fprintf(w, "\nresult: %s\n", res.Conclusion)
}
