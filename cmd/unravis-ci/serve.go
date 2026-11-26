package main

import (
	"context"
	"errors"
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

// How often serve saves the comparison with GitHub for the Releases app, and how many recent runs it looks at.
const (
	defaultCompareEvery = 10 * time.Minute
	defaultCompareLimit = 30
)

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
	noClaims := fs.Bool("no-claims", false, "do not claim requests on the host (with more than one PC watching the repository, two of them may then run the same request)")
	claimTTL := fs.Duration("claim-ttl", localci.DefaultLeaseTTL, "a claim its PC did not refresh for this long is taken over by another PC")
	compareEvery := fs.Duration("compare-every", defaultCompareEvery, "how often the comparison with GitHub is saved for the Releases app's Overview (0 turns it off)")
	cacheLimit := fs.Int64("cache-limit-gb", 8, "size the build cache is trimmed to, in GB (half of it for runs of code that is not the owner's); -1 never trims")
	minFree := fs.Int64("min-free-gb", 10, "the agent claims no request while the drive of the store has less free space than this, in GB (a full disk makes tests fail for the wrong reason); -1 never checks")
	runKeepDays := fs.Int("run-keep-days", int(localci.DefaultRunKeep/(24*time.Hour)), "days the folders of a finished run (logs, work folder) are kept; -1 never removes them")
	runsLimit := fs.Int64("runs-limit-gb", localci.DefaultRunsLimit>>30, "size the run and work folders are cut down to after that, oldest finished run first, in GB; -1 turns the size rule off")
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
	if *claimTTL < 2*localci.DefaultFleetEvery {
		_, _ = fmt.Fprintf(stderr, "serve: --claim-ttl must be at least %s (a claim is refreshed every %s)\n", 2*localci.DefaultFleetEvery, localci.DefaultFleetEvery)
		return exitUsage
	}
	if *compareEvery < 0 || (*compareEvery > 0 && *compareEvery < time.Minute) {
		_, _ = fmt.Fprintln(stderr, "serve: --compare-every must be 0 (off) or at least 1m")
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
		CacheLimit: cacheLimitBytes(*cacheLimit),
		MinFree:    cacheLimitBytes(*minFree),
		RunKeep:    runKeepDuration(*runKeepDays),
		RunsLimit:  cacheLimitBytes(*runsLimit),
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
	if !*noClaims {
		fleet, code := startFleet(ctx, newClaimHost(environ), *repo, store.Dir, labels, *claimTTL, logw, stderr, redact)
		if code != exitOK {
			return code
		}
		agent.Fleet = fleet
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
	if *compareEvery > 0 {
		cmpNaming, code := namingFromFlags("serve", wfFor, pushWf, ctxFlag, false, stderr)
		if code != exitOK {
			return code
		}
		pollDone.Add(1)
		go func() {
			defer pollDone.Done()
			compareTimer(pollCtx, *compareEvery, *repo, store.Dir, cmpNaming, defaultCompareLimit, environ, func(m string) {
				_, _ = fmt.Fprintf(logw, "%s %s\n", time.Now().Format("15:04:05"), redact(m))
			})
		}()
	}
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

// startFleet makes the fleet of this PC and checks that the host takes its records. A token that may not write the
// repository ends serve with a plain message: carrying on without claims would let two PCs run the same request
// without anyone knowing. A host that cannot be reached now is no reason to stop: requests wait until it can.
func startFleet(ctx context.Context, host localci.ClaimHost, repo, dir string, labels []string, ttl time.Duration, logw, stderr io.Writer, redact func(string) string) (*localci.Fleet, int) {
	id, err := localci.LoadFleetID(dir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "serve:", err)
		return nil, exitUsage
	}
	fleet := &localci.Fleet{Host: host, Repo: repo, ID: id, Labels: labels, TTL: ttl, Log: func(format string, a ...any) {
		_, _ = fmt.Fprintf(logw, "%s %s\n", time.Now().Format("15:04:05"), redact(fmt.Sprintf(format, a...)))
	}}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch err := fleet.Probe(pctx); {
	case errors.Is(err, localci.ErrClaimDenied):
		_, _ = fmt.Fprintf(stderr, "serve: this PC may not write claim records to %s: %s\n"+
			"  Claims keep two PCs from running the same request. They need a GitHub login that can write the repository's contents\n"+
			"  (a token that can only post commit statuses is not enough). Sign in with `gh auth login`, or start serve with --no-claims\n"+
			"  if this is the only PC that watches the repository.\n", repo, redact(err.Error()))
		return nil, exitUsage
	case err != nil:
		_, _ = fmt.Fprintf(logw, "claims: the host could not be reached now (%s); requests wait until it can be\n", redact(err.Error()))
	default:
		_, _ = fmt.Fprintf(logw, "claims: on. This PC is %s to the other PCs; a claim not refreshed for %s is taken over\n", id, ttl)
	}
	return fleet, exitOK
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

// runKeepDuration turns --run-keep-days into the agent's RunKeep: a negative number keeps every run, 0 is the
// agent's default.
func runKeepDuration(days int) time.Duration {
	if days < 0 {
		return -1
	}
	return time.Duration(days) * 24 * time.Hour
}

// cacheLimitBytes turns --cache-limit-gb into the agent's CacheLimit: a negative number turns trimming off.
func cacheLimitBytes(gb int64) int64 {
	if gb < 0 {
		return -1
	}
	if gb == 0 {
		return 0 // the agent's default
	}
	return gb << 30
}
