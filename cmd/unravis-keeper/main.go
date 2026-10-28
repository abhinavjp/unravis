// Command unravis-keeper is the Unravis Key Keeper (P14): a small Windows service that holds saved SQL Server passwords, saved MongoDB
// link passwords and Blackbox read keys, and does the work that needs them (the fixed checks and the keyed Blackbox reads) on behalf of
// Unravis, over a local named pipe. It never hands a secret back.
//
// It installs nothing. The installer registers it as a service (P14 installer work); on a developer PC "unravis-keeper console" runs the
// same thing in the foreground with the keeper folder taken from UNRAVIS_KEEPER_DIR, for trying it by hand.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/datadir"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/sqlconn"
)

// ServiceName is the Windows service name the installer registers.
const ServiceName = "UnravisKeeper"

var version = "dev"

const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

// deps are the outside world, replaced in tests.
type deps struct {
	getenv    func(string) string
	protector func() (sealedfile.Protector, error)
	listen    func(name string) (keeper.Listener, error)
	runner    func() keeper.Runner
}

func realDeps() deps {
	return deps{
		getenv:    os.Getenv,
		protector: sealedfile.CurrentUser, // the keeper's own service account: nobody else can open its files
		listen:    func(name string) (keeper.Listener, error) { return keeper.ListenPipe(name, keeper.PipeListenOptions{}) },
		runner:    func() keeper.Runner { return keeper.NewRealRunner() },
	}
}

// keeperFiles are the keeper's own files in its folder.
type keeperFiles struct {
	SQL, Mongo, BB, Log string
}

func filesIn(dir string) keeperFiles {
	return keeperFiles{
		SQL:   filepath.Join(dir, "sqlconnections.dat"),
		Mongo: filepath.Join(dir, "connections.dat"),
		BB:    filepath.Join(dir, "blackboxes.dat"),
		Log:   filepath.Join(dir, "audit.log"),
	}
}

// serve opens the keeper's stores and answers on the pipe until ctx ends.
func serve(ctx context.Context, d deps, dir, pipe string, logf func(string, ...any)) error {
	if dir == "" {
		return errors.New("no keeper folder")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("keeper folder: %w", err)
	}
	p, err := d.protector()
	if err != nil {
		return fmt.Errorf("sealing is not available: %w", err)
	}
	f := filesIn(dir)
	sql, err := sqlconn.New(f.SQL, p)
	if err != nil {
		return err
	}
	mongo, err := history.New(f.Mongo, p)
	if err != nil {
		return err
	}
	bb, err := bbsaved.New(f.BB, p)
	if err != nil {
		return err
	}
	l, err := d.listen(pipe)
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	s := keeper.NewServer(keeper.ServerOptions{
		Log:     keeper.NewAuditLog(f.Log, 0),
		Limiter: keeper.NewRateLimiter(nil, 0),
	})
	(&keeper.Backend{SQL: sql, Mongo: mongo, BB: bb, Runner: d.runner()}).Register(s)
	if logf != nil {
		logf("Unravis Key Keeper %s is answering on %s", version, pipe)
	}
	err = s.Serve(ctx, l)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Unravis Key Keeper "+version)
	fmt.Fprintln(w, "  unravis-keeper console   answer on the keeper pipe in this window (developer PCs; Ctrl+C stops)")
	fmt.Fprintln(w, "  unravis-keeper version")
	fmt.Fprintln(w, "The installer runs it as the Windows service "+ServiceName+". This program never installs anything itself.")
}

func run(args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return exitOK
	case "console":
		fs := flag.NewFlagSet("console", flag.ContinueOnError)
		fs.SetOutput(stderr)
		if err := fs.Parse(args[1:]); err != nil {
			return exitUsage
		}
		dir, pipe := datadir.KeeperDefault(d.getenv)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		logf := func(f string, a ...any) { fmt.Fprintf(stdout, f+"\n", a...) }
		if err := serve(ctx, d, dir, pipe, logf); err != nil {
			fmt.Fprintln(stderr, "The key keeper stopped:", err)
			return exitFail
		}
		return exitOK
	case "help", "-h", "--help", "/?":
		usage(stdout)
		return exitOK
	default:
		usage(stderr)
		return exitUsage
	}
}

func main() {
	d := realDeps()
	if handled, code := serviceEntry(os.Args[1:], d); handled {
		os.Exit(code)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, d))
}
