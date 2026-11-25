package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/localci"
	"api-audit-explorer/internal/releasedash"
)

// Project defaults of the agent: the repository it watches and the workflows that run on a push. Like the
// pull request defaults in pr.go they live in the command only; internal/localci knows none of them.
const defaultRepo = "abhinavjp/unravis"

var defaultPushWorkflows = map[string][]string{"develop": {"fast-gate.yml", "ci.yml"}, "master": {"ci.yml"}}

// pauseFileName is the file in the state folder whose presence stops the agent from taking new requests.
const pauseFileName = "PAUSE"

// syncWriter makes the many log writers of one process (poller, agent, runs, children) take turns.
type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// parseBaseFiles reads repeatable base=file.yml flags into base -> files, in the order given.
func parseBaseFiles(cmd, flagName string, items []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, kv := range items {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("%s: %s wants base=file.yml (got %q)", cmd, flagName, kv)
		}
		out[k] = append(out[k], v)
	}
	return out, nil
}

// pauseReason says why the agent should not take new requests now, or "" when it may. Two things pause it: the
// pause file in the state folder (made by hand, or by a script that needs the PC quiet), and a release running
// on this PC (releasedash reads the release script's lock-info.json; it never opens release.lock or run.lock).
func pauseReason(pauseFile, releaseDir string, now time.Time) string {
	if pauseFile != "" {
		if _, err := os.Stat(pauseFile); err == nil {
			return "the pause file " + pauseFile + " exists"
		}
	}
	if releaseDir != "" && releasedash.ReleaseRunning(releaseDir, now) {
		return "a release is running on this PC"
	}
	return ""
}

