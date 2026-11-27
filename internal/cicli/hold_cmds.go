package cicli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/hsaction"
	"api-audit-explorer/internal/releasehold"
)

// `unravis-ci hold ...` and `unravis-ci activity`: the freeze windows of the release flow and the record of who did
// what. They live here and not in unravis-releases because unravis-releases is a window program (built with
// -H=windowsgui): it has no console to print to and a script cannot wait for its exit code, and `hold check` is a gate
// whose exit code (5, held) is the whole point. The holds and the log sit in the same CI store folder as the queue,
// which this program already finds (--state-dir).
//
// A hold blocks Promote only (tools/release/Promote-Develop.ps1 runs `hold check`). Release cutting and CI runs are not
// held.

// holdNow is the clock of the hold and activity commands; tests set it.
var holdNow = time.Now

// actorEnv names the environment variable that sets the actor of an activity entry when --actor is not given: the UI
// and Claude set it when they run these commands for a person.
const actorEnv = "HOMESTRETCH_ACTOR"

func cmdHold(args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "hold: expected add, lift, list or check")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "add":
		return cmdHoldAdd(args[1:], stdout, stderr, environ)
	case "lift":
		return cmdHoldLift(args[1:], stdout, stderr, environ)
	case "list":
		return cmdHoldList(args[1:], stdout, stderr, environ)
	case "check":
		return cmdHoldCheck(args[1:], stdout, stderr, environ)
	}
	_, _ = fmt.Fprintf(stderr, "hold: unknown command %q (expected add, lift, list or check)\n", args[0])
	usage(stderr)
	return exitUsage
}

// actorOf is the actor of an entry: the flag, else HOMESTRETCH_ACTOR, else cli.
func actorOf(flagValue string, environ []string) (string, error) {
	a := strings.ToLower(strings.TrimSpace(flagValue))
	if a == "" {
		a = strings.ToLower(strings.TrimSpace(getenv(environ, actorEnv)))
	}
	if a == "" {
		a = activitylog.ActorCLI
	}
	if !activitylog.ValidActor(a) {
		return "", fmt.Errorf("actor %q is not one of %s (--actor or %s)", a, strings.Join(activitylog.Actors, ", "), actorEnv)
	}
	return a, nil
}

// userOf is the name of the person at this PC, the default owner and lifter.
func userOf(environ []string) string {
	for _, k := range []string{"USERNAME", "USER"} {
		if v := strings.TrimSpace(getenv(environ, k)); v != "" {
			return v
		}
	}
	return ""
}

// parseWhen reads a time a person typed (see hsaction.ParseWhen).
func parseWhen(s string, now time.Time, endOfDay bool) (time.Time, error) {
	return hsaction.ParseWhen(s, now, endOfDay)
}

// whenText prints a stored time for a person (see hsaction.WhenText).
func whenText(t time.Time) string { return hsaction.WhenText(t) }

func projectText(p string) string { return hsaction.ProjectText(p) }

// ---------------------------------------------------------------- add

func cmdHoldAdd(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("hold add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	project := fs.String("project", "", "the project id to hold")
	all := fs.Bool("all", false, "hold every project")
	until := fs.String("until", "", "when the hold ends: 90m, 2h, 3d, 2026-12-01 (the whole day), 2026-12-01 18:00 (local) or RFC 3339")
	from := fs.String("from", "", "when the hold starts (default: now); same forms as --until")
	reason := fs.String("reason", "", "why Promote is held (shown to whoever tries to promote)")
	owner := fs.String("owner", "", "who owns the hold (default: the user name of this PC)")
	actor := fs.String("actor", "", "who acts: cli, ui or claude (default: "+actorEnv+", else cli)")
	asJSON := fs.Bool("json", false, "print the new hold as JSON")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "hold add: takes no arguments (did you forget quotes around --reason?)")
		}
		return exitUsage
	}
	if (*project == "") == !*all {
		_, _ = fmt.Fprintln(stderr, "hold add: give exactly one of --project P or --all")
		return exitUsage
	}
	act, err := actorOf(*actor, environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hold add:", err)
		return exitUsage
	}
	if *until == "" {
		_, _ = fmt.Fprintln(stderr, "hold add: --until is needed (for example --until 2h or --until 2026-12-01)")
		return exitUsage
	}
	env, err := cliEnv(dirOf(environ), act, environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hold add:", err)
		return exitUsage
	}
	res, code := runAction(env, "hold.add", hsaction.HoldAddArgs{Project: *project, All: *all, Until: *until, From: *from, Reason: *reason, Owner: *owner})
	h, ok := res.Data.(releasehold.Hold)
	if !res.OK || !ok {
		_, _ = fmt.Fprintln(stderr, "hold add:", errorText(res))
		return finishAction("hold add", res, code, stderr)
	}
	if *asJSON {
		if rc := writeJSON("hold add", stdout, stderr, holdJSONOf(h, env.Now())); rc != exitOK {
			return rc
		}
	} else {
		_, _ = fmt.Fprintln(stdout, res.Message)
	}
	return finishAction("hold add", res, code, stderr)
}

