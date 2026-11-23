package cmdtree

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// How long `app start` waits for the new owner to answer, and `app stop` for the old one to go; and how often each asks (spec 05 3.5
// D, E). Variables so a test need not wait.
var (
	startWait = 15 * time.Second
	stopWait  = 5 * time.Second
	appPoll   = 100 * time.Millisecond
)

// noArgs reads --request (or nothing) as the arguments of a command that takes none.
func (st *state) noArgs(c *cobra.Command, name string) (json.RawMessage, error) {
	a, err := st.requestOr(c, nil, func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
	if err != nil {
		return nil, err
	}
	var none bytes.Buffer
	if json.Compact(&none, a) != nil || none.String() != "{}" {
		return nil, usage(name + " takes no arguments.")
	}
	return a, nil
}

// stopped is the error of a command that was canceled or ran out of time, nil while it may go on.
func (st *state) stopped() *clicore.Error {
	switch st.ctx.Err() {
	case nil:
		return nil
	case context.DeadlineExceeded:
		return clicore.NewError(clicore.CodeTimeout, "The command ran out of time.", "")
	}
	return clicore.NewError(clicore.CodeCanceled, "Canceled.", "")
}

// sleep waits for d or until the command is stopped.
func (st *state) sleep(d time.Duration) *clicore.Error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-st.ctx.Done():
		return st.stopped()
	}
}

// absent tells whether an attach error means that no owner runs (a missing or a dead locator).
func absent(e *clicore.Error) bool {
	return e != nil && (e.Code == clicore.CodeAppNotRunning || e.Code == clicore.CodeStaleLocator)
}

type startData struct {
	Started        bool   `json:"started"`
	AlreadyRunning bool   `json:"alreadyRunning"`
	PID            int    `json:"pid"`
	Owner          string `json:"owner"`
}

func (st *state) appStartCmd() *cobra.Command {
	var headless bool
	c := &cobra.Command{
		Use:   "start --headless",
		Short: "Start the app with no window, for the command line and the shell to use",
		Long: "Starts the app program with no window and no browser (an owner called headless) and waits until it answers. If the app already runs, " +
			"nothing is started and the answer says so. The app runs on after this command ends; stop it with 'unravis app stop'.",
		Args:        cobra.NoArgs,
		Annotations: online("app.start", ""),
		RunE: func(c *cobra.Command, _ []string) error {
			if _, err := st.noArgs(c, "app start"); err != nil {
				return err
			}
			if !headless {
				return usage("app start needs --headless.")
			}
			d, e := st.startHeadless()
			if e != nil {
				return e
			}
			return st.emit("app.start", d, clicore.Meta{Coverage: "complete", Identifiers: "hidden"}, func(w io.Writer) error {
				rows := [][]string{{"started", strconv.FormatBool(d.Started)}, {"already running", strconv.FormatBool(d.AlreadyRunning)},
					{"pid", strconv.Itoa(d.PID)}, {"owner", clicore.Printable(d.Owner)}}
				return clicore.WriteTable(w, []string{"FIELD", "VALUE"}, rows)
			})
		},
	}
	c.Flags().BoolVar(&headless, "headless", false, "start the app with no window (required)")
	return c
}

// startHeadless finds a running owner or starts one and waits for its hello.
func (st *state) startHeadless() (startData, *clicore.Error) {
	if sess, e := st.attach(st.ctx); e == nil {
		return startData{AlreadyRunning: true, PID: sess.Hello.PID, Owner: sess.Hello.Owner}, nil
	} else if !absent(e) {
		return startData{}, e // an owner that cannot be read or trusted: nothing is started on top of it
	}
	if st.io.StartHeadless == nil {
		return startData{}, clicore.NewError(clicore.CodeEndpointUnavailable, "This build of the command line cannot start the app.", "")
	}
	child, e := st.io.StartHeadless(st.ctx)
	if e != nil {
		return startData{}, e
	}
	deadline := time.Now().Add(startWait)
	busy := false // the child found another owner (exit 12): that owner is waited for, and is the answer when it comes
	for {
		// the code of a child that ended comes first: 11 and 13 end the wait; 12 means another owner exists, so that one is attached to
		select {
		case code := <-child.Exit:
			c := clicore.FromSidecarExit(code) // 0 and any other code are INTERNAL
			if c != clicore.CodeOwnerBusy {
				return startData{}, clicore.NewError(c, sidecarText(c), sidecarAction(c))
			}
			busy = true
		default:
		}
		sess, e := st.attach(st.ctx)
		switch {
		case e == nil:
			return startData{Started: sess.Hello.PID == child.PID, AlreadyRunning: sess.Hello.PID != child.PID, PID: sess.Hello.PID, Owner: sess.Hello.Owner}, nil
		case st.ctx.Err() != nil:
			return startData{}, st.stopped()
		case !absent(e) && e.Code != clicore.CodeOwnerBusy:
			return startData{}, e
		}
		if time.Now().After(deadline) {
			if busy {
				return startData{}, clicore.NewError(clicore.CodeOwnerBusy, sidecarText(clicore.CodeOwnerBusy), "Try again in a moment.")
			}
			return startData{}, clicore.NewError(clicore.CodeTimeout, "The app did not answer in time.", "Run 'unravis doctor'.")
		}
		if e := st.sleep(appPoll); e != nil {
			return startData{}, e
		}
	}
}

