package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RunOptions configures Run.
type RunOptions struct {
	Workflow *Workflow
	Config   *LocalConfig // may be nil => defaults
	Workdir  string       // the checkout (already exists); every job runs here
	StateDir string       // per-run folder: logs, run.json, artifacts
	GitHub   map[string]any
	Inputs   map[string]string
	Vars     map[string]string
	Env      []string // base process environment (KEY=VALUE), already scrubbed by the caller
	Only     []string // run only these job ids; empty => all
	Slots    int      // max jobs at once (default 2)
	Redact   func(string) string
	Log      io.Writer
	Now      func() time.Time
	// DryRun resolves and prints the plan and runs nothing.
	DryRun bool

	// stepTimeoutOverride replaces every step timeout (test hook).
	stepTimeoutOverride time.Duration
	// jobTimeoutOverride replaces the job timeout-minutes (test hook).
	jobTimeoutOverride time.Duration
	// waitDelayOverride replaces how long a step's output pipes are drained after it exits (test hook).
	waitDelayOverride time.Duration
}

// StepResult is the result of one step. Index is 1-based.
type StepResult struct {
	Index      int       `json:"index"`
	ID         string    `json:"id,omitempty"`
	Name       string    `json:"name"`
	Outcome    string    `json:"outcome"`
	Conclusion string    `json:"conclusion"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	LogFile    string    `json:"log_file,omitempty"`
	Err        string    `json:"err,omitempty"`
}

// JobResult is the result of one job instance (one matrix combination).
type JobResult struct {
	Key     string            `json:"key"`
	ID      string            `json:"id"`
	Label   string            `json:"label"`
	Matrix  map[string]any    `json:"matrix,omitempty"`
	Status  string            `json:"status"`
	Reason  string            `json:"reason,omitempty"`
	Steps   []StepResult      `json:"steps,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
	Start   time.Time         `json:"start"`
	End     time.Time         `json:"end"`
	// Pseudo marks a record of the runner itself (the stage a run stopped at), not a job of the workflow.
	Pseudo bool `json:"pseudo,omitempty"`

	coe bool // continue-on-error, evaluated for this instance
}

// RunResult is the result of a whole workflow run.
type RunResult struct {
	Workflow   string       `json:"workflow"`
	Conclusion string       `json:"conclusion"`
	Jobs       []*JobResult `json:"jobs"`
	Start      time.Time    `json:"start"`
	End        time.Time    `json:"end"`
}

// WriteRunRecord writes StateDir/run.json.
func WriteRunRecord(stateDir string, r *RunResult) error {
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(stateDir, "run.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(stateDir, "run.json"))
}

// ---------------------------------------------------------------- run state

type jobAgg struct {
	result   string // success|failure|cancelled|skipped|not_run_locally
	accepted bool
	outputs  map[string]string
}

// needsResult is the value of needs.<id>.result.
func (a *jobAgg) needsResult() string {
	if a.result == "not_run_locally" {
		if a.accepted {
			return "success"
		}
		return "failure"
	}
	return a.result
}

type runState struct {
	o      RunOptions
	wf     *Workflow
	file   string // workflow base name (for config overrides)
	masks  masker
	sem    chan struct{}
	logMu  sync.Mutex
	sumMu  sync.Mutex
	tblMu  sync.Mutex
	table  map[string]*jobAgg
	inputs map[string]any
}

func (r *runState) now() time.Time {
	if r.o.Now != nil {
		return r.o.Now()
	}
	return time.Now()
}

// mask is the one redactor every sink goes through: the ::add-mask:: values and the caller's Redact (which
// the command line fills with the values of the environment variables it scrubbed).
func (r *runState) mask(s string) string {
	s = r.masks.apply(s)
	if r.o.Redact != nil {
		s = r.o.Redact(s)
	}
	return s
}

// maskResult masks everything in the result that is written to run.json or printed.
func (r *runState) maskResult(res *RunResult) {
	for _, j := range res.Jobs {
		j.Reason = r.mask(j.Reason)
		for k, v := range j.Outputs {
			j.Outputs[k] = r.mask(v)
		}
		for i := range j.Steps {
			j.Steps[i].Name = r.mask(j.Steps[i].Name)
			j.Steps[i].Err = r.mask(j.Steps[i].Err)
		}
	}
}

func (r *runState) logf(format string, a ...any) {
	if r.o.Log == nil {
		return
	}
	line := r.mask(fmt.Sprintf(format, a...))
	r.logMu.Lock()
	defer r.logMu.Unlock()
	_, _ = fmt.Fprintln(r.o.Log, line)
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(time.Second).String()
}

var slugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-.")
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-.")
	}
	if s == "" {
		s = "x"
	}
	return s
}

