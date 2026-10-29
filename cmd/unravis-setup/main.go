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
	elevator       func(exe string) setup.Elevator
	payload        func() (*setup.Payload, error)
	static         func() (fs.FS, error)
	// openWindow, openFolder and startApp are the window's doings (never the worker's).
	openWindow func(browser, url string) error
	openFolder func(dir string) error
	startApp   func(exe string) error
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
		elevator: func(exe string) setup.Elevator { return setup.OSElevator{SetupExe: exe} },
		payload: func() (*setup.Payload, error) {
			sub, err := fs.Sub(payloadFiles, "payload")
			if err != nil {
				return nil, err
			}
			return setup.LoadPayload(sub)
		},
		static:     func() (fs.FS, error) { return fs.Sub(uiFiles, "ui") },
		openWindow: setup.OpenWindow, openFolder: setup.ShowFolder, startApp: setup.StartProgram,
		dialWorker: setup.DialWorkerPipe, listen: setup.Listen, idleAfter: 3 * time.Minute,
		signals: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt)
		},
		capture: captureScenarios,
	}
}

type options struct {
	silent, uninstall, repair, upgrade, allowDowngrade, worker, noBrowser, showVersion, capture bool
	config, logPath, pipe                                                                      string
	parent                                                                                     uint
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
	if o.config != "" && !o.silent {
		return o, errors.New("--config is for --silent; add --silent, or leave --config out to open the setup window")
	}
	if o.silent && o.config == "" && !o.uninstall && !o.repair {
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
	case o.capture:
		return runCapture(o, e)
	case o.worker:
		return runWorker(o, e)
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
	if p, err := e.payload(); err == nil {
		r.Payload = p
	} else if o.op() != setup.OpUninstall {
		say("%s Nothing was changed.", err)
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
		res = setup.RunWorker(context.Background(), e.elevator(r.SetupExe), req, nil, func(s string) { l.Line("worker: " + s) })
	} else {
		res = r.Perform(context.Background(), req)
	}
	report(say, res, logPath)
	return res.Outcome.ExitCode()
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
	wp, err := e.dialWorker(o.pipe, uint32(o.parent))
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup worker:", err)
		return setup.OutcomeInvalid.ExitCode()
	}
	defer wp.Conn.Close()
	// The worker's own log goes next to the window's, in the person's folder (by the profile Windows names, not by the request).
	l, _ := openLog(o, e, filepath.Join(wp.Profile, "AppData", "Local"), true)
	defer closeLog(l)
	r.Log = l.Line
	r.UserSID = ""
	if p, err := e.payload(); err == nil {
		r.Payload = p
	}
	l.Line("worker started by " + version.Short())
	res := setup.ServeWorker(context.Background(), wp.Conn, r, setup.WorkerPrepare(wp.SID, wp.Profile))
	l.Line("worker finished: " + string(res.Outcome))
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
	be := &setup.LocalBackend{R: r, Elev: e.elevator(r.SetupExe), Mode: o.op(), IsAdmin: r.Sys.Mach.IsAdmin(), LogPath: logPath,
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
