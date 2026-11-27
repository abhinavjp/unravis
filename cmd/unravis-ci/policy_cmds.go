package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"strings"
	"time"

	"api-audit-explorer/internal/hsaction"
	"api-audit-explorer/internal/localci"
)

// `unravis-ci policy ...` and `unravis-ci machines ...` set which machines may run a project's requests and show
// the machines of a state folder. Labels decide whether a machine fits a request; the policy decides whether it
// may take it. Every edit goes through localci.ChangePolicy, which measures the edit first and refuses one that
// would leave queued runs with no machine unless --force is given.

// ---------------------------------------------------------------- policy

func cmdPolicy(args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "policy: expected set or show")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "set":
		return cmdPolicySet(args[1:], stdout, stderr, environ)
	case "show":
		return cmdPolicyShow(args[1:], stdout, stderr, environ)
	}
	_, _ = fmt.Fprintf(stderr, "policy: unknown command %q (expected set or show)\n", args[0])
	usage(stderr)
	return exitUsage
}

// splitNames reads "a,b , c" as [a b c].
func splitNames(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdPolicySet(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("policy set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	anyM := fs.Bool("any", false, "any machine may run the project (the default)")
	only := fs.String("only", "", "only these machines may run it (comma separated)")
	except := fs.String("except", "", "any machine except these (comma separated)")
	force := fs.Bool("force", false, "apply the edit even when queued runs would have no machine")
	dry := fs.Bool("dry-run", false, "show the impact and change nothing")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "policy set: expected one project (owner/name)")
		return exitUsage
	}
	picked := 0
	for _, b := range []bool{*anyM, *only != "", *except != ""} {
		if b {
			picked++
		}
	}
	if picked != 1 {
		_, _ = fmt.Fprintln(stderr, "policy set: give exactly one of --any, --only m1,m2 or --except m1")
		return exitUsage
	}
	pa := hsaction.PolicySetArgs{Project: pos[0], Confirm: *force, DryRun: *dry}
	switch {
	case *anyM:
		pa.Mode = localci.PolicyAny
	case *only != "":
		pa.Mode, pa.Machines = localci.PolicyOnly, splitNames(*only)
	default:
		pa.Mode, pa.Machines = localci.PolicyExcept, splitNames(*except)
	}
	return applyPolicyEdit("policy set", dirOf(environ), "policy.set", pa, *asJSON, stdout, stderr, environ)
}

// applyPolicyEdit runs one policy action (policy.set, machines.allow or machines.deny) and prints the outcome:
// its notes as extra lines (text) or the "notes" field (JSON).
func applyPolicyEdit(cmd, dir, id string, args any, asJSON bool, stdout, stderr io.Writer, environ []string) int {
	env, err := cliEnv(dir, "", environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return exitUsage
	}
	res, code := runAction(env, id, args)
	d, ok := res.Data.(hsaction.PolicyEdit)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", cmd, errorText(res))
		return finishAction(cmd, res, code, stderr)
	}
	ch := d.Change
	if asJSON {
		type out struct {
			localci.PolicyChange
			DryRun bool     `json:"dryRun"`
			Line   string   `json:"line"`
			Notes  []string `json:"notes"`
		}
		if c := writeJSON(cmd, stdout, stderr, out{PolicyChange: ch, DryRun: d.DryRun, Line: ch.Impact.Line(), Notes: nonNil(d.Notes)}); c != exitOK {
			return c
		}
		return finishAction(cmd, res, code, stderr)
	}
	for _, n := range d.Notes {
		_, _ = fmt.Fprintln(stdout, "note:", n)
	}
	switch {
	case ch.Unchanged:
		_, _ = fmt.Fprintf(stdout, "%s: already %s; nothing changed\n", ch.Project, ch.After.Describe())
	case ch.Refused:
		_, _ = fmt.Fprintf(stderr, "%s: refused: %s (%s). Nothing changed. Add --force to apply it anyway.\n", cmd, ch.Impact.Line(), strings.Join(ch.Impact.Stranded, ", "))
		_, _ = fmt.Fprintf(stderr, "  %s: %s -> %s\n", ch.Project, ch.Before.Describe(), ch.After.Describe())
	case !ch.Applied:
		_, _ = fmt.Fprintf(stdout, "%s: would change %s -> %s; %s (dry run, nothing changed)\n", ch.Project, ch.Before.Describe(), ch.After.Describe(), ch.Impact.Line())
	default:
		_, _ = fmt.Fprintf(stdout, "%s: %s -> %s; %s\n", ch.Project, ch.Before.Describe(), ch.After.Describe(), ch.Impact.Line())
		if len(ch.Impact.Stranded) > 0 {
			_, _ = fmt.Fprintf(stdout, "  forced: %s wait until the rule or the machines change\n", strings.Join(ch.Impact.Stranded, ", "))
		}
	}
	return finishAction(cmd, res, code, stderr)
}

