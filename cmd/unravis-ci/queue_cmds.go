package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/localci"
)

// childStdin is what a `run-item --child` process watches: when it closes, the agent wants the run stopped.
// Tests replace it.
var childStdin io.Reader = os.Stdin

// agentStale is how long after its last heartbeat an agent counts as gone.
const agentStale = 3 * localci.DefaultBeatEvery

func stateDirOf(flagValue string, environ []string) string {
	if flagValue != "" {
		return flagValue
	}
	return defaultStoreDir(environ)
}

// ---------------------------------------------------------------- run-item

// cmdRunItem runs one queued request in this process. The agent starts it as a child (--child): it then leaves
// its result in the outcome file and stops when its stdin closes. By hand it only runs the request and prints
// the result; the request's place in the queue is not touched.
func cmdRunItem(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("run-item", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	origin := fs.String("origin", "", "URL or path the mirror fetches from (default: the host's clone URL)")
	keep := fs.Bool("keep", false, "keep the worktree after the run")
	jobSlots := fs.Int("job-slots", 2, "jobs at once")
	child := fs.Bool("child", false, "started by an agent: leave the outcome file, stop when stdin closes")
	var protect, vars, trustedBase, allowEnv multiFlag
	fs.Var(&trustedBase, "trusted-base", "extra base branch a pull request of a non-owner may run into (repeatable)")
	fs.Var(&allowEnv, "allow-env", "NAME of an environment variable steps may see (repeatable)")
	fs.Var(&protect, "protect", "extra path read from the base branch for pull requests of non-owners (repeatable; added to the defaults)")
	fs.Var(&vars, "var", "vars context value k=v (repeatable)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "run-item: expected exactly one request id")
		usage(stderr)
		return exitUsage
	}
	varMap, err := kvList(vars)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-item: --var:", err)
		return exitUsage
	}
	store, err := localci.OpenStore(stateDirOf(*state, environ))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-item:", err)
		return exitUsage
	}
	queue, err := localci.OpenQueue(localci.QueueDir(store.Dir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-item:", err)
		return exitUsage
	}
	redact := newRedactor(secretValues(environ))
	req, err := queue.Get(pos[0])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "run-item:", redact(err.Error()))
		return exitUsage
	}
	if *child {
		var stop context.CancelFunc
		ctx, stop = context.WithCancel(ctx)
		defer stop()
		// The reader is taken here, not inside the goroutine: the goroutine may start (or still be running) after a test has put
		// the variable back, and reading a package variable from it would race with that write.
		stdin := childStdin
		go func() {
			_, _ = io.Copy(io.Discard, stdin)
			stop()
		}()
	}
	ir := &localci.ItemRunner{
		Store: store, Host: newHost(environ), OriginURL: *origin, Protect: append(append([]string{}, defaultProtect...), protect...),
		TrustedBases: trustedBase, Env: scrubEnv(environ, allowEnv), Vars: varMap, Slots: *jobSlots, Keep: *keep, Redact: redact, Log: stdout,
	}
	out, err := ir.Run(ctx, req)
	if err != nil {
		out.Conclusion, out.Reason = "error", redact(err.Error())
		_, _ = fmt.Fprintln(stderr, "run-item:", out.Reason)
	}
	if *child {
		if werr := localci.WriteOutcome(store.Dir, req.ID, out); werr != nil {
			_, _ = fmt.Fprintln(stderr, "run-item: cannot leave the outcome:", werr)
			return exitUsage
		}
	}
	_, _ = fmt.Fprintf(stdout, "%s: %s%s\n", req.ID, out.Conclusion, noteIf(out.Reason != "", " ("+out.Reason+")"))
	if out.RunID != "" {
		_, _ = fmt.Fprintf(stdout, "run id: %s\nlogs:   unravis-ci log %s [job]\n", out.RunID, out.RunID)
	}
	if out.Conclusion == "success" {
		return exitOK
	}
	return exitFailed
}

