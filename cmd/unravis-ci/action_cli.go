package main

import (
	"errors"
	"fmt"
	"io"

	"api-audit-explorer/internal/hsaction"
	"api-audit-explorer/internal/localci"
)

// The commands that change something (cancel, rerun, drain, queue top/pause/resume, policy set, machines
// allow/deny/resume, hold add/lift) run through internal/hsaction, the same registry the Homestretch page posts to,
// so the checks, the change and the activity entry are one piece of code. These helpers turn a command line into an
// hsaction.Env and an hsaction.Result back into the command's output and exit code.

// cliEnv is the Env of a command: the actor from --actor (when the command has one), else HOMESTRETCH_ACTOR, else
// cli; the person is the user name of this PC.
func cliEnv(dir, actorFlag string, environ []string) (hsaction.Env, error) {
	act, err := actorOf(actorFlag, environ)
	if err != nil {
		return hsaction.Env{}, err
	}
	return hsaction.Env{Dir: dir, Actor: act, Person: userOf(environ), Now: holdNow, ShowPaths: true, AgentStale: agentStale}, nil
}

// runAction runs one action. It returns the result and the exit code of its status; finishAction settles the code
// once the command has printed its output.
func runAction(env hsaction.Env, id string, args any) (hsaction.Result, int) {
	res := hsaction.Run(env, id, args)
	return res, res.Status.ExitCode()
}

// finishAction reports an activity entry that could not be written. When the change was saved the command fails, so
// nobody believes the action was recorded; when nothing changed it only notes it. A missing CI store folder is not
// reported: there is nothing to record into, and the command says so itself where it matters.
func finishAction(cmd string, res hsaction.Result, code int, stderr io.Writer) int {
	if res.LogErr == nil || errors.Is(res.LogErr, hsaction.ErrNoStore) {
		return code
	}
	if res.Changed {
		_, _ = fmt.Fprintf(stderr, "%s: the change was saved, but the activity log could not be written: %v\n", cmd, res.LogErr)
		return exitFailed
	}
	_, _ = fmt.Fprintf(stderr, "%s: note: the activity log could not be written: %v\n", cmd, res.LogErr)
	return code
}

// errorText is what a command prints after "cmd: " for a result that did not end done.
func errorText(res hsaction.Result) string {
	if res.Field == "until" || res.Field == "from" {
		return "--" + res.Field + ": " + res.Message
	}
	return res.Message
}

// printItems prints a queue action that touched one request or several: each done one on stdout, each failed one
// on stderr.
func printItems(cmd string, res hsaction.Result, stdout, stderr io.Writer, line func(localci.Request) string) {
	items, ok := res.Data.(hsaction.Items)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", cmd, errorText(res))
		return
	}
	for _, it := range items {
		if it.Err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmd, it.Err)
			continue
		}
		_, _ = fmt.Fprintln(stdout, line(it.Request))
	}
}
