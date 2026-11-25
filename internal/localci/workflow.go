package localci

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Defaults is a defaults.run block.
type Defaults struct{ Shell, WorkingDirectory string }

// Concurrency is a workflow-level concurrency block.
type Concurrency struct {
	Group            string
	CancelInProgress string // may be an expression
}

// TriggerSpec is one entry of `on`.
type TriggerSpec struct {
	Branches, Paths []string
	Cron            []string
	Line            int
}

// InputSpec is a workflow_dispatch input.
type InputSpec struct {
	Type, Default string
	Required      bool
	Description   string
	Options       []string
}

// Workflow is a parsed workflow file.
type Workflow struct {
	File, Name  string
	On          map[string]TriggerSpec
	Env         map[string]string
	Defaults    Defaults
	Concurrency *Concurrency
	Jobs        []*Job
	Inputs      map[string]InputSpec
}

// Job is one job of a workflow. RunsOn is raw and may be an expression.
type Job struct {
	ID              string
	Name            string
	RunsOn          string
	Needs           []string
	If              string
	Env             map[string]string
	Defaults        Defaults
	TimeoutMinutes  int
	ContinueOnError string
	Strategy        *Strategy
	Outputs         map[string]string
	Steps           []*Step
	Line            int
}

// Strategy is a job strategy.
type Strategy struct {
	FailFast    bool
	Matrix      MatrixSpec
	MaxParallel int
}

// MatrixSpec is a strategy.matrix block with list axes.
type MatrixSpec struct {
	Axes      map[string][]any
	AxisOrder []string
	Include   []map[string]any
	Exclude   []map[string]any
}

// Step is one step of a job.
type Step struct {
	ID               string
	Name             string
	If               string
	Run              string
	Shell            string
	WorkingDirectory string
	Uses             string
	With             map[string]string
	Env              map[string]string
	ContinueOnError  string
	TimeoutMinutes   int
	Line             int
}

// Unsupported is something the runner does not know how to run.
type Unsupported struct {
	Thing, File string
	Line        int
}

func (u Unsupported) Error() string {
	return fmt.Sprintf("unsupported: %s at %s:%d", u.Thing, u.File, u.Line)
}

var allowedUses = setOf(
	"actions/checkout",
	"actions/setup-go",
	"actions/setup-node",
	"actions/cache",
	"actions/cache/restore",
	"actions/cache/save",
	"actions/upload-artifact",
	"actions/download-artifact",
	"trufflesecurity/trufflehog",
)

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, i := range items {
		m[i] = true
	}
	return m
}

var allowedShells = setOf("bash", "pwsh", "powershell", "cmd", "sh")

type wfParser struct {
	file string
	uns  []Unsupported
	err  error
}

type kv struct {
	key  string
	k, v *yaml.Node
}

func (p *wfParser) unsupported(line int, format string, a ...any) {
	p.uns = append(p.uns, Unsupported{Thing: fmt.Sprintf(format, a...), File: p.file, Line: line})
}

func (p *wfParser) fail(line int, format string, a ...any) {
	if p.err == nil {
		p.err = fmt.Errorf("%s:%d: %s", p.file, line, fmt.Sprintf(format, a...))
	}
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func nodeLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Line
}

func isNull(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

// pairs returns the ordered key/value pairs of a mapping (null = empty). Duplicate keys and
// non-scalar keys are structural errors.
func (p *wfParser) pairs(n *yaml.Node, ctx string) []kv {
	n = deref(n)
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		p.fail(n.Line, "%s must be a mapping", ctx)
		return nil
	}
	var out []kv
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := deref(n.Content[i]), n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			p.fail(k.Line, "%s: keys must be scalars", ctx)
			continue
		}
		if seen[k.Value] {
			p.fail(k.Line, "%s: duplicate key %q", ctx, k.Value)
			continue
		}
		seen[k.Value] = true
		out = append(out, kv{k.Value, k, v})
	}
	return out
}