// ---------------------------------------------------------------- lift

func cmdHoldLift(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("hold lift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	reason := fs.String("reason", "", "why the hold ends early")
	by := fs.String("by", "", "who lifts it (default: the user name of this PC)")
	actor := fs.String("actor", "", "who acts: cli, ui or claude (default: "+actorEnv+", else cli)")
	asJSON := fs.Bool("json", false, "print the lifted hold as JSON")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "hold lift: give one hold id (see: "+Prog+" hold list)")
		return exitUsage
	}
	act, err := actorOf(*actor, environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hold lift:", err)
		return exitUsage
	}
	env, err := cliEnv(dirOf(environ), act, environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hold lift:", err)
		return exitUsage
	}
	res, code := runAction(env, "hold.lift", hsaction.HoldLiftArgs{ID: pos[0], Reason: *reason, By: *by})
	h, ok := res.Data.(releasehold.Hold)
	if !res.OK || !ok {
		_, _ = fmt.Fprintln(stderr, "hold lift:", errorText(res))
		return finishAction("hold lift", res, code, stderr)
	}
	if *asJSON {
		if rc := writeJSON("hold lift", stdout, stderr, holdJSONOf(h, env.Now())); rc != exitOK {
			return rc
		}
	} else {
		_, _ = fmt.Fprintln(stdout, res.Message)
	}
	return finishAction("hold lift", res, code, stderr)
}

// ---------------------------------------------------------------- list

// holdJSON is one hold in --json output: the stored hold and its state now. The field names are part of the command's
// interface: add fields, do not rename or drop them.
type holdJSON struct {
	releasehold.Hold
	State string `json:"state"` // upcoming, active, ended or lifted
}

func holdJSONOf(h releasehold.Hold, now time.Time) holdJSON {
	return holdJSON{Hold: h, State: h.StateAt(now)}
}

// holdListJSON is the output of `hold list --json`.
type holdListJSON struct {
	StateDir string     `json:"stateDir"`
	Now      time.Time  `json:"now"`
	Holds    []holdJSON `json:"holds"`
}

func cmdHoldList(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("hold list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	everything := fs.Bool("all", false, "also list holds that ended or were lifted")
	project := fs.String("project", "", "only holds that cover this project (all-project holds included)")
	asJSON := fs.Bool("json", false, "print the list as one JSON object (see holdListJSON)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "hold list: takes no arguments")
		}
		return exitUsage
	}
	now := holdNow()
	dir := dirOf(environ)
	all, err := (releasehold.Store{Dir: dir}).List()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "hold list:", err)
		return exitUsage
	}
	out := holdListJSON{StateDir: dir, Now: now.UTC().Truncate(time.Second), Holds: []holdJSON{}}
	for _, h := range all {
		st := h.StateAt(now)
		if !*everything && st != releasehold.StateActive && st != releasehold.StateUpcoming {
			continue
		}
		if !h.Covers(*project) {
			continue
		}
		out.Holds = append(out.Holds, holdJSONOf(h, now))
	}
	if *asJSON {
		return writeJSON("hold list", stdout, stderr, out)
	}
	if len(out.Holds) == 0 {
		if *everything {
			_, _ = fmt.Fprintln(stdout, "no holds")
		} else {
			_, _ = fmt.Fprintln(stdout, "no active or upcoming holds (--all lists the ones that ended)")
		}
		return exitOK
	}
	for _, h := range out.Holds {
		_, _ = fmt.Fprintf(stdout, "%-8s  %s  %s\n          from %s until %s, owner %s\n          reason: %s\n",
			h.State, h.ID, projectText(h.Project), whenText(h.Start), whenText(h.End), h.Owner, h.Reason)
		if h.Lifted() {
			_, _ = fmt.Fprintf(stdout, "          lifted by %s at %s", h.LiftedBy, whenText(h.LiftedAt))
			if h.LiftReason != "" {
				_, _ = fmt.Fprintf(stdout, ": %s", h.LiftReason)
			}
			_, _ = fmt.Fprintln(stdout)
		}
	}
	return exitOK
}

