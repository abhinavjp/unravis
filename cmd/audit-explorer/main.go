package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	explorer "api-audit-explorer"
	"api-audit-explorer/internal/applog"
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/datadir"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/instance"
	"api-audit-explorer/internal/ledger"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/store"
	"api-audit-explorer/internal/tray"
	"api-audit-explorer/internal/version"
)

const (
	memorySoftLimit = 256 << 20
	// defaultPort 0 lets the OS choose a free loopback port, so no other local
	// process can claim a well-known port before the dashboard starts. The real
	// address is printed and written to the session file for --mcp.
	defaultPort      = 0
	httpReadTimeout  = 35 * time.Second
	httpWriteTimeout = httpReadTimeout + max(audit.OperationTimeout, dbhealth.LaneRunTimeout) + 10*time.Second
)

// stdout is where --version prints. Tests swap it.
var stdout io.Writer = os.Stdout

func main() {
	// The console shim comes first: a windowless build has no console until it
	// attaches to its parent's, and stdout was captured before that.
	attachParentConsole()
	stdout = os.Stdout
	err := run(os.Args[1:], os.Getenv)
	// run has returned: its cleanup (session file, server, tray icon) is done, so
	// an end-session handler waiting on runDone can let Windows go on.
	close(runDone)
	if err != nil {
		reportFatal(err)
	}
}

// runFlags is the parsed and validated command line.
type runFlags struct {
	demo                bool
	port                int
	noBrowser           bool
	mcp                 bool
	mcpAllowIdentifiers bool
	allowBroadScans     bool
	docsExaminedBudget  int64
	version             bool
	quit                bool
	background          bool
}

