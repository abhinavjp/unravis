package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"
	"unicode/utf8"

	explorer "api-audit-explorer"
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/ledger"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/store"
)

const (
	memorySoftLimit = 256 << 20
	// defaultPort 0 lets the OS choose a free loopback port, so no other local
	// process can claim a well-known port before the dashboard starts. The real
	// address is printed and written to the session file for --mcp.
	defaultPort      = 0
	httpReadTimeout  = 35 * time.Second
	httpWriteTimeout = httpReadTimeout + audit.OperationTimeout + 10*time.Second
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		log.Fatal(err)
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
	if err := flags.Parse(args); err != nil {
		return runFlags{}, err
	}
	if flags.NArg() != 0 {
		return runFlags{}, fmt.Errorf("unexpected positional arguments")
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

func run(args []string, getenv func(string) string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	demo, port, noBrowser, mcpMode := &f.demo, &f.port, &f.noBrowser, &f.mcp
	if *mcpMode {
		options := []mcp.Option{mcp.WithIdentifiers(f.mcpAllowIdentifiers), mcp.WithToken(getenv("AUDIT_SESSION_TOKEN"))}
		if path, pathErr := mcp.SessionFilePath(getenv); pathErr == nil {
			options = append(options, mcp.WithSessionFile(path))
		}
		bridge, err := mcp.New(*port, options...)
		if err != nil {
			return err
		}
		debug.SetMemoryLimit(memorySoftLimit)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return bridge.Serve(ctx, os.Stdin, os.Stdout)
	}
	token, err := sessionToken(getenv)
	if err != nil {
		return err
	}
	debug.SetMemoryLimit(memorySoftLimit)

	var repo audit.Repository
	var serverOptions []server.Option
	if *demo {
		// The shipped demo carries a little unfinished work so the incomplete panel has something to show.
		repo = audit.NewDemoWithPatterns(time.Now())
	} else {
		repo = store.New(storeOptions(f)...)
		if savedHistory, historyErr := history.NewDefault(getenv); historyErr == nil {
			serverOptions = append(serverOptions, server.WithHistory(savedHistory))
		}
	}
	hypotheses, ledgerErr := openLedger(*demo, getenv)
	if ledgerErr != nil {
		log.Printf("Hypothesis ledger unavailable: %v", ledgerErr)
		hypotheses = nil
	} else {
		serverOptions = append(serverOptions, server.WithLedger(hypotheses))
	}
	// Imported cases (the importer wave) are live-mode only: demo mode runs on
	// illustrative data and writes nothing to disk, so its case and host routes
	// answer 503 "not available" rather than keep a case store nobody asked for.
	if !*demo {
		if wiring, casesErr := openCases(getenv, hypotheses); casesErr != nil {
			log.Printf("Imported cases unavailable: %v", casesErr)
		} else {
			serverOptions = append(serverOptions, wiring.options...)
			defer wiring.close()
		}
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = repo.Close(ctx)
	}()

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(*port))
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return fmt.Errorf("could not listen on loopback address: %w", err)
	}
	// Listen for Ctrl+C before the session file exists, so an early interrupt
	// still takes the clean path that removes it.
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	localURL := "http://" + listener.Addr().String()
	// The token travels only in the fragment, which the browser never sends to
	// the server; the page reads it and removes it from the address bar.
	launchURL := localURL + "/#t=" + url.QueryEscape(token)
	served := server.New(repo, explorer.Assets(), token, *demo, serverOptions...)
	defer server.LogAnalyzeRepeats(served)
	httpServer := &http.Server{
		Handler:           served,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()

	sessionPath, sessionErr := mcp.SessionFilePath(getenv)
	if sessionErr == nil {
		sessionErr = mcp.WriteSessionFile(sessionPath, listener.Addr().(*net.TCPAddr).Port, token)
	}
	removeSession := func() {}
	if sessionErr != nil {
		log.Printf("Session file not written; --mcp needs --port and AUDIT_SESSION_TOKEN to reach this dashboard")
	} else {
		removeSession = func() { _ = mcp.RemoveSessionFile(sessionPath, token) }
		defer removeSession()
	}

	if !*noBrowser && runtime.GOOS == "windows" {
		_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", launchURL).Start()
	}
	log.Printf("Unravis listening at %s (demo=%t)", localURL, *demo)
	log.Printf("Open Unravis at %s", launchURL)

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
// mode keeps it sealed for the current user (ledger.dat) beside the saved
// connections (same application folder). An older plain ledger.json is migrated
// on first start. Without a sealing backend the ledger is unavailable rather
// than written in plaintext.
func openLedger(demo bool, getenv func(string) string) (ledger.Store, error) {
	if demo {
		return ledger.Open("")
	}
	// Same folder as saved connections and the session file; see history.NewDefault.
	base, err := mcp.SessionDir(getenv)
	if err != nil {
		return nil, err
	}
	protector, err := sealedfile.CurrentUser()
	if err != nil {
		return nil, fmt.Errorf("encrypted storage is unavailable on this platform")
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