// checkExprs validates every ${{ }} of s (or the whole of s when wholeIsExpr and there is none).
func (p *wfParser) checkExprs(line int, s string, wholeIsExpr bool) {
	exprs := ExprsIn(s)
	if len(exprs) == 0 && wholeIsExpr && strings.TrimSpace(s) != "" {
		exprs = []string{strings.TrimSpace(s)}
	}
	for _, e := range exprs {
		if err := CheckExpr(e); err != nil {
			p.unsupported(line, "expression %s: %v", e, err)
		}
	}
}

func (p *wfParser) str(n *yaml.Node, ctx string) string { return p.strx(n, ctx, false) }

func (p *wfParser) strx(n *yaml.Node, ctx string, wholeIsExpr bool) string {
	n = deref(n)
	if isNull(n) {
		return ""
	}
	if n.Kind != yaml.ScalarNode {
		p.fail(n.Line, "%s must be a scalar", ctx)
		return ""
	}
	p.checkExprs(n.Line, n.Value, wholeIsExpr)
	return n.Value
}

func (p *wfParser) strList(n *yaml.Node, ctx string) []string {
	n = deref(n)
	if isNull(n) {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		p.checkExprs(n.Line, n.Value, false)
		return []string{n.Value}
	}
	if n.Kind != yaml.SequenceNode {
		p.fail(n.Line, "%s must be a string or a list", ctx)
		return nil
	}
	var out []string
	for _, c := range n.Content {
		out = append(out, p.str(c, ctx))
	}
	return out
}

func (p *wfParser) strMap(n *yaml.Node, ctx string) map[string]string {
	n = deref(n)
	if n != nil && n.Kind == yaml.ScalarNode && !isNull(n) {
		p.unsupported(n.Line, "%s as a single expression", ctx)
		return nil
	}
	out := map[string]string{}
	for _, e := range p.pairs(n, ctx) {
		out[e.key] = p.str(e.v, ctx+"."+e.key)
	}
	return out
}

func (p *wfParser) intVal(n *yaml.Node, ctx string) int {
	n = deref(n)
	if isNull(n) || n.Kind != yaml.ScalarNode {
		p.fail(nodeLine(n), "%s must be an integer", ctx)
		return 0
	}
	if strings.Contains(n.Value, "${{") {
		p.unsupported(n.Line, "expression in %s", ctx)
		return 0
	}
	v, err := strconv.Atoi(n.Value)
	if err != nil {
		p.fail(n.Line, "%s must be an integer, got %q", ctx, n.Value)
	}
	return v
}

func (p *wfParser) boolVal(n *yaml.Node, ctx string) (bool, bool) {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		p.fail(nodeLine(n), "%s must be a boolean", ctx)
		return false, false
	}
	switch n.Value {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	if strings.Contains(n.Value, "${{") {
		p.unsupported(n.Line, "expression in %s", ctx)
		return false, false
	}
	p.fail(n.Line, "%s must be true or false, got %q", ctx, n.Value)
	return false, false
}

func (p *wfParser) shell(n *yaml.Node) string {
	s := p.str(n, "shell")
	if s != "" && !allowedShells[s] && !strings.Contains(s, "${{") {
		p.unsupported(deref(n).Line, "shell %s", s)
	}
	return s
}

func (p *wfParser) defaults(n *yaml.Node) Defaults {
	var d Defaults
	for _, e := range p.pairs(n, "defaults") {
		if e.key != "run" {
			p.unsupported(e.k.Line, "defaults key %s", e.key)
			continue
		}
		for _, r := range p.pairs(e.v, "defaults.run") {
			switch r.key {
			case "shell":
				d.Shell = p.shell(r.v)
			case "working-directory":
				d.WorkingDirectory = p.str(r.v, "working-directory")
			default:
				p.unsupported(r.k.Line, "defaults.run key %s", r.key)
			}
		}
	}
	return d
}