// parseFlags parses and validates the command line. Every privacy and
// live-source switch defaults to off.
func parseFlags(args []string) (runFlags, error) {
	var f runFlags
	flags := flag.NewFlagSet("unravis", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.BoolVar(&f.demo, "demo", false, "run with deterministic illustrative data")
	flags.IntVar(&f.port, "port", defaultPort, "loopback HTTP port; 0 (the default) chooses an available port. With --mcp, the running dashboard's port (by default read from its session file)")
	flags.BoolVar(&f.noBrowser, "no-browser", false, "do not open the local dashboard")
	flags.BoolVar(&f.mcp, "mcp", false, "serve read-only MCP over stdio, using the dashboard already running for this user (found through its session file)")
	flags.BoolVar(&f.mcpAllowIdentifiers, "mcp-allow-identifiers", false, "with --mcp: let MCP return user ids, user agents and IP addresses, and filter on them, URLs and audit ids (off by default, demo included)")
	flags.BoolVar(&f.allowBroadScans, "allow-broad-scans", false, "dashboard only: allow checks that read every record in a window on a live source (off by default)")
	flags.Int64Var(&f.docsExaminedBudget, "docs-examined-budget", 0, "dashboard only: cap on records examined per query; 0 leaves it unset")
	flags.BoolVar(&f.version, "version", false, "print the version and exit")
	flags.BoolVar(&f.quit, "quit", false, "ask the running Unravis to quit")
	flags.BoolVar(&f.background, "background", false, "start in the tray without opening a browser; used by start at sign-in")
	if err := flags.Parse(args); err != nil {
		return runFlags{}, err
	}
	if f.version {
		// Asking for the version wins over every other flag, so it never fails on them.
		return runFlags{version: true}, nil
	}
	if flags.NArg() != 0 {
		return runFlags{}, fmt.Errorf("unexpected positional arguments")
	}
	if f.quit && flags.NFlag() > 1 {
		return runFlags{}, fmt.Errorf("--quit cannot be combined with other options")
	}
	if f.background && hostOS != "windows" {
		return runFlags{}, fmt.Errorf("--background is only available on Windows")
	}
	if f.background && f.noBrowser {
		return runFlags{}, fmt.Errorf("--background and --no-browser cannot be combined")
	}
	if f.background && f.mcp {
		return runFlags{}, fmt.Errorf("--background starts the dashboard; do not combine it with --mcp")
	}
	if f.port < 0 || f.port > 65535 {
		return runFlags{}, fmt.Errorf("port must be between 0 and 65535")
	}
	if f.mcpAllowIdentifiers && !f.mcp {
		return runFlags{}, fmt.Errorf("--mcp-allow-identifiers is only valid with --mcp")
	}
	if f.docsExaminedBudget < 0 {
		return runFlags{}, fmt.Errorf("--docs-examined-budget must not be negative")
	}
	if f.mcp {
		if f.demo {
			return runFlags{}, fmt.Errorf("--mcp connects to an existing dashboard; do not combine it with --demo")
		}
		if f.allowBroadScans || f.docsExaminedBudget != 0 {
			return runFlags{}, fmt.Errorf("--allow-broad-scans and --docs-examined-budget configure the dashboard; do not combine them with --mcp")
		}
	}
	return f, nil
}

// storeOptions turns the dashboard flags into store options.
func storeOptions(f runFlags) []store.Option {
	return []store.Option{store.WithBroadScans(f.allowBroadScans), store.WithDocsExaminedBudget(f.docsExaminedBudget)}
}

// run is the whole program after the command line is parsed. Each step is a small named function below; run keeps their order and the
// defers that undo them (they run last-in first-out, so the session file goes before the server, the server before the stores).
func run(args []string, getenv func(string) string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	if f.version {
		_, err := fmt.Fprintln(stdout, version.String())
		return err
	}
	if f.quit {
		if code := runQuit(os.Stderr, quitSignal); code != 0 {
			exitProcess(code)
		}
		return nil
	}
	demoCfg, demoNotes, err := demoEnv(f.demo, getenv, time.Now())
	if err != nil {
		return err
	}
	for _, note := range demoNotes {
		log.Print(note)
	}
	if f.mcp {
		return runMCP(f, getenv)
	}
	// One interactive copy per user. --no-browser (the e2e launcher) never
	// reaches the gate: no mutex, no events. --mcp, --version and --quit have
	// already returned.
	interactive := !f.noBrowser && hostOS == "windows"
	background := f.background
	openBrowser := interactive && !background
	var instanceNames instance.Names
	if interactive {
		gate, err := enterSingleInstance(f.demo, background, productionGateDeps())
		if err != nil {
			return err
		}
		if !gate.proceed {
			return nil
		}
		defer gate.release()
		instanceNames = gate.names
	}
	token, err := sessionToken(getenv)
	if err != nil {
		return err
	}
	debug.SetMemoryLimit(memorySoftLimit)

	logFile := openSupportLog(getenv, token)
	if logFile != nil {
		defer logFile.Close()
	}

	wired, err := wireData(f, demoCfg, getenv)
	if err != nil {
		return err
	}
	defer wired.closeCases()
	defer wired.closeRepo()
	serverOptions := wired.options

	listener, err := listenLoopback(f.port)
	if err != nil {
		return err
	}
	// Listen for Ctrl+C before the session file exists, so an early interrupt
	// still takes the clean path that removes it.
	// appCtx is cancelled by a quit signal from another copy (--quit, the tray);
	// the signal context below is its child, so it runs the same shutdown as Ctrl+C.
	appCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signalContext, stop := signal.NotifyContext(appCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	localURL := "http://" + listener.Addr().String()
	// The session token never goes on a command line or into a log line. The
	// browser is opened on a single-use /launch link; the server answers it with
	// a redirect that carries the token in the fragment, which the browser never
	// sends back. The page reads the fragment and removes it from the address bar.
	codeLife := launchCodeLifeManual
	if openBrowser {
		codeLife = launchCodeLifeBrowser
	}
	// A --background start mints no code: one is made only when someone asks to open.
	code, err := startupCode(background, mintLaunchCode)
	if err != nil {
		return err
	}
	if code != "" {
		serverOptions = append(serverOptions, server.WithLaunchCode(code, time.Now().Add(codeLife)))
	}
	if logFile != nil {
		serverOptions = append(serverOptions, server.WithLogger(logFile.Logger), server.WithLogDir(logFile.Dir))
	}
	served := server.New(wired.repo, explorer.Assets(), token, f.demo, serverOptions...)
	if !f.demo {
		// Open the saved databases whose password is saved, a few at a time and with a time limit, so a slow one never holds the app back.
		// It stops on Ctrl+C or a quit signal, and the deferred wait below ends it before the repository closes.
		reconnected := make(chan struct{})
		go func() {
			defer close(reconnected)
			server.ReconnectSavedSources(signalContext, served, server.StartupReconnectBudget)
		}()
		defer func() { cancel(); <-reconnected }()
	}
	reo := newReopener(served, localURL)
	defer server.LogAnalyzeRepeats(served)
	// Registered after the case store's close, so it runs first: background
	// imports stop and roll back before the store closes.
	defer server.CancelImports(served)
	httpServer := newHTTPServer(served)
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()

	removeSession := writeSessionFile(getenv, listener, token)
	defer removeSession()

	if interactive {
		defer startInteractive(appCtx, cancel, reo, instanceNames)()
	}

	opened := false
	if openBrowser {
		opened = openInBrowser(launchLink(localURL, code)) == nil
	}
	printed := false
	if code == "" {
		log.Printf("%s running in the tray at %s (demo=%t)", version.Short(), localURL, f.demo)
	} else {
		printed = printStartup(startupLines(localURL, code, codeLife, f.demo, opened), opened, func(s string) { log.Print(s) }, os.Stderr)
	}
	if logFile != nil {
		logStart(logFile, getenv, listener, f.demo, launchOutcome(opened, printed))
	}
	return awaitShutdown(signalContext, serveDone, removeSession, httpServer)
}

// runMCP serves the read-only MCP bridge over stdio, against the dashboard another copy already runs.
func runMCP(f runFlags, getenv func(string) string) error {
	options := []mcp.Option{mcp.WithIdentifiers(f.mcpAllowIdentifiers), mcp.WithToken(getenv("AUDIT_SESSION_TOKEN"))}
	if path, pathErr := mcp.SessionFilePath(getenv); pathErr == nil {
		options = append(options, mcp.WithSessionFile(path))
	}
	bridge, err := mcp.New(f.port, options...)
	if err != nil {
		return err
	}
	debug.SetMemoryLimit(memorySoftLimit)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return bridge.Serve(ctx, os.Stdin, os.Stdout)
}

// openSupportLog opens the journey log: one rotating file in the per-user logs folder, kept so the owner can send it to a developer
// when something goes wrong. If it cannot be opened the dashboard still runs (nil result); it just has no file to send. The caller
// closes a non-nil log.
func openSupportLog(getenv func(string) string, token string) *applog.File {
	logFile, logErr := applog.Open(applog.DefaultDir(getenv), applog.LevelFromEnv(getenv), casestore.SecureFile)
	if logErr != nil {
		log.Printf("Support log unavailable: %v", logErr)
		return nil
	}
	// The session token is registered so the sanitizer strips it wherever
	// it might turn up in an error message. It is never logged on purpose.
	applog.RegisterSecret(token)
	// The lines Unravis already prints keep going to the console, and to
	// the file as well.
	log.SetOutput(applog.Tee(os.Stderr, logFile.Writer()))
	return logFile
}

// dataWiring is what the audit data, the hypothesis ledger, the case store and the Blackbox contribute to the server.
type dataWiring struct {
	repo    audit.Repository
	options []server.Option
	// closeCases closes the case store; closeRepo closes the audit repository. The caller defers closeCases first, then closeRepo.
	closeCases func()
	closeRepo  func()
}

// wireData opens the audit repository (demo data or the saved sources), the ledger, the cases and the Blackbox, in that order.
func wireData(f runFlags, demoCfg demoSettings, getenv func(string) string) (dataWiring, error) {
	var repo audit.Repository
	var serverOptions []server.Option
	var saved savedWiring
	if f.demo {
		// The shipped demo carries a little unfinished work so the incomplete panel has something to show.
		repo = audit.NewDemoWithPatterns(demoCfg.now)
	} else {
		saved = wireSaved(getenv, storageFor(getenv))
		repo = saved.auditRepo(store.New(storeOptions(f)...))
		serverOptions = append(serverOptions, saved.options...)
		if links := openSourceLinks(getenv); links != nil {
			serverOptions = append(serverOptions, server.WithSourceLinks(links))
		}
	}
	hypotheses, ledgerErr := openLedger(f.demo, getenv)
	if ledgerErr != nil {
		log.Printf("Hypothesis ledger unavailable: %v", ledgerErr)
		hypotheses = nil
	} else {
		serverOptions = append(serverOptions, server.WithLedger(hypotheses))
	}
	// Imported cases (the importer wave) are live-mode only: demo mode runs on
	// illustrative data and writes nothing to disk, so its case and host routes
	// answer 503 "not available" rather than keep a case store nobody asked for.
	// The one exception is the screenshot harness: --demo with UNRAVIS_DEMO_CASES=1
	// and AUDIT_CONFIG_DIR opens a seeded store under that folder.
	caseOptions, closeCases, casesErr := wireCases(f.demo, demoCfg, getenv, hypotheses)
	if casesErr != nil {
		return dataWiring{}, casesErr
	}
	serverOptions = append(serverOptions, caseOptions...)
	serverOptions = append(serverOptions, wireBlackbox(f.demo, demoCfg, getenv, saved.bb)...)
	return dataWiring{
		repo:       repo,
		options:    serverOptions,
		closeCases: closeCases,
		closeRepo: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = repo.Close(ctx)
		},
	}, nil
}

// newReopener builds the "open Unravis" handler for the running server.
func newReopener(served http.Handler, localURL string) *reopener {
	return &reopener{
		base:    localURL,
		rearm:   func(c string, u time.Time) bool { return server.Rearm(served, c, u) },
		newCode: mintLaunchCode,
		open:    openInBrowser,
		warn:    showMessage,
		now:     time.Now,
		link:    func(c string) string { return launchLink(localURL, c) },
	}
}

// newHTTPServer wraps the dashboard handler with the server's limits.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}

// writeSessionFile writes the session file --mcp reads (port and token) and returns the function that removes it. When the file
// cannot be written it logs that and returns a no-op.
func writeSessionFile(getenv func(string) string, listener net.Listener, token string) (remove func()) {
	sessionPath, sessionErr := mcp.SessionFilePath(getenv)
	if sessionErr == nil {
		// The file is restricted to this user before the token is written to it.
		sessionErr = mcp.WriteSessionFileSecured(sessionPath, listener.Addr().(*net.TCPAddr).Port, token, casestore.SecureFile)
	}
	if sessionErr != nil {
		log.Printf("Session file not written; --mcp needs --port and AUDIT_SESSION_TOKEN to reach this dashboard")
		return func() {}
	}
	return func() { _ = mcp.RemoveSessionFile(sessionPath, token) }
}

// startInteractive wires the parts of an interactive (single-instance, Windows) start: the instance events other copies reach this one
// through ("open" re-arms a launch code and opens the browser, "quit" cancels appCtx) and the tray icon. Quit and an end-session cancel
// appCtx, which runs the same shutdown as Ctrl+C. If either cannot start the app still runs. The result stops the tray, then the
// instance listener; the caller defers it.
func startInteractive(appCtx context.Context, cancel context.CancelFunc, reo *reopener, names instance.Names) (stop func()) {
	stopListen := func() {}
	if stopFn, lerr := instance.Listen(appCtx, names, reo.Reopen, cancel); lerr != nil {
		log.Printf("Second launches cannot reach this copy: %v", lerr)
	} else {
		stopListen = stopFn
	}
	deps := productionTrayDeps(reo.Reopen, cancel, showMessage)
	opts := tray.Options{
		Tooltip:      "Unravis",
		Menu:         func() []tray.Item { return trayMenu(deps) },
		OnSelect:     trayOnSelect(deps),
		OnOpen:       reo.Reopen,
		OnEndSession: endSessionHandler(cancel, &endingSession, runDone, endSessionGuard),
		Logf:         log.Printf,
	}
	trayDone := startTray(appCtx, tray.Run, opts, log.Printf)
	return func() {
		stopTray(cancel, &endingSession, trayDone, trayExitGuard)
		stopListen()
	}
}

// launchOutcome names how the user gets in, for the start line. The launch code itself is never written, only this outcome.
func launchOutcome(opened, printed bool) string {
	switch {
	case opened:
		return "browser_opened"
	case printed:
		return "link_printed"
	}
	return "none"
}

// logStart writes the start line to the support log: what is running, where, and how the user gets in.
func logStart(logFile *applog.File, getenv func(string) string, listener net.Listener, demo bool, launch string) {
	layout := datadir.Default(getenv)
	dataDir := layout.Base
	if layout.Root != "" {
		dataDir = layout.Root
	}
	logFile.Logger.Info("start",
		"version", version.Version,
		"os", runtime.GOOS+"/"+runtime.GOARCH,
		"port", listener.Addr().(*net.TCPAddr).Port,
		"data_dir", dataDir,
		"demo", demo,
		"launch", launch)
}

// awaitShutdown blocks until the server stops by itself or a quit signal arrives, then shuts it down cleanly.
func awaitShutdown(signalContext context.Context, serveDone <-chan error, removeSession func(), httpServer shutdowner) error {
	select {
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalContext.Done():
		if err := shutdownOnSignal(removeSession, httpServer); err != nil {
			return err
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// openInBrowser opens url in the default browser. rundll32 is started by full
// path, so a planted rundll32.exe in the current folder or on PATH is never found.
func openInBrowser(url string) error {
	return exec.Command(system32File("rundll32.exe"), "url.dll,FileProtocolHandler", url).Start()
}

// listenLoopback opens the dashboard's listening socket on 127.0.0.1. When the port is taken it says so in plain words and names the
// next step instead of printing the raw bind error (D-C-18); any other failure keeps the raw error behind a short lead.
func listenLoopback(port int) (net.Listener, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err == nil {
		return listener, nil
	}
	if port != 0 && isAddressInUse(err) {
		return nil, fmt.Errorf("port %d on 127.0.0.1 is already in use. Another Unravis may already be running; close it, or start this one with a different --port (--port 0 picks a free one)", port)
	}
	return nil, fmt.Errorf("could not listen on loopback address: %w", err)
}

// isAddressInUse reports whether a listen error means the port is already taken. Windows reports WSAEADDRINUSE (10048), which is not
// syscall.EADDRINUSE there, so both are checked; the text is the last resort for a wrapped error that lost its code.
func isAddressInUse(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == syscall.EADDRINUSE || uintptr(errno) == 10048 {
			return true
		}
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "address already in use") || strings.Contains(text, "only one usage of each socket address")
}

const (
	// The code in a link we open ourselves only has to outlive the browser start.
	launchCodeLifeBrowser = 2 * time.Minute
	// A link printed for the user to open by hand gets longer.
	launchCodeLifeManual = 10 * time.Minute
)

// newLaunchCode returns a random single-use code for the /launch link. It is
// not the session token and cannot be used to call the API.
func newLaunchCode() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("could not generate a local launch code")
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func launchLink(localURL, code string) string {
	return localURL + "/launch?c=" + code
}

// startupLines is what the dashboard logs when it starts. It never contains the
// session token. When the browser was opened for the user, it contains no link
// at all. Otherwise it contains the one-time launch link, which works once and
// then expires.
func startupLines(localURL, code string, life time.Duration, demo, opened bool) []string {
	lines := []string{fmt.Sprintf("%s listening at %s (demo=%t)", version.Short(), localURL, demo)}
	if opened {
		return append(lines, "Unravis was opened in your browser. To open it again, restart it, or start it with --no-browser to get a link.")
	}
	return append(lines, fmt.Sprintf("Open Unravis with this one-time link (works once, expires in %d minutes): %s", int(life/time.Minute), launchLink(localURL, code)))
}

// shutdownTimeout stays under the five seconds Windows gives a console process
// after its window is closed (CTRL_CLOSE_EVENT, delivered as SIGTERM).
const shutdownTimeout = 4 * time.Second

type shutdowner interface {
	Shutdown(context.Context) error
	Close() error
}

// shutdownOnSignal removes the session file first, so a dashboard killed while
// it drains requests never leaves a file pointing at a port it no longer
// holds, then shuts the server down within shutdownTimeout.
func shutdownOnSignal(removeSession func(), server shutdowner) error {
	removeSession()
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
		return fmt.Errorf("server shutdown did not finish cleanly")
	}
	return nil
}

// openLedger opens the hypothesis ledger. Demo mode keeps it in memory; live
// mode keeps it sealed (ledger.dat) in the base folder: per user, beside the
// saved connections, sealed for the current user; shared, in the data folder,
// sealed for this PC. An older plain ledger.json is migrated on first start.
// Without a sealing backend the ledger is unavailable rather than written in
// plaintext.
func openLedger(demo bool, getenv func(string) string) (ledger.Store, error) {
	if demo {
		return ledger.Open("")
	}
	st := storageFor(getenv)
	base, err := st.base(getenv)
	if err != nil {
		return nil, err
	}
	protector, err := st.protector()
	if err != nil {
		return nil, err
	}
	return ledger.OpenSealed(filepath.Join(base, "ledger.dat"), filepath.Join(base, "ledger.json"), protector)
}

func sessionToken(getenv func(string) string) (string, error) {
	if token := getenv("AUDIT_SESSION_TOKEN"); token != "" {
		if utf8.RuneCountInString(token) < 32 {
			return "", fmt.Errorf("AUDIT_SESSION_TOKEN must contain at least 32 characters")
		}
		if len(token) > 4096 {
			return "", fmt.Errorf("AUDIT_SESSION_TOKEN must not exceed 4096 bytes")
		}
		for _, value := range token {
			if value < 0x21 || value > 0x7e {
				return "", fmt.Errorf("AUDIT_SESSION_TOKEN must use visible ASCII characters")
			}
		}
		return token, nil
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("could not generate a local session token")
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