// policyShowJSON is the output of `policy show --json`. The field names are part of the command's interface;
// add fields, do not rename or drop them.
type policyShowJSON struct {
	StateDir string                `json:"stateDir"`
	Project  string                `json:"project"`
	Policy   localci.ProjectPolicy `json:"policy"`
	Text     string                `json:"text"`
	Machines []policyMachineJSON   `json:"machines"` // the machines this folder knows, and whether the rule allows them
	Queued   []queuedWhyJSON       `json:"queued"`   // the project's queued requests, in claim order, with why each waits
}

type policyMachineJSON struct {
	Name    string `json:"name"`
	Allowed bool   `json:"allowed"`
	State   string `json:"state"`
}

type queuedWhyJSON struct {
	ID    string             `json:"id"`
	Label string             `json:"label"`
	Why   localci.WaitReason `json:"why"`
}

func cmdPolicyShow(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("policy show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	asJSON := fs.Bool("json", false, "print the rule as JSON (see policyShowJSON)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "policy show: expected one project (owner/name)")
		return exitUsage
	}
	key, ok := localci.ProjectKey(pos[0])
	if !ok {
		_, _ = fmt.Fprintf(stderr, "policy show: %q is not a project (owner/name)\n", pos[0])
		return exitUsage
	}
	dir := dirOf(environ)
	now := time.Now()
	in, err := localci.WaitInputOf(dir, now, agentStale)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "policy show:", err)
		return exitUsage
	}
	all, err := (&localci.Queue{Dir: localci.QueueDir(dir)}).List()
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "policy show:", err)
		return exitUsage
	}
	rule := in.Policy.For(key)
	out := policyShowJSON{StateDir: dir, Project: key, Policy: rule, Text: rule.Describe(), Machines: []policyMachineJSON{}, Queued: []queuedWhyJSON{}}
	for _, h := range in.Agents {
		out.Machines = append(out.Machines, policyMachineJSON{Name: h.Name, Allowed: rule.Allows(h.Name), State: agentStateOf(h, now)})
	}
	for _, r := range all {
		if r.State == localci.StateQueued && strings.EqualFold(r.Repo, key) {
			out.Queued = append(out.Queued, queuedWhyJSON{ID: r.ID, Label: r.Label(), Why: localci.WhyWaiting(r, in)})
		}
	}
	if *asJSON {
		return writeJSON("policy show", stdout, stderr, out)
	}
	_, _ = fmt.Fprintf(stdout, "%s: %s\n", key, out.Text)
	if len(out.Machines) == 0 {
		_, _ = fmt.Fprintln(stdout, "machines: none known in this folder")
	}
	for _, m := range out.Machines {
		verdict := "denied"
		if m.Allowed {
			verdict = "allowed"
		}
		_, _ = fmt.Fprintf(stdout, "  %-20s %-8s %s\n", m.Name, verdict, m.State)
	}
	_, _ = fmt.Fprintf(stdout, "queued: %d\n", len(out.Queued))
	for _, q := range out.Queued {
		_, _ = fmt.Fprintf(stdout, "  %s  %s\n    waiting: %s\n", q.ID, q.Label, q.Why.Text)
	}
	return exitOK
}

// ---------------------------------------------------------------- machines

func cmdMachines(args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "list":
			return cmdMachinesList(args[1:], stdout, stderr, environ)
		case "allow":
			return cmdMachinesAllowDeny(true, args[1:], stdout, stderr, environ)
		case "deny":
			return cmdMachinesAllowDeny(false, args[1:], stdout, stderr, environ)
		case "resume":
			return cmdMachinesResume(args[1:], stdout, stderr, environ)
		}
		_, _ = fmt.Fprintf(stderr, "machines: unknown command %q (expected list, allow, deny or resume)\n", args[0])
		usage(stderr)
		return exitUsage
	}
	return cmdMachinesList(args, stdout, stderr, environ)
}

