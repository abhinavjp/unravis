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
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/store"
)

const (
	memorySoftLimit  = 256 << 20
	defaultPort      = 18765
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
	flags.IntVar(&f.port, "port", defaultPort, "loopback HTTP port (0 chooses an available port)")
	flags.BoolVar(&f.noBrowser, "no-browser", false, "do not open the local dashboard")
	flags.BoolVar(&f.mcp, "mcp", false, "serve read-only MCP over stdio, using an already running dashboard at --port")
	flags.BoolVar(&f.mcpAllowIdentifiers, "mcp-allow-identifiers", false, "with --mcp: let MCP return user ids, user agents and IP addresses (off by default)")
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
		bridge, err := mcp.New(*port, mcp.WithIdentifiers(f.mcpAllowIdentifiers))
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
		repo = audit.NewDemoWithStaleWork(time.Now())
	} else {
		repo = store.New(storeOptions(f)...)
		if savedHistory, historyErr := history.NewDefault(getenv); historyErr == nil {
			serverOptions = append(serverOptions, server.WithHistory(savedHistory))
		}
	}
	if hypotheses, ledgerErr := openLedger(*demo, getenv); ledgerErr != nil {
		log.Printf("Hypothesis ledger unavailable: %v", ledgerErr)
	} else {
		serverOptions = append(serverOptions, server.WithLedger(hypotheses))
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
	localURL := "http://" + listener.Addr().String()
	httpServer := &http.Server{
		Handler:           server.New(repo, explorer.Assets(), token, *demo, serverOptions...),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()

	if !*noBrowser && runtime.GOOS == "windows" {
		_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", localURL).Start()
	}
	log.Printf("Unravis listening at %s (demo=%t)", localURL, *demo)

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalContext.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			_ = httpServer.Close()
			return fmt.Errorf("server shutdown did not finish cleanly")
		}
		if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// openLedger opens the hypothesis ledger. Demo mode keeps it in memory; live
// mode stores it beside the saved connections (same application folder).
func openLedger(demo bool, getenv func(string) string) (ledger.Store, error) {
	if demo {
		return ledger.Open("")
	}
	base := ""
	if getenv != nil {
		base = getenv("AUDIT_CONFIG_DIR")
	}
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		// Same folder name as saved connections; see history.NewDefault.
		base = filepath.Join(dir, "APIAuditExplorer")
	}
	return ledger.Open(filepath.Join(base, "ledger.json"))
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