func sidecarText(c clicore.Code) string {
	switch c {
	case clicore.CodeElevationRefused:
		return "Unravis does not run as administrator. Start it from a normal window."
	case clicore.CodeOwnerBusy:
		return "Another Unravis owner is starting or running and did not answer."
	case clicore.CodeUpstreamUnavailable:
		return "Another Unravis is running without command line access, or is still starting."
	}
	return "The app stopped before it answered."
}

func sidecarAction(c clicore.Code) string {
	if c == clicore.CodeUpstreamUnavailable {
		return "Quit it, then run 'unravis app start --headless' again."
	}
	return ""
}

type stopData struct {
	Stopped        bool `json:"stopped"`
	AlreadyStopped bool `json:"alreadyStopped"`
	PID            int  `json:"pid,omitempty"`
}

func (st *state) appStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "stop",
		Short:       "Stop a headless app",
		Long:        "Stops the app that 'unravis app start --headless' started and waits until it is gone. An app that the desktop shell owns is not stopped from here: quit it from the tray. An app that does not run is already stopped.",
		Args:        cobra.NoArgs,
		Annotations: online("app.stop", ""),
		RunE: func(c *cobra.Command, _ []string) error {
			a, err := st.noArgs(c, "app stop")
			if err != nil {
				return err
			}
			d, e := st.stopHeadless(a)
			if e != nil {
				return e
			}
			return st.emit("app.stop", d, clicore.Meta{Coverage: "complete", Identifiers: "hidden"}, func(w io.Writer) error {
				rows := [][]string{{"stopped", strconv.FormatBool(d.Stopped)}, {"already stopped", strconv.FormatBool(d.AlreadyStopped)}}
				if d.PID != 0 {
					rows = append(rows, []string{"pid", strconv.Itoa(d.PID)})
				}
				return clicore.WriteTable(w, []string{"FIELD", "VALUE"}, rows)
			})
		},
	}
}

func (st *state) stopHeadless(args json.RawMessage) (stopData, *clicore.Error) {
	sess, e := st.attach(st.ctx)
	if absent(e) {
		return stopData{AlreadyStopped: true}, nil
	}
	if e != nil {
		return stopData{}, e
	}
	pid := sess.Hello.PID
	started, _ := time.Parse(time.RFC3339Nano, sess.Hello.Gen)
	if sess.Hello.Owner != "headless" {
		return stopData{}, clicore.NewError(clicore.CodePermissionDenied, "Unravis is owned by the desktop app. Quit it from the tray.", "")
	}
	callID, err := newCallID()
	if err != nil {
		return stopData{}, clicore.NewError(clicore.CodeInternal, "No random source for the call id.", "")
	}
	if _, e := sess.Do(st.ctx, clicore.Call{Op: "app.stop", Args: args, RequestID: st.reqID, CallID: callID, Deadline: st.opts.timeout}); e != nil {
		return stopData{}, e
	}
	deadline := time.Now().Add(stopWait)
	for {
		if e := st.stopped(); e != nil {
			return stopData{}, e
		}
		s, e := st.attach(st.ctx)
		if absent(e) || (e == nil && s.Hello.PID != pid) {
			// the locator is gone before the process is: wait until the process is, so a start that follows finds the name free
			if st.io.ProcessGone == nil || st.io.ProcessGone(pid, started) {
				return stopData{Stopped: true, PID: pid}, nil
			}
		}
		if time.Now().After(deadline) {
			return stopData{}, clicore.NewError(clicore.CodeTimeout, "The app did not stop in time.", "")
		}
		if e := st.sleep(appPoll); e != nil {
			return stopData{}, e
		}
	}
}