func noteIf(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

// ---------------------------------------------------------------- status

func ago(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

func shortID(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// requestLine is one request as a row of the status listing.
func requestLine(now time.Time, r localci.Request) string {
	what := r.Label()
	when := "waiting " + ago(now, r.EnqueuedAt)
	switch r.State {
	case localci.StateRunning:
		when = "on " + r.ClaimedBy + " for " + ago(now, r.ClaimedAt)
	case localci.StateQueued:
	default:
		when = r.Conclusion + " " + ago(now, r.FinishedAt) + " ago"
		if r.Reason != "" {
			when += ": " + r.Reason
		}
	}
	return fmt.Sprintf("  %-34s %-9s %-34s %-8s %-13s %s", r.ID, r.State, what, shortID(r.SHA), r.Workflow, when)
}

func cmdStatus(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	recent := fs.Int("recent", 5, "show the last N finished requests (0 = none)")
	pauseFile := fs.String("pause-file", "", "the pause file serve uses (default: PAUSE in the state folder)")
	noReleasePause := fs.Bool("no-release-pause", false, "do not report a running release as a pause")
	releaseDir := fs.String("release-dir", "", "the release script's data folder (default: where Release.ps1 keeps it)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "status: takes no arguments")
		}
		return exitUsage
	}
	dir := stateDirOf(*state, environ)
	now := time.Now()
	_, _ = fmt.Fprintf(stdout, "state: %s\n", dir)
	pf := *pauseFile
	if pf == "" {
		pf = filepath.Join(dir, pauseFileName)
	}
	rdir := *releaseDir
	if rdir == "" {
		rdir = releaseDirOf(environ)
	}
	if *noReleasePause {
		rdir = ""
	}
	if why := pauseReason(pf, rdir, now); why != "" {
		_, _ = fmt.Fprintf(stdout, "claiming is paused: %s\n", why)
	}

	hbs, err := localci.ReadAgents(dir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "status:", err)
		return exitUsage
	}
	_, _ = fmt.Fprintln(stdout, "agents:")
	if len(hbs) == 0 {
		_, _ = fmt.Fprintln(stdout, "  none (start one with: unravis-ci serve)")
	}
	for _, h := range hbs {
		st := h.State
		if !h.Fresh(now, agentStale) {
			st = "gone"
		}
		_, _ = fmt.Fprintf(stdout, "  %-12s %-9s slots %d  running %d  pid %d  seen %s ago%s\n", h.Name, st, h.Slots, len(h.Jobs), h.PID, ago(now, h.LastSeen), labelText(h.Labels))
	}
	statusLines(stdout, hbs, now)

	queue := &localci.Queue{Dir: localci.QueueDir(dir)}
	all, err := queue.List()
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "status:", err)
		return exitUsage
	}
	var active, finished []localci.Request
	for _, r := range all {
		if r.Active() {
			active = append(active, r)
		} else {
			finished = append(finished, r)
		}
	}
	running := 0
	for _, r := range active {
		if r.State == localci.StateRunning {
			running++
		}
	}
	_, _ = fmt.Fprintf(stdout, "queue: %d waiting, %d running\n", len(active)-running, running)
	for _, r := range active {
		_, _ = fmt.Fprintln(stdout, requestLine(now, r))
	}
	if *recent > 0 && len(finished) > 0 {
		if len(finished) > *recent {
			finished = finished[:*recent]
		}
		_, _ = fmt.Fprintln(stdout, "finished (newest first):")
		for _, r := range finished {
			_, _ = fmt.Fprintln(stdout, requestLine(now, r))
		}
	}
	return exitOK
}

func labelText(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return "  labels " + strings.Join(l, ",")
}

// ---------------------------------------------------------------- cancel

func isNumber(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0 && strconv.Itoa(n) == s
}

func cmdCancel(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	reason := fs.String("reason", "cancelled from the command line", "why (kept with the request)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "cancel: expected one request id or pull request number")
		usage(stderr)
		return exitUsage
	}
	queue, err := localci.OpenQueue(localci.QueueDir(stateDirOf(*state, environ)))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "cancel:", err)
		return exitUsage
	}
	var ids []string
	if n, ok := isNumber(pos[0]); ok {
		all, err := queue.List()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "cancel:", err)
			return exitUsage
		}
		for _, r := range all {
			if r.Active() && r.Kind == localci.KindPR && r.Number == n {
				ids = append(ids, r.ID)
			}
		}
		if len(ids) == 0 {
			_, _ = fmt.Fprintf(stderr, "cancel: pull request %d has no queued or running request\n", n)
			return exitFailed
		}
	} else {
		ids = []string{pos[0]}
	}
	code := exitOK
	for _, id := range ids {
		r, err := queue.Cancel(id, *reason)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cancel: %v\n", err)
			code = exitFailed
			continue
		}
		switch {
		case r.State == localci.StateRunning:
			_, _ = fmt.Fprintf(stdout, "%s: %s is running; its agent has been told to stop it\n", r.ID, r.Label())
		case r.Active():
			_, _ = fmt.Fprintf(stdout, "%s: cancel requested\n", r.ID)
		default:
			_, _ = fmt.Fprintf(stdout, "%s: %s (%s)\n", r.ID, r.State, r.Label())
		}
	}
	return code
}

// ---------------------------------------------------------------- rerun

// requestOfRun rebuilds the request that made an index entry, for a rerun of the same commit.
func requestOfRun(e localci.IndexEntry) (localci.Request, error) {
	r := localci.Request{Repo: e.Repo, Workflow: e.Workflow, Ref: e.HeadRef, Base: e.BaseRef, SHA: e.HeadSHA, Number: e.PR}
	switch e.Event {
	case "pull_request":
		if e.PR <= 0 {
			return r, fmt.Errorf("run %s has no pull request number", e.RunID)
		}
		r.Kind = localci.KindPR
	case "push":
		r.Kind = localci.KindPush
	case "schedule":
		r.Kind, r.Cron = localci.KindSchedule, "rerun"
	default:
		return r, fmt.Errorf("run %s was a %q run; only pull request, push and schedule runs can be run again", e.RunID, e.Event)
	}
	if r.Repo == "" || r.Workflow == "" || r.SHA == "" {
		return r, fmt.Errorf("run %s does not record the repository, workflow and commit", e.RunID)
	}
	return r, nil
}