// Run executes the workflow. The error is only for setup problems; job and step failures are in the result.
func Run(ctx context.Context, o RunOptions) (*RunResult, error) {
	w := o.Workflow
	if w == nil {
		return nil, errors.New("run: no workflow")
	}
	var problems []string
	for _, j := range w.Jobs {
		for i, st := range j.Steps {
			where := fmt.Sprintf("%s:%d (job %s step %d)", w.File, st.Line, j.ID, i+1)
			if st.Uses != "" {
				if _, ok := shims[shimName(st.Uses)]; !ok {
					problems = append(problems, fmt.Sprintf("unsupported: uses %s at %s", shimName(st.Uses), where))
				}
			}
			if st.Run != "" {
				for _, sh := range []string{st.Shell, j.Defaults.Shell, w.Defaults.Shell} {
					if sh != "" && !knownShell(sh) {
						problems = append(problems, fmt.Sprintf("unsupported: shell %s at %s", sh, where))
					}
				}
			}
		}
	}
	levels, err := TopoLevels(w)
	if err != nil {
		problems = append(problems, err.Error())
	}
	only := map[string]bool{}
	for _, id := range o.Only {
		if w.JobByID(id) == nil {
			problems = append(problems, fmt.Sprintf("--job %s: no such job", id))
		}
		only[id] = true
	}
	if !o.DryRun {
		if o.Workdir == "" {
			problems = append(problems, "no workdir")
		} else if st, err := os.Stat(o.Workdir); err != nil || !st.IsDir() {
			problems = append(problems, fmt.Sprintf("workdir %s does not exist", o.Workdir))
		}
		if o.StateDir == "" {
			problems = append(problems, "no state dir")
		}
	}
	r := &runState{o: o, wf: w, file: filepath.Base(w.File), table: map[string]*jobAgg{}}
	if o.Slots <= 0 {
		r.o.Slots = 2
	}
	r.sem = make(chan struct{}, r.o.Slots)
	if r.o.GitHub == nil {
		r.o.GitHub = map[string]any{}
	}
	r.inputs = map[string]any{}
	for name, spec := range w.Inputs {
		v, given := o.Inputs[name]
		if !given {
			v = spec.Default
		}
		if spec.Required && !given && spec.Default == "" && fmt.Sprint(r.o.GitHub["event_name"]) == "workflow_dispatch" && !o.DryRun {
			problems = append(problems, fmt.Sprintf("input %s is required", name))
		}
		if spec.Type == "boolean" {
			r.inputs[name] = v == "true"
		} else {
			r.inputs[name] = v
		}
	}
	for k, v := range o.Inputs {
		if _, ok := r.inputs[k]; !ok {
			r.inputs[k] = v
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("cannot run %s:\n  %s", w.File, strings.Join(problems, "\n  "))
	}
	if o.Workdir != "" {
		if abs, err := filepath.Abs(o.Workdir); err == nil {
			r.o.Workdir = abs
		}
	}
	if o.StateDir != "" {
		if abs, err := filepath.Abs(o.StateDir); err == nil {
			r.o.StateDir = abs
		}
		if !o.DryRun {
			if err := os.MkdirAll(r.o.StateDir, 0o750); err != nil {
				return nil, err
			}
		}
	}
	res := &RunResult{Workflow: w.Name, Start: r.now()}
	if o.DryRun {
		r.plan(levels, only, res)
		res.Conclusion = "success"
		res.End = r.now()
		return res, nil
	}
	for _, level := range levels {
		var wg sync.WaitGroup
		perJob := make([][]*JobResult, len(level))
		for i, id := range level {
			if len(only) > 0 && !only[id] {
				continue
			}
			wg.Add(1)
			go func(i int, job *Job) {
				defer wg.Done()
				perJob[i] = r.runJob(ctx, job)
			}(i, w.JobByID(id))
		}
		wg.Wait()
		for _, jrs := range perJob {
			res.Jobs = append(res.Jobs, jrs...)
		}
	}
	res.End = r.now()
	r.maskResult(res)
	res.Conclusion = "success"
	for _, a := range r.table {
		if a.result == "failure" || (a.result == "not_run_locally" && !a.accepted) {
			res.Conclusion = "failure"
		}
	}
	if res.Conclusion == "success" && ctx.Err() != nil {
		res.Conclusion = "cancelled"
	}
	return res, nil
}

// ---------------------------------------------------------------- contexts

func copyAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func strMapToAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func runnerOSFor(label string) string {
	switch {
	case strings.HasPrefix(label, "windows-"):
		return "Windows"
	case strings.HasPrefix(label, "ubuntu-"):
		return "Linux"
	case strings.HasPrefix(label, "macos-"):
		return "macOS"
	}
	if runtime.GOOS == "windows" {
		return "Windows"
	}
	return "Linux"
}

func (r *runState) githubCtx(jobID string) map[string]any {
	g := copyAnyMap(r.o.GitHub)
	g["workspace"] = r.o.Workdir
	g["token"] = ""
	g["job"] = jobID
	if _, ok := g["workflow"]; !ok {
		g["workflow"] = r.wf.Name
	}
	return g
}

// baseVars are the contexts shared by every expression of a job instance.
func (r *runState) baseVars(job *Job, combo map[string]any) map[string]any {
	needs := map[string]any{}
	r.tblMu.Lock()
	for _, n := range job.Needs {
		if a := r.table[n]; a != nil {
			needs[n] = map[string]any{"result": a.needsResult(), "outputs": strMapToAny(a.outputs)}
		}
	}
	r.tblMu.Unlock()
	return map[string]any{
		"github":  r.githubCtx(job.ID),
		"vars":    strMapToAny(r.o.Vars),
		"secrets": map[string]any{},
		"inputs":  copyAnyMap(r.inputs),
		"matrix":  copyAnyMap(combo),
		"needs":   needs,
		"steps":   map[string]any{},
		"env":     map[string]any{},
	}
}

// needStatus is the job-level status function value derived from the needed jobs.
func (r *runState) needStatus(job *Job) string {
	r.tblMu.Lock()
	defer r.tblMu.Unlock()
	st := "success"
	for _, n := range job.Needs {
		a := r.table[n]
		if a == nil {
			continue
		}
		switch a.needsResult() {
		case "failure":
			return "failure"
		case "cancelled":
			if st != "failure" {
				st = "cancelled"
			}
		case "skipped":
			if st == "success" {
				st = "skipped"
			}
		}
	}
	return st
}

func interpEnv(m map[string]string, c *ExprContext) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, err := Interpolate(v, c)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}

