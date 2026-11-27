package hsaction

import (
	"fmt"
	"strings"

	"api-audit-explorer/internal/localci"
)

func init() {
	register(
		define("machines.drain", "unravis-ci drain --name N", "Ask a machine to take nothing new, finish what runs, then exit.", machinesDrain),
		define("machines.resume", "unravis-ci machines resume <name>", "Withdraw a drain the machine has not acted on yet.", machinesResume),
		define("machines.allow", "unravis-ci machines allow <name> --project owner/name", "Let a machine run a project's requests.",
			func(env Env, a *MachineRuleArgs) step { return machineRule(env, a, true) }),
		define("machines.deny", "unravis-ci machines deny <name> --project owner/name", "Stop a machine from running a project's requests.",
			func(env Env, a *MachineRuleArgs) step { return machineRule(env, a, false) }),
		define("policy.set", "unravis-ci policy set <owner/name> --any|--only m1,m2|--except m1", "Set which machines may run a project.", policySet),
	)
}

// MachineArgs names one machine.
type MachineArgs struct {
	Name string `json:"name"`
}

// ---------------------------------------------------------------- machines.drain

// DrainLine is how the CLI and the page say a drain was asked.
func DrainLine(name string) string {
	return name + ": asked to drain (it takes nothing new, finishes what runs, then exits)"
}

func machinesDrain(env Env, a *MachineArgs) step {
	if !localci.ValidAgentName(a.Name) {
		st := step{res: Result{Status: Invalid, Field: "name", Message: fmt.Sprintf("%q is not an agent name", a.Name)}}
		return st
	}
	if err := localci.RequestDrain(env.Dir, a.Name); err != nil {
		st := fail(ioOr(err, Invalid), "%v", err)
		st.target = a.Name
		return st
	}
	return step{res: Result{Message: DrainLine(a.Name), Changed: true}, target: a.Name}
}

// ---------------------------------------------------------------- machines.resume

// Resumed is the Data of machines.resume.
type Resumed struct {
	Name    string
	Outcome string // resumed, not-running, cannot-resume, not-draining or unknown
	Cleared bool   // a drain mark was removed
}

func machinesResume(env Env, a *MachineArgs) step {
	if !localci.ValidAgentName(a.Name) {
		return step{res: Result{Status: Invalid, Field: "name", Message: fmt.Sprintf("%q is not an agent name", a.Name)}}
	}
	name := a.Name
	cleared, err := localci.ResumeAgent(env.Dir, name)
	if err != nil {
		st := fail(ioOr(err, Invalid), "%v", err)
		st.target = name
		return st
	}
	hbs, err := localci.ReadAgents(env.Dir)
	if err != nil {
		st := fail(Broken, "%v", err)
		st.target = name
		return st
	}
	var hb *localci.Heartbeat
	for i := range hbs {
		if hbs[i].Name == name {
			hb = &hbs[i]
		}
	}
	live := hb != nil && hb.Fresh(env.wall(), env.stale())
	d := Resumed{Name: name, Cleared: cleared}
	var text string
	st := Done
	switch {
	case cleared && live:
		d.Outcome, text = "resumed", name+": the drain is withdrawn; it takes requests again"
	case cleared:
		d.Outcome, text, st = "not-running", name+": the drain mark is cleared, but the machine is not running; start it again with unravis-ci serve", Failed
	case live && hb.State == "draining":
		d.Outcome, text, st = "cannot-resume", name+": it is draining because it was stopped in its own window (Ctrl-C); start it again with unravis-ci serve once it exits", Refused
	case hb != nil:
		d.Outcome, text = "not-draining", name+": it is not draining; nothing changed"
	default:
		d.Outcome, text, st = "unknown", name+": no machine of that name is known in "+env.Dir, Refused
	}
	return step{res: Result{Status: st, Message: text, Data: d, Changed: cleared}, target: name}
}

// ---------------------------------------------------------------- policy edits

// PolicyEdit is the Data of policy.set, machines.allow and machines.deny.
type PolicyEdit struct {
	Change localci.PolicyChange
	DryRun bool
	Notes  []string
}

// MachineRuleArgs is the arguments of machines.allow and machines.deny.
type MachineRuleArgs struct {
	Name    string `json:"name"`
	Project string `json:"project"` // owner/name
	// Confirm applies the edit even when it leaves queued runs with no machine (the CLI's --force).
	Confirm bool `json:"confirm,omitempty"`
	// DryRun only measures the edit.
	DryRun bool `json:"dryRun,omitempty"`
}