// machineJSON is one machine in `machines --json`. The field names are part of the command's interface; add
// fields, do not rename or drop them.
type machineJSON struct {
	Name       string    `json:"name"`
	State      string    `json:"state"` // ready, paused, draining, low-disk, or gone when the heartbeat is stale
	Labels     []string  `json:"labels"`
	Slots      int       `json:"slots"`
	Running    int       `json:"running"`
	LastSeen   time.Time `json:"lastSeen"`
	SeenAgo    string    `json:"seenAgo"`
	DrainAsked bool      `json:"drainAsked"` // a drain mark waits for it (machines resume clears it)
	// Denied are the projects whose policy does not allow this machine.
	Denied []string `json:"denied"`
}

type machinesJSON struct {
	StateDir string        `json:"stateDir"`
	Machines []machineJSON `json:"machines"`
}

func cmdMachinesList(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("machines", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	asJSON := fs.Bool("json", false, "print the machines as JSON (see machinesJSON)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "machines: takes no arguments")
		}
		return exitUsage
	}
	dir := dirOf(environ)
	now := time.Now()
	in, err := localci.WaitInputOf(dir, now, agentStale)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "machines:", err)
		return exitUsage
	}
	out := machinesJSON{StateDir: dir, Machines: []machineJSON{}}
	for _, h := range in.Agents {
		m := machineJSON{Name: h.Name, State: agentStateOf(h, now), Labels: nonNil(h.Labels), Slots: h.Slots, Running: len(h.Jobs),
			LastSeen: h.LastSeen, SeenAgo: ago(now, h.LastSeen), DrainAsked: in.DrainMarks[h.Name], Denied: []string{}}
		for p := range in.Policy.Projects {
			if !in.Policy.Allows(p, h.Name) {
				m.Denied = append(m.Denied, p)
			}
		}
		m.Denied = sortedStrings(m.Denied)
		out.Machines = append(out.Machines, m)
	}
	if *asJSON {
		return writeJSON("machines", stdout, stderr, out)
	}
	if len(out.Machines) == 0 {
		_, _ = fmt.Fprintln(stdout, "no machine is known in", dir)
		return exitOK
	}
	for _, m := range out.Machines {
		line := fmt.Sprintf("%-20s %-9s %d/%d running  seen %s%s", m.Name, m.State, m.Running, m.Slots, m.SeenAgo, labelText(m.Labels))
		if m.DrainAsked {
			line += "  (drain asked)"
		}
		if len(m.Denied) > 0 {
			line += "  denied for " + strings.Join(m.Denied, ", ")
		}
		_, _ = fmt.Fprintln(stdout, line)
	}
	return exitOK
}

func cmdMachinesAllowDeny(allow bool, args []string, stdout, stderr io.Writer, environ []string) int {
	cmd := "machines deny"
	if allow {
		cmd = "machines allow"
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	project := fs.String("project", "", "the project (owner/name)")
	force := fs.Bool("force", false, "apply the edit even when queued runs would have no machine")
	dry := fs.Bool("dry-run", false, "show the impact and change nothing")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 || *project == "" {
		_, _ = fmt.Fprintf(stderr, "%s: expected one machine name and --project owner/name\n", cmd)
		return exitUsage
	}
	id := "machines.deny"
	if allow {
		id = "machines.allow"
	}
	return applyPolicyEdit(cmd, dirOf(environ), id, hsaction.MachineRuleArgs{Name: pos[0], Project: *project, Confirm: *force, DryRun: *dry}, *asJSON, stdout, stderr, environ)
}

func cmdMachinesResume(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("machines resume", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "machines resume: expected one machine name")
		return exitUsage
	}
	env, err := cliEnv(dirOf(environ), "", environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "machines resume:", err)
		return exitUsage
	}
	res, code := runAction(env, "machines.resume", hsaction.MachineArgs{Name: pos[0]})
	d, ok := res.Data.(hsaction.Resumed)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "machines resume:", res.Message)
		return finishAction("machines resume", res, code, stderr)
	}
	if *asJSON {
		if c := writeJSON("machines resume", stdout, stderr, map[string]any{"name": d.Name, "outcome": d.Outcome, "cleared": d.Cleared, "text": res.Message}); c != exitOK {
			return c
		}
		return finishAction("machines resume", res, code, stderr)
	}
	if res.OK {
		_, _ = fmt.Fprintln(stdout, res.Message)
	} else {
		_, _ = fmt.Fprintln(stderr, "machines resume: "+res.Message)
	}
	return finishAction("machines resume", res, code, stderr)
}

func sortedStrings(s []string) []string {
	out := append([]string{}, s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
