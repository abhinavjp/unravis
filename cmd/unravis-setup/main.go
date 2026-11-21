// Command unravis-setup is the one-file Unravis installer: the setup window (a small page on a loopback port), silent mode, and the
// elevated worker the window starts when a choice needs administrator rights (buildout/plan/installer-and-releases.md).
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"api-audit-explorer/internal/setup"
	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

// ui is the built setup page (web/setup, built by "npm run build" into this folder with fixed file names).
//
//go:embed all:ui
var uiFiles embed.FS

// payload is the program files and manifest.json, put here by build.ps1 just before it builds setup. A build without them is a
// developer build: the window opens and says the setup program has no program files inside.
//
//go:embed payload
var payloadFiles embed.FS

func main() {
	os.Exit(run(os.Args[1:], osEnv()))
}

// env is everything run needs from the PC, so tests can run it against the fake system.
type env struct {
	stdout, stderr io.Writer
	now            func() time.Time
	system         func() (*setup.System, error)
	dirs           func() (setup.Dirs, error)
	userSID        func() (string, error)
	setupExe       func() (string, error)
	elevator       func(exe string, extra []string) setup.Elevator
	payload        func() (*setup.Payload, error)
	static         func() (fs.FS, error)
	// openWindow, openFolder and startApp are the window's doings (never the worker's).
	openWindow func(browser, url string) error
	openFolder func(dir string) error
	startApp   func(exe string) error
	// startBackground starts Unravis in the tray with no browser tab (an unattended update's restart).
	startBackground func(exe string) error
	// elevateWait runs exe with args as administrator (the Windows approval prompt), waits and returns its exit code (nil in tests).
	elevateWait func(exe string, args []string) (int, error)
	// notice shows a message box; used only when an unattended update leaves no running program (nil in tests).
	notice func(title, text string)
	// dialWorker is the worker's end of the pipe.
	dialWorker func(name string, parent uint32) (*setup.WorkerPipe, error)
	// listen opens the window's loopback port.
	listen func() (net.Listener, error)
	// idleAfter ends the window when the page has gone quiet for this long (0: never; tests).
	idleAfter time.Duration
	// signals ends the window on Ctrl+C (nil in tests).
	signals func(ctx context.Context) (context.Context, context.CancelFunc)
	// capture is set in the screenshot build (-tags capture): the fake backends the X-Setup-Scenario header picks.
	capture func() map[string]setup.UIBackend
}

func osEnv() env {
	return env{
		stdout: os.Stdout, stderr: os.Stderr, now: time.Now,
		system: setup.NewOSSystem, dirs: setup.OSDirs, userSID: setup.CurrentUserSID, setupExe: os.Executable,
		elevator: func(exe string, extra []string) setup.Elevator {
			return setup.OSElevator{SetupExe: exe, ExtraArgs: extra}
		},
		payload: func() (*setup.Payload, error) {
			sub, err := fs.Sub(payloadFiles, "payload")
			if err != nil {
				return nil, err
			}
			return setup.LoadPayload(sub)
		},
		static:     func() (fs.FS, error) { return fs.Sub(uiFiles, "ui") },
		openWindow: setup.OpenWindow, openFolder: setup.ShowFolder, startApp: setup.StartProgram, startBackground: setup.StartProgramBackground, notice: setup.ShowNotice,
		elevateWait: func(exe string, args []string) (int, error) { return setup.RunElevatedWait(exe, args, 5*time.Minute) },
		dialWorker:  setup.DialWorkerPipe, listen: setup.Listen, idleAfter: 3 * time.Minute,
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt)
		},
		capture: captureScenarios,
	}
}

type options struct {
	silent, uninstall, repair, upgrade, allowDowngrade, worker, noBrowser, showVersion, capture, closeApp, recover, noRestart bool
	config, logPath, pipe, verify                                                                                             string
	expectSHA, expectVersion                                                                                                  string // --expect-sha256 / --expect-version: what the updater verified
	parent                                                                                                                    uint
}

