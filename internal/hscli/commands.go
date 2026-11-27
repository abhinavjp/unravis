package hscli

import (
	"strings"
)

// command is one entry of the command table.
type command struct {
	Path    []string // the words that name it: {"queue", "cancel"}
	Usage   string   // the line help prints, without the program name
	Summary string   // one line, for help
	Group   string   // the heading help lists it under
	// Action is the id of the registry action (internal/hsaction) the command runs, or "". The table test needs every
	// registry action to be named by one command.
	Action string
	// NotBacked is why nothing stands behind the command yet. The command then prints it, changes nothing and exits 3.
	NotBacked string
	Hidden    bool // left out of help
	Run       func(c *run, args []string) int
}

func (cmd *command) run(c *run, args []string) int {
	if cmd.NotBacked != "" {
		return c.notBacked(cmd)
	}
	return cmd.Run(c, args)
}

const (
	gLook     = "Look"
	gShip     = "Ship"
	gQueue    = "Queue"
	gMachines = "Machines and policy"
	gHolds    = "Holds and history"
	gFix      = "Fixing a failing pull request"
	gOther    = "Other"
)

// forward returns a Run that runs the engine command with these leading words and the arguments after the command's
// own words. flagState says whether the engine command takes --state where this command line says --state-dir.
func forward(engineWords []string, flagState bool) func(c *run, args []string) int {
	return func(c *run, args []string) int {
		if flagState {
			args = stateFlag(args)
		}
		return c.engine(append(append([]string{}, engineWords...), args...)...)
	}
}

// group returns the Run of a word that only has sub-commands (`queue`, `machines`, `hold`, `policy`): without a
// sub-command it runs def (the engine's own default, or "" to ask for one); a word that is not a sub-command is
// refused by name.
func groupRun(word string, subs []string, def func(c *run, args []string) int) func(c *run, args []string) int {
	return func(c *run, args []string) int {
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			return c.usageErr("%s: %q is not a command (expected %s)", word, args[0], strings.Join(subs, ", "))
		}
		if def == nil {
			return c.usageErr("%s: expected %s", word, strings.Join(subs, ", "))
		}
		return def(c, args)
	}
}