func releaseDirOf(environ []string) string {
	return releasedash.DataDir(func(k string) string { return getenv(environ, k) })
}

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", defaultRepo, "owner/name of the repository to watch")
	state := fs.String("state", "", "the CI store folder (default: under LOCALAPPDATA)")
	origin := fs.String("origin", "", "URL or path the mirror fetches from (default: the host's clone URL of --repo)")
	name := fs.String("name", "local", "name of this agent (one running agent per name)")
	slots := fs.Int("slots", localci.DefaultAgentSlots, "requests run at once")
	jobSlots := fs.Int("job-slots", 2, "jobs at once inside one request")
	interval := fs.Duration("interval", localci.DefaultPollInterval, "time between two looks at the host")
	quiet := fs.Duration("quiet", localci.DefaultQuietPeriod, "a pushed branch must stay unchanged this long before it runs")
	pauseFile := fs.String("pause-file", "", "while this file exists no new request is taken (default: PAUSE in the state folder)")
	releaseDir := fs.String("release-dir", "", "the release script's data folder, for the release check (default: where Release.ps1 keeps it)")
	noReleasePause := fs.Bool("no-release-pause", false, "do not stop taking requests while a release runs on this PC")
	inProcess := fs.Bool("in-process", false, "run requests inside this process instead of one child process each")
	grace := fs.Duration("grace", 30*time.Second, "how long a cancelled child process has to end before it is killed")
	keep := fs.Bool("keep", false, "keep the worktrees after the runs")
	noKeepAwake := fs.Bool("no-keep-awake", false, "do not stop Windows from sleeping while there is work")
	noPush := fs.Bool("no-push", false, "do not run workflows for pushes")
	postRequired := fs.Bool("post-required", false, "post the REAL required status names (cutover phase 1/2 only; the default is shadow mode: local/<name> only)")
	noStatus := fs.Bool("no-status", false, "post no commit statuses at all")
	schedBranch := fs.String("schedule-branch", "master", "branch the schedule workflows are read from")
	var wfFor, pushWf, protect, vars, labels, schedule, trustedBase, allowEnv, ctxFlag multiFlag
	fs.Var(&wfFor, "workflow-for", "base=file.yml: the workflow that gates pull requests into base (repeatable; replaces the defaults)")
	fs.Var(&pushWf, "push-workflow", "branch=file.yml: a workflow run when branch is pushed (repeatable; replaces the defaults)")
	fs.Var(&protect, "protect", "extra path read from the base branch for pull requests of non-owners (repeatable; added to the defaults)")
	fs.Var(&trustedBase, "trusted-base", "extra base branch a pull request of a non-owner may run into (repeatable; the bases of --workflow-for are always trusted)")
	fs.Var(&allowEnv, "allow-env", "NAME of an environment variable steps may see (repeatable; the default is a short allow-list)")
	fs.Var(&ctxFlag, "context", "file.yml=name: the status name GitHub requires for the gate of a workflow (repeatable; added to the defaults fast-gate.yml=fast-build, ci.yml=build)")
	fs.Var(&vars, "var", "vars context value k=v (repeatable)")
	fs.Var(&labels, "label", "a label of this agent (repeatable)")
	fs.Var(&schedule, "schedule", "a workflow file whose on.schedule entries run here (repeatable; off by default)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 0 {
		_, _ = fmt.Fprintln(stderr, "serve: takes no arguments, got", pos[0])
		return exitUsage
	}
	if !strings.Contains(*repo, "/") || strings.HasPrefix(*repo, "/") || strings.HasSuffix(*repo, "/") {
		_, _ = fmt.Fprintf(stderr, "serve: --repo wants owner/name (got %q)\n", *repo)
		return exitUsage
	}
	if !localci.ValidAgentName(*name) {
		_, _ = fmt.Fprintf(stderr, "serve: --name %q is not usable (letters, digits, dot, dash, underscore)\n", *name)
		return exitUsage
	}
	if *slots < 1 || *jobSlots < 1 {
		_, _ = fmt.Fprintln(stderr, "serve: --slots and --job-slots must be at least 1")
		return exitUsage
	}
	prWorkflows := map[string]string{}
	if len(wfFor) == 0 {
		prWorkflows = defaultWorkflowFor
	} else {
		m, err := parseBaseFiles("serve", "--workflow-for", wfFor)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitUsage
		}
		for k, v := range m {
			prWorkflows[k] = v[len(v)-1]
		}
	}
	pushWorkflows := defaultPushWorkflows
	if len(pushWf) > 0 {
		if pushWorkflows, err = parseBaseFiles("serve", "--push-workflow", pushWf); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return exitUsage
		}
	}
	if *noPush {
		pushWorkflows = map[string][]string{}
	}
	ctxFor, err := contextMap(ctxFlag)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "serve:", err)
		return exitUsage
	}
	naming := statusNaming(prWorkflows, pushWorkflows, ctxFor, *postRequired)
	varMap, err := kvList(vars)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "serve: --var:", err)
		return exitUsage
	}
	dir := *state
	if dir == "" {
		dir = defaultStoreDir(environ)
	}
	store, err := localci.OpenStore(dir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "serve:", err)
		return exitUsage
	}
	queue, err := localci.OpenQueue(localci.QueueDir(store.Dir))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "serve:", err)
		return exitUsage
	}
	pf := *pauseFile
	if pf == "" {
		pf = filepath.Join(store.Dir, pauseFileName)
	}
	rdir := *releaseDir
	if rdir == "" && !*noReleasePause {
		rdir = releaseDirOf(environ)
	}
	if *noReleasePause {
		rdir = ""
	}

	logw := syncWriter{mu: &sync.Mutex{}, w: stdout}
	redact := newRedactor(secretValues(environ))
	host := newHost(environ)
	poller := &localci.Poller{
		Host: host, Queue: queue, Repo: *repo, PRWorkflows: prWorkflows, PushWorkflows: pushWorkflows,
		ScheduleWorkflows: schedule, ScheduleBranch: *schedBranch, Interval: *interval, Quiet: *quiet,
		Ran: localci.IndexRan(store), StatePath: filepath.Join(store.Dir, "poller.json"), Log: logw,
	}
	if len(schedule) > 0 {
		entries, err := poller.Schedules(ctx)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "serve: schedule:", redact(err.Error()))
			if strings.Contains(err.Error(), "cron ") {
				return exitUnsupported
			}
			return exitUsage
		}
		for _, e := range entries {
			_, _ = fmt.Fprintf(logw, "schedule %s %q next at %s\n", e.Workflow, e.Cron, e.Next.UTC().Format("2006-01-02 15:04 UTC"))
		}
	}

	runner := runnerFor(*inProcess, store, host, *origin, trustCfg{protect: protect, trusted: trustedBases(prWorkflows, trustedBase), allowEnv: allowEnv}, environ, varMap, *jobSlots, *keep, *grace, redact, logw)
	if runner == nil {
		_, _ = fmt.Fprintln(stderr, "serve: cannot find this program to start the child processes (use --in-process)")
		return exitUsage
	}

	var statusSvc *localci.StatusService
	var poster *localci.StatusPoster
	if *noStatus {
		_, _ = fmt.Fprintln(logw, statusBanner(naming, true))
	} else {
		_, _ = fmt.Fprintln(logw, statusBanner(naming, false))
		rep := &localci.ShadowGuard{Next: &localci.DedupReporter{
			Path: ledgerPath(store.Dir),
			Next: &localci.RetryReporter{Next: newReporter(environ, redact)},
		}, Prefix: naming.Prefix, PostRequired: *postRequired}
		poster = &localci.StatusPoster{Reporter: rep, Log: func(format string, a ...any) {
			_, _ = fmt.Fprintf(logw, "%s status: %s\n", time.Now().Format("15:04:05"), redact(fmt.Sprintf(format, a...)))
		}}
		statusSvc = &localci.StatusService{Poster: poster, Naming: naming, Dir: store.Dir}
	}

	var lastPause string
	var pauseMu sync.Mutex
	agent := &localci.Agent{
		Name: *name, Dir: store.Dir, Queue: queue, Slots: *slots, Labels: labels, Runner: runner,
		Paused: func() bool {
			why := pauseReason(pf, rdir, time.Now())
			pauseMu.Lock()
			defer pauseMu.Unlock()
			if why != lastPause {
				if why != "" {
					_, _ = fmt.Fprintf(logw, "%s paused: %s\n", time.Now().Format("15:04:05"), why)
				} else {
					_, _ = fmt.Fprintf(logw, "%s resumed\n", time.Now().Format("15:04:05"))
				}
				lastPause = why
			}
			return why != ""
		},
		PIDAlive: func(pid int) bool { return releasedash.ProcessAlive(pid, time.Now()) },
		Log:      logw,
		Status:   statusSvc,
	}
	if !*noKeepAwake {
		ka := localci.NewKeepAwake()
		defer ka.Close()
		agent.KeepAwake = ka.Set
	}

	// The first Ctrl-C drains: no new request is taken and the running ones finish. The second stops them (they
	// go back to the queue). The context of the poller ends with the first one.
	hard, hardStop := context.WithCancel(context.WithoutCancel(ctx))
	defer hardStop()
	pollCtx, pollStop := context.WithCancel(ctx)
	defer pollStop()
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-sig:
		case <-finished:
			return
		}
		_, _ = fmt.Fprintf(logw, "%s stopping: letting the running requests finish (Ctrl-C again stops them)\n", time.Now().Format("15:04:05"))
		pollStop()
		agent.Drain()
		// the press that started the drain may arrive twice (here and through the context): it counts once
		first := time.Now()
		for {
			select {
			case <-sig:
				if time.Since(first) > time.Second {
					hardStop()
					return
				}
			case <-finished:
				return
			}
		}
	}()

	var pollDone sync.WaitGroup
	pollDone.Add(1)
	go func() {
		defer pollDone.Done()
		poller.Run(pollCtx)
	}()
	_, _ = fmt.Fprintf(logw, "watching %s every %s (state %s)\n", *repo, *interval, store.Dir)
	runErr := agent.Run(hard)
	if poster != nil {
		poster.Close(30 * time.Second)
	}
	close(finished)
	pollStop()
	pollDone.Wait()
	if runErr != nil {
		_, _ = fmt.Fprintln(stderr, "serve:", redact(runErr.Error()))
		return exitUsage
	}
	return exitOK
}