// ---------------------------------------------------------------- jobs

func aggregate(jrs []*JobResult, override JobOverride) *jobAgg {
	a := &jobAgg{outputs: map[string]string{}}
	var failed, cancelled, notRun, skipped, ok int
	for _, jr := range jrs {
		switch jr.Status {
		case "failure":
			if jr.coe {
				ok++ // continue-on-error: the failure does not fail the job
			} else {
				failed++
			}
		case "cancelled":
			cancelled++
		case "not_run_locally":
			notRun++
		case "skipped":
			skipped++
		default:
			ok++
		}
		for k, v := range jr.Outputs {
			if v != "" || a.outputs[k] == "" {
				a.outputs[k] = v
			}
		}
	}
	switch {
	case failed > 0:
		a.result = "failure"
	case cancelled > 0:
		a.result = "cancelled"
	case notRun == len(jrs):
		a.result = "not_run_locally"
		a.accepted = override.Accepted
	case skipped+notRun == len(jrs):
		a.result = "skipped"
	default:
		a.result = "success"
	}
	return a
}

func (r *runState) comboList(job *Job) []map[string]any {
	if job.Strategy == nil {
		return []map[string]any{{}}
	}
	return ExpandMatrix(job.Strategy.Matrix)
}

func (r *runState) instanceKeys(job *Job, combos []map[string]any) (keys, labels []string) {
	seen := map[string]bool{}
	for _, c := range combos {
		label := MatrixLabel(job, c)
		key := slug(job.ID)
		if len(combos) > 1 || len(c) > 0 {
			key = slug(label)
		}
		base := key
		for n := 2; seen[key]; n++ {
			key = base + "-" + strconv.Itoa(n)
		}
		seen[key] = true
		keys = append(keys, key)
		labels = append(labels, label)
	}
	return
}