// commands is the whole command list. The order is the order of help.
var commands = []command{
	// ---- Look
	{Path: []string{"status"}, Group: gLook, Usage: "status [--project P] [--state-dir DIR] [--json]",
		Summary: "Look at a project: the queue, the machines, what ran last", Run: cmdStatus},
	{Path: []string{"queue"}, Group: gLook, Usage: "queue [list] [--state S] [--state-dir DIR] [--json]",
		Summary: "List the queue (S: queued, running, active, done, cancelled, superseded)",
		Run:     groupRun("queue", []string{"list", "cancel", "retry", "top", "pause", "resume", "fallback"}, forward([]string{"queue", "list"}, false))},
	{Path: []string{"queue", "list"}, Hidden: true, Run: forward([]string{"queue", "list"}, false)},
	{Path: []string{"machines"}, Group: gLook, Usage: "machines [list] [--state-dir DIR] [--json]",
		Summary: "List the machines that run this project's work",
		Run:     groupRun("machines", []string{"list", "drain", "resume", "allow", "deny", "label", "enrol"}, forward([]string{"machines", "list"}, false))},
	{Path: []string{"machines", "list"}, Hidden: true, Run: forward([]string{"machines", "list"}, false)},
	{Path: []string{"policy", "show"}, Group: gLook, Usage: "policy show <owner/name> [--state-dir DIR] [--json]",
		Summary: "Show which machines may run a project", Run: forward([]string{"policy", "show"}, false)},
	{Path: []string{"hold", "list"}, Group: gLook, Usage: "hold list [--all] [--project P] [--state-dir DIR] [--json]",
		Summary: "List the freeze windows", Run: forward([]string{"hold", "list"}, false)},
	{Path: []string{"activity"}, Group: gLook, Usage: "activity [--actor ui|cli|claude] [--limit N] [--state-dir DIR] [--json]",
		Summary: "Read the activity log: who did what", Run: forward([]string{"activity"}, false)},
	{Path: []string{"compare"}, Group: gLook, Usage: "compare [--repo owner/name] [--state-dir DIR] [--limit N] [--json]",
		Summary: "Compare the local runs with what GitHub says", Run: forward([]string{"compare"}, true)},

	// ---- Ship
	{Path: []string{"promote"}, Group: gShip, Usage: "promote [develop] [--project P] [--up-to SHA] [--dry-run] [--no-merge]",
		Summary: "Promote develop to master with one pull request (checks the freeze first)", Run: cmdPromote},
	{Path: []string{"release"}, Group: gShip, Usage: "release <rc|stable> [--version V] [--dry-run] [--yes] | release --status [--json]",
		Summary: "Cut a release candidate or a stable release (checks the freeze first)", Run: cmdRelease},
	{Path: []string{"rollback"}, Group: gShip, Usage: "rollback <step>",
		Summary:   "Roll a step back",
		NotBacked: "No rollback flow exists to call yet, and a rollback must not be faked."},

	// ---- Queue
	{Path: []string{"queue", "cancel"}, Group: gQueue, Usage: "queue cancel <id|pr> [--reason R] [--state-dir DIR]",
		Summary: "Cancel a queued or running item", Action: "queue.cancel", Run: forward([]string{"cancel"}, true)},
	{Path: []string{"queue", "retry"}, Group: gQueue, Usage: "queue retry <runid|pr> [--state-dir DIR]",
		Summary: "Queue a finished run again", Action: "queue.retry", Run: forward([]string{"rerun"}, true)},
	{Path: []string{"queue", "top"}, Group: gQueue, Usage: "queue top <id|pr> [--state-dir DIR]",
		Summary: "Put a waiting item first in line", Action: "queue.top", Run: forward([]string{"queue", "top"}, false)},
	{Path: []string{"queue", "pause"}, Group: gQueue, Usage: "queue pause [--state-dir DIR]",
		Summary: "Stop machines taking new items; running ones go on", Action: "queue.pause", Run: forward([]string{"queue", "pause"}, false)},
	{Path: []string{"queue", "resume"}, Group: gQueue, Usage: "queue resume [--state-dir DIR]",
		Summary: "Let machines take items again", Action: "queue.resume", Run: forward([]string{"queue", "resume"}, false)},
	{Path: []string{"queue", "fallback"}, Group: gQueue, Usage: "queue fallback <pr> [--repo owner/name] [--dry-run]",
		Summary: "Run a pull request on this PC through GitHub", Run: forward([]string{"fallback"}, true)},

	// ---- Machines and policy
	{Path: []string{"machines", "drain"}, Group: gMachines, Usage: "machines drain <name> [--state-dir DIR]",
		Summary: "Finish what runs, take nothing new", Action: "machines.drain", Run: cmdMachinesDrain},
	{Path: []string{"machines", "resume"}, Group: gMachines, Usage: "machines resume <name> [--state-dir DIR] [--json]",
		Summary: "Withdraw a drain the machine has not acted on", Action: "machines.resume", Run: forward([]string{"machines", "resume"}, false)},
	{Path: []string{"machines", "allow"}, Group: gMachines, Usage: "machines allow <name> --project owner/name [--force] [--dry-run] [--json]",
		Summary: "Let a machine run a project", Action: "machines.allow", Run: forward([]string{"machines", "allow"}, false)},
	{Path: []string{"machines", "deny"}, Group: gMachines, Usage: "machines deny <name> --project owner/name [--force] [--dry-run] [--json]",
		Summary: "Stop a machine running a project", Action: "machines.deny", Run: forward([]string{"machines", "deny"}, false)},
	{Path: []string{"machines", "label"}, Group: gMachines, Usage: "machines label <name> <label>",
		Summary:   "Add a label to a machine",
		NotBacked: "A machine's labels are set when it starts: homestretch ci serve --label L."},
	{Path: []string{"machines", "enrol"}, Group: gMachines, Usage: "machines enrol --name N --code C",
		Summary:   "Enrol a machine",
		NotBacked: "There is no enrolment flow in the engine to call yet (no `agent enrol`). A machine joins by running: homestretch ci serve."},
	{Path: []string{"policy", "set"}, Group: gMachines, Usage: "policy set <owner/name> --any|--only m1,m2|--except m1 [--force] [--dry-run] [--json]",
		Summary: "Set which machines may run a project", Action: "policy.set", Run: forward([]string{"policy", "set"}, false)},
	{Path: []string{"policy"}, Hidden: true, Run: groupRun("policy", []string{"set", "show"}, nil)},

	// ---- Holds
	{Path: []string{"hold", "add"}, Group: gHolds, Usage: "hold add --project P|--all --until T --reason R [--from T] [--owner O] [--json]",
		Summary: "Freeze Promote for a project (or all) until a time", Action: "hold.add", Run: forward([]string{"hold", "add"}, false)},
	{Path: []string{"hold", "lift"}, Group: gHolds, Usage: "hold lift <id> [--reason R] [--json]",
		Summary: "End a freeze early", Action: "hold.lift", Run: forward([]string{"hold", "lift"}, false)},
	{Path: []string{"hold", "check"}, Group: gHolds, Usage: "hold check [--project P] [--json]",
		Summary: "Exit 5 while a freeze covers the project", Run: forward([]string{"hold", "check"}, false)},
	{Path: []string{"hold"}, Hidden: true, Run: groupRun("hold", []string{"add", "lift", "list", "check"}, nil)},

	// ---- Fixing a failing pull request
	{Path: []string{"claim"}, Group: gFix, Usage: "claim <pr> --by NAME | claim <pr> --fixed",
		Summary:   "Claim a failing pull request, or mark the fix done",
		NotBacked: "Nothing sets or reads the hs:claimed and hs:fixed labels yet."},
	{Path: []string{"brief"}, Group: gFix, Usage: "brief <pr>",
		Summary:   "Copy the brief for a failing pull request",
		NotBacked: "No brief is written yet."},

	// ---- Other
	{Path: []string{"ci"}, Group: gOther, Usage: "ci <engine command> ...",
		Summary: "The engine: serve, run, run-pr, lint, runs, log, exclusive, claims, and the rest", Run: cmdCI},
	{Path: []string{"version"}, Group: gOther, Usage: "version [--json]",
		Summary: "Print the version", Run: cmdVersion},
}
