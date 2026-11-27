package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/localci"
)

// Project defaults of this repository's CI. They live here, in the command, and nowhere in internal/localci:
// another project passes its own with --workflow-for, --protect and --state.
var (
	defaultWorkflowFor = map[string]string{"develop": "fast-gate.yml", "master": "ci.yml"}
	defaultProtect     = []string{"tools/ci"}
)

// newHost builds the git host client. Tests replace it with a fake.
var newHost = func(environ []string) localci.Host { return &localci.GHHost{Env: environ} }

// newClaimHost builds the store of claims and agent records the PCs share. Tests replace it with a fake.
var newClaimHost = func(environ []string) localci.ClaimHost {
	return localci.NewGHClaimHost(&localci.GHHost{Env: environ})
}

func defaultStoreDir(environ []string) string {
	base := getenv(environ, "LOCALAPPDATA")
	if base == "" {
		base = filepath.Join(os.TempDir(), "Unravis-local")
	}
	return filepath.Join(base, "Unravis", "ci", "store")
}

// ---------------------------------------------------------------- run-pr

func cmdRunPR(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("run-pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "owner/name of the repository (required)")
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	origin := fs.String("origin", "", "URL or path the mirror fetches from (default: the host's clone URL of --repo)")
	workflow := fs.String("workflow", "", "run this workflow file whatever the base branch is")
	keep := fs.Bool("keep", false, "keep the worktree after the run")
	slots := fs.Int("slots", 2, "jobs at once")
	trustOwner := fs.Bool("trust-owner-branch", false, "for a run you start by hand: do not require a GitHub login on every commit of your own pull request (the check of who pushed to the branch still runs, and a branch anybody else pushed to still runs from the base)")
	var wfFor, protect, vars, trustedBase, allowEnv multiFlag
	fs.Var(&wfFor, "workflow-for", "base=file.yml: the workflow that gates pull requests into base (repeatable; replaces the defaults)")
	fs.Var(&protect, "protect", "extra path read from the base branch for pull requests of non-owners (repeatable; added to the defaults)")
	fs.Var(&vars, "var", "vars context value k=v (repeatable)")
	fs.Var(&trustedBase, "trusted-base", "extra base branch a pull request of a non-owner may run into (repeatable; the bases of --workflow-for are always trusted)")
	fs.Var(&allowEnv, "allow-env", "NAME of an environment variable steps may see (repeatable; the default is a short allow-list)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	var number int
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "run-pr: expected exactly one pull request number")
		usage(stderr)
		return exitUsage
	}
	if _, err := fmt.Sscanf(pos[0], "%d", &number); err != nil || number <= 0 || fmt.Sprint(number) != pos[0] {
		_, _ = fmt.Fprintf(stderr, "run-pr: %q is not a pull request number\n", pos[0])
		return exitUsage
	}
	if *repo == "" {
		_, _ = fmt.Fprintln(stderr, "run-pr: --repo owner/name is required")
		return exitUsage
	}
	workflows := defaultWorkflowFor
	if len(wfFor) > 0 {
		workflows = map[string]string{}
		for _, kv := range wfFor {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" || v == "" {
				_, _ = fmt.Fprintf(stderr, "run-pr: --workflow-for wants base=file.yml (got %q)\n", kv)
				return exitUsage
			}
			workflows[k] = v
		}
	}
	varMap, err := kvList(vars)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-pr: --var:", err)
		return exitUsage
	}
	dir := *state
	if dir == "" {
		dir = defaultStoreDir(environ)
	}
	store, err := localci.OpenStore(dir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-pr:", err)
		return exitUsage
	}
	redact := newRedactor(secrets(environ))
	res, err := localci.RunPR(ctx, localci.PROptions{
		Store: store, Host: newHost(environ), Repo: *repo, Number: number, OriginURL: *origin,
		Workflows: workflows, Workflow: *workflow, Protect: append(append([]string{}, defaultProtect...), protect...),
		TrustOwnerBranch: *trustOwner, Keep: *keep, Env: scrubEnv(environ, allowEnv), Vars: varMap, Slots: *slots,
		TrustedBases: trustedBases(workflows, trustedBase), Redact: redact, Log: stdout,
	})
	if res != nil && res.Run != nil {
		printSummary(stdout, res.Run)
		_, _ = fmt.Fprintf(stdout, "run id: %s\nstore:  %s\nlogs:   unravis-ci log %s [job]\n", res.RunID, store.Dir, res.RunID)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-pr:", redact(err.Error()))
		if strings.Contains(err.Error(), "unsupported:") {
			return exitUnsupported
		}
		return exitUsage
	}
	if res.Run.Conclusion == "success" {
		return exitOK
	}
	return exitFailed
}