// PolicySetArgs is the arguments of policy.set.
type PolicySetArgs struct {
	Project  string   `json:"project"`            // owner/name
	Mode     string   `json:"mode"`               // any, only or except
	Machines []string `json:"machines,omitempty"` // for only and except
	Confirm  bool     `json:"confirm,omitempty"`
	DryRun   bool     `json:"dryRun,omitempty"`
}

const maxMachines = 64

func policySet(env Env, a *PolicySetArgs) step {
	if len(a.Machines) > maxMachines {
		return step{res: Result{Status: Invalid, Field: "machines", Message: fmt.Sprintf("at most %d machines", maxMachines)}}
	}
	var mode string
	switch strings.ToLower(strings.TrimSpace(a.Mode)) {
	case localci.PolicyAny:
		mode = localci.PolicyAny
	case localci.PolicyOnly:
		mode = localci.PolicyOnly
	case localci.PolicyExcept:
		mode = localci.PolicyExcept
	default:
		return step{res: Result{Status: Invalid, Field: "mode", Message: "the mode is one of any, only or except"}}
	}
	var names []string
	if mode != localci.PolicyAny {
		names = a.Machines
	}
	pp, err := localci.NewProjectPolicy(mode, names)
	if err != nil {
		return step{res: Result{Status: Invalid, Field: "machines", Message: err.Error()}}
	}
	return policyEdit(env, a.Project, func(localci.ProjectPolicy) (localci.ProjectPolicy, error) { return pp, nil }, a.Confirm, a.DryRun, nil)
}

func machineRule(env Env, a *MachineRuleArgs, allow bool) step {
	name := a.Name
	if !localci.ValidAgentName(name) {
		return step{res: Result{Status: Invalid, Field: "name", Message: fmt.Sprintf("%q is not a machine name", name)}}
	}
	hbs, err := localci.ReadAgents(env.Dir)
	if err != nil {
		return fail(Broken, "%v", err)
	}
	known := false
	for _, h := range hbs {
		known = known || strings.EqualFold(h.Name, name)
	}
	var notes []string
	if !known {
		notes = append(notes, fmt.Sprintf("no machine named %s is known in this folder yet; the rule applies when it starts", name))
	}
	edit := func(pp localci.ProjectPolicy) (localci.ProjectPolicy, error) {
		if allow {
			return pp.AllowMachine(name), nil
		}
		return pp.DenyMachine(name), nil
	}
	return policyEdit(env, a.Project, edit, a.Confirm, a.DryRun, notes)
}

// policyEdit runs one edit through localci.ChangePolicy, which measures it first and refuses one that strands queued
// runs unless confirm is set. A dry run is not logged; a refused or applied edit is.
func policyEdit(env Env, project string, edit func(localci.ProjectPolicy) (localci.ProjectPolicy, error), confirm, dry bool, notes []string) step {
	if _, ok := localci.ProjectKey(project); !ok {
		return step{res: Result{Status: Invalid, Field: "project", Message: fmt.Sprintf("%q is not a project (owner/name)", project)}}
	}
	ch, err := localci.ChangePolicy(env.Dir, project, edit, confirm, dry)
	if err != nil {
		st := fail(ioOr(err, Invalid), "%v", err)
		st.target = project
		return st
	}
	d := PolicyEdit{Change: ch, DryRun: dry, Notes: notes}
	res := Result{Data: d, Impact: impactOf(ch.Impact), DryRun: dry, Changed: ch.Applied}
	from, to := ch.Before.Describe(), ch.After.Describe()
	switch {
	case ch.Unchanged:
		res.Message = fmt.Sprintf("%s: already %s; nothing changed", ch.Project, to)
	case ch.Refused:
		res.Status = Refused
		res.Message = fmt.Sprintf("refused: %s (%s). Nothing changed. Confirm to apply it anyway; those runs then wait until the rule or the machines change.",
			ch.Impact.Line(), strings.Join(ch.Impact.Stranded, ", "))
	case !ch.Applied:
		res.Message = fmt.Sprintf("%s: would change %s -> %s; %s (dry run, nothing changed)", ch.Project, from, to, ch.Impact.Line())
	default:
		res.Message = fmt.Sprintf("%s: %s -> %s; %s", ch.Project, from, to, ch.Impact.Line())
		if len(ch.Impact.Stranded) > 0 {
			res.Message += fmt.Sprintf(". Confirmed: %s wait until the rule or the machines change", strings.Join(ch.Impact.Stranded, ", "))
		}
	}
	st := step{res: res, target: fmt.Sprintf("%s: %s -> %s", ch.Project, from, to), noLog: dry}
	if ch.Applied && len(ch.Impact.Stranded) > 0 {
		st.reason = "confirmed although " + ch.Impact.Line()
	}
	return st
}