func (r *runState) runJob(ctx context.Context, job *Job) []*JobResult {
	combos := r.comboList(job)
	keys, labels := r.instanceKeys(job, combos)
	needStatus := r.needStatus(job)
	jobCtx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()
	failFast := job.Strategy != nil && job.Strategy.FailFast && len(combos) > 1
	out := make([]*JobResult, len(combos))
	var wg sync.WaitGroup
	for i := range combos {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			jr := r.runInstance(jobCtx, job, combos[i], keys[i], labels[i], needStatus)
			out[i] = jr
			if jr.Status == "failure" && failFast && !jr.coe {
				cancelJob()
			}
		}(i)
	}
	wg.Wait()
	ov := r.o.Config.Override(r.file, job.ID)
	agg := aggregate(out, ov)
	r.tblMu.Lock()
	r.table[job.ID] = agg
	r.tblMu.Unlock()
	return out
}

func (r *runState) runInstance(ctx context.Context, job *Job, combo map[string]any, key, label, needStatus string) *JobResult {
	jr := &JobResult{Key: key, ID: job.ID, Label: label, Start: r.now()}
	if len(combo) > 0 {
		jr.Matrix = combo
	}
	fin := func(status, reason string) *JobResult {
		jr.Status, jr.Reason, jr.End = status, reason, r.now()
		r.logf("[%s] job %s%s", key, status, map[bool]string{true: ": " + reason}[reason != ""])
		return jr
	}
	vars := r.baseVars(job, combo)
	ec := &ExprContext{Vars: vars, JobStatus: needStatus, Workdir: r.o.Workdir}
	wfEnv, err := interpEnv(r.wf.Env, ec)
	if err != nil {
		return fin("failure", err.Error())
	}
	vars["env"] = strMapToAny(wfEnv)
	ok, err := EvalCond(job.If, ec)
	if err != nil {
		return fin("failure", "if: "+err.Error())
	}
	if !ok {
		reason := "condition is false"
		if job.If == "" {
			reason = "a needed job did not succeed"
		}
		return fin("skipped", reason)
	}
	if job.ContinueOnError != "" {
		v, err := Interpolate(job.ContinueOnError, ec)
		if err != nil {
			return fin("failure", "continue-on-error: "+err.Error())
		}
		jr.coe = strings.TrimSpace(v) == "true"
	}
	runsOn, err := Interpolate(job.RunsOn, ec)
	if err != nil {
		return fin("failure", "runs-on: "+err.Error())
	}
	runsOn = strings.TrimSpace(runsOn)
	ov := r.o.Config.Override(r.file, job.ID)
	if ov.Skip != "" {
		return fin("not_run_locally", ov.Skip)
	}
	mode, known := r.o.Config.Resolve(runsOn)
	if mode == "skip" {
		if !known {
			return fin("not_run_locally", fmt.Sprintf("runs-on %q is not configured in local-ci.yml", runsOn))
		}
		return fin("not_run_locally", fmt.Sprintf("runs-on %q is set to skip in local-ci.yml", runsOn))
	}
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return fin("cancelled", "cancelled before it started")
	}
	if ctx.Err() != nil {
		return fin("cancelled", "cancelled before it started")
	}
	in := &instance{r: r, job: job, jr: jr, combo: combo, key: key, label: label, runsOn: runsOn,
		runnerOS: runnerOSFor(runsOn), vars: vars, wfEnv: wfEnv, ghEnv: map[string]string{}, status: "success"}
	in.run(ctx)
	jr.End = r.now()
	r.logf("[%s] job %s (%s)", key, jr.Status, fmtDur(jr.End.Sub(jr.Start)))
	return jr
}

// ---------------------------------------------------------------- one job instance

type instance struct {
	r        *runState
	job      *Job
	jr       *JobResult
	combo    map[string]any
	key      string
	label    string
	runsOn   string
	runnerOS string
	vars     map[string]any
	wfEnv    map[string]string
	jobEnv   map[string]string
	ghEnv    map[string]string // appended to GITHUB_ENV by earlier steps
	pathAdd  []string          // GITHUB_PATH entries, first = highest priority
	steps    map[string]any
	status   string // success|failure|cancelled
	tmpDir   string
	summed   bool

	deadline     time.Time // the job-wide timeout-minutes deadline
	deadlineDesc string
}