// ---------------------------------------------------------------- check

// holdCheckJSON is the output of `hold check --json`.
type holdCheckJSON struct {
	Project string     `json:"project"` // "" is any project
	Held    bool       `json:"held"`
	Holds   []holdJSON `json:"holds"` // the active holds, soonest ending first
}

// cmdHoldCheck is the gate Promote runs: exit 5 (held) while a hold covers the project, 0 when none does. A hold store
// that cannot be read is exit 2, and the caller must refuse then too: not knowing is not "free".
// Without --project, any active hold counts.
func cmdHoldCheck(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("hold check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	project := fs.String("project", "", "the project about to be promoted (default: any project)")
	asJSON := fs.Bool("json", false, "print the result as one JSON object (see holdCheckJSON)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "hold check: takes no arguments")
		}
		return exitUsage
	}
	if *project != "" && !releasehold.ValidProject(*project) {
		_, _ = fmt.Fprintf(stderr, "hold check: %q is not a project id\n", *project)
		return exitUsage
	}
	now := holdNow()
	active, err := (releasehold.Store{Dir: dirOf(environ)}).Active(*project, now)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hold check: the holds could not be read, so treat Promote as held: %v\n", err)
		return exitUsage
	}
	what := "any project"
	if *project != "" {
		what = "project " + *project
	}
	if *asJSON {
		out := holdCheckJSON{Project: *project, Held: len(active) > 0, Holds: []holdJSON{}}
		for _, h := range active {
			out.Holds = append(out.Holds, holdJSONOf(h, now))
		}
		if rc := writeJSON("hold check", stdout, stderr, out); rc != exitOK {
			return rc
		}
	} else if len(active) == 0 {
		_, _ = fmt.Fprintf(stdout, "no hold is active for %s\n", what)
	} else {
		_, _ = fmt.Fprintf(stdout, "Promote is held for %s.\n", what)
		for _, h := range active {
			_, _ = fmt.Fprintf(stdout, "  hold %s (%s): %s\n    owner %s, until %s\n    lift it early with: %s hold lift %s --reason \"...\"\n",
				h.ID, projectText(h.Project), h.Reason, h.Owner, whenText(h.End), Prog, h.ID)
		}
	}
	if len(active) > 0 {
		return exitHeld
	}
	return exitOK
}

// ---------------------------------------------------------------- activity

// activityJSON is the output of `activity --json`.
type activityJSON struct {
	StateDir string              `json:"stateDir"`
	Skipped  int                 `json:"skipped"` // lines that were not a valid entry (a torn last line after a crash)
	Entries  []activitylog.Entry `json:"entries"` // oldest first
}

func cmdActivity(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("activity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	actor := fs.String("actor", "", "only entries of this actor: "+strings.Join(activitylog.Actors, ", "))
	limit := fs.Int("limit", 50, "only the newest N entries (0: all)")
	asJSON := fs.Bool("json", false, "print the entries as one JSON object (see activityJSON)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "activity: takes no arguments")
		}
		return exitUsage
	}
	if *actor != "" && !activitylog.ValidActor(*actor) {
		_, _ = fmt.Fprintf(stderr, "activity: --actor is one of %s\n", strings.Join(activitylog.Actors, ", "))
		return exitUsage
	}
	if *limit < 0 {
		_, _ = fmt.Fprintln(stderr, "activity: --limit must be 0 or more")
		return exitUsage
	}
	dir := dirOf(environ)
	entries, skipped, err := (activitylog.Log{Dir: dir}).Read(activitylog.Filter{Actor: *actor, Limit: *limit})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "activity:", err)
		return exitUsage
	}
	if *asJSON {
		if entries == nil {
			entries = []activitylog.Entry{}
		}
		return writeJSON("activity", stdout, stderr, activityJSON{StateDir: dir, Skipped: skipped, Entries: entries})
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(stdout, "no activity recorded")
	}
	for _, e := range entries {
		line := fmt.Sprintf("%s  %-6s  %-10s  %-10s  %s", e.Time.Local().Format("2006-01-02 15:04:05"), e.Actor, dash(e.Person), e.Action, e.Target)
		if e.Reason != "" {
			line += "  (" + e.Reason + ")"
		}
		if e.Outcome != "" && e.Outcome != "done" {
			line += "  [" + e.Outcome + "]"
		}
		_, _ = fmt.Fprintln(stdout, strings.TrimRight(line, " "))
	}
	if skipped > 0 {
		_, _ = fmt.Fprintf(stderr, "activity: %d line(s) of the log were not valid entries and were skipped\n", skipped)
	}
	return exitOK
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