// ParseWorkflow parses a workflow file strictly. The error is for broken YAML or structural
// problems; the Unsupported list is everything the runner does not know.
func ParseWorkflow(file string, data []byte) (*Workflow, []Unsupported, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", file, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil, fmt.Errorf("%s: empty workflow", file)
	}
	p := &wfParser{file: file}
	root := deref(doc.Content[0])
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s:%d: workflow must be a mapping", file, root.Line)
	}
	w := &Workflow{File: file, On: map[string]TriggerSpec{}, Env: map[string]string{}, Inputs: map[string]InputSpec{}}
	haveJobs := false
	for _, e := range p.pairs(root, "workflow") {
		switch e.key {
		case "name":
			w.Name = p.str(e.v, "name")
		case "on":
			p.parseOn(w, e.v)
		case "env":
			w.Env = p.strMap(e.v, "env")
		case "defaults":
			w.Defaults = p.defaults(e.v)
		case "concurrency":
			w.Concurrency = p.concurrency(e.v)
		case "permissions":
			// accepted and ignored: the local runner has no token scopes
		case "jobs":
			haveJobs = true
			p.parseJobs(w, e.v)
		default:
			p.unsupported(e.k.Line, "top-level key %s", e.key)
		}
	}
	if p.err == nil && !haveJobs {
		p.fail(root.Line, "workflow has no jobs")
	}
	if w.Name == "" {
		w.Name = filepath.Base(file)
	}
	sort.SliceStable(p.uns, func(i, j int) bool { return p.uns[i].Line < p.uns[j].Line })
	if p.err != nil {
		return nil, p.uns, p.err
	}
	return w, p.uns, nil
}

func (p *wfParser) concurrency(n *yaml.Node) *Concurrency {
	n = deref(n)
	if n != nil && n.Kind == yaml.ScalarNode {
		return &Concurrency{Group: p.str(n, "concurrency")}
	}
	c := &Concurrency{}
	for _, e := range p.pairs(n, "concurrency") {
		switch e.key {
		case "group":
			c.Group = p.str(e.v, "concurrency.group")
		case "cancel-in-progress":
			c.CancelInProgress = p.str(e.v, "concurrency.cancel-in-progress")
		default:
			p.unsupported(e.k.Line, "concurrency key %s", e.key)
		}
	}
	return c
}

func (p *wfParser) parseOn(w *Workflow, n *yaml.Node) {
	n = deref(n)
	type ev struct {
		name string
		k, v *yaml.Node
	}
	var evs []ev
	switch {
	case n.Kind == yaml.ScalarNode:
		evs = append(evs, ev{n.Value, n, nil})
	case n.Kind == yaml.SequenceNode:
		for _, c := range n.Content {
			c = deref(c)
			if c.Kind != yaml.ScalarNode {
				p.fail(c.Line, "on: events must be strings")
				continue
			}
			evs = append(evs, ev{c.Value, c, nil})
		}
	default:
		for _, e := range p.pairs(n, "on") {
			evs = append(evs, ev{e.key, e.k, e.v})
		}
	}
	for _, e := range evs {
		switch e.name {
		case "push", "pull_request":
			ts := TriggerSpec{Line: e.k.Line}
			for _, f := range p.pairs(e.v, "on."+e.name) {
				switch f.key {
				case "branches":
					ts.Branches = p.strList(f.v, "branches")
				case "paths":
					ts.Paths = p.strList(f.v, "paths")
				case "paths-ignore", "types", "tags", "branches-ignore", "tags-ignore":
					p.strList(f.v, f.key) // accepted, not used locally
				default:
					p.unsupported(f.k.Line, "%s filter %s", e.name, f.key)
				}
			}
			w.On[e.name] = ts
		case "schedule":
			ts := TriggerSpec{Line: e.k.Line}
			sn := deref(e.v)
			if isNull(sn) || sn.Kind != yaml.SequenceNode {
				p.fail(e.k.Line, "on.schedule must be a list of cron entries")
				break
			}
			for _, item := range sn.Content {
				for _, f := range p.pairs(item, "on.schedule entry") {
					if f.key != "cron" {
						p.unsupported(f.k.Line, "schedule key %s", f.key)
						continue
					}
					ts.Cron = append(ts.Cron, p.str(f.v, "cron"))
				}
			}
			w.On[e.name] = ts
		case "workflow_dispatch":
			w.On[e.name] = TriggerSpec{Line: e.k.Line}
			for _, f := range p.pairs(e.v, "on.workflow_dispatch") {
				if f.key != "inputs" {
					p.unsupported(f.k.Line, "workflow_dispatch key %s", f.key)
					continue
				}
				for _, in := range p.pairs(f.v, "inputs") {
					var spec InputSpec
					for _, a := range p.pairs(in.v, "input "+in.key) {
						switch a.key {
						case "description":
							spec.Description = p.str(a.v, "description")
						case "required":
							spec.Required, _ = p.boolVal(a.v, "required")
						case "default":
							spec.Default = p.str(a.v, "default")
						case "type":
							spec.Type = p.str(a.v, "type")
						case "options":
							spec.Options = p.strList(a.v, "options")
						default:
							p.unsupported(a.k.Line, "input key %s", a.key)
						}
					}
					w.Inputs[in.key] = spec
				}
			}
		default:
			p.unsupported(e.k.Line, "event %s", e.name)
		}
	}
}