func cmdRerun(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("rerun", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	repo := fs.String("repo", defaultRepo, "owner/name of the repository (for a pull request number)")
	workflow := fs.String("workflow", "", "run this workflow file whatever the base branch is (for a pull request number)")
	var wfFor multiFlag
	fs.Var(&wfFor, "workflow-for", "base=file.yml: the workflow that gates pull requests into base (repeatable; replaces the defaults)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "rerun: expected one run id or pull request number")
		usage(stderr)
		return exitUsage
	}
	dir := stateDirOf(*state, environ)
	store, err := localci.OpenStore(dir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rerun:", err)
		return exitUsage
	}
	queue, err := localci.OpenQueue(localci.QueueDir(store.Dir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rerun:", err)
		return exitUsage
	}
	var req localci.Request
	if n, ok := isNumber(pos[0]); ok {
		workflows := defaultWorkflowFor
		if len(wfFor) > 0 {
			m, err := parseBaseFiles("rerun", "--workflow-for", wfFor)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return exitUsage
			}
			workflows = map[string]string{}
			for k, v := range m {
				workflows[k] = v[len(v)-1]
			}
		}
		pr, err := newHost(environ).PullRequest(ctx, *repo, n)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "rerun:", newRedactor(secretValues(environ))(err.Error()))
			return exitFailed
		}
		if !strings.EqualFold(pr.State, "OPEN") {
			_, _ = fmt.Fprintf(stderr, "rerun: pull request %d is %s, not open\n", n, strings.ToLower(pr.State))
			return exitFailed
		}
		wf := *workflow
		if wf == "" {
			wf = workflows[pr.BaseRef]
		}
		if wf == "" {
			_, _ = fmt.Fprintf(stderr, "rerun: no workflow is set for pull requests into %q (use --workflow or --workflow-for)\n", pr.BaseRef)
			return exitUsage
		}
		req = localci.Request{Kind: localci.KindPR, Repo: *repo, Number: n, Ref: pr.HeadRef, Base: pr.BaseRef, SHA: pr.HeadSHA, Workflow: wf}
	} else {
		entries, err := store.ReadIndex()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "rerun:", err)
			return exitUsage
		}
		var hit []localci.IndexEntry
		for _, e := range entries {
			if e.RunID == pos[0] {
				hit = []localci.IndexEntry{e}
				break
			}
			if strings.HasPrefix(e.RunID, pos[0]) {
				hit = append(hit, e)
			}
		}
		switch len(hit) {
		case 0:
			_, _ = fmt.Fprintf(stderr, "rerun: no run %q in %s\n", pos[0], dir)
			return exitFailed
		case 1:
		default:
			_, _ = fmt.Fprintf(stderr, "rerun: %q matches %d runs; give more of the id\n", pos[0], len(hit))
			return exitUsage
		}
		if req, err = requestOfRun(hit[0]); err != nil {
			_, _ = fmt.Fprintln(stderr, "rerun:", err)
			return exitFailed
		}
	}
	req.Rerun = true // an answer another PC left for this commit does not stand in the way of a rerun
	got, added, err := queue.Enqueue(req)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "rerun:", err)
		return exitFailed
	}
	if !added {
		_, _ = fmt.Fprintf(stdout, "%s: already %s (%s at %s); nothing added\n", got.ID, got.State, got.Label(), shortID(got.SHA))
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "%s: queued %s at %s with %s\n", got.ID, got.Label(), shortID(got.SHA), got.Workflow)
	return exitOK
}

// ---------------------------------------------------------------- drain

func cmdDrain(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	name := fs.String("name", "", "the agent to drain (default: every live agent)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "drain: takes no arguments")
		}
		return exitUsage
	}
	dir := stateDirOf(*state, environ)
	var names []string
	if *name != "" {
		names = []string{*name}
	} else {
		hbs, err := localci.ReadAgents(dir)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "drain:", err)
			return exitUsage
		}
		for _, h := range hbs {
			if h.Fresh(time.Now(), agentStale) {
				names = append(names, h.Name)
			}
		}
	}
	if len(names) == 0 {
		_, _ = fmt.Fprintln(stderr, "drain: no agent is running in", dir)
		return exitFailed
	}
	for _, n := range names {
		if err := localci.RequestDrain(dir, n); err != nil {
			_, _ = fmt.Fprintln(stderr, "drain:", err)
			return exitUsage
		}
		_, _ = fmt.Fprintf(stdout, "%s: asked to drain (it takes nothing new, finishes what runs, then exits)\n", n)
	}
	return exitOK
}