func (in *instance) envCtx(extra map[string]string) map[string]any {
	m := map[string]any{}
	for k, v := range in.wfEnv {
		m[k] = v
	}
	for k, v := range in.jobEnv {
		m[k] = v
	}
	for k, v := range in.ghEnv {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func (in *instance) exprCtx(status string, env map[string]string) *ExprContext {
	v := make(map[string]any, len(in.vars)+3)
	for k, x := range in.vars {
		v[k] = x
	}
	v["env"] = in.envCtx(env)
	v["steps"] = in.steps
	v["job"] = map[string]any{"status": status}
	v["runner"] = map[string]any{
		"os": in.runnerOS, "arch": "X64", "name": "local", "temp": filepath.Join(in.tmpDir, "temp"),
		"tool_cache": filepath.Join(in.tmpDir, "toolcache"),
	}
	return &ExprContext{Vars: v, JobStatus: status, Workdir: in.r.o.Workdir}
}

func (in *instance) fail(reason string) {
	in.jr.Status, in.jr.Reason = "failure", reason
}

func (in *instance) run(ctx context.Context) {
	r, job := in.r, in.job
	in.tmpDir = filepath.Join(r.o.StateDir, "tmp", in.key)
	in.steps = map[string]any{}
	jobTimeout, jobDesc := 360*time.Minute, "360 minutes"
	if m := job.TimeoutMinutes; m > 0 {
		jobTimeout, jobDesc = time.Duration(m)*time.Minute, fmt.Sprintf("%d minutes", m)
	}
	if r.o.jobTimeoutOverride > 0 {
		jobTimeout, jobDesc = r.o.jobTimeoutOverride, r.o.jobTimeoutOverride.String()
	}
	in.deadline, in.deadlineDesc = time.Now().Add(jobTimeout), jobDesc
	if err := os.MkdirAll(filepath.Join(in.tmpDir, "temp"), 0o750); err != nil {
		in.fail(err.Error())
		return
	}
	var err error
	in.jobEnv, err = interpEnv(job.Env, in.exprCtx("success", nil))
	if err != nil {
		in.fail("job " + err.Error())
		return
	}
	for i, st := range job.Steps {
		in.step(ctx, i+1, st)
	}
	switch {
	case in.status == "failure":
		in.jr.Status = "failure"
	case in.status == "cancelled" || ctx.Err() != nil:
		in.jr.Status = "cancelled"
	default:
		in.jr.Status = "success"
	}
	// job outputs are published whatever the job status, so an if: always() job can read needs.x.outputs
	if len(job.Outputs) > 0 {
		in.jr.Outputs = map[string]string{}
		ec := in.exprCtx(in.jr.Status, nil)
		for name, expr := range job.Outputs {
			v, err := Interpolate(expr, ec)
			if err != nil {
				msg := "outputs." + name + ": " + err.Error()
				if in.jr.Status == "success" {
					in.fail(msg)
				} else if in.jr.Reason == "" {
					in.jr.Reason = msg
				}
				break
			}
			in.jr.Outputs[name] = v
		}
	}
}

func (in *instance) defaultShell() string {
	if in.runnerOS == "Windows" {
		return "pwsh"
	}
	return "bash"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (in *instance) step(ctx context.Context, n int, st *Step) {
	r := in.r
	total := len(in.job.Steps)
	sr := StepResult{Index: n, ID: st.ID, Start: r.now()}
	status := in.status
	if ctx.Err() != nil {
		status = "cancelled"
	}
	finish := func() {
		sr.End = r.now()
		in.jr.Steps = append(in.jr.Steps, sr)
		if st.ID != "" {
			entry, _ := in.steps[st.ID].(map[string]any)
			if entry == nil {
				entry = map[string]any{"outputs": map[string]any{}}
			}
			entry["outcome"], entry["conclusion"] = sr.Outcome, sr.Conclusion
			in.steps[st.ID] = entry
		}
		verdict := sr.Conclusion
		switch {
		case sr.Conclusion == "skipped":
			verdict = "skipped"
		case sr.Outcome == "failure" && sr.Conclusion == "success":
			verdict = "failed (continue-on-error)"
		case sr.Conclusion == "success":
			verdict = "ok"
		case sr.Conclusion == "failure":
			verdict = "FAILED"
		}
		msg := ""
		if sr.Err != "" {
			msg = ": " + firstLine(sr.Err)
		}
		r.logf("[%s] step %d/%d %s ... %s (%s)%s", in.key, n, total, sr.Name, verdict, fmtDur(sr.End.Sub(sr.Start)), msg)
	}
	ec := in.exprCtx(status, nil)
	name, nerr := Interpolate(st.Name, ec)
	if nerr != nil || strings.TrimSpace(name) == "" {
		switch {
		case st.Name != "" && nerr == nil:
			name = st.Uses + st.Run
		case st.Name != "":
			name = st.Name
		case st.Run != "":
			name = "Run " + firstLine(st.Run)
		default:
			name = st.Uses
		}
	}
	sr.Name = name
	stepFail := func(msg string) {
		sr.Err = r.mask(msg)
		sr.Outcome, sr.Conclusion = "failure", "failure"
		in.status = "failure"
	}
	ok, err := EvalCond(st.If, ec)
	if err != nil {
		stepFail("if: " + err.Error())
		finish()
		return
	}
	if !ok {
		sr.Outcome, sr.Conclusion = "skipped", "skipped"
		finish()
		return
	}
	// step environment and the other interpolated fields
	stepEnv, err := interpEnv(st.Env, ec)
	if extra := r.o.Config.Override(r.file, in.job.ID).StepEnv[st.Name]; len(extra) > 0 && err == nil {
		if stepEnv == nil {
			stepEnv = map[string]string{}
		}
		for k, v := range extra {
			stepEnv[k] = v
		}
	}
	if err != nil {
		stepFail(err.Error())
		finish()
		return
	}
	ec = in.exprCtx(status, stepEnv)
	coe := false
	if st.ContinueOnError != "" {
		v, err := Interpolate(st.ContinueOnError, ec)
		coe = err == nil && strings.TrimSpace(v) == "true"
	}
	runCtx := ctx
	if ctx.Err() != nil {
		runCtx = context.WithoutCancel(ctx) // an always()/cancelled() step still runs
	}
	timeout := time.Duration(360) * time.Minute
	desc := "360 minutes"
	if m := st.TimeoutMinutes; m > 0 {
		timeout, desc = time.Duration(m)*time.Minute, fmt.Sprintf("%d minutes", m)
	}
	if r.o.stepTimeoutOverride > 0 {
		timeout, desc = r.o.stepTimeoutOverride, r.o.stepTimeoutOverride.String()
	}
	// the job timeout is one deadline for the whole job; a step gets the smaller of its own limit and what is left
	if rem := time.Until(in.deadline); rem <= 0 {
		if in.status == "success" {
			stepFail("job timed out after " + in.deadlineDesc)
			finish()
			return
		}
		// a cleanup step (always(), failure()) still runs after the deadline, bounded by its own limit
	} else if rem < timeout {
		timeout, desc = rem, "the job limit of "+in.deadlineDesc
	}
	stepCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()

	logDir := filepath.Join(r.o.StateDir, "logs", in.key)
	_ = os.MkdirAll(logDir, 0o750)
	sr.LogFile = filepath.Join(logDir, fmt.Sprintf("%02d-%s.log", n, slug(name)))
	lf, ferr := os.Create(sr.LogFile)
	if ferr != nil {
		stepFail(ferr.Error())
		finish()
		return
	}
	var lfMu sync.Mutex
	emit := func(line string) {
		if rest, ok := strings.CutPrefix(line, "::add-mask::"); ok {
			r.masks.add(rest)
			return
		}
		line = r.mask(line)
		lfMu.Lock()
		_, _ = fmt.Fprintln(lf, line)
		lfMu.Unlock()
	}
	lw := &lineWriter{emit: emit}

	// special files, truncated for every step
	files := map[string]string{}
	for _, f := range []string{"GITHUB_OUTPUT", "GITHUB_ENV", "GITHUB_PATH", "GITHUB_STEP_SUMMARY"} {
		p := filepath.Join(in.tmpDir, f)
		_ = os.WriteFile(p, nil, 0o600)
		files[f] = p
	}
	env := in.processEnv(stepEnv, files)

	var outputs map[string]string
	var runErr error
	if st.Uses != "" {
		with, werr := interpEnv(st.With, ec)
		if werr != nil {
			runErr = fmt.Errorf("with: %w", werr)
		} else {
			se := &shimEnv{With: with, Workdir: r.o.Workdir, StateDir: r.o.StateDir, TmpDir: in.tmpDir, RunnerOS: in.runnerOS,
				Env: env.items, GitHub: in.r.githubCtx(in.job.ID), Logf: func(f string, a ...any) { emit(fmt.Sprintf(f, a...)) }}
			outputs, runErr = shims[shimName(st.Uses)](stepCtx, se)
		}
	} else {
		runErr = in.runScript(stepCtx, st, ec, n, env, lw)
	}
	lw.Flush()
	_ = lf.Close()
	// special files
	if perr := in.applyFiles(files, st, &outputs, emit); perr != nil && runErr == nil {
		runErr = perr
	}
	if st.ID != "" {
		o := map[string]any{}
		for k, v := range outputs {
			o[k] = v
		}
		in.steps[st.ID] = map[string]any{"outputs": o}
	}
	switch {
	case runErr == nil:
		sr.Outcome, sr.Conclusion = "success", "success"
	case stepCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil:
		sr.Err = "timed out after " + desc
		sr.Outcome, sr.Conclusion = "failure", "failure"
	case ctx.Err() != nil:
		sr.Err = r.mask(runErr.Error())
		sr.Outcome, sr.Conclusion = "cancelled", "cancelled"
		in.status = "cancelled"
	default:
		sr.Err = r.mask(runErr.Error())
		sr.Outcome, sr.Conclusion = "failure", "failure"
	}
	if sr.Outcome == "failure" {
		if coe {
			sr.Conclusion = "success"
		} else {
			in.status = "failure"
		}
	}
	if sr.Err != "" {
		// keep the reason in the log file too
		if f, err := os.OpenFile(sr.LogFile, os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintln(f, "[ci] "+sr.Err)
			_ = f.Close()
		}
	}
	finish()
}

func (in *instance) processEnv(stepEnv map[string]string, files map[string]string) *envList {
	r := in.r
	g := r.o.GitHub
	e := newEnvList(r.o.Env)
	str := func(k string) string {
		if v, ok := g[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	kv := [][2]string{
		{"CI", "true"}, {"GITHUB_ACTIONS", "true"}, {"GITHUB_WORKSPACE", r.o.Workdir},
		{"GITHUB_REF", str("ref")}, {"GITHUB_REF_NAME", str("ref_name")}, {"GITHUB_SHA", str("sha")},
		{"GITHUB_EVENT_NAME", str("event_name")}, {"GITHUB_BASE_REF", str("base_ref")}, {"GITHUB_HEAD_REF", str("head_ref")},
		{"GITHUB_RUN_ID", str("run_id")}, {"GITHUB_RUN_NUMBER", str("run_number")}, {"GITHUB_RUN_ATTEMPT", str("run_attempt")},
		{"GITHUB_REPOSITORY", str("repository")}, {"GITHUB_WORKFLOW", r.wf.Name}, {"GITHUB_ACTOR", str("actor")},
		{"GITHUB_JOB", in.job.ID}, {"RUNNER_OS", in.runnerOS}, {"RUNNER_TEMP", filepath.Join(in.tmpDir, "temp")},
		{"RUNNER_ARCH", "X64"},
	}
	for _, p := range kv {
		e.set(p[0], p[1])
	}
	for _, m := range []map[string]string{in.wfEnv, in.jobEnv, in.ghEnv, stepEnv} {
		for k, v := range m {
			e.set(k, v)
		}
	}
	if len(in.pathAdd) > 0 {
		cur, _ := e.get("PATH")
		parts := append(append([]string{}, in.pathAdd...), cur)
		e.set("PATH", strings.Join(parts, string(os.PathListSeparator)))
	}
	for k, v := range files {
		e.set(k, v)
	}
	// never a token
	var items []string
	for _, kv := range e.items {
		if k, _, ok := splitKV(kv); ok && (strings.EqualFold(k, "GITHUB_TOKEN") || strings.EqualFold(k, "GH_TOKEN")) {
			continue
		}
		items = append(items, kv)
	}
	e.items = items
	return e
}

// applyFiles reads the special files after a step.
func (in *instance) applyFiles(files map[string]string, st *Step, outputs *map[string]string, emit func(string)) error {
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if data, err := os.ReadFile(files["GITHUB_OUTPUT"]); err == nil && len(data) > 0 {
		pairs, perr := parseCommandFile(string(data))
		note(wrapFile("GITHUB_OUTPUT", perr))
		if *outputs == nil {
			*outputs = map[string]string{}
		}
		for _, p := range pairs {
			(*outputs)[p.Key] = p.Value
		}
	}
	if data, err := os.ReadFile(files["GITHUB_ENV"]); err == nil && len(data) > 0 {
		pairs, perr := parseCommandFile(string(data))
		note(wrapFile("GITHUB_ENV", perr))
		for _, p := range pairs {
			in.ghEnv[p.Key] = p.Value
		}
	}
	if data, err := os.ReadFile(files["GITHUB_PATH"]); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				in.pathAdd = append([]string{l}, in.pathAdd...)
			}
		}
	}
	if data, err := os.ReadFile(files["GITHUB_STEP_SUMMARY"]); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		in.r.sumMu.Lock()
		defer in.r.sumMu.Unlock()
		f, err := os.OpenFile(filepath.Join(in.r.o.StateDir, "summary.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			note(err)
		} else {
			if !in.summed {
				_, _ = fmt.Fprintf(f, "## %s\n\n", in.label)
				in.summed = true
			}
			text := in.r.mask(string(data))
			if !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			_, _ = fmt.Fprintln(f, text)
			if cerr := f.Close(); cerr != nil {
				note(cerr)
			}
		}
	}
	return firstErr
}

func wrapFile(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("unable to process %s: %w", name, err)
}

// runScript interpolates and runs a `run:` step.
func (in *instance) runScript(ctx context.Context, st *Step, ec *ExprContext, n int, env *envList, lw *lineWriter) error {
	r := in.r
	script, err := Interpolate(st.Run, ec)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	shell := firstNonEmpty(st.Shell, in.job.Defaults.Shell, r.wf.Defaults.Shell, in.defaultShell())
	wdRaw := firstNonEmpty(st.WorkingDirectory, in.job.Defaults.WorkingDirectory, r.wf.Defaults.WorkingDirectory)
	wd, err := Interpolate(wdRaw, ec)
	if err != nil {
		return fmt.Errorf("working-directory: %w", err)
	}
	dir := r.o.Workdir
	if wd != "" {
		dir = wd
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(r.o.Workdir, filepath.FromSlash(wd))
		}
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("working-directory %s does not exist", dir)
	}
	sc, err := writeScript(shell, script, in.tmpDir, fmt.Sprintf("step-%02d", n))
	if err != nil {
		return err
	}
	cmd := exec.Command(sc.Path, sc.Args...) //nolint:gosec // running the workflow command is the point of the runner
	cmd.Dir = dir
	cmd.Env = env.items
	cmd.Stdout, cmd.Stderr = lw, lw
	cmd.WaitDelay = 3 * time.Second
	if r.o.waitDelayOverride > 0 {
		cmd.WaitDelay = r.o.waitDelayOverride
	}
	started, werr := runTree(ctx, cmd, sc.RawLine)
	if !started {
		return fmt.Errorf("cannot start %s: %w", shell, werr)
	}
	if werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			return fmt.Errorf("process completed with exit code %d", ee.ExitCode())
		}
		return werr
	}
	return nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- dry run