func parseFlags(args []string, e env, errOut io.Writer) (options, error) {
	var o options
	fl := flag.NewFlagSet("unravis-setup", flag.ContinueOnError)
	fl.SetOutput(errOut)
	fl.BoolVar(&o.silent, "silent", false, "install, upgrade, repair or uninstall without a window, as setup.json says")
	fl.StringVar(&o.config, "config", "", "the setup.json file for --silent")
	fl.StringVar(&o.logPath, "log", "", `where to write the setup log (default %LOCALAPPDATA%\Unravis\setup-logs)`)
	fl.BoolVar(&o.uninstall, "uninstall", false, "remove Unravis")
	fl.BoolVar(&o.repair, "repair", false, "put the program files, shortcuts and service back as installed")
	fl.BoolVar(&o.upgrade, "upgrade", false, "replace the installed version with this one")
	fl.BoolVar(&o.allowDowngrade, "allow-downgrade", false, "allow installing an older version over a newer one")
	fl.StringVar(&o.expectSHA, "expect-sha256", "", "refuse to run unless this setup program's own file has this SHA-256 (the update check passes it)")
	fl.StringVar(&o.expectVersion, "expect-version", "", "refuse to run unless this setup installs exactly this version (the update check passes it)")
	fl.StringVar(&o.verify, "verify", "", "check a release in this folder offline (update-manifest.json, its .sig and the files) and print the result; installs nothing")
	fl.BoolVar(&o.closeApp, "close-app", false, "with --silent --upgrade and the --expect flags: ask a running Unravis to quit, upgrade, then start it again (what an automatic update does)")
	fl.BoolVar(&o.recover, "recover", false, "put back what a setup run that was cut off (power loss, a killed setup) changed, from its journal, and start Unravis again; installs nothing")
	fl.BoolVar(&o.noRestart, "no-restart", false, "")
	fl.BoolVar(&o.noBrowser, "no-browser", false, "do not open the setup window; print its address instead")
	fl.BoolVar(&o.showVersion, "version", false, "print the version and exit")
	// The worker flags are for the copy the window starts with "run as administrator"; they are not shown in the help.
	fl.BoolVar(&o.worker, "worker", false, "")
	fl.StringVar(&o.pipe, "pipe", "", "")
	fl.UintVar(&o.parent, "parent", 0, "")
	if e.capture != nil {
		fl.BoolVar(&o.capture, "capture", false, "screenshot build: serve the setup page with fake scenarios")
	}
	fl.Usage = func() {
		fmt.Fprintln(errOut, "Usage: unravis-setup.exe [flags] (no flags opens the setup window)")
		fl.VisitAll(func(f *flag.Flag) {
			if f.Usage == "" {
				return
			}
			name, usage := flag.UnquoteUsage(f)
			if name != "" {
				name = " " + name
			}
			fmt.Fprintf(errOut, "  --%s%s\n    \t%s\n", f.Name, name, usage)
		})
	}
	if err := fl.Parse(args); err != nil {
		return o, err
	}
	if fl.NArg() > 0 {
		return o, fmt.Errorf("unexpected %q (setup takes only flags; run --help to see them)", fl.Arg(0))
	}
	n := 0
	for _, b := range []bool{o.uninstall, o.repair, o.upgrade} {
		if b {
			n++
		}
	}
	if n > 1 {
		return o, errors.New("choose only one of --uninstall, --repair and --upgrade")
	}
	if err := o.checkExpectFlags(); err != nil {
		return o, err
	}
	if o.closeApp && !(o.silent && o.upgrade && o.hasExpect()) {
		return o, errors.New("--close-app is for an update the app started: use it with --silent --upgrade --expect-sha256 --expect-version")
	}
	if o.config != "" && !o.silent {
		return o, errors.New("--config is for --silent; add --silent, or leave --config out to open the setup window")
	}
	if o.silent && o.config == "" && !o.uninstall && !o.repair && !o.upgrade {
		return o, errors.New("--silent needs --config setup.json (see install.md for its keys)")
	}
	if o.worker != (o.pipe != "") || (o.worker && o.parent == 0) {
		return o, errors.New("--worker is started by the setup window only")
	}
	if o.parent > 1<<32-1 {
		return o, errors.New("--parent is not a process id")
	}
	return o, nil
}

func (o options) op() setup.Op {
	switch {
	case o.uninstall:
		return setup.OpUninstall
	case o.repair:
		return setup.OpRepair
	case o.upgrade:
		return setup.OpUpgrade
	}
	return setup.OpInstall
}

// exitInvalid is the exit code of a command line or setup.json setup cannot use.
const exitInvalid = 2