func (p *wfParser) parseJobs(w *Workflow, n *yaml.Node) {
	for _, e := range p.pairs(n, "jobs") {
		j := &Job{ID: e.key, Line: e.k.Line}
		hasUses, hasRunsOn := false, false
		for _, f := range p.pairs(e.v, "job "+e.key) {
			ctx := "job " + e.key + "." + f.key
			switch f.key {
			case "name":
				j.Name = p.str(f.v, ctx)
			case "runs-on":
				hasRunsOn = true
				j.RunsOn = p.runsOn(f.v, ctx)
			case "needs":
				j.Needs = p.strList(f.v, ctx)
			case "if":
				j.If = p.strx(f.v, ctx, true)
			case "env":
				j.Env = p.strMap(f.v, ctx)
			case "defaults":
				j.Defaults = p.defaults(f.v)
			case "timeout-minutes":
				j.TimeoutMinutes = p.intVal(f.v, ctx)
			case "continue-on-error":
				j.ContinueOnError = p.str(f.v, ctx)
			case "strategy":
				j.Strategy = p.strategy(f.v)
			case "outputs":
				j.Outputs = p.strMap(f.v, ctx)
			case "steps":
				j.Steps = p.steps(f.v, e.key)
			case "permissions":
				// accepted and ignored
			case "uses":
				hasUses = true
				p.unsupported(f.k.Line, "job key uses (reusable workflow)")
			default:
				p.unsupported(f.k.Line, "job key %s", f.key)
			}
		}
		if !hasRunsOn && !hasUses {
			p.fail(e.k.Line, "job %s has no runs-on", e.key)
		}
		w.Jobs = append(w.Jobs, j)
	}
}

func (p *wfParser) runsOn(n *yaml.Node, ctx string) string {
	n = deref(n)
	switch n.Kind {
	case yaml.ScalarNode:
		return p.str(n, ctx)
	case yaml.SequenceNode:
		if len(n.Content) == 1 {
			return p.str(n.Content[0], ctx)
		}
		p.unsupported(n.Line, "runs-on list with %d labels", len(n.Content))
	default:
		p.unsupported(n.Line, "runs-on as a mapping (group/labels)")
	}
	return ""
}

func (p *wfParser) strategy(n *yaml.Node) *Strategy {
	s := &Strategy{FailFast: true}
	for _, e := range p.pairs(n, "strategy") {
		switch e.key {
		case "fail-fast":
			if v, ok := p.boolVal(e.v, "fail-fast"); ok {
				s.FailFast = v
			}
		case "max-parallel":
			s.MaxParallel = p.intVal(e.v, "max-parallel")
		case "matrix":
			s.Matrix = p.matrix(e.v)
		default:
			p.unsupported(e.k.Line, "strategy key %s", e.key)
		}
	}
	return s
}