func (r *runState) plan(levels [][]string, only map[string]bool, res *RunResult) {
	r.logf("plan for %s (%s)", r.wf.Name, r.wf.File)
	for li, level := range levels {
		for _, id := range level {
			if len(only) > 0 && !only[id] {
				continue
			}
			job := r.wf.JobByID(id)
			combos := r.comboList(job)
			keys, labels := r.instanceKeys(job, combos)
			for i, combo := range combos {
				jr := &JobResult{Key: keys[i], ID: id, Label: labels[i], Status: "planned"}
				if len(combo) > 0 {
					jr.Matrix = combo
				}
				vars := r.baseVars(job, combo)
				ec := &ExprContext{Vars: vars, Workdir: r.o.Workdir}
				runsOn, err := Interpolate(job.RunsOn, ec)
				if err != nil {
					runsOn = job.RunsOn
				}
				mode, _ := r.o.Config.Resolve(runsOn)
				if ov := r.o.Config.Override(r.file, id); ov.Skip != "" {
					mode = "skip: " + ov.Skip
					jr.Status, jr.Reason = "not_run_locally", ov.Skip
				} else if mode == "skip" {
					jr.Status, jr.Reason = "not_run_locally", "runs-on "+runsOn+" is skipped"
				}
				needs := ""
				if len(job.Needs) > 0 {
					needs = " needs " + strings.Join(job.Needs, ",")
				}
				r.logf("level %d job %s: %s runs-on %s (%s)%s", li, id, labels[i], runsOn, mode, needs)
				for si, st := range job.Steps {
					name, err := Interpolate(st.Name, ec)
					if err != nil || name == "" {
						name = st.Name
					}
					kind := ""
					switch {
					case st.Uses != "":
						kind = "uses " + shimName(st.Uses)
						name = firstNonEmpty(name, shimName(st.Uses))
					default:
						sh := firstNonEmpty(st.Shell, job.Defaults.Shell, r.wf.Defaults.Shell, map[bool]string{true: "pwsh", false: "bash"}[runnerOSFor(runsOn) == "Windows"])
						kind = "run " + sh
						name = firstNonEmpty(name, "Run "+firstLine(st.Run))
					}
					r.logf("    step %d: %s [%s]", si+1, name, kind)
					jr.Steps = append(jr.Steps, StepResult{Index: si + 1, ID: st.ID, Name: name + " [" + kind + "]"})
				}
				res.Jobs = append(res.Jobs, jr)
			}
		}
	}
}