func run(args []string, e env) int {
	o, err := parseFlags(args, e, e.stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup:", err)
		return exitInvalid
	}
	if o.showVersion {
		fmt.Fprintln(e.stdout, strings.Replace(version.String(), "Unravis", "Unravis Setup", 1))
		return 0
	}
	switch {
	case o.verify != "":
		return runVerify(o, e)
	case o.capture:
		return runCapture(o, e)
	case o.worker:
		return runWorker(o, e)
	case o.recover:
		return runRecover(o, e)
	case o.silent:
		return runSilent(o, e)
	}
	return runWindow(o, e)
}

// openLog opens the log file (--log, or the default under the person's LOCALAPPDATA). A log that cannot be opened is said once and
// setup goes on: the log is help, not a condition.
func openLog(o options, e env, localAppData string, worker bool) (*setup.FileLog, string) {
	p := o.logPath
	if p == "" || worker {
		p = setup.DefaultLogPath(localAppData, e.now(), worker)
	}
	l, err := setup.OpenFileLog(p)
	if err != nil {
		fmt.Fprintf(e.stderr, "unravis-setup: the log cannot be written to %s (%v); setup goes on without it\n", p, err)
		return nil, ""
	}
	return l, p
}

// newRunner builds the runner for this PC: the system, the folders, the payload and this program.
func newRunner(e env) (*setup.Runner, setup.Dirs, error) {
	sys, err := e.system()
	if err != nil {
		return nil, setup.Dirs{}, err
	}
	d, err := e.dirs()
	if err != nil {
		return nil, d, err
	}
	r := &setup.Runner{Sys: sys, Dirs: d}
	if exe, err := e.setupExe(); err == nil {
		r.SetupExe = exe
	}
	if sid, err := e.userSID(); err == nil {
		r.UserSID = sid
	}
	return r, d, nil
}

func runSilent(o options, e env) int {
	r, d, err := newRunner(e)
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup:", err)
		return setup.OutcomePreflightFailed.ExitCode() // setup cannot look at the PC, so it changed nothing
	}
	l, logPath := openLog(o, e, d.LocalAppData, false)
	defer closeLog(l)
	r.Log = l.Line
	say := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		fmt.Fprintln(e.stdout, msg)
		l.Line(msg)
	}
	l.Line("silent " + string(o.op()) + " started by " + version.Short())
	cfg := setup.SilentConfig{}
	if o.config != "" {
		data, err := readConfig(o.config)
		if err == nil {
			cfg, err = setup.ParseSilentConfig(data)
		}
		if err != nil {
			say("Setup cannot use the config: %s. Nothing was changed.", err)
			return exitInvalid
		}
	}
	req, err := cfg.Request(o.op())
	if err != nil {
		say("Setup cannot use the config: %s. Nothing was changed.", err)
		return exitInvalid
	}
	req.AllowDowngrade = o.allowDowngrade
	req.Options.CloseRunningApp = o.closeApp
	if p, err := e.payload(); err == nil {
		r.Payload = p
	} else if o.op() != setup.OpUninstall {
		say("%s Nothing was changed.", err)
		return setup.OutcomePreflightFailed.ExitCode()
	}
	release, err := verifyExpect(o, e, payloadVersionOf(r))
	defer release()
	if err != nil {
		say("%s", err)
		return setup.OutcomePreflightFailed.ExitCode()
	}
	old, _ := setup.Detect(r.Sys.Reg)
	setup.FillRecordingsConfirmation(&req, old)
	opts := req.Options
	if old != nil {
		opts = old.State.Options() // upgrade, repair, uninstall and a second install keep the choices of the install
	}
	var res setup.Result
	if setup.NeedsAdminFor(r.Sys.Svc, opts) && !r.Sys.Mach.IsAdmin() {
		u := d.User()
		req.User = &u
		say("This needs administrator approval; Windows will ask.")
		// The worker never touches a link inside the user's profile (WP-74i); this process, as the user, does it after the worker
		// has committed, through the same two functions the setup window uses.
		payloadVersion := ""
		if r.Payload != nil {
			payloadVersion = r.Payload.Manifest.Version
		}
		r.DeriveStartAtSignIn(setup.ResolveOp(req.Op, old, payloadVersion), opts, &req.Options)
		res = setup.RunWorker(context.Background(), e.elevator(r.SetupExe, o.expectArgs()), req, nil, func(s string) { l.Line("worker: " + s) })
		o2 := opts
		o2.StartAtSignIn = req.Options.StartAtSignIn
		r.ApplyProfileLinks(o2, &res, func(s string) { l.Line("links: " + s) })
	} else {
		res = r.Perform(context.Background(), req)
	}
	report(say, res, logPath)
	if o.closeApp {
		afterUnattended(o, e, d, old, res, say, l)
	}
	return res.Outcome.ExitCode()
}