func (p *wfParser) matrix(n *yaml.Node) MatrixSpec {
	m := MatrixSpec{Axes: map[string][]any{}}
	n = deref(n)
	if n != nil && n.Kind == yaml.ScalarNode && !isNull(n) {
		p.unsupported(n.Line, "matrix as an expression")
		return m
	}
	for _, e := range p.pairs(n, "matrix") {
		v := deref(e.v)
		switch e.key {
		case "include", "exclude":
			if v.Kind == yaml.ScalarNode {
				p.unsupported(v.Line, "matrix %s as an expression", e.key)
				continue
			}
			if v.Kind != yaml.SequenceNode {
				p.fail(v.Line, "matrix.%s must be a list", e.key)
				continue
			}
			for _, item := range v.Content {
				var entry map[string]any
				if err := item.Decode(&entry); err != nil {
					p.fail(item.Line, "matrix.%s entry: %v", e.key, err)
					continue
				}
				if e.key == "include" {
					m.Include = append(m.Include, entry)
				} else {
					m.Exclude = append(m.Exclude, entry)
				}
			}
		default:
			if v.Kind == yaml.ScalarNode {
				p.unsupported(v.Line, "matrix axis %s as an expression", e.key)
				continue
			}
			if v.Kind != yaml.SequenceNode {
				p.fail(v.Line, "matrix axis %s must be a list", e.key)
				continue
			}
			var vals []any
			if err := v.Decode(&vals); err != nil {
				p.fail(v.Line, "matrix axis %s: %v", e.key, err)
				continue
			}
			m.Axes[e.key] = vals
			m.AxisOrder = append(m.AxisOrder, e.key)
		}
	}
	return m
}

func (p *wfParser) steps(n *yaml.Node, jobID string) []*Step {
	n = deref(n)
	if isNull(n) {
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		p.fail(n.Line, "job %s: steps must be a list", jobID)
		return nil
	}
	var out []*Step
	for _, sn := range n.Content {
		sn = deref(sn)
		st := &Step{Line: sn.Line}
		hasRun, hasUses := false, false
		for _, f := range p.pairs(sn, "step") {
			ctx := "step " + f.key
			switch f.key {
			case "id":
				st.ID = p.str(f.v, ctx)
			case "name":
				st.Name = p.str(f.v, ctx)
			case "if":
				st.If = p.strx(f.v, ctx, true)
			case "run":
				hasRun = true
				st.Run = p.str(f.v, ctx)
			case "shell":
				st.Shell = p.shell(f.v)
			case "working-directory":
				st.WorkingDirectory = p.str(f.v, ctx)
			case "env":
				st.Env = p.strMap(f.v, ctx)
			case "with":
				st.With = p.strMap(f.v, ctx)
			case "continue-on-error":
				st.ContinueOnError = p.str(f.v, ctx)
			case "timeout-minutes":
				st.TimeoutMinutes = p.intVal(f.v, ctx)
			case "uses":
				hasUses = true
				st.Uses = p.str(f.v, ctx)
				name := st.Uses
				if i := strings.Index(name, "@"); i >= 0 {
					name = name[:i]
				}
				name = strings.TrimSpace(name)
				if !allowedUses[name] {
					p.unsupported(f.k.Line, "uses %s", name)
				}
			default:
				p.unsupported(f.k.Line, "step key %s", f.key)
			}
		}
		switch {
		case hasRun && hasUses:
			p.unsupported(sn.Line, "step with both run and uses")
		case !hasRun && !hasUses:
			p.unsupported(sn.Line, "step with neither run nor uses")
		}
		out = append(out, st)
	}
	return out
}