// trustedBases are the bases whose own workflow decides how pull requests into them run: the bases of the
// workflow map plus the ones named with --trusted-base.
func trustedBases(workflows map[string]string, extra []string) []string {
	var out []string
	for b := range workflows {
		out = append(out, b)
	}
	sort.Strings(out)
	return append(out, extra...)
}

// ---------------------------------------------------------------- runs

// runJSON is one run of `runs --json`: the index entry (run_id, workflow, event, repo, pr, conclusion, reason, start,
// end, ...) plus the length of the run, which the table shows as TIME. The field names are part of the command's
// interface; add fields, do not rename or drop them.
type runJSON struct {
	localci.IndexEntry
	DurationSeconds int64 `json:"duration_seconds"` // 0 when the run has no start or end
}

func runSeconds(e localci.IndexEntry) int64 {
	if e.End.IsZero() || e.Start.IsZero() {
		return 0
	}
	return int64(e.End.Sub(e.Start).Round(time.Second) / time.Second)
}

func cmdRuns(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	limit := fs.Int("limit", 20, "show the latest N runs (0 = all)")
	asJSON := fs.Bool("json", false, "print the runs as a JSON array (see runJSON), oldest first like the table")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "runs: takes no arguments")
		}
		return exitUsage
	}
	dir := *state
	if dir == "" {
		dir = defaultStoreDir(environ)
	}
	store := &localci.Store{Dir: dir}
	entries, err := store.ReadIndex()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "runs:", err)
		return exitUsage
	}
	if *limit > 0 && len(entries) > *limit {
		entries = entries[len(entries)-*limit:]
	}
	if *asJSON {
		rows := make([]runJSON, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, runJSON{IndexEntry: e, DurationSeconds: runSeconds(e)})
		}
		return writeJSON("runs", stdout, stderr, rows)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintf(stdout, "no runs in %s\n", dir)
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "%-40s %-10s %-6s %-14s %-8s %s\n", "RUN ID", "RESULT", "PR", "WORKFLOW", "TIME", "REASON")
	for _, e := range entries {
		pr := ""
		if e.PR > 0 {
			pr = fmt.Sprintf("#%d", e.PR)
		}
		d := ""
		if !e.End.IsZero() && !e.Start.IsZero() {
			d = e.End.Sub(e.Start).Round(time.Second).String()
		}
		_, _ = fmt.Fprintf(stdout, "%-40s %-10s %-6s %-14s %-8s %s\n", e.RunID, e.Conclusion, pr, e.Workflow, d, e.Reason)
	}
	return exitOK
}

// ---------------------------------------------------------------- log

func cmdLog(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) < 1 || len(pos) > 2 {
		_, _ = fmt.Fprintln(stderr, "log: expected <runid> [job]")
		usage(stderr)
		return exitUsage
	}
	dir := *state
	if dir == "" {
		dir = defaultStoreDir(environ)
	}
	store := &localci.Store{Dir: dir}
	if len(pos) == 1 {
		return listLogs(store, pos[0], stdout, stderr)
	}
	parts, err := store.JobLogs(pos[0], pos[1])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "log:", err)
		return exitUsage
	}
	for i, p := range parts {
		b, err := os.ReadFile(p.Path)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "log:", err)
			return exitUsage
		}
		if i > 0 {
			_, _ = fmt.Fprintln(stdout)
		}
		_, _ = fmt.Fprintf(stdout, "===== %s =====\n", p.Title)
		_, _ = stdout.Write(b)
		if len(b) > 0 && b[len(b)-1] != '\n' {
			_, _ = fmt.Fprintln(stdout)
		}
	}
	return exitOK
}

// listLogs prints what a run has: its jobs and the names `log <runid> <job>` accepts.
func listLogs(store *localci.Store, id string, stdout, stderr io.Writer) int {
	rec, err := store.ReadRunRecord(id)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = fmt.Errorf("run %s has no run.json yet (still running, or it stopped)", id)
		}
		_, _ = fmt.Fprintln(stderr, "log:", err)
		return exitUsage
	}
	_, _ = fmt.Fprintf(stdout, "run %s: %s\n", id, rec.Conclusion)
	if _, err := store.JobLogs(id, localci.CheckoutLogName); err == nil {
		_, _ = fmt.Fprintf(stdout, "  %-30s unravis-ci log %s %s\n", localci.CheckoutLogName, id, localci.CheckoutLogName)
	}
	for _, j := range rec.Jobs {
		if localci.IsPseudoJob(j) {
			// a stage of the runner itself: its log is the checkout log above; a stage with no log says why it stopped
			if j.Key != localci.CheckoutLogName {
				_, _ = fmt.Fprintf(stdout, "  %-30s (runner stage, %s) %s\n", j.Key, j.Status, j.Reason)
			}
			continue
		}
		_, _ = fmt.Fprintf(stdout, "  %-30s unravis-ci log %s %s   (%s)\n", j.Key, id, j.Key, j.Status)
	}
	return exitOK
}