// afterUnattended is the end of an update the app started and nobody watches: start the app again if it was running, and leave a
// plain note for the next start (or the current one) to show. A failed run was rolled back by the engine; the old version is started
// again then too, so the person is never left with no program.
func afterUnattended(o options, e env, d setup.Dirs, old *setup.Installed, res setup.Result, say func(string, ...any), l *setup.FileLog) {
	from := ""
	loc := ""
	if old != nil {
		from, loc = old.Version, old.Location
	}
	r := update.ApplyResult{From: from, To: o.expectVersion, At: e.now().UTC()}
	switch res.Outcome {
	case setup.OutcomeDone:
		r.Outcome = "done"
		r.Message = "Updated to " + o.expectVersion + "."
		if o.allowDowngrade { // the app went back to the kept version because the new one kept failing to start
			r.Outcome = "rolled-back"
			r.Message = "Version " + from + " did not start properly, so Unravis went back to " + o.expectVersion + ". That version will not be offered again."
		}
	case setup.OutcomeRolledBack:
		r.Outcome = "rolled-back"
		r.Message = "The update to " + o.expectVersion + " did not work and was undone. Unravis stays on " + from + ". " + res.Message
	default:
		r.Outcome = "failed"
		r.Message = "The update to " + o.expectVersion + " did not happen. Unravis stays on " + from + ". " + res.Message
	}
	// --close-app is only ever passed by the app itself, so the app was running when it asked. Start it again in the tray with no
	// browser tab (the new version after a good run, the old one after a rollback) unless it is already up. Waiting for a snapshot
	// would lose the app when it left before setup looked.
	failedToStart := false
	if loc != "" && e.startBackground != nil {
		if running, _ := appRunning(e, loc); !running {
			if err := e.startBackground(filepath.Join(loc, "unravis.exe")); err != nil {
				l.Line("restart: could not start Unravis: " + err.Error())
				r.Message += " Unravis could not be started again; open it from the Start menu."
				failedToStart = true
			} else {
				r.Restarted = true
			}
		}
	}
	// The note is written first: the message box below waits for a click, and the note must not wait with it.
	if d.LocalAppData != "" {
		if err := update.WriteApplyResult(filepath.Join(d.LocalAppData, "Unravis", "update-result"), r); err != nil {
			l.Line("result: could not write the update result: " + err.Error())
		}
	}
	if failedToStart && e.notice != nil {
		e.notice("Unravis", r.Message)
	}
	say("update result: %s", r.Outcome)
}

// report prints the end of a silent run. It never prints the read key: that went to readKeyFile.
func report(say func(string, ...any), res setup.Result, logPath string) {
	say("%s: %s", res.Outcome, res.Message)
	for _, c := range res.Checks {
		if c.Status == setup.CheckFailed {
			say("  %s: %s %s", c.Title, c.Detail, c.Fix)
		}
	}
	if res.Fix != "" {
		say("  %s", res.Fix)
	}
	for _, x := range res.Left {
		say("  left: %s", x)
	}
	if logPath != "" {
		say("Log: %s", logPath)
	}
}

func readConfig(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("setup.json cannot be opened (%v)", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("setup.json cannot be read (%v)", err)
	}
	return data, nil
}

func closeLog(l *setup.FileLog) {
	if l != nil {
		l.Close()
	}
}

