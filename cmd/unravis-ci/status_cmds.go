package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/localci"
)

// The names this project's branch protection requires, by the workflow that produces them. Like the other
// project defaults they live in the command only; internal/localci knows none of them.
var defaultWorkflowContext = map[string]string{"fast-gate.yml": "fast-build", "ci.yml": "build"}

// Test hooks: the real ones talk to GitHub through gh with the token of this process.
var (
	newReporter = func(environ []string, redact func(string) string) localci.StatusReporter {
		return &localci.GHReporter{Env: environ, Redact: redact}
	}
	newCompareSource = func(environ []string) localci.CompareSource { return &localci.GHCompareSource{Env: environ} }
	newDispatcher    = func(environ []string) localci.Dispatcher { return &localci.GHDispatcher{Env: environ} }
)

// statusNaming builds the naming of the statuses from the workflows the agent runs. The context of a gate is the
// one its workflow is known by (ctxFor: workflow file to context). A workflow without one gets no gate status.
func statusNaming(prWorkflows map[string]string, pushWorkflows map[string][]string, ctxFor map[string]string, postRequired bool) localci.StatusNaming {
	n := localci.StatusNaming{PostRequired: postRequired}
	bases := make([]string, 0, len(prWorkflows))
	for b := range prWorkflows {
		bases = append(bases, b)
	}
	sort.Strings(bases)
	for _, b := range bases {
		if c := ctxFor[prWorkflows[b]]; c != "" {
			n.Gates = append(n.Gates, localci.GateRule{Kind: localci.KindPR, Branch: b, Workflow: prWorkflows[b], Context: c})
		}
	}
	branches := make([]string, 0, len(pushWorkflows))
	for b := range pushWorkflows {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	for _, b := range branches {
		for _, wf := range pushWorkflows[b] {
			if c := ctxFor[wf]; c != "" {
				n.Gates = append(n.Gates, localci.GateRule{Kind: localci.KindPush, Branch: b, Workflow: wf, Context: c})
			}
		}
	}
	return n
}

// contextMap is the default workflow-to-context map with the --context overrides on top.
func contextMap(items []string) (map[string]string, error) {
	m := map[string]string{}
	for k, v := range defaultWorkflowContext {
		m[k] = v
	}
	over, err := parseBaseFiles("--context", "--context", items)
	if err != nil {
		return nil, err
	}
	for k, v := range over {
		m[k] = v[len(v)-1]
	}
	return m, nil
}

// statusBanner says in words what this run will post. In required mode it is deliberately loud.
func statusBanner(n localci.StatusNaming, off bool) string {
	var names []string
	for _, g := range n.Gates {
		name := n.Prefix + g.Context
		if n.Prefix == "" {
			name = localci.DefaultStatusPrefix + g.Context
		}
		if n.PostRequired {
			name = g.Context
		}
		dup := false
		for _, x := range names {
			dup = dup || x == name
		}
		if !dup {
			names = append(names, name)
		}
	}
	list := strings.Join(names, ", ")
	if list == "" {
		list = "(no gate)"
	}
	switch {
	case off:
		return "statuses: OFF (--no-status): nothing is posted to GitHub"
	case n.PostRequired:
		return "!!! POST-REQUIRED MODE: this agent posts the REAL required names (" + list + ") and can merge-block or merge-allow pull requests.\n" +
			"!!! This is phase 1/2 of the cutover only. Stop and restart without --post-required to go back to shadow mode."
	}
	return "statuses: SHADOW mode: posting " + list + " and local/<job>; the required names are never posted (--post-required is off)"
}

// ---------------------------------------------------------------- compare

func cmdCompare(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", defaultRepo, "owner/name of the repository")
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	limit := fs.Int("limit", 30, "how many recent local runs and pull requests to look at")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	save := fs.Bool("save", false, "also save the report in the store folder (compare/latest.json), where the Releases app's Overview shows it")
	var wfFor, pushWf, ctxFlag multiFlag
	fs.Var(&wfFor, "workflow-for", "base=file.yml: the workflow that gates pull requests into base (repeatable; replaces the defaults)")
	fs.Var(&pushWf, "push-workflow", "branch=file.yml: a workflow run when branch is pushed (repeatable; replaces the defaults)")
	fs.Var(&ctxFlag, "context", "file.yml=name: the name GitHub knows the workflow's gate by (repeatable; added to the defaults)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "compare: takes no arguments")
		}
		return exitUsage
	}
	naming, code := namingFromFlags("compare", wfFor, pushWf, ctxFlag, false, stderr)
	if code != exitOK {
		return code
	}
	dir := stateDirOf(*state, environ)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		_, _ = fmt.Fprintf(stderr, "compare: the CI store folder %s does not exist (check --state); nothing to compare\n", dir)
		return exitUsage
	}
	rep, err := compareReport(ctx, *repo, dir, naming, *limit, *save, environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "compare:", err)
		return exitUsage
	}
	if *asJSON {
		if err := rep.WriteJSON(stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, "compare:", err)
			return exitUsage
		}
		return exitOK
	}
	rep.WriteTable(stdout)
	return exitOK
}

