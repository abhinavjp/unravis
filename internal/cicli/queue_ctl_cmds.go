package cicli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"strings"
	"time"

	"api-audit-explorer/internal/hsaction"
	"api-audit-explorer/internal/localci"
)

// `unravis-ci queue ...` is the queue as a person or a script sees it: list it, put one request first, stop
// and start the claiming. It changes nothing a running agent does not already honour (the request files and
// the PAUSE file in the state folder).

func cmdQueue(args []string, stdout, stderr io.Writer, environ []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "queue: expected list, top, pause or resume")
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "list":
		return cmdQueueList(args[1:], stdout, stderr, environ)
	case "top":
		return cmdQueueTop(args[1:], stdout, stderr, environ)
	case "pause":
		return cmdQueuePause(true, args[1:], stdout, stderr, environ)
	case "resume":
		return cmdQueuePause(false, args[1:], stdout, stderr, environ)
	}
	_, _ = fmt.Fprintf(stderr, "queue: unknown command %q (expected list, top, pause or resume)\n", args[0])
	usage(stderr)
	return exitUsage
}

// queueDirFlag registers the flag that names the CI store folder of a queue command. --state-dir is the name
// these commands document; --state is accepted as well because every other unravis-ci command calls it that.
func queueDirFlag(fs *flag.FlagSet, alsoState bool) func(environ []string) string {
	dir := fs.String("state-dir", "", "the CI store folder (default: under LOCALAPPDATA)")
	var old *string
	if alsoState {
		old = fs.String("state", "", "the same as --state-dir")
	}
	return func(environ []string) string {
		if *dir == "" && old != nil {
			return stateDirOf(*old, environ)
		}
		return stateDirOf(*dir, environ)
	}
}

// ---------------------------------------------------------------- list

// queueStates are the values of `queue list --state`.
var queueStates = []string{"active", "all", localci.StateQueued, localci.StateRunning, localci.StateDone, localci.StateCancelled, localci.StateSuperseded}

func stateMatches(filter, state string, active bool) bool {
	switch filter {
	case "all":
		return true
	case "active":
		return active
	}
	return filter == state
}

// queueListJSON is the output of `queue list --json`. The field names are part of the command's interface;
// add fields, do not rename or drop them.
type queueListJSON struct {
	StateDir     string            `json:"stateDir"`
	Paused       bool              `json:"paused"` // the PAUSE file exists: no agent takes a new request
	Waiting      int               `json:"waiting"`
	Running      int               `json:"running"`
	Filter       string            `json:"filter"`
	Requests     []localci.Request `json:"requests"` // running first, then waiting in claim order, then finished newest first
	PausedReason string            `json:"pausedReason,omitempty"`
	// Why says, by request id, why each queued request waits (localci.WhyWaiting); absent when the folder cannot say.
	Why map[string]localci.WaitReason `json:"why,omitempty"`
}

func cmdQueueList(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("queue list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, false)
	filter := fs.String("state", "active", "which requests: "+strings.Join(queueStates, ", "))
	asJSON := fs.Bool("json", false, "print the list as one JSON object (see queueListJSON)")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "queue list: takes no arguments")
		}
		return exitUsage
	}
	known := false
	for _, s := range queueStates {
		known = known || s == *filter
	}
	if !known {
		_, _ = fmt.Fprintf(stderr, "queue list: --state is a request state (%s), not a folder; the folder is --state-dir\n", strings.Join(queueStates, ", "))
		return exitUsage
	}
	dir := dirOf(environ)
	all, err := (&localci.Queue{Dir: localci.QueueDir(dir)}).List()
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "queue list:", err)
		return exitUsage
	}
	var shown []localci.Request
	waiting, running := 0, 0
	for _, r := range all {
		switch r.State {
		case localci.StateQueued:
			waiting++
		case localci.StateRunning:
			running++
		}
		if stateMatches(*filter, r.State, r.Active()) {
			shown = append(shown, r)
		}
	}
	now := time.Now()
	why := ""
	if localci.IsPaused(dir) {
		why = "the pause file " + localci.PausePath(dir) + " exists"
	}
	whyOf := map[string]localci.WaitReason{}
	if in, err := localci.WaitInputOf(dir, now, agentStale); err != nil {
		_, _ = fmt.Fprintln(stderr, "queue list: cannot say why requests wait:", err)
		whyOf = nil
	} else {
		for _, r := range shown {
			if r.State == localci.StateQueued {
				whyOf[r.ID] = localci.WhyWaiting(r, in)
			}
		}
	}
	if *asJSON {
		return writeJSON("queue list", stdout, stderr, queueListJSON{StateDir: dir, Paused: why != "", PausedReason: why, Waiting: waiting, Running: running,
			Filter: *filter, Requests: nonNil(shown), Why: whyOf})
	}
	if why != "" {
		_, _ = fmt.Fprintf(stdout, "claiming is paused: %s\n", why)
	}
	_, _ = fmt.Fprintf(stdout, "queue: %d waiting, %d running\n", waiting, running)
	for _, r := range shown {
		_, _ = fmt.Fprintln(stdout, requestLine(now, r))
		if w, ok := whyOf[r.ID]; ok {
			_, _ = fmt.Fprintf(stdout, "    waiting: %s\n", w.Text)
		}
	}
	return exitOK
}

// ---------------------------------------------------------------- top

func cmdQueueTop(args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("queue top", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "queue top: expected one request id or pull request number")
		usage(stderr)
		return exitUsage
	}
	env, err := cliEnv(dirOf(environ), "", environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "queue top:", err)
		return exitUsage
	}
	res, code := runAction(env, "queue.top", hsaction.TopArgs{Target: targetOf(pos[0])})
	printItems("queue top", res, stdout, stderr, hsaction.TopLine)
	return finishAction("queue top", res, code, stderr)
}

// targetOf reads a request id or a pull request number.
func targetOf(s string) hsaction.Target {
	if n, ok := isNumber(s); ok {
		return hsaction.Target{PR: n}
	}
	return hsaction.Target{ID: s}
}

// ---------------------------------------------------------------- pause and resume

func cmdQueuePause(pause bool, args []string, stdout, stderr io.Writer, environ []string) int {
	name := "resume"
	if pause {
		name = "pause"
	}
	fs := flag.NewFlagSet("queue "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dirOf := queueDirFlag(fs, true)
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintf(stderr, "queue %s: takes no arguments\n", name)
		}
		return exitUsage
	}
	dir := dirOf(environ)
	env, err := cliEnv(dir, "", environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "queue %s: %v\n", name, err)
		return exitUsage
	}
	id := "queue.resume"
	if pause {
		id = "queue.pause"
	}
	res, code := runAction(env, id, hsaction.NoArgs{})
	if !res.OK {
		_, _ = fmt.Fprintf(stderr, "queue %s: %s\n", name, res.Message)
		return finishAction("queue "+name, res, code, stderr)
	}
	_, _ = fmt.Fprintln(stdout, res.Message)
	if _, err := os.Stat(dir); err != nil && !pause {
		_, _ = fmt.Fprintf(stderr, "queue resume: note: the folder %s does not exist (check --state-dir)\n", dir)
	}
	return finishAction("queue "+name, res, code, stderr)
}