// runWorker is the elevated copy: it serves exactly one request from the window over the pipe, then ends.
func runWorker(o options, e env) int {
	r, _, err := newRunner(e)
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup worker:", err)
		return setup.OutcomeInvalid.ExitCode()
	}
	if !r.Sys.Mach.IsAdmin() {
		fmt.Fprintln(e.stderr, "unravis-setup worker: not running as administrator")
		return setup.OutcomeInvalid.ExitCode()
	}
	// Check this file and payload against what the update check verified before anything else, and before the window is told anything.
	if p, err := e.payload(); err == nil {
		r.Payload = p
	}
	release, err := verifyExpect(o, e, payloadVersionOf(r))
	defer release()
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup worker:", err)
		return setup.OutcomePreflightFailed.ExitCode()
	}
	wp, err := e.dialWorker(o.pipe, uint32(o.parent))
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup worker:", err)
		return setup.OutcomeInvalid.ExitCode()
	}
	defer wp.Conn.Close()
	// The worker writes no log file: it must not create or append to anything under the person's profile. ServeWorker sends each
	// line to the window as MsgLog, and the window (or silent mode) writes the one log, as the user, as "worker: ...".
	r.Log = nil
	r.UserSID = ""
	res := setup.ServeWorker(context.Background(), wp.Conn, r, setup.WorkerPrepare(wp.SID, wp.Profile))
	return res.Outcome.ExitCode()
}

// runWindow serves the setup page and opens it.
func runWindow(o options, e env) int {
	r, d, err := newRunner(e)
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup:", err)
		return setup.OutcomeInvalid.ExitCode()
	}
	l, logPath := openLog(o, e, d.LocalAppData, false)
	defer closeLog(l)
	r.Log = l.Line
	l.Line("setup window started by " + version.Short())
	if p, err := e.payload(); err == nil {
		r.Payload = p
	} else {
		l.Line("payload: " + err.Error())
	}
	release, err := verifyExpect(o, e, payloadVersionOf(r))
	defer release()
	if err != nil {
		l.Line("expect: " + err.Error())
		fmt.Fprintln(e.stderr, "unravis-setup:", err)
		return setup.OutcomePreflightFailed.ExitCode()
	}
	be := &setup.LocalBackend{R: r, Elev: e.elevator(r.SetupExe, o.expectArgs()), Mode: o.op(), IsAdmin: r.Sys.Mach.IsAdmin(), LogPath: logPath,
		Log: l.Line, AllowDowngrade: o.allowDowngrade}
	return serve(o, e, be, nil, r.Sys.Mach.Browser(), l.Line)
}

func serve(o options, e env, be setup.UIBackend, scenarios map[string]setup.UIBackend, browser string, logLine func(string)) int {
	static, err := e.static()
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup:", err)
		return setup.OutcomeInvalid.ExitCode()
	}
	key, err := setup.NewKey()
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup:", err)
		return setup.OutcomeInvalid.ExitCode()
	}
	ln, err := e.listen()
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup: cannot open the setup window's port:", err)
		return setup.OutcomeInvalid.ExitCode()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if e.signals != nil {
		var stop context.CancelFunc
		ctx, stop = e.signals(ctx)
		defer stop()
	}
	srv := &setup.UIServer{Key: key, Static: static, Backend: be, Scenarios: scenarios, Addr: ln.Addr().String(),
		OpenApp: e.startApp, OpenFolder: e.openFolder, Quit: cancel}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	url := srv.URL()
	logLine("setup window at " + url) // the log hides the key
	if o.noBrowser || o.capture {
		fmt.Fprintln(e.stdout, "Unravis Setup is listening at "+url)
	} else if err := e.openWindow(browser, url); err != nil {
		fmt.Fprintln(e.stdout, "Open this address in your browser: "+url)
	}
	if e.idleAfter > 0 {
		go func() {
			tick := 15 * time.Second
			if e.idleAfter < tick {
				tick = e.idleAfter
			}
			t := time.NewTicker(tick)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-t.C:
					if srv.Idle(now, e.idleAfter) {
						logLine("the setup window was closed; setup ends")
						cancel()
						return
					}
				}
			}
		}()
	}
	<-ctx.Done()
	shut, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	_ = hs.Shutdown(shut)
	return 0
}

// appRunning says whether the installed Unravis program is running now.
func appRunning(e env, loc string) (bool, error) {
	sys, err := e.system()
	if err != nil {
		return false, err
	}
	list, err := sys.Proc.List()
	if err != nil {
		return false, err
	}
	want := strings.ToLower(filepath.Join(loc, "unravis.exe"))
	for _, q := range list {
		if strings.ToLower(filepath.Clean(q.Exe)) == want {
			return true, nil
		}
	}
	return false, nil
}