// JobByID returns the job with the given id, or nil.
func (w *Workflow) JobByID(id string) *Job {
	for _, j := range w.Jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func valuesEqual(a, b any) bool {
	return reflect.DeepEqual(a, b) || fmt.Sprint(a) == fmt.Sprint(b)
}

func matchesAll(combo, filter map[string]any, only func(string) bool) bool {
	for k, v := range filter {
		if only != nil && !only(k) {
			continue
		}
		cv, ok := combo[k]
		if !ok || !valuesEqual(cv, v) {
			return false
		}
	}
	return true
}

// ExpandMatrix returns the matrix combinations: the cartesian product of the axes in axis order,
// minus excludes, then includes (an include whose original-axis keys all match a combination adds
// its other keys to it, otherwise it becomes a new combination). An empty matrix gives one empty
// combination.
func ExpandMatrix(m MatrixSpec) []map[string]any {
	order := m.AxisOrder
	if len(order) != len(m.Axes) {
		order = nil
		for k := range m.Axes {
			order = append(order, k)
		}
		sort.Strings(order)
	}
	if len(order) == 0 && len(m.Include) == 0 {
		return []map[string]any{{}}
	}
	var base []map[string]any
	if len(order) > 0 {
		base = []map[string]any{{}}
		for _, ax := range order {
			var next []map[string]any
			for _, c := range base {
				for _, v := range m.Axes[ax] {
					nc := make(map[string]any, len(c)+1)
					for k, cv := range c {
						nc[k] = cv
					}
					nc[ax] = v
					next = append(next, nc)
				}
			}
			base = next
		}
	}
	isAxis := func(k string) bool { _, ok := m.Axes[k]; return ok }
	var kept []map[string]any
	for _, c := range base {
		excluded := false
		for _, ex := range m.Exclude {
			if len(ex) > 0 && matchesAll(c, ex, nil) {
				excluded = true
				break
			}
		}
		if !excluded {
			kept = append(kept, c)
		}
	}
	original := make([]map[string]any, len(kept))
	for i, c := range kept {
		original[i] = make(map[string]any, len(c))
		for k, v := range c {
			original[i][k] = v
		}
	}
	for _, inc := range m.Include {
		matched := false
		for i, orig := range original {
			if !matchesAll(orig, inc, isAxis) {
				continue
			}
			matched = true
			for k, v := range inc {
				if !isAxis(k) {
					kept[i][k] = v
				}
			}
		}
		if !matched {
			nc := make(map[string]any, len(inc))
			for k, v := range inc {
				nc[k] = v
			}
			kept = append(kept, nc)
		}
	}
	return kept
}

// MatrixLabel is the display name of a job instance: the job name (or id) plus the combination
// values in parentheses, in axis order (keys from includes follow, sorted).
func MatrixLabel(job *Job, combo map[string]any) string {
	name := job.Name
	if name == "" {
		name = job.ID
	}
	var keys []string
	seen := map[string]bool{}
	if job.Strategy != nil {
		for _, k := range job.Strategy.Matrix.AxisOrder {
			if _, ok := combo[k]; ok {
				keys = append(keys, k)
				seen[k] = true
			}
		}
	}
	var rest []string
	for k := range combo {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	if len(keys) == 0 {
		return name
	}
	vals := make([]string, len(keys))
	for i, k := range keys {
		vals[i] = fmt.Sprint(combo[k])
	}
	return name + " (" + strings.Join(vals, ", ") + ")"
}

// TopoLevels groups job ids by dependency level (level 0 has no needs). Order inside a level is
// file order. An unknown needs target or a cycle is an error.
func TopoLevels(w *Workflow) ([][]string, error) {
	idx := map[string]*Job{}
	for _, j := range w.Jobs {
		idx[j.ID] = j
	}
	for _, j := range w.Jobs {
		for _, n := range j.Needs {
			if idx[n] == nil {
				return nil, fmt.Errorf("job %s needs unknown job %s", j.ID, n)
			}
		}
	}
	level := map[string]int{}
	state := map[string]int{} // 1 visiting, 2 done
	var stack []string
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 2:
			return nil
		case 1:
			cyc := []string{id}
			for i := len(stack) - 1; i >= 0; i-- {
				cyc = append([]string{stack[i]}, cyc...)
				if stack[i] == id {
					break
				}
			}
			return fmt.Errorf("dependency cycle: %s", strings.Join(cyc, " -> "))
		}
		state[id] = 1
		stack = append(stack, id)
		lv := 0
		for _, n := range idx[id].Needs {
			if err := visit(n); err != nil {
				return err
			}
			if level[n]+1 > lv {
				lv = level[n] + 1
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		level[id] = lv
		return nil
	}
	maxLv := -1
	for _, j := range w.Jobs {
		if err := visit(j.ID); err != nil {
			return nil, err
		}
		if level[j.ID] > maxLv {
			maxLv = level[j.ID]
		}
	}
	out := make([][]string, maxLv+1)
	for _, j := range w.Jobs {
		out[level[j.ID]] = append(out[level[j.ID]], j.ID)
	}
	return out, nil
}