// trustCfg is what a run needs besides the request: the protected paths, the trusted bases and the allowed
// environment names. A child process gets them as flags.
type trustCfg struct{ protect, trusted, allowEnv []string }

func (t trustCfg) argv() []string {
	var a []string
	for _, p := range t.protect {
		a = append(a, "--protect", p)
	}
	for _, b := range t.trusted {
		a = append(a, "--trusted-base", b)
	}
	for _, n := range t.allowEnv {
		a = append(a, "--allow-env", n)
	}
	return a
}

// runnerFor builds the Runner of the agent: the request runs in this process, or in a child `run-item` process
// that can be stopped with its whole tree. It returns nil when the child process program cannot be found.
func runnerFor(inProcess bool, store *localci.Store, host localci.Host, origin string, trust trustCfg, environ []string, vars map[string]string, jobSlots int, keep bool, grace time.Duration, redact func(string) string, log io.Writer) localci.Runner {
	if inProcess {
		ir := &localci.ItemRunner{
			Store: store, Host: host, OriginURL: origin, Protect: append(append([]string{}, defaultProtect...), trust.protect...), TrustedBases: trust.trusted, Env: scrubEnv(environ, trust.allowEnv), Vars: vars,
			Slots: jobSlots, Keep: keep, Redact: redact, Log: log,
		}
		return ir.Run
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	er := &localci.ExecRunner{
		StateDir: store.Dir, Env: environ, Log: log, Grace: grace,
		Argv: func(r localci.Request) []string {
			argv := []string{exe, "run-item", r.ID, "--child", "--state", store.Dir, "--job-slots", fmt.Sprint(jobSlots)}
			if origin != "" {
				argv = append(argv, "--origin", origin)
			}
			if keep {
				argv = append(argv, "--keep")
			}
			argv = append(argv, trust.argv()...)
			for k, v := range vars {
				argv = append(argv, "--var", k+"="+v)
			}
			return argv
		},
	}
	return er.Run
}