// compareReport compares this PC's local runs with what GitHub says and, when save is set, keeps the report in the
// store folder where the Releases app's Overview reads it. `compare --save` and the timer of `serve` both use it.
func compareReport(ctx context.Context, repo, dir string, naming localci.StatusNaming, limit int, save bool, environ []string) (*localci.CompareReport, error) {
	entries, err := (&localci.Store{Dir: dir}).ReadIndex()
	if err != nil {
		return nil, err
	}
	rep, err := localci.Compare(ctx, localci.CompareOptions{Repo: repo, Index: entries, Source: newCompareSource(environ), Naming: naming, Limit: limit})
	if err != nil {
		return nil, err
	}
	if save {
		if err := localci.SaveCompare(dir, rep); err != nil {
			return nil, fmt.Errorf("the report could not be saved: %w", err)
		}
	}
	return rep, nil
}

// compareTimer saves the comparison every `every` until ctx ends, so the Overview headline stays fresh while the
// agent runs. A failure (GitHub unreachable, rate limit) is logged once per streak and the timer keeps going.
func compareTimer(ctx context.Context, every time.Duration, repo, dir string, naming localci.StatusNaming, limit int, environ []string, log func(string)) {
	t := time.NewTicker(every)
	defer t.Stop()
	failed := false
	for {
		if _, err := compareReport(ctx, repo, dir, naming, limit, true, environ); err != nil {
			if !failed && ctx.Err() == nil {
				log("compare: could not refresh the comparison, trying again in " + every.String() + ": " + err.Error())
			}
			failed = true
		} else {
			failed = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// namingFromFlags is the naming the serve, compare and fallback commands share.
func namingFromFlags(cmd string, wfFor, pushWf, ctxFlag []string, postRequired bool, stderr io.Writer) (localci.StatusNaming, int) {
	prWorkflows := map[string]string{}
	if len(wfFor) == 0 {
		prWorkflows = defaultWorkflowFor
	} else {
		m, err := parseBaseFiles(cmd, "--workflow-for", wfFor)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return localci.StatusNaming{}, exitUsage
		}
		for k, v := range m {
			prWorkflows[k] = v[len(v)-1]
		}
	}
	pushWorkflows := defaultPushWorkflows
	if len(pushWf) > 0 {
		var err error
		if pushWorkflows, err = parseBaseFiles(cmd, "--push-workflow", pushWf); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return localci.StatusNaming{}, exitUsage
		}
	}
	ctxFor, err := contextMap(ctxFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, cmd+":", err)
		return localci.StatusNaming{}, exitUsage
	}
	return statusNaming(prWorkflows, pushWorkflows, ctxFor, postRequired), exitOK
}

// ---------------------------------------------------------------- fallback

func cmdFallback(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("fallback", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", defaultRepo, "owner/name of the repository")
	dry := fs.Bool("dry-run", false, "check everything and start nothing")
	var wfFor, ctxFlag multiFlag
	fs.Var(&wfFor, "workflow-for", "base=file.yml: the workflow that gates pull requests into base (repeatable; replaces the defaults)")
	fs.Var(&ctxFlag, "context", "file.yml=name (ignored here; accepted so the flags of serve work)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "fallback: expected exactly one pull request number")
		return exitUsage
	}
	n, err := strconv.Atoi(strings.TrimPrefix(pos[0], "#"))
	if err != nil || n <= 0 {
		_, _ = fmt.Fprintf(stderr, "fallback: %q is not a pull request number\n", pos[0])
		return exitUsage
	}
	naming, code := namingFromFlags("fallback", wfFor, nil, ctxFlag, false, stderr)
	if code != exitOK {
		return code
	}
	disp := newDispatcher(environ)
	res, err := localci.Fallback(ctx, localci.FallbackOptions{Repo: *repo, Number: n, Host: newHost(environ), Naming: naming, Dispatcher: disp, DryRun: *dry})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "fallback:", newRedactor(secretValues(environ))(err.Error()))
		return exitFailed
	}
	if res.Started {
		_, _ = fmt.Fprintf(stdout, "started %s on branch %s for pull request %d (%s)\n  ran: %s\n  watch it: gh run list --repo %s --workflow %s --branch %s\n",
			res.Workflow, res.Branch, res.Number, res.SHA[:min(7, len(res.SHA))], res.Command, res.Repo, res.Workflow, res.Branch)
	} else {
		_, _ = fmt.Fprintf(stdout, "dry run: would start %s on branch %s for pull request %d\n  would run: %s\n", res.Workflow, res.Branch, res.Number, res.Command)
	}
	return exitOK
}

// statusLines prints how posting goes for each agent that posts.
func statusLines(w io.Writer, hbs []localci.Heartbeat, now time.Time) {
	for _, h := range hbs {
		if h.Status == nil {
			continue
		}
		s := h.Status
		_, _ = fmt.Fprintf(w, "  %-12s statuses %s: %d posted, %d failed, %d waiting", h.Name, s.Mode, s.Posted, s.Failed, s.Waiting)
		if !s.LastPost.IsZero() {
			_, _ = fmt.Fprintf(w, ", last post %s ago", ago(now, s.LastPost))
		}
		_, _ = fmt.Fprintln(w)
		if s.LastError != "" {
			_, _ = fmt.Fprintf(w, "    last posting problem (%s ago): %s\n", ago(now, s.LastErrorAt), s.LastError)
		}
	}
}

// ledgerPath is the dedup memory of the posted statuses.
func ledgerPath(stateDir string) string { return filepath.Join(stateDir, "status", "ledger.json") }
